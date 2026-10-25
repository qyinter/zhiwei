// Package runtime 按当前设置装配渠道与客服大脑，设置变更时整体重装。
package runtime

import (
	"context"
	"log/slog"
	"sync"

	"zhiwei/internal/agent"
	"zhiwei/internal/channel"
	"zhiwei/internal/channel/qqbot"
	"zhiwei/internal/channel/telegram"
	"zhiwei/internal/embed"
	"zhiwei/internal/kb"
	"zhiwei/internal/settings"
)

type Runtime struct {
	Settings *settings.Service
	Registry *channel.Registry
	Agent    *agent.Orchestrator
	Index    *kb.Index
	log      *slog.Logger

	// 串行化重装：连续两次保存设置不会交错启停适配器
	mu       sync.Mutex
	llmError string
}

func New(s *settings.Service, reg *channel.Registry, ag *agent.Orchestrator, ix *kb.Index, log *slog.Logger) *Runtime {
	rt := &Runtime{Settings: s, Registry: reg, Agent: ag, Index: ix, log: log.With("mod", "runtime")}
	s.OnChange(func(cfg settings.All) { rt.Apply(context.Background(), cfg) })
	return rt
}

// Apply 未完成初始化时只配置大脑（体验台可用），不启动任何渠道。
func (rt *Runtime) Apply(ctx context.Context, cfg settings.All) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	var emb kb.Embedder
	if cfg.Embedding.Enabled {
		emb = embed.New(cfg.Embedding)
	}
	rt.Index.Configure(ctx, emb, cfg.Embedding.MinScore)

	rt.llmError = ""
	if err := rt.Agent.Configure(cfg, rt.Index); err != nil {
		rt.llmError = err.Error()
		rt.log.Warn("LLM 未就绪", "err", err)
	}
	var adapters []channel.Adapter
	if cfg.Setup.Completed {
		if cfg.QQBot.Enabled {
			adapters = append(adapters, qqbot.New(cfg.QQBot, qqbot.Options{MemberEvents: cfg.AnyWelcome() || cfg.AnyJoinReview()}, rt.log))
		}
		if cfg.Telegram.Enabled {
			adapters = append(adapters, telegram.New(cfg.Telegram, rt.log))
		}
		// 企业微信：预留（internal/channel/wecom）
	}
	for _, err := range rt.Registry.Replace(ctx, adapters, rt.Agent.Handle) {
		rt.log.Warn("渠道启动失败（后台会继续重试）", "err", err)
	}
	rt.log.Info("运行时已装配", "channels", len(adapters))
}

func (rt *Runtime) LLMError() string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.llmError
}

// ChannelStatuses 已启用渠道的实时状态 + 未启用渠道的占位，后台总览按固定顺序展示。
func (rt *Runtime) ChannelStatuses() []channel.Status {
	cfg := rt.Settings.Get()
	live := rt.Registry.Statuses()
	out := []channel.Status{}
	for _, c := range []struct {
		name    string
		enabled bool
		detail  string
	}{
		{channel.QQBot, cfg.QQBot.Enabled, "QQ 开放平台官方机器人"},
		{channel.Telegram, cfg.Telegram.Enabled, "Telegram Bot"},
		{channel.WeCom, false, "企业微信（预留，暂未开放）"},
	} {
		if s, ok := live[c.name]; ok {
			out = append(out, s)
			continue
		}
		out = append(out, channel.Status{Channel: c.name, Enabled: c.enabled, Detail: c.detail})
	}
	return out
}
