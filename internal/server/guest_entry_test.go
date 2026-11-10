package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type guestListItem struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	LinkCount     int     `json:"link_count"`
	PendingCount  int     `json:"pending_count"`
	TicketCount   int     `json:"ticket_count"`
	LastContactAt *string `json:"last_contact_at"`
	LastSentAt    *string `json:"last_sent_at"`
}

func guestsByName(t testing.TB, s *tbtest.Server, c *http.Client) map[string]guestListItem {
	t.Helper()
	var got struct {
		Guests []guestListItem `json:"guests"`
	}
	if res := s.Do(t, c, http.MethodGet, "/api/v1/guests", nil, &got); res.StatusCode != http.StatusOK {
		t.Fatalf("列出访客名单状态码 = %d，想 200", res.StatusCode)
	}
	byName := make(map[string]guestListItem, len(got.Guests))
	for _, g := range got.Guests {
		byName[g.Name] = g
	}
	return byName
}

// 访客名单（#345）要支撑通讯录式展示：每行有待处理数（轮到访客的工单数），
// 最近联系的访客排横排；最近联系来自访客打开工单的访问记录。
func Test访客名单带最近联系与待处理数(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	// 检验科-张三名下两张都轮到访客；王五没有。
	pending := mustCreateTicket(t, s, web, "待访客补全的说明")
	link := mustCreateGuestLink(t, s, web, pending,
		map[string]any{"note": "补全说明", "new_guest_name": "检验科-张三", "can_edit_brief": true})
	alsoPending := mustCreateTicket(t, s, web, "另一张待补全")
	mustCreateGuestLink(t, s, web, alsoPending,
		map[string]any{"note": "再补一份", "guest_id": link.GuestID, "can_edit_brief": true})
	mustCreateGuestLink(t, s, web, mustCreateTicket(t, s, web, "另一位访客的工单"),
		map[string]any{"note": "设备科联系", "new_guest_name": "王五"})

	byName := guestsByName(t, s, web)
	if zhang := byName["检验科-张三"]; zhang.PendingCount != 2 {
		t.Fatalf("检验科-张三待处理数 = %d，想 2", zhang.PendingCount)
	}
	if wang := byName["王五"]; wang.PendingCount != 0 {
		t.Fatalf("王五待处理数 = %d，想 0", wang.PendingCount)
	}
	if byName["检验科-张三"].LastContactAt != nil || byName["王五"].LastContactAt != nil {
		t.Fatalf("访客还没打开过工单，不应有最近联系：%+v", byName)
	}

	// 访客打开一张工单 → 只有这位访客有最近联系。
	guest := s.GuestClient(guestKeyOf(t, link))
	if res := s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+pending, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("访客打开工单状态码 = %d，想 200", res.StatusCode)
	}
	byName = guestsByName(t, s, web)
	if byName["检验科-张三"].LastContactAt == nil {
		t.Fatal("访客打开工单后，名单应带最近联系时间")
	}
	if byName["王五"].LastContactAt != nil {
		t.Fatalf("王五没有访问记录，不应带最近联系：%+v", byName["王五"])
	}

	// 访客回复后那张工单轮到部门，待处理数减一（不是链接数）。
	if res := s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+pending+"/replies",
		map[string]string{"body_md": "已补全", "author_name": "张三"}, nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("访客回复状态码 = %d，想 201", res.StatusCode)
	}
	byName = guestsByName(t, s, web)
	if zhang := byName["检验科-张三"]; zhang.PendingCount != 1 || zhang.LinkCount != 2 {
		t.Fatalf("回复后张三待处理数/链接数 = %d/%d，想 1/2", zhang.PendingCount, zhang.LinkCount)
	}
}

// 待处理数按查看者可见范围过滤（ADR 0019）：部门共享的访客聚合不能用来
// 绕过工单可见范围。
func Test访客名单待处理数按查看者可见范围过滤(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "secret-pass")

	visible := mustCreateTicketAssignedTo(t, s, admin, "王工跟进的待补全", "王工")
	link := mustCreateGuestLink(t, s, admin, visible,
		map[string]any{"note": "补全说明", "new_guest_name": "检验科-张三", "can_edit_brief": true})
	invisible := mustCreateTicket(t, s, admin, "只有陈工可见的待补全")
	mustCreateGuestLink(t, s, admin, invisible,
		map[string]any{"note": "另补一份", "guest_id": link.GuestID, "can_edit_brief": true})

	if got := guestsByName(t, s, admin)["检验科-张三"].PendingCount; got != 2 {
		t.Fatalf("管理员看到的待处理数 = %d，想 2", got)
	}
	if got := guestsByName(t, s, member)["检验科-张三"].PendingCount; got != 1 {
		t.Fatalf("王工看到的待处理数 = %d，想 1（不可见工单不计入）", got)
	}
}

// “最近发过”与“最近联系”都不能借访客聚合绕过工单可见范围；前者在成员
// 刚建链接、访客尚未打开时就出现，供建单向导使用。
func Test访客名单最近发过与最近联系按可见范围过滤(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "secret-pass")

	beijing := time.FixedZone("UTC+8", 8*60*60)
	visible := mustCreateTicketAssignedTo(t, s, admin, "王工可见的访客工单", "王工")
	s.Clock().Set(time.Date(2026, 10, 16, 10, 0, 0, 0, beijing))
	visibleLink := mustCreateGuestLink(t, s, admin, visible,
		map[string]any{"note": "可见联系", "new_guest_name": "检验科-张三"})
	invisible := mustCreateTicket(t, s, admin, "只有陈工可见的访客工单")
	s.Clock().Set(time.Date(2026, 10, 16, 11, 0, 0, 0, beijing))
	invisibleLink := mustCreateGuestLink(t, s, admin, invisible,
		map[string]any{"note": "不可见联系", "guest_id": visibleLink.GuestID})
	s.Clock().Set(time.Date(2026, 10, 16, 11, 30, 0, 0, beijing))
	claim := claimGuestLink(t, s, guestKeyOf(t, guestLinkResponse{
		ID: invisibleLink.ID, URL: invisibleLink.URL,
	}), "检验科 张三")
	if res := s.Do(t, s.AgentClient(claim.DeviceToken), http.MethodGet,
		"/api/v1/tickets/"+invisible, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("访客打开不可见工单状态码 = %d，想 200", res.StatusCode)
	}

	adminGot := guestsByName(t, s, admin)["检验科-张三"]
	memberGot := guestsByName(t, s, member)["检验科-张三"]
	if adminGot.TicketCount != 2 || adminGot.LastSentAt == nil || adminGot.LastContactAt == nil {
		t.Fatalf("管理员访客活动统计 = %+v，想可见两张工单且带最近发送/联系", adminGot)
	}
	if memberGot.TicketCount != 1 || memberGot.LastSentAt == nil || memberGot.LastContactAt != nil {
		t.Fatalf("王工访客活动统计 = %+v，想只计可见工单的最近发送且不计不可见访问", memberGot)
	}
	if *memberGot.LastSentAt != "2026-10-16T10:00:00+08:00" {
		t.Fatalf("王工最近发送时间 = %q，想可见链接创建时间", *memberGot.LastSentAt)
	}
}
