package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// viewerFromRequest 把请求上的凭证（网页会话 cookie、Agent token 或访客链接密钥）
// 解析成 Viewer。凭证缺失或失效时写出错误并返回 ok=false。带 Authorization: Bearer
// 时按凭证解析（先试 Agent token，再试访客链接密钥），且不回退到 cookie——
// 凭证坏了应当报错，而不是悄悄当作本人。访客链接的撤销与过期即时生效：
// 每个请求都实时查库判定，分别返回 401 link_revoked / link_expired。
func (a *API) viewerFromRequest(w http.ResponseWriter, r *http.Request) (identity.Viewer, bool) {
	if token, ok := bearerToken(r); ok {
		viewer, err := a.ident.ViewerFromAgentToken(r.Context(), token)
		if errors.Is(err, identity.ErrInvalidCredentials) {
			viewer, err = a.ident.ViewerFromGuestKey(r.Context(), token)
		}
		if err != nil {
			switch {
			case errors.Is(err, identity.ErrInvalidCredentials):
				WriteError(w, http.StatusUnauthorized, CodeInvalidToken, "凭证无效或已撤销",
					"检查 token 是否完整；访客可以把整条访客链接的密钥传进来")
			case errors.Is(err, identity.ErrLinkRevoked):
				WriteError(w, http.StatusUnauthorized, CodeLinkRevoked, "访客链接已被撤销",
					"请联系发链接的成员重新获取一条新链接")
			case errors.Is(err, identity.ErrLinkExpired):
				WriteError(w, http.StatusUnauthorized, CodeLinkExpired, "访客链接已过期",
					"请联系发链接的成员延期，或重新获取一条新链接")
			default:
				panic(err)
			}
			return identity.Viewer{}, false
		}
		return viewer, true
	}

	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "未登录", "先调用 POST /api/v1/session 登录")
		return identity.Viewer{}, false
	}
	viewer, err := a.ident.ViewerFromSession(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCredentials) {
			WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "会话不存在或已过期", "重新登录")
			return identity.Viewer{}, false
		}
		panic(err)
	}
	return viewer, true
}

// webSessionFromRequest 要求凭证是成员本人的网页会话。
// 创建和撤销 Agent token、创建成员账号只接受这种凭证（ADR 0003）。
func (a *API) webSessionFromRequest(w http.ResponseWriter, r *http.Request) (identity.Viewer, bool) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return identity.Viewer{}, false
	}
	if !viewer.IsWebSession() {
		WriteError(w, http.StatusForbidden, CodeWebSessionRequired,
			"这个操作只接受网页会话发起的请求",
			"Agent token 不能签发或撤销凭证；请在网页上登录后操作（ADR 0003）")
		return identity.Viewer{}, false
	}
	return viewer, true
}

// bearerToken 取出 Authorization: Bearer <token> 里的 token。
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}
