package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

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

func (a *API) handleNotifications(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	if viewer.Kind == identity.ViewerGuest {
		WriteError(w, http.StatusForbidden, CodeMembersOnly, "通知接口仅限成员使用", "访客请继续使用工单链接和反馈入口")
		return
	}
	q := notifications.ListQuery{MemberID: viewer.Member.ID}
	if raw := r.URL.Query().Get("after"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			WriteError(w, http.StatusBadRequest, "invalid_after", "after 必须是非负整数", "after 使用上次响应返回的 cursor")
			return
		}
		q.After = &n
	}
	if raw := r.URL.Query().Get("before"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_before", "before 必须是整数", "before 使用本页最后一条通知的 id")
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
	}
	if q.Limit > 200 {
		q.Limit = 200
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
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	if viewer.Kind == identity.ViewerGuest {
		WriteError(w, http.StatusForbidden, CodeMembersOnly, "通知接口仅限成员使用", "访客请继续使用工单链接和反馈入口")
		return
	}
	out, err := a.notifications.Unread(r.Context(), viewer.Member.ID)
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"unread_count": out.Count, "latest_id": out.LatestID})
}

func newNotificationItemView(it notifications.Item) notificationItemView {
	readAt := ""
	if it.ReadAt != nil {
		readAt = it.ReadAt.Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	v := notificationItemView{
		ID: it.ID, Kind: it.Kind, Reason: it.Reason, Text: it.Text, Summary: it.Summary,
		Ticket: notificationTicketView{Number: it.Number, Title: it.Title, URL: "/t/" + it.Number},
		Event: notificationEventView{Seq: it.EventSeq, Type: it.EventType,
			Actor: notificationActorView{Kind: it.ActorKind, MemberID: it.ActorMemberID,
				AgentID: it.ActorAgentID, Name: it.ActorName}},
		ReadNew:   fmt.Sprintf("/api/v1/tickets/%s/timeline?after=%d", it.Number, it.EventSeq-1),
		ReadFull:  "/api/v1/tickets/" + it.Number + "?format=markdown",
		CreatedAt: it.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
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
