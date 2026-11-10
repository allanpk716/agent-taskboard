package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type assignGuestLinkResponse struct {
	ID        int64  `json:"id"`
	GuestID   int64  `json:"guest_id"`
	GuestName string `json:"guest_name"`
}

// 归属接口把无主老链接挂到目标访客，并返回归属后的链接视图。
func assignGuestLink(t testing.TB, s *tbtest.Server, c *http.Client, ref string, id int64,
	body map[string]any) (*http.Response, assignGuestLinkResponse) {
	t.Helper()
	var got assignGuestLinkResponse
	res := s.Do(t, c, http.MethodPost,
		"/api/v1/tickets/"+ref+"/guest-links/"+itoa(id)+"/assign-guest", body, &got)
	return res, got
}

// 访客未认领时，老链接的认领结果整体转给访客：老设备立即能访问名下另一张
// 工单，老访问码也变成这位访客的访问码。
func Test未认领访客归入已认领老链接后凭证整体转移(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	ticketA := mustCreateTicket(t, s, web, "老链接工单")
	ticketB := mustCreateTicket(t, s, web, "访客另一张工单")

	legacy := s.CreateLegacyGuestLink(t, web, ticketA, map[string]any{"note": "上线前的联系"})
	legacyLink := guestLinkResponse{ID: legacy.ID, URL: legacy.URL, Note: legacy.Note,
		ExpiresAt: legacy.ExpiresAt, CanEditBrief: legacy.CanEditBrief}
	linkB := mustCreateGuestLink(t, s, web, ticketB,
		map[string]any{"note": "新链接", "new_guest_name": "检验科-张三"})
	guestID := listGuestLinks(t, s, web, ticketB).Links[0].GuestID
	claim := claimGuestLink(t, s, guestKeyOf(t, legacyLink), "检验科 张三")

	s.Clock().Set(time.Date(2026, 10, 16, 10, 30, 0, 0, clock.Beijing))
	res, assigned := assignGuestLink(t, s, web, ticketA, legacy.ID,
		map[string]any{"guest_id": guestID})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("归属状态码 = %d，想 200", res.StatusCode)
	}
	if assigned.ID != legacy.ID || assigned.GuestID != guestID ||
		assigned.GuestName != "检验科-张三" {
		t.Fatalf("归属响应 = %+v，想链接归到检验科-张三", assigned)
	}

	if res := s.Do(t, s.AgentClient(claim.DeviceToken), http.MethodGet,
		"/api/v1/tickets/"+ticketB, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("老设备读取访客另一张工单状态码 = %d，想 200", res.StatusCode)
	}
	var entered claimResult
	res = s.Do(t, s.AgentClient(guestKeyOf(t, linkB)), http.MethodPost,
		"/api/v1/guest-links/enter", map[string]string{"access_code": claim.AccessCode}, &entered)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("用老访问码进入访客名下链接状态码 = %d，想 201", res.StatusCode)
	}
	if res := s.Do(t, s.AgentClient(entered.DeviceToken), http.MethodGet,
		"/api/v1/tickets/"+ticketA, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("凭老访问码换到的设备读取老链接工单状态码 = %d，想 200", res.StatusCode)
	}
}
