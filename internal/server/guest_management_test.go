package server_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type guestDetailLinkBody struct {
	ID           int64   `json:"id"`
	Note         string  `json:"note"`
	ExpiresAt    string  `json:"expires_at"`
	CanEditBrief bool    `json:"can_edit_brief"`
	RevokedAt    *string `json:"revoked_at"`
}

type guestDetailTicketBody struct {
	Number string                `json:"number"`
	Title  string                `json:"title"`
	Status string                `json:"status"`
	Links  []guestDetailLinkBody `json:"links"`
}

type guestDetailBody struct {
	Guest struct {
		ID         int64   `json:"id"`
		Name       string  `json:"name"`
		ClaimedAt  *string `json:"claimed_at"`
		ClaimName  string  `json:"claim_name"`
		DisabledAt *string `json:"disabled_at"`
		LinkCount  int     `json:"link_count"`
	} `json:"guest"`
	Tickets []guestDetailTicketBody `json:"tickets"`
}

func fetchGuestDetail(t testing.TB, s *tbtest.Server, c *http.Client, guestID int64) (*http.Response, guestDetailBody) {
	t.Helper()
	var body guestDetailBody
	res := s.Do(t, c, http.MethodGet, "/api/v1/guests/"+strconv.FormatInt(guestID, 10), nil, &body)
	return res, body
}

// 成员查看访客详情：名字、认领状态、名下工单和链接都按查看者可见范围过滤，
// 部门共享的访客聚合不能绕过工单可见范围。
func Test成员查看访客详情只列可见范围内工单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "secret-pass")

	visible := mustCreateTicketAssignedTo(t, s, admin, "王工可见的反馈", "王工")
	link := mustCreateGuestLink(t, s, admin, visible,
		map[string]any{"note": "补全说明", "new_guest_name": "检验科-张三", "can_edit_brief": true})
	invisible := mustCreateTicket(t, s, admin, "只有陈工可见")
	mustCreateGuestLink(t, s, admin, invisible,
		map[string]any{"note": "回看结果", "guest_id": link.GuestID})

	res, detail := fetchGuestDetail(t, s, member, link.GuestID)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("成员查看访客详情状态码 = %d，想 200", res.StatusCode)
	}
	if detail.Guest.ID != link.GuestID || detail.Guest.Name != "检验科-张三" {
		t.Fatalf("访客 = %+v，想 id=%d 名字 检验科-张三", detail.Guest, link.GuestID)
	}
	if detail.Guest.ClaimedAt != nil || detail.Guest.ClaimName != "" {
		t.Fatalf("未认领访客不应带认领信息：%+v", detail.Guest)
	}
	if detail.Guest.DisabledAt != nil {
		t.Fatalf("正常访客不应带 disabled_at：%+v", detail.Guest)
	}
	if len(detail.Tickets) != 1 || detail.Tickets[0].Number != visible {
		t.Fatalf("王工看到的访客工单 = %+v，想只有 %s", detail.Tickets, visible)
	}
	if len(detail.Tickets[0].Links) != 1 ||
		detail.Tickets[0].Links[0].ID != link.ID ||
		!detail.Tickets[0].Links[0].CanEditBrief {
		t.Fatalf("可见工单上的链接 = %+v，想带那条可填写说明链接", detail.Tickets[0].Links)
	}

	res, detail = fetchGuestDetail(t, s, admin, link.GuestID)
	if res.StatusCode != http.StatusOK || len(detail.Tickets) != 2 {
		t.Fatalf("管理员应看到访客全部工单：%d/%+v", res.StatusCode, detail.Tickets)
	}
	if detail.Guest.LinkCount != 2 {
		t.Fatalf("link_count = %d，想 2", detail.Guest.LinkCount)
	}

	if res := s.Do(t, s.NewClient(), http.MethodGet,
		"/api/v1/guests/"+strconv.FormatInt(link.GuestID, 10), nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("匿名查看部门访客详情状态码 = %d，想 401/403", res.StatusCode)
	}
}

// mustCreateTicketAssignedTo 建一张指派给指定成员的工单并返回编号。
func mustCreateTicketAssignedTo(t testing.TB, s *tbtest.Server, c *http.Client, title, assignee string) string {
	t.Helper()
	res := s.Do(t, c, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": title, "assignee": assignee, "labels": []string{"机型:UD-2300"},
	}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单 %q 状态码 = %d，想 201", title, res.StatusCode)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)
	return created.Number
}
