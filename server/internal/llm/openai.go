package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"zhiwei/internal/settings"
)

// openai OpenAI Chat Completions 协议（POST {base}/chat/completions），覆盖绝大多数国产模型的兼容端点。
type openai struct {
	cfg  settings.LLM
	base string
	http *http.Client
	temp samplingGate
}

func (c *openai) Model() string { return c.cfg.Model }

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaMessage struct {
	Role string `json:"role"`
	// Content 字符串；带图的 user 消息是 [{type:text}, {type:image_url}] 数组；只有 tool_calls 的 assistant 消息为 null
	Content    any          `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

func (c *openai) Chat(ctx context.Context, req Request) (Response, error) {
	msgs := make([]oaMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, oaMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		om := oaMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		if m.Role == RoleUser && len(m.Images) > 0 {
			om.Content = oaParts(m)
		}
		for _, tc := range m.ToolCalls {
			otc := oaToolCall{ID: tc.ID, Type: "function"}
			otc.Function.Name, otc.Function.Arguments = tc.Name, tc.Arguments
			om.ToolCalls = append(om.ToolCalls, otc)
		}
		// 只有 tool_calls 的 assistant 消息 content 必须是 null，部分兼容端点对空串报错
		if m.Role == RoleAssistant && m.Content == "" && len(om.ToolCalls) > 0 {
			om.Content = nil
		}
		msgs = append(msgs, om)
	}
	body := map[string]any{
		"model":      c.cfg.Model,
		"messages":   msgs,
		"max_tokens": c.cfg.MaxTokens,
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.Parameters,
			}}
		}
		body["tools"] = tools
	}

	headers := map[string]string{"Authorization": "Bearer " + c.cfg.APIKey}
	c.temp.set(body, c.cfg.Temperature)
	respBody, err := postJSON(ctx, c.http, c.base+"/chat/completions", headers, body)
	if c.temp.rejected(err) {
		c.temp.set(body, c.cfg.Temperature)
		respBody, err = postJSON(ctx, c.http, c.base+"/chat/completions", headers, body)
	}
	if err != nil {
		return Response{}, err
	}

	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   string       `json:"content"`
				ToolCalls []oaToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return Response{}, &APIError{Status: http.StatusOK, Body: "响应不是合法 JSON: " + truncate(string(respBody), 300)}
	}
	if len(out.Choices) == 0 {
		return Response{}, &APIError{Status: http.StatusOK, Body: "响应没有 choices: " + truncate(string(respBody), 300)}
	}
	msg := out.Choices[0].Message
	r := Response{
		Content: msg.Content,
		Model:   out.Model,
		Usage: Usage{
			InputTokens:     out.Usage.PromptTokens,
			OutputTokens:    out.Usage.CompletionTokens,
			CacheReadTokens: out.Usage.PromptTokensDetails.CachedTokens,
		},
	}
	for _, tc := range msg.ToolCalls {
		r.ToolCalls = append(r.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return r, nil
}

// oaParts 带图 user 消息的内容数组：文字在前，图片以 data URL 内联。
func oaParts(m Message) []map[string]any {
	parts := make([]map[string]any, 0, len(m.Images)+1)
	if m.Content != "" {
		parts = append(parts, map[string]any{"type": "text", "text": m.Content})
	}
	for _, im := range m.Images {
		url := "data:" + im.MIME + ";base64," + base64.StdEncoding.EncodeToString(im.Data)
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": url}})
	}
	return parts
}
