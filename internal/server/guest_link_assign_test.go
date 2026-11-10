package server_test

import (
	"net/http"
	"net/url"
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

type unassignedGuestLinkResponse struct {
	ID             int64   `json:"id"`
	TicketNumber   string  `json:"ticket_number"`
	TicketTitle    string  `json:"ticket_title"`
	Note           string  `json:"note"`
	Claimed        bool    `json:"claimed"`
	ClaimName      string  `json:"claim_name"`
	LastUsedAt     *string `json:"last_used_at"`
	PossiblePerson bool    `json:"possible_person"`
}

type unassignedGuestLinksResponse struct {
	Links []unassignedGuestLinkResponse `json:"links"`
}

type batchAssignGuestLinkItem struct {
	ID           int64  `json:"id"`
	TicketNumber string `json:"ticket_number"`
	TicketTitle  string `json:"ticket_title"`
}

type batchAssignGuestLinksResponse struct {
	GuestID          int64                      `json:"guest_id"`
	GuestName        string                     `json:"guest_name"`
	LinkCount        int                        `json:"link_count"`
	TicketCount      int                        `json:"ticket_count"`
	AccessCodeAction string                     `json:"access_code_action"`
	AccessCode       string                     `json:"access_code,omitempty"`
	Links            []batchAssignGuestLinkItem `json:"links"`
}

type batchAssignPreviewResponse struct {
	GuestID                int64   `json:"guest_id"`
	GuestName              string  `json:"guest_name"`
	GuestClaimed           bool    `json:"guest_claimed"`
	LinkCount              int     `json:"link_count"`
	TicketCount            int     `json:"ticket_count"`
	ClaimedLinkCount       int     `json:"claimed_link_count"`
	DeviceCount            int     `json:"device_count"`
	AccessCodeAction       string  `json:"access_code_action"`
	SourceLinkIDs          []int64 `json:"source_link_ids"`
	SourceLinkID           int64   `json:"source_link_id,omitempty"`
	SourceDeviceCount      int     `json:"source_device_count"`
	InvalidatedDeviceCount int     `json:"invalidated_device_count"`
}

func listUnassignedGuestLinks(t testing.TB, s *tbtest.Server, c *http.Client, query string) unassignedGuestLinksResponse {
	t.Helper()
	var got unassignedGuestLinksResponse
	res := s.Do(t, c, http.MethodGet, "/api/v1/guest-links/unassigned"+query, nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读取无主老链接状态码 = %d，想 200", res.StatusCode)
	}
	return got
}

func batchAssignGuestLinks(t testing.TB, s *tbtest.Server, c *http.Client, guestID int64,
	body map[string]any) (*http.Response, batchAssignGuestLinksResponse) {
	t.Helper()
	var got batchAssignGuestLinksResponse
	res := s.Do(t, c, http.MethodPost,
		"/api/v1/guests/"+itoa(guestID)+"/assign-links", body, nil)
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		tbtest.DecodeJSON(t, res, &got)
	}
	return res, got
}

func Test无主老链接列表只列可见有效无主并支持搜索和相近排序(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "li-pass-123")
	admin := s.Login("陈工", "secret-pass")
	web := s.Login("李工", "li-pass-123")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)

	wantedTicket := mustCreateTicket(t, s, web, "第一张老工单")
	wanted := s.CreateLegacyGuestLink(t, web, wantedTicket,
		map[string]any{"note": "检验科-张三"})
	sameSurnameTicket := mustCreateTicket(t, s, web, "同姓老工单")
	sameSurname := s.CreateLegacyGuestLink(t, web, sameSurnameTicket,
		map[string]any{"note": "财务处-张三丰"})
	otherTicket := mustCreateTicket(t, s, web, "第二张老工单")
	other := s.CreateLegacyGuestLink(t, web, otherTicket,
		map[string]any{"note": "财务处-李四"})

	s.Clock().Set(time.Date(2026, 10, 16, 10, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60)))
	claim := claimGuestLink(t, s, guestKeyOf(t, guestLinkResponse{ID: wanted.ID, URL: wanted.URL}),
		"检验科 张三")
	if res := s.Do(t, s.AgentClient(claim.DeviceToken), http.MethodGet,
		"/api/v1/tickets/"+wantedTicket, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("访客读取老工单状态码 = %d，想 200", res.StatusCode)
	}

	assignedTicket := mustCreateTicket(t, s, web, "已归属老工单")
	assigned := s.CreateLegacyGuestLink(t, web, assignedTicket, map[string]any{"note": "已归属"})
	if res := s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+assignedTicket+"/guest-links/"+itoa(assigned.ID)+"/assign-guest",
		map[string]any{"new_guest_name": "检验科-王五"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("预置已归属老链接状态码 = %d，想 200", res.StatusCode)
	}
	revokedTicket := mustCreateTicket(t, s, web, "已撤销老工单")
	revoked := s.CreateLegacyGuestLink(t, web, revokedTicket, map[string]any{"note": "已撤销"})
	if res := s.Do(t, web, http.MethodDelete,
		"/api/v1/tickets/"+revokedTicket+"/guest-links/"+itoa(revoked.ID), nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("预置撤销老链接状态码 = %d，想 204", res.StatusCode)
	}
	expiredTicket := mustCreateTicket(t, s, web, "已过期老工单")
	s.Clock().Set(time.Date(2026, 9, 1, 10, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60)))
	expired := s.CreateLegacyGuestLink(t, web, expiredTicket,
		map[string]any{"note": "已过期"})
	s.Clock().Set(time.Date(2026, 10, 18, 10, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60)))

	admin = s.Login("陈工", "secret-pass")
	invisibleTicket := mustCreateTicketAssignedTo(t, s, admin, "李工不可见老工单", "陈工")
	invisible := s.CreateLegacyGuestLink(t, admin, invisibleTicket, map[string]any{"note": "不可见"})

	got := listUnassignedGuestLinks(t, s, web, "")
	var ids []int64
	for _, link := range got.Links {
		ids = append(ids, link.ID)
		if link.ID == invisible.ID {
			t.Fatalf("列表包含不可见老链接 %d：%+v", invisible.ID, got.Links)
		}
		if link.ID == assigned.ID || link.ID == revoked.ID || link.ID == expired.ID {
			t.Fatalf("列表包含不可归属老链接 %d：%+v", link.ID, got.Links)
		}
	}
	for _, wantedID := range []int64{wanted.ID, sameSurname.ID, other.ID} {
		found := false
		for _, id := range ids {
			found = found || id == wantedID
		}
		if !found {
			t.Fatalf("列表缺少老链接 %d：%+v", wantedID, got.Links)
		}
	}
	if got.Links[0].ID != wanted.ID {
		t.Fatalf("默认排序第一条 = %d，想最近使用过的 %d", got.Links[0].ID, wanted.ID)
	}
	if got.Links[0].Claimed != true || got.Links[0].ClaimName != "检验科 张三" ||
		got.Links[0].LastUsedAt == nil {
		t.Fatalf("老链接认领与使用信息不对：%+v", got.Links[0])
	}
	if *got.Links[0].LastUsedAt != "2026-10-16T10:30:00+08:00" {
		t.Fatalf("最近使用时间 = %q，想访问记录时间", *got.Links[0].LastUsedAt)
	}

	got = listUnassignedGuestLinks(t, s, agent, "?q="+otherTicket)
	if len(got.Links) != 1 || got.Links[0].ID != other.ID ||
		got.Links[0].TicketNumber != otherTicket || got.Links[0].TicketTitle != "第二张老工单" {
		t.Fatalf("按工单编号搜索结果不对：%+v", got.Links)
	}

	got = listUnassignedGuestLinks(t, s, web, "?name="+url.QueryEscape("检验科-张三"))
	if len(got.Links) < 2 || got.Links[0].ID != wanted.ID ||
		got.Links[0].PossiblePerson != true || got.Links[0].TicketTitle != "第一张老工单" {
		t.Fatalf("按名字相近排序结果不对：%+v", got.Links)
	}
	for _, link := range got.Links {
		if link.ID == sameSurname.ID && !link.PossiblePerson {
			t.Fatalf("同姓老链接缺少可能是这个人标记：%+v", link)
		}
		if link.ID == other.ID && link.PossiblePerson {
			t.Fatalf("不同姓名老链接不应标记可能是这个人：%+v", link)
		}
	}
}

// 相近判定按 #337 的口径：只有访客名字去部门前缀；备注和自报称呼原样参与
// 包含判断，共享姓氏只用于自报称呼。
func Test无主老链接名字相近判定按规格口径(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	cases := []struct {
		title     string
		note      string
		claimName string
		possible  bool
	}{
		// 备注里的“张三-”是事由前缀，不是部门前缀；去掉后应仍能命中访客名字。
		{title: "复查报告", note: "张三-复查报告", claimName: "", possible: true},
		// 备注只和目标同姓时不算相近；共享姓氏只用于自报称呼。
		{title: "同姓备注", note: "张四的报告", claimName: "", possible: false},
		// 自报称呼和目标共享姓氏时算相近。
		{title: "同姓自称", note: "现场联系", claimName: "张四", possible: true},
		// 单字被包含时不标记，避免“张三”误命中所有单字简称。
		{title: "单字备注", note: "A", claimName: "", possible: false},
	}
	ids := map[string]int64{}
	for _, tc := range cases {
		ticket := mustCreateTicket(t, s, web, tc.title)
		link := s.CreateLegacyGuestLink(t, web, ticket, map[string]any{"note": tc.note})
		ids[tc.title] = link.ID
		if tc.claimName == "" {
			continue
		}
		claimGuestLink(t, s, guestKeyOf(t, guestLinkResponse{ID: link.ID, URL: link.URL}), tc.claimName)
	}

	got := listUnassignedGuestLinks(t, s, web, "?name="+url.QueryEscape("检验科-张三"))
	possibleByID := map[int64]bool{}
	for _, link := range got.Links {
		possibleByID[link.ID] = link.PossiblePerson
	}
	for _, tc := range cases {
		if got := possibleByID[ids[tc.title]]; got != tc.possible {
			t.Errorf("%s possible_person = %v，想 %v", tc.title, got, tc.possible)
		}
	}
}

// 批量归给已认领访客时保留访客自己的访问码与设备；所有老链接的旧凭证作废。
func Test批量归给已认领访客保留访客访问码(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)
	targetTicket := mustCreateTicket(t, s, web, "访客已有工单")
	targetLink := mustCreateGuestLink(t, s, web, targetTicket,
		map[string]any{"note": "访客链接", "new_guest_name": "检验科-张三"})
	guestID := listGuestLinks(t, s, web, targetTicket).Links[0].GuestID
	guestClaim := claimGuestLink(t, s, guestKeyOf(t, targetLink), "检验科 张三")

	oldTicketA := mustCreateTicket(t, s, web, "第一张老工单")
	oldA := s.CreateLegacyGuestLink(t, s.Login("陈工", "secret-pass"), oldTicketA,
		map[string]any{"note": "检验科-张三"})
	oldTicketB := mustCreateTicket(t, s, web, "第二张老工单")
	oldB := s.CreateLegacyGuestLink(t, web, oldTicketB, map[string]any{"note": "另一个老联系"})
	oldAClaim := claimGuestLink(t, s,
		guestKeyOf(t, guestLinkResponse{ID: oldA.ID, URL: oldA.URL}), "检验科 张三")

	s.Clock().Set(time.Date(2026, 10, 16, 11, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60)))
	var preview batchAssignPreviewResponse
	previewRes := s.Do(t, web, http.MethodGet,
		"/api/v1/guests/"+itoa(guestID)+"/assign-links/preview?link_ids="+itoa(oldA.ID)+","+itoa(oldB.ID), nil, nil)
	if previewRes.StatusCode != http.StatusOK {
		t.Fatalf("批量归属预览状态码 = %d，想 200", previewRes.StatusCode)
	}
	tbtest.DecodeJSON(t, previewRes, &preview)
	if preview.GuestID != guestID || preview.GuestName != "检验科-张三" || !preview.GuestClaimed ||
		preview.LinkCount != 2 || preview.TicketCount != 2 || preview.ClaimedLinkCount != 1 ||
		preview.DeviceCount != 1 || preview.AccessCodeAction != "keep_guest" ||
		len(preview.SourceLinkIDs) != 0 {
		t.Fatalf("批量归属预览 = %+v，想保留访客访问码并说明影响", preview)
	}

	res, result := batchAssignGuestLinks(t, s, agent, guestID,
		map[string]any{"link_ids": []int64{oldB.ID, oldA.ID}})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("批量归属状态码 = %d，想 200", res.StatusCode)
	}
	if result.GuestID != guestID || result.GuestName != "检验科-张三" ||
		result.LinkCount != 2 || result.TicketCount != 2 ||
		result.AccessCodeAction != "keep_guest" || len(result.Links) != 2 {
		t.Fatalf("批量归属响应 = %+v，想保留访客访问码并归入两条链接", result)
	}

	for _, ticket := range []string{oldTicketA, oldTicketB} {
		if res := s.Do(t, s.AgentClient(guestClaim.DeviceToken), http.MethodGet,
			"/api/v1/tickets/"+ticket, nil, nil); res.StatusCode != http.StatusOK {
			t.Fatalf("访客设备读取 %s 状态码 = %d，想 200", ticket, res.StatusCode)
		}
	}
	if res := s.Do(t, s.AgentClient(oldAClaim.DeviceToken), http.MethodGet,
		"/api/v1/tickets/"+oldTicketA, nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("老链接设备读取状态码 = %d，想 401", res.StatusCode)
	}
	var entered claimResult
	res = s.Do(t, s.AgentClient(guestKeyOf(t, guestLinkResponse{ID: oldB.ID, URL: oldB.URL})),
		http.MethodPost, "/api/v1/guest-links/enter",
		map[string]string{"access_code": guestClaim.AccessCode}, &entered)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("用访客访问码进入老链接状态码 = %d，想 201", res.StatusCode)
	}

	events := 0
	for _, ticket := range []string{oldTicketA, oldTicketB} {
		var view ticketResponseBody
		if res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+ticket, nil, &view); res.StatusCode != http.StatusOK {
			t.Fatalf("读取工单 %s 状态码 = %d，想 200", ticket, res.StatusCode)
		}
		count := 0
		for _, ev := range view.Timeline {
			if ev.Type != "guest_link_assigned" {
				continue
			}
			count++
			if ev.Note == "" || ev.To != "检验科-张三" || ev.Reason != "manual" {
				t.Fatalf("批量归属事件附加字段不对：%+v", ev)
			}
		}
		if count != 1 {
			t.Fatalf("工单 %s 的 guest_link_assigned 事件数 = %d，想 1", ticket, count)
		}
		events += count
	}
	if events != 2 {
		t.Fatalf("两条链接共写事件 %d 条，想 2", events)
	}
	var notices struct {
		Items []map[string]any `json:"items"`
	}
	if res := s.Do(t, web, http.MethodGet, "/api/v1/notifications", nil, &notices); res.StatusCode != http.StatusOK {
		t.Fatalf("读取通知状态码 = %d，想 200", res.StatusCode)
	}
	if len(notices.Items) != 0 {
		t.Fatalf("批量归属不应发通知：%+v", notices.Items)
	}
}

// 所选老链接都没被认领时不能带访问码选项，目标访客保持未认领。
func Test批量归入全未认领链接后访客保持未认领(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	targetTicket := mustCreateTicket(t, s, web, "访客已有工单")
	mustCreateGuestLink(t, s, web, targetTicket,
		map[string]any{"note": "访客链接", "new_guest_name": "检验科-张三"})
	guestID := listGuestLinks(t, s, web, targetTicket).Links[0].GuestID

	oldTicketA := mustCreateTicket(t, s, web, "第一张未认领老工单")
	oldA := s.CreateLegacyGuestLink(t, web, oldTicketA, map[string]any{"note": "老联系一"})
	oldTicketB := mustCreateTicket(t, s, web, "第二张未认领老工单")
	oldB := s.CreateLegacyGuestLink(t, web, oldTicketB, map[string]any{"note": "老联系二"})

	res, _ := batchAssignGuestLinks(t, s, web, guestID,
		map[string]any{"link_ids": []int64{oldA.ID, oldB.ID}, "access_code": "generate"})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("全未认领时带访问码选项状态码 = %d，想 422", res.StatusCode)
	}
	var before errorBody
	tbtest.DecodeJSON(t, res, &before)
	if before.Error.Code != "access_code_choice_not_allowed" {
		t.Fatalf("全未认领访问码错误码 = %q，想 access_code_choice_not_allowed", before.Error.Code)
	}

	res, result := batchAssignGuestLinks(t, s, web, guestID,
		map[string]any{"link_ids": []int64{oldA.ID, oldB.ID}})
	if res.StatusCode != http.StatusOK || result.AccessCodeAction != "remain_unclaimed" ||
		result.LinkCount != 2 || result.TicketCount != 2 {
		t.Fatalf("全未认领批量归属 = %d/%+v，想 200/remain_unclaimed", res.StatusCode, result)
	}
	var detail struct {
		Guest struct {
			ClaimedAt *string `json:"claimed_at"`
		} `json:"guest"`
	}
	res = s.Do(t, web, http.MethodGet, "/api/v1/guests/"+itoa(guestID), nil, &detail)
	if res.StatusCode != http.StatusOK || detail.Guest.ClaimedAt != nil {
		t.Fatalf("归属后访客认领状态 = %d/%+v，想未认领", res.StatusCode, detail.Guest)
	}
	var info struct {
		State string `json:"state"`
	}
	if res := s.Do(t, s.AgentClient(guestKeyOf(t, guestLinkResponse{ID: oldA.ID, URL: oldA.URL})),
		http.MethodGet, "/api/v1/guest-link", nil, &info); res.StatusCode != http.StatusOK || info.State != "unclaimed" {
		t.Fatalf("老链接状态 = %d/%q，想 200/unclaimed", res.StatusCode, info.State)
	}
}

// 批量归属先校验整批；任一链接不可归属时不改任何一条，也不写时间线。
func Test批量归属请求校验与整批原子性(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "li-pass-123")
	admin := s.Login("陈工", "secret-pass")
	web := s.Login("李工", "li-pass-123")
	targetTicket := mustCreateTicket(t, s, web, "访客已有工单")
	targetLink := mustCreateGuestLink(t, s, web, targetTicket,
		map[string]any{"note": "访客链接", "new_guest_name": "检验科-张三"})
	guestID := listGuestLinks(t, s, web, targetTicket).Links[0].GuestID
	claimGuestLink(t, s, guestKeyOf(t, targetLink), "检验科 张三")

	goodTicketA := mustCreateTicket(t, s, web, "可归属老工单一")
	goodA := s.CreateLegacyGuestLink(t, web, goodTicketA, map[string]any{"note": "老联系一"})
	goodTicketB := mustCreateTicket(t, s, web, "可归属老工单二")
	goodB := s.CreateLegacyGuestLink(t, web, goodTicketB, map[string]any{"note": "老联系二"})

	assignedTicket := mustCreateTicket(t, s, web, "已归属老工单")
	assigned := s.CreateLegacyGuestLink(t, web, assignedTicket, map[string]any{"note": "已归属"})
	if res := s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+assignedTicket+"/guest-links/"+itoa(assigned.ID)+"/assign-guest",
		map[string]any{"new_guest_name": "另一位访客"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("预置已归属链接状态码 = %d，想 200", res.StatusCode)
	}
	revokedTicket := mustCreateTicket(t, s, web, "已撤销老工单")
	revoked := s.CreateLegacyGuestLink(t, web, revokedTicket, map[string]any{"note": "已撤销"})
	if res := s.Do(t, web, http.MethodDelete,
		"/api/v1/tickets/"+revokedTicket+"/guest-links/"+itoa(revoked.ID), nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("预置撤销链接状态码 = %d，想 204", res.StatusCode)
	}
	s.Clock().Set(time.Date(2026, 9, 1, 10, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60)))
	expiredTicket := mustCreateTicket(t, s, web, "已过期老工单")
	expired := s.CreateLegacyGuestLink(t, web, expiredTicket, map[string]any{"note": "已过期"})
	s.Clock().Set(time.Date(2026, 10, 18, 10, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60)))
	invisibleTicket := mustCreateTicketAssignedTo(t, s, admin, "李工不可见老工单", "陈工")
	invisible := s.CreateLegacyGuestLink(t, admin, invisibleTicket, map[string]any{"note": "不可见"})

	for name, tc := range map[string]struct {
		id   int64
		code string
	}{
		"不可见": {invisible.ID, "guest_link_not_found"},
		"已归属": {assigned.ID, "guest_link_already_assigned"},
		"已撤销": {revoked.ID, "guest_link_not_assignable"},
		"已过期": {expired.ID, "guest_link_not_assignable"},
	} {
		res, _ := batchAssignGuestLinks(t, s, web, guestID,
			map[string]any{"link_ids": []int64{goodA.ID, tc.id, goodB.ID}})
		if res.StatusCode == http.StatusOK {
			t.Fatalf("[%s] 混入不可归属链接状态码 = 200，想失败", name)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		wantStatus := http.StatusUnprocessableEntity
		switch name {
		case "不可见":
			wantStatus = http.StatusNotFound
		case "已归属":
			wantStatus = http.StatusConflict
		}
		if res.StatusCode != wantStatus || got.Error.Code != tc.code {
			t.Fatalf("[%s] 状态/错误码 = %d/%q，想 %d/%q", name, res.StatusCode, got.Error.Code, wantStatus, tc.code)
		}
		links := listUnassignedGuestLinks(t, s, web, "").Links
		for _, link := range links {
			if link.ID == goodA.ID || link.ID == goodB.ID {
				continue
			}
			if link.ID == tc.id && name == "不可见" {
				t.Fatalf("不可见链接 %d 出现在成员列表中", tc.id)
			}
		}
		foundGood := 0
		for _, link := range links {
			if link.ID == goodA.ID || link.ID == goodB.ID {
				foundGood++
			}
		}
		if foundGood != 2 {
			t.Fatalf("[%s] 失败后可归属链接数 = %d，想 2：%+v", name, foundGood, links)
		}
		for _, ticket := range []string{goodTicketA, goodTicketB} {
			var view ticketResponseBody
			if res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+ticket, nil, &view); res.StatusCode != http.StatusOK {
				t.Fatalf("读取失败后工单状态码 = %d，想 200", res.StatusCode)
			}
			for _, ev := range view.Timeline {
				if ev.Type == "guest_link_assigned" {
					t.Fatalf("[%s] 整批失败仍写入归属事件：%+v", name, ev)
				}
			}
		}
	}

	for name, body := range map[string]map[string]any{
		"空链接":    {"link_ids": []int64{}},
		"重复链接":   {"link_ids": []int64{goodA.ID, goodA.ID}},
		"已认领带选项": {"link_ids": []int64{goodA.ID}, "access_code": map[string]any{"source_link_id": goodA.ID}},
	} {
		res, _ := batchAssignGuestLinks(t, s, web, guestID, body)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("[%s] 状态码 = %d，想 422", name, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		want := "invalid_link_ids"
		if name == "已认领带选项" {
			want = "access_code_choice_not_allowed"
		}
		if got.Error.Code != want {
			t.Fatalf("[%s] 错误码 = %q，想 %q", name, got.Error.Code, want)
		}
	}

	unclaimedTicket := mustCreateTicket(t, s, web, "未认领访客的工单")
	mustCreateGuestLink(t, s, web, unclaimedTicket,
		map[string]any{"note": "访客链接", "new_guest_name": "检验科-李四"})
	unclaimedGuestID := listGuestLinks(t, s, web, unclaimedTicket).Links[0].GuestID
	claimedOldTicket := mustCreateTicket(t, s, web, "已认领老工单")
	claimedOld := s.CreateLegacyGuestLink(t, web, claimedOldTicket, map[string]any{"note": "已认领老联系"})
	claimGuestLink(t, s, guestKeyOf(t, guestLinkResponse{ID: claimedOld.ID, URL: claimedOld.URL}), "检验科 李四")
	res, _ := batchAssignGuestLinks(t, s, web, unclaimedGuestID,
		map[string]any{"link_ids": []int64{goodA.ID, claimedOld.ID}})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("未认领访客混入已认领链接且缺少访问码选项状态码 = %d，想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "access_code_choice_required" {
		t.Fatalf("未认领访客缺少访问码选项错误码 = %q，想 access_code_choice_required", got.Error.Code)
	}
	links := listUnassignedGuestLinks(t, s, web, "").Links
	remaining := map[int64]bool{goodA.ID: false, claimedOld.ID: false}
	for _, link := range links {
		if _, ok := remaining[link.ID]; ok {
			remaining[link.ID] = true
		}
	}
	for id, ok := range remaining {
		if !ok {
			t.Fatalf("失败的未认领混合批次不应改动链接 %d：%+v", id, links)
		}
	}
}
