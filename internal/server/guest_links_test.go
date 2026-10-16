package server_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// guestLinkResponse 是 POST …/guest-links 创建成功的响应（201）。
type guestLinkResponse struct {
	ID           int64  `json:"id"`
	URL          string `json:"url"`
	Note         string `json:"note"`
	ExpiresAt    string `json:"expires_at"`
	CanEditBrief bool   `json:"can_edit_brief"`
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
		"指定日期": {map[string]any{"note": "外包-赵", "expires_at": "2026-12-01"}, "2026-12-01T00:00:00+08:00"},
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
	wantSeqs := []int64{1, 2, 5} // created、guest_link_created、再一条 guest_link_created；3、4 是标签事件
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
	if len(tl.Events) != 2 || tl.Events[0].Seq != 2 || tl.Events[1].Seq != 5 {
		t.Errorf("访客增量时间线不对：%+v", tl.Events)
	}
	if tl.Cursor != 5 {
		t.Errorf("访客 cursor = %d, 想 5（可见的最后一条，不是全量最后一条）", tl.Cursor)
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
