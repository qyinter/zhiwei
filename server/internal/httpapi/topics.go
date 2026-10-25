package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"zhiwei/internal/agent"
	"zhiwei/internal/settings"
	"zhiwei/internal/store"
)

// ── 问题排行 ──

// windowParam ?days=1..90&tz=…：时间窗起点 = days-1 天前的零点（按 tz）。
func windowParam(c *gin.Context, def int) (time.Time, int, bool) {
	loc, ok := tzParam(c)
	if !ok {
		return time.Time{}, 0, false
	}
	days, _ := strconv.Atoi(c.DefaultQuery("days", strconv.Itoa(def)))
	if days < 1 || days > 90 {
		fail(c, http.StatusBadRequest, "统计天数需在 1~90 之间")
		return time.Time{}, 0, false
	}
	return dayStart(time.Now().In(loc)).AddDate(0, 0, -(days - 1)), days, true
}

func (s *Server) listTopics(c *gin.Context) {
	from, days, ok := windowParam(c, 7)
	if !ok {
		return
	}
	cfg := s.rt.Settings.Get()
	ranking, err := s.st.TopicRanking(c, from, 100)
	if err != nil {
		internal(c, err)
		return
	}
	chats, err := s.chatProgress(c, "topic:", func(string, string) time.Duration { return agent.TopicSettle }, cfg.TopicsOn)
	if err != nil {
		internal(c, err)
		return
	}
	resp := gin.H{"config": cfg.Topics, "anyEnabled": cfg.AnyTopics(), "qqGroupAll": cfg.QQBot.GroupAll,
		"from": from, "days": days, "ranking": ranking, "chats": chats}
	if at, ok, err := s.st.Cursor(c, "topic:private"); err == nil && ok {
		resp["privateCursor"] = at
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) topicOccurrences(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	from, _, ok := windowParam(c, 7)
	if !ok {
		return
	}
	p := page(c)
	list, total, err := s.st.TopicOccurrences(c, id, from, p, pageSize)
	if err != nil {
		internal(c, err)
		return
	}
	paged(c, list, total, p)
}

func (s *Server) renameTopic(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		Title string `json:"title"`
	}
	if !bind(c, &req) {
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" || utf8.RuneCountInString(title) > 60 {
		fail(c, http.StatusBadRequest, "主题名称为 1~60 字")
		return
	}
	err := s.st.RenameTopic(c, id, title)
	if errors.Is(err, store.ErrTopicExists) {
		fail(c, http.StatusConflict, fmt.Sprintf("已有主题「%s」，请改用「合并到…」", title))
		return
	}
	if err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) mergeTopic(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		Into int64 `json:"into"`
	}
	if !bind(c, &req) {
		return
	}
	if err := s.st.MergeTopic(c, id, req.Into); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) deleteTopic(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.st.DeleteTopic(c, id); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// runTopics 后台「立即归类」：同步跑一轮，可能要一两分钟（每批一次模型调用）。
func (s *Server) runTopics(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c, 10*time.Minute)
	defer cancel()
	n, err := s.rt.Agent.ScanTopics(ctx)
	if err != nil {
		fail(c, http.StatusBadGateway, "归类失败："+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("本轮归类 %d 条提问", n)})
}

// ── 按群设置 ──

// groupRow 后台「群管理」的一行：群的基本信息、单独设置与各功能的实际生效值。
type groupRow struct {
	Channel   string                `json:"channel"`
	ChatType  string                `json:"chatType"`
	ChatID    string                `json:"chatId"`
	Title     string                `json:"title"`
	Messages  int                   `json:"messages"`
	LastAt    *time.Time            `json:"lastAt"`
	Allowed   bool                  `json:"allowed"`
	Policy    *settings.GroupPolicy `json:"policy"`
	Effective groupEffective        `json:"effective"`
}

type groupEffective struct {
	Proactive  bool `json:"proactive"`
	SilenceSec int  `json:"silenceSec"`
	Welcome    bool `json:"welcome"`
	JoinReview bool `json:"joinReview"`
	Reflect    bool `json:"reflect"`
	Topics     bool `json:"topics"`
}

func effective(cfg settings.All, ch, chatID string) groupEffective {
	return groupEffective{
		Proactive: cfg.ProactiveOn(ch, chatID), SilenceSec: cfg.ProactiveSilence(ch, chatID),
		Welcome: cfg.WelcomeOn(ch, chatID), JoinReview: cfg.JoinReviewOn(ch, chatID),
		Reflect: cfg.ReflectOn(ch, chatID), Topics: cfg.TopicsOn(ch, chatID),
	}
}

// groupNameLookups 一次请求最多向渠道查几个群名（QQ 接口 30 QPM）
const groupNameLookups = 10

// listGroupPolicies 机器人见过的群（会话 + 群时间线）与已有单独设置的群合在一起，最近活跃的在前。
func (s *Server) listGroupPolicies(c *gin.Context) {
	cfg := s.rt.Settings.Get()
	rows := map[string]*groupRow{}
	get := func(ch, chatType, chatID string) *groupRow {
		key := ch + ":" + chatID
		r := rows[key]
		if r == nil {
			r = &groupRow{Channel: ch, ChatType: chatType, ChatID: chatID}
			rows[key] = r
		}
		return r
	}
	known, err := s.st.KnownGroups(c)
	if err != nil {
		internal(c, err)
		return
	}
	for _, g := range known {
		r := get(g.Channel, "group", g.ChatID)
		r.Title, r.LastAt = g.Title, &g.LastAt
	}
	stats, err := s.st.GroupChatStats(c)
	if err != nil {
		internal(c, err)
		return
	}
	for _, st := range stats {
		r := get(st.Channel, st.ChatType, st.ChatID)
		r.Messages = st.Messages
		if r.Title == "" {
			r.Title = st.Title
		}
		if r.LastAt == nil || st.LastAt.After(*r.LastAt) {
			r.LastAt = &st.LastAt
		}
	}
	for _, p := range cfg.Groups.Policies {
		ch, chatID, _ := strings.Cut(p.Chat, ":")
		r := get(ch, "group", chatID)
		p := p
		r.Policy = &p
	}
	// 还没有群名的群（QQ 群事件只带 openid）向渠道查一次；渠道侧有缓存，每次最多查 groupNameLookups 个
	lookups := 0
	ctx, cancel := context.WithTimeout(c, 5*time.Second)
	defer cancel()
	for _, r := range rows {
		if r.Title == "" && lookups < groupNameLookups && ctx.Err() == nil {
			lookups++
			r.Title = s.rt.Registry.GroupName(ctx, r.Channel, r.ChatID)
			if r.Title != "" {
				if err := s.st.FillGroupTitle(ctx, r.Channel, r.ChatID, r.Title); err != nil {
					s.log.Warn("写回群名失败", "err", err)
				}
			}
		}
	}
	list := make([]*groupRow, 0, len(rows))
	for key, r := range rows {
		r.Allowed = cfg.Agent.AllowAllChats || contains(cfg.Agent.Chats, key)
		r.Effective = effective(cfg, r.Channel, r.ChatID)
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i].LastAt, list[j].LastAt
		if (a == nil) != (b == nil) {
			return a != nil
		}
		if a != nil && !a.Equal(*b) {
			return a.After(*b)
		}
		return list[i].Channel+list[i].ChatID < list[j].Channel+list[j].ChatID
	})
	c.JSON(http.StatusOK, gin.H{
		"list": list,
		"global": groupEffective{
			Proactive: cfg.Proactive.Enabled, SilenceSec: cfg.Proactive.SilenceSec, Welcome: cfg.Welcome.Enabled,
			JoinReview: cfg.JoinReview.Enabled, Reflect: cfg.Reflect.Enabled, Topics: cfg.Topics.Enabled,
		},
		"allowAllChats": cfg.Agent.AllowAllChats,
		"qqGroupAll":    cfg.QQBot.GroupAll,
	})
}
