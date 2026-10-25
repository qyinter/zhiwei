package settings

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// 后台「卡片」预设与缺省模板必须逐字一致，否则新装实例打开设置页时预设不高亮、会被当成自定义模板。
func TestDefaultWelcomeMatchesWebPreset(t *testing.T) {
	src, err := os.ReadFile("../../../web/src/components/welcome-editor.tsx")
	if err != nil {
		t.Skip("找不到前端源码（单独构建 server 时）")
	}
	if !strings.Contains(string(src), "text: `"+DefaultWelcomeQQ+"`") {
		t.Fatal("settings.DefaultWelcomeQQ 与 web WELCOME_PRESETS「卡片」不一致，两处一起改")
	}
}

func TestDefaultsValid(t *testing.T) {
	if err := Validate(Defaults()); err != nil {
		t.Fatal(err)
	}
}

func TestWelcomePlaceholderBlocked(t *testing.T) {
	a := Defaults()
	a.Welcome.Enabled = true
	a.Welcome.MessageQQ = "官网 👉 [点我进入](https://example.com)"
	if err := Validate(a); err == nil {
		t.Fatal("enabled welcome with example.com must be rejected")
	}
	a.Welcome.Enabled = false
	if err := Validate(a); err != nil {
		t.Fatalf("disabled welcome may keep placeholders: %v", err)
	}
}

func TestGroupPolicyOverridesGlobal(t *testing.T) {
	on, off, silence := true, false, 30
	a := Defaults()
	a.Proactive.Enabled = false
	a.Reflect.Enabled = true
	a.Groups.Policies = []GroupPolicy{
		{Chat: "qqbot:G1", Proactive: &on, SilenceSec: &silence, Reflect: &off},
		{Chat: "telegram:-100"},
	}
	if err := Validate(a); err != nil {
		t.Fatal(err)
	}
	if !a.ProactiveOn("qqbot", "G1") || a.ProactiveOn("qqbot", "G2") || a.ProactiveOn("telegram", "-100") {
		t.Fatal("主动回复：G1 单独开启，其他群跟随全局（关）")
	}
	if a.ProactiveSilence("qqbot", "G1") != 30 || a.ProactiveSilence("qqbot", "G2") != a.Proactive.SilenceSec {
		t.Fatal("静默阈值：G1 用自己的，其他群用全局")
	}
	if a.ReflectOn("qqbot", "G1") || !a.ReflectOn("qqbot", "G2") {
		t.Fatal("反思：G1 单独关闭，其他群跟随全局（开）")
	}
	if !a.AnyProactive() {
		t.Fatal("全局关闭但有群单独开启时，扫描器仍要运行")
	}
	a.Groups.Policies[0].Proactive = nil
	if a.AnyProactive() {
		t.Fatal("没有任何群开启时扫描器不运行")
	}
}

func TestGroupPolicyValidation(t *testing.T) {
	neg := -1
	on := true
	for name, ps := range map[string][]GroupPolicy{
		"格式":   {{Chat: "G1"}},
		"渠道":   {{Chat: "discord:1"}},
		"重复":   {{Chat: "qqbot:G1"}, {Chat: "qqbot:G1"}},
		"静默阈值": {{Chat: "qqbot:G1", SilenceSec: &neg}},
	} {
		a := Defaults()
		a.Groups.Policies = ps
		if Validate(a) == nil {
			t.Errorf("%s：应校验失败", name)
		}
	}
	// 单群开启入群审核也要求填核对接口
	a := Defaults()
	a.Groups.Policies = []GroupPolicy{{Chat: "qqbot:G1", JoinReview: &on}}
	if Validate(a) == nil {
		t.Error("单群开启入群审核但没有核对接口时应校验失败")
	}
}

// 以当前设置为底再解 JSON 时，旧策略里有、新 JSON 里没有的字段不能残留。
func TestGroupsUnmarshalReplaces(t *testing.T) {
	on := true
	a := Defaults()
	a.Groups.Policies = []GroupPolicy{{Chat: "qqbot:G1", Proactive: &on}}
	if err := json.Unmarshal([]byte(`{"groups":{"policies":[{"chat":"qqbot:G1"}]}}`), &a); err != nil {
		t.Fatal(err)
	}
	if len(a.Groups.Policies) != 1 || a.Groups.Policies[0].Proactive != nil {
		t.Fatalf("应整体替换：%+v", a.Groups.Policies)
	}
}
