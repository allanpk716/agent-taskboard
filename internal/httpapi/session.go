package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/captcha"

	accessrecords "github.com/allanpk716/agent-taskboard/internal/accessrecords"
	"github.com/allanpk716/agent-taskboard/internal/archive"
	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/drafts"
	"github.com/allanpk716/agent-taskboard/internal/idempotency"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/notifications"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/storage"
	"github.com/allanpk716/agent-taskboard/internal/templates"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// SessionCookieName 是网页会话 cookie 的名字。
const SessionCookieName = "tb_session"
const GuestDeviceCookieName = "tb_guest_device"

// API 挂载 /api/v1 下的接口。
type API struct {
	ident          *identity.Manager
	tickets        *tickets.Manager
	templates      *templates.Manager
	idem           *idempotency.Manager
	atts           *attachments.Manager
	backups        *ops.Backups
	notifications  *notifications.Manager
	accessRecords  *accessrecords.Manager
	drafts         *drafts.Manager
	storage        *storage.Service
	archive        *archive.Service
	cfg            config.Config
	clk            clock.Clock
	intakeUploads  *requestRateLimiter
	clientOnlineMu sync.Mutex
	clientOnline   map[int64]int
	captchaSource  captcha.Source
	captchaMu      sync.Mutex
	captchas       map[string]captchaRecord
	loginLimits    *requestRateLimiter
	loginLocks     *loginLockStore
}

// SetCaptchaSource 装配登录验证码来源（生产固定随机，测试可注入）。
func (a *API) SetCaptchaSource(source captcha.Source) {
	a.captchaSource = source
	a.captchas = make(map[string]captchaRecord)
}

// SetLoginRateLimit 调整登录来源限流；只供测试二进制装配使用，生产固定 20 次/分钟。
func (a *API) SetLoginRateLimit(perMinute int) {
	if perMinute < 1 {
		panic("登录限流必须大于 0")
	}
	a.loginLimits = newRequestRateLimiter(perMinute, a.clk)
}

type captchaRecord struct {
	answer    string
	expiresAt time.Time
	used      bool
}

func NewAPI(ident *identity.Manager, tk *tickets.Manager, tpl *templates.Manager, idem *idempotency.Manager, atts *attachments.Manager, backups *ops.Backups, accessRecs *accessrecords.Manager, draftStore *drafts.Manager, storageStats *storage.Service, archives *archive.Service, cfg config.Config, clk clock.Clock) *API {
	return &API{ident: ident, tickets: tk, templates: tpl, idem: idem, atts: atts, backups: backups,
		accessRecords: accessRecs, drafts: draftStore, storage: storageStats, archive: archives, cfg: cfg, clk: clk,
		intakeUploads: newRequestRateLimiter(cfg.Intake.UploadRatePerMin, clk),
		clientOnline:  make(map[int64]int),
		captchaSource: captcha.Random{}, captchas: make(map[string]captchaRecord),
		loginLimits: newRequestRateLimiter(loginRatePerMinute, clk),
		loginLocks:  newLoginLockStore()}
}

// Handler 返回 /api/v1 前缀的 http.Handler（带 panic 恢复，统一错误格式）。
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/session", a.handleSession)
	mux.HandleFunc("/api/v1/login-captcha", a.handleLoginCaptcha)
	mux.HandleFunc("/api/v1/guest-link", a.handleGuestLinkClaim)
	mux.HandleFunc("/api/v1/guest-links/claim", a.handleGuestLinkClaim)
	mux.HandleFunc("/api/v1/guest-links/enter", a.handleGuestLinkClaim)
	mux.HandleFunc("/api/v1/me/agent-tokens", a.handleAgentTokens)
	mux.HandleFunc("/api/v1/me/agent-tokens/", a.handleAgentToken)
	mux.HandleFunc("/api/v1/me/notices/", a.handleMemberNotice)
	mux.HandleFunc("/api/v1/me/password", a.handleOwnPassword)
	mux.HandleFunc("/api/v1/tickets", a.handleTicketsRoot)
	mux.HandleFunc("/api/v1/tickets/", a.handleTicketRef)
	mux.HandleFunc("/api/v1/drafts", a.handleDrafts)
	mux.HandleFunc("/api/v1/drafts/", a.handleDraft)
	mux.HandleFunc("/api/v1/inbox", a.handleInbox)
	mux.HandleFunc("/api/v1/notifications", a.handleNotifications)
	mux.HandleFunc("/api/v1/notifications/unread", a.handleNotificationsUnread)
	mux.HandleFunc("/api/v1/notifications/read", a.handleNotificationsRead)
	mux.HandleFunc("/api/v1/notifications/stream", a.handleNotificationsStream)
	mux.HandleFunc("/api/v1/client/connect", a.handleClientConnect)
	mux.HandleFunc("/api/v1/client/token", a.handleClientToken)
	mux.HandleFunc("/api/v1/me/clients", a.handleClients)
	mux.HandleFunc("/api/v1/me/clients/", a.handleClient)
	mux.HandleFunc("/api/v1/members", a.handleMembersRoot)
	mux.HandleFunc("/api/v1/members/", a.handleMembersRef)
	mux.HandleFunc("/api/v1/labels", a.handleLabelsRoot)
	mux.HandleFunc("/api/v1/labels/", a.handleLabelsSub)
	mux.HandleFunc("/api/v1/attachments/", a.handleAttachmentDownload)
	mux.HandleFunc("/api/v1/templates", a.handleTemplatesRoot)
	mux.HandleFunc("/api/v1/templates/", a.handleTemplateRef)
	mux.HandleFunc("/api/v1/intakes", a.handleIntakesRoot)
	mux.HandleFunc("/api/v1/intakes/", a.handleIntakeRef)
	mux.HandleFunc("/api/v1/admin/backups", a.handleAdminBackups)
	mux.HandleFunc("/api/v1/admin/storage", a.handleAdminStorage)
	mux.HandleFunc("/api/v1/admin/archive/preview", a.handleAdminArchivePreview)
	mux.HandleFunc("/api/v1/admin/archive/packages", a.handleAdminArchivePackages)
	mux.HandleFunc("/api/v1/admin/archive/packages/", a.handleAdminArchivePackages)
	mux.HandleFunc("/api/v1/admin/login-locks", a.handleLoginLocks)
	mux.HandleFunc("/api/v1/admin/login-locks/unlock", a.handleLoginUnlock)
	mux.HandleFunc("/api/v1/admin/update", a.handleAdminUpdate)
	mux.HandleFunc("/api/v1/health", a.handleHealth)
	mux.HandleFunc("/api/v1", a.handleAPIRoot)
	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/" {
			a.handleAPIRoot(w, r)
			return
		}
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口",
			"接口说明见 "+a.docsURL(r)+"；主要接口在 /api/v1/tickets、/api/v1/inbox、/api/v1/labels")
	})
	return recoverMiddleware(a.clientScopeMiddleware(mux))
}

// clientScopeMiddleware 在认证成功后限制客户端凭证的接口面。通知接口的子路径
// 由这里统一放行，后续新增通知端点不需要逐个 handler 补检查。
func (a *API) clientScopeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, hasToken := bearerToken(r)
		if !hasToken || !identity.IsClientToken(token) {
			next.ServeHTTP(w, r)
			return
		}
		viewer, ok := a.viewerFromRequest(w, r)
		if !ok {
			return
		}
		allowed := strings.HasPrefix(r.URL.Path, "/api/v1/notifications") ||
			(r.Method == http.MethodGet && r.URL.Path == "/api/v1/session") ||
			(r.Method == http.MethodDelete && r.URL.Path == "/api/v1/me/clients/self")
		if !allowed {
			WriteError(w, http.StatusForbidden, CodeClientScope, "客户端凭证只能读取通知和身份",
				"请用浏览器打开工单处理；如需其它操作，登录网页后操作")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), viewerContextKey, viewer)))
	})
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
	Username      string `json:"username"`
	Password      string `json:"password"`
	CaptchaID     string `json:"captcha_id"`
	CaptchaAnswer string `json:"captcha_answer"`
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if retry := a.loginLimits.allow(ip); retry > 0 {
		a.writeLoginRateLimited(w, retry)
		return
	}
	var req loginRequest
	if !decodeJSON(w, r, &req, `{"username": "…", "password": "…"}`) {
		return
	}
	if err := a.consumeCaptcha(req.CaptchaID, req.CaptchaAnswer); err != nil {
		a.writeCaptchaError(w, err)
		return
	}
	now := a.clk.Now()
	if remaining, locked := a.loginLocks.locked(req.Username, ip, now); locked {
		minutes := remainingMinutes(remaining)
		WriteErrorExtra(w, http.StatusForbidden, CodeLoginLocked,
			fmt.Sprintf("账号已锁定，约 %d 分钟后再试或联系管理员", minutes),
			"", map[string]any{"remaining_minutes": minutes})
		return
	}
	// 验证码校验通过后才是密码校验；#229 的密码失败统计应加在这个调用内部/调用点。
	sess, err := a.ident.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCredentials) {
			if a.loginLocks.recordFailure(req.Username, ip, now) {
				WriteErrorExtra(w, http.StatusForbidden, CodeLoginLocked,
					fmt.Sprintf("账号已锁定，约 %d 分钟后再试或联系管理员", remainingMinutes(loginLockDuration)),
					"", map[string]any{"remaining_minutes": remainingMinutes(loginLockDuration)})
				return
			}
			WriteError(w, http.StatusUnauthorized, CodeInvalidCredentials, "用户名或密码不正确", "")
			return
		}
		if errors.Is(err, identity.ErrMemberDeactivated) {
			WriteError(w, http.StatusUnauthorized, CodeMemberDeactivated, "账号已停用", "请联系管理员重新启用账号（管理员在网页的管理页停用与启用账号）")
			return
		}
		panic(err) // 由 recoverMiddleware 统一转成 500
	}
	a.loginLocks.reset(req.Username, ip)
	http.SetCookie(w, sessionCookie(sess, sess.ExpiresAt))
	logSessionEvent(r, "login user="+sess.Member.Username, sess.ID)
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
	view := a.newSessionView(viewer)
	if viewer.Kind == identity.ViewerGuest {
		seq := strconv.FormatInt(viewer.GuestLink.TicketSeq, 10)
		full, err := a.tickets.ByRef(r.Context(), seq)
		if err != nil {
			a.writeTicketError(w, r, seq, err)
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
		a.wakeMemberStreams(sess.Member.ID)
		logSessionEvent(r, "logout user="+sess.Member.Username, sess.ID)
	}
	// 无论是否登录过，都清掉 cookie，幂等返回 204。
	http.SetCookie(w, sessionCookie(identity.Session{}, timeZero()))
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleOwnPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	// Viewer 只带身份；ChangeOwnPassword 还需要当前会话 id 来保留这次登录。
	sess, ok := a.sessionFromRequest(w, r)
	if !ok {
		return
	}
	if sess.Member.ID != viewer.Member.ID {
		WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "会话不存在或已过期", a.authHint(r))
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !decodeJSON(w, r, &req, `{"old_password": "…", "new_password": "…"}`) {
		return
	}
	err := a.ident.ChangeOwnPassword(r.Context(), sess.Member.ID, sess.ID, req.OldPassword, req.NewPassword)
	switch {
	case errors.Is(err, identity.ErrInvalidOldPassword):
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidOldPassword, "原密码不正确", "")
	case errors.Is(err, identity.ErrPasswordTooShort):
		WriteError(w, http.StatusUnprocessableEntity, CodePasswordTooShort, "新密码至少 8 位", "")
	case errors.Is(err, identity.ErrPasswordUnchanged):
		WriteError(w, http.StatusUnprocessableEntity, CodePasswordUnchanged, "新密码与旧密码相同", "")
	case err != nil:
		panic(err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) sessionFromRequest(w http.ResponseWriter, r *http.Request) (identity.Session, bool) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "未登录", a.authHint(r))
		return identity.Session{}, false
	}
	sess, err := a.ident.SessionByID(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCredentials) {
			WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "会话不存在或已过期", a.authHint(r))
			return identity.Session{}, false
		}
		panic(err)
	}
	return sess, true
}

// agentView 是 viewer.agent 的结构：具名 Agent 的 id 和名字（本人为 null）。
type agentView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	DraftMode string `json:"draft_mode,omitempty"`
}

type clientView struct {
	ID       int64  `json:"id"`
	Hostname string `json:"hostname"`
}

type draftsSummaryView struct {
	Count int    `json:"count"`
	URL   string `json:"url"`
}

// viewerView 是 viewer 的对外结构：成员带 member/agent；访客（#32）member 与
// agent 为 null，另带 link_note / link_expires_at / can_edit_brief。
type viewerView struct {
	Kind          string           `json:"kind"`
	Member        *identity.Member `json:"member"`
	Agent         *agentView       `json:"agent"`
	Client        *clientView      `json:"client"`
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
	Viewer  viewerView         `json:"viewer"`
	Inbox   string             `json:"inbox,omitempty"`
	Ticket  *sessionTicketView `json:"ticket,omitempty"`
	Drafts  *draftsSummaryView `json:"drafts,omitempty"`
	Notices []string           `json:"notices,omitempty"`
}

func (a *API) newSessionView(v identity.Viewer) sessionView {
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
		vv.Agent = &agentView{ID: v.Agent.ID, Name: v.Agent.Name, DraftMode: v.Agent.DraftMode}
	}
	if v.Client != nil {
		vv.Client = &clientView{ID: v.Client.ID, Hostname: v.Client.Hostname}
	}
	count := 0
	if list, err := a.drafts.List(context.Background(), v); err == nil {
		count = len(list)
	}
	view := sessionView{Viewer: vv, Inbox: "/api/v1/inbox",
		Drafts: &draftsSummaryView{Count: count, URL: "/api/v1/drafts"}, Notices: []string{}}
	if v.IsWebSession() {
		notices, err := a.ident.UnseenWebNotices(context.Background(), v.Member)
		if err == nil {
			view.Notices = notices
		}
	}
	return view
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
