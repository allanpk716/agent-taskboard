package httpapi

import (
	"net/http"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// recoverMiddleware 把 handler 里的 panic 转成统一格式的 500。
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				WriteError(w, http.StatusInternalServerError, CodeInternal, "服务器内部错误", "")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func timeZero() time.Time { return time.Time{}.UTC() }

// sessionCookie 构造网页会话 cookie：HttpOnly、Secure、SameSite=Lax。
// Secure 始终设置：生产环境经 Caddy 提供 HTTPS，后端只听 127.0.0.1 看不到 TLS。
func sessionCookie(sess identity.Session, expires time.Time) *http.Cookie {
	c := &http.Cookie{
		Name:     SessionCookieName,
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
	if expires.IsZero() {
		c.MaxAge = -1 // 立即删除
	} else {
		c.Expires = expires
	}
	return c
}
