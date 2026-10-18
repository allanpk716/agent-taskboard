// 成员账号接口（#42）：GET /api/v1/members 列出成员（#41 指派下拉用），
// POST /api/v1/members 创建成员账号、POST /api/v1/members/{id}/deactivate 停用
// （都仅限管理员的网页会话：创建成员账号只接受网页会话发起，ADR 0003；
// 访客 403 members_only，决议 4）。
package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// adminWebSessionFromRequest 要求凭证是管理员的本人网页会话（创建与停用成员
// 账号；成员 → 网页会话 → 管理员逐层检查，共用 viewer.go 旁的帮手）。
func (a *API) adminWebSessionFromRequest(w http.ResponseWriter, r *http.Request, adminHint string) (identity.Viewer, bool) {
	viewer, ok := a.memberFromRequestFor(w, r, "访客不能管理成员账号")
	if !ok {
		return identity.Viewer{}, false
	}
	if !webSessionFromRequest(w, viewer) {
		return identity.Viewer{}, false
	}
	if !adminViewerOr403(w, viewer, adminHint) {
		return identity.Viewer{}, false
	}
	return viewer, true
}

// handleMembersRoot 处理成员集合：
//
//	GET  /api/v1/members  成员名单（仅成员可读，#41 指派下拉用）
//	POST /api/v1/members  创建成员账号（管理员网页会话；初始密码只回显一次）
func (a *API) handleMembersRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.listMembers(w, r)
	case http.MethodPost:
		a.createMember(w, r)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			"成员列表用 GET 读取；账号管理在后续版本加入")
	}
}

// handleMembersRef 处理单个成员：
//
//	POST /api/v1/members/{id}/deactivate  停用（管理员网页会话，幂等）
//	POST /api/v1/members/{id}/reactivate  恢复（管理员网页会话，幂等）
//	POST /api/v1/members/{id}/triager     设置初筛人（管理员网页会话，幂等）
//	POST /api/v1/members/{id}/reset-password 重置密码（管理员网页会话）
func (a *API) handleMembersRef(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimPrefix(r.URL.Path, "/api/v1/members/")
	id, sub, hasSub := strings.Cut(ref, "/")
	if !hasSub {
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口",
			"成员账号接口：GET|POST /api/v1/members、POST /api/v1/members/{id}/deactivate|reactivate|triager|reset-password")
		return
	}
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			"停用/恢复账号、设置初筛人和重置密码都用 POST")
		return
	}
	switch sub {
	case "triager":
		a.setTriager(w, r, id)
		return
	case "reset-password":
		a.resetMemberPassword(w, r, id)
		return
	case "reactivate":
		a.reactivateMember(w, r, id)
		return
	case "deactivate":
		a.deactivateMember(w, r, id)
	default:
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口",
			"成员账号接口：GET|POST /api/v1/members、POST /api/v1/members/{id}/deactivate|reactivate|triager|reset-password")
	}
}

func (a *API) resetMemberPassword(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusNotFound, CodeMemberNotFound, "没有这位成员", "")
		return
	}
	admin, ok := a.adminWebSessionFromRequest(w, r, "管理员不能在管理页重置自己的密码")
	if !ok {
		return
	}
	password, err := a.ident.AdminResetMemberPassword(r.Context(), admin.Member.ID, id)
	switch {
	case errors.Is(err, identity.ErrCannotResetSelf):
		WriteError(w, http.StatusUnprocessableEntity, CodeCannotResetSelf, "不能重置自己的密码",
			"自助改密请使用「我的」页面")
	case errors.Is(err, identity.ErrMemberNotFound):
		WriteError(w, http.StatusNotFound, CodeMemberNotFound, "没有这位成员", "")
	case errors.Is(err, identity.ErrMemberInactive):
		WriteError(w, http.StatusUnprocessableEntity, CodeMemberInactive, "成员账号已停用",
			"先恢复账号再重置密码")
	case err != nil:
		panic(err)
	default:
		a.wakeMemberStreams(id)
		writeJSON(w, http.StatusOK, map[string]string{"initial_password": password})
	}
}

// memberListItem 是成员名单（GET /api/v1/members）的条目；active 供 #42 管理页
// 与下拉过滤使用（已停用成员不能再登录，但历史署名与事件保留）。
type memberListItem struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Active   *bool  `json:"active,omitempty"`
	Triager  bool   `json:"triager"`
}

func (a *API) listMembers(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	if viewer.Kind != identity.ViewerMember {
		WriteError(w, http.StatusForbidden, CodeMembersOnly, "成员列表只对成员开放",
			"访客不能浏览成员名单")
		return
	}
	rows, err := a.ident.ListMembersDetailed(r.Context())
	if err != nil {
		panic(err)
	}
	refs := make([]memberListItem, 0, len(rows))
	for _, m := range rows {
		active := m.Active()
		refs = append(refs, memberListItem{ID: m.ID, Username: m.Username, Name: m.Name, Active: &active, Triager: m.Triager})
	}
	writeJSON(w, http.StatusOK, struct {
		Members []memberListItem `json:"members"`
	}{Members: refs})
}

func (a *API) setTriager(w http.ResponseWriter, r *http.Request, idStr string) {
	if _, ok := a.adminWebSessionFromRequest(w, r, "初筛人只能由管理员在网页上指定"); !ok {
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusNotFound, CodeMemberNotFound, "没有这位成员", "")
		return
	}
	var req struct {
		Triager bool `json:"triager"`
	}
	if !decodeJSON(w, r, &req, `{"triager": true}`) {
		return
	}
	if err := a.ident.SetTriager(r.Context(), id, req.Triager); err != nil {
		if errors.Is(err, identity.ErrMemberNotFound) {
			WriteError(w, http.StatusNotFound, CodeMemberNotFound, "没有这位成员", "")
			return
		}
		panic(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "triager": req.Triager})
}

// createMemberRequest 是 POST /api/v1/members 的请求体；密码不收（服务器生成
// 初始密码，只在创建响应里出现一次，成员用它首次登录）。
type createMemberRequest struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

// memberAccountView 是创建成员响应里 member 的结构（active 恒 true）。
type memberAccountView struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	IsAdmin  bool   `json:"is_admin"`
	Active   bool   `json:"active"`
}

// createMemberView 是创建成员的响应：member + 只出现一次的 initial_password。
type createMemberView struct {
	Member          memberAccountView `json:"member"`
	InitialPassword string            `json:"initial_password"`
}

func (a *API) createMember(w http.ResponseWriter, r *http.Request) {
	// 创建成员账号只接受网页会话（ADR 0003：Agent 不能签发凭证、创建账号），
	// 且仅限管理员（spec 用户故事 88：账号只能由管理员在网页上创建与停用）。
	if _, ok := a.adminWebSessionFromRequest(w, r, "成员账号只能由管理员在网页上创建（spec 用户故事 88）"); !ok {
		return
	}
	var req createMemberRequest
	if !decodeJSON(w, r, &req, `{"username": "zhao", "name": "赵工"}`) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Name = strings.TrimSpace(req.Name)
	if req.Username == "" {
		WriteError(w, http.StatusUnprocessableEntity, CodeUsernameRequired, "登录名不能为空",
			"登录名用小写字母和数字，成员用它登录，如 zhao")
		return
	}
	if strings.ContainsAny(req.Username, " \t/\\") {
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidUsername, "登录名里有不适合的字符",
			"登录名用小写字母和数字（可带连字符），不要有空格或斜杠")
		return
	}
	password, err := identity.GenerateInitialPassword()
	if err != nil {
		panic(err)
	}
	member, err := a.ident.CreateMember(r.Context(), req.Username, req.Name, password)
	if err != nil {
		if errors.Is(err, identity.ErrUsernameTaken) {
			WriteError(w, http.StatusConflict, CodeUsernameTaken, "登录名已存在",
				"换一个登录名；已有账号的停用与启用见 POST /api/v1/members/{id}/deactivate")
			return
		}
		panic(err)
	}
	writeJSON(w, http.StatusCreated, createMemberView{
		Member: memberAccountView{
			ID: member.ID, Username: member.Username, Name: member.Name,
			IsAdmin: member.IsAdmin, Active: true,
		},
		InitialPassword: password,
	})
}

func (a *API) deactivateMember(w http.ResponseWriter, r *http.Request, idStr string) {
	viewer, ok := a.adminWebSessionFromRequest(w, r, "成员账号只能由管理员在网页上停用（spec 用户故事 88）")
	if !ok {
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusNotFound, CodeMemberNotFound, "没有这位成员", "")
		return
	}
	if id == viewer.Member.ID {
		WriteError(w, http.StatusUnprocessableEntity, CodeCannotDeactivateSelf,
			"不能停用自己的账号", "把自己停用后就没人能管理账号了；如需移交管理员，先创建另一位管理员")
		return
	}
	changed, err := a.ident.DeactivateMember(r.Context(), id)
	if err != nil {
		if errors.Is(err, identity.ErrMemberNotFound) {
			WriteError(w, http.StatusNotFound, CodeMemberNotFound, "没有这位成员", "")
			return
		}
		panic(err)
	}
	a.wakeMemberStreams(id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "active": false, "deactivated": changed})
}

func (a *API) reactivateMember(w http.ResponseWriter, r *http.Request, idStr string) {
	if _, ok := a.adminWebSessionFromRequest(w, r, "成员账号只能由管理员在网页上恢复"); !ok {
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusNotFound, CodeMemberNotFound, "没有这位成员", "")
		return
	}
	changed, err := a.ident.ReactivateMember(r.Context(), id)
	if err != nil {
		if errors.Is(err, identity.ErrMemberNotFound) {
			WriteError(w, http.StatusNotFound, CodeMemberNotFound, "没有这位成员", "")
			return
		}
		panic(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "active": true, "reactivated": changed})
}
