package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/publicbase"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// viewerFromRequest 把请求上的凭证（网页会话 cookie、Agent token 或访客链接密钥）
// 解析成 Viewer。凭证缺失或失效时写出错误并返回 ok=false。带 Authorization: Bearer
// 时按凭证解析（先试 Agent token，再试访客链接密钥），且不回退到 cookie——
// 凭证坏了应当报错，而不是悄悄当作本人。访客链接的撤销与过期即时生效：
// 每个请求都实时查库判定，分别返回 401 link_revoked / link_expired。
func (a *API) viewerFromRequest(w http.ResponseWriter, r *http.Request) (identity.Viewer, bool) {
	if viewer, ok := viewerFromContext(r.Context()); ok {
		return viewer, true
	}
	if token, ok := bearerToken(r); ok {
		var viewer identity.Viewer
		var err error
		guestCredential, isGuestAgentCredential := identity.ParseGuestAgentCredential(token)
		credentialForArchive := token
		if isGuestAgentCredential {
			credentialForArchive = guestCredential.Key
			viewer, err = a.guestAgentViewer(r, guestCredential)
		} else if identity.IsClientToken(token) {
			viewer, err = a.ident.ViewerFromClientToken(r.Context(), token, r.UserAgent())
		} else if identity.IsGuestDeviceKey(token) {
			viewer, err = a.ident.ViewerFromGuestDevice(r.Context(), token)
		} else {
			viewer, err = a.ident.ViewerFromAgentToken(r.Context(), token)
		}
		if errors.Is(err, identity.ErrInvalidCredentials) {
			viewer, err = a.ident.ViewerFromGuestKey(r.Context(), token)
		}
		if err != nil {
			switch {
			case errors.Is(err, identity.ErrInvalidCredentials):
				if a.writeArchivedCredential(w, r, credentialForArchive) {
					return identity.Viewer{}, false
				}
				WriteError(w, http.StatusUnauthorized, CodeInvalidToken, "凭证无效或已撤销",
					a.authHint(r))
			case errors.Is(err, identity.ErrLinkRevoked),
				errors.Is(err, identity.ErrLinkExpired),
				errors.Is(err, identity.ErrLinkUnclaimed),
				errors.Is(err, identity.ErrLinkClaimed):
				a.writeGuestLinkError(w, r, err)
			case errors.Is(err, tickets.ErrAccessCodeInvalid):
				WriteError(w, http.StatusUnauthorized, CodeAccessCodeInvalid, err.Error(),
					"访客 Agent 凭证是 tbg_<链接密钥>:<访问码>；连续输错 5 次会锁定")
			case errors.Is(err, tickets.ErrAccessCodeLocked):
				WriteError(w, http.StatusForbidden, CodeAccessCodeLocked, err.Error(),
					"请联系能管理这条链接的成员重置访问码")
			default:
				panic(err)
			}
			return identity.Viewer{}, false
		}
		return viewer, true
	}

	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		viewer, guestErr, ok := a.guestDeviceFromCookie(r)
		if ok {
			if guestErr != nil {
				a.writeGuestLinkError(w, r, guestErr)
				return identity.Viewer{}, false
			}
			return viewer, true
		}
		logSessionEvent(r, "401 no-cookie path="+r.URL.Path, "")
		WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "未登录", a.authHint(r))
		return identity.Viewer{}, false
	}
	viewer, err := a.ident.ViewerFromSession(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCredentials) {
			logSessionEvent(r, "401 invalid-session path="+r.URL.Path, cookie.Value)
			WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "会话不存在或已过期", a.authHint(r))
			return identity.Viewer{}, false
		}
		panic(err)
	}
	return viewer, true
}

func (a *API) guestAgentViewer(r *http.Request, credential identity.GuestAgentCredential) (identity.Viewer, error) {
	link, err := a.ident.GuestLinkForKey(r.Context(), credential.Key)
	if errors.Is(err, identity.ErrLinkClaimed) {
		var status *identity.LinkStatusError
		if errors.As(err, &status) {
			return a.tickets.ViewerFromGuestAgentCredential(r.Context(), status.Link, credential.AccessCode)
		}
		return identity.Viewer{}, err
	}
	if errors.Is(err, identity.ErrLinkUnclaimed) {
		// ViewerFromGuestKey 会带回被拒访问记录所需的链接身份。
		_, statusErr := a.ident.ViewerFromGuestKey(r.Context(), credential.Key)
		if statusErr != nil {
			return identity.Viewer{}, statusErr
		}
		return identity.Viewer{}, err
	}
	if err != nil {
		return identity.Viewer{}, err
	}
	return a.tickets.ViewerFromGuestAgentCredential(r.Context(), link, credential.AccessCode)
}

// guestDeviceFromCookie 让同源插图与附件链接复用记住的设备凭证，
// 且不把凭证暴露给 JavaScript。
func (a *API) guestDeviceFromCookie(r *http.Request) (identity.Viewer, error, bool) {
	cookie, err := r.Cookie(GuestDeviceCookieName)
	if err != nil || cookie.Value == "" {
		return identity.Viewer{}, nil, false
	}
	viewer, err := a.ident.ViewerFromGuestDevice(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCredentials) {
			return identity.Viewer{}, nil, false
		}
		return identity.Viewer{}, err, true
	}
	return viewer, nil, true
}

// writeGuestLinkError 统一处理访客链接状态拒绝的响应与访问记录。
func (a *API) writeGuestLinkError(w http.ResponseWriter, r *http.Request, err error) {
	var status *identity.LinkStatusError
	if !errors.As(err, &status) {
		panic(err)
	}
	if recErr := a.accessRecords.RecordGuestLinkDenied(r.Context(), status.Link.TicketSeq, status.Link.ID, string(status.Status)); recErr != nil {
		panic(recErr)
	}
	a.writeLinkStatusError(w, r, err)
}

func (a *API) authHint(r *http.Request) string {
	return "Agent 用主人在网页「我的 → Agent」签发的 token：Authorization: Bearer $TASKBOARD_TOKEN；说明见 " +
		a.docsURL(r) + "。网页用户请先登录。"
}

func (a *API) docsURL(r *http.Request) string {
	return a.requestBaseURL(r) + "/agent.md"
}

func (a *API) requestBaseURL(r *http.Request) string {
	return publicbase.FromRequest(a.cfg.Server.PublicURL, r)
}

type viewerContextKeyType struct{}

var viewerContextKey viewerContextKeyType

func viewerFromContext(ctx context.Context) (identity.Viewer, bool) {
	viewer, ok := ctx.Value(viewerContextKey).(identity.Viewer)
	return viewer, ok
}

// webSessionFromRequest 要求凭证是成员本人的网页会话。
// 创建和撤销 Agent token、创建成员账号只接受这种凭证（ADR 0003）。
func (a *API) webSessionFromRequest(w http.ResponseWriter, r *http.Request) (identity.Viewer, bool) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return identity.Viewer{}, false
	}
	if !webSessionFromRequest(w, viewer) {
		return identity.Viewer{}, false
	}
	return viewer, true
}

// memberFromRequest 要求凭证是成员（本人网页会话或成员 Agent）；访客 403 members_only。
func (a *API) memberFromRequest(w http.ResponseWriter, r *http.Request) (identity.Viewer, bool) {
	return a.memberFromRequestFor(w, r, "访客不能查看或维护标签")
}

// memberFromRequestFor 同 memberFromRequest，但用传入的 hint 提示访客该模块的
// 正确用法（反馈入口 #35 等：同一错误码、不同的指引）。
func (a *API) memberFromRequestFor(w http.ResponseWriter, r *http.Request, guestHint string) (identity.Viewer, bool) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return identity.Viewer{}, false
	}
	if viewer.Kind != identity.ViewerMember {
		WriteError(w, http.StatusForbidden, CodeMembersOnly, "这个接口只对成员开放", guestHint)
		return identity.Viewer{}, false
	}
	return viewer, true
}

// adminFromRequestFor 要求凭证是管理员的成员（本人网页会话或管理员的 Agent
// token；标签治理、更新、备份状态都走这里）。访客 403 members_only（决议 4：
// 仅成员的接口对访客统一这一个码），普通成员 403 admin_required，adminHint
// 告诉成员这个模块有哪些对成员开放的入口。
func (a *API) adminFromRequestFor(w http.ResponseWriter, r *http.Request, guestHint, adminHint string) (identity.Viewer, bool) {
	viewer, ok := a.memberFromRequestFor(w, r, guestHint)
	if !ok {
		return identity.Viewer{}, false
	}
	if !adminViewerOr403(w, viewer, adminHint) {
		return identity.Viewer{}, false
	}
	return viewer, true
}

// adminViewerOr403 检查成员凭证是否管理员，不是则写 403 admin_required。
func adminViewerOr403(w http.ResponseWriter, viewer identity.Viewer, adminHint string) bool {
	if !viewer.Member.IsAdmin {
		WriteError(w, http.StatusForbidden, CodeAdminRequired, "这个接口只对管理员开放", adminHint)
		return false
	}
	return true
}

// webSessionFromRequest 要求成员凭证是本人的网页会话（创建和撤销 Agent token、
// 创建成员账号只接受这种凭证，ADR 0003）。
func webSessionFromRequest(w http.ResponseWriter, viewer identity.Viewer) bool {
	if !viewer.IsWebSession() {
		WriteError(w, http.StatusForbidden, CodeWebSessionRequired,
			"这个操作只接受网页会话发起的请求",
			"Agent token 不能做这个操作；请在网页上登录后操作（ADR 0003）")
		return false
	}
	return true
}

// absURL 把站内相对路径变成回给用户的地址：配置了 [server] public_url 时
// 用它拼绝对地址（结尾斜杠容忍），否则原样返回相对路径（评审 P2-7）。
func (a *API) absURL(path string) string {
	base := strings.TrimRight(a.cfg.Server.PublicURL, "/")
	if base == "" {
		return path
	}
	return base + path
}

// bearerToken 取出 Authorization: Bearer 请求头里的凭证。
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}
