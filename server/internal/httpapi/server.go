// Package httpapi 管理后台 REST API + 渠道 webhook 入口。
//
// 约定：成功直接返回数据 JSON；失败返回 {"error": "可读中文"} + 语义化 HTTP 状态码。
// 前端（Next.js）经 rewrites 把 /api/* 与 /webhook/* 转到这里，浏览器只和 Next 同源通信，
// 所以会话 cookie 不需要跨域设置。
package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"zhiwei/internal/agent"
	"zhiwei/internal/channel"
	"zhiwei/internal/logbuf"
	"zhiwei/internal/media"
	"zhiwei/internal/runtime"
	"zhiwei/internal/settings"
	"zhiwei/internal/store"
)

const pageSize = 20

type Server struct {
	st      *store.Store
	rt      *runtime.Runtime
	log     *slog.Logger
	limiter *loginLimiter
	logs    *logbuf.Ring
	// setupCode 未初始化时启动日志里打印的一次性口令：防止实例刚暴露到网络上就被路人抢先初始化
	setupCode string
}

func New(st *store.Store, rt *runtime.Runtime, log *slog.Logger, logs *logbuf.Ring, setupCode string) *Server {
	return &Server{st: st, rt: rt, log: log.With("mod", "http"), limiter: &loginLimiter{fail: map[string]*failRec{}}, logs: logs, setupCode: setupCode}
}

func (s *Server) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), s.accessLog)
	_ = r.SetTrustedProxies([]string{"127.0.0.1", "::1"})

	r.GET("/api/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	r.Any("/webhook/:channel", func(c *gin.Context) {
		s.rt.Registry.ServeWebhook(c.Param("channel"), c.Writer, c.Request)
	})

	setup := r.Group("/api/setup")
	setup.GET("/status", s.setupStatus)
	setup.POST("/llm-test", s.requireSetupPending, s.setupLLMTest)
	setup.POST("/complete", s.requireSetupPending, s.setupComplete)

	r.POST("/api/auth/login", s.login)
	r.POST("/api/auth/logout", s.logout)

	a := r.Group("/api", s.requireAdmin)
	a.GET("/auth/me", s.me)
	a.POST("/auth/password", s.changePassword)
	a.GET("/overview", s.overview)
	a.GET("/usage", s.usage)
	a.GET("/logs", s.listLogs)
	a.GET("/settings", s.getSettings)
	a.PUT("/settings", s.putSettings)
	a.POST("/settings/llm-test", s.settingsLLMTest)
	a.POST("/settings/embedding-test", s.settingsEmbeddingTest)
	a.POST("/settings/catalog-test", s.settingsCatalogTest)

	a.GET("/conversations", s.listConversations)
	a.GET("/conversations/:id", s.getConversation)
	a.GET("/conversations/:id/messages", s.listMessages)
	a.GET("/message-images/:id", s.messageImage)
	a.POST("/conversations/:id/reply", s.humanReply)
	a.POST("/conversations/:id/resume", s.resume)
	a.POST("/conversations/:id/reset", s.resetConversation)
	a.GET("/handoffs", s.listHandoffs)

	a.GET("/kb", s.listKB)
	a.POST("/kb", s.saveKB)
	a.GET("/kb/:id", s.getKB)
	a.PUT("/kb/:id", s.saveKB)
	a.DELETE("/kb/:id", s.deleteKB)
	a.POST("/kb/search", s.searchKB)
	a.POST("/kb/import", s.importKB)
	a.POST("/kb/folders/rename", s.renameKBFolder)

	a.GET("/skills", s.listSkills)
	a.POST("/skills", s.saveSkill)
	a.GET("/skills/:id", s.getSkill)
	a.PUT("/skills/:id", s.saveSkill)
	a.PATCH("/skills/:id", s.toggleSkill)
	a.DELETE("/skills/:id", s.deleteSkill)
	a.POST("/skills/import", s.importSkills)

	a.POST("/playground", s.playground)

	a.GET("/proactive", s.getProactive)
	a.PATCH("/proactive/:id", s.markProactive)
	a.GET("/join-requests", s.listJoinRequests)
	a.GET("/groups", s.listGroups)
	a.POST("/welcome/test", s.welcomeTest)
	a.GET("/topics", s.listTopics)
	a.GET("/topics/:id/occurrences", s.topicOccurrences)
	a.PATCH("/topics/:id", s.renameTopic)
	a.POST("/topics/:id/merge", s.mergeTopic)
	a.DELETE("/topics/:id", s.deleteTopic)
	a.POST("/topics/run", s.runTopics)
	a.GET("/group-policies", s.listGroupPolicies)
	a.GET("/reflection", s.getReflection)
	a.GET("/reflection/compactions/:id", s.getCompaction)
	a.PATCH("/reflection/:id", s.reflectionAction)
	a.POST("/reflection/run/:job", s.reflectionRun)
	return r
}

func (s *Server) accessLog(c *gin.Context) {
	start := time.Now()
	c.Next()
	if c.Writer.Status() >= 500 {
		s.log.Error("request", "method", c.Request.Method, "path", c.Request.URL.Path, "status", c.Writer.Status(), "ms", time.Since(start).Milliseconds())
	}
}

// ── 响应助手 ──

func fail(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"error": msg})
}

func abort(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

func internal(c *gin.Context, err error) {
	slog.Error("internal error", "path", c.Request.URL.Path, "err", err)
	fail(c, http.StatusInternalServerError, "服务器内部错误")
}

func storeErr(c *gin.Context, err error) {
	if errors.Is(err, store.ErrNotFound) {
		fail(c, http.StatusNotFound, "记录不存在")
		return
	}
	internal(c, err)
}

func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		fail(c, http.StatusBadRequest, "请求格式错误")
		return false
	}
	return true
}

func idParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(c, http.StatusBadRequest, "ID 非法")
		return 0, false
	}
	return id, true
}

func page(c *gin.Context) int {
	p, _ := strconv.Atoi(c.Query("page"))
	return max(p, 1)
}

func paged[T any](c *gin.Context, list []T, total, p int) {
	c.JSON(http.StatusOK, gin.H{"list": list, "total": total, "page": p, "pageSize": pageSize})
}

// ── 初始化向导 ──

func (s *Server) setupStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"completed": s.rt.Settings.Get().Setup.Completed})
}

func (s *Server) requireSetupPending(c *gin.Context) {
	if s.rt.Settings.Get().Setup.Completed {
		abort(c, http.StatusConflict, "系统已完成初始化")
		return
	}
	if c.GetHeader("X-Setup-Code") != s.setupCode {
		abort(c, http.StatusForbidden, "初始化口令错误（见服务端启动日志）")
		return
	}
	c.Next()
}

func (s *Server) setupLLMTest(c *gin.Context) {
	var req settings.LLM
	if !bind(c, &req) {
		return
	}
	s.llmTest(c, req)
}

func (s *Server) llmTest(c *gin.Context, cfg settings.LLM) {
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 30
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 64
	}
	ctx, cancel := context.WithTimeout(c, time.Duration(cfg.TimeoutSec)*time.Second)
	defer cancel()
	reply, latency, err := agent.Probe(ctx, cfg)
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"reply": reply, "latencyMs": latency.Milliseconds()})
}

type setupRequest struct {
	Admin struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"admin"`
	Brand    settings.Brand    `json:"brand"`
	LLM      settings.LLM      `json:"llm"`
	QQBot    settings.QQBot    `json:"qqbot"`
	Telegram settings.Telegram `json:"telegram"`
}

func (s *Server) setupComplete(c *gin.Context) {
	var req setupRequest
	if !bind(c, &req) {
		return
	}
	req.Admin.Username = strings.TrimSpace(req.Admin.Username)
	if err := validCredentials(req.Admin.Username, req.Admin.Password); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Brand.Name) == "" {
		fail(c, http.StatusBadRequest, "请填写品牌名称")
		return
	}
	next := settings.Defaults()
	next.Brand, next.LLM, next.QQBot, next.Telegram = req.Brand, req.LLM, req.QQBot, req.Telegram
	if next.QQBot.Mode == "" {
		next.QQBot.Mode = "websocket"
	}
	if next.Telegram.APIBase == "" {
		next.Telegram.APIBase = "https://api.telegram.org"
	}
	if err := settings.Validate(next); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := hashPassword(req.Admin.Password)
	if err != nil {
		internal(c, err)
		return
	}
	ok, err := s.st.CompleteSetup(c, store.SetupInput{Username: req.Admin.Username, PasswordHash: hash, Settings: next.Sections()})
	if err != nil {
		internal(c, err)
		return
	}
	if !ok {
		fail(c, http.StatusConflict, "系统已完成初始化")
		return
	}
	// 重新从库加载并触发装配（Load 不广播，这里手动 Apply）
	if err := s.rt.Settings.Load(c); err != nil {
		internal(c, err)
		return
	}
	s.rt.Apply(context.Background(), s.rt.Settings.Get())
	s.log.Info("初始化完成", "admin", req.Admin.Username)
	c.JSON(http.StatusOK, gin.H{"completed": true})
}

// ── 总览与设置 ──

func (s *Server) overview(c *gin.Context) {
	loc, ok := tzParam(c)
	if !ok {
		return
	}
	o, err := s.st.Overview(c, dayStart(time.Now().In(loc)))
	if err != nil {
		internal(c, err)
		return
	}
	cfg := s.rt.Settings.Get()
	c.JSON(http.StatusOK, gin.H{
		"stats":    o,
		"channels": s.rt.ChannelStatuses(),
		"llm":      gin.H{"protocol": cfg.LLM.Protocol, "model": cfg.LLM.Model, "error": s.rt.LLMError()},
		"kbChunks": s.rt.Index.Size(),
		"brand":    cfg.Brand,
	})
}

func (s *Server) getSettings(c *gin.Context) {
	c.JSON(http.StatusOK, s.rt.Settings.Get().Masked())
}

func (s *Server) putSettings(c *gin.Context) {
	cur := s.rt.Settings.Get()
	next := cur // 以当前值为底：前端漏传的分区保持不变
	// 切片另起底层数组：encoding/json 解数组会就地覆写元素，否则校验失败也会改脏当前设置
	next.Agent.Chats = slices.Clone(cur.Agent.Chats)
	if !bind(c, &next) {
		return
	}
	next.Setup = cur.Setup
	next = next.Unmask(cur)
	if next.Agent.Chats == nil {
		next.Agent.Chats = []string{}
	}
	if err := s.rt.Settings.Save(c, next); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, s.rt.Settings.Get().Masked())
}

func (s *Server) settingsLLMTest(c *gin.Context) {
	var req settings.LLM
	if !bind(c, &req) {
		return
	}
	cur := s.rt.Settings.Get()
	req.APIKey = settings.All{LLM: req}.Unmask(cur).LLM.APIKey
	s.llmTest(c, req)
}

// ── 会话 ──

func (s *Server) listConversations(c *gin.Context) {
	p := page(c)
	list, total, err := s.st.ListConversations(c, store.ConvFilter{
		Channel: c.Query("channel"), Status: c.Query("status"), Keyword: strings.TrimSpace(c.Query("keyword")),
		Page: p, Size: pageSize,
	})
	if err != nil {
		internal(c, err)
		return
	}
	paged(c, list, total, p)
}

func (s *Server) getConversation(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	conv, err := s.st.Conversation(c, id)
	if err != nil {
		storeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, conv)
}

func (s *Server) listMessages(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	before, _ := strconv.ParseInt(c.Query("before"), 10, 64)
	list, err := s.st.ListMessages(c, id, before, 50)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"list": list})
}

// messageImage 客户发来的图片（已缩放）。图片 ID 不可变，浏览器可长期缓存。
func (s *Server) messageImage(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	im, err := s.st.MessageImage(c, id)
	if err != nil {
		storeErr(c, err)
		return
	}
	c.Header("Cache-Control", "private, max-age=86400, immutable")
	c.Data(http.StatusOK, im.MIME, im.Data)
}

func (s *Server) humanReply(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if !bind(c, &req) {
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		fail(c, http.StatusBadRequest, "回复内容不能为空")
		return
	}
	if err := s.rt.Agent.HumanReply(c, id, currentAdmin(c).Username, strings.TrimSpace(req.Text)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			storeErr(c, err)
			return
		}
		fail(c, http.StatusBadGateway, "发送失败："+err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) resume(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	adminID := currentAdmin(c).ID
	if err := s.rt.Agent.Resume(c, id, &adminID); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) resetConversation(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.st.ResetContext(c, id); err != nil {
		internal(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) listHandoffs(c *gin.Context) {
	p := page(c)
	list, total, err := s.st.ListHandoffs(c, c.Query("status"), p, pageSize)
	if err != nil {
		internal(c, err)
		return
	}
	paged(c, list, total, p)
}

// ── 体验台 ──

// playgroundMaxBytes 体验台请求体上限：4 张 10MB 原图的 base64 还有富余。
const playgroundMaxBytes = 60 << 20

func (s *Server) playground(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, playgroundMaxBytes)
	var req struct {
		Text  string `json:"text"`
		Reset bool   `json:"reset"`
		// Images data URL（data:image/png;base64,…）或纯 base64
		Images []string `json:"images"`
	}
	if !bind(c, &req) {
		return
	}
	if strings.TrimSpace(req.Text) == "" && len(req.Images) == 0 {
		fail(c, http.StatusBadRequest, "消息不能为空")
		return
	}
	if len(req.Images) > channel.MaxImages {
		fail(c, http.StatusBadRequest, fmt.Sprintf("一次最多 %d 张图片", channel.MaxImages))
		return
	}
	images := make([][]byte, 0, len(req.Images))
	for _, d := range req.Images {
		if i := strings.Index(d, ";base64,"); strings.HasPrefix(d, "data:") && i > 0 {
			d = d[i+len(";base64,"):]
		}
		raw, err := base64.StdEncoding.DecodeString(d)
		if err != nil || len(raw) > media.MaxDownload {
			fail(c, http.StatusBadRequest, "图片无法读取或超过 10MB")
			return
		}
		images = append(images, raw)
	}
	res, err := s.rt.Agent.Playground(c, currentAdmin(c).ID, strings.TrimSpace(req.Text), images, req.Reset)
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	c.JSON(http.StatusOK, res)
}
