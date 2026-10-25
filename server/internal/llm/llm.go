// Package llm 直连大模型的 tool calling 接口，不依赖任何 agent 框架 / CLI。
//
// 内部统一用 OpenAI 风格的消息模型（user / assistant(+tool_calls) / tool），
// 各协议实现负责双向翻译。工具循环在 agent 包里，这里只做「一次请求 → 一次响应」。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"zhiwei/internal/settings"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Arguments 原始 JSON 字符串（模型产出，可能不合法，由工具自行校验）
	Arguments string `json:"arguments"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"`
	// Images 随 user 消息一起发给模型的图片（只在 user 消息上生效）
	Images []Image `json:"-"`
}

// Image 已缩放、编码好的图片（image/jpeg 或 image/png），各协议按自己的格式内联为 base64。
type Image struct {
	MIME string
	Data []byte
}

// HasImages 请求里是否带图（模型不支持识图时，调用方据此去图重试）。
func (r Request) HasImages() bool {
	for _, m := range r.Messages {
		if len(m.Images) > 0 {
			return true
		}
	}
	return false
}

type Tool struct {
	Name        string
	Description string
	// Parameters JSON Schema（object）
	Parameters json.RawMessage
}

type Request struct {
	// System 恒定的系统提示词。动态内容（检索结果、时间）一律放在 user 消息里——
	// 前缀稳定才吃得到提供方的 prompt cache。
	System   string
	Messages []Message
	Tools    []Tool
}

// Usage InputTokens 是全部输入（含缓存命中与缓存写入），各协议在这里统一口径。
type Usage struct {
	InputTokens      int `json:"inputTokens"`
	OutputTokens     int `json:"outputTokens"`
	CacheReadTokens  int `json:"cacheReadTokens"`
	CacheWriteTokens int `json:"cacheWriteTokens"`
}

func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.CacheReadTokens += o.CacheReadTokens
	u.CacheWriteTokens += o.CacheWriteTokens
}

type Response struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
	Model     string
}

type Client interface {
	Chat(ctx context.Context, req Request) (Response, error)
	Model() string
}

// New 按设置构造客户端。
func New(cfg settings.LLM) (Client, error) {
	if cfg.APIKey == "" || cfg.Model == "" || cfg.BaseURL == "" {
		return nil, fmt.Errorf("LLM 未配置完整（Base URL / API Key / 模型）")
	}
	h := &http.Client{Timeout: time.Duration(cfg.TimeoutSec) * time.Second}
	base := strings.TrimRight(cfg.BaseURL, "/")
	switch cfg.Protocol {
	case "anthropic":
		return &anthropic{cfg: cfg, base: base, http: h}, nil
	case "openai":
		return &openai{cfg: cfg, base: base, http: h}, nil
	}
	return nil, fmt.Errorf("不支持的 LLM 协议 %q", cfg.Protocol)
}

// samplingGate 记住上游是否拒收 temperature。Claude 4.7 及之后的模型（含 Opus 5.5）与部分推理模型
// 只接受缺省采样参数，带上就 400（如「`temperature` is deprecated for this model」）；不维护模型名单，
// 第一次被拒就去掉重试，之后这个客户端不再发送（换模型 / 改设置会新建客户端，重新探测）。
type samplingGate struct{ off atomic.Bool }

func (g *samplingGate) set(body map[string]any, temperature float64) {
	if g.off.Load() {
		delete(body, "temperature")
	} else {
		body["temperature"] = temperature
	}
}

// rejected 是「temperature 被拒」的 400 时关闭发送并返回 true，调用方据此重试一次。
func (g *samplingGate) rejected(err error) bool {
	var ae *APIError
	if g.off.Load() || !errors.As(err, &ae) || ae.Status != http.StatusBadRequest ||
		!strings.Contains(strings.ToLower(ae.Body), "temperature") {
		return false
	}
	g.off.Store(true)
	return true
}

// postJSON 发一次 JSON 请求，非 2xx 返回 *APIError（带状态码与原文片段）。
func postJSON(ctx context.Context, h *http.Client, url string, headers map[string]string, body any) ([]byte, error) {
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := h.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode/100 != 2 {
		return nil, &APIError{Status: resp.StatusCode, Body: truncate(string(out), 500)}
	}
	return out, nil
}

// APIError 上游返回的非 2xx，保留状态码与原文片段供后台排障。
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("LLM HTTP %d: %s", e.Status, e.Body)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
