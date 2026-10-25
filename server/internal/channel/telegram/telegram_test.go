package telegram

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"zhiwei/internal/channel"
	"zhiwei/internal/settings"
)

func parseJSON(t *testing.T, a *Adapter, raw string) (string, bool, bool) {
	t.Helper()
	var m tgMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	in, ok := a.parse(&m)
	return in.Text, in.Mentioned, ok
}

func TestParseMentions(t *testing.T) {
	a := New(settings.Telegram{Private: true}, slog.Default())
	a.botID, a.botUsername = 99, "ZhiweiBot"

	// 表情占 2 个 UTF-16 码元：偏移必须按 UTF-16 计算
	text, mentioned, ok := parseJSON(t, a, `{"message_id":1,"from":{"id":1,"first_name":"A"},"chat":{"id":-100,"type":"supergroup"},
		"text":"😀 @zhiweibot 怎么充值","entities":[{"type":"mention","offset":3,"length":10}]}`)
	if !ok || !mentioned || text != "😀  怎么充值" {
		t.Fatalf("got text=%q mentioned=%v ok=%v", text, mentioned, ok)
	}

	_, mentioned, _ = parseJSON(t, a, `{"message_id":2,"from":{"id":1},"chat":{"id":-100,"type":"group"},"text":"大家好"}`)
	if mentioned {
		t.Fatal("plain group message must not count as mention")
	}

	_, mentioned, _ = parseJSON(t, a, `{"message_id":3,"from":{"id":1},"chat":{"id":-100,"type":"group"},"text":"那退款呢",
		"reply_to_message":{"message_id":2,"from":{"id":99},"text":"支持支付宝"}}`)
	if !mentioned {
		t.Fatal("reply to bot should count as mention")
	}

	_, _, ok = parseJSON(t, a, `{"message_id":4,"from":{"id":99},"chat":{"id":5,"type":"private"},"text":"自己发的"}`)
	if ok {
		t.Fatal("bot's own messages must be ignored")
	}
}

func TestMemberJoins(t *testing.T) {
	a := New(settings.Telegram{Private: true}, slog.Default())
	a.botID = 99
	var m tgMessage
	_ = json.Unmarshal([]byte(`{"message_id":5,"chat":{"id":-100,"type":"supergroup","title":"用户群"},
		"new_chat_members":[{"id":7,"first_name":"Li","last_name":"Lei"},{"id":99,"is_bot":true,"first_name":"Bot"}]}`), &m)
	got := a.memberJoins(&m)
	if len(got) != 1 || got[0].SenderID != "7" || got[0].SenderName != "Li Lei" || got[0].ChatID != "-100" || got[0].ChatTitle != "用户群" {
		t.Fatalf("got %+v", got)
	}
	if _, ok := a.parse(&m); ok {
		t.Fatal("service message without text must not be parsed as a message")
	}
}

func TestSendHTML(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if b["parse_mode"] == "HTML" && strings.Contains(b["text"].(string), "坏") {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities: unsupported start tag"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()
	a := New(settings.Telegram{BotToken: "T", APIBase: srv.URL}, slog.Default())

	if err := a.Send(context.Background(), channel.Outbound{ChatID: "42", Text: "点 **新建**", Format: channel.FormatMarkdown, ReplyTo: "7", Quote: true}); err != nil {
		t.Fatal(err)
	}
	if bodies[0]["parse_mode"] != "HTML" || bodies[0]["text"] != "点 <b>新建</b>" || bodies[0]["reply_parameters"] == nil {
		t.Fatalf("html body = %v", bodies[0])
	}
	bodies = nil
	if err := a.Send(context.Background(), channel.Outbound{ChatID: "42", Text: "坏 **x**", Format: channel.FormatMarkdown}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[1]["parse_mode"] != nil || bodies[1]["text"] != "坏 x" {
		t.Fatalf("fallback bodies = %v", bodies)
	}
}

func TestParsePhoto(t *testing.T) {
	a := New(settings.Telegram{Private: true}, slog.Default())
	a.botID, a.botUsername = 99, "ZhiweiBot"
	var m tgMessage
	// 群里带说明的图片：@ 在 caption_entities 里；photo 取不超过上限的最大尺寸；引用的图也带上
	_ = json.Unmarshal([]byte(`{"message_id":5,"from":{"id":1},"chat":{"id":-100,"type":"supergroup"},
		"caption":"@ZhiweiBot 这是啥","caption_entities":[{"type":"mention","offset":0,"length":10}],
		"photo":[{"file_id":"s","file_size":1000},{"file_id":"l","file_size":90000000},{"file_id":"m","file_size":50000}],
		"reply_to_message":{"message_id":4,"from":{"id":2},"chat":{"id":-100,"type":"supergroup"},"document":{"file_id":"d","mime_type":"image/webp","file_size":10}}}`), &m)
	in, ok := a.parse(&m)
	if !ok || !in.Mentioned || in.Text != "这是啥" || len(in.Images) != 2 {
		t.Fatalf("parse = %+v ok=%v", in, ok)
	}
	// 私聊里只发图、不带文字也要处理
	m = tgMessage{}
	_ = json.Unmarshal([]byte(`{"message_id":6,"from":{"id":1},"chat":{"id":1,"type":"private"},"photo":[{"file_id":"s","file_size":10}]}`), &m)
	if in, ok := a.parse(&m); !ok || in.Text != "" || len(in.Images) != 1 {
		t.Fatalf("纯图片私聊 = %+v ok=%v", in, ok)
	}
}
