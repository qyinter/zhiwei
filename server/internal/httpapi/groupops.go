package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"zhiwei/internal/agent"
	"zhiwei/internal/settings"
	"zhiwei/internal/store"
)

// ── 主动回复 ──

// chatProgress 某个群的扫描进度。LagSec = 应扫到的位置 − 游标，nil 表示还没扫过（冷启动）。
// Allowed 在群白名单内；Enabled 该功能对这个群开着（按群设置覆盖全局）。
type chatProgress struct {
	store.GroupChatStat
	Allowed  bool       `json:"allowed"`
	Enabled  bool       `json:"enabled"`
	Cursor   *time.Time `json:"cursor"`
	ScanAt   *time.Time `json:"scanAt"`
	LagSec   *int64     `json:"lagSec"`
	Replies  int        `json:"replies"`
	Sediment int        `json:"sediment"`
}

func (s *Server) chatProgress(c *gin.Context, prefix string, delay func(ch, chatID string) time.Duration, on func(ch, chatID string) bool) ([]chatProgress, error) {
	stats, err := s.st.GroupChatStats(c)
	if err != nil {
		return nil, err
	}
	cursors, err := s.st.CursorsByPrefix(c, prefix)
	if err != nil {
		return nil, err
	}
	ag := s.rt.Settings.Get().Agent
	now := time.Now()
	out := make([]chatProgress, 0, len(stats))
	for _, st := range stats {
		p := chatProgress{GroupChatStat: st, Allowed: ag.AllowAllChats || contains(ag.Chats, st.Key()), Enabled: on(st.Channel, st.ChatID)}
		if cur, ok := cursors[prefix+st.Key()]; ok {
			at, upd := cur[0], cur[1]
			lag := int64(max(now.Add(-delay(st.Channel, st.ChatID)).Sub(at), 0) / time.Second)
			p.Cursor, p.ScanAt, p.LagSec = &at, &upd, &lag
		}
		out = append(out, p)
	}
	return out, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Server) getProactive(c *gin.Context) {
	cfg := s.rt.Settings.Get()
	chats, err := s.chatProgress(c, "proactive:", func(ch, id string) time.Duration {
		return time.Duration(cfg.ProactiveSilence(ch, id)) * time.Second
	}, cfg.ProactiveOn)
	if err != nil {
		internal(c, err)
		return
	}
	counts, err := s.st.ProactiveCounts(c)
	if err != nil {
		internal(c, err)
		return
	}
	for i := range chats {
		chats[i].Replies = counts[chats[i].Key()]
	}
	replies, err := s.st.ListProactiveReplies(c, 50)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"config": cfg.Proactive, "anyEnabled": cfg.AnyProactive(), "qqGroupAll": cfg.QQBot.GroupAll, "chats": chats, "replies": replies})
}

func (s *Server) markProactive(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		Quality string `json:"quality"`
	}
	if !bind(c, &req) {
		return
	}
	if req.Quality != "ok" && req.Quality != "bad" && req.Quality != "" {
		fail(c, http.StatusBadRequest, "quality 只能是 ok / bad / 空")
		return
	}
	if err := s.st.SetProactiveQuality(c, id, req.Quality); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ── 入群申请 ──

func (s *Server) listJoinRequests(c *gin.Context) {
	p := page(c)
	list, total, err := s.st.ListJoinRequests(c, p, pageSize)
	if err != nil {
		internal(c, err)
		return
	}
	paged(c, list, total, p)
}

// ── 反思沉淀 ──

func (s *Server) getReflection(c *gin.Context) {
	cfg := s.rt.Settings.Get()
	chats, err := s.chatProgress(c, "reflect:", func(string, string) time.Duration {
		return time.Duration(cfg.Reflect.SettleMin) * time.Minute
	}, cfg.ReflectOn)
	if err != nil {
		internal(c, err)
		return
	}
	byChat, err := s.st.ReflectionCountsByChat(c)
	if err != nil {
		internal(c, err)
		return
	}
	for i := range chats {
		chats[i].Sediment = byChat[chats[i].Key()]
	}
	counts, err := s.st.ReflectionCounts(c)
	if err != nil {
		internal(c, err)
		return
	}
	entries, err := s.st.ListReflections(c, c.Query("status"), 200)
	if err != nil {
		internal(c, err)
		return
	}
	compactions, err := s.st.ListCompactions(c, 10)
	if err != nil {
		internal(c, err)
		return
	}
	resp := gin.H{"config": cfg.Reflect, "anyEnabled": cfg.AnyReflect(), "qqGroupAll": cfg.QQBot.GroupAll, "counts": counts, "chats": chats, "entries": entries, "compactions": compactions}
	for key, name := range map[string]string{"reflect_compact_at": "compactAt", "reflect_promote_at": "promoteAt"} {
		if at, ok, err := s.st.Cursor(c, key); err == nil && ok {
			resp[name] = at
		}
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) getCompaction(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	cp, err := s.st.Compaction(c, id)
	if err != nil {
		storeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, cp)
}

func (s *Server) reflectionAction(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if !bind(c, &req) {
		return
	}
	var err error
	switch req.Action {
	case "approve":
		err = s.rt.Agent.SetReflectionStatus(c, id, store.ReflectApproved)
	case "reject":
		err = s.rt.Agent.SetReflectionStatus(c, id, store.ReflectRejected)
	case "promote":
		var doc store.KBDocument
		doc, err = s.rt.Agent.PromoteReflection(c, id)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"documentId": doc.ID, "title": doc.Title})
			return
		}
	default:
		fail(c, http.StatusBadRequest, "action 只能是 approve / reject / promote")
		return
	}
	switch {
	case errors.Is(err, agent.ErrReflectionState), errors.Is(err, store.ErrNotFound):
		fail(c, http.StatusConflict, agent.ErrReflectionState.Error())
	case err != nil:
		internal(c, err)
	default:
		c.Status(http.StatusNoContent)
	}
}

func (s *Server) reflectionRun(c *gin.Context) {
	var msg string
	var err error
	switch c.Param("job") {
	case "compact":
		msg, err = s.rt.Agent.CompactNow(c)
	case "promote":
		msg, err = s.rt.Agent.PromoteNow(c)
	default:
		fail(c, http.StatusNotFound, "记录不存在")
		return
	}
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": msg})
}

// ── 入群欢迎测试 ──

func (s *Server) listGroups(c *gin.Context) {
	list, err := s.st.KnownGroups(c)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"list": list})
}

func (s *Server) welcomeTest(c *gin.Context) {
	var req struct {
		Channel string           `json:"channel"`
		ChatID  string           `json:"chatId"`
		Welcome settings.Welcome `json:"welcome"`
	}
	if !bind(c, &req) {
		return
	}
	if req.ChatID == "" {
		fail(c, http.StatusBadRequest, "请选择测试群")
		return
	}
	if req.Welcome.FormatQQ != "markdown" && req.Welcome.FormatQQ != "text" {
		req.Welcome.FormatQQ = "text"
	}
	at, err := s.rt.Agent.WelcomeTest(c, req.Welcome, req.Channel, req.ChatID)
	if err != nil {
		fail(c, http.StatusBadGateway, "发送失败："+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"at": at})
}
