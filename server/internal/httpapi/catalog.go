package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"zhiwei/internal/catalog"
)

// ── 模型目录测试 ──

// settingsCatalogTest 不走缓存直接拉一次目录，返回模型数与一条示例报价。
func (s *Server) settingsCatalogTest(c *gin.Context) {
	var req struct {
		URL string `json:"url"`
	}
	if !bind(c, &req) {
		return
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		fail(c, http.StatusBadRequest, "目录接口地址需以 http:// 或 https:// 开头")
		return
	}
	ctx, cancel := context.WithTimeout(c, 15*time.Second)
	defer cancel()
	start := time.Now()
	cat, _, err := catalog.NewClient().Get(ctx, req.URL, 0)
	if err != nil {
		fail(c, http.StatusBadGateway, "拉取目录失败："+err.Error())
		return
	}
	sample := ""
	if list := cat.Filter(catalog.Filter{}); len(list) > 0 {
		sample = cat.Price(catalog.Filter{Keyword: list[0].CanonicalName})
		if i := strings.IndexByte(sample, '\n'); i >= 0 {
			sample = sample[i+1:]
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"latencyMs": time.Since(start).Milliseconds(), "models": len(cat.List),
		"families": cat.Families(), "sample": sample,
	})
}
