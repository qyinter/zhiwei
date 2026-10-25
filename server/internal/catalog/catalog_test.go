package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func load(t *testing.T) *Catalog {
	t.Helper()
	raw, err := os.ReadFile("testdata/models.json")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func mustContain(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("输出缺少 %q：\n%s", w, got)
		}
	}
}

func TestPrice(t *testing.T) {
	cat := load(t)
	out := cat.Price(Filter{Keyword: "OPUS"})
	// 终价原样输出、美元符号、折扣
	mustContain(t, out, "匹配 1 个模型", "勿再乘任何倍率", "claude-opus-5-5: 输入 $4，输出 $20，缓存读取 $0.2（每百万 token），已优惠 70%")

	// 人民币模型带 ¥；按名称排序，与上游数组顺序无关
	out = cat.Price(Filter{})
	mustContain(t, out, "MiniMax-M3: 输入 ¥4.2，输出 ¥16.8", "deepseek-v4-pro: 输入 ¥4.5")
	if strings.Index(out, "MiniMax-M3") > strings.Index(out, "claude-opus-5-5") {
		t.Error("应按 canonicalName 码位序排列")
	}
	// discountPercent 为 null 不输出折扣
	if line := lineOf(out, "claude-fable-5:"); strings.Contains(line, "优惠") {
		t.Errorf("无折扣模型不应显示优惠：%s", line)
	}

	if got := cat.Price(Filter{Keyword: "不存在"}); !strings.Contains(got, "没有匹配的模型") {
		t.Error(got)
	}
	if got := cat.Price(Filter{Family: "deepseek", Capability: "tools"}); !strings.Contains(got, "deepseek-v4-pro") || strings.Contains(got, "MiniMax") {
		t.Error(got)
	}
}

func lineOf(s, prefix string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

func TestPriceFallbackAllCharges(t *testing.T) {
	cat := &Catalog{ChargeOrder: []string{"image_output", "image_input"}, List: []Model{{
		CanonicalName: "img", PriceCurrency: "usd",
		Charges: map[string]Charge{"image_input": {"per_image", "0.01"}, "image_output": {"per_image", "0.04"}, "zzz": {"per_second", "1"}},
	}}}
	// 没有输入/输出项时退回全部计费项，按 chargeOrder 排，不在其中的排最后
	mustContain(t, cat.Price(Filter{}), "img: 图片输出 $0.04/每张图，图片输入 $0.01/每张图，zzz $1/每秒")
}

func TestDetailAndChannels(t *testing.T) {
	cat := load(t)
	out := cat.Detail("Claude-Opus-5-5") // 大小写不敏感兜底
	mustContain(t, out, "模型 claude-opus-5-5", "币种 美元 USD", "已优惠 70%", "缓存写入5分钟: $5 每百万 token", "可用性: 正常")
	if strings.Index(out, "输入:") > strings.Index(out, "缓存写入5分钟") {
		t.Error("计费项应按 chargeOrder 排序")
	}
	mustContain(t, cat.Detail("claude-fable-5"), "暂无探测数据")
	mustContain(t, cat.Detail("nope"), "未找到模型 nope")
	mustContain(t, cat.ChannelList(), "不要用倍率再算一遍", "厂商 family: anthropic、deepseek、minimax、openai")
	mustContain(t, cat.Run(Args{Action: "detail"}), "需要 model 参数")
	mustContain(t, cat.Run(Args{Action: "raw"}), "action 只能是")
	mustContain(t, cat.Models(Filter{Keyword: "minimax"}), "MiniMax-M3(MiniMax M3) 厂商 minimax，上下文 1000K")
}

func TestClientCache(t *testing.T) {
	raw, _ := os.ReadFile("testdata/models.json")
	var hits atomic.Int32
	var down atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if down.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write(raw)
	}))
	defer srv.Close()

	c := NewClient()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, _, err := c.Get(ctx, srv.URL, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("缓存期内应只请求一次，实际 %d 次", hits.Load())
	}

	// 过期 + 上游故障：退回旧缓存
	down.Store(true)
	cat, at, err := c.Get(ctx, srv.URL, 0)
	if err != nil || cat == nil || time.Since(at) > time.Minute {
		t.Fatalf("上游失败应退回旧缓存：%v", err)
	}
	// 换了地址没有可用的旧缓存 → 报错
	if _, _, err := c.Get(ctx, srv.URL+"/other", time.Minute); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("err = %v", err)
	}
	if _, err := Parse([]byte(`{"data":{"list":[]}}`)); err == nil {
		t.Fatal("空列表应报错")
	}
}
