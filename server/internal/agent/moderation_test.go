package agent

import (
	"testing"

	"zhiwei/internal/channel"
	"zhiwei/internal/settings"
)

func TestRuleHit(t *testing.T) {
	m := settings.Defaults().Moderation
	m.Keywords = []string{"日结兼职", " USDT "}
	m.AllowDomains = []string{"Example.com"}
	cases := []struct {
		name  string
		in    channel.Inbound
		isNew bool
		want  string
	}{
		{"关键词对老成员也生效", channel.Inbound{Text: "招日结兼职"}, false, "命中关键词「日结兼职」"},
		{"关键词不区分大小写并去空格", channel.Inbound{Text: "出 usdt"}, false, "命中关键词「usdt」"},
		{"老成员发链接放行", channel.Inbound{Links: []string{"https://spam.io/x"}}, false, ""},
		{"新成员发链接", channel.Inbound{Links: []string{"https://spam.io/x"}}, true, "新成员发送链接"},
		{"放行域名含子域", channel.Inbound{Links: []string{"https://docs.example.com/a", "www.example.com"}}, true, ""},
		{"新成员 @ 他人", channel.Inbound{MentionsUser: true}, true, "新成员 @ 其他用户"},
		{"新成员转发", channel.Inbound{ForwardFrom: "channel"}, true, "新成员转发消息"},
		{"新成员发图默认不拦", channel.Inbound{Images: make([]channel.Image, 1)}, true, ""},
	}
	for _, c := range cases {
		if got := ruleHit(m, c.in, c.isNew); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	m.NewMemberMedia = true
	if got := ruleHit(m, channel.Inbound{Images: make([]channel.Image, 1)}, true); got != "新成员发送图片" {
		t.Errorf("media rule: got %q", got)
	}
}

func TestSuspicious(t *testing.T) {
	if suspicious(channel.Inbound{Text: "请问怎么充值"}, false) {
		t.Fatal("plain question from old member must not be suspicious")
	}
	for _, text := range []string{"加我微信 abc", "联系 @promo_bot", "电话 13800138000", "t.me/joinchat/x", "日结兼职"} {
		if !suspicious(channel.Inbound{Text: text}, false) {
			t.Errorf("%q should be suspicious", text)
		}
	}
	if !suspicious(channel.Inbound{Text: "大家好"}, true) {
		t.Fatal("new member's first message goes to the model")
	}
}

func TestLinkHost(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.Example.com/a?b=1": "example.com",
		"t.me/spam":                     "t.me",
		"EXAMPLE.org":                   "example.org",
	} {
		if got := linkHost(in); got != want {
			t.Errorf("linkHost(%q) = %q, want %q", in, got, want)
		}
	}
}
