// Package config 读取进程级环境变量。
//
// 知微刻意只保留「连上数据库之前就必须知道」的配置：DSN 与监听地址。
// 其余一切（管理员、LLM、渠道凭据、回复策略）都存在 PG 的 system_settings 里，
// 由首次启动的初始化向导写入、管理后台修改——改配置不需要重启，也不需要碰服务器上的文件。
package config

import (
	"errors"
	"os"
)

type Config struct {
	// DSN PostgreSQL 连接串（必填），如 postgres://zhiwei:zhiwei@127.0.0.1:5432/zhiwei?sslmode=disable
	DSN string
	// Addr HTTP 监听地址，缺省 127.0.0.1:8080（由 Next.js rewrites 反代，不直接对公网）。
	// QQ 官方机器人 webhook 模式需要公网可达时，在前置 Nginx 上把 /webhook/ 转到这里。
	Addr string
}

func Load() (Config, error) {
	cfg := Config{
		DSN:  os.Getenv("ZHIWEI_DB_DSN"),
		Addr: os.Getenv("ZHIWEI_HTTP_ADDR"),
	}
	if cfg.DSN == "" {
		return cfg, errors.New("ZHIWEI_DB_DSN 未设置")
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8080"
	}
	return cfg, nil
}
