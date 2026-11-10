package server_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func renameGuest(t testing.TB, s *tbtest.Server, c *http.Client, guestID int64, name string) *http.Response {
	t.Helper()
	return s.Do(t, c, http.MethodPost, "/api/v1/guests/"+strconv.FormatInt(guestID, 10)+"/rename",
		map[string]string{"name": name}, nil)
}

type guestDetailLinkBody struct {
	ID           int64   `json:"id"`
	Note         string  `json:"note"`
	ExpiresAt    string  `json:"expires_at"`
	CanEditBrief bool    `json:"can_edit_brief"`
	RevokedAt    *string `json:"revoked_at"`
}

type guestDetailTicketBody struct {
	Number         string                `json:"number"`
	Title          string                `json:"title"`
	Status         string                `json:"status"`
	WaitingOnGuest bool                  `json:"waiting_on_guest"`
	WaitingReason  string                `json:"waiting_reason"`
	Links          []guestDetailLinkBody `json:"links"`
}

type guestDetailBody struct {
	Guest struct {
		ID          int64   `json:"id"`
		Name        string  `json:"name"`
		ClaimedAt   *string `json:"claimed_at"`
		ClaimName   string  `json:"claim_name"`
		DisabledAt  *string `json:"disabled_at"`
		LinkCount   int     `json:"link_count"`
		DeviceCount int     `json:"device_count"`
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

// 访客详情是档案页数据源（#338）：轮到访客的工单置顶并注明成员侧原因；
// 已登录设备数按访客级凭证统计，同一字段也供「访问」标签展示。
func Test访客详情轮到访客置顶原因与设备数(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	// 最旧的工单：可填写说明链接、尚无回复 → 轮到访客补全说明。
	fill := mustCreateTicket(t, s, web, "等待补全说明")
	fillLink := mustCreateGuestLink(t, s, web, fill, map[string]any{
		"note": "补全说明", "new_guest_name": "检验科-张三", "can_edit_brief": true,
	})
	// 较新的反馈工单：成员回复后轮到访客答复。
	var repliedCreated struct {
		Number string `json:"number"`
	}
	res := createWithGuestLink(t, s, web, map[string]any{
		"kind": "report", "title": "等待访客答复", "brief_md": "打印日志待补充",
		"labels":     []string{"机型:UD-2300"},
		"guest_link": map[string]any{"note": "追问日志", "guest_id": fillLink.GuestID},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建反馈工单状态码 = %d，想 201", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &repliedCreated)
	if res := reply(t, s, web, repliedCreated.Number, "请把打印日志发一下"); res.StatusCode != http.StatusCreated {
		t.Fatalf("成员回复状态码 = %d，想 201", res.StatusCode)
	}
	// 最新的工单已关闭：不轮到访客，也不应挤到置顶工单前面。
	closed := mustCreateTicket(t, s, web, "已关闭的旧工单")
	mustCreateGuestLink(t, s, web, closed, map[string]any{"note": "回看结果", "guest_id": fillLink.GuestID})
	if res := s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+closed+"/close",
		map[string]any{"reason": "done"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d，想 200", res.StatusCode)
	}
	// 访客认领一次（访客级凭证）→ 1 台已登录设备。
	var claim struct {
		DeviceToken string `json:"device_token"`
		AccessCode  string `json:"access_code"`
	}
	res = s.Do(t, s.AgentClient(guestKeyOf(t, fillLink)), http.MethodPost, "/api/v1/guest-links/claim",
		map[string]string{"author_name": "检验科 张三"}, &claim)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("访客认领状态码 = %d，想 201", res.StatusCode)
	}

	res, detail := fetchGuestDetail(t, s, web, fillLink.GuestID)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("成员查看访客详情状态码 = %d，想 200", res.StatusCode)
	}
	if detail.Guest.DeviceCount != 1 || detail.Guest.ClaimedAt == nil {
		t.Fatalf("访客 = %+v，想已认领且 1 台已登录设备", detail.Guest)
	}
	numbers := make([]string, 0, len(detail.Tickets))
	for _, item := range detail.Tickets {
		numbers = append(numbers, item.Number)
	}
	want := []string{repliedCreated.Number, fill, closed}
	if strings.Join(numbers, ",") != strings.Join(want, ",") {
		t.Fatalf("访客工单顺序 = %v，想轮到访客的置顶（新到旧）再已关闭：%v", numbers, want)
	}
	if !detail.Tickets[0].WaitingOnGuest || detail.Tickets[0].WaitingReason != "已回复，等待访客答复" {
		t.Fatalf("成员回复后的反馈工单 = %+v，想轮到访客并注明原因", detail.Tickets[0])
	}
	if !detail.Tickets[1].WaitingOnGuest || detail.Tickets[1].WaitingReason != "等待访客补全说明" {
		t.Fatalf("可填写说明工单 = %+v，想轮到访客并注明原因", detail.Tickets[1])
	}
	if detail.Tickets[2].WaitingOnGuest || detail.Tickets[2].WaitingReason != "" {
		t.Fatalf("已关闭工单 = %+v，不应轮到访客", detail.Tickets[2])
	}
}

// 改名只有访客建立者、管理员或名下链接的管理人可以操作；新回复记新名字，
// 历史署名保持改名当时的快照。
func Test给访客改名权限与历史署名(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	outsider := s.Login("王工", "secret-pass")

	number := mustCreateTicket(t, s, admin, "整理质控对照表")
	link := mustCreateGuestLink(t, s, admin, number,
		map[string]any{"note": "请补充", "new_guest_name": "检验科-张三"})
	guest := s.GuestClient(guestKeyOf(t, link))
	if res := s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "先看一下", "author_name": "张三"}, nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("改名前访客回复状态码 = %d，想 201", res.StatusCode)
	}

	if res := renameGuest(t, s, outsider, link.GuestID, "检验科-李四"); res.StatusCode != http.StatusForbidden {
		t.Fatalf("无关成员改名状态码 = %d，想 403", res.StatusCode)
	}
	var errBody errorBody
	res := renameGuest(t, s, admin, link.GuestID, "  ")
	tbtest.DecodeJSON(t, res, &errBody)
	if res.StatusCode != http.StatusUnprocessableEntity || errBody.Error.Code != "guest_name_required" {
		t.Fatalf("空白名字 = %d/%s，想 422 guest_name_required", res.StatusCode, errBody.Error.Code)
	}

	_, outsiderAgentToken := s.IssueAgentToken(t, outsider, "王工的Agent")
	res = renameGuest(t, s, s.AgentClient(outsiderAgentToken), link.GuestID, "检验科-李四")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("无关成员 Agent 改名状态码 = %d，想 403", res.StatusCode)
	}

	_, agentToken := s.IssueAgentToken(t, admin, "陈工的Agent")
	res = renameGuest(t, s, s.AgentClient(agentToken), link.GuestID, " 检验科-李四 ")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("链接管理人（含成员 Agent）改名状态码 = %d，想 200", res.StatusCode)
	}
	if res := s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "改完名字再回复", "author_name": "张三"}, nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("改名后访客回复状态码 = %d，想 201", res.StatusCode)
	}

	var tl timelineBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number+"/timeline", nil, &tl)
	if len(tl.Events) != 5 {
		t.Fatalf("改名不应写工单时间线，事件数 = %d，想 5：%+v", len(tl.Events), tl.Events)
	}
	var first, second *timelineEventBody
	for i := range tl.Events {
		e := tl.Events[i]
		if e.Type != "reply" {
			continue
		}
		if first == nil {
			first = &tl.Events[i]
		} else {
			second = &tl.Events[i]
		}
	}
	if first == nil || second == nil || first.Actor.GuestName == nil || second.Actor.GuestName == nil {
		t.Fatalf("两条访客回复应带访客名字快照：%+v", tl.Events)
	}
	if *first.Actor.GuestName != "检验科-张三" {
		t.Errorf("改名前的回复署名 = %q，想保留旧名 检验科-张三", *first.Actor.GuestName)
	}
	if *second.Actor.GuestName != "检验科-李四" {
		t.Errorf("改名后的回复署名 = %q，想用新名 检验科-李四", *second.Actor.GuestName)
	}
}

// 停用访客：设备凭证、链接、主页与两类 Agent 凭证全部被拒绝，需要访客
// 补全说明的工单回到指派人；受影响的工单写时间线、不发通知、不改写链接
// 的撤销与有效期。恢复后未过期链接重新可用，已过期仍然失效。
func Test停用恢复访客凭证失效与轮次回指派人(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	s.CreateMember("王工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	assignee := s.Login("李工", "secret-pass")
	outsider := s.Login("王工", "secret-pass")

	fillTicket := mustCreateTicketAssignedTo(t, s, admin, "请补全质控说明", "李工")
	fillLink := mustCreateGuestLink(t, s, admin, fillTicket,
		map[string]any{"note": "补全说明", "new_guest_name": "检验科-张三", "can_edit_brief": true})
	normalTicket := mustCreateTicket(t, s, admin, "打印机异常")
	normalLink := mustCreateGuestLink(t, s, admin, normalTicket,
		map[string]any{"note": "追问日志", "guest_id": fillLink.GuestID})
	expiringTicket := mustCreateTicket(t, s, admin, "短期联系")
	_ = mustCreateGuestLink(t, s, admin, expiringTicket, map[string]any{
		"note": "只到今天", "guest_id": fillLink.GuestID,
		"expires_at": s.Clock().Now().Add(time.Hour).Format(time.RFC3339Nano),
	})

	home := copyGuestHomeText(t, s, admin, fillLink.GuestID)
	homeKey := strings.TrimPrefix(home.URL, "/h/")
	var claim struct {
		DeviceToken string `json:"device_token"`
		AccessCode  string `json:"access_code"`
	}
	res := s.Do(t, s.AgentClient(homeKey), http.MethodPost, "/api/v1/guest-home/claim",
		map[string]string{"author_name": "检验科 张三"}, &claim)
	if res.StatusCode != http.StatusCreated || claim.DeviceToken == "" || claim.AccessCode == "" {
		t.Fatalf("认领访客主页 = %d/%+v，想 201 且带设备凭证与访问码", res.StatusCode, claim)
	}
	device := s.AgentClient(claim.DeviceToken)
	linkAgent := s.AgentClient(guestKeyOf(t, normalLink) + ":" + claim.AccessCode)
	homeAgent := s.AgentClient(homeKey + ":" + claim.AccessCode)
	if res := s.Do(t, device, http.MethodGet, "/api/v1/tickets/"+normalTicket, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("停用前设备凭证状态码 = %d，想 200", res.StatusCode)
	}

	_, assigneeAgent := s.IssueAgentToken(t, assignee, "李工的Agent")
	var beforeNotes notificationsBody
	s.Do(t, assignee, http.MethodGet, "/api/v1/notifications", nil, &beforeNotes)
	if res := setGuestDisabled(t, s, s.AgentClient(assigneeAgent), fillLink.GuestID, "disable"); res.StatusCode != http.StatusForbidden {
		t.Fatalf("成员 Agent 停用访客状态码 = %d，想 403", res.StatusCode)
	}
	if res := setGuestDisabled(t, s, outsider, fillLink.GuestID, "disable"); res.StatusCode != http.StatusForbidden {
		t.Fatalf("无关成员停用访客状态码 = %d，想 403", res.StatusCode)
	}
	if res := setGuestDisabled(t, s, assignee, fillLink.GuestID, "disable"); res.StatusCode != http.StatusNoContent {
		t.Fatalf("链接管理人停用访客状态码 = %d，想 204", res.StatusCode)
	}

	for name, c := range map[string]*http.Client{
		"设备凭证":    device,
		"链接密钥":    s.AgentClient(guestKeyOf(t, normalLink)),
		"主页密钥":    s.AgentClient(homeKey),
		"链接Agent": linkAgent,
		"主页Agent": homeAgent,
	} {
		if res := s.Do(t, c, http.MethodGet, "/api/v1/tickets/"+normalTicket, nil, nil); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("[%s] 停用后访问工单状态码 = %d，想 401", name, res.StatusCode)
		}
	}
	if res := s.Do(t, s.AgentClient(homeKey), http.MethodGet, "/api/v1/guest-home", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("停用后主页密钥访问列表状态码 = %d，想 401", res.StatusCode)
	}
	if res := s.Do(t, s.AgentClient(homeKey), http.MethodPost, "/api/v1/guest-home/enter",
		map[string]string{"access_code": claim.AccessCode}, nil); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("停用后凭访问码换设备状态码 = %d，想 401", res.StatusCode)
	}

	var fillView ticketResponseBody
	s.Do(t, assignee, http.MethodGet, "/api/v1/tickets/"+fillTicket, nil, &fillView)
	if fillView.WaitingOn.Party != "assignee" || fillView.WaitingOn.Member == nil ||
		*fillView.WaitingOn.Member != "李工" || fillView.WaitingOn.Why != "guest_not_completed" {
		t.Fatalf("停用后等待方 = %+v，想指派给 李工 且原因 guest_not_completed", fillView.WaitingOn)
	}
	var detail guestDetailBody
	s.Do(t, admin, http.MethodGet, "/api/v1/guests/"+strconv.FormatInt(fillLink.GuestID, 10), nil, &detail)
	if detail.Guest.DisabledAt == nil {
		t.Fatal("停用后的访客详情应带 disabled_at")
	}
	var links struct {
		Links []guestLinkResponse `json:"links"`
	}
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+fillTicket+"/guest-links", nil, &links)
	if len(links.Links) != 1 || links.Links[0].RevokedAt != nil {
		t.Fatalf("停用不应改写链接撤销字段：%+v", links.Links)
	}

	var disabledEvents []timelineEventBody
	for _, number := range []string{fillTicket, normalTicket, expiringTicket} {
		var tl timelineBody
		s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number+"/timeline", nil, &tl)
		for _, e := range tl.Events {
			if e.Type == "guest_disabled" {
				disabledEvents = append(disabledEvents, e)
			}
		}
	}
	if len(disabledEvents) != 3 {
		t.Fatalf("guest_disabled 事件 = %d 条，想 3（每张受影响工单一条）", len(disabledEvents))
	}
	for _, e := range disabledEvents {
		if e.Actor.Member == nil || e.Actor.Member.Name != "李工" || e.To != "检验科-张三" {
			t.Errorf("guest_disabled 事件 = %+v，想操作者 李工、访客 检验科-张三", e)
		}
	}
	var notes notificationsBody
	s.Do(t, assignee, http.MethodGet, "/api/v1/notifications", nil, &notes)
	if len(notes.Items) != len(beforeNotes.Items) {
		t.Fatalf("停用访客不应发通知：%+v", notes.Items)
	}

	s.Clock().Advance(2 * time.Hour)
	if res := setGuestDisabled(t, s, s.AgentClient(assigneeAgent), fillLink.GuestID, "enable"); res.StatusCode != http.StatusForbidden {
		t.Fatalf("成员 Agent 恢复访客状态码 = %d，想 403", res.StatusCode)
	}
	if res := setGuestDisabled(t, s, admin, fillLink.GuestID, "enable"); res.StatusCode != http.StatusNoContent {
		t.Fatalf("恢复访客状态码 = %d，想 204", res.StatusCode)
	}
	var enabledEvents []timelineEventBody
	for _, number := range []string{fillTicket, normalTicket, expiringTicket} {
		var tl timelineBody
		s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number+"/timeline", nil, &tl)
		for _, e := range tl.Events {
			if e.Type == "guest_enabled" {
				enabledEvents = append(enabledEvents, e)
			}
		}
	}
	if res := s.Do(t, device, http.MethodGet, "/api/v1/tickets/"+normalTicket, nil, nil); res.StatusCode != http.StatusOK {
		t.Errorf("恢复后未过期链接状态码 = %d，想 200", res.StatusCode)
	}
	s.Do(t, assignee, http.MethodGet, "/api/v1/tickets/"+fillTicket, nil, &fillView)
	if fillView.WaitingOn.Party != "guest" {
		t.Fatalf("恢复后等待方 = %+v，想 guest（请补全说明）", fillView.WaitingOn)
	}
	if res := s.Do(t, device, http.MethodGet, "/api/v1/tickets/"+expiringTicket, nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("恢复后已过期链接状态码 = %d，想 401", res.StatusCode)
	}
	if len(enabledEvents) != 2 {
		t.Fatalf("guest_enabled 事件 = %d 条，想 2（过期链接不再受恢复影响）", len(enabledEvents))
	}
}

// 已停用的访客不能再被新链接或老链接归属选中，避免成员误以为链接可用。
func Test已停用访客不能建链接或归属老链接(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	ticketA := mustCreateTicket(t, s, admin, "建立访客")
	link := mustCreateGuestLink(t, s, admin, ticketA,
		map[string]any{"note": "第一次联系", "new_guest_name": "检验科-张三"})
	if res := setGuestDisabled(t, s, admin, link.GuestID, "disable"); res.StatusCode != http.StatusNoContent {
		t.Fatalf("停用访客状态码 = %d, 想 204", res.StatusCode)
	}

	ticketB := mustCreateTicket(t, s, admin, "再建链接")
	res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets/"+ticketB+"/guest-links",
		map[string]any{"note": "第二次联系", "guest_id": link.GuestID}, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("给停用访客建链接状态码 = %d, 想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "guest_disabled" {
		t.Fatalf("给停用访客建链接错误码 = %q, 想 guest_disabled", got.Error.Code)
	}

	ticketC := mustCreateTicket(t, s, admin, "老链接归属")
	legacy := s.CreateLegacyGuestLink(t, admin, ticketC, map[string]any{"note": "上线前的联系"})
	res = s.Do(t, admin, http.MethodPost,
		"/api/v1/tickets/"+ticketC+"/guest-links/"+itoa(legacy.ID)+"/assign-guest",
		map[string]any{"guest_id": link.GuestID}, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("老链接归属停用访客状态码 = %d, 想 422", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "guest_disabled" {
		t.Fatalf("老链接归属停用访客错误码 = %q, 想 guest_disabled", got.Error.Code)
	}
}

// 已并入的访客是历史身份，不能变更停用状态。
func Test已并入访客不能停用或恢复(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	ticketA := mustCreateTicket(t, s, admin, "来源访客")
	source := mustCreateGuestLink(t, s, admin, ticketA,
		map[string]any{"note": "第一次联系", "new_guest_name": "检验科-张三"})
	ticketB := mustCreateTicket(t, s, admin, "目标访客")
	target := mustCreateGuestLink(t, s, admin, ticketB,
		map[string]any{"note": "第二次联系", "new_guest_name": "检验科-李四"})
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/guests/"+strconv.FormatInt(source.GuestID, 10)+"/merge-into",
		map[string]any{"target_guest_id": target.GuestID}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("合并访客状态码 = %d, 想 200", res.StatusCode)
	}

	for _, action := range []string{"disable", "enable"} {
		res := setGuestDisabled(t, s, admin, source.GuestID, action)
		if res.StatusCode != http.StatusConflict {
			t.Fatalf("已并入访客 %s 状态码 = %d, 想 409", action, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "guest_already_merged" {
			t.Fatalf("已并入访客 %s 错误码 = %q, 想 guest_already_merged", action, got.Error.Code)
		}
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

func setGuestDisabled(t testing.TB, s *tbtest.Server, c *http.Client, guestID int64, action string) *http.Response {
	t.Helper()
	return s.Do(t, c, http.MethodPost, "/api/v1/guests/"+strconv.FormatInt(guestID, 10)+"/"+action, nil, nil)
}
