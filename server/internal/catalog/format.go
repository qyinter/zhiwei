package catalog

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// 渲染给模型看的文本：紧凑的「键: 值」行，由模型改写成回复，不原样粘贴给客户。
// 一切数值原样取自接口，这里不做任何乘除。

// maxListLines 列表类输出的行数上限，防止不带过滤条件时整份目录灌进上下文。
const maxListLines = 60

var unitLabel = map[string]string{
	"per_M_tokens": "每百万 token",
	"per_image":    "每张图",
	"per_k_calls":  "每千次调用",
	"per_second":   "每秒",
}

var chargeLabel = map[string]string{
	"prompt":           "输入",
	"completion":       "输出",
	"cache_read":       "缓存读取",
	"cache_write":      "缓存写入",
	"cache_write_5m":   "缓存写入5分钟",
	"cache_write_1h":   "缓存写入1小时",
	"image_input":      "图片输入",
	"image_output":     "图片输出",
	"image_cache_read": "图片缓存读取",
	"audio_input":      "音频输入",
	"video_input":      "视频输入",
	"video_output":     "视频输出",
	"web_search":       "联网搜索",
}

func label(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	return k
}

// symbol 币种符号：cny 与 usd 在同一份目录里混用，任何报价都必须带上。
func symbol(cur string) string {
	if strings.EqualFold(cur, "cny") {
		return "¥"
	}
	return "$"
}

func currency(cur string) string {
	if strings.EqualFold(cur, "cny") {
		return "人民币 CNY"
	}
	return "美元 USD"
}

// discount null 表示无折扣（倍率 ≥ 1），不能说成 0 折。
func discount(m Model) string {
	if m.DiscountPercent == nil {
		return ""
	}
	return "，已优惠 " + strconv.FormatFloat(*m.DiscountPercent, 'f', -1, 64) + "%"
}

func health(m Model) string {
	h := m.Health
	if h == nil || h.Status == nil {
		return "暂无探测数据（不代表不可用）"
	}
	s := "最近探测失败"
	if *h.Status == "ok" {
		s = "正常"
	}
	if av := h.Availability; av != nil && av.Total > 0 {
		s += fmt.Sprintf("，近期成功率 %.1f%%", float64(av.Success)/float64(av.Total)*100)
	}
	if h.LastLatencyMs > 0 {
		s += fmt.Sprintf("，最近延迟 %dms", h.LastLatencyMs)
	}
	return s
}

func caps(m Model) string {
	c := m.Capabilities
	if c == nil {
		return "未标注"
	}
	var on []string
	for _, x := range []struct {
		ok   bool
		name string
	}{{c.Tools, "工具调用"}, {c.Cache, "提示缓存"}, {c.Stream, "流式"}, {c.Reasoning, "推理"}, {c.ResponseSchema, "结构化输出"}, {c.Image, "图片"}, {c.Video, "视频"}} {
		if x.ok {
			on = append(on, x.name)
		}
	}
	if len(on) == 0 {
		return "未标注"
	}
	return strings.Join(on, "/")
}

func channels(m Model) string {
	if len(m.Channels) == 0 {
		return "未标注"
	}
	parts := make([]string, len(m.Channels))
	for i, c := range m.Channels {
		parts[i] = fmt.Sprintf("%s(×%s)", c.Name, num(c.Multiplier))
	}
	return strings.Join(parts, "、")
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func kilo(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%dK", int(math.Round(float64(n)/1000)))
	}
	return strconv.Itoa(n)
}

// priceLine 一行价格摘要：只挑最常问的输入 / 输出 / 缓存读取；图像、视频类模型没有这三项，退回全部计费项。
func (cat *Catalog) priceLine(m Model) string {
	s := symbol(m.PriceCurrency)
	var parts []string
	for _, k := range []string{"prompt", "completion", "cache_read"} {
		if c, ok := m.Charges[k]; ok {
			parts = append(parts, fmt.Sprintf("%s %s%s", label(chargeLabel, k), s, c.Price))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "，") + "（每百万 token）"
	}
	for _, k := range cat.chargeKeys(m) {
		c := m.Charges[k]
		parts = append(parts, fmt.Sprintf("%s %s%s/%s", label(chargeLabel, k), s, c.Price, label(unitLabel, c.Unit)))
	}
	if len(parts) == 0 {
		return "未标价"
	}
	return strings.Join(parts, "，")
}

func capped(head string, lines []string) string {
	extra := ""
	if len(lines) > maxListLines {
		extra = fmt.Sprintf("\n…另有 %d 个未列出，请加 keyword / family / capability 缩小范围。", len(lines)-maxListLines)
		lines = lines[:maxListLines]
	}
	return head + "\n" + strings.Join(lines, "\n") + extra
}

// Price 计价表。
func (cat *Catalog) Price(f Filter) string {
	list := cat.Filter(f)
	if len(list) == 0 {
		return "没有匹配的模型。可用 action=models 查看全部模型 ID。"
	}
	lines := make([]string, len(list))
	for i, m := range list {
		lines[i] = fmt.Sprintf("%s: %s%s", m.CanonicalName, cat.priceLine(m), discount(m))
	}
	return capped(fmt.Sprintf("匹配 %d 个模型（价格为终价，已含分组优惠，勿再乘任何倍率；¥=人民币，$=美元）:", len(list)), lines)
}

// Models 列可用模型 ID。
func (cat *Catalog) Models(f Filter) string {
	list := cat.Filter(f)
	if len(list) == 0 {
		return "没有匹配的模型。"
	}
	lines := make([]string, len(list))
	for i, m := range list {
		line := m.CanonicalName
		if m.DisplayName != "" && m.DisplayName != m.CanonicalName {
			line += "(" + m.DisplayName + ")"
		}
		fam := m.ProviderFamily
		if fam == "" {
			fam = "未标注"
		}
		line += " 厂商 " + fam
		if m.ContextWindow != nil {
			line += "，上下文 " + kilo(*m.ContextWindow)
			if m.MaxOutputTokens != nil && *m.MaxOutputTokens > 0 {
				line += "，最大输出 " + kilo(*m.MaxOutputTokens)
			}
		}
		lines[i] = line
	}
	return capped(fmt.Sprintf("共 %d 个模型:", len(list)), lines)
}

// Detail 单模型全量。
func (cat *Catalog) Detail(name string) string {
	m := cat.FindModel(strings.TrimSpace(name))
	if m == nil {
		return fmt.Sprintf("未找到模型 %s。用 action=models 查确切模型 ID（须完全一致）。", name)
	}
	title := "模型 " + m.CanonicalName
	if m.DisplayName != "" {
		title += "(" + m.DisplayName + ")"
	}
	fam := m.ProviderFamily
	if fam == "" {
		fam = "未标注"
	}
	ctx := "不适用"
	if m.ContextWindow != nil {
		ctx = strconv.Itoa(*m.ContextWindow)
		if m.MaxOutputTokens != nil && *m.MaxOutputTokens > 0 {
			ctx += "，最大输出 " + strconv.Itoa(*m.MaxOutputTokens)
		}
	}
	lines := []string{title, "厂商: " + fam, "可用分组: " + channels(*m), "能力: " + caps(*m), "上下文: " + ctx, "可用性: " + health(*m)}
	if m.ThroughputTps > 0 {
		lines = append(lines, fmt.Sprintf("吞吐: 约 %.1f token/秒", m.ThroughputTps))
	}
	lines = append(lines, fmt.Sprintf("计价（币种 %s，均为终价，勿再乘倍率%s）:", currency(m.PriceCurrency), discount(*m)))
	s := symbol(m.PriceCurrency)
	for _, k := range cat.chargeKeys(*m) {
		c := m.Charges[k]
		lines = append(lines, fmt.Sprintf("  %s: %s%s %s", label(chargeLabel, k), s, c.Price, label(unitLabel, c.Unit)))
	}
	return strings.Join(lines, "\n")
}

// ChannelList 分组倍率与厂商。
func (cat *Catalog) ChannelList() string {
	if len(cat.Channels) == 0 {
		return "目录未返回分组信息。"
	}
	cs := append([]Channel{}, cat.Channels...)
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].Multiplier < cs[j].Multiplier })
	lines := []string{"分组倍率（仅供理解定价档位；各模型价格已是终价，不要用倍率再算一遍）:"}
	for _, c := range cs {
		lines = append(lines, fmt.Sprintf("%s: ×%s", c.Name, num(c.Multiplier)))
	}
	lines = append(lines, "厂商 family: "+strings.Join(cat.Families(), "、"))
	return strings.Join(lines, "\n")
}

// Args 工具参数。
type Args struct {
	Action     string `json:"action"`
	Keyword    string `json:"keyword"`
	Family     string `json:"family"`
	Channel    string `json:"channel"`
	Capability string `json:"capability"`
	Model      string `json:"model"`
}

// Run 按 action 分派；参数问题返回给模型看的提示（不报错，让模型自己改正）。
func (cat *Catalog) Run(a Args) string {
	f := Filter{Keyword: a.Keyword, Family: a.Family, Channel: a.Channel, Capability: a.Capability}
	switch a.Action {
	case "price":
		return cat.Price(f)
	case "models":
		return cat.Models(f)
	case "detail":
		if strings.TrimSpace(a.Model) == "" {
			return "detail 需要 model 参数；先用 action=models 查确切模型 ID。"
		}
		return cat.Detail(a.Model)
	case "channels":
		return cat.ChannelList()
	}
	return "action 只能是 price / models / detail / channels。"
}

// ToolParameters 工具 JSON Schema。
func ToolParameters() json.RawMessage {
	caps, _ := json.Marshal(CapabilityKeys)
	return json.RawMessage(`{"type":"object","properties":{` +
		`"action":{"type":"string","enum":["price","models","detail","channels"],"description":"price=计价表（输入/输出/缓存读取终价）；models=列可用模型 ID 与上下文长度；detail=单模型全量（全部计费项、能力、可用分组、可用性）；channels=全部分组倍率与厂商"},` +
		`"keyword":{"type":"string","description":"price/models：按模型 ID 或展示名子串过滤，不区分大小写，如 opus、gpt-5"},` +
		`"family":{"type":"string","description":"price/models：按厂商精确过滤，如 anthropic / openai / google / deepseek / qwen / xai / zhipu / minimax"},` +
		`"channel":{"type":"string","description":"price/models：按分组名子串过滤，分组名用 action=channels 查"},` +
		`"capability":{"type":"string","enum":` + string(caps) + `,"description":"price/models：只留具备该能力的模型，如 tools=支持工具调用、cache=支持提示缓存"},` +
		`"model":{"type":"string","description":"detail：精确模型 ID，须与 models 列出的一致"}` +
		`},"required":["action"]}`)
}
