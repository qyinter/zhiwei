// Package logbuf 把进程日志同时写进一个内存环形缓冲，供后台「运行日志」页查看最近的日志。
//
// 只在内存里：重启即清空，不落库、不写文件。完整日志仍以 stdout 为准（由部署方的进程管理收集）。
package logbuf

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Size 缓冲保留的条数。
const Size = 2000

type Attr struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Entry struct {
	// Seq 进程内单调递增的序号，前端据此增量拉取
	Seq   int64     `json:"seq"`
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Msg   string    `json:"msg"`
	Attrs []Attr    `json:"attrs"`
}

type Ring struct {
	mu   sync.Mutex
	buf  []Entry
	head int // 下一个写入位置
	full bool
	seq  int64
}

func NewRing(size int) *Ring {
	return &Ring{buf: make([]Entry, size)}
}

func (r *Ring) add(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	e.Seq = r.seq
	r.buf[r.head] = e
	r.head = (r.head + 1) % len(r.buf)
	if r.head == 0 {
		r.full = true
	}
}

// Query 过滤条件。MinLevel 为空不限级别；Keyword 在消息与属性值里做不区分大小写的子串匹配。
type Query struct {
	After    int64
	MinLevel slog.Level
	Keyword  string
	Limit    int
}

// List 按时间正序返回 Seq > After 且满足条件的最近 Limit 条，以及当前最新序号。
func (r *Ring) List(q Query) ([]Entry, int64) {
	r.mu.Lock()
	all := make([]Entry, 0, len(r.buf))
	if r.full {
		all = append(all, r.buf[r.head:]...)
	}
	all = append(all, r.buf[:r.head]...)
	latest := r.seq
	r.mu.Unlock()

	kw := strings.ToLower(strings.TrimSpace(q.Keyword))
	out := []Entry{}
	for _, e := range all {
		if e.Seq <= q.After || levelOf(e.Level) < q.MinLevel {
			continue
		}
		if kw != "" && !e.matches(kw) {
			continue
		}
		out = append(out, e)
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[len(out)-q.Limit:]
	}
	return out, latest
}

func (e Entry) matches(kw string) bool {
	if strings.Contains(strings.ToLower(e.Msg), kw) {
		return true
	}
	for _, a := range e.Attrs {
		if strings.Contains(strings.ToLower(a.Value), kw) || strings.Contains(strings.ToLower(a.Key), kw) {
			return true
		}
	}
	return false
}

func levelOf(s string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo
	}
	return l
}

// Handler 包一层 slog.Handler：每条日志先进环形缓冲，再交给 inner。
type Handler struct {
	inner slog.Handler
	ring  *Ring
	attrs []Attr // WithAttrs 预置的属性（已带分组前缀）
	group string
}

func NewHandler(inner slog.Handler, ring *Ring) *Handler {
	return &Handler{inner: inner, ring: ring}
}

func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }

func (h *Handler) Handle(ctx context.Context, rec slog.Record) error {
	attrs := append([]Attr{}, h.attrs...)
	rec.Attrs(func(a slog.Attr) bool {
		attrs = appendAttr(attrs, h.group, a)
		return true
	})
	h.ring.add(Entry{Time: rec.Time, Level: rec.Level.String(), Msg: rec.Message, Attrs: attrs})
	return h.inner.Handle(ctx, rec)
}

func (h *Handler) WithAttrs(as []slog.Attr) slog.Handler {
	n := &Handler{inner: h.inner.WithAttrs(as), ring: h.ring, group: h.group, attrs: append([]Attr{}, h.attrs...)}
	for _, a := range as {
		n.attrs = appendAttr(n.attrs, h.group, a)
	}
	return n
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	g := name
	if h.group != "" {
		g = h.group + "." + name
	}
	return &Handler{inner: h.inner.WithGroup(name), ring: h.ring, group: g, attrs: h.attrs}
}

func appendAttr(out []Attr, group string, a slog.Attr) []Attr {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return out
	}
	key := a.Key
	if group != "" {
		key = group + "." + key
	}
	if a.Value.Kind() == slog.KindGroup {
		for _, sub := range a.Value.Group() {
			out = appendAttr(out, key, sub)
		}
		return out
	}
	return append(out, Attr{Key: key, Value: a.Value.String()})
}
