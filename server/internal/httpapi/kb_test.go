package httpapi

import (
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestDecodeText(t *testing.T) {
	gbk, _ := simplifiedchinese.GBK.NewEncoder().String("充值方法：控制台")
	for in, want := range map[string]string{
		"\xef\xbb\xbf# 标题": "# 标题",
		"plain":            "plain",
		gbk:                "充值方法：控制台",
	} {
		if got, ok := decodeText([]byte(in)); !ok || got != want {
			t.Errorf("decodeText(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	if _, ok := decodeText([]byte{0xff, 0xfe, 0xfd}); ok {
		t.Error("乱码应识别失败")
	}
}
