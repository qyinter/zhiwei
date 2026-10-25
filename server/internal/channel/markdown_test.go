package channel

import "testing"

const sample = "在 Claude Code 里改两个环境变量即可：\n" +
	"1. 在控制台 **令牌** 页面新建 Key\n" +
	"2. 设置环境变量：\n" +
	"```bash\n" +
	"export ANTHROPIC_BASE_URL=https://example.com\n" +
	"export ANTHROPIC_AUTH_TOKEN=<你的Key>\n" +
	"```\n" +
	"详见 [接入教程](https://example.com/docs)，用 `claude` 启动。\n\n\n" +
	"> Key 只显示一次，请立即保存。"

func TestRenderQQ(t *testing.T) {
	want := "在 Claude Code 里改两个环境变量即可：\n\n" +
		"1. 在控制台 **令牌** 页面新建 Key\n" +
		"2. 设置环境变量：\n\n" +
		"> export ANTHROPIC_BASE_URL=https://example.com\n" +
		"> export ANTHROPIC_AUTH_TOKEN=<你的Key>\n\n" +
		"详见 [接入教程](https://example.com/docs)，用 claude 启动。\n\n" +
		"> Key 只显示一次，请立即保存。"
	if got := RenderQQ(sample); got != want {
		t.Fatalf("RenderQQ:\n%s\n--- want ---\n%s", got, want)
	}
	if got := RenderQQ("```\n{\n  \"a\": 1\n}\n```"); got != "> {\n> \u00a0\u00a0\"a\": 1\n> }" {
		t.Fatalf("代码缩进应保留：%q", got)
	}
}

func TestRenderTelegramHTML(t *testing.T) {
	want := "在 Claude Code 里改两个环境变量即可：\n\n" +
		"1. 在控制台 <b>令牌</b> 页面新建 Key\n" +
		"2. 设置环境变量：\n\n" +
		"<pre>export ANTHROPIC_BASE_URL=https://example.com\nexport ANTHROPIC_AUTH_TOKEN=&lt;你的Key&gt;</pre>\n\n" +
		`详见 <a href="https://example.com/docs">接入教程</a>，用 <code>claude</code> 启动。` + "\n\n" +
		"<blockquote>Key 只显示一次，请立即保存。</blockquote>"
	if got := RenderTelegramHTML(sample); got != want {
		t.Fatalf("RenderTelegramHTML:\n%s\n--- want ---\n%s", got, want)
	}
	if got := RenderTelegramHTML("## 价格\n- a < b & **c**\n---\n`**x**`"); got != "<b>价格</b>\n\n• a &lt; b &amp; <b>c</b>\n\n<code>**x**</code>" {
		t.Fatalf("标题 / 转义 / 分割线 / 行内代码不解析加粗：%q", got)
	}
}

func TestRenderPlain(t *testing.T) {
	want := "在 Claude Code 里改两个环境变量即可：\n\n" +
		"1. 在控制台 令牌 页面新建 Key\n" +
		"2. 设置环境变量：\n\n" +
		"export ANTHROPIC_BASE_URL=https://example.com\n" +
		"export ANTHROPIC_AUTH_TOKEN=<你的Key>\n\n" +
		"详见 接入教程（https://example.com/docs），用 claude 启动。\n\n" +
		"Key 只显示一次，请立即保存。"
	if got := RenderPlain(sample); got != want {
		t.Fatalf("RenderPlain:\n%s\n--- want ---\n%s", got, want)
	}
	if got := RenderPlain("收到，查询中…"); got != "收到，查询中…" {
		t.Fatalf("纯文本应原样：%q", got)
	}
}
