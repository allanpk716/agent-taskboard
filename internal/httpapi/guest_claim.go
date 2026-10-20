package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

func (a *API) handleGuestLinkClaim(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/guest-link":
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "读取认领页信息用 GET")
			return
		}
		a.guestLinkInfo(w, r)
	case "/api/v1/guest-links/claim":
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "认领访客链接用 POST")
			return
		}
		a.claimGuestLink(w, r)
	case "/api/v1/guest-links/enter":
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "凭访问码进入用 POST")
			return
		}
		a.enterGuestLink(w, r)
	default:
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口", "")
	}
}

func (a *API) enterGuestLink(w http.ResponseWriter, r *http.Request) {
	link, ok := a.guestClaimLink(w, r, true)
	if !ok {
		return
	}
	var req struct {
		AccessCode string `json:"access_code"`
	}
	if !decodeJSON(w, r, &req, `{"access_code": "123456"}`) {
		return
	}
	result, err := a.tickets.EnterGuestLink(r.Context(), link, req.AccessCode)
	if err != nil {
		switch {
		case errors.Is(err, tickets.ErrAccessCodeRequired):
			WriteError(w, http.StatusUnprocessableEntity, CodeAccessCodeRequired, err.Error(), "访问码是认领时显示的 6 位数字")
		case errors.Is(err, tickets.ErrAccessCodeInvalid):
			WriteError(w, http.StatusUnauthorized, CodeAccessCodeInvalid, err.Error(), "请核对访问码；连续输错 5 次会锁定")
		case errors.Is(err, tickets.ErrAccessCodeLocked):
			WriteError(w, http.StatusForbidden, CodeAccessCodeLocked, err.Error(), "请联系能管理这条链接的成员重置访问码")
		default:
			panic(err)
		}
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: GuestDeviceCookieName, Value: result.DeviceToken, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Expires: result.ClaimedAt.Add(365 * 24 * time.Hour),
	})
	writeJSON(w, http.StatusCreated, map[string]any{"device_token": result.DeviceToken})
}

func (a *API) guestClaimLink(w http.ResponseWriter, r *http.Request, allowClaimed bool) (identity.GuestLink, bool) {
	token, ok := bearerToken(r)
	if !ok || identity.IsGuestDeviceKey(token) {
		WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "未登录",
			"访客认领和凭访问码进入接口用链接里的 tbg_ 密钥作为 Bearer 凭证")
		return identity.GuestLink{}, false
	}
	link, err := a.ident.GuestLinkForKey(r.Context(), token)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrInvalidCredentials):
			WriteError(w, http.StatusUnauthorized, CodeInvalidToken, "凭证无效或已撤销", "")
		case errors.Is(err, identity.ErrLinkRevoked), errors.Is(err, identity.ErrLinkExpired):
			a.writeLinkStatusError(w, r, err)
		case errors.Is(err, identity.ErrLinkClaimed):
			if allowClaimed {
				var status *identity.LinkStatusError
				if errors.As(err, &status) {
					return status.Link, true
				}
			}
			a.writeLinkStatusError(w, r, err)
		default:
			panic(err)
		}
		return identity.GuestLink{}, false
	}
	return link, true
}

func (a *API) writeLinkStatusError(w http.ResponseWriter, r *http.Request, err error) {
	var status *identity.LinkStatusError
	if !errors.As(err, &status) {
		panic(err)
	}
	switch status.Status {
	case identity.GuestLinkRevoked:
		WriteError(w, http.StatusUnauthorized, CodeLinkRevoked, "访客链接已被撤销", "请联系发链接的成员重新获取一条新链接")
	case identity.GuestLinkExpired:
		WriteError(w, http.StatusUnauthorized, CodeLinkExpired, "访客链接已过期", "请联系发链接的成员延期，或重新获取一条新链接")
	case identity.GuestLinkUnclaimed:
		WriteError(w, http.StatusUnauthorized, CodeLinkUnclaimed, "访客链接尚未认领",
			"第一次打开时在认领页输入你的称呼，并点「是我，打开工单」")
	default:
		WriteError(w, http.StatusUnauthorized, CodeLinkClaimed, "这条链接已被认领",
			"如果不是你认领的，请联系邀请人重新处理；请勿把链接转发给别人")
	}
}

func (a *API) guestLinkInfo(w http.ResponseWriter, r *http.Request) {
	link, ok := a.guestClaimLink(w, r, true)
	if !ok {
		return
	}
	state, _, _, err := a.tickets.GuestLinkStatus(r.Context(), link)
	if err != nil {
		panic(err)
	}
	ref := strconv.FormatInt(link.TicketSeq, 10)
	full, err := a.tickets.ByRef(r.Context(), ref)
	if err != nil {
		a.writeTicketError(w, r, ref, err)
		return
	}
	inviter := full.Creator.MemberName
	if inviter == "" {
		inviter = full.AssigneeName
	}
	if inviter == "" {
		inviter = "邀请人"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"state": state, "inviter": inviter, "note": link.Note,
		"valid_until": link.ExpiresAt, "visibility_hint": "这张工单只有你和处理人能看到",
	})
}

func (a *API) claimGuestLink(w http.ResponseWriter, r *http.Request) {
	link, ok := a.guestClaimLink(w, r, true)
	if !ok {
		return
	}
	var req struct {
		AuthorName string `json:"author_name"`
	}
	if !decodeJSON(w, r, &req, `{"author_name": "检验科 张三"}`) {
		return
	}
	result, err := a.tickets.ClaimGuestLink(r.Context(), link, req.AuthorName)
	if err != nil {
		switch {
		case errors.Is(err, tickets.ErrClaimNameRequired):
			WriteError(w, http.StatusUnprocessableEntity, CodeClaimNameRequired, err.Error(), "填写邀请人能认出的称呼")
		case errors.Is(err, tickets.ErrLinkAlreadyClaimed):
			WriteError(w, http.StatusConflict, CodeLinkClaimed, err.Error(),
				"如果不是你认领的，请联系邀请人重新处理；请勿把链接转发给别人")
		default:
			panic(err)
		}
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: GuestDeviceCookieName, Value: result.DeviceToken, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Expires: result.ClaimedAt.Add(365 * 24 * time.Hour),
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"device_token": result.DeviceToken, "access_code": result.AccessCode,
		"claimed_at": result.ClaimedAt,
	})
}
