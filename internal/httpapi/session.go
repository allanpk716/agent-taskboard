package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/idempotency"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// SessionCookieName 是网页会话 cookie 的名字。
const SessionCookieName = "tb_session"

// API 挂载 /api/v1 下的接口。
type API struct {
	ident   *identity.Manager
	tickets *tickets.Manager
	idem    *idempotency.Manager
}

func NewAPI(ident *identity.Manager, tk *tickets.Manager, idem *idempotency.Manager) *API {
	return &API{ident: ident, tickets: tk, idem: idem}
}

// Handler 返回 /api/v1 前缀的 http.Handler（带 panic 恢复，统一错误格式）。
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/session", a.handleSession)
	mux.HandleFunc("/api/v1/me/agent-tokens", a.handleAgentTokens)
	mux.HandleFunc("/api/v1/me/agent-tokens/", a.handleAgentToken)
	mux.HandleFunc("/api/v1/tickets", a.handleTicketsRoot)
	mux.HandleFunc("/api/v1/tickets/", a.handleTicketRef)
	mux.HandleFunc("/api/v1/labels", a.handleLabelsRoot)
	mux.HandleFunc("/api/v1/labels/", a.handleLabelsSub)
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
	member := sess.Member
	writeJSON(w, http.StatusOK, sessionView{Viewer: viewerView{Kind: "member", Member: &member}})
}

// current 返回当前凭证的身份；未登录、会话过期或凭证失效时 401。
// 凭证是 Agent token 时，viewer.agent 指向那个具名 Agent；凭证是访客链接
// 密钥时，另返回链接指向的工单（spec 用户故事 84：贴一条链接就能干活）。
func (a *API) current(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	view := newSessionView(viewer)
	if viewer.Kind == identity.ViewerGuest {
		full, err := a.tickets.ByRef(r.Context(), strconv.FormatInt(viewer.GuestLink.TicketSeq, 10))
		if err != nil {
			a.writeTicketError(w, r, err)
			return
		}
		view.Ticket = &sessionTicketView{
			Number: full.Number, Seq: full.Seq, Title: full.Title,
			API: "/api/v1/tickets/" + full.Number,
		}
	}
	writeJSON(w, http.StatusOK, view)
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

// agentView 是 viewer.agent 的结构：具名 Agent 的 id 和名字（本人为 null）。
type agentView struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// viewerView 是 viewer 的对外结构：成员带 member/agent；访客（#32）member 与
// agent 为 null，另带 link_note / link_expires_at / can_edit_brief。
type viewerView struct {
	Kind          string           `json:"kind"`
	Member        *identity.Member `json:"member"`
	Agent         *agentView       `json:"agent"`
	LinkNote      *string          `json:"link_note,omitempty"`
	LinkExpiresAt *string          `json:"link_expires_at,omitempty"`
	CanEditBrief  *bool            `json:"can_edit_brief,omitempty"`
}

// sessionTicketView 是 GET /api/v1/session 里链接指向的工单（仅访客凭证）。
type sessionTicketView struct {
	Number string `json:"number"`
	Seq    int64  `json:"seq"`
	Title  string `json:"title"`
	API    string `json:"api"`
}

// sessionView 是 GET /api/v1/session 和登录响应的结构：成员带 inbox，
// 访客（#32）改带链接指向的工单。
type sessionView struct {
	Viewer viewerView         `json:"viewer"`
	Inbox  string             `json:"inbox,omitempty"`
	Ticket *sessionTicketView `json:"ticket,omitempty"`
}

func newSessionView(v identity.Viewer) sessionView {
	vv := viewerView{Kind: string(v.Kind)}
	if v.Kind == identity.ViewerGuest {
		note := v.GuestLink.Note
		expiresAt := v.GuestLink.ExpiresAt.Format(time.RFC3339Nano)
		canEdit := v.GuestLink.CanEditBrief
		vv.LinkNote = &note
		vv.LinkExpiresAt = &expiresAt
		vv.CanEditBrief = &canEdit
		return sessionView{Viewer: vv}
	}
	member := v.Member
	vv.Member = &member
	if v.Agent != nil {
		vv.Agent = &agentView{ID: v.Agent.ID, Name: v.Agent.Name}
	}
	return sessionView{Viewer: vv, Inbox: "/api/v1/inbox"}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
