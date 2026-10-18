package server_test

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type notificationBody struct {
	ID        int64   `json:"id"`
	Kind      string  `json:"kind"`
	Reason    string  `json:"reason"`
	Text      string  `json:"text"`
	Summary   string  `json:"summary"`
	ReadNew   string  `json:"read_new"`
	ReadFull  string  `json:"read_full"`
	CreatedAt string  `json:"created_at"`
	ReadAt    *string `json:"read_at"`
	Ticket    struct {
		Number string `json:"number"`
		Title  string `json:"title"`
		URL    string `json:"url"`
	} `json:"ticket"`
	Event notificationEventBody `json:"event"`
}

type notificationEventBody struct {
	Seq   int64                 `json:"seq"`
	Type  string                `json:"type"`
	Actor notificationActorBody `json:"actor"`
}

type notificationActorBody struct {
	Kind     string `json:"kind"`
	MemberID int64  `json:"member_id"`
	AgentID  int64  `json:"agent_id"`
	Name     string `json:"name"`
}

type notificationsBody struct {
	Items       []notificationBody `json:"items"`
	Cursor      int64              `json:"cursor"`
	UnreadCount int                `json:"unread_count"`
	Next        string             `json:"next"`
}

func notificationsOf(t testing.TB, w *handoffWorld, c *http.Client, query string) notificationsBody {
	t.Helper()
	var body notificationsBody
	res := w.s.Do(t, c, http.MethodGet, "/api/v1/notifications"+query, nil, &body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /notifications%s 状态码 = %d, 想 200", query, res.StatusCode)
	}
	return body
}

func Test指派工单生成轮到我的通知并限制访客访问(t *testing.T) {
	w := newHandoffWorld(t)
	created := w.createHandoffTicket(t, w.wangA, "整理 Q3 质控规则对照表", "李工")

	li := notificationsOf(t, w, w.liA, "")
	if len(li.Items) != 1 {
		t.Fatalf("李工通知数 = %d, 想 1", len(li.Items))
	}
	n := li.Items[0]
	if n.Kind != "turn" || n.Reason != "assigned" {
		t.Errorf("kind/reason = %s/%s, 想 turn/assigned", n.Kind, n.Reason)
	}
	if want := "王工 的 Agent · 笔记本 把工单「整理 Q3 质控规则对照表」指派给你"; n.Text != want {
		t.Errorf("text = %q, 想 %q", n.Text, want)
	}
	if n.Summary != "" {
		t.Errorf("assigned 通知 summary = %q, 想为空", n.Summary)
	}
	if n.ReadNew != "/api/v1/tickets/"+created.Number+"/timeline?after=1" {
		t.Errorf("read_new = %q", n.ReadNew)
	}
	if n.ReadFull != "/api/v1/tickets/"+created.Number+"?format=markdown" {
		t.Errorf("read_full = %q", n.ReadFull)
	}
	if n.Ticket.Number != created.Number || n.Ticket.Title != "整理 Q3 质控规则对照表" || n.Ticket.URL != "/t/"+created.Number {
		t.Errorf("ticket = %+v", n.Ticket)
	}
	if n.Event.Type != "assigned" || n.Event.Actor.Kind != "agent" || n.Event.Actor.Name != "笔记本" {
		t.Errorf("event = %+v", n.Event)
	}
	if li.UnreadCount != 1 || li.Cursor != n.ID {
		t.Errorf("unread/cursor = %d/%d, 想 1/%d", li.UnreadCount, li.Cursor, n.ID)
	}

	wang := notificationsOf(t, w, w.webWang, "")
	if len(wang.Items) != 0 || wang.UnreadCount != 0 {
		t.Errorf("操作者本人的通知 = %+v", wang)
	}

	link := w.s.CreateGuestLink(t, w.webWang, created.Number, map[string]any{"note": "检验科-张三"})
	for _, path := range []string{"/api/v1/notifications", "/api/v1/notifications/unread"} {
		res := w.s.Do(t, w.s.GuestClient(link.Key(t)), http.MethodGet, path, nil, nil)
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("访客访问 %s 状态码 = %d, 想 403", path, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "members_only" {
			t.Errorf("访客访问 %s 错误码 = %q, 想 members_only", path, got.Error.Code)
		}
	}
}

func Test轮到我期间对方回复按原因生成通知(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createHandoffTicket(t, w.webWang, "整理对照表", "李工").Number

	assertLatest := func(note string, c *http.Client, want notificationBody) {
		t.Helper()
		got := notificationsOf(t, w, c, "")
		if len(got.Items) == 0 {
			t.Fatalf("%s：没有通知", note)
		}
		n := got.Items[0]
		if n.Kind != "turn" || n.Reason != want.Reason {
			t.Errorf("%s：kind/reason = %s/%s, 想 turn/%s", note, n.Kind, n.Reason, want.Reason)
		}
		if want.Text != "" && n.Text != want.Text {
			t.Errorf("%s：text = %q, 想 %q", note, n.Text, want.Text)
		}
		if want.Summary != "" && n.Summary != want.Summary {
			t.Errorf("%s：summary = %q, 想 %q", note, n.Summary, want.Summary)
		}
		if want.Event.Type != "" && n.Event.Type != want.Event.Type {
			t.Errorf("%s：event.type = %q, 想 %q", note, n.Event.Type, want.Event.Type)
		}
		wantReadNew := "/api/v1/tickets/" + number + "/timeline?after=" + strconv.FormatInt(want.Event.Seq-1, 10)
		if want.Event.Seq != 0 && n.ReadNew != wantReadNew {
			t.Errorf("%s：read_new = %q, 想 %q", note, n.ReadNew, wantReadNew)
		}
	}

	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "「适用机型」要细到软件版本吗？"}, nil)
	assertLatest("指派人提问", w.webWang, notificationBody{
		Reason: "assignee_replied", Text: "李工 的 Agent · 工位 回复了「整理对照表」",
		Summary: "「适用机型」要细到软件版本吗？", Event: notificationEventBody{Seq: 3, Type: "reply"},
	})

	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "不用，写到机型即可。"}, nil)
	assertLatest("创建者答复", w.liA, notificationBody{
		Reason: "creator_replied", Summary: "不用，写到机型即可。", Event: testNotificationEvent(4),
	})

	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "对照表已完成。", "deliver": true}, nil)
	assertLatest("交付", w.webWang, notificationBody{
		Reason: "delivered", Text: "李工 的 Agent · 工位 交付了「整理对照表」",
		Summary: "对照表已完成。", Event: testNotificationEvent(5),
	})

	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "漏了 R4s 规则，请补上。"}, nil)
	assertLatest("退回修改", w.liA, notificationBody{
		Reason: "returned", Summary: "漏了 R4s 规则，请补上。", Event: testNotificationEvent(6),
	})

	link := w.s.CreateGuestLink(t, w.webWang, number, map[string]any{"note": "检验科-张三"})
	w.s.Do(t, w.s.GuestClient(link.Key(t)), http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "今天又复现了一次。", "author_name": "张三"}, nil)
	assertLatest("访客回复", w.liA, notificationBody{
		Reason: "guest_replied", Summary: "今天又复现了一次。", Event: testNotificationEvent(8),
	})

	// 本人操作不通知自己；上面的王工普通回复只通知李工。
	wang := notificationsOf(t, w, w.webWang, "")
	for _, n := range wang.Items {
		if n.Reason == "creator_replied" || n.Reason == "returned" {
			t.Errorf("王工本人操作不应收到 %s：%+v", n.Reason, n)
		}
	}
}

func testNotificationEvent(seq int64) notificationEventBody {
	return notificationEventBody{Seq: seq, Type: "reply"}
}

func Test通知增量倒序未读和Markdown拉取(t *testing.T) {
	w := newHandoffWorld(t)
	first := w.createHandoffTicket(t, w.wangA, "第一张", "李工").Number
	second := w.createHandoffTicket(t, w.wangA, "第二张", "赵工").Number
	_ = second

	liFirst := notificationsOf(t, w, w.liA, "")
	zhaoSecond := notificationsOf(t, w, w.zhaoA, "")
	if len(liFirst.Items) != 1 || len(zhaoSecond.Items) != 1 {
		t.Fatalf("李工/赵工通知数 = %d/%d, 想 1/1", len(liFirst.Items), len(zhaoSecond.Items))
	}
	if zhaoSecond.Items[0].ID <= liFirst.Items[0].ID {
		t.Errorf("通知 id 应全局递增：李工 #%d，赵工 #%d", liFirst.Items[0].ID, zhaoSecond.Items[0].ID)
	}

	liAfter := notificationsOf(t, w, w.liA, "?after=0")
	if len(liAfter.Items) != 1 || liAfter.Cursor != liFirst.Items[0].ID {
		t.Fatalf("after=0 结果/cursor = %+v/%d", liAfter, liFirst.Items[0].ID)
	}
	caughtUp := notificationsOf(t, w, w.liA, "?after="+strconv.FormatInt(liAfter.Cursor, 10))
	if len(caughtUp.Items) != 0 || caughtUp.Cursor != liAfter.Cursor {
		t.Errorf("追平后 = %d 项 cursor=%d, 想 0 项 cursor=%d",
			len(caughtUp.Items), caughtUp.Cursor, liAfter.Cursor)
	}

	// 三条通知用于倒序、未读过滤和 limit；顺序按全局 id 记下来。
	third := w.createHandoffTicket(t, w.webLi, "第三张", "王工").Number
	_ = third
	_ = w.createHandoffTicket(t, w.webZhao, "第四张", "王工").Number
	_ = w.createHandoffTicket(t, w.liA, "第五张", "王工").Number
	wang := notificationsOf(t, w, w.webWang, "?limit=2")
	if len(wang.Items) != 2 || wang.Items[0].ID <= wang.Items[1].ID {
		t.Fatalf("倒序前两页 = %+v", wang.Items)
	}
	if wang.Next == "" {
		t.Fatal("还有通知时 next 不应为空")
	}
	older := notificationsOf(t, w, w.webWang, "?before="+strconv.FormatInt(wang.Items[0].ID, 10)+"&limit=1")
	if len(older.Items) != 1 || older.Items[0].ID >= wang.Items[0].ID {
		t.Fatalf("before 翻页 = %+v", older.Items)
	}
	if older.Next == "" {
		t.Fatal("翻页后仍有更早通知时 next 不应为空")
	}
	unread := notificationsOf(t, w, w.webWang, "?unread=1&limit=2")
	if len(unread.Items) != 2 || unread.UnreadCount != 3 {
		t.Errorf("unread 结果/总数 = %+v/%d, 想 2 项/3", unread.Items, unread.UnreadCount)
	}
	clamped := notificationsOf(t, w, w.webWang, "?limit=999")
	if len(clamped.Items) != 3 {
		t.Errorf("limit=999 应截到 200 并返回现有 3 条，实际 %d", len(clamped.Items))
	}
	for _, n := range clamped.Items {
		if n.ReadAt != nil {
			t.Errorf("新通知 read_at = %q, 想为空", *n.ReadAt)
		}
	}

	var unreadCount struct {
		UnreadCount int   `json:"unread_count"`
		LatestID    int64 `json:"latest_id"`
	}
	res := w.s.Do(t, w.liA, http.MethodGet, "/api/v1/notifications/unread", nil, &unreadCount)
	if res.StatusCode != http.StatusOK || unreadCount.UnreadCount != 1 || unreadCount.LatestID != liFirst.Items[0].ID {
		t.Errorf("李工 unread = %+v, 想 1/%d", unreadCount, liFirst.Items[0].ID)
	}

	res = w.s.Do(t, w.liA, http.MethodGet, "/api/v1/notifications?after=0&format=markdown", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("markdown 状态码 = %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	md := string(body)
	for _, want := range []string{first, "新事件：/api/v1/tickets/" + first + "/timeline?after=1", "下一步：下次用 after="} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown 缺少 %q：\n%s", want, md)
		}
	}
}

func Test成员Agent操作通知主人而本人自派自不通知(t *testing.T) {
	w := newHandoffWorld(t)
	byAgent := w.createHandoffTicket(t, w.liA, "Agent 指派回主人", "李工").Number
	got := notificationsOf(t, w, w.liA, "")
	if len(got.Items) != 1 {
		t.Fatalf("李工通知数 = %d, 想 1", len(got.Items))
	}
	n := got.Items[0]
	if n.Ticket.Number != byAgent || n.Kind != "turn" || n.Reason != "assigned" {
		t.Errorf("Agent 指派主人的通知 = %+v", n)
	}
	if n.Event.Actor.Kind != "agent" || n.Event.Actor.Name != "工位" {
		t.Errorf("事件操作者 = %+v, 想 Li 的 Agent 工位", n.Event.Actor)
	}

	w.createHandoffTicket(t, w.webLi, "本人自派自", "李工")
	got = notificationsOf(t, w, w.liA, "")
	if len(got.Items) != 1 {
		t.Errorf("本人自派自后通知数 = %d, 想 仍为 1", len(got.Items))
	}
}
