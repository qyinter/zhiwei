package channel

import (
	"context"
	"fmt"
	"net/http"
	"sync"
)

// Registry 持有当前生效的适配器集合。设置变更时 runtime 整体 Replace：
// 先停旧的再起新的，期间到达的出站消息会拿到「渠道未启用」错误而不是发到旧连接上。
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

func NewRegistry() *Registry {
	return &Registry{adapters: map[string]Adapter{}}
}

func (r *Registry) Replace(ctx context.Context, next []Adapter, h Handler) []error {
	r.mu.Lock()
	old := r.adapters
	r.adapters = map[string]Adapter{}
	r.mu.Unlock()
	for _, a := range old {
		a.Stop()
	}

	var errs []error
	started := map[string]Adapter{}
	for _, a := range next {
		if err := a.Start(ctx, h); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.Name(), err))
		}
		// 启动失败也登记：Status 里能看到错误，适配器自己会退避重连
		started[a.Name()] = a
	}
	r.mu.Lock()
	r.adapters = started
	r.mu.Unlock()
	return errs
}

func (r *Registry) StopAll() {
	r.Replace(context.Background(), nil, nil)
}

func (r *Registry) Get(name string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[name]
	return a, ok
}

func (r *Registry) Send(ctx context.Context, channel string, out Outbound) error {
	a, ok := r.Get(channel)
	if !ok {
		return fmt.Errorf("渠道 %s 未启用", channel)
	}
	return a.Send(ctx, out)
}

// ReviewJoin 审批入群申请；渠道未启用或不支持返回错误。
func (r *Registry) ReviewJoin(ctx context.Context, channel, chatID, userID string, req JoinRequest, approve bool, reason string) error {
	a, ok := r.Get(channel)
	if !ok {
		return fmt.Errorf("渠道 %s 未启用", channel)
	}
	jr, ok := a.(JoinReviewer)
	if !ok {
		return fmt.Errorf("渠道 %s 不支持入群审批", channel)
	}
	return jr.ReviewJoin(ctx, chatID, userID, req, approve, reason)
}

// GroupName 查群名；渠道未启用或不支持返回空串。
func (r *Registry) GroupName(ctx context.Context, channel, chatID string) string {
	a, ok := r.Get(channel)
	if !ok {
		return ""
	}
	gn, ok := a.(GroupNamer)
	if !ok {
		return ""
	}
	return gn.GroupName(ctx, chatID)
}

func (r *Registry) Statuses() map[string]Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]Status{}
	for name, a := range r.adapters {
		out[name] = a.Status()
	}
	return out
}

// ServeWebhook 把 /webhook/{name} 转给对应适配器；未启用或不支持回调返回 404。
func (r *Registry) ServeWebhook(name string, w http.ResponseWriter, req *http.Request) {
	a, ok := r.Get(name)
	wa, isHook := a.(WebhookAdapter)
	if !ok || !isHook {
		http.NotFound(w, req)
		return
	}
	wa.ServeWebhook(w, req)
}
