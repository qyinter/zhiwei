// Package qqbot 对接 QQ 开放平台官方机器人（API v2）。
//
// 鉴权：AppID + AppSecret 换 access_token（约 2 小时有效），请求头 `Authorization: QQBot {token}`。
// 事件接收两种方式，按设置二选一：
//   - websocket：GET /gateway 拿 wss 地址，Identify 后收 Dispatch。无需公网，开发与内网部署首选。
//   - webhook：开放平台把事件 POST 到 /webhook/qqbot，Ed25519 签名校验。平台对新机器人更推荐这种。
//
// 回复优先走「被动回复」（带 msg_id；群 5 分钟内、每条最多 5 次，单聊 60 分钟、4 次），不占主动消息配额。
// 被动窗口过期或次数用尽（如后台人工回复时距用户上一条已超 5 分钟）时，自动改发主动消息
// （群：Bot 60/qpm（未认证 30）、单群 20/qpm 且每天 1000 条；用户关闭「允许主动发送」时会失败）。
package qqbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	tokenURL   = "https://bots.qq.com/app/getAppAccessToken"
	apiBase    = "https://api.sgroup.qq.com"
	sandboxAPI = "https://sandbox.api.sgroup.qq.com"
)

type apiClient struct {
	appID, secret, base string
	http                *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

func newAPIClient(appID, secret string, sandbox bool) *apiClient {
	base := apiBase
	if sandbox {
		base = sandboxAPI
	}
	return &apiClient{appID: appID, secret: secret, base: base, http: &http.Client{Timeout: 15 * time.Second}}
}

// accessToken 缓存 token，过期前 60 秒刷新（官方：旧 token 在新 token 签发后仍有 60 秒宽限）。
func (c *apiClient) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Until(c.expires) > 60*time.Second {
		return c.token, nil
	}
	body, _ := json.Marshal(map[string]string{"appId": c.appID, "clientSecret": c.secret})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("获取 access_token: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		// 官方返回的是字符串秒数
		ExpiresIn json.RawMessage `json:"expires_in"`
		Code      int             `json:"code"`
		Message   string          `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("解析 access_token 响应: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("获取 access_token 失败: code=%d %s", out.Code, out.Message)
	}
	secs, _ := strconv.Atoi(string(bytes.Trim(out.ExpiresIn, `"`)))
	if secs <= 0 {
		secs = 7200
	}
	c.token, c.expires = out.AccessToken, time.Now().Add(time.Duration(secs)*time.Second)
	return c.token, nil
}

// apiError 平台返回的业务错误，保留 code 供调用方分支（如被动回复超窗改发主动消息）。
type apiError struct {
	Method, Path string
	Status, Code int
	Message      string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("QQ API %s %s: HTTP %d code=%d %s", e.Method, e.Path, e.Status, e.Code, e.Message)
}

// call 调用 OpenAPI。非 2xx 时把平台的 code/message 带进错误，便于在后台直接看到原因。
func (c *apiClient) call(ctx context.Context, method, path string, in, out any) error {
	tok, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		raw, _ := json.Marshal(in)
		body = bytes.NewReader(raw)
	}
	req, _ := http.NewRequestWithContext(ctx, method, c.base+path, body)
	req.Header.Set("Authorization", "QQBot "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		return &apiError{Method: method, Path: path, Status: resp.StatusCode, Code: e.Code, Message: e.Message}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *apiClient) gatewayURL(ctx context.Context) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	if err := c.call(ctx, http.MethodGet, "/gateway", nil, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}
