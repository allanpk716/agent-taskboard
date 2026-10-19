package server_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// guestLinkResponse 是 POST …/guest-links 创建成功的响应（201）。
type guestLinkResponse struct {
	ID           int64   `json:"id"`
	URL          string  `json:"url"`
	Note         string  `json:"note"`
	ExpiresAt    string  `json:"expires_at"`
	RevokedAt    *string `json:"revoked_at"`
	CanEditBrief bool    `json:"can_edit_brief"`
}

// createGuestLink 发一个创建访客链接的请求，返回原始响应。
func createGuestLink(t testing.TB, s *tbtest.Server, c *http.Client, ref string, body map[string]any) *http.Response {
	t.Helper()
	return s.Do(t, c, http.MethodPost, "/api/v1/tickets/"+ref+"/guest-links", body, nil)
}

// mustCreateGuestLink 创建一条访客链接并返回响应结构。
func mustCreateGuestLink(t testing.TB, s *tbtest.Server, c *http.Client, ref string, body map[string]any) guestLinkResponse {
	t.Helper()
	res := createGuestLink(t, s, c, ref, body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("创建访客链接状态码 = %d, 想 201（body=%v）", res.StatusCode, body)
	}
	var link guestLinkResponse
	tbtest.DecodeJSON(t, res, &link)
	return link
}

// guestKeyOf 从访客链接地址 /g/<密钥> 里取出密钥（密钥直接作 Bearer token）。
func guestKeyOf(t testing.TB, link guestLinkResponse) string {
	t.Helper()
	if !strings.HasPrefix(link.URL, "/g/") {
		t.Fatalf("访客链接地址 = %q, 想以 /g/ 开头", link.URL)
	}
	return strings.TrimPrefix(link.URL, "/g/")
}

// 成员为工单创建访客链接：默认 30 天，可选 7/30/90 天或指定日期，没有永久选项；
// 创建记入时间线（guest_link_created 事件带备注）。任何成员都能操作。
func Test成员创建访客链接默认三十天并记入时间线(t *testing.T) {
	s := tbtest.Start(t) // 假时钟起点：2026-10-16 09:00（北京时间）
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")

	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	if link.Note != "检验科-张三" {
		t.Errorf("note = %q, 想 检验科-张三", link.Note)
	}
	if !strings.HasPrefix(link.URL, "/g/tbg_") {
		t.Errorf("访客链接地址 = %q, 想形如 /g/tbg_…", link.URL)
	}
	// 默认 30 天：从创建时刻起算。
	if want := "2026-11-15T09:00:00+08:00"; link.ExpiresAt != want {
		t.Errorf("默认有效期 expires_at = %q, 想 %q", link.ExpiresAt, want)
	}
	if link.CanEditBrief {
		t.Error("默认不带「可填写说明」权限")
	}

	// 7、30、90 天与指定日期。
	for name, tc := range map[string]struct {
		body map[string]any
		want string
	}{
		"七天":   {map[string]any{"note": "设备科-王五", "valid_days": 7}, "2026-10-23T09:00:00+08:00"},
		"九十天":  {map[string]any{"note": "质量部", "valid_days": 90}, "2027-01-14T09:00:00+08:00"},
		"指定日期": {map[string]any{"note": "外包-赵", "expires_at": "2026-12-01"}, "2026-12-02T00:00:00+08:00"},
		"指定时刻": {map[string]any{"note": "短期", "expires_at": "2026-10-18T18:00:00+08:00"}, "2026-10-18T18:00:00+08:00"},
	} {
		got := mustCreateGuestLink(t, s, web, number, tc.body)
		if got.ExpiresAt != tc.want {
			t.Errorf("[%s] expires_at = %q, 想 %q", name, got.ExpiresAt, tc.want)
		}
	}

	// 建单、打标签等成员 Agent 也能创建访客链接。
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)
	if res := createGuestLink(t, s, agent, number, map[string]any{"note": "质量部 Agent"}); res.StatusCode != http.StatusCreated {
		t.Errorf("成员 Agent 创建访客链接状态码 = %d, 想 201", res.StatusCode)
	}

	// 校验：备注必填；天数只能 7/30/90；日期必须是将来。
	for name, tc := range map[string]struct {
		body map[string]any
		code string
	}{
		"缺备注":     {map[string]any{}, "note_required"},
		"空白备注":    {map[string]any{"note": "  "}, "note_required"},
		"非法天数":    {map[string]any{"note": "x", "valid_days": 14}, "invalid_valid_days"},
		"永久零天":    {map[string]any{"note": "x", "valid_days": 0}, "invalid_valid_days"},
		"负天数":     {map[string]any{"note": "x", "valid_days": -30}, "invalid_valid_days"},
		"日期格式错误":  {map[string]any{"note": "x", "expires_at": "明天"}, "invalid_expires_at"},
		"日期已过去":   {map[string]any{"note": "x", "expires_at": "2026-10-01"}, "invalid_expires_at"},
		"天数与日期并存": {map[string]any{"note": "x", "valid_days": 7, "expires_at": "2026-12-01"}, "invalid_valid_days"},
	} {
		res := createGuestLink(t, s, web, number, tc.body)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("[%s] 状态码 = %d, 想 422", name, res.StatusCode)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != tc.code {
			t.Errorf("[%s] 错误码 = %q, 想 %q", name, got.Error.Code, tc.code)
		}
	}

	// 每次创建都记一条 guest_link_created 事件（带备注），作者是指令人。
	var view ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	created := 0
	for _, ev := range view.Timeline {
		if ev.Type != "guest_link_created" {
			continue
		}
		created++
		if ev.Note == "" {
			t.Error("guest_link_created 事件缺 note 字段")
		}
		if ev.Actor.Member == nil || ev.Actor.Member.Name != "陈工" {
			t.Errorf("guest_link_created 作者不对：%+v", ev.Actor)
		}
	}
	if want := 6; created != want { // 1 默认 + 4 种有效期 + Agent 1；校验失败的不记
		t.Errorf("guest_link_created 事件有 %d 条, 想 %d", created, want)
	}
}

// 访客链接的密钥直接作 Bearer token：GET /api/session 返回链接指向的工单，
// 访客由此知道该读哪张单（spec 用户故事 84）。
func Test访客用链接密钥查明指向的工单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "BH-7600CS 质控结果偏高")

	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三", "valid_days": 90})
	guest := s.GuestClient(guestKeyOf(t, link))

	var sess struct {
		Viewer struct {
			Kind   string `json:"kind"`
			Member *struct {
				ID   int64
				Name string
			} `json:"member"`
			Agent *struct {
				ID   int64
				Name string
			} `json:"agent"`
			LinkNote      string `json:"link_note"`
			LinkExpiresAt string `json:"link_expires_at"`
			CanEditBrief  bool   `json:"can_edit_brief"`
		} `json:"viewer"`
		Ticket struct {
			Number string `json:"number"`
			Seq    int64  `json:"seq"`
			Title  string `json:"title"`
			API    string `json:"api"`
		} `json:"ticket"`
	}
	res := s.Do(t, guest, http.MethodGet, "/api/v1/session", nil, &sess)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客 GET /session 状态码 = %d, 想 200", res.StatusCode)
	}
	if sess.Viewer.Kind != "guest" {
		t.Errorf("viewer.kind = %q, 想 guest", sess.Viewer.Kind)
	}
	if sess.Viewer.Member != nil || sess.Viewer.Agent != nil {
		t.Errorf("访客的 viewer 不应带成员或 Agent：%+v", sess.Viewer)
	}
	if sess.Viewer.LinkNote != "检验科-张三" {
		t.Errorf("link_note = %q, 想 检验科-张三", sess.Viewer.LinkNote)
	}
	if want := link.ExpiresAt; sess.Viewer.LinkExpiresAt != want {
		t.Errorf("link_expires_at = %q, 想 %q", sess.Viewer.LinkExpiresAt, want)
	}
	if sess.Viewer.CanEditBrief {
		t.Error("can_edit_brief 应为 false")
	}
	if sess.Ticket.Number != number || sess.Ticket.Seq != 1 || sess.Ticket.Title != "BH-7600CS 质控结果偏高" {
		t.Errorf("链接指向的工单不对：%+v", sess.Ticket)
	}
	if sess.Ticket.API != "/api/v1/tickets/"+number {
		t.Errorf("ticket.api = %q", sess.Ticket.API)
	}

	// 不认识的密钥：401 invalid_token（与 Agent token 同一出口）。
	res = s.Do(t, s.GuestClient("tbg_nosuchkey"), http.MethodGet, "/api/v1/session", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未知密钥状态码 = %d, 想 401", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "invalid_token" {
		t.Errorf("未知密钥错误码 = %q, 想 invalid_token", got.Error.Code)
	}
}

// 时钟推进到有效期之后，链接立即失效：401 link_expired。
func Test访客链接过期即时生效(t *testing.T) {
	s := tbtest.Start(t) // 2026-10-16 09:00
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")

	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三", "valid_days": 7})
	guest := s.GuestClient(guestKeyOf(t, link))
	if res := s.Do(t, guest, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("有效期内 GET /session 状态码 = %d, 想 200", res.StatusCode)
	}

	// 推进 7 天整：valid_days=7 的到期时刻是 2026-10-23 09:00，含该时刻即失效。
	s.Clock().Advance(7 * 24 * time.Hour)
	for name, path := range map[string]string{
		"session": "/api/v1/session",
		"读工单":     "/api/v1/tickets/" + number,
		"时间线":     "/api/v1/tickets/" + number + "/timeline",
	} {
		res := s.Do(t, guest, http.MethodGet, path, nil, nil)
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("[%s] 过期后状态码 = %d, 想 401", name, res.StatusCode)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "link_expired" {
			t.Errorf("[%s] 过期后错误码 = %q, 想 link_expired", name, got.Error.Code)
		}
		if !strings.Contains(got.Error.Hint, "重新") {
			t.Errorf("[%s] hint 应提示重新获取链接：%q", name, got.Error.Hint)
		}
	}

	// 回复（POST）同样拒绝。
	res := s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "过期后回复"}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("过期后回复状态码 = %d, 想 401", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "link_expired" {
		t.Errorf("过期后回复错误码 = %q, 想 link_expired", got.Error.Code)
	}

	// 成员视角不受影响（推进了 7 天，网页会话也到期了，重新登录再看）。
	var view ticketResponseBody
	s.Do(t, s.Login("陈工", "secret-pass"), http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if view.Status != "open" {
		t.Errorf("成员读工单 status = %q", view.Status)
	}
}

// 访客读工单看不到 labels 字段和标签事件（事件序号会跳号，这是已接受的）；
// 成员视角仍然齐全。时间线增量接口同样过滤。
func Test访客读工单看不到标签与标签事件(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "BH-7600CS 质控结果偏高")

	// 造标签事件：created(seq1) → guest_link_created(seq2) → label_added(seq3) →
	// label_added(seq4)。访客应只看到 1、2（跳过 3、4）。
	mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	setLabels := func(add ...string) {
		t.Helper()
		res := s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/labels",
			map[string]any{"add": add, "allow_new": true}, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("打标签状态码 = %d, 想 200", res.StatusCode)
		}
	}
	setLabels("部门:研发部", "待初筛")
	key := guestKeyOf(t, mustCreateGuestLink(t, s, web, number, map[string]any{"note": "质量部 Agent"}))
	guest := s.GuestClient(key)

	// JSON：没有 labels 字段；时间线没有标签事件；viewer 是访客形状。
	var raw map[string]any
	s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number, nil, &raw)
	if _, has := raw["labels"]; has {
		t.Error("访客的 JSON 里不应有 labels 字段")
	}
	var got ticketResponseBody
	res := s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客读工单状态码 = %d, 想 200", res.StatusCode)
	}
	for _, ev := range got.Timeline {
		if ev.Type == "label_added" || ev.Type == "label_removed" {
			t.Errorf("访客时间线里出现了标签事件：%+v", ev)
		}
	}
	wantSeqs := []int64{1, 2, 5, 6} // 两条 guest_link_created、认领；3、4 是标签事件
	if len(got.Timeline) != len(wantSeqs) {
		t.Fatalf("访客时间线有 %d 条, 想 %d（跳号）", len(got.Timeline), len(wantSeqs))
	}
	for i, want := range wantSeqs {
		if got.Timeline[i].Seq != want {
			t.Errorf("访客时间线[%d].seq = %d, 想 %d（跳号是已接受的）", i, got.Timeline[i].Seq, want)
		}
	}
	if got.Viewer.Kind != "guest" || got.Viewer.Role != "guest" {
		t.Errorf("viewer 不对：%+v", got.Viewer.Kind)
	}
	if got.Viewer.Member != nil || got.Viewer.Agent != nil {
		t.Errorf("访客的 viewer 不应带成员或 Agent")
	}
	if got.Viewer.LinkNote == nil || *got.Viewer.LinkNote != "质量部 Agent" {
		t.Errorf("viewer.link_note 不对：%+v", got.Viewer.LinkNote)
	}
	// 访客的能力表：可回复/上传（工单开启），不可打标签、关闭、管理链接。
	if !got.Viewer.Can.Reply || !got.Viewer.Can.Upload {
		t.Errorf("开启工单上访客应可回复/上传：%+v", got.Viewer.Can)
	}
	if got.Viewer.Can.Label || got.Viewer.Can.Close || got.Viewer.Can.Reopen || got.Viewer.Can.ManageGuestLinks {
		t.Errorf("访客不应能打标签/关闭/重开/管理链接：%+v", got.Viewer.Can)
	}
	if got.Viewer.Can.EditBrief {
		t.Error("这条链接没有「可填写说明」权限")
	}

	// 时间线增量接口：同样过滤，cursor 是访客可见的最后一条。
	var tl timelineBody
	s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number+"/timeline?after=1", nil, &tl)
	if len(tl.Events) != 3 || tl.Events[0].Seq != 2 || tl.Events[1].Seq != 5 || tl.Events[2].Seq != 6 {
		t.Errorf("访客增量时间线不对：%+v", tl.Events)
	}
	if tl.Cursor != 6 {
		t.Errorf("访客 cursor = %d, 想 6（可见的最后一条，不是全量最后一条）", tl.Cursor)
	}

	// Markdown 视图：没有标签行、没有标签事件。
	res = s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number+"?format=markdown", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客 Markdown 状态码 = %d, 想 200", res.StatusCode)
	}
	body := readAll(t, res)
	if strings.Contains(string(body), "标签：") || strings.Contains(string(body), "打上标签") {
		t.Errorf("访客 Markdown 里不应有标签：%s", body)
	}

	// 成员视角：labels 与标签事件齐全。
	var member ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &member)
	if len(member.Labels) != 3 {
		t.Errorf("成员的 labels = %+v, 想三个", member.Labels)
	}
	labelEvents := 0
	for _, ev := range member.Timeline {
		if ev.Type == "label_added" {
			labelEvents++
		}
	}
	if labelEvents != 2 {
		t.Errorf("成员看到 %d 条标签事件, 想 2", labelEvents)
	}
}

// 访客访问其他工单一律 404（防枚举，不用 403）：读、时间线、回复都一样。
func Test访客访问其他工单返回404(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	mine := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")
	other := mustCreateTicket(t, s, web, "另一张工单")

	link := mustCreateGuestLink(t, s, web, mine, map[string]any{"note": "检验科-张三"})
	guest := s.GuestClient(guestKeyOf(t, link))

	for name, req := range map[string]struct{ method, path string }{
		"读工单":  {http.MethodGet, "/api/v1/tickets/" + other},
		"按序号读": {http.MethodGet, "/api/v1/tickets/2"},
		"时间线":  {http.MethodGet, "/api/v1/tickets/" + other + "/timeline"},
		"回复":   {http.MethodPost, "/api/v1/tickets/" + other + "/replies"},
		"修订说明": {http.MethodPatch, "/api/v1/tickets/" + other},
	} {
		res := s.Do(t, guest, req.method, req.path, map[string]string{"body_md": "x"}, nil)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("[%s] 状态码 = %d, 想 404", name, res.StatusCode)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "ticket_not_found" {
			t.Errorf("[%s] 错误码 = %q, 想 ticket_not_found", name, got.Error.Code)
		}
	}

	// 指向的工单照常可读。
	if res := s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+mine, nil, nil); res.StatusCode != http.StatusOK {
		t.Errorf("指向的工单状态码 = %d, 想 200", res.StatusCode)
	}
}

// 访客调用仅成员的接口：统一 403 members_only（决策 4）。这也是 #31 留下的
// 「访客 → 标签接口」测试。
func Test访客调用仅成员接口返回membersOnly(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")
	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	guest := s.GuestClient(guestKeyOf(t, link))

	for name, req := range map[string]struct {
		method, path string
		body         any
	}{
		"建单":        {http.MethodPost, "/api/v1/tickets", map[string]any{"title": "x", "labels": []string{"机型:UD-2300"}}},
		"字典列表":      {http.MethodGet, "/api/v1/labels", nil},
		"resolve":   {http.MethodGet, "/api/v1/labels/resolve?q=7600", nil},
		"打标签":       {http.MethodPost, "/api/v1/tickets/" + number + "/labels", map[string]any{"add": []string{"待初筛"}}},
		"加别名":       {http.MethodPost, "/api/v1/labels/aliases", map[string]any{"label": "机型:UD-2300", "alias": "x"}},
		"关闭":        {http.MethodPost, "/api/v1/tickets/" + number + "/close", map[string]any{"reason": "done"}},
		"重开":        {http.MethodPost, "/api/v1/tickets/" + number + "/reopen", nil},
		"创建访客链接":    {http.MethodPost, "/api/v1/tickets/" + number + "/guest-links", map[string]any{"note": "x"}},
		"列出访客链接":    {http.MethodGet, "/api/v1/tickets/" + number + "/guest-links", nil},
		"签发Agent令牌": {http.MethodPost, "/api/v1/me/agent-tokens", map[string]any{"name": "x"}},
	} {
		res := s.Do(t, guest, req.method, req.path, req.body, nil)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("[%s] 状态码 = %d, 想 403", name, res.StatusCode)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		want := "members_only"
		if name == "签发Agent令牌" {
			want = "web_session_required" // 只接受网页会话（ADR 0003），比 members_only 更具体
		}
		if got.Error.Code != want {
			t.Errorf("[%s] 错误码 = %q, 想 %q", name, got.Error.Code, want)
		}
	}
}

// 访客回复：记录链接备注与自报的 author_name / via_agent，署名与成员 Agent 在
// 结构上区分开（actor.member/agent 为 null，另带 link_note/author_name/via_agent）。
// Markdown 里写成「张三（经「检验科-张三」链接）」「检验科-张三（访客）的 Agent（自报）」。
func Test访客回复记录链接备注与自报信息(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "BH-7600CS 质控结果偏高")
	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	guest := s.GuestClient(guestKeyOf(t, link))

	// 访客本人回复（自报署名，不自称 Agent）。
	res := s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "附上近一个月的质控数据。", "author_name": "张三"}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("访客回复状态码 = %d, 想 201", res.StatusCode)
	}
	var first replyBody
	tbtest.DecodeJSON(t, res, &first)
	if first.Author != "张三（经「检验科-张三」链接）" {
		t.Errorf("author = %q, 想 张三（经「检验科-张三」链接）", first.Author)
	}
	// 访客回复后轮到指派人；未指派时轮到本部门，原因「访客回复了」。
	if first.WaitingOn.Party != "department" || first.WaitingOn.Reason != "访客回复了" {
		t.Errorf("waiting_on 不对：%+v", first.WaitingOn)
	}

	// 访客的 Agent 用同一条链接回复（自报 via_agent）。
	res = s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "已按模板补全字段。", "author_name": "张三", "via_agent": true}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("访客 Agent 回复状态码 = %d, 想 201", res.StatusCode)
	}
	var second replyBody
	tbtest.DecodeJSON(t, res, &second)
	if second.Author != "检验科-张三（访客）的 Agent（自报）" {
		t.Errorf("author = %q, 想 检验科-张三（访客）的 Agent（自报）", second.Author)
	}

	// 不自报署名的访客：写「访客（经「…」链接）」。
	res = s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "收到。"}, nil)
	var third replyBody
	tbtest.DecodeJSON(t, res, &third)
	if third.Author != "访客（经「检验科-张三」链接）" {
		t.Errorf("author = %q, 想 访客（经「检验科-张三」链接）", third.Author)
	}

	// 成员看时间线：访客回复的 actor 结构与成员、成员 Agent 都不同。
	var view ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if len(view.Timeline) != 6 { // created + guest_link_created + claim + 三条访客回复
		t.Fatalf("时间线有 %d 条, 想 6", len(view.Timeline))
	}
	g := view.Timeline[3]
	if g.Type != "reply" || g.Actor.Member != nil || g.Actor.Agent != nil {
		t.Errorf("访客回复的 actor 不应带成员或 Agent：%+v", g.Actor)
	}
	if g.Actor.LinkNote == nil || *g.Actor.LinkNote != "检验科-张三" {
		t.Errorf("actor.link_note 不对：%+v", g.Actor.LinkNote)
	}
	if g.Actor.AuthorName == nil || *g.Actor.AuthorName != "张三" {
		t.Errorf("actor.author_name 不对：%+v", g.Actor.AuthorName)
	}
	if g.Actor.ViaAgent == nil || *g.Actor.ViaAgent {
		t.Errorf("actor.via_agent 应为 false（本人回复）：%+v", g.Actor.ViaAgent)
	}
	if view.Timeline[4].Actor.ViaAgent == nil || !*view.Timeline[4].Actor.ViaAgent {
		t.Errorf("自报 via_agent 的回复应记 true：%+v", view.Timeline[4].Actor.ViaAgent)
	}

	// 成员的 Agent 回复一条作对照：actor.agent 是平台认证的具名 Agent。
	_, token := s.IssueAgentToken(t, web, "笔记本")
	reply(t, s, s.AgentClient(token), number, "收到，正在处理。")
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	agentReply := view.Timeline[6]
	if agentReply.Actor.Agent == nil || agentReply.Actor.Agent.Name != "笔记本" ||
		agentReply.Actor.Member == nil || agentReply.Actor.Member.Name != "陈工" {
		t.Errorf("成员 Agent 回复的 actor 应带平台认证的成员与 Agent：%+v", agentReply.Actor)
	}
	if agentReply.Actor.LinkNote != nil || agentReply.Actor.ViaAgent != nil {
		t.Errorf("成员回复的 actor 不应带访客字段：%+v", agentReply.Actor)
	}

	// 成员的 author_name / via_agent 自报不生效（署名由凭证决定，ADR 0003）。
	res = s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "本人回复。", "author_name": "假冒的署名", "via_agent": true}, nil)
	var mine replyBody
	tbtest.DecodeJSON(t, res, &mine)
	if mine.Author != "陈工" {
		t.Errorf("成员自报署名不应生效：author = %q, 想 陈工", mine.Author)
	}

	// Markdown 视图：访客署名按上面的写法；轮到访客时「下一步」给回复示例。
	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"?format=markdown", nil, nil)
	md := string(readAll(t, res))
	if !strings.Contains(md, "张三（经「检验科-张三」链接） 回复") {
		t.Errorf("Markdown 缺访客署名：\n%s", md)
	}
	if !strings.Contains(md, "检验科-张三（访客）的 Agent（自报） 回复") {
		t.Errorf("Markdown 缺访客 Agent 署名：\n%s", md)
	}
}

// 反馈单（无对手）成员回复后轮到访客：访客视角 your_turn 为 true；访客的
// Markdown「下一步」给带 author_name 的回复示例。任务单创建者回复则轮到本部门。
func Test轮到访客时访客视角的你的回合与下一步(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateReport(t, s, web, "LIS 导出报错")
	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	guest := s.GuestClient(guestKeyOf(t, link))

	var before ticketResponseBody
	s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number, nil, &before)
	if before.Viewer.YourTurn {
		t.Error("尚无回复时未指派的反馈单轮到本部门，不是访客")
	}

	reply(t, s, web, number, "请补充出现时间。")
	var after ticketResponseBody
	s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number, nil, &after)
	if after.WaitingOn.Party != "guest" || after.WaitingOn.Text != "访客" {
		t.Errorf("waiting_on 不对：%+v", after.WaitingOn)
	}
	if !after.Viewer.YourTurn {
		t.Error("轮到访客时访客的 your_turn 应为 true")
	}
	// 成员视角同一张单不是他的轮次。
	var member ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &member)
	if member.Viewer.YourTurn {
		t.Error("未指派的反馈单不应轮到创建者本人")
	}

	res := s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number+"?format=markdown", nil, nil)
	md := string(readAll(t, res))
	if !strings.Contains(md, "角色是 **访客**") || !strings.Contains(md, `"author_name"`) {
		t.Errorf("访客 Markdown 的「下一步」应给带 author_name 的回复示例：\n%s", md)
	}
}

// 工单关闭后访客链接只读：回复与修订说明都被拒（409 ticket_closed）；
// 重新开启后恢复可回复。成员在关闭的工单上仍可回复。
func Test工单关闭后访客链接只读重开后恢复(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")
	editLink := mustCreateGuestLink(t, s, web, number,
		map[string]any{"note": "检验科-张三", "can_edit_brief": true})
	guest := s.GuestClient(guestKeyOf(t, editLink))

	closeTicket(t, s, web, number, closeBody{Reason: "done"})

	res := s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "关闭后还想补充"}, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("关闭后访客回复状态码 = %d, 想 409", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "ticket_closed" {
		t.Errorf("错误码 = %q, 想 ticket_closed", got.Error.Code)
	}
	res = revise(t, s, guest, number, map[string]any{"brief_md": "关闭后修订", "base_revision": 1})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("关闭后访客修订状态码 = %d, 想 409", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "ticket_closed" {
		t.Errorf("错误码 = %q, 想 ticket_closed", got.Error.Code)
	}

	// 只读但可看；能力表全灭。
	var view ticketResponseBody
	s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if view.Status != "closed" || view.Viewer.Can.Reply || view.Viewer.Can.EditBrief {
		t.Errorf("关闭后的访客视角不对：status=%q can=%+v", view.Status, view.Viewer.Can)
	}

	// 成员在关闭的工单上仍可回复（不重开工单）。
	if res := reply(t, s, web, number, "成员补充。"); res.StatusCode != http.StatusCreated {
		t.Errorf("成员在关闭工单上回复状态码 = %d, 想 201", res.StatusCode)
	}

	// 重新开启后链接恢复可回复、可修订。
	s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/reopen", nil, nil)
	res = s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "重开后补充", "author_name": "张三"}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("重开后访客回复状态码 = %d, 想 201", res.StatusCode)
	}
}

// mustCreateReport 建一张反馈（report）工单并返回编号：反馈单没有对手，
// 成员回复后轮到访客。
func mustCreateReport(t testing.TB, s *tbtest.Server, c *http.Client, title string) string {
	t.Helper()
	res := createTicket(t, s, c, createTicketBody{Kind: "report", Title: title, Labels: []string{"系统:LIS"}})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建反馈 %q 状态码 = %d, 想 201", title, res.StatusCode)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)
	return created.Number
}

// 「可填写说明」权限：没有权限的链接修订被拒（403 link_cannot_edit_brief）；
// 有权限的访客可修订标题和说明，记一条 brief_revised 事件（作者带链接备注）。
func Test可填写说明的访客链接能修订标题和说明(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	owner := s.Login("陈工", "secret-pass")
	other := s.Login("李工", "secret-pass")
	number := mustCreateTicket(t, s, owner, "质控结果偏高，请补充信息")

	noEdit := s.GuestClient(guestKeyOf(t, mustCreateGuestLink(t, s, owner, number,
		map[string]any{"note": "检验科-张三"})))
	res := revise(t, s, noEdit, number, map[string]any{"brief_md": "越权修订", "base_revision": 1})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("无权限链接修订状态码 = %d, 想 403", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "link_cannot_edit_brief" {
		t.Errorf("错误码 = %q, 想 link_cannot_edit_brief", got.Error.Code)
	}

	// 有权限的链接：修订标题与说明，版本推进，事件记入时间线。
	edit := s.GuestClient(guestKeyOf(t, mustCreateGuestLink(t, s, owner, number,
		map[string]any{"note": "检验科-张三", "valid_days": 90, "can_edit_brief": true})))
	res = revise(t, s, edit, number, map[string]any{
		"title":         "BH-7600CS 质控结果偏高（已补全）",
		"brief_md":      "机型：BH-7600CS\n现象：ALT 质控连续偏高",
		"base_revision": 1,
		"author_name":   "张三",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("有权限链接修订状态码 = %d, 想 200", res.StatusCode)
	}
	var revised struct {
		Revision int64 `json:"revision"`
	}
	tbtest.DecodeJSON(t, res, &revised)
	if revised.Revision != 2 {
		t.Errorf("revision = %d, 想 2", revised.Revision)
	}

	var view ticketResponseBody
	s.Do(t, owner, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if view.Title != "BH-7600CS 质控结果偏高（已补全）" || view.Brief.Markdown != "机型：BH-7600CS\n现象：ALT 质控连续偏高" {
		t.Errorf("修订后的标题/说明不对：%q %q", view.Title, view.Brief.Markdown)
	}
	var briefEvent *timelineEventBody
	for i := range view.Timeline {
		if view.Timeline[i].Type == "brief_revised" {
			briefEvent = &view.Timeline[i]
		}
	}
	if briefEvent == nil {
		t.Fatal("没有 brief_revised 事件")
	}
	if briefEvent.Revision != 2 {
		t.Errorf("事件 revision = %d, 想 2", briefEvent.Revision)
	}
	if briefEvent.Actor.Member != nil || briefEvent.Actor.LinkNote == nil || *briefEvent.Actor.LinkNote != "检验科-张三" {
		t.Errorf("修订事件的作者应是访客（带链接备注）：%+v", briefEvent.Actor)
	}

	// 乐观锁对访客同样生效。
	res = revise(t, s, edit, number, map[string]any{"brief_md": "旧版本再改", "base_revision": 1})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("过期 base_revision 状态码 = %d, 想 409", res.StatusCode)
	}

	// 其他成员仍然不能修订（访客链接的权限只属于链接持有者）。
	res = revise(t, s, other, number, map[string]any{"brief_md": "成员修订", "base_revision": 2})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("其他成员修订状态码 = %d, 想 403", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "not_brief_owner" {
		t.Errorf("错误码 = %q, 想 not_brief_owner", got.Error.Code)
	}

	// 访客视角的能力表：有权限的链接 edit_brief 为 true。
	var guest ticketResponseBody
	s.Do(t, edit, http.MethodGet, "/api/v1/tickets/"+number, nil, &guest)
	if !guest.Viewer.Can.EditBrief {
		t.Error("带「可填写说明」权限的链接 edit_brief 应为 true")
	}
}

// 撤销与延期：都记入时间线（guest_link_revoked / guest_link_extended 带备注与
// 新到期时刻）；撤销立即生效（401 link_revoked）；已撤销或已过期的链接不能延期；
// 列表不含密钥。
func Test访客链接的撤销延期与列表(t *testing.T) {
	s := tbtest.Start(t) // 2026-10-16 09:00
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	other := s.Login("李工", "secret-pass") // 任何成员都能撤销/延期
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")
	otherTicket := mustCreateTicket(t, s, web, "另一张工单")

	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三", "valid_days": 7})
	guest := s.GuestClient(guestKeyOf(t, link))

	// 延期到 90 天：记事件，新到期时刻生效。
	res := s.Do(t, other, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10)+"/extend",
		map[string]any{"valid_days": 90}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("延期状态码 = %d, 想 200", res.StatusCode)
	}
	var extended guestLinkResponse
	tbtest.DecodeJSON(t, res, &extended)
	if want := "2027-01-14T09:00:00+08:00"; extended.ExpiresAt != want {
		t.Errorf("延期后 expires_at = %q, 想 %q", extended.ExpiresAt, want)
	}
	// 原有效期过了之后链接仍可用（已延到 90 天）。
	s.Clock().Advance(8 * 24 * time.Hour)
	if res := s.Do(t, guest, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("延期后链接应仍有效，状态码 = %d", res.StatusCode)
	}
	// 推进了 8 天，网页会话过期，重新登录。
	web = s.Login("陈工", "secret-pass")
	other = s.Login("李工", "secret-pass")

	var view ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	found := 0
	for _, ev := range view.Timeline {
		if ev.Type == "guest_link_extended" {
			found++
			if ev.Note != "检验科-张三" {
				t.Errorf("事件 note = %q", ev.Note)
			}
			if ev.ExpiresAt != "2027-01-14T09:00:00+08:00" {
				t.Errorf("事件 expires_at = %q", ev.ExpiresAt)
			}
		}
	}
	if found != 1 {
		t.Fatalf("guest_link_extended 事件有 %d 条, 想 1", found)
	}

	// 撤销：立即生效（401 link_revoked），记事件，幂等。
	res = s.Do(t, other, http.MethodDelete,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10), nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销状态码 = %d, 想 204", res.StatusCode)
	}
	res = s.Do(t, guest, http.MethodGet, "/api/v1/session", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("撤销后状态码 = %d, 想 401", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "link_revoked" {
		t.Errorf("撤销后错误码 = %q, 想 link_revoked", got.Error.Code)
	}
	// 再撤销一次：幂等成功，不重复记事件。
	res = s.Do(t, web, http.MethodDelete,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10), nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("重复撤销状态码 = %d, 想 204", res.StatusCode)
	}

	// 已撤销的链接不能延期；已过期的也不能。
	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10)+"/extend",
		map[string]any{"valid_days": 30}, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("撤销后延期状态码 = %d, 想 409", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "link_not_extendable" {
		t.Errorf("错误码 = %q, 想 link_not_extendable", got.Error.Code)
	}
	expired := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "短期", "valid_days": 7})
	s.Clock().Advance(8 * 24 * time.Hour)
	web = s.Login("陈工", "secret-pass")
	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(expired.ID, 10)+"/extend",
		map[string]any{"valid_days": 30}, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("过期后延期状态码 = %d, 想 409", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "link_not_extendable" {
		t.Errorf("错误码 = %q, 想 link_not_extendable", got.Error.Code)
	}

	// 列表：这个工单的全部链接，含备注、到期、撤销时刻与创建者；不含密钥。
	var list struct {
		Links []guestLinkResponse `json:"links"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/guest-links", nil, &list)
	if len(list.Links) != 2 {
		t.Fatalf("列表有 %d 条, 想 2", len(list.Links))
	}
	for _, l := range list.Links {
		if l.URL != "" {
			t.Errorf("列表不应回显密钥：url = %q", l.URL)
		}
	}
	if list.Links[0].Note != "检验科-张三" || list.Links[0].RevokedAt == nil {
		t.Errorf("第一条（已撤销）不对：%+v", list.Links[0])
	}

	// 不属于这张工单的链接 id：404（防跨工单误操作）。
	res = s.Do(t, web, http.MethodDelete,
		"/api/v1/tickets/"+otherTicket+"/guest-links/"+strconv.FormatInt(link.ID, 10), nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("跨工单撤销状态码 = %d, 想 404", res.StatusCode)
	}
	res = s.Do(t, web, http.MethodDelete,
		"/api/v1/tickets/"+number+"/guest-links/999", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("不存在的链接撤销状态码 = %d, 想 404", res.StatusCode)
	}
	var nf errorBody
	tbtest.DecodeJSON(t, res, &nf)
	if nf.Error.Code != "guest_link_not_found" {
		t.Errorf("错误码 = %q, 想 guest_link_not_found", nf.Error.Code)
	}

	// 撤销与延期事件在时间线上（成员可见，访客也可见——可见性只隐藏标签）。
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	revoked := 0
	for _, ev := range view.Timeline {
		if ev.Type == "guest_link_revoked" {
			revoked++
		}
	}
	if revoked != 1 { // 第二次撤销幂等，不重复记
		t.Errorf("guest_link_revoked 事件有 %d 条, 想 1", revoked)
	}
}

// 访客的幂等键按凭证隔离：同一 key 重放返回首次结果、不产生新回复；
// 成员拿同一个 key 回复是另一个凭证，正常执行。
func Test访客回复的幂等键按凭证隔离(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "BH-7600CS 质控结果偏高")
	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	guest := s.GuestClient(guestKeyOf(t, link))

	headers := map[string]string{"Idempotency-Key": "guest-reply-001"}
	path := "/api/v1/tickets/" + number + "/replies"
	first := s.DoWithHeaders(t, guest, http.MethodPost, path, headers,
		map[string]any{"body_md": "网络闪断后重试。", "author_name": "张三"}, nil)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("首次回复状态码 = %d, 想 201", first.StatusCode)
	}
	firstBody := readAll(t, first)
	second := s.DoWithHeaders(t, guest, http.MethodPost, path, headers,
		map[string]any{"body_md": "网络闪断后重试。", "author_name": "张三"}, nil)
	if second.StatusCode != http.StatusCreated || string(firstBody) != string(readAll(t, second)) {
		t.Errorf("重放应返回首次结果：%d %s", second.StatusCode, readAll(t, second))
	}

	// 成员用同一个 key：凭证不同，是新的请求。
	res := s.DoWithHeaders(t, web, http.MethodPost, path, headers,
		map[string]string{"body_md": "成员回复。"}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("成员同 key 回复状态码 = %d, 想 201（按凭证隔离）", res.StatusCode)
	}

	var view ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	replies := 0
	for _, ev := range view.Timeline {
		if ev.Type == "reply" {
			replies++
		}
	}
	if replies != 2 {
		t.Errorf("回复有 %d 条, 想 2（访客重放不产生新内容）", replies)
	}
}

// 日期写法「到 12-01 为止」要管满一整天（评审 P2-8，#32）：有效期到
// 2026-10-16 的链接在 10-16 全天可用，10-17 00:00（北京时间）才过期。
func Test日期有效期管满一整天(t *testing.T) {
	s := tbtest.Start(t) // 假时钟起点：2026-10-16 09:00（北京时间）
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "升级 UD-2300 固件")

	link := s.CreateGuestLink(t, web, number, map[string]any{"note": "检验科-张三", "expires_at": "2026-10-16"})
	if link.ExpiresAt != "2026-10-17T00:00:00+08:00" {
		t.Fatalf("日期有效期应到次日 00:00，实际 %q", link.ExpiresAt)
	}

	// 当天白天（09:00 与 23:59）链接都能用。
	guest := s.GuestClient(link.Key(t))
	for _, at := range []string{"当天 09:00", "当天 23:59"} {
		res := s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s 用链接读工单状态码 = %d, 想 200", at, res.StatusCode)
		}
		s.Clock().Advance(14*time.Hour + 59*time.Minute) // 拨到 23:59
	}
	// 次日 00:00 已过 → 401 link_expired。
	s.Clock().Advance(time.Minute)
	res := s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number, nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("次日 00:00 后状态码 = %d, 想 401", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "link_expired" {
		t.Errorf("错误码 = %q, 想 link_expired", got.Error.Code)
	}
}
