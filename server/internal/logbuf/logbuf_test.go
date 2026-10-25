package logbuf

import (
	"errors"
	"io"
	"log/slog"
	"testing"
)

func TestRingKeepsLatestAndFilters(t *testing.T) {
	ring := NewRing(3)
	log := slog.New(NewHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}), ring))
	agent := log.With("mod", "agent")
	agent.Info("一")
	agent.Warn("二", "err", errors.New("超时"))
	log.WithGroup("req").Error("三", "path", "/api/x")
	agent.Info("四")

	all, latest := ring.List(Query{})
	if latest != 4 || len(all) != 3 || all[0].Msg != "二" || all[2].Msg != "四" {
		t.Fatalf("应只保留最近 3 条且按时间正序：latest=%d %+v", latest, all)
	}
	if a := all[0].Attrs; len(a) != 2 || a[0] != (Attr{"mod", "agent"}) || a[1] != (Attr{"err", "超时"}) {
		t.Fatalf("预置属性与本条属性都要带上：%+v", a)
	}
	if a := all[1].Attrs; len(a) != 1 || a[0].Key != "req.path" {
		t.Fatalf("分组前缀：%+v", a)
	}

	warn, _ := ring.List(Query{MinLevel: slog.LevelWarn})
	if len(warn) != 2 {
		t.Fatalf("WARN 及以上应有 2 条：%+v", warn)
	}
	kw, _ := ring.List(Query{Keyword: "超时"})
	if len(kw) != 1 || kw[0].Msg != "二" {
		t.Fatalf("关键词应匹配属性值：%+v", kw)
	}
	after, _ := ring.List(Query{After: 3})
	if len(after) != 1 || after[0].Seq != 4 {
		t.Fatalf("增量拉取：%+v", after)
	}
	lim, _ := ring.List(Query{Limit: 1})
	if len(lim) != 1 || lim[0].Msg != "四" {
		t.Fatalf("Limit 取最近的：%+v", lim)
	}
}
