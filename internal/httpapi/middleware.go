package httpapi

import (
	"context"
	"errors"
	"log"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// recoverMiddleware 把 handler 里的 panic 转成统一格式的 500：先带堆栈记
// 日志（评审修正：不再无声吞掉），只在响应还没开始写时补写 500——已写过
// 响应头的补写只会得到 superfluous WriteHeader 和双份响应体，此时保持
// 已写出的部分（连接交给客户端截断处理）。
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tracker := &writeTracker{ResponseWriter: w}
		defer func() {
			if rec := recover(); rec != nil {
				// 客户端断开时查询会带着 context.Canceled 失败：这不是服务器
				// 故障，记一行即可，连接已断也不必补写 500。
				if err, ok := rec.(error); ok && errors.Is(err, context.Canceled) && r.Context().Err() != nil {
					log.Printf("client gone: %s %s: %v", r.Method, r.URL.Path, err)
					return
				}
				log.Printf("panic: %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				if !tracker.wrote {
					WriteError(w, http.StatusInternalServerError, CodeInternal, "服务器内部错误", "")
				}
			}
		}()
		next.ServeHTTP(tracker, r)
	})
}

// writeTracker 记录响应是否已经开始写（写过响应头或响应体都算）。
type writeTracker struct {
	http.ResponseWriter
	wrote bool
}

func (t *writeTracker) WriteHeader(status int) {
	t.wrote = true
	t.ResponseWriter.WriteHeader(status)
}

func (t *writeTracker) Write(p []byte) (int, error) {
	t.wrote = true
	return t.ResponseWriter.Write(p)
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
