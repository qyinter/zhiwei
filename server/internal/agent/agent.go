// Package agent 客服大脑：触发判定 → 组装上下文 → LLM 工具循环 → 回复 / 转人工。
//
// 不依赖任何 agent 框架或 CLI：工具循环就是「请求 → 有 tool_calls 就执行并回填 → 再请求」，
// 最多 MaxToolRounds 轮，超出后去掉工具强制模型用已有信息收尾。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"zhiwei/internal/catalog"
	"zhiwei/internal/kb"
	"zhiwei/internal/llm"
	"zhiwei/internal/settings"
	"zhiwei/internal/skill"
)

// Step 一次工具调用的轨迹（Playground 展示与日志用）。
type Step struct {
	Tool   string `json:"tool"`
	Args   string `json:"args"`
	Result string `json:"result"`
}

type RunInput struct {
	History []llm.Message
	Texts   []string
	Quoted  string
	// Proactive 主动回复模式：没人 @ 机器人，只在有确切依据时补位；只给检索工具，不转人工
	Proactive bool
	// Images 随本轮交给模型的图片；ImageCount 客户本轮发来的总张数（关闭识图或下载失败时多于 len(Images)）
	Images     []llm.Image
	ImageCount int
}

type RunResult struct {
	Reply    string
	Handoff  *HandoffRequest
	Steps    []Step
	KBHits   []kb.Hit
	Usage    llm.Usage
	LLMCalls int
	Model    string
	// Images 实际交给模型的图片张数；ImagesDropped 图片没交给模型（本轮被拒后去图重试，或此前已确认模型不收图）；
	// ImagesRejected 本轮才发现模型不收图（只在这一次记警告日志）
	Images         int
	ImagesDropped  bool
	ImagesRejected bool
}

type HandoffRequest struct {
	Reason  string `json:"reason"`
	Summary string `json:"summary"`
}

// Brain 单次推理所需的不可变依赖快照；设置变更时整体替换。
type Brain struct {
	client llm.Client
	index  *kb.Index
	cfg    settings.All
	system string
	skills map[string]skill.Skill
	// catalog 实时模型目录；nil = 未开启
	catalog *catalog.Client
	// tools 全量工具；proactiveTools 主动模式只读工具（不转人工）。顺序固定，保持请求前缀可缓存
	tools, proactiveTools []llm.Tool
	// onSkill 模型读取技能时回调（记使用次数），可为 nil
	onSkill func(id int64)
	// noVision 已确认当前模型不收图（带图被拒、去图重试成功）：之后不再带图白跑一趟。换模型 / 改设置时 Brain 整体重建，自然复位
	noVision atomic.Bool
}

// NewBrain cat 为 nil 或设置未开启模型目录时不提供 model_catalog。
func NewBrain(client llm.Client, index *kb.Index, cfg settings.All, skills []skill.Skill, cat *catalog.Client) *Brain {
	if !cfg.Catalog.Enabled {
		cat = nil
	}
	b := &Brain{
		client: client, index: index, cfg: cfg, catalog: cat,
		system: systemPrompt(cfg.Brand, cfg.Agent.ExtraPrompt, cfg.Agent.ReplyMarkdown, skills, cat != nil),
		skills: map[string]skill.Skill{},
	}
	// 顺序固定：kb_search, transfer_to_human, model_catalog, read_skill；主动模式去掉 transfer_to_human
	b.tools = append(b.tools, baseTools...)
	b.proactiveTools = append(b.proactiveTools, baseTools[0])
	if cat != nil {
		b.tools = append(b.tools, catalogTool)
		b.proactiveTools = append(b.proactiveTools, catalogTool)
	}
	if len(skills) > 0 {
		for _, k := range skills {
			b.skills[k.Name] = k
		}
		b.tools = append(b.tools, readSkillTool)
		b.proactiveTools = append(b.proactiveTools, readSkillTool)
	}
	return b
}

var catalogTool = llm.Tool{
	Name: "model_catalog",
	Description: "查询本平台实时模型目录：模型价格、可用模型 ID、能力（工具调用 / 缓存 / 推理等）、可用性与分组倍率。客户问价格、有哪些模型、某模型支不支持某能力、现在能不能用时调用，以它为准。" +
		"报价规则：返回的价格已是终价、已含分组优惠，绝不可再乘任何倍率；币种逐模型看，¥ 是人民币、$ 是美元，报价必须带币种；" +
		"「已优惠」缺失表示无折扣，不是数据缺失；可用性「暂无探测数据」不代表不可用。",
	Parameters: catalog.ToolParameters(),
}

var readSkillTool = llm.Tool{
	Name:        "read_skill",
	Description: "读取一个技能包的完整说明（system prompt 的【技能】里列出的名称）。说明里提到的附属文件，用 file 参数传相对路径读取。",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"技能名称"},"file":{"type":"string","description":"可选：附属文件的相对路径，不填则读取技能说明正文"}},"required":["name"]}`),
}

var baseTools = []llm.Tool{
	{
		Name:        "kb_search",
		Description: "检索产品知识库。用完整的自然语言问句检索比堆关键词更准；首次结果不相关时换个说法再查一次。",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"检索问句"}},"required":["query"]}`),
	},
	{
		Name:        "transfer_to_human",
		Description: "把当前客户转接给人工客服。调用后本会话暂停自动回复，直到人工处理完毕。仅在需要人工核实/操作、客户明确要求人工、或你无法解决时调用。",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string","description":"转人工原因（给客服看）"},"summary":{"type":"string","description":"客户诉求摘要（一两句话，给客服看）"}},"required":["reason","summary"]}`),
	},
}

// Run 执行一轮客服推理。返回错误时 result 里仍带已累计的用量，供落库统计。
func (b *Brain) Run(ctx context.Context, in RunInput) (RunResult, error) {
	res := RunResult{Model: b.client.Model()}
	ag := b.cfg.Agent

	if ag.KBPrefetch {
		probe := stripImageNote(strings.Join(in.Texts, "\n") + "\n" + in.Quoted)
		// 太短的探针（「在吗」「你好」）检索只会召回噪声
		if len([]rune(probe)) >= 4 {
			res.KBHits = b.index.Search(ctx, truncateRunes(probe, 1000), ag.KBTopK)
		}
	}

	total := max(in.ImageCount, len(in.Images))
	images := in.Images
	if len(images) > 0 && b.noVision.Load() {
		images, res.ImagesDropped = nil, true
	}
	cur := len(in.History) // 本轮 user 消息的下标
	msgs := append(append([]llm.Message{}, in.History...), llm.Message{
		Role:    llm.RoleUser,
		Content: userPrompt(res.KBHits, in.Quoted, in.Texts, in.Proactive, imageNote(len(images), total)),
		Images:  images,
	})
	res.Images = len(images)
	retried := false // 本轮因图片被拒去图重试过：重试成功才确认是图片的问题（400 也可能是上下文过长等别的原因）

	for round := 0; ; round++ {
		req := llm.Request{System: b.system, Messages: msgs, Tools: b.tools}
		if in.Proactive {
			req.Tools = b.proactiveTools
		}
		// 工具轮次用尽：去掉工具再问一次，逼模型基于已有信息作答
		if round >= ag.MaxToolRounds {
			req.Tools = nil
			req.Messages = append(msgs, llm.Message{Role: llm.RoleUser, Content: "（系统：工具调用次数已用尽，请基于已有信息直接回复客户。）"})
		}
		resp, err := b.client.Chat(ctx, req)
		res.LLMCalls++
		res.Usage.Add(resp.Usage)
		if resp.Model != "" {
			res.Model = resp.Model
		}
		// 带图被拒（多半是模型不支持识图）：去掉图片、改成「看不到图片」的说明，本轮重来一次
		if err != nil && len(msgs[cur].Images) > 0 && imageRejected(err) {
			msgs[cur].Images = nil
			msgs[cur].Content = userPrompt(res.KBHits, in.Quoted, in.Texts, in.Proactive, imageNote(0, total))
			res.Images, res.ImagesDropped = 0, true
			retried = true
			round--
			continue
		}
		if err != nil {
			return res, err
		}
		if retried {
			retried, res.ImagesRejected = false, true
			b.noVision.Store(true)
		}
		if len(resp.ToolCalls) == 0 || req.Tools == nil {
			res.Reply = strings.TrimSpace(resp.Content)
			return res, nil
		}
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			out := b.execTool(ctx, tc, &res)
			res.Steps = append(res.Steps, Step{Tool: tc.Name, Args: tc.Arguments, Result: truncateRunes(out, 2000)})
			msgs = append(msgs, llm.Message{Role: llm.RoleTool, ToolCallID: tc.ID, Content: out})
		}
	}
}

func (b *Brain) execTool(ctx context.Context, tc llm.ToolCall, res *RunResult) string {
	switch tc.Name {
	case "kb_search":
		var args struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil || strings.TrimSpace(args.Query) == "" {
			return "参数错误：需要 query 字符串。"
		}
		hits := b.index.Search(ctx, args.Query, b.cfg.Agent.KBTopK)
		if len(hits) == 0 {
			return "知识库无相关内容。"
		}
		return formatHits(hits)
	case "transfer_to_human":
		var args HandoffRequest
		_ = json.Unmarshal([]byte(tc.Arguments), &args)
		if args.Reason == "" {
			args.Reason = "模型判定需要人工"
		}
		res.Handoff = &args
		return "已登记转人工。请用一两句话告知客户：已为其转接人工客服，请耐心等待回复。"
	case "model_catalog":
		if b.catalog != nil {
			return b.queryCatalog(ctx, tc.Arguments)
		}
	case "read_skill":
		if len(b.skills) > 0 {
			return b.readSkill(tc.Arguments)
		}
	}
	names := make([]string, 0, len(b.tools))
	for _, t := range b.tools {
		names = append(names, t.Name)
	}
	return fmt.Sprintf("工具 %s 不存在。可用工具：%s。", tc.Name, strings.Join(names, "、"))
}

func (b *Brain) queryCatalog(ctx context.Context, raw string) string {
	var args catalog.Args
	if err := json.Unmarshal([]byte(raw), &args); err != nil || args.Action == "" {
		return "参数错误：需要 action（price / models / detail / channels）。"
	}
	mc := b.cfg.Catalog
	ttl := time.Duration(mc.CacheMin) * time.Minute
	cat, at, err := b.catalog.Get(ctx, mc.URL, ttl)
	if err != nil {
		return fmt.Sprintf("模型目录暂时查不到（%v）。请如实告诉客户暂时无法查询实时价格，稍后再试；不要凭记忆或知识库里的旧价格报价。", err)
	}
	out := cat.Run(args)
	if age := time.Since(at); age > ttl {
		out += fmt.Sprintf("\n（目录接口暂时不可用，以上为 %d 分钟前的数据）", int(age.Minutes()))
	}
	return out
}

// maxSkillRead 单次 read_skill 返回的字数上限，防止一份超长参考文件撑爆上下文。
const maxSkillRead = 12000

func (b *Brain) readSkill(raw string) string {
	var args struct {
		Name string `json:"name"`
		File string `json:"file"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil || strings.TrimSpace(args.Name) == "" {
		return "参数错误：需要 name 字符串。"
	}
	k, ok := b.skills[strings.TrimSpace(args.Name)]
	if !ok {
		names := make([]string, 0, len(b.skills))
		for n := range b.skills {
			names = append(names, n)
		}
		sort.Strings(names)
		return fmt.Sprintf("技能 %s 不存在。可用技能：%s。", args.Name, strings.Join(names, "、"))
	}
	if b.onSkill != nil {
		b.onSkill(k.ID)
	}
	if f := strings.TrimSpace(args.File); f != "" {
		content, ok := k.Files[skill.CleanFilePath(f)]
		if !ok {
			return fmt.Sprintf("技能 %s 没有附属文件 %s。%s", k.Name, f, fileList(k))
		}
		return truncateRunes(content, maxSkillRead)
	}
	out := truncateRunes(k.Body, maxSkillRead)
	if len(k.Files) > 0 {
		out += "\n\n" + fileList(k)
	}
	return out
}

func fileList(k skill.Skill) string {
	if len(k.Files) == 0 {
		return "该技能没有附属文件。"
	}
	paths := make([]string, 0, len(k.Files))
	for p := range k.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var sb strings.Builder
	sb.WriteString("【附属文件（需要时用 read_skill 的 file 参数读取）】")
	for _, p := range paths {
		fmt.Fprintf(&sb, "\n- %s（%d 字）", p, len([]rune(k.Files[p])))
	}
	return sb.String()
}

// Probe 用一句固定问候测试 LLM 连通性（初始化向导与设置页的「测试连接」）。
func Probe(ctx context.Context, cfg settings.LLM) (reply string, latency time.Duration, err error) {
	client, err := llm.New(cfg)
	if err != nil {
		return "", 0, err
	}
	start := time.Now()
	resp, err := client.Chat(ctx, llm.Request{
		System:   "你是连通性测试助手，只回复「连接正常」四个字。",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "ping"}},
	})
	latency = time.Since(start)
	if err != nil {
		var apiErr *llm.APIError
		if errors.As(err, &apiErr) {
			return "", latency, fmt.Errorf("上游返回 HTTP %d：%s", apiErr.Status, apiErr.Body)
		}
		return "", latency, err
	}
	return resp.Content, latency, nil
}

// BuiltinTool 内置工具（后台技能页只读展示）。
type BuiltinTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Proactive   bool   `json:"proactive"`         // 主动回复模式下是否可用
	OnlySkills  bool   `json:"onlyWithSkills"`    // 只在有启用技能时提供
	Setting     string `json:"setting,omitempty"` // 由该设置分区的 enabled 控制
}

func BuiltinTools() []BuiltinTool {
	out := make([]BuiltinTool, 0, len(baseTools)+1)
	for i, t := range baseTools {
		out = append(out, BuiltinTool{Name: t.Name, Description: t.Description, Proactive: i == 0})
	}
	out = append(out, BuiltinTool{Name: catalogTool.Name, Description: catalogTool.Description, Proactive: true, Setting: "modelCatalog"})
	return append(out, BuiltinTool{Name: readSkillTool.Name, Description: readSkillTool.Description, Proactive: true, OnlySkills: true})
}
