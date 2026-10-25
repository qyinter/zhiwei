package skill

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	m, err := Parse("\ufeff---\r\nname: pricing\r\ndescription: >\r\n  查询模型价格\r\n  与可用模型\r\n---\r\n\r\n# 正文\n内容\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "pricing" || m.Description != "查询模型价格 与可用模型" || !strings.HasPrefix(m.Body, "# 正文") {
		t.Fatalf("%+v", m)
	}

	// 没写 name 时用目录名兜底
	if m, err := Parse("---\ndescription: 退款\n---\n正文", "退款规则"); err != nil || m.Name != "退款规则" {
		t.Fatalf("%+v %v", m, err)
	}

	for in, want := range map[string]string{
		"# 没有 frontmatter":                        "缺少 frontmatter",
		"---\nname: a\n---\n正文":                   "缺少 description",
		"---\nname: a b\ndescription: x\n---\n正文": "name 只能用",
		"---\nname: a\ndescription: x\n---\n":     "正文为空",
		"---\nname: [a\ndescription: x\n---\n正文":  "不是合法的 YAML",
		"---\nname: a\ndescription: x\n---正文":     "缺少 frontmatter",
	} {
		if _, err := Parse(in, ""); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v，期望包含 %q", in, err, want)
		}
	}
}

func TestCleanFilePath(t *testing.T) {
	for in, want := range map[string]string{
		"references/a.md":   "references/a.md",
		`references\a.md`:   "references/a.md",
		"./a/../b.md":       "b.md",
		"../etc/passwd":     "",
		"/abs.md":           "",
		"a/.hidden/b.md":    "",
		"__MACOSX/a/b.md":   "",
		" references/x.md ": "references/x.md",
	} {
		if got := CleanFilePath(in); got != want {
			t.Errorf("CleanFilePath(%q) = %q，期望 %q", in, got, want)
		}
	}
}
