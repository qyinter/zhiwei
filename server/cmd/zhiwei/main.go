// 知微（Zhiwei）服务端入口：连库 → 迁移 → 加载设置 → 装配运行时 → HTTP。
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // 统计按后台浏览器时区切日；精简镜像里可能没有系统时区库

	"zhiwei/db/migrations"
	"zhiwei/internal/agent"
	"zhiwei/internal/channel"
	"zhiwei/internal/config"
	"zhiwei/internal/httpapi"
	"zhiwei/internal/kb"
	"zhiwei/internal/logbuf"
	"zhiwei/internal/runtime"
	"zhiwei/internal/settings"
	"zhiwei/internal/store"
)

func main() {
	// 日志写 stdout，同时留最近 2000 条在内存里给后台「运行日志」页
	logs := logbuf.NewRing(logbuf.Size)
	log := slog.New(logbuf.NewHandler(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}), logs))
	slog.SetDefault(log)
	if err := run(log, logs); err != nil {
		log.Error("启动失败", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, logs *logbuf.Ring) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DSN)
	if err != nil {
		return fmt.Errorf("连接 PostgreSQL: %w", err)
	}
	defer st.Close()
	applied, err := store.Migrate(ctx, st.Pool(), migrations.PG, migrations.PGDir)
	if err != nil {
		return err
	}
	if len(applied) > 0 {
		log.Info("已执行数据库迁移", "files", applied)
	}

	svc := settings.NewService(st)
	if err := svc.Load(ctx); err != nil {
		return err
	}
	index := kb.NewIndex(st)
	if err := index.Rebuild(ctx); err != nil {
		return fmt.Errorf("加载知识库: %w", err)
	}
	reg := channel.NewRegistry()
	orch := agent.NewOrchestrator(st, reg, log)
	if err := orch.ReloadSkills(ctx); err != nil {
		return fmt.Errorf("加载技能: %w", err)
	}
	rt := runtime.New(svc, reg, orch, index, log)
	rt.Apply(ctx, svc.Get())
	go orch.RunHandoffSweeper(ctx)
	go orch.RunProactive(ctx)
	go orch.RunReflector(ctx)
	go orch.RunTopics(ctx)
	go index.RunEmbedder(ctx)

	setupCode := ""
	if !svc.Get().Setup.Completed {
		setupCode = newSetupCode()
		log.Warn("系统尚未初始化：打开管理后台进入初始化向导，并输入以下初始化口令", "setupCode", setupCode)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(st, rt, log, logs, setupCode).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("HTTP 已监听", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("正在关闭…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	reg.StopAll()
	return nil
}

// newSetupCode 8 位数字口令：只在本次进程有效，重启会换新的。
func newSetupCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1e8))
	return fmt.Sprintf("%08d", n.Int64())
}
