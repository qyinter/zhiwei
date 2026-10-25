package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"zhiwei/internal/settings"
)

// anthropic Messages API（POST {base}/v1/messages）。
// system 块打 cache_control：system 恒定，命中缓存后每轮只付增量输入的钱。
type anthropic struct {
	cfg  settings.LLM
	base string
	http *http.Client
	temp samplingGate
}

func (c *anthropic) Model() string { return c.cfg.Model }

type anBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	Source    *anSource       `json:"source,omitempty"`
}

type anSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anMessage struct {
	Role    string    `json:"role"`
	Content []anBlock `json:"content"`
}

// toAnthropic 内部消息 → Messages API：tool 结果变成 user 消息里的 tool_result 块，
// 连续的同角色消息合并（API 要求 user / assistant 严格交替）。
func toAnthropic(msgs []Message) []anMessage {
	var out []anMessage
	push := func(role string, b anBlock) {
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, b)
			return
		}
		out = append(out, anMessage{Role: role, Content: []anBlock{b}})
	}
	for _, m := range msgs {
		switch m.Role {
		case RoleTool:
			push(RoleUser, anBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content})
		case RoleAssistant:
			if m.Content != "" {
				push(RoleAssistant, anBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(tc.Arguments)
				if !json.Valid(input) {
					input = json.RawMessage(`{}`)
				}
				push(RoleAssistant, anBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
			}
		default:
			if m.Content != "" || len(m.Images) == 0 {
				push(RoleUser, anBlock{Type: "text", Text: m.Content})
			}
			for _, im := range m.Images {
				push(RoleUser, anBlock{Type: "image", Source: &anSource{
					Type: "base64", MediaType: im.MIME, Data: base64.StdEncoding.EncodeToString(im.Data),
				}})
			}
		}
	}
	return out
}

func (c *anthropic) Chat(ctx context.Context, req Request) (Response, error) {
	body := map[string]any{
		"model":      c.cfg.Model,
		"max_tokens": c.cfg.MaxTokens,
		"messages":   toAnthropic(req.Messages),
	}
	if req.System != "" {
		body["system"] = []map[string]any{{
			"type": "text", "text": req.System, "cache_control": map[string]string{"type": "ephemeral"},
		}}
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Parameters}
		}
		body["tools"] = tools
	}

	headers := map[string]string{
		"x-api-key": c.cfg.APIKey,
		// 兼容只认 Bearer 的 relay
		"Authorization":     "Bearer " + c.cfg.APIKey,
		"anthropic-version": "2023-06-01",
	}
	c.temp.set(body, c.cfg.Temperature)
	respBody, err := postJSON(ctx, c.http, c.base+"/v1/messages", headers, body)
	if c.temp.rejected(err) {
		c.temp.set(body, c.cfg.Temperature)
		respBody, err = postJSON(ctx, c.http, c.base+"/v1/messages", headers, body)
	}
	if err != nil {
		return Response{}, err
	}

	var out struct {
		Model   string    `json:"model"`
		Content []anBlock `json:"content"`
		Usage   struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return Response{}, &APIError{Status: http.StatusOK, Body: "响应不是合法 JSON: " + truncate(string(respBody), 300)}
	}
	r := Response{
		Model: out.Model,
		// input_tokens 只是缓存断点之后的部分，加上缓存命中与写入才是全部输入
		Usage: Usage{
			InputTokens:      out.Usage.InputTokens + out.Usage.CacheReadInputTokens + out.Usage.CacheCreationInputTokens,
			OutputTokens:     out.Usage.OutputTokens,
			CacheReadTokens:  out.Usage.CacheReadInputTokens,
			CacheWriteTokens: out.Usage.CacheCreationInputTokens,
		},
	}
	for _, b := range out.Content {
		switch b.Type {
		case "text":
			r.Content += b.Text
		case "tool_use":
			r.ToolCalls = append(r.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Arguments: string(b.Input)})
		}
	}
	return r, nil
}
