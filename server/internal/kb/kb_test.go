package kb

import (
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	got := strings.Join(tokenize("API Key 怎么充值?"), "|")
	if got != "api|key|怎么|么充|充值" {
		t.Fatalf("tokenize = %s", got)
	}
}

func TestSplitHeadingsAndLength(t *testing.T) {
	long := strings.Repeat("这是一句很长的说明。", 80)
	chunks := Split("文档", "# 充值\n\n支持支付宝。\n\n## 退款\n\n"+long)
	if len(chunks) < 3 {
		t.Fatalf("expected >=3 chunks, got %d", len(chunks))
	}
	if chunks[0].Heading != "充值" || chunks[0].Content != "支持支付宝。" {
		t.Fatalf("first chunk = %+v", chunks[0])
	}
	for _, c := range chunks {
		if n := len([]rune(c.Content)); n > chunkMaxRunes {
			t.Fatalf("chunk too long: %d", n)
		}
	}
	if chunks[1].Heading != "退款" {
		t.Fatalf("heading not carried: %+v", chunks[1])
	}
}

func TestNearlySame(t *testing.T) {
	if !NearlySame("API Key 在控制台-令牌页面创建。", "api key 在控制台令牌页面创建") {
		t.Error("punctuation / case differences should be same")
	}
	if !NearlySame("充值后余额不到账怎么办：刷新页面后再看", "充值后余额不到账怎么办") {
		t.Error("containment should be same")
	}
	if NearlySame("怎么充值", "怎么退款") {
		t.Error("different topics must differ")
	}
}
