package qqbot

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// 群名：群事件只带 group_openid，群名要调「获取群基本信息」GET /v2/groups/{group_openid}/info（30 QPM）。
// 该接口是白名单接口，没有权限时返回 11253：记一次日志后本进程不再调用。
//
// 收到群消息时同步查一次（缓存命中直接用），把群名带进 Inbound.ChatTitle 写到会话上；
// 后台「群管理」对还没有群名的群也走这里补齐。

const (
	codeNoAPIPermission = 11253
	groupNameTTL        = 12 * time.Hour
	// groupNameRetry 查询失败（网络、限流等）后隔这么久再试，期间按「没有群名」处理
	groupNameRetry = 10 * time.Minute
	// groupNameWait 收消息时同步查询的最长等待：查不到也不能耽误消息处理
	groupNameWait = 3 * time.Second
)

type groupNames struct {
	mu     sync.Mutex
	m      map[string]groupNameItem
	denied atomic.Bool
}

type groupNameItem struct {
	name string
	at   time.Time
	ok   bool
}

func (c *groupNames) get(id string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, hit := c.m[id]
	if !hit {
		return "", false
	}
	ttl := groupNameRetry
	if it.ok {
		ttl = groupNameTTL
	}
	if time.Since(it.at) > ttl {
		return "", false
	}
	return it.name, true
}

func (c *groupNames) put(id, name string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]groupNameItem{}
	}
	c.m[id] = groupNameItem{name: name, at: time.Now(), ok: ok}
}

// GroupName 群名（带缓存）；接口无权限或查询失败时返回空串，失败原因记日志。
func (a *Adapter) GroupName(ctx context.Context, groupOpenID string) string {
	if groupOpenID == "" || a.names.denied.Load() {
		return ""
	}
	if name, ok := a.names.get(groupOpenID); ok {
		return name
	}
	var out struct {
		GroupName string `json:"group_name"`
	}
	err := a.api.call(ctx, http.MethodGet, "/v2/groups/"+groupOpenID+"/info", nil, &out)
	var ae *apiError
	if errors.As(err, &ae) && ae.Code == codeNoAPIPermission {
		if !a.names.denied.Swap(true) {
			a.log.Info("「获取群基本信息」接口无权限（白名单接口），群名取不到，可在后台群管理里填备注", "err", err)
		}
		return ""
	}
	if err != nil {
		a.log.Warn("查询群名失败", "group", groupOpenID, "err", err)
		a.names.put(groupOpenID, "", false)
		return ""
	}
	a.names.put(groupOpenID, out.GroupName, true)
	return out.GroupName
}

func (a *Adapter) groupTitle(groupOpenID string) string {
	ctx, cancel := context.WithTimeout(context.Background(), groupNameWait)
	defer cancel()
	return a.GroupName(ctx, groupOpenID)
}
