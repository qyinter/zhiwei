package agent

import "testing"

func TestValidateCompacted(t *testing.T) {
	faqs := func(n int) []string { return make([]string, n) }
	if err := validateCompacted(faqs(0), 10); err == nil {
		t.Error("empty result must fail")
	}
	if err := validateCompacted(faqs(16), 10); err == nil {
		t.Error("growth over 1.5x must fail")
	}
	if err := validateCompacted(faqs(3), 10); err == nil {
		t.Error("shrinking below 0.4x must fail")
	}
	if err := validateCompacted(faqs(4), 10); err != nil {
		t.Errorf("4 of 10 should pass: %v", err)
	}
	if err := validateCompacted(faqs(1), 2); err != nil {
		t.Errorf("1 of 2 should pass: %v", err)
	}
}

func TestParseJSONObject(t *testing.T) {
	var out struct {
		Answer bool `json:"answer"`
	}
	if err := parseJSONObject("好的：\n```json\n{\"answer\": true}\n```", &out); err != nil || !out.Answer {
		t.Fatalf("fenced json: %v %v", out, err)
	}
	if err := parseJSONObject("没有 JSON", &out); err == nil {
		t.Fatal("missing json must fail")
	}
}
