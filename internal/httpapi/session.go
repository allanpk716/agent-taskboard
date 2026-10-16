package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// SessionCookieName 是网页会话 cookie 的名字。
const SessionCookieName = "tb_session"

// API 挂载 /api/v1 下的接口。
type API struct {
	ident *identity.Manager
}

func NewAPI(ident *identity.Manager) *API {
	return &API{ident: ident}
}

// Handler 返回 /api/v1 前缀的 http.Handler（带 panic 恢复，统一错误格式）。
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/session", a.handleSession)
	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口", "接口都在 /api/v1 之下，检查路径拼写")
	})
	return recoverMiddleware(mux)
}

// handleSession 处理会话资源：
//
//	POST   /api/v1/session  登录（用户名 + 密码换会话 cookie）
//	GET    /api/v1/session  当前凭证的身份（受保护）
//	DELETE /api/v1/session  登出
func (a *API) handleSession(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		a.login(w, r)
	case http.MethodGet:
		a.current(w, r)
	case http.MethodDelete:
		a.logout(w, r)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "请求体不是合法的 JSON", "请求体应为 {\"username\": \"…\", \"password\": \"…\"}")
		return
	}
	sess, err := a.ident.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCredentials) {
			WriteError(w, http.StatusUnauthorized, CodeInvalidCredentials, "用户名或密码不正确", "")
			return
		}
		panic(err) // 由 recoverMiddleware 统一转成 500
	}
	http.SetCookie(w, sessionCookie(sess, sess.ExpiresAt))
	writeJSON(w, http.StatusOK, memberView{Kind: "member", Member: sess.Member})
}

// current 返回当前会话的身份；未登录或会话过期时 401。
func (a *API) current(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.sessionFromRequest(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, memberView{Kind: "member", Member: sess.Member})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if sess, ok := a.sessionFromRequest(w, r); ok {
		if err := a.ident.DeleteSession(r.Context(), sess.ID); err != nil {
			panic(err)
		}
	}
	// 无论是否登录过，都清掉 cookie，幂等返回 204。
	http.SetCookie(w, sessionCookie(identity.Session{}, timeZero()))
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) sessionFromRequest(w http.ResponseWriter, r *http.Request) (identity.Session, bool) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "未登录", "先调用 POST /api/v1/session 登录")
		return identity.Session{}, false
	}
	sess, err := a.ident.SessionByID(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCredentials) {
			WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "会话不存在或已过期", "重新登录")
			return identity.Session{}, false
		}
		panic(err)
	}
	return sess, true
}

type memberView struct {
	Kind   string           `json:"kind"`
	Member identity.Member `json:"member"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
