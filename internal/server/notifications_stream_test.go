package server_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test通知流首连只发送hello(t *testing.T) {
	w := newHandoffWorld(t)
	created := w.createHandoffTicket(t, w.webWang, "实时通知", "李工")
	li := notificationsOf(t, w, w.webLi, "")
	if len(li.Items) != 1 {
		t.Fatalf("李工通知数 = %d, 想 1", len(li.Items))
	}

	stream := w.s.OpenSSE(t, w.webLi, "")
	if got := stream.Response().Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, 想 text/event-stream", got)
	}
	if got := stream.Response().Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, 想 no-cache", got)
	}
	ev := stream.Event(t)
	var hello struct {
		UnreadCount int   `json:"unread_count"`
		LatestID    int64 `json:"latest_id"`
	}
	decodeSSEData(t, ev, &hello)
	if ev.Name != "hello" || ev.ID != "" || hello.UnreadCount != 1 || hello.LatestID != li.Items[0].ID {
		t.Fatalf("hello = %+v/%+v, 想 event=hello unread=1 latest=%d", ev, hello, li.Items[0].ID)
	}
	_ = created
}

func Test通知流按LastEventID升序补发后发送hello(t *testing.T) {
	w := newHandoffWorld(t)
	first := w.createHandoffTicket(t, w.webWang, "断线前", "李工").Number
	second := w.createHandoffTicket(t, w.webWang, "断线后", "李工").Number
	got := notificationsOf(t, w, w.webLi, "?after=0")
	if len(got.Items) != 2 {
		t.Fatalf("李工通知数 = %d, 想 2", len(got.Items))
	}
	firstID, secondID := got.Items[0].ID, got.Items[1].ID

	stream := w.s.OpenSSE(t, w.webLi, strconv.FormatInt(firstID, 10))
	replayed := stream.Event(t)
	var replayBody notificationBody
	decodeSSEData(t, replayed, &replayBody)
	if replayed.Name != "notification" || replayed.ID != strconv.FormatInt(secondID, 10) ||
		replayBody.ID != secondID || replayBody.Ticket.Number != second {
		t.Fatalf("补发事件 = %+v/%+v, 想 %d/%s", replayed, replayBody, secondID, second)
	}
	hello := stream.Event(t)
	var helloBody struct {
		UnreadCount int   `json:"unread_count"`
		LatestID    int64 `json:"latest_id"`
	}
	decodeSSEData(t, hello, &helloBody)
	if hello.Name != "hello" || helloBody.UnreadCount != 2 || helloBody.LatestID != secondID {
		t.Fatalf("补发后 hello = %+v/%+v, 想 unread=2 latest=%d", hello, helloBody, secondID)
	}
	_ = first
}

func Test新通知实时推送到成员的全部连接且不推给其他成员(t *testing.T) {
	w := newHandoffWorld(t)
	liFirst := w.s.OpenSSE(t, w.webLi, "")
	liSecond := w.s.OpenSSE(t, w.liA, "")
	zhao := w.s.OpenSSE(t, w.webZhao, "")
	for _, stream := range []*tbtest.SSEStream{liFirst, liSecond, zhao} {
		if ev := stream.Event(t); ev.Name != "hello" {
			t.Fatalf("首帧 = %q, 想 hello", ev.Name)
		}
	}

	created := w.createHandoffTicket(t, w.webWang, "实时指派", "李工")
	for i, stream := range []*tbtest.SSEStream{liFirst, liSecond} {
		ev := stream.Event(t)
		var body notificationBody
		decodeSSEData(t, ev, &body)
		if ev.Name != "notification" || ev.ID != strconv.FormatInt(body.ID, 10) ||
			body.Ticket.Number != created.Number || body.Reason != "assigned" {
			t.Fatalf("第 %d 条连接收到 = %+v/%+v", i+1, ev, body)
		}
	}
	zhao.ExpectNoEvent(t, 100*time.Millisecond)
}

func Test一处标已读向成员的其他连接广播read(t *testing.T) {
	w := newHandoffWorld(t)
	w.createHandoffTicket(t, w.webWang, "第一张", "李工")
	w.createHandoffTicket(t, w.webWang, "第二张", "李工")
	li := notificationsOf(t, w, w.webLi, "")
	if len(li.Items) != 2 {
		t.Fatalf("李工通知数 = %d, 想 2", len(li.Items))
	}
	first, second := w.s.OpenSSE(t, w.webLi, ""), w.s.OpenSSE(t, w.webLi, "")
	for _, stream := range []*tbtest.SSEStream{first, second} {
		if ev := stream.Event(t); ev.Name != "hello" {
			t.Fatalf("首帧 = %q, 想 hello", ev.Name)
		}
	}

	res := w.s.Do(t, w.webLi, http.MethodPost, "/api/v1/notifications/read",
		map[string]any{"ids": []int64{li.Items[1].ID}}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("标已读状态码 = %d, 想 200", res.StatusCode)
	}
	for i, stream := range []*tbtest.SSEStream{first, second} {
		ev := stream.Event(t)
		var body struct {
			IDs         []int64 `json:"ids"`
			UnreadCount int     `json:"unread_count"`
		}
		decodeSSEData(t, ev, &body)
		if ev.Name != "read" || ev.ID != "" || len(body.IDs) != 1 || body.IDs[0] != li.Items[1].ID ||
			body.UnreadCount != 1 {
			t.Fatalf("第 %d 条连接 read = %+v/%+v", i+1, ev, body)
		}
	}
	_ = second
}

func Test通知流按假时钟间隔发送保活注释(t *testing.T) {
	w := newHandoffWorld(t)
	stream := w.s.OpenSSE(t, w.webLi, "")
	if ev := stream.Event(t); ev.Name != "hello" {
		t.Fatalf("首帧 = %q, 想 hello", ev.Name)
	}

	w.s.Clock().Advance(25 * time.Second)
	ev := stream.Event(t)
	if ev.Name != "" || ev.ID != "" || ev.Comment != "ping" || len(ev.Data) != 0 {
		t.Fatalf("保活帧 = %+v, 想 : ping", ev)
	}
}

func Test访客订阅通知流被拒绝(t *testing.T) {
	w := newHandoffWorld(t)
	created := w.createHandoffTicket(t, w.webWang, "访客不能订阅", "李工")
	link := w.s.CreateGuestLink(t, w.webWang, created.Number, map[string]any{"note": "检验科-张三"})

	req, err := http.NewRequest(http.MethodGet, w.s.URL()+"/api/v1/notifications/stream", nil)
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	req.Header.Set("Authorization", "Bearer "+link.Key(t))
	res, err := w.s.GuestClient(link.Key(t)).Do(req)
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("访客订阅状态码 = %d, 想 403", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "members_only" {
		t.Fatalf("访客订阅错误码 = %q, 想 members_only", got.Error.Code)
	}
}

func Test服务停机时通知流正常结束(t *testing.T) {
	w := newHandoffWorld(t)
	stream := w.s.OpenSSE(t, w.webLi, "")
	if ev := stream.Event(t); ev.Name != "hello" {
		t.Fatalf("首帧 = %q, 想 hello", ev.Name)
	}

	w.s.Shutdown(t)
	stream.Closed(t)
}

func decodeSSEData(t testing.TB, ev tbtest.SSEEvent, out any) {
	t.Helper()
	if ev.Name == "" || len(ev.Data) == 0 {
		t.Fatalf("SSE 事件缺少 event 或 data：%+v", ev)
	}
	if err := json.Unmarshal(ev.Data, out); err != nil {
		t.Fatalf("解析 SSE data 失败：%v", err)
	}
}
