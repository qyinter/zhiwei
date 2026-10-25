package store_test

import (
	"testing"

	"zhiwei/internal/store"
)

func TestCleanKBPath(t *testing.T) {
	for in, want := range map[string]string{
		"":               "",
		"/":              "",
		" 计费 / 退款 /":     "计费/退款",
		"产品\\接入//OpenAI": "产品/接入/OpenAI",
	} {
		if got := store.CleanKBPath(in); got != want {
			t.Errorf("CleanKBPath(%q) = %q, want %q", in, got, want)
		}
	}
}
