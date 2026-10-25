// Package settings 定义全部「存在数据库里」的配置分区。
//
// 每个分区 = system_settings 的一行（key → JSON）。读取时先填默认值再覆盖库里的字段，
// 所以新增字段不需要迁移，老库自动拿到默认值。
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"zhiwei/internal/store"
)

const (
	KeySetup    = "setup"
	KeyBrand    = "brand"
	KeyLLM      = "llm"
	KeyAgent    = "agent"
	KeyQQBot    = "channel.qqbot"
	KeyTelegram = "channel.telegram"
	KeyWeCom    = "channel.wecom"

	KeyWelcome    = "welcome"
	KeyJoinReview = "joinReview"
	KeyProactive  = "proactive"
	KeyReflect    = "reflect"
	KeyEmbedding  = "embedding"
	KeyCatalog    = "modelCatalog"
	KeyTopics     = "topics"
	KeyGroups     = "groups"
)

type Setup struct {
	Completed bool `json:"completed"`
}

type Brand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// SupportURL 兜底文案与「转人工不可用」时给用户的联系入口
	SupportURL string `json:"supportUrl"`
}

// LLM 直连大模型。protocol 决定请求格式：
//   - openai：POST {baseUrl}/chat/completions（DeepSeek / Qwen / MiniMax / Kimi / vLLM / Ollama 等 OpenAI 兼容端点）
//   - anthropic：POST {baseUrl}/v1/messages（Anthropic 官方或兼容 relay）
type LLM struct {
	Protocol    string  `json:"protocol"`
	BaseURL     string  `json:"baseUrl"`
	APIKey      string  `json:"apiKey"`
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"maxTokens"`
	TimeoutSec  int     `json:"timeoutSec"`
}

type Agent struct {
	// ExtraPrompt 部署方追加的业务口径，拼在固定 system prompt 末尾（仍属 system，保持恒定利于缓存）
	ExtraPrompt string `json:"extraPrompt"`
	// HistoryLimit 喂给模型的历史消息上限（条）
	HistoryLimit int `json:"historyLimit"`
	// IdleResetMin 会话空闲超过该分钟数，下一条消息开启新上下文
	IdleResetMin int `json:"idleResetMin"`
	// MaxToolRounds 单轮最多工具往返次数，超过即强制模型收尾
	MaxToolRounds int `json:"maxToolRounds"`
	// RunTimeoutSec 单轮总超时（含全部工具往返）
	RunTimeoutSec int `json:"runTimeoutSec"`
	// MaxReplyChars 单条回复字数上限，超出按标点拆条；0 不拆
	MaxReplyChars int `json:"maxReplyChars"`
	// KBPrefetch 调模型前先按用户问题检索知识库并注入候选片段
	KBPrefetch bool `json:"kbPrefetch"`
	KBTopK     int  `json:"kbTopK"`
	// HandoffTimeoutMin 人工接待超过该分钟数无人处理则自动恢复；0 不自动恢复
	HandoffTimeoutMin int `json:"handoffTimeoutMin"`
	// AllowAllChats false 时只响应 Chats 白名单内的群（私聊不受限）
	AllowAllChats bool     `json:"allowAllChats"`
	Chats         []string `json:"chats"`
	// AckEnabled 群里被 @ 后先回一句「收到，查询中…」再推理。走被动回复，不占主动消息额度，
	// 但占同一条消息 5 次被动回复中的 1 次
	AckEnabled bool `json:"ackEnabled"`
	// ReplyMarkdown 机器人回复用精简 Markdown 排版：QQ 群聊 / 单聊发 Markdown 消息，Telegram 转 HTML，
	// 其他场景去掉标记发纯文本。关闭时提示词要求纯文本
	ReplyMarkdown bool `json:"replyMarkdown"`
	// Vision 把客户发来的图片交给模型识别（需要模型支持识图；上游拒收图片时自动去掉图片重试）。
	// 关闭时只告诉模型「客户发了 N 张图片」，由模型请客户用文字描述
	Vision bool `json:"vision"`
}

// Welcome 入群欢迎：新成员加入生效群时发欢迎语。QQ 的入群事件不支持被动回复，欢迎语走主动消息（受频控）。
// 模板占位：{name} 昵称、{brand} 品牌名、{at} @新成员、{ask:问题} 可点击的快捷提问（仅 QQ Markdown）。
type Welcome struct {
	Enabled bool `json:"enabled"`
	// FormatQQ QQ 欢迎语格式：markdown（自定义 Markdown 消息）或 text（纯文本）
	FormatQQ  string `json:"formatQq"`
	MessageQQ string `json:"messageQq"`
	MessageTG string `json:"messageTg"`
}

// welcomePlaceholders 后台「入口导航」预设里的示例内容，开启入群欢迎时不允许原样上线。
var welcomePlaceholders = []string{"example.com", "**10000**"}

func HasWelcomePlaceholder(s string) bool {
	for _, p := range welcomePlaceholders {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// DefaultWelcomeQQ QQ Markdown 欢迎语缺省模板，必须与后台 WELCOME_PRESETS「卡片」逐字一致（否则预设不高亮）。
const DefaultWelcomeQQ = `# 👋 欢迎加入 {brand}
{at} 你好，很高兴在这里见到你～

> 有问题直接 @我，我会先查资料再回答你
> 需要人工处理时，@我 并发送「人工」

***
**🧭 新人三步走**

1️⃣ 先看一眼群公告和群规
2️⃣ 使用问题直接 @我 提问
3️⃣ 账户、订单类问题找人工客服`

// JoinReview 入群审核（仅 QQ，机器人需是群管理员）：取申请人的验证消息 / 入群问题答案，
// 按 IDPattern 提取 ID 后 GET {CheckURL}?id=…，{"status":"ok"} 同意、{"status":"error"} 拒绝，
// 其余情况（超时、非 2xx、其他内容）不处理，留给管理员。
type JoinReview struct {
	Enabled      bool   `json:"enabled"`
	CheckURL     string `json:"checkUrl"`
	RejectReason string `json:"rejectReason"`
	// IDPattern 从答案里提取 ID 的正则（取第一个匹配，前后不能紧贴字母数字）；空 = 整段答案
	IDPattern string `json:"idPattern"`
}

// Proactive 主动回复：群里有人提问且长时间无人应答时谨慎补位。
// 需要渠道能收到未 @ 机器人的群消息（QQ 开启 groupAll、TG 关闭 privacy mode）。
type Proactive struct {
	Enabled bool `json:"enabled"`
	// SilenceSec 提问后静默多久（无人应答）才补位
	SilenceSec int `json:"silenceSec"`
	// ScanSec 扫描间隔
	ScanSec int `json:"scanSec"`
	// MaxPerScan 单次扫描每个群最多补位几人
	MaxPerScan int `json:"maxPerScan"`
}

// Reflect 反思沉淀：从群主 / 管理员的答复里提炼可复用问答进入知识检索，定期整理合并、评审升格为正式文档。
type Reflect struct {
	Enabled bool `json:"enabled"`
	// ScanMin 扫描间隔（分钟）
	ScanMin int `json:"scanMin"`
	// LookbackMin 群消息时间线保留窗口（分钟），超出的群消息被清理
	LookbackMin int `json:"lookbackMin"`
	// SettleMin 静置阈值：消息至少过这么久才参与提炼，等后续追问 / 确认落定
	SettleMin int `json:"settleMin"`
	// WindowMax 单次提炼最多读取的消息数
	WindowMax int `json:"windowMax"`
	// CompactHours 整理（近义合并）周期，0 关闭
	CompactHours      int `json:"compactHours"`
	CompactMinEntries int `json:"compactMinEntries"`
	// PromoteHours 自动升格评审周期，0 关闭
	PromoteHours      int `json:"promoteHours"`
	PromoteMinEntries int `json:"promoteMinEntries"`
	PromoteMaxPerRun  int `json:"promoteMaxPerRun"`
}

// Embedding 向量检索：调用 OpenAI 兼容的 /embeddings 接口（缺省对接 docker-compose 里的
// text-embeddings-inference + BAAI/bge-small-zh-v1.5）。与关键词检索融合排序；
// 关闭或服务不可用时自动退回纯关键词检索。
type Embedding struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
	// Model 模型名同时是向量的版本标记：改了模型，已有向量会全部重算
	Model string `json:"model"`
	// MinScore 向量召回的余弦相似度下限，低于它的块不参与排序
	MinScore float64 `json:"minScore"`
}

// ModelCatalog 实时模型目录工具（model_catalog）：从站点公开的 JSON 接口查模型价格、能力与可用性。
// 接口返回格式见 internal/catalog/testdata/models.json。
type ModelCatalog struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	// CacheMin 目录缓存分钟数：价格不会秒级变化，缓存避免每次提问都拉 100KB
	CacheMin int `json:"cacheMin"`
}

// Topics 问题排行：定期把客户的提问交给模型归类到主题，后台按时间窗看哪些问题问得最多。
// 素材是群时间线（群里全部群友消息）与私聊里客户发来的消息。
type Topics struct {
	Enabled bool `json:"enabled"`
	// ScanMin 扫描间隔（分钟）
	ScanMin int `json:"scanMin"`
	// WindowMax 单次归类最多交给模型的消息数
	WindowMax int `json:"windowMax"`
	// RetentionDays 归类记录保留天数
	RetentionDays int `json:"retentionDays"`
}

// GroupPolicy 单个群的策略。指针字段为 nil 表示跟随全局设置；只对生效群（群白名单内）起作用。
type GroupPolicy struct {
	// Chat channel:chatId，与群白名单写法一致
	Chat string `json:"chat"`
	// Note 备注（后台显示用，群名拿不到时方便辨认）
	Note       string `json:"note"`
	Proactive  *bool  `json:"proactive"`
	SilenceSec *int   `json:"silenceSec"`
	Welcome    *bool  `json:"welcome"`
	JoinReview *bool  `json:"joinReview"`
	Reflect    *bool  `json:"reflect"`
	Topics     *bool  `json:"topics"`
}

// Groups 按群策略。单群开关覆盖全局：全局关闭时也可以只给某几个群打开，反之亦然。
type Groups struct {
	Policies []GroupPolicy `json:"policies"`
}

// UnmarshalJSON 每次解到全新的切片：设置保存是「以当前值为底再覆盖」，复用旧切片会让旧元素里
// 没出现在新 JSON 里的字段残留下来（encoding/json 解数组时复用底层元素）。
func (g *Groups) UnmarshalJSON(b []byte) error {
	var raw struct {
		Policies []GroupPolicy `json:"policies"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw.Policies == nil {
		raw.Policies = []GroupPolicy{}
	}
	g.Policies = raw.Policies
	return nil
}

type QQBot struct {
	Enabled   bool   `json:"enabled"`
	AppID     string `json:"appId"`
	AppSecret string `json:"appSecret"`
	// Mode websocket（无需公网，开发/内网首选）或 webhook（QQ 开放平台回调，需公网 HTTPS）
	Mode    string `json:"mode"`
	Sandbox bool   `json:"sandbox"`
	// 订阅范围
	Group   bool `json:"group"`
	C2C     bool `json:"c2c"`
	Guild   bool `json:"guild"`
	GuildDM bool `json:"guildDm"`
	// GroupAll 订阅群全量消息（GROUP_MESSAGE_CREATE），需在开放平台为机器人开通「接收所有消息」；
	// 关闭时只收 @机器人 的消息（GROUP_AT_MESSAGE_CREATE）。主动回复与反思沉淀依赖它
	GroupAll bool `json:"groupAll"`
	// AtUser 群里回复时 @ 提问人（官方「文本交互」格式 <qqbot-at-user id="…" />）。
	// 2026-10-24 实测主动消息里显示为原文，缺省关闭
	AtUser bool `json:"atUser"`
}

type Telegram struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"botToken"`
	// APIBase 缺省 https://api.telegram.org，可指向自建 Bot API Server 或反代
	APIBase string `json:"apiBase"`
	// Private 是否响应私聊
	Private bool `json:"private"`
}

// WeCom 企业微信（预留）：一期只存配置，不启动适配器。
type WeCom struct {
	Enabled        bool   `json:"enabled"`
	CorpID         string `json:"corpId"`
	AgentID        string `json:"agentId"`
	Secret         string `json:"secret"`
	Token          string `json:"token"`
	EncodingAESKey string `json:"encodingAesKey"`
}

// All 全部分区的快照，运行时装配只认它。
type All struct {
	Setup    Setup    `json:"setup"`
	Brand    Brand    `json:"brand"`
	LLM      LLM      `json:"llm"`
	Agent    Agent    `json:"agent"`
	QQBot    QQBot    `json:"qqbot"`
	Telegram Telegram `json:"telegram"`
	WeCom    WeCom    `json:"wecom"`

	Welcome    Welcome      `json:"welcome"`
	JoinReview JoinReview   `json:"joinReview"`
	Proactive  Proactive    `json:"proactive"`
	Reflect    Reflect      `json:"reflect"`
	Embedding  Embedding    `json:"embedding"`
	Catalog    ModelCatalog `json:"modelCatalog"`
	Topics     Topics       `json:"topics"`
	Groups     Groups       `json:"groups"`
}

func Defaults() All {
	return All{
		Brand: Brand{Name: "知微", Description: "多渠道 AI 客服"},
		LLM: LLM{
			Protocol: "openai", BaseURL: "https://api.openai.com/v1",
			Temperature: 0.3, MaxTokens: 1024, TimeoutSec: 60,
		},
		Agent: Agent{
			HistoryLimit: 20, IdleResetMin: 30, MaxToolRounds: 5, RunTimeoutSec: 120,
			MaxReplyChars: 900, KBPrefetch: true, KBTopK: 5, HandoffTimeoutMin: 30,
			AllowAllChats: true, Chats: []string{}, AckEnabled: true, ReplyMarkdown: true, Vision: true,
		},
		QQBot:    QQBot{Mode: "websocket", Group: true, C2C: true},
		Telegram: Telegram{APIBase: "https://api.telegram.org", Private: true},
		// 以下为欢迎、审核、主动回复、反思、排行等群运营功能的缺省值
		Welcome: Welcome{
			FormatQQ:  "markdown",
			MessageQQ: DefaultWelcomeQQ,
			MessageTG: "Welcome {name}! Feel free to @mention me if you have any questions.",
		},
		JoinReview: JoinReview{
			RejectReason: "未查到该 ID,请在控制台·个人设置复制用户 ID 后重新申请",
			IDPattern:    "[A-Za-z0-9]{10}",
		},
		Proactive: Proactive{SilenceSec: 180, ScanSec: 60, MaxPerScan: 2},
		Reflect: Reflect{
			Enabled: true, ScanMin: 5, LookbackMin: 120, SettleMin: 10, WindowMax: 60,
			CompactHours: 1, CompactMinEntries: 10,
			PromoteHours: 24, PromoteMinEntries: 1, PromoteMaxPerRun: 5,
		},
		Embedding: Embedding{BaseURL: "http://127.0.0.1:8081/v1", Model: "BAAI/bge-small-zh-v1.5", MinScore: 0.5},
		Catalog:   ModelCatalog{CacheMin: 5},
		Topics:    Topics{Enabled: true, ScanMin: 10, WindowMax: 50, RetentionDays: 90},
		Groups:    Groups{Policies: []GroupPolicy{}},
	}
}

// section 把分区 key 映射到 All 里的字段指针，读写共用一张表。
func (a *All) sections() map[string]any {
	return map[string]any{
		KeySetup: &a.Setup, KeyBrand: &a.Brand, KeyLLM: &a.LLM, KeyAgent: &a.Agent,
		KeyQQBot: &a.QQBot, KeyTelegram: &a.Telegram, KeyWeCom: &a.WeCom,
		KeyWelcome: &a.Welcome, KeyJoinReview: &a.JoinReview, KeyProactive: &a.Proactive, KeyReflect: &a.Reflect,
		KeyEmbedding: &a.Embedding, KeyCatalog: &a.Catalog, KeyTopics: &a.Topics, KeyGroups: &a.Groups,
	}
}

// Service 带进程内缓存的设置读写。写入成功后通知订阅者（runtime 据此重新装配）。
type Service struct {
	st *store.Store

	mu       sync.RWMutex
	cur      All
	watchers []func(All)
}

func NewService(st *store.Store) *Service {
	return &Service{st: st, cur: Defaults()}
}

func (s *Service) Load(ctx context.Context) error {
	all := Defaults()
	for key, ptr := range all.sections() {
		if err := s.st.GetSetting(ctx, key, ptr); err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("读取设置 %s: %w", key, err)
		}
	}
	s.mu.Lock()
	s.cur = all
	s.mu.Unlock()
	return nil
}

func (s *Service) Get() All {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

func (s *Service) OnChange(fn func(All)) {
	s.mu.Lock()
	s.watchers = append(s.watchers, fn)
	s.mu.Unlock()
}

// Save 校验并写入 next 中与当前不同的分区，然后广播。
func (s *Service) Save(ctx context.Context, next All) error {
	if err := Validate(next); err != nil {
		return err
	}
	cur := s.Get()
	curSec, nextSec := cur.sections(), next.sections()
	for key, v := range nextSec {
		if key == KeySetup || reflect.DeepEqual(v, curSec[key]) {
			continue
		}
		if err := s.st.PutSetting(ctx, key, v); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cur = next
	watchers := append([]func(All){}, s.watchers...)
	s.mu.Unlock()
	for _, fn := range watchers {
		fn(next)
	}
	return nil
}

// Sections 初始化向导一次性落库用：返回 key → 分区值。
func (a All) Sections() map[string]any {
	out := map[string]any{}
	for k, v := range a.sections() {
		if k != KeySetup {
			out[k] = v
		}
	}
	return out
}

func Validate(a All) error {
	switch a.LLM.Protocol {
	case "openai", "anthropic":
	default:
		return errors.New("LLM 协议只支持 openai / anthropic")
	}
	if a.LLM.BaseURL != "" && !strings.HasPrefix(a.LLM.BaseURL, "http://") && !strings.HasPrefix(a.LLM.BaseURL, "https://") {
		return errors.New("LLM Base URL 必须以 http:// 或 https:// 开头")
	}
	if a.LLM.MaxTokens < 64 || a.LLM.MaxTokens > 32000 {
		return errors.New("LLM 最大输出 token 需在 64~32000 之间")
	}
	if a.LLM.TimeoutSec < 5 || a.LLM.TimeoutSec > 600 {
		return errors.New("LLM 超时需在 5~600 秒之间")
	}
	ag := a.Agent
	if ag.HistoryLimit < 0 || ag.HistoryLimit > 200 {
		return errors.New("历史消息上限需在 0~200 之间")
	}
	if ag.MaxToolRounds < 1 || ag.MaxToolRounds > 20 {
		return errors.New("工具往返上限需在 1~20 之间")
	}
	if ag.RunTimeoutSec < 10 || ag.RunTimeoutSec > 600 {
		return errors.New("单轮超时需在 10~600 秒之间")
	}
	if ag.MaxReplyChars != 0 && ag.MaxReplyChars < 50 {
		return errors.New("单条回复字数上限为 0（不拆）或 ≥50")
	}
	if ag.KBTopK < 1 || ag.KBTopK > 20 {
		return errors.New("预检索条数需在 1~20 之间")
	}
	switch a.QQBot.Mode {
	case "websocket", "webhook":
	default:
		return errors.New("QQ 机器人接入方式只支持 websocket / webhook")
	}
	if a.QQBot.Enabled && (a.QQBot.AppID == "" || a.QQBot.AppSecret == "") {
		return errors.New("启用 QQ 机器人需要填写 AppID 与 AppSecret")
	}
	if a.Telegram.Enabled && a.Telegram.BotToken == "" {
		return errors.New("启用 Telegram 需要填写 Bot Token")
	}
	if a.WeCom.Enabled {
		return errors.New("企业微信渠道尚未开放，请保持关闭")
	}
	if a.Welcome.FormatQQ != "markdown" && a.Welcome.FormatQQ != "text" {
		return errors.New("QQ 欢迎语格式只支持 markdown / text")
	}
	if err := validateGroups(a.Groups); err != nil {
		return err
	}
	if a.AnyWelcome() && HasWelcomePlaceholder(a.Welcome.MessageQQ) {
		return errors.New("QQ 欢迎语里还有预设的示例内容（example.com 网址或 QQ 10000），请换成你们自己的再开启入群欢迎")
	}
	if jr := a.JoinReview; a.AnyJoinReview() && !strings.HasPrefix(jr.CheckURL, "http://") && !strings.HasPrefix(jr.CheckURL, "https://") {
		return errors.New("开启入群审核需要填写以 http:// 或 https:// 开头的核对接口")
	}
	if _, err := regexp.Compile(a.JoinReview.IDPattern); err != nil {
		return errors.New("入群审核的 ID 提取规则不是合法的正则表达式")
	}
	pr := a.Proactive
	if pr.SilenceSec < 0 || pr.SilenceSec > 86400 {
		return errors.New("主动回复静默阈值需在 0~86400 秒之间")
	}
	if pr.ScanSec < 1 || pr.ScanSec > 3600 {
		return errors.New("主动回复扫描间隔需在 1~3600 秒之间")
	}
	if pr.MaxPerScan < 1 || pr.MaxPerScan > 20 {
		return errors.New("主动回复单次最多补位数需在 1~20 之间")
	}
	if em := a.Embedding; em.Enabled {
		if !strings.HasPrefix(em.BaseURL, "http://") && !strings.HasPrefix(em.BaseURL, "https://") {
			return errors.New("开启向量检索需要填写以 http:// 或 https:// 开头的嵌入服务地址")
		}
		if strings.TrimSpace(em.Model) == "" {
			return errors.New("开启向量检索需要填写嵌入模型名")
		}
	}
	if a.Embedding.MinScore < 0 || a.Embedding.MinScore > 1 {
		return errors.New("向量相似度下限需在 0~1 之间")
	}
	if mc := a.Catalog; mc.Enabled && !strings.HasPrefix(mc.URL, "http://") && !strings.HasPrefix(mc.URL, "https://") {
		return errors.New("开启模型目录需要填写以 http:// 或 https:// 开头的目录接口地址")
	}
	if a.Catalog.CacheMin < 1 || a.Catalog.CacheMin > 60 {
		return errors.New("模型目录缓存时间需在 1~60 分钟之间")
	}
	rf, tp := a.Reflect, a.Topics
	for _, c := range []struct {
		v, lo, hi int
		msg       string
	}{
		{rf.ScanMin, 1, 1440, "反思扫描间隔需在 1~1440 分钟之间"},
		{rf.LookbackMin, 10, 10080, "反思回看窗口需在 10~10080 分钟之间"},
		{rf.SettleMin, 0, 1440, "反思静置阈值需在 0~1440 分钟之间"},
		{rf.WindowMax, 5, 500, "反思单窗最大消息数需在 5~500 之间"},
		{rf.CompactHours, 0, 720, "反思整理周期需在 0~720 小时之间"},
		{rf.CompactMinEntries, 2, 1000, "反思整理最少条目需在 2~1000 之间"},
		{rf.PromoteHours, 0, 720, "自动升格周期需在 0~720 小时之间"},
		{rf.PromoteMinEntries, 1, 1000, "升格最少候选需在 1~1000 之间"},
		{rf.PromoteMaxPerRun, 1, 50, "单轮最多升格需在 1~50 之间"},
		{tp.ScanMin, 1, 1440, "问题排行扫描间隔需在 1~1440 分钟之间"},
		{tp.WindowMax, 5, 200, "问题排行单次归类消息数需在 5~200 之间"},
		{tp.RetentionDays, 1, 365, "问题排行保留天数需在 1~365 之间"},
	} {
		if c.v < c.lo || c.v > c.hi {
			return errors.New(c.msg)
		}
	}
	return nil
}

var chatKeyPattern = regexp.MustCompile(`^(qqbot|telegram|wecom):\S+$`)

func validateGroups(g Groups) error {
	seen := map[string]bool{}
	for _, p := range g.Policies {
		if !chatKeyPattern.MatchString(p.Chat) {
			return fmt.Errorf("按群设置的群标识「%s」格式不对，应为 渠道:群ID（如 qqbot:ABCDEF）", p.Chat)
		}
		if seen[p.Chat] {
			return fmt.Errorf("按群设置里「%s」重复了", p.Chat)
		}
		seen[p.Chat] = true
		if utf8.RuneCountInString(p.Note) > 64 {
			return errors.New("按群设置的备注不超过 64 字")
		}
		if p.SilenceSec != nil && (*p.SilenceSec < 0 || *p.SilenceSec > 86400) {
			return errors.New("按群设置的主动回复静默阈值需在 0~86400 秒之间")
		}
	}
	return nil
}

// ── 按群生效 ──
//
// 群开关（非 nil）覆盖全局开关。扫描器每轮用 Any* 判断要不要跑，再按群用 *On 过滤。

// Policy 某个群的策略；没有单独设置时返回只带 Chat 的零值（全部跟随全局）。
func (a All) Policy(channel, chatID string) GroupPolicy {
	key := channel + ":" + chatID
	for _, p := range a.Groups.Policies {
		if p.Chat == key {
			return p
		}
	}
	return GroupPolicy{Chat: key}
}

func pick(override *bool, global bool) bool {
	if override != nil {
		return *override
	}
	return global
}

func (a All) ProactiveOn(channel, chatID string) bool {
	return pick(a.Policy(channel, chatID).Proactive, a.Proactive.Enabled)
}

// ProactiveSilence 该群的主动回复静默阈值（秒）。
func (a All) ProactiveSilence(channel, chatID string) int {
	if v := a.Policy(channel, chatID).SilenceSec; v != nil {
		return *v
	}
	return a.Proactive.SilenceSec
}

func (a All) WelcomeOn(channel, chatID string) bool {
	return pick(a.Policy(channel, chatID).Welcome, a.Welcome.Enabled)
}

func (a All) JoinReviewOn(channel, chatID string) bool {
	return pick(a.Policy(channel, chatID).JoinReview, a.JoinReview.Enabled)
}

func (a All) ReflectOn(channel, chatID string) bool {
	return pick(a.Policy(channel, chatID).Reflect, a.Reflect.Enabled)
}

func (a All) TopicsOn(channel, chatID string) bool {
	return pick(a.Policy(channel, chatID).Topics, a.Topics.Enabled)
}

// any 全局开启，或任一群单独开启。
func (a All) any(global bool, field func(GroupPolicy) *bool) bool {
	if global {
		return true
	}
	for _, p := range a.Groups.Policies {
		if v := field(p); v != nil && *v {
			return true
		}
	}
	return false
}

func (a All) AnyProactive() bool {
	return a.any(a.Proactive.Enabled, func(p GroupPolicy) *bool { return p.Proactive })
}

func (a All) AnyWelcome() bool {
	return a.any(a.Welcome.Enabled, func(p GroupPolicy) *bool { return p.Welcome })
}

func (a All) AnyJoinReview() bool {
	return a.any(a.JoinReview.Enabled, func(p GroupPolicy) *bool { return p.JoinReview })
}

func (a All) AnyReflect() bool {
	return a.any(a.Reflect.Enabled, func(p GroupPolicy) *bool { return p.Reflect })
}

func (a All) AnyTopics() bool {
	return a.any(a.Topics.Enabled, func(p GroupPolicy) *bool { return p.Topics })
}

// ── 密钥掩码 ──
//
// GET 接口把密钥替换成 ••••后四位；PUT 回来的值仍带掩码符号就保留旧值。
// 这样前端整份表单原样回写也不会把真实密钥覆盖成掩码。

const maskMark = "••••"

func mask(v string) string {
	if v == "" {
		return ""
	}
	r := []rune(v)
	if len(r) <= 4 {
		return maskMark
	}
	return maskMark + string(r[len(r)-4:])
}

func keep(next, prev string) string {
	if strings.Contains(next, maskMark) {
		return prev
	}
	return next
}

func (a All) Masked() All {
	a.LLM.APIKey = mask(a.LLM.APIKey)
	a.QQBot.AppSecret = mask(a.QQBot.AppSecret)
	a.Telegram.BotToken = mask(a.Telegram.BotToken)
	a.WeCom.Secret = mask(a.WeCom.Secret)
	a.WeCom.EncodingAESKey = mask(a.WeCom.EncodingAESKey)
	a.Embedding.APIKey = mask(a.Embedding.APIKey)
	return a
}

// Unmask 把 next 中仍为掩码的密钥还原成 prev 的真实值。
func (a All) Unmask(prev All) All {
	a.LLM.APIKey = keep(a.LLM.APIKey, prev.LLM.APIKey)
	a.QQBot.AppSecret = keep(a.QQBot.AppSecret, prev.QQBot.AppSecret)
	a.Telegram.BotToken = keep(a.Telegram.BotToken, prev.Telegram.BotToken)
	a.WeCom.Secret = keep(a.WeCom.Secret, prev.WeCom.Secret)
	a.WeCom.EncodingAESKey = keep(a.WeCom.EncodingAESKey, prev.WeCom.EncodingAESKey)
	a.Embedding.APIKey = keep(a.Embedding.APIKey, prev.Embedding.APIKey)
	return a
}
