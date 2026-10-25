package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"zhiwei/internal/store"
)

const (
	sessionCookie = "zw_session"
	sessionTTL    = 14 * 24 * time.Hour
	ctxAdmin      = "admin"
)

// dummyHash 用户不存在时陪跑一次 bcrypt，让两种失败的耗时一致。
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("zhiwei-dummy"), bcrypt.DefaultCost)

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func hashPassword(p string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(h), err
}

func validCredentials(username, password string) error {
	if l := len([]rune(username)); l < 3 || l > 32 {
		return errors.New("用户名长度需在 3~32 个字符之间")
	}
	if len(password) < 8 || len(password) > 72 {
		return errors.New("密码长度需在 8~72 位之间")
	}
	return nil
}

// loginLimiter 同一 IP 连续失败 5 次锁 15 分钟（进程内，单实例部署足够）。
type loginLimiter struct {
	mu   sync.Mutex
	fail map[string]*failRec
}

type failRec struct {
	count int
	until time.Time
}

func (l *loginLimiter) locked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.fail[ip]
	return r != nil && time.Now().Before(r.until)
}

func (l *loginLimiter) record(ip string, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ok {
		delete(l.fail, ip)
		return
	}
	r := l.fail[ip]
	if r == nil || time.Now().After(r.until) && r.count >= 5 {
		r = &failRec{}
		l.fail[ip] = r
	}
	r.count++
	if r.count >= 5 {
		r.until = time.Now().Add(15 * time.Minute)
	}
}

func setSessionCookie(c *gin.Context, token string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https"),
	})
}

func (s *Server) requireAdmin(c *gin.Context) {
	token, err := c.Cookie(sessionCookie)
	if err != nil || token == "" {
		abort(c, http.StatusUnauthorized, "未登录")
		return
	}
	u, err := s.st.AdminBySession(c, hashToken(token))
	if err != nil {
		abort(c, http.StatusUnauthorized, "登录已过期，请重新登录")
		return
	}
	c.Set(ctxAdmin, u)
	c.Next()
}

func currentAdmin(c *gin.Context) store.AdminUser {
	return c.MustGet(ctxAdmin).(store.AdminUser)
}

func (s *Server) login(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !bind(c, &req) {
		return
	}
	ip := c.ClientIP()
	if s.limiter.locked(ip) {
		fail(c, http.StatusTooManyRequests, "登录失败次数过多，请 15 分钟后再试")
		return
	}
	u, err := s.st.AdminByUsername(c, strings.TrimSpace(req.Username))
	switch {
	case err == nil:
		err = bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password))
	case errors.Is(err, store.ErrNotFound):
		// 用户不存在也做一次等价耗时的比较，避免按响应时间枚举用户名
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
	default:
		// 数据库故障不算登录失败：否则库挂着时管理员重试几次就被锁 15 分钟
		internal(c, err)
		return
	}
	s.limiter.record(ip, err == nil)
	if err != nil {
		fail(c, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	token := randomToken()
	if err := s.st.CreateSession(c, hashToken(token), u.ID, sessionTTL); err != nil {
		internal(c, err)
		return
	}
	setSessionCookie(c, token, int(sessionTTL.Seconds()))
	c.JSON(http.StatusOK, gin.H{"username": u.Username})
}

func (s *Server) logout(c *gin.Context) {
	if token, err := c.Cookie(sessionCookie); err == nil {
		_ = s.st.DeleteSession(c, hashToken(token))
	}
	setSessionCookie(c, "", -1)
	c.Status(http.StatusNoContent)
}

func (s *Server) me(c *gin.Context) {
	c.JSON(http.StatusOK, currentAdmin(c))
}

func (s *Server) changePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if !bind(c, &req) {
		return
	}
	u := currentAdmin(c)
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.OldPassword)) != nil {
		fail(c, http.StatusBadRequest, "原密码错误")
		return
	}
	if err := validCredentials(u.Username, req.NewPassword); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	h, err := hashPassword(req.NewPassword)
	if err == nil {
		err = s.st.UpdateAdminPassword(c, u.ID, h)
	}
	if err != nil {
		internal(c, err)
		return
	}
	// 改密会清掉全部会话，包括当前这个
	setSessionCookie(c, "", -1)
	c.Status(http.StatusNoContent)
}
