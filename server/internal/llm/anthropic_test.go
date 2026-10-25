package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"zhiwei/internal/settings"
)

func TestToAnthropicGroupsToolResults(t *testing.T) {
	out := toAnthropic([]Message{
		{Role: RoleUser, Content: "q"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "a", Name: "kb_search", Arguments: `{"query":"x"}`}, {ID: "b", Name: "kb_search", Arguments: `bad json`}}},
		{Role: RoleTool, ToolCallID: "a", Content: "r1"},
		{Role: RoleTool, ToolCallID: "b", Content: "r2"},
	})
	if len(out) != 3 {
		t.Fatalf("want user/assistant/user, got %d messages", len(out))
	}
	if len(out[1].Content) != 2 || string(out[1].Content[1].Input) != `{}` {
		t.Fatalf("invalid tool args must fall back to {}: %+v", out[1])
	}
	if out[2].Role != RoleUser || len(out[2].Content) != 2 || out[2].Content[0].ToolUseID != "a" {
		t.Fatalf("consecutive tool results must merge into one user message: %+v", out[2])
	}
}

func TestTemperatureDroppedWhenRejected(t *testing.T) {
	var sent []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, has := body["temperature"]
		sent = append(sent, has)
		if has {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"` + "`temperature`" + ` is deprecated for this model.","type":"invalid_request_error"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"model":"m","content":[{"type":"thinking","thinking":""},{"type":"text","text":"好"}]}`))
	}))
	defer srv.Close()
	c, err := New(settings.LLM{Protocol: "anthropic", BaseURL: srv.URL, APIKey: "k", Model: "claude-opus-5-5", Temperature: 0.3, MaxTokens: 100, TimeoutSec: 5})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		r, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
		if err != nil || r.Content != "好" {
			t.Fatalf("round %d: %+v %v", i, r, err)
		}
	}
	if len(sent) != 3 || !sent[0] || sent[1] || sent[2] {
		t.Fatalf("应只在第一次带 temperature，被拒后重试并记住：%v", sent)
	}
}
