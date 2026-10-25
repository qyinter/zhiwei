// Package catalog 实时模型目录查询。
//
// 数据源是站点公开的模型目录 JSON 接口（格式见 testdata/models.json），一次约 100KB；这里取数后按问题过滤、
// 渲染成紧凑的「键: 值」行再交给模型，不把整包 JSON 塞进上下文。
//
// 这里没有任何计价推算：charges[项].price 就是终价。priceMultiplier 与分组 multiplier
// 是两个独立的量（2026-09 实测 deepseek-v4-pro 模型倍率 0.55、所属分组却是 ×0.7），
// 任何「终价 = 倍率 × 基准价」的推算都会报错价。
// discountPercent = (1 − priceMultiplier) × 100，倍率 ≥ 1 时为 null —— null 表示无折扣，不是数据缺失。
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type Charge struct {
	Unit  string `json:"unit"`
	Price string `json:"price"` // 字符串原样保留，避免精度被改写
}

type Channel struct {
	Name       string  `json:"name"`
	Multiplier float64 `json:"multiplier"`
}

type Capabilities struct {
	Cache          bool `json:"cache"`
	Image          bool `json:"image"`
	Tools          bool `json:"tools"`
	Video          bool `json:"video"`
	Stream         bool `json:"stream"`
	Reasoning      bool `json:"reasoning"`
	ResponseSchema bool `json:"responseSchema"`
}

// Has 按能力键名（tools / cache / reasoning 等）取值。
func (c *Capabilities) Has(key string) bool {
	if c == nil {
		return false
	}
	switch key {
	case "cache":
		return c.Cache
	case "image":
		return c.Image
	case "tools":
		return c.Tools
	case "video":
		return c.Video
	case "stream":
		return c.Stream
	case "reasoning":
		return c.Reasoning
	case "responseSchema":
		return c.ResponseSchema
	}
	return false
}

// CapabilityKeys 工具参数 capability 的取值。
var CapabilityKeys = []string{"cache", "image", "tools", "video", "stream", "reasoning", "responseSchema"}

type Health struct {
	// Status 为 null 表示「暂无探测数据」，不是「不可用」
	Status       *string `json:"status"`
	Availability *struct {
		Success int `json:"success"`
		Total   int `json:"total"`
	} `json:"availability"`
	LastLatencyMs int `json:"lastLatencyMs"`
}

type Model struct {
	CanonicalName   string            `json:"canonicalName"`
	DisplayName     string            `json:"displayName"`
	ProviderFamily  string            `json:"providerFamily"`
	ContextWindow   *int              `json:"contextWindow"`
	MaxOutputTokens *int              `json:"maxOutputTokens"`
	PriceCurrency   string            `json:"priceCurrency"` // cny 与 usd 混用，报价必须带币种
	DiscountPercent *float64          `json:"discountPercent"`
	Capabilities    *Capabilities     `json:"capabilities"`
	Channels        []Channel         `json:"channels"`
	Charges         map[string]Charge `json:"charges"`
	Health          *Health           `json:"health"`
	ThroughputTps   float64           `json:"throughputTps"`
}

type Catalog struct {
	List     []Model   `json:"list"`
	Channels []Channel `json:"channels"`
	// ChargeOrder 计费项的官方展示顺序，渲染时据此排序
	ChargeOrder []string `json:"chargeOrder"`
}

// ── 取数与缓存 ──

const (
	fetchTimeout = 10 * time.Second
	maxBody      = 8 << 20
	// staleMax 上游失败时，旧缓存最多再用这么久（附上数据时间）
	staleMax = time.Hour
)

// Client 带进程内缓存的目录客户端，跨设置变更复用（缓存按 URL 区分）。
type Client struct {
	http *http.Client

	mu   sync.Mutex
	url  string
	cat  *Catalog
	at   time.Time
	busy chan struct{} // 正在请求时非 nil，并发调用等同一次请求
}

func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: fetchTimeout}}
}

// Get 返回目录与取数时间；缓存未过期直接用。上游失败而旧缓存不超过 1 小时时退回旧缓存（err 为 nil）。
func (c *Client) Get(ctx context.Context, url string, ttl time.Duration) (*Catalog, time.Time, error) {
	for {
		c.mu.Lock()
		if c.url == url && c.cat != nil && time.Since(c.at) < ttl {
			cat, at := c.cat, c.at
			c.mu.Unlock()
			return cat, at, nil
		}
		if c.busy == nil {
			c.busy = make(chan struct{})
			c.mu.Unlock()
			break
		}
		wait := c.busy
		c.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, time.Time{}, ctx.Err()
		}
	}

	cat, err := c.fetch(ctx, url)
	c.mu.Lock()
	defer c.mu.Unlock()
	close(c.busy)
	c.busy = nil
	if err == nil {
		c.url, c.cat, c.at = url, cat, time.Now()
		return cat, c.at, nil
	}
	if c.url == url && c.cat != nil && time.Since(c.at) < staleMax {
		return c.cat, c.at, nil
	}
	return nil, time.Time{}, err
}

func (c *Client) fetch(ctx context.Context, url string) (*Catalog, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "zhiwei-model-catalog")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return Parse(raw)
}

// Parse 解析接口响应 {code, message, data:{list, channels, chargeOrder}}。
func Parse(raw []byte) (*Catalog, error) {
	var env struct {
		Data *Catalog `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("响应不是合法 JSON")
	}
	if env.Data == nil || len(env.Data.List) == 0 {
		return nil, fmt.Errorf("响应里没有模型列表")
	}
	return env.Data, nil
}

// ── 过滤 ──

type Filter struct {
	Keyword    string // canonicalName / displayName 子串，不区分大小写
	Family     string // providerFamily 精确匹配，不区分大小写
	Channel    string // 分组名子串
	Capability string // 只留具备该能力的模型
}

// FindModel 先精确匹配 canonicalName，再退到不区分大小写。
func (cat *Catalog) FindModel(name string) *Model {
	for i := range cat.List {
		if cat.List[i].CanonicalName == name {
			return &cat.List[i]
		}
	}
	for i := range cat.List {
		if strings.EqualFold(cat.List[i].CanonicalName, name) {
			return &cat.List[i]
		}
	}
	return nil
}

// Filter 过滤后按 canonicalName 码位升序 —— 同一查询两次输出必须一致，不依赖上游数组顺序。
func (cat *Catalog) Filter(f Filter) []Model {
	kw, fam, ch := strings.ToLower(strings.TrimSpace(f.Keyword)), strings.ToLower(strings.TrimSpace(f.Family)), strings.ToLower(strings.TrimSpace(f.Channel))
	var out []Model
	for _, m := range cat.List {
		if kw != "" && !strings.Contains(strings.ToLower(m.CanonicalName+" "+m.DisplayName), kw) {
			continue
		}
		if fam != "" && strings.ToLower(m.ProviderFamily) != fam {
			continue
		}
		if ch != "" {
			hit := false
			for _, c := range m.Channels {
				if strings.Contains(strings.ToLower(c.Name), ch) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		if f.Capability != "" && !m.Capabilities.Has(f.Capability) {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CanonicalName < out[j].CanonicalName })
	return out
}

// Families 目录里出现过的全部 providerFamily，升序去重。
func (cat *Catalog) Families() []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range cat.List {
		if m.ProviderFamily != "" && !seen[m.ProviderFamily] {
			seen[m.ProviderFamily] = true
			out = append(out, m.ProviderFamily)
		}
	}
	sort.Strings(out)
	return out
}

// chargeKeys 计费项排序：先按官方 chargeOrder，不在其中的排最后按字典序。
func (cat *Catalog) chargeKeys(m Model) []string {
	rank := map[string]int{}
	for i, k := range cat.ChargeOrder {
		rank[k] = i
	}
	keys := make([]string, 0, len(m.Charges))
	for k := range m.Charges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, iok := rank[keys[i]]
		rj, jok := rank[keys[j]]
		switch {
		case iok && jok && ri != rj:
			return ri < rj
		case iok != jok:
			return iok
		}
		return keys[i] < keys[j]
	})
	return keys
}
