package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/notifications"
)

type notificationItemView struct {
	ID        int64                  `json:"id"`
	Ticket    notificationTicketView `json:"ticket"`
	Kind      string                 `json:"kind"`
	Reason    string                 `json:"reason"`
	Text      string                 `json:"text"`
	Event     notificationEventView  `json:"event"`
	Summary   string                 `json:"summary"`
	ReadNew   string                 `json:"read_new"`
	ReadFull  string                 `json:"read_full"`
	CreatedAt string                 `json:"created_at"`
	ReadAt    *string                `json:"read_at"`
}

type notificationTicketView struct {
	Number string `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

type notificationEventView struct {
	Seq   int64                 `json:"seq"`
	Type  string                `json:"type"`
	Actor notificationActorView `json:"actor"`
}

type notificationActorView struct {
	Kind     string `json:"kind"`
	MemberID int64  `json:"member_id"`
	AgentID  int64  `json:"agent_id"`
	Name     string `json:"name"`
}

func (a *API) SetNotifications(m *notifications.Manager) { a.notifications = m }

func (a *API) notificationMember(w http.ResponseWriter, r *http.Request) (identity.Viewer, bool) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return identity.Viewer{}, false
	}
	if viewer.Kind == identity.ViewerGuest {
		WriteError(w, http.StatusForbidden, CodeMembersOnly, "通知接口仅限成员使用",
			"访客请继续使用工单链接和反馈入口")
		return identity.Viewer{}, false
	}
	return viewer, true
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

func (a *API) handleNotifications(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.notificationMember(w, r)
	if !ok {
		return
	}
	q := notifications.ListQuery{MemberID: viewer.Member.ID}
	if raw := r.URL.Query().Get("after"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			WriteError(w, http.StatusBadRequest, CodeInvalidAfter, "after 必须是非负整数", "after 使用上次响应返回的 cursor")
			return
		}
		q.After = &n
	}
	if raw := r.URL.Query().Get("before"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			WriteError(w, http.StatusBadRequest, CodeInvalidBefore, "before 必须是整数", "before 使用本页最后一条通知的 id")
			return
		}
		q.Before = &n
	}
	if r.URL.Query().Get("unread") == "1" {
		q.Unread = true
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			WriteError(w, http.StatusBadRequest, CodeInvalidLimit, "limit 必须是正整数", "不传时默认 50，服务端最大 200")
			return
		}
		q.Limit = n
		q.RequestedLimit = n
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	if raw := r.URL.Query().Get("format"); raw != "" {
		q.Format = raw
	}
	result, err := a.notifications.List(r.Context(), q)
	if err != nil {
		panic(err)
	}
	views := make([]notificationItemView, len(result.Items))
	for i, it := range result.Items {
		views[i] = newNotificationItemView(it)
	}
	body := map[string]any{
		"items": views, "cursor": result.Cursor,
		"unread_count": result.UnreadCount, "next": result.Next,
	}
	if r.URL.Query().Get("format") == "markdown" {
		md := make([]string, len(views))
		for i := range views {
			md[i] = notificationMarkdown(views[i])
		}
		body["items"] = strings.Join(md, "\n\n") + "\n\n下一步：下次用 after=" +
			strconv.FormatInt(result.Cursor, 10) + "；要看现在轮到谁，用 /api/v1/inbox。"
	}
	writeJSON(w, http.StatusOK, body)
}

func (a *API) handleNotificationsUnread(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.notificationMember(w, r)
	if !ok {
		return
	}
	out, err := a.notifications.Unread(r.Context(), viewer.Member.ID)
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"unread_count": out.Count, "latest_id": out.LatestID})
}

type markNotificationsReadRequest struct {
	IDs    *[]int64 `json:"ids"`
	Ticket *string  `json:"ticket"`
	UpTo   *int64   `json:"up_to"`
}

func (a *API) handleNotificationsRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	viewer, ok := a.notificationMember(w, r)
	if !ok {
		return
	}
	if viewer.Agent != nil {
		WriteError(w, http.StatusForbidden, CodeAgentCannotMarkRead,
			"成员 Agent 不能标通知已读", "已读状态保留给成员本人查看；Agent 只读取通知")
		return
	}
	var req markNotificationsReadRequest
	if !decodeJSON(w, r, &req, `{"ids": [1]} / {"ticket": "20261016-001"} / {"up_to": 3} 三选一`) {
		return
	}
	chosen := 0
	for _, present := range []bool{req.IDs != nil, req.Ticket != nil, req.UpTo != nil} {
		if present {
			chosen++
		}
	}
	if chosen != 1 {
		WriteError(w, http.StatusBadRequest, CodeInvalidReadRequest,
			"请求体必须正好包含 ids、ticket、up_to 中的一个", "三选一：按通知 id、按工单编号，或标到某个通知为止")
		return
	}
	target := notifications.ReadTarget{}
	if req.IDs != nil {
		if len(*req.IDs) == 0 {
			WriteError(w, http.StatusBadRequest, CodeInvalidReadRequest, "ids 不能为空", "至少带一个通知 id")
			return
		}
		if len(*req.IDs) > 500 {
			WriteError(w, http.StatusBadRequest, CodeTooManyNotificationIDs,
				"ids 最多 500 个", "分多次标已读，或改用 up_to / ticket")
			return
		}
		unique := make(map[int64]struct{}, len(*req.IDs))
		for _, id := range *req.IDs {
			if id <= 0 {
				WriteError(w, http.StatusBadRequest, CodeInvalidReadRequest, "ids 里的通知 id 必须是正整数", "")
				return
			}
			unique[id] = struct{}{}
		}
		target.IDs = make([]int64, 0, len(unique))
		for id := range unique {
			target.IDs = append(target.IDs, id)
		}
	} else if req.Ticket != nil {
		target.Ticket = strings.TrimSpace(*req.Ticket)
		if target.Ticket == "" {
			WriteError(w, http.StatusBadRequest, CodeInvalidReadRequest, "ticket 不能为空", "填工单编号，如 20261016-001")
			return
		}
	} else if *req.UpTo < 0 {
		WriteError(w, http.StatusBadRequest, CodeInvalidReadRequest, "up_to 必须是非负整数", "0 表示不标任何通知")
		return
	} else {
		target.UpTo = *req.UpTo
	}
	result, err := a.notifications.MarkRead(r.Context(), viewer.Member.ID, target)
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"marked": len(result.IDs), "unread_count": result.UnreadCount})
}

func newNotificationItemView(it notifications.Item) notificationItemView {
	readAt := ""
	if it.ReadAt != nil {
		readAt = formatTime(*it.ReadAt)
	}
	v := notificationItemView{
		ID: it.ID, Kind: it.Kind, Reason: it.Reason, Text: it.Text, Summary: it.Summary,
		Ticket: notificationTicketView{Number: it.Number, Title: it.Title, URL: "/t/" + it.Number},
		Event: notificationEventView{Seq: it.EventSeq, Type: it.EventType,
			Actor: notificationActorView{Kind: it.ActorKind, MemberID: it.ActorMemberID,
				AgentID: it.ActorAgentID, Name: it.ActorName}},
		ReadNew:   fmt.Sprintf("/api/v1/tickets/%s/timeline?after=%d", it.Number, it.EventSeq-1),
		ReadFull:  "/api/v1/tickets/" + it.Number + "?format=markdown",
		CreatedAt: formatTime(it.CreatedAt),
	}
	if readAt != "" {
		v.ReadAt = &readAt
	}
	return v
}

func notificationMarkdown(n notificationItemView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- #%d [%s/%s] %s（%s）\n  - 新事件：%s\n  - 全文：%s", n.ID, n.Kind, n.Reason, n.Text, n.CreatedAt, n.ReadNew, n.ReadFull)
	if n.Summary != "" {
		fmt.Fprintf(&b, "\n  - 摘要：%s", n.Summary)
	}
	return b.String()
}
