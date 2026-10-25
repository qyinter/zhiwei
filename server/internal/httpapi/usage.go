package httpapi

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"zhiwei/internal/logbuf"
)

// ── 用量统计 ──

// tzParam 后台浏览器所在时区（?tz=Asia/Shanghai）。库的时区通常是 UTC，「今日」与按天统计都按它切日；缺省 UTC。
// 时区名还会原样交给 Postgres（AT TIME ZONE），所以只收 IANA 名：Go 认的 "Local" 是服务器本机时区的别名，Postgres 不认。
func tzParam(c *gin.Context) (*time.Location, bool) {
	name := c.Query("tz")
	if name == "" {
		return time.UTC, true
	}
	loc, err := time.LoadLocation(name)
	if err != nil || name == "Local" {
		fail(c, http.StatusBadRequest, "时区参数无效")
		return nil, false
	}
	return loc, true
}

func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// usage ?days=1|7|30：从 days-1 天前的零点（按 tz）到现在。
func (s *Server) usage(c *gin.Context) {
	loc, ok := tzParam(c)
	if !ok {
		return
	}
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	if days < 1 || days > 90 {
		fail(c, http.StatusBadRequest, "统计天数需在 1~90 之间")
		return
	}
	from := dayStart(time.Now().In(loc)).AddDate(0, 0, -(days - 1))
	u, err := s.st.UsageSince(c, from, loc.String())
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"from": from, "days": days, "usage": u})
}

// ── 运行日志 ──

// listLogs ?after=序号&level=INFO|WARN|ERROR&q=关键词：增量拉取内存里最近的日志（重启清空）。
func (s *Server) listLogs(c *gin.Context) {
	q := logbuf.Query{Keyword: c.Query("q"), Limit: 500}
	q.After, _ = strconv.ParseInt(c.Query("after"), 10, 64)
	if lv := c.Query("level"); lv != "" {
		if err := q.MinLevel.UnmarshalText([]byte(lv)); err != nil {
			fail(c, http.StatusBadRequest, "日志级别只能是 INFO / WARN / ERROR")
			return
		}
	} else {
		q.MinLevel = slog.LevelDebug
	}
	list, latest := s.logs.List(q)
	c.JSON(http.StatusOK, gin.H{"list": list, "latest": latest, "capacity": logbuf.Size})
}
