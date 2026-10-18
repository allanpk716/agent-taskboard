package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/notifications"
)

func (a *API) handleNotificationsStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	if viewer.Kind != identity.ViewerMember {
		WriteError(w, http.StatusForbidden, CodeMembersOnly, "通知接口仅限成员使用", "访客请继续使用工单链接和反馈入口")
		return
	}
	events, stop := a.notifications.Subscribe(viewer.Member.ID)
	defer stop()
	last := int64(0)
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			WriteError(w, http.StatusBadRequest, CodeInvalidLastEventID,
				"Last-Event-ID 必须是非负整数", "重连时带上最后收到的通知 id")
			return
		}
		last = n
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flush(w)
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		replay, err := a.notifications.List(r.Context(), notifications.ListQuery{
			MemberID: viewer.Member.ID, After: &last, Limit: -1,
		})
		if err != nil {
			panic(err)
		}
		for _, it := range replay.Items {
			writeSSEData(w, strconv.FormatInt(it.ID, 10), "notification", newNotificationItemView(it))
			if it.ID > last {
				last = it.ID
			}
		}
	}
	unread, err := a.notifications.Unread(r.Context(), viewer.Member.ID)
	if err != nil {
		panic(err)
	}
	if unread.LatestID > last {
		last = unread.LatestID
	}
	writeSSEData(w, "", "hello", map[string]any{
		"unread_count": unread.Count, "latest_id": last,
	})
	flush(w)
	flush(w)
	ping := a.notifications.After(notificationPingInterval)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping:
			if !a.streamViewerValid(r, viewer.Member.ID) {
				return
			}
			fmt.Fprint(w, ": ping\n\n")
			flush(w)
			ping = a.notifications.After(notificationPingInterval)
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Kind == notifications.EventKindDisconnect || !a.streamViewerValid(r, viewer.Member.ID) {
				return
			}
			switch ev.Kind {
			case notifications.EventKindNotify:
				replay, err := a.notifications.List(r.Context(), notifications.ListQuery{
					MemberID: viewer.Member.ID, After: &last, Limit: -1,
				})
				if err != nil {
					panic(err)
				}
				for _, it := range replay.Items {
					writeSSEData(w, strconv.FormatInt(it.ID, 10), "notification", newNotificationItemView(it))
					last = it.ID
				}
				flush(w)
			case notifications.EventKindRead:
				writeSSEData(w, "", "read", map[string]any{
					"ids": ev.ReadIDs, "unread_count": ev.UnreadCount,
				})
				flush(w)
			}
		}
	}
}

const notificationPingInterval = 25 * time.Second

func (a *API) streamViewerValid(r *http.Request, memberID int64) bool {
	viewer, ok := a.viewerFromRequest(noopResponseWriter{}, r)
	return ok && viewer.Kind == identity.ViewerMember && viewer.Member.ID == memberID
}

type noopResponseWriter struct{}

func (noopResponseWriter) Header() http.Header       { return http.Header{} }
func (noopResponseWriter) Write([]byte) (int, error) { return 0, nil }
func (noopResponseWriter) WriteHeader(int)           {}

func writeSSEData(w http.ResponseWriter, id, event string, data any) {
	if id != "" {
		fmt.Fprintf(w, "id: %s\n", id)
	}
	fmt.Fprintf(w, "event: %s\ndata: ", event)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		panic(err)
	}
	fmt.Fprint(w, "\n")
}

func flush(w http.ResponseWriter) {
	_ = http.NewResponseController(w).Flush()
}
