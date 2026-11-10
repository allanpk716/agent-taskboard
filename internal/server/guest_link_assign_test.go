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

	var view ticketResponseBody
	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+ticketA, nil, &view)
	found := false
	for _, ev := range view.Timeline {
		if ev.Type != "guest_link_assigned" {
			continue
		}
		found = true
		if ev.Actor.Member == nil || ev.Actor.Member.Name != "陈工" || ev.Actor.Agent != nil {
			t.Errorf("归属事件操作者不对：%+v", ev.Actor)
		}
		if ev.At != "2026-10-16T10:30:00+08:00" {
			t.Errorf("归属事件时间 = %q，想 2026-10-16T10:30:00+08:00", ev.At)
		}
		if ev.Note != "上线前的联系" || ev.To != "检验科-张三" || ev.Reason != "manual" {
			t.Errorf("归属事件附加字段 = note %q / to %q / reason %q", ev.Note, ev.To, ev.Reason)
		}
	}
	if !found {
		t.Fatalf("时间线缺少链接归属事件：%+v", view.Timeline)
	}

	var notices struct {
		Items []map[string]any `json:"items"`
	}
	if res := s.Do(t, web, http.MethodGet, "/api/v1/notifications", nil, &notices); res.StatusCode != http.StatusOK {
		t.Fatalf("读取通知状态码 = %d，想 200", res.StatusCode)
	}
	if len(notices.Items) != 0 {
		t.Fatalf("链接归属不应发通知：%+v", notices.Items)
	}
}

// 访客已认领时保留访客自己的访问码和设备；老链接原来的设备与访问码作废。
func Test已认领访客归入老链接后旧凭证作废(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	ticketA := mustCreateTicket(t, s, web, "老链接工单")
	ticketB := mustCreateTicket(t, s, web, "访客已有工单")

	legacy := s.CreateLegacyGuestLink(t, web, ticketA, map[string]any{"note": "上线前的联系"})
	legacyLink := guestLinkResponse{ID: legacy.ID, URL: legacy.URL, Note: legacy.Note,
		ExpiresAt: legacy.ExpiresAt, CanEditBrief: legacy.CanEditBrief}
	linkB := mustCreateGuestLink(t, s, web, ticketB,
		map[string]any{"note": "访客链接", "new_guest_name": "检验科-张三"})
	guestID := listGuestLinks(t, s, web, ticketB).Links[0].GuestID
	oldClaim := claimGuestLink(t, s, guestKeyOf(t, legacyLink), "老链接的人")
	guestClaim := claimGuestLink(t, s, guestKeyOf(t, linkB), "检验科 张三")

	res, _ := assignGuestLink(t, s, web, ticketA, legacy.ID, map[string]any{"guest_id": guestID})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("归属状态码 = %d，想 200", res.StatusCode)
	}

	if res := s.Do(t, s.AgentClient(oldClaim.DeviceToken), http.MethodGet,
		"/api/v1/tickets/"+ticketA, nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("老设备读取状态码 = %d，想 401", res.StatusCode)
	}
	if res := s.Do(t, s.AgentClient(guestKeyOf(t, legacyLink)), http.MethodPost,
		"/api/v1/guest-links/enter", map[string]string{"access_code": oldClaim.AccessCode}, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("老访问码进入状态码 = %d，想 401", res.StatusCode)
	}
	if res := s.Do(t, s.AgentClient(guestClaim.DeviceToken), http.MethodGet,
		"/api/v1/tickets/"+ticketA, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("访客设备读取老链接工单状态码 = %d，想 200", res.StatusCode)
	}
}

// 未认领的老链接归给已认领访客后不再有自己的认领流程，直接沿用访客凭证。
func Test未认领老链接归给已认领访客后沿用访客凭证(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	ticketA := mustCreateTicket(t, s, web, "未认领老链接")
	ticketB := mustCreateTicket(t, s, web, "访客已有工单")

	legacy := s.CreateLegacyGuestLink(t, web, ticketA, map[string]any{"note": "上线前的联系"})
	legacyLink := guestLinkResponse{ID: legacy.ID, URL: legacy.URL, Note: legacy.Note,
		ExpiresAt: legacy.ExpiresAt, CanEditBrief: legacy.CanEditBrief}
	linkB := mustCreateGuestLink(t, s, web, ticketB,
		map[string]any{"note": "访客链接", "new_guest_name": "检验科-张三"})
	guestID := listGuestLinks(t, s, web, ticketB).Links[0].GuestID
	guestClaim := claimGuestLink(t, s, guestKeyOf(t, linkB), "检验科 张三")

	res, _ := assignGuestLink(t, s, web, ticketA, legacy.ID, map[string]any{"guest_id": guestID})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("归属状态码 = %d，想 200", res.StatusCode)
	}

	var info struct {
		State string `json:"state"`
	}
	if res := s.Do(t, s.AgentClient(guestKeyOf(t, legacyLink)), http.MethodGet,
		"/api/v1/guest-link", nil, &info); res.StatusCode != http.StatusOK || info.State != "claimed" {
		t.Fatalf("归属后老链接状态 = %d/%q，想 200/claimed", res.StatusCode, info.State)
	}
	if res := s.Do(t, s.AgentClient(guestClaim.DeviceToken), http.MethodGet,
		"/api/v1/tickets/"+ticketA, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("访客设备读取老链接工单状态码 = %d，想 200", res.StatusCode)
	}
}

// 归属请求沿用建链接的访客二选一规则；已归属链接不能再次归属，访客凭证不能操作。
func Test归属访客请求校验与已归属冲突(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "li-pass-123")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)
	number := mustCreateTicket(t, s, web, "老链接归属校验")
	legacy := s.CreateLegacyGuestLink(t, web, number, map[string]any{"note": "上线前的联系"})
	li := s.Login("李工", "li-pass-123")
	res := s.Do(t, li, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+itoa(legacy.ID)+"/assign-guest",
		map[string]any{"new_guest_name": "检验科-张三"}, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("不可见成员归属状态码 = %d，想 404", res.StatusCode)
	}
	var invisible errorBody
	tbtest.DecodeJSON(t, res, &invisible)
	if invisible.Error.Code != "ticket_not_found" {
		t.Fatalf("不可见成员归属错误码 = %q，想 ticket_not_found", invisible.Error.Code)
	}

	for name, body := range map[string]map[string]any{
		"都不带": {},
		"都带":  {"guest_id": 1, "new_guest_name": "检验科-张三"},
		"空白":  {"new_guest_name": "  "},
		"不存在": {"guest_id": 999999},
	} {
		res = s.Do(t, web, http.MethodPost,
			"/api/v1/tickets/"+number+"/guest-links/"+itoa(legacy.ID)+"/assign-guest", body, nil)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("[%s] 状态码 = %d，想 422", name, res.StatusCode)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		want := "guest_required"
		if name == "不存在" {
			want = "guest_not_found"
		}
		if got.Error.Code != want {
			t.Errorf("[%s] 错误码 = %q，想 %q", name, got.Error.Code, want)
		}
	}

	legacyKey := guestLinkResponse{ID: legacy.ID, URL: legacy.URL}
	guestClaim := claimGuestLink(t, s, guestKeyOf(t, legacyKey), "检验科 张三")
	guest := s.AgentClient(guestClaim.DeviceToken)
	res = s.Do(t, guest, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+itoa(legacy.ID)+"/assign-guest",
		map[string]any{"new_guest_name": "检验科-张三"}, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("访客操作归属状态码 = %d，想 403", res.StatusCode)
	}

	res, assigned := assignGuestLink(t, s, agent, number, legacy.ID,
		map[string]any{"new_guest_name": "检验科-张三"})
	if res.StatusCode != http.StatusOK || assigned.GuestName != "检验科-张三" {
		t.Fatalf("新建访客并归属 = %d/%+v，想 200/检验科-张三", res.StatusCode, assigned)
	}
	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+itoa(legacy.ID)+"/assign-guest",
		map[string]any{"new_guest_name": "另一位"}, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("再次归属状态码 = %d，想 409", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "guest_link_already_assigned" {
		t.Fatalf("再次归属错误码 = %q，想 guest_link_already_assigned", got.Error.Code)
	}
}
