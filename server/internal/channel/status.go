package channel

import "sync"

// StatusBox 适配器共用的并发安全状态容器。
type StatusBox struct {
	mu sync.Mutex
	s  Status
}

func NewStatusBox(channel, detail string) *StatusBox {
	return &StatusBox{s: Status{Channel: channel, Enabled: true, Detail: detail}}
}

func (b *StatusBox) Get() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.s
}

func (b *StatusBox) Connected(detail string) {
	b.mu.Lock()
	b.s.Connected, b.s.LastError = true, ""
	if detail != "" {
		b.s.Detail = detail
	}
	b.mu.Unlock()
}

func (b *StatusBox) Failed(err error) {
	b.mu.Lock()
	b.s.Connected, b.s.LastError = false, err.Error()
	b.mu.Unlock()
}
