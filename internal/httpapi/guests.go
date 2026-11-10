// 访客接口：成员和成员 Agent 都能列出并按名字搜索部门访客名单（#328），
// 也能不建工单、先单独新建一位访客（#339）。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// guestView 是访客名单的列表项。
type guestView struct {
	ID                 int64      `json:"id"`
	Name               string     `json:"name"`
	DisabledAt         *time.Time `json:"disabled_at,omitempty"`
	MergedIntoGuestID  int64      `json:"merged_into_guest_id,omitempty"`
	MergedAt           *time.Time `json:"merged_at,omitempty"`
	ClaimedAt          *time.Time `json:"claimed_at,omitempty"`
	ClaimName          string     `json:"claim_name,omitempty"`
	AccessCodeLockedAt *time.Time `json:"access_code_locked_at,omitempty"`
	LinkCount          int        `json:"link_count"`
	PendingCount       int        `json:"pending_count"`
	LastContactAt      *time.Time `json:"last_contact_at,omitempty"`
	DeviceCount        int        `json:"device_count"`
	CreatedBy          authorView `json:"created_by"`
	CreatedAt          time.Time  `json:"created_at"`
}

// handleGuests 处理 /api/v1/guests：
//
//	GET  /api/v1/guests?q=<名字>  列出部门访客名单
//	POST /api/v1/guests           单独新建访客（成员与成员 Agent）
func (a *API) handleGuests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			"列出部门访客用 GET /api/v1/guests?q=<名字>，新建访客用 POST /api/v1/guests")
		return
	}
	viewer, ok := a.memberFromRequestFor(w, r, "访客不能查看部门访客名单")
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		a.createGuest(w, r, viewer)
		return
	}
	guests, err := a.tickets.Guests(r.Context(), viewer, r.URL.Query().Get("q"))
	if err != nil {
		panic(err)
	}
	out := make([]guestView, 0, len(guests))
	for _, g := range guests {
		out = append(out, newGuestView(g))
	}
	writeJSON(w, http.StatusOK, map[string]any{"guests": out})
}

// createGuest 处理 POST /api/v1/guests {"name": "..."}：单独新建一位访客。
// 名字规则与建链接当场新建完全一致（空白 422 guest_required），返回
// 未认领、名下没有链接的访客视图。
func (a *API) createGuest(w http.ResponseWriter, r *http.Request, viewer identity.Viewer) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req, `{"name": "检验科-张三"}`) {
		return
	}
	guest, err := a.tickets.CreateGuest(r.Context(), viewer, req.Name)
	if errors.Is(err, tickets.ErrGuestRequired) {
		WriteError(w, http.StatusUnprocessableEntity, CodeGuestRequired,
			"访客名字不能为空", "请填写去空白后的访客名字，如「检验科-张三」；规则与建链接时新建访客一致")
		return
	}
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"guest": newGuestView(guest)})
}

// handleGuestsRef 处理 /api/v1/guests/{id}/ 下的成员操作：
//
//	GET  /api/v1/guests/{id}                            访客详情（可见范围内的工单与链接）
//	POST /api/v1/guests/{id}/home-share-text            重复复制主页文案（成员与成员 Agent）
//	POST /api/v1/guests/{id}/rename                     改名（访客建立者/管理员/链接管理人，含成员 Agent）
//	POST /api/v1/guests/{id}/disable                    停用访客（仅网页成员会话）
//	POST /api/v1/guests/{id}/enable                     恢复访客（仅网页成员会话）
//	GET  /api/v1/guests/{id}/reset-access-code/preview  重置访问码影响预览（仅网页成员）
//	POST /api/v1/guests/{id}/reset-access-code          访客级重置访问码（仅网页成员）
//	POST /api/v1/guests/{id}/reset-home-key             重置主页地址（仅网页成员）
//	GET  /api/v1/guests/{b}/merge-into/{a}/preview      合并影响预览（仅网页成员）
//	POST /api/v1/guests/{b}/merge-into                   合并访客（仅网页成员）
//	GET  /api/v1/guests/{id}/assign-links/preview        批量归属影响预览（成员与成员 Agent）
//	POST /api/v1/guests/{id}/assign-links                批量归属老链接（成员与成员 Agent）
func (a *API) handleGuestsRef(w http.ResponseWriter, r *http.Request) {
	rest := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if (len(rest) < 5 || len(rest) > 7) && len(rest) != 4 ||
		rest[0] != "api" || rest[1] != "v1" || rest[2] != "guests" || rest[3] == "" {
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口", "")
		return
	}
	id, err := strconv.ParseInt(rest[3], 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "访客 id 必须是名单里的数字 id", "")
		return
	}
	if len(rest) == 4 {
		a.guestDetail(w, r, id)
		return
	}
	if len(rest) == 7 {
		targetID, err := strconv.ParseInt(rest[5], 10, 64)
		if err != nil || targetID <= 0 || rest[4] != "merge-into" || rest[6] != "preview" {
			WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口", "")
			return
		}
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
				"合并影响预览用 GET /api/v1/guests/{b_id}/merge-into/{a_id}/preview")
			return
		}
		a.previewGuestMerge(w, r, id, targetID)
		return
	}
	action := rest[4]
	preview := len(rest) == 6 && rest[5] == "preview"
	if len(rest) > 6 || (len(rest) == 6 && !preview) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口", "")
		return
	}
	switch {
	case action == "home-share-text" && len(rest) == 5 && r.Method == http.MethodPost:
		a.copyGuestHomeText(w, r, id)
	case action == "reset-access-code" && preview && r.Method == http.MethodGet:
		a.previewGuestAccessCodeReset(w, r, id)
	case action == "reset-access-code" && len(rest) == 5 && r.Method == http.MethodPost:
		a.resetGuestAccessCodeForGuest(w, r, id)
	case action == "reset-home-key" && len(rest) == 5 && r.Method == http.MethodPost:
		a.resetGuestHomeKey(w, r, id)
	case action == "assign-links" && preview && r.Method == http.MethodGet:
		a.previewGuestLinkAssignment(w, r, id)
	case action == "assign-links" && len(rest) == 5 && r.Method == http.MethodPost:
		a.assignGuestLinks(w, r, id)
	case action == "rename" && len(rest) == 5 && r.Method == http.MethodPost:
		a.renameGuest(w, r, id)
	case action == "disable" && len(rest) == 5 && r.Method == http.MethodPost:
		a.setGuestDisabled(w, r, id, true)
	case action == "enable" && len(rest) == 5 && r.Method == http.MethodPost:
		a.setGuestDisabled(w, r, id, false)
	case action == "merge-into" && len(rest) == 5 && r.Method == http.MethodPost:
		a.mergeGuest(w, r, id)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			"访客操作接口：POST /api/v1/guests/{id}/home-share-text、"+
				"GET /api/v1/guests/{id}/reset-access-code/preview、"+
				"POST /api/v1/guests/{id}/reset-access-code、"+
				"POST /api/v1/guests/{id}/reset-home-key、POST /api/v1/guests/{id}/rename、"+
				"POST /api/v1/guests/{id}/disable、POST /api/v1/guests/{id}/enable、"+
				"GET /api/v1/guests/{id}/assign-links/preview、POST /api/v1/guests/{id}/assign-links、"+
				"POST /api/v1/guests/{b_id}/merge-into")
	}
}

// guestDetail 处理 GET /api/v1/guests/{id}：任何成员（含成员 Agent）都能查看，
// 工单与链接按调用者可见范围过滤。
func (a *API) guestDetail(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			"查看访客详情用 GET /api/v1/guests/{id}")
		return
	}
	viewer, ok := a.memberFromRequestFor(w, r, "访客不能查看部门访客详情")
	if !ok {
		return
	}
	detail, err := a.tickets.GuestDetail(r.Context(), viewer, id)
	switch {
	case errors.Is(err, tickets.ErrGuestNotFound):
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "没有这位访客", "")
	case err != nil:
		panic(err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"guest": newGuestView(detail.Guest), "tickets": newGuestDetailTicketViews(detail.Tickets),
		})
	}
}

type guestDetailTicketView struct {
	Number         string          `json:"number"`
	Title          string          `json:"title"`
	Status         string          `json:"status"`
	WaitingOnGuest bool            `json:"waiting_on_guest"`
	WaitingReason  string          `json:"waiting_reason"`
	API            string          `json:"api"`
	Links          []guestLinkView `json:"links"`
}

func newGuestDetailTicketViews(items []tickets.GuestDetailTicket) []guestDetailTicketView {
	views := make([]guestDetailTicketView, 0, len(items))
	for _, item := range items {
		links := make([]guestLinkView, 0, len(item.Links))
		for _, l := range item.Links {
			links = append(links, newGuestLinkView(l))
		}
		views = append(views, guestDetailTicketView{
			Number: item.Number, Title: item.Title, Status: item.Status,
			WaitingOnGuest: item.WaitingOnGuest, WaitingReason: item.WaitingReason,
			API: "/api/v1/tickets/" + item.Number, Links: links,
		})
	}
	return views
}

// renameGuest 处理 POST /api/v1/guests/{id}/rename：访客建立者、管理员或名下
// 任一链接的管理人可以改名，成员 Agent 同样可以代主人操作。
func (a *API) renameGuest(w http.ResponseWriter, r *http.Request, id int64) {
	viewer, ok := a.memberFromRequestFor(w, r, "访客不能给部门访客改名")
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req, `{"name": "检验科-张三"}`) {
		return
	}
	guest, err := a.tickets.RenameGuest(r.Context(), viewer, id, req.Name)
	switch {
	case errors.Is(err, tickets.ErrGuestNotFound):
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "没有这位访客", "")
	case errors.Is(err, tickets.ErrGuestNameRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeGuestNameRequired, err.Error(),
			"请填写去空白后的访客名字")
	case errors.Is(err, tickets.ErrGuestManagementNotAllowed):
		WriteError(w, http.StatusForbidden, CodeGuestManagementNotAllowed, err.Error(),
			"只有访客建立者、管理员或名下链接的管理人可以改名")
	case err != nil:
		panic(err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"guest": newGuestView(guest)})
	}
}

// setGuestDisabled 处理停用/恢复。停用等同名下链接全部失效但不改写各链接
// 的撤销与有效期字段；影响多张工单且难撤回，只接受网页成员会话。
func (a *API) setGuestDisabled(w http.ResponseWriter, r *http.Request, id int64, disable bool) {
	action := "停用"
	if !disable {
		action = "恢复"
	}
	viewer, ok := a.memberFromRequestFor(w, r, "访客不能"+action+"部门访客")
	if !ok {
		return
	}
	var err error
	if disable {
		err = a.tickets.DisableGuest(r.Context(), viewer, id)
	} else {
		err = a.tickets.EnableGuest(r.Context(), viewer, id)
	}
	switch {
	case errors.Is(err, tickets.ErrGuestNotFound):
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "没有这位访客", "")
	case errors.Is(err, tickets.ErrGuestAlreadyMerged):
		WriteError(w, http.StatusConflict, CodeGuestAlreadyMerged, err.Error(),
			"已并入的访客保留为历史身份，不能停用或恢复")
	case errors.Is(err, tickets.ErrGuestAlreadyDisabled):
		WriteError(w, http.StatusConflict, CodeGuestAlreadyDisabled, err.Error(), "访客已停用，无需重复停用")
	case errors.Is(err, tickets.ErrGuestNotDisabled):
		WriteError(w, http.StatusConflict, CodeGuestNotDisabled, err.Error(), "这位访客当前是正常状态")
	case errors.Is(err, tickets.ErrGuestManagementNotAllowed):
		if !viewer.IsWebSession() {
			WriteError(w, http.StatusForbidden, CodeWebSessionRequired, "停用和恢复访客只接受网页成员会话",
				"成员 Agent 不能代替主人停用或恢复访客")
			return
		}
		WriteError(w, http.StatusForbidden, CodeGuestManagementNotAllowed, err.Error(),
			"只有访客建立者、管理员或名下链接的管理人可以在网页上停用或恢复")
	case err != nil:
		panic(err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) copyGuestHomeText(w http.ResponseWriter, r *http.Request, id int64) {
	viewer, ok := a.memberFromRequestFor(w, r, "访客不能复制部门访客的主页文案")
	if !ok {
		return
	}
	share, err := a.tickets.GuestHomeShareForViewer(r.Context(), viewer, id)
	switch {
	case errors.Is(err, tickets.ErrGuestNotFound), errors.Is(err, tickets.ErrGuestDisabled):
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "没有这位访客，或这位访客已停用", "")
	case errors.Is(err, tickets.ErrGuestAlreadyMerged):
		WriteError(w, http.StatusNotFound, CodeGuestAlreadyMerged, err.Error(), "")
	case errors.Is(err, tickets.ErrGuestManagementNotAllowed):
		WriteError(w, http.StatusForbidden, CodeGuestManagementNotAllowed, err.Error(),
			"只有访客建立者、管理员或名下链接的管理人可以复制主页文案")
	case errors.Is(err, tickets.ErrGuestLinkCipherUnavailable):
		WriteError(w, http.StatusServiceUnavailable, CodeGuestLinkCipherUnavailable, err.Error(), "等服务端加密密钥可用后再复制")
	case errors.Is(err, tickets.ErrGuestShareTextUnavailable):
		WriteError(w, http.StatusConflict, CodeShareTextUnavailable, err.Error(), "主页密钥不可恢复，请联系管理员处理")
	case err != nil:
		panic(err)
	default:
		path := "/h/" + share.Key
		writeJSON(w, http.StatusOK, map[string]any{
			"home_url": path, "share_text": tickets.GuestHomeShareText(share.GuestName, a.absURL(path)),
		})
	}
}

func (a *API) previewGuestAccessCodeReset(w http.ResponseWriter, r *http.Request, id int64) {
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	preview, err := a.tickets.GuestAccessCodeResetPreviewForViewer(r.Context(), viewer, id)
	if !a.writeGuestResetError(w, err, "") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"guest_id": preview.GuestID, "guest_name": preview.GuestName,
		"device_count": preview.DeviceCount, "ticket_count": preview.TicketCount,
		"access_code_locked": preview.AccessCodeLocked,
	})
}

func (a *API) resetGuestAccessCodeForGuest(w http.ResponseWriter, r *http.Request, id int64) {
	w.Header().Set("Cache-Control", "no-store")
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		reset, err := a.tickets.ResetGuestAccessCodeForGuest(r.Context(), viewer, id)
		if !a.writeGuestResetError(w, err,
			"主页地址暂时不可恢复；请先重置主页地址，再重置访问码") {
			return
		}
		homePath := "/h/" + reset.HomeKey
		writeJSON(w, http.StatusOK, map[string]any{
			"guest_id":    reset.GuestID,
			"access_code": reset.AccessCode,
			"home_url":    homePath,
			"reset_text": tickets.GuestAccessCodeResetTextForGuest(
				reset.GuestName, a.absURL(homePath), reset.AccessCode),
		})
	})
}

func (a *API) resetGuestHomeKey(w http.ResponseWriter, r *http.Request, id int64) {
	w.Header().Set("Cache-Control", "no-store")
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		share, err := a.tickets.ResetGuestHomeKeyForViewer(r.Context(), viewer, id)
		if !a.writeGuestResetError(w, err, "") {
			return
		}
		path := "/h/" + share.Key
		writeJSON(w, http.StatusOK, map[string]any{
			"guest_id":   share.GuestID,
			"home_url":   path,
			"share_text": tickets.GuestHomeShareText(share.GuestName, a.absURL(path)),
		})
	})
}

type assignGuestLinksRequest struct {
	LinkIDs    []int64         `json:"link_ids"`
	AccessCode json.RawMessage `json:"access_code"`
}

// previewGuestLinkAssignment 处理 GET /api/v1/guests/{id}/assign-links/preview。
func (a *API) previewGuestLinkAssignment(w http.ResponseWriter, r *http.Request, id int64) {
	w.Header().Set("Cache-Control", "no-store")
	viewer, ok := a.memberFromRequestFor(w, r, "访客不能归属部门的老访客链接")
	if !ok {
		return
	}
	ids, ok := parseAssignLinkIDsQuery(w, r)
	if !ok {
		return
	}
	choice, ok := parseAssignLinkPreviewChoice(w, r)
	if !ok {
		return
	}
	preview, err := a.tickets.GuestLinkAssignmentPreview(r.Context(), viewer, id, ids, choice)
	if !a.writeGuestLinkAssignmentError(w, err) {
		return
	}
	body := map[string]any{
		"guest_id": preview.GuestID, "guest_name": preview.GuestName,
		"guest_claimed": preview.GuestClaimed, "link_count": preview.LinkCount,
		"ticket_count": preview.TicketCount, "claimed_link_count": preview.ClaimedLinkCount,
		"device_count": preview.DeviceCount, "access_code_action": preview.AccessCodeAction,
		"source_link_ids":          preview.SourceLinkIDs,
		"source_device_count":      preview.SourceDeviceCount,
		"invalidated_device_count": preview.InvalidatedDeviceCount,
	}
	if preview.SourceLinkID != 0 {
		body["source_link_id"] = preview.SourceLinkID
	}
	writeJSON(w, http.StatusOK, body)
}

// assignGuestLinks 处理 POST /api/v1/guests/{id}/assign-links。
func (a *API) assignGuestLinks(w http.ResponseWriter, r *http.Request, id int64) {
	w.Header().Set("Cache-Control", "no-store")
	viewer, ok := a.memberFromRequestFor(w, r, "访客不能归属部门的老访客链接")
	if !ok {
		return
	}
	var req assignGuestLinksRequest
	if !decodeJSON(w, r, &req,
		`{"link_ids":[1,2], "access_code": {"source_link_id":1} 或 "generate"}`) {
		return
	}
	choice, ok := parseGuestAccessCodeChoice(w, req.AccessCode)
	if !ok {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		result, err := a.tickets.AssignGuestLinks(r.Context(), viewer, id, req.LinkIDs, choice)
		if !a.writeGuestLinkAssignmentError(w, err) {
			return
		}
		links := make([]map[string]any, 0, len(result.Links))
		for _, link := range result.Links {
			links = append(links, map[string]any{
				"id": link.ID, "ticket_number": link.TicketNumber, "ticket_title": link.TicketTitle,
			})
		}
		body := map[string]any{
			"guest_id": result.GuestID, "guest_name": result.GuestName,
			"link_count": result.LinkCount, "ticket_count": result.TicketCount,
			"access_code_action": result.AccessCodeAction, "links": links,
		}
		if result.AccessCode != "" {
			body["access_code"] = result.AccessCode
		}
		writeJSON(w, http.StatusOK, body)
	})
}

func parseAssignLinkIDsQuery(w http.ResponseWriter, r *http.Request) ([]int64, bool) {
	query := r.URL.Query()
	if apiErr := rejectUnknownParams(query,
		[]string{"link_ids", "source_link_id", "access_code"}, ""); apiErr != nil {
		writeAPIError(w, apiErr)
		return nil, false
	}
	raw := strings.TrimSpace(query.Get("link_ids"))
	if raw == "" {
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidGuestLinkIDs,
			tickets.ErrInvalidGuestLinkIDs.Error(), "link_ids=1,2；空数组不能提交")
		return nil, false
	}
	parts := strings.Split(raw, ",")
	ids := make([]int64, 0, len(parts))
	seen := make(map[int64]bool, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			WriteError(w, http.StatusUnprocessableEntity, CodeInvalidGuestLinkIDs,
				tickets.ErrInvalidGuestLinkIDs.Error(), "link_ids 必须是逗号分隔的正整数，如 link_ids=1,2")
			return nil, false
		}
		if seen[id] {
			WriteError(w, http.StatusUnprocessableEntity, CodeInvalidGuestLinkIDs,
				tickets.ErrInvalidGuestLinkIDs.Error(), "link_ids 里的 id 不能重复")
			return nil, false
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, true
}

func parseAssignLinkPreviewChoice(w http.ResponseWriter, r *http.Request) (tickets.GuestAccessCodeChoice, bool) {
	query := r.URL.Query()
	rawSource := strings.TrimSpace(query.Get("source_link_id"))
	rawAccessCode := strings.TrimSpace(query.Get("access_code"))
	if rawSource == "" && rawAccessCode == "" {
		return tickets.GuestAccessCodeChoice{}, true
	}
	if rawSource != "" && rawAccessCode != "" {
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidAccessCodeChoice,
			"source_link_id 与 access_code=generate 只能二选一",
			"沿用访问码用 source_link_id=链接id；生成新码用 access_code=generate")
		return tickets.GuestAccessCodeChoice{}, false
	}
	if rawAccessCode != "" {
		if rawAccessCode != "generate" {
			WriteError(w, http.StatusUnprocessableEntity, CodeInvalidAccessCodeChoice,
				"预览的 access_code 只能是 generate", "生成新码用 access_code=generate；沿用访问码用 source_link_id=链接id")
			return tickets.GuestAccessCodeChoice{}, false
		}
		return tickets.GuestAccessCodeChoice{Provided: true, Generate: true}, true
	}
	id, err := strconv.ParseInt(rawSource, 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidAccessCodeChoice,
			"source_link_id 必须是正整数", "例如 source_link_id=12")
		return tickets.GuestAccessCodeChoice{}, false
	}
	return tickets.GuestAccessCodeChoice{Provided: true, SourceLinkID: id}, true
}

func parseGuestAccessCodeChoice(w http.ResponseWriter, raw json.RawMessage) (tickets.GuestAccessCodeChoice, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return tickets.GuestAccessCodeChoice{}, true
	}
	var generate string
	if err := json.Unmarshal(raw, &generate); err == nil {
		if generate == "generate" {
			return tickets.GuestAccessCodeChoice{Provided: true, Generate: true}, true
		}
	}
	var source struct {
		SourceLinkID int64 `json:"source_link_id"`
	}
	if err := json.Unmarshal(raw, &source); err == nil && source.SourceLinkID > 0 {
		return tickets.GuestAccessCodeChoice{Provided: true, SourceLinkID: source.SourceLinkID}, true
	}
	WriteError(w, http.StatusUnprocessableEntity, CodeInvalidAccessCodeChoice,
		"access_code 只能省略，或取 {source_link_id: 链接id} / \"generate\"",
		"未认领访客归入已认领链接时必须二选一；其余情形省略 access_code")
	return tickets.GuestAccessCodeChoice{}, false
}

func (a *API) writeGuestLinkAssignmentError(w http.ResponseWriter, err error) bool {
	var notAssignable *tickets.GuestLinkNotAssignableError
	switch {
	case errors.Is(err, tickets.ErrGuestNotFound):
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "没有这位访客", "")
	case errors.Is(err, tickets.ErrGuestDisabled):
		WriteError(w, http.StatusUnprocessableEntity, CodeGuestDisabled, err.Error(), "先恢复访客，再归属老链接")
	case errors.Is(err, tickets.ErrGuestAlreadyMerged):
		WriteError(w, http.StatusConflict, CodeGuestAlreadyMerged, err.Error(), "")
	case errors.Is(err, tickets.ErrInvalidGuestLinkIDs):
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidGuestLinkIDs, err.Error(),
			"link_ids 必须是非空、不重复的正整数数组")
	case errors.Is(err, tickets.ErrGuestLinkNotFound):
		WriteError(w, http.StatusNotFound, CodeGuestLinkNotFound, "没有这条可归属的访客链接",
			"重新读取 GET /api/v1/guest-links/unassigned，只选择调用者可见的无主老链接")
	case errors.Is(err, tickets.ErrGuestLinkAlreadyAssigned):
		WriteError(w, http.StatusConflict, CodeGuestLinkAlreadyAssigned, err.Error(),
			"这条链接已有归属；需要改归时等访客合并功能处理")
	case errors.As(err, &notAssignable):
		WriteError(w, http.StatusUnprocessableEntity, CodeGuestLinkNotAssignable, err.Error(),
			"批量归属前重新读取 GET /api/v1/guest-links/unassigned")
	case errors.Is(err, tickets.ErrGuestAccessCodeChoiceNotAllowed):
		WriteError(w, http.StatusUnprocessableEntity, CodeAccessCodeChoiceNotAllowed, err.Error(),
			"已认领访客保留现有访问码；所选链接都未认领时访客保持未认领，都应省略 access_code")
	case errors.Is(err, tickets.ErrGuestAccessCodeChoiceRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeAccessCodeChoiceRequired, err.Error(),
			"所选链接里有已认领链接，需要选择沿用哪条访问码或生成新码")
	case errors.Is(err, tickets.ErrGuestAccessCodeChoiceInvalid):
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidAccessCodeChoice, err.Error(),
			"重新读取 GET /api/v1/guest-links/unassigned，并从本批已认领链接中选择一条")
	case errors.Is(err, tickets.ErrGuestAccessCodeGenerateWebSessionRequired):
		WriteError(w, http.StatusForbidden, CodeWebSessionRequired,
			"生成新的访问码只接受网页成员会话",
			"Agent token 不能看到只显示一次的新访问码；请在网页上登录后操作（ADR 0003）")
	case err == nil:
		return true
	default:
		panic(err)
	}
	return false
}

func (a *API) previewGuestMerge(w http.ResponseWriter, r *http.Request, sourceID, targetID int64) {
	w.Header().Set("Cache-Control", "no-store")
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	preview, err := a.tickets.GuestMergePreviewForViewer(r.Context(), viewer, sourceID, targetID)
	if !a.writeGuestMergeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source_guest_id": preview.SourceGuestID, "source_guest_name": preview.SourceGuestName,
		"target_guest_id": preview.TargetGuestID, "target_guest_name": preview.TargetGuestName,
		"link_count": preview.LinkCount, "device_count": preview.DeviceCount,
	})
}

func (a *API) mergeGuest(w http.ResponseWriter, r *http.Request, sourceID int64) {
	w.Header().Set("Cache-Control", "no-store")
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	var req struct {
		TargetGuestID int64 `json:"target_guest_id"`
	}
	if !decodeJSON(w, r, &req, `{"target_guest_id": 2}`) {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		merged, err := a.tickets.MergeGuest(r.Context(), viewer, sourceID, req.TargetGuestID)
		if !a.writeGuestMergeError(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"source_guest_id": merged.SourceGuestID, "target_guest_id": merged.TargetGuestID,
			"link_count": merged.LinkCount, "device_count": merged.DeviceCount,
		})
	})
}

func (a *API) writeGuestMergeError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, tickets.ErrGuestNotFound):
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "没有这位访客", "")
	case errors.Is(err, tickets.ErrGuestMergeTargetRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeGuestMergeTargetRequired, err.Error(),
			"先选择一位未被停用、未并入别人的目标访客")
	case errors.Is(err, tickets.ErrGuestMergeSelf):
		WriteError(w, http.StatusConflict, CodeGuestMergeSelf, err.Error(), "")
	case errors.Is(err, tickets.ErrGuestAlreadyMerged):
		WriteError(w, http.StatusConflict, CodeGuestAlreadyMerged, err.Error(), "合并不能撤回")
	case errors.Is(err, tickets.ErrGuestMergeTargetMerged):
		WriteError(w, http.StatusConflict, CodeGuestMergeTargetMerged, err.Error(), "")
	case errors.Is(err, tickets.ErrGuestMergeTargetDisabled):
		WriteError(w, http.StatusConflict, CodeGuestMergeTargetDisabled, err.Error(), "先恢复目标访客，再执行合并")
	case errors.Is(err, tickets.ErrGuestManagementNotAllowed):
		WriteError(w, http.StatusForbidden, CodeGuestManagementNotAllowed, err.Error(),
			"只有访客建立者、管理员或名下链接的管理人，且须同时能管理两位访客")
	case err == nil:
		return true
	default:
		panic(err)
	}
	return false
}

// writeGuestResetError 统一映射访客级重置的管理与凭证错误；返回 false 表示
// 已写出响应。shareHint 传给 share_text_unavailable，两个接口的恢复建议不同。
func (a *API) writeGuestResetError(w http.ResponseWriter, err error, shareHint string) bool {
	switch {
	case errors.Is(err, tickets.ErrGuestNotFound), errors.Is(err, tickets.ErrGuestDisabled):
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "没有这位访客，或这位访客已停用", "")
	case errors.Is(err, tickets.ErrGuestAlreadyMerged):
		WriteError(w, http.StatusConflict, CodeGuestAlreadyMerged, err.Error(), "")
	case errors.Is(err, tickets.ErrGuestManagementNotAllowed):
		WriteError(w, http.StatusForbidden, CodeGuestManagementNotAllowed, err.Error(),
			"只有访客建立者、管理员或名下链接的管理人可以重置")
	case errors.Is(err, tickets.ErrGuestNotClaimed):
		WriteError(w, http.StatusConflict, CodeGuestNotClaimed, err.Error(),
			"等访客第一次打开主页或链接认领后再重置")
	case errors.Is(err, tickets.ErrGuestLinkCipherUnavailable):
		WriteError(w, http.StatusServiceUnavailable, CodeGuestLinkCipherUnavailable,
			err.Error(), "等服务端加密密钥可用后再重置")
	case errors.Is(err, tickets.ErrGuestShareTextUnavailable):
		hint := shareHint
		if hint == "" {
			hint = "主页密钥不可恢复，请联系管理员处理"
		}
		WriteError(w, http.StatusConflict, CodeShareTextUnavailable, err.Error(), hint)
	case err == nil:
		return true
	default:
		panic(err)
	}
	return false
}

func newGuestView(g tickets.GuestInfo) guestView {
	return guestView{
		ID: g.ID, Name: g.Name, DisabledAt: g.DisabledAt,
		MergedIntoGuestID: g.MergedIntoGuestID, MergedAt: g.MergedAt,
		ClaimedAt: g.ClaimedAt, ClaimName: g.ClaimName,
		AccessCodeLockedAt: g.AccessCodeLockedAt, LinkCount: g.LinkCount,
		PendingCount: g.PendingCount, LastContactAt: g.LastContactAt, DeviceCount: g.DeviceCount,
		CreatedBy: newAuthorView(g.CreatedBy, false), CreatedAt: g.CreatedAt,
	}
}
