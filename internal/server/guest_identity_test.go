package server_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// guestLinkListResponse 是成员读取访客链接列表的响应（只取本测试关心的字段）。
type guestLinkListResponse struct {
	Links []struct {
		ID        int64  `json:"id"`
		Note      string `json:"note"`
		GuestID   int64  `json:"guest_id"`
		GuestName string `json:"guest_name"`
	} `json:"links"`
}

func listGuestLinks(t testing.TB, s *tbtest.Server, c *http.Client, ref string) guestLinkListResponse {
	t.Helper()
	var got guestLinkListResponse
	res := s.Do(t, c, http.MethodGet, "/api/v1/tickets/"+ref+"/guest-links", nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读取访客链接列表状态码 = %d，想 200", res.StatusCode)
	}
	return got
}

// 建访客链接必须带“已有访客”或“新访客名字”之一：都不带或都带都返回 422；
// 带任一项时链接归该访客，成员侧列表能看到访客名字。
func Test建访客链接必须选择已有访客或新建访客(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "访客身份测试")

	for name, body := range map[string]map[string]any{
		"都不带":   {"note": "请补全说明"},
		"都带":    {"note": "请补全说明", "guest_id": 1, "new_guest_name": "检验科-张三"},
		"新名字空白": {"note": "请补全说明", "new_guest_name": "  "},
	} {
		res := createGuestLink(t, s, web, number, body)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("[%s] 状态码 = %d，想 422（body=%v）", name, res.StatusCode, body)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "guest_required" {
			t.Errorf("[%s] 错误码 = %q，想 guest_required", name, got.Error.Code)
		}
	}

	created := mustCreateGuestLink(t, s, web, number,
		map[string]any{"note": "请补全说明", "new_guest_name": "检验科-张三"})
	links := listGuestLinks(t, s, web, number)
	if len(links.Links) != 1 {
		t.Fatalf("链接列表有 %d 条，想 1：%+v", len(links.Links), links.Links)
	}
	if links.Links[0].GuestName != "检验科-张三" || links.Links[0].GuestID == 0 {
		t.Errorf("链接未归属新访客：%+v", links.Links[0])
	}
	if links.Links[0].Note != "请补全说明" {
		t.Errorf("链接备注 = %q，想保留用途备注", links.Links[0].Note)
	}

	again := mustCreateGuestLink(t, s, web, number,
		map[string]any{"note": "第二次联系", "guest_id": links.Links[0].GuestID})
	if again.ID == created.ID {
		t.Fatalf("第二次建链接返回了同一条链接：%+v", again)
	}
	links = listGuestLinks(t, s, web, number)
	if len(links.Links) != 2 {
		t.Fatalf("链接列表有 %d 条，想 2：%+v", len(links.Links), links.Links)
	}
	for _, l := range links.Links {
		if l.GuestName != "检验科-张三" {
			t.Errorf("链接 %d 的访客名 = %q，想 检验科-张三", l.ID, l.GuestName)
		}
	}

	res := createGuestLink(t, s, web, number, map[string]any{"note": "x", "guest_id": 999999})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("不存在的访客状态码 = %d，想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "guest_not_found" {
		t.Errorf("不存在的访客错误码 = %q，想 guest_not_found", got.Error.Code)
	}
}

// 成员和成员 Agent 都能列出部门访客名单并按名字搜索；访客凭证一律 403。
func Test成员与Agent能列出并搜索部门访客名单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "访客名单测试")
	mustCreateGuestLink(t, s, web, number, map[string]any{"note": "补全说明", "new_guest_name": "检验科-张三"})
	mustCreateGuestLink(t, s, web, number, map[string]any{"note": "追问进度", "new_guest_name": "设备科-王五"})
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)

	for name, c := range map[string]*http.Client{"成员": web, "成员Agent": agent} {
		var got struct {
			Guests []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"guests"`
		}
		res := s.Do(t, c, http.MethodGet, "/api/v1/guests", nil, &got)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("[%s] 列出访客状态码 = %d，想 200", name, res.StatusCode)
		}
		if len(got.Guests) != 2 {
			t.Fatalf("[%s] 访客有 %d 位，想 2：%+v", name, len(got.Guests), got.Guests)
		}
		if got.Guests[0].Name != "检验科-张三" || got.Guests[1].Name != "设备科-王五" {
			t.Errorf("[%s] 访客名单顺序不对：%+v", name, got.Guests)
		}

		res = s.Do(t, c, http.MethodGet, "/api/v1/guests?q=%E7%8E%8B%E4%BA%94", nil, &got)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("[%s] 搜索访客状态码 = %d，想 200", name, res.StatusCode)
		}
		if len(got.Guests) != 1 || got.Guests[0].Name != "设备科-王五" {
			t.Errorf("[%s] 搜索“王五”结果不对：%+v", name, got.Guests)
		}
	}

	guest := s.GuestClient(guestKeyOf(t, mustCreateGuestLink(t, s, web, number,
		map[string]any{"note": "访客自查", "new_guest_name": "检验科-李四"})))
	res := s.Do(t, guest, http.MethodGet, "/api/v1/guests", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("访客读取名单状态码 = %d，想 403", res.StatusCode)
	}
}

// claimResult 是认领与凭访问码进入的一次性响应。
type claimResult struct {
	DeviceToken string `json:"device_token"`
	AccessCode  string `json:"access_code"`
}

func claimGuestLink(t testing.TB, s *tbtest.Server, key, authorName string) claimResult {
	t.Helper()
	var got claimResult
	res := s.Do(t, s.AgentClient(key), http.MethodPost, "/api/v1/guest-links/claim",
		map[string]string{"author_name": authorName}, &got)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("认领访客链接状态码 = %d，想 201", res.StatusCode)
	}
	return got
}

func enterGuestAccessCode(t testing.TB, s *tbtest.Server, key, code string) *http.Response {
	t.Helper()
	return s.Do(t, s.AgentClient(key), http.MethodPost, "/api/v1/guest-links/enter",
		map[string]string{"access_code": code}, nil)
}

// 访客认领名下任一链接后，这台设备对同一访客的全部有效链接可信：
// 能直接读取、回复另一张工单；之后成员新加的链接也直接可信。
// 别的访客的链接仍要求认领；不在名下链接集合里的工单对外返回 404。
func Test访客认领一条链接后一码通行名下全部工单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	ticketA := mustCreateTicket(t, s, web, "第一张工单")
	ticketB := mustCreateTicket(t, s, web, "第二张工单")
	otherTicket := mustCreateTicket(t, s, web, "别的访客的工单")

	linkA := mustCreateGuestLink(t, s, web, ticketA,
		map[string]any{"note": "请补全说明", "new_guest_name": "检验科-张三"})
	guestID := listGuestLinks(t, s, web, ticketA).Links[0].GuestID
	other := mustCreateGuestLink(t, s, web, otherTicket,
		map[string]any{"note": "别人的链接", "new_guest_name": "检验科-李四"})

	claim := claimGuestLink(t, s, guestKeyOf(t, linkA), "检验科 张三")
	device := s.AgentClient(claim.DeviceToken)

	// 成员在认领之后新加的链接也直接可信。
	mustCreateGuestLink(t, s, web, ticketB, map[string]any{"note": "追问进度", "guest_id": guestID})
	var view ticketResponseBody
	res := s.Do(t, device, http.MethodGet, "/api/v1/tickets/"+ticketB, nil, &view)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("同访客另一张工单读取状态码 = %d，想 200", res.StatusCode)
	}
	if view.Title != "第二张工单" {
		t.Errorf("读到的工单标题 = %q，想 第二张工单", view.Title)
	}
	if res := reply(t, s, device, ticketB, "这台设备直接可信"); res.StatusCode != http.StatusCreated {
		t.Fatalf("同访客另一张工单回复状态码 = %d，想 201", res.StatusCode)
	}

	// 打开别的访客的链接仍要求认领。
	var info struct {
		State string `json:"state"`
	}
	res = s.Do(t, s.AgentClient(guestKeyOf(t, other)), http.MethodGet, "/api/v1/guest-link", nil, &info)
	if res.StatusCode != http.StatusOK || info.State != "unclaimed" {
		t.Fatalf("别的访客链接状态 = %d/%q，想 200/unclaimed", res.StatusCode, info.State)
	}
	// 名下没有链接的工单对外 404，与不存在不可区分。
	res = s.Do(t, device, http.MethodGet, "/api/v1/tickets/"+otherTicket, nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("名下无链接的工单状态码 = %d，想 404", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "ticket_not_found" {
		t.Errorf("名下无链接的错误码 = %q，想 ticket_not_found", got.Error.Code)
	}
}

// 换设备时在名下任一链接输入一次访问码，这台设备就对名下全部工单可信。
func Test新设备在任一链接输访问码后通行名下工单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	ticketA := mustCreateTicket(t, s, web, "换设备A")
	ticketB := mustCreateTicket(t, s, web, "换设备B")
	linkA := mustCreateGuestLink(t, s, web, ticketA,
		map[string]any{"note": "第一次联系", "new_guest_name": "检验科-张三"})
	guestID := listGuestLinks(t, s, web, ticketA).Links[0].GuestID
	linkB := mustCreateGuestLink(t, s, web, ticketB, map[string]any{"note": "第二次联系", "guest_id": guestID})

	claim := claimGuestLink(t, s, guestKeyOf(t, linkA), "检验科 张三")
	res := enterGuestAccessCode(t, s, guestKeyOf(t, linkB), claim.AccessCode)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("在链接 B 输访问码状态码 = %d，想 201", res.StatusCode)
	}
	var entered claimResult
	tbtest.DecodeJSON(t, res, &entered)
	if res := s.Do(t, s.AgentClient(entered.DeviceToken), http.MethodGet,
		"/api/v1/tickets/"+ticketA, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("新设备读取链接 A 的工单状态码 = %d，想 200", res.StatusCode)
	}
}

// 浏览器输码与访客 Agent 凭证共用访客级失败计数：满 5 次后这个访客的
// 全部链接都不能再凭访问码进入。
func Test访客访问码浏览器与Agent共用失败计数满五次锁定全部链接(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	ticketA := mustCreateTicket(t, s, web, "锁定A")
	ticketB := mustCreateTicket(t, s, web, "锁定B")
	linkA := mustCreateGuestLink(t, s, web, ticketA,
		map[string]any{"note": "第一张", "new_guest_name": "检验科-张三"})
	guestID := listGuestLinks(t, s, web, ticketA).Links[0].GuestID
	linkB := mustCreateGuestLink(t, s, web, ticketB, map[string]any{"note": "第二张", "guest_id": guestID})

	claim := claimGuestLink(t, s, guestKeyOf(t, linkA), "检验科 张三")
	for i := 0; i < 4; i++ {
		if res := enterGuestAccessCode(t, s, guestKeyOf(t, linkB), "000000"); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误访问码状态码 = %d，想 401", i+1, res.StatusCode)
		}
	}
	agentCredential := guestKeyOf(t, linkA) + ":000000"
	if res := s.Do(t, s.AgentClient(agentCredential), http.MethodGet,
		"/api/v1/tickets/"+ticketA, nil, nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("第 5 次错误访问码（Agent）状态码 = %d，想 403", res.StatusCode)
	}
	res := enterGuestAccessCode(t, s, guestKeyOf(t, linkB), claim.AccessCode)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("锁定后正确访问码状态码 = %d，想 403", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "access_code_locked" {
		t.Errorf("锁定错误码 = %q，想 access_code_locked", got.Error.Code)
	}
}

// 同一访客在同一张工单上有多条有效链接时按权限最大者处理。
func Test同一工单多条访客链接按权限最大者处理(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "多条链接权限")
	plain := mustCreateGuestLink(t, s, web, number,
		map[string]any{"note": "只读", "new_guest_name": "检验科-张三"})
	guestID := listGuestLinks(t, s, web, number).Links[0].GuestID
	mustCreateGuestLink(t, s, web, number,
		map[string]any{"note": "可填写", "guest_id": guestID, "can_edit_brief": true})

	claim := claimGuestLink(t, s, guestKeyOf(t, plain), "检验科 张三")
	var view struct {
		Viewer struct {
			Can struct {
				EditBrief bool `json:"edit_brief"`
			} `json:"can"`
		} `json:"viewer"`
	}
	res := s.Do(t, s.AgentClient(claim.DeviceToken), http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读取工单状态码 = %d，想 200", res.StatusCode)
	}
	if !view.Viewer.Can.EditBrief {
		t.Error("多条链接应按权限最大者处理：viewer.can.edit_brief 应为 true")
	}
}

// 访客作者的留痕带访客名字快照：回复的时间线记录访客名字，访问记录与
// 收件箱的往来对象/触发者显示访客名字；无主老链接仍显示链接备注。
func Test访客回复与访问记录显示访客名字(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	web := s.Login("陈工", "secret-pass")
	wang := s.Login("王工", "member-pass")
	var created feedbackCreateResponse
	res := createWithGuestLink(t, s, web, map[string]any{
		"kind": "report", "title": "生化仪打印异常", "labels": []string{}, "assignee": "王工",
		"guest_link": map[string]any{
			"note": "请补充打印日志", "new_guest_name": "检验科-张三", "can_edit_brief": true,
		},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d，想 201", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &created)
	claim := claimGuestLink(t, s, strings.TrimPrefix(created.GuestLink.URL, "/g/"), "检验科 张三")
	guest := s.AgentClient(claim.DeviceToken)
	if got := s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, nil); got.StatusCode != http.StatusOK {
		t.Fatalf("访客读取工单状态码 = %d，想 200", got.StatusCode)
	}
	res = s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+created.Number+"/replies",
		map[string]string{"body_md": "日志稍后传上来", "author_name": "检验科 张三"}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("访客回复状态码 = %d，想 201", res.StatusCode)
	}

	var view ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &view)
	foundReply := false
	for _, ev := range view.Timeline {
		if ev.Type != "reply" || ev.Actor.Member != nil {
			continue
		}
		foundReply = true
		if ev.Actor.GuestName == nil || *ev.Actor.GuestName != "检验科-张三" {
			t.Errorf("回复未记录访客名字快照：%+v", ev.Actor)
		}
		if ev.Actor.LinkNote == nil || *ev.Actor.LinkNote != "请补充打印日志" {
			t.Errorf("回复应保留链接备注快照：%+v", ev.Actor)
		}
		if ev.Actor.AuthorName == nil || *ev.Actor.AuthorName != "检验科 张三" {
			t.Errorf("回复应保留自报署名：%+v", ev.Actor)
		}
	}
	if !foundReply {
		t.Fatalf("时间线缺少访客回复：%+v", view.Timeline)
	}

	records := readAccessRecords(t, s, web, created.Number)
	foundView := false
	for _, r := range records.Records {
		if r.Action != "view" || r.Actor.Kind != "guest" {
			continue
		}
		foundView = true
		if !strings.Contains(r.Actor.Name, "检验科-张三") {
			t.Errorf("访问记录应以访客名字记录：%+v", r.Actor)
		}
		if r.Actor.LinkNote != "请补充打印日志" {
			t.Errorf("访问记录应保留链接备注：%+v", r.Actor)
		}
	}
	if !foundView {
		t.Fatalf("缺少访客查看记录：%+v", records.Records)
	}

	var inbox inboxBody
	if got := s.Do(t, wang, http.MethodGet, "/api/v1/inbox", nil, &inbox); got.StatusCode != http.StatusOK {
		t.Fatalf("读取收件箱状态码 = %d，想 200", got.StatusCode)
	}
	item := inbox.itemByNumber(created.Number)
	if item == nil {
		t.Fatalf("王工信箱缺少工单 %s：%+v", created.Number, inbox.Items)
	}
	if item.Counterpart.Type != "guest" || item.Counterpart.Name == nil || *item.Counterpart.Name != "检验科-张三" {
		t.Errorf("收件箱往来对象 = %+v，想 guest/检验科-张三", item.Counterpart)
	}
	if item.Trigger.Type != "guest" || item.Trigger.Name != "检验科-张三" {
		t.Errorf("收件箱触发者 = %+v，想 guest/检验科-张三", item.Trigger)
	}
}

// 无主老链接（反馈入口上线前的数据）的留痕行为不变：仍显示链接备注。
func Test无主老链接留痕仍显示链接备注(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})
	submitted := submitToIntake(t, s, "bug", map[string]any{
		"title": "入口老链接", "fields": bugFields(), "author_name": "赵六",
	})
	guest := s.AgentClient(submitted.DeviceToken)
	if got := s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+submitted.Ticket.Number, nil, nil); got.StatusCode != http.StatusOK {
		t.Fatalf("老链接读取工单状态码 = %d，想 200", got.StatusCode)
	}
	if res := reply(t, s, guest, submitted.Ticket.Number, "这是老链接的回复"); res.StatusCode != http.StatusCreated {
		t.Fatalf("老链接回复状态码 = %d，想 201", res.StatusCode)
	}

	var view ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+submitted.Ticket.Number, nil, &view)
	for _, ev := range view.Timeline {
		if ev.Type != "reply" || ev.Actor.Member != nil {
			continue
		}
		if ev.Actor.GuestName != nil && *ev.Actor.GuestName != "" {
			t.Errorf("无主链接不应有访客名字快照：%+v", ev.Actor)
		}
		if ev.Actor.LinkNote == nil || *ev.Actor.LinkNote != submitted.GuestLink.Note {
			t.Errorf("无主链接应保留备注快照：%+v", ev.Actor)
		}
	}
	records := readAccessRecords(t, s, web, submitted.Ticket.Number)
	found := false
	for _, r := range records.Records {
		if r.Action != "view" || r.Actor.Kind != "guest" {
			continue
		}
		found = true
		if r.Actor.Name != submitted.GuestLink.Note+" · 赵六" {
			t.Errorf("老链接访问记录署名 = %q，想 %q", r.Actor.Name, submitted.GuestLink.Note+" · 赵六")
		}
	}
	if !found {
		t.Fatalf("缺少老链接查看记录：%+v", records.Records)
	}
}

// 建单时顺带建访客链接同样必须带访客（guest_id 或 new_guest_name 二选一）。
func Test建单顺带建访客链接必须选择访客(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	res := createWithGuestLink(t, s, web, map[string]any{
		"kind": "report", "title": "缺访客的建单", "labels": []string{},
		"guest_link": map[string]any{"note": "请补充", "can_edit_brief": true},
	})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("缺访客建单状态码 = %d，想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "guest_required" {
		t.Errorf("错误码 = %q，想 guest_required", got.Error.Code)
	}
}

// 有主链接的重置语义：重置链接只换地址，不重置访客级访问码、不作废设备；
// 链接级重置访问码只保留给无主老链接，有主链接返回明确错误。
func Test有主链接重置链接不重置访客访问码(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "重置语义")
	link := mustCreateGuestLink(t, s, web, number,
		map[string]any{"note": "现场支持", "new_guest_name": "检验科-张三"})
	claim := claimGuestLink(t, s, guestKeyOf(t, link), "检验科 张三")

	var rotated struct {
		URL        string `json:"url"`
		AccessCode string `json:"access_code"`
	}
	res := s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10)+"/rotate-key",
		nil, &rotated)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("重置链接状态码 = %d，想 200", res.StatusCode)
	}
	if rotated.AccessCode != "" {
		t.Errorf("重置链接不应返回新访问码：%q", rotated.AccessCode)
	}
	if got := s.Do(t, s.AgentClient(claim.DeviceToken), http.MethodGet,
		"/api/v1/tickets/"+number, nil, nil); got.StatusCode != http.StatusOK {
		t.Fatalf("重置链接后原设备应仍可信，状态码 = %d，想 200", got.StatusCode)
	}

	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10)+"/reset-access-code", nil, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("有主链接重置访问码状态码 = %d，想 409", res.StatusCode)
	}
	var err errorBody
	tbtest.DecodeJSON(t, res, &err)
	if err.Error.Code != "guest_access_code_guest_level" {
		t.Errorf("有主链接重置访问码错误码 = %q，想 guest_access_code_guest_level", err.Error.Code)
	}
}

// 有主链接的转发文案称呼访客名字（备注只写用途）；无主老链接仍称呼备注。
func Test有主链接转发文案称呼访客名字(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "转发文案称呼")
	link := mustCreateGuestLink(t, s, web, number,
		map[string]any{"note": "请补充日志", "new_guest_name": "检验科-张三"})
	var got struct {
		ShareText string `json:"share_text"`
	}
	res := s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10)+"/share-text", nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("复制转发文案状态码 = %d，想 200", res.StatusCode)
	}
	if !strings.Contains(got.ShareText, "检验科-张三：") {
		t.Errorf("转发文案应称呼访客名字：%q", got.ShareText)
	}
}
