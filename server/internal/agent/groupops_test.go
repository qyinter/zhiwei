package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractMemberID(t *testing.T) {
	cases := []struct{ answer, pattern, want string }{
		{"我的ID是 Ab12Cd34Ef 谢谢", "[A-Za-z0-9]{10}", "Ab12Cd34Ef"},
		{"Ab12Cd34Ef", "[A-Za-z0-9]{10}", "Ab12Cd34Ef"},
		// 11 位连续字母数字不算 10 位 ID：前后不能紧贴字母数字
		{"Ab12Cd34Ef9", "[A-Za-z0-9]{10}", "Ab12Cd34Ef9"},
		{"  随便写写  ", "[A-Za-z0-9]{10}", "随便写写"},
		{" 整段答案 ", "", "整段答案"},
		{"", "[A-Za-z0-9]{10}", ""},
	}
	for _, c := range cases {
		if got := extractMemberID(c.answer, c.pattern); got != c.want {
			t.Errorf("extractMemberID(%q) = %q, want %q", c.answer, got, c.want)
		}
	}
}

func TestCheckMember(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("id") {
		case "good":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "bad":
			_, _ = w.Write([]byte(`{"status":"error"}`))
		case "weird":
			_, _ = w.Write([]byte(`{"status":"maybe"}`))
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	if st, err := checkMember(ctx, srv.URL+"/check?x=1", "good"); err != nil || st != "ok" {
		t.Fatalf("good: %q %v", st, err)
	}
	if st, err := checkMember(ctx, srv.URL, "bad"); err != nil || st != "error" {
		t.Fatalf("bad: %q %v", st, err)
	}
	for _, id := range []string{"weird", "down"} {
		if st, err := checkMember(ctx, srv.URL, id); err == nil || st != "" {
			t.Fatalf("%s must be undecided, got %q %v", id, st, err)
		}
	}
}

func TestRenderWelcome(t *testing.T) {
	tpl := "{at} 欢迎 **{name}** 加入 {brand}\n- {ask:怎么充值}\n- {ask:API Key 在哪}"
	md := renderWelcome(tpl, welcomeVars{brand: "知微", name: "小明", userID: "U1", qq: true, markdown: true})
	want := `<qqbot-at-user id="U1" /> 欢迎 **小明** 加入 知微` + "\n" +
		`- <qqbot-cmd-input text="%E6%80%8E%E4%B9%88%E5%85%85%E5%80%BC" show="%E6%80%8E%E4%B9%88%E5%85%85%E5%80%BC" reference="false" />` + "\n" +
		`- <qqbot-cmd-input text="API%20Key%20%E5%9C%A8%E5%93%AA" show="API%20Key%20%E5%9C%A8%E5%93%AA" reference="false" />`
	if md != want {
		t.Fatalf("markdown:\n%s\nwant:\n%s", md, want)
	}
	// 纯文本 / TG：@ 退化为昵称，快捷提问退化为「问题」
	plain := renderWelcome(tpl, welcomeVars{name: "Li", userID: "7", markdown: false})
	if plain != "Li 欢迎 **Li** 加入 本群\n- 「怎么充值」\n- 「API Key 在哪」" {
		t.Fatalf("plain = %q", plain)
	}
}
