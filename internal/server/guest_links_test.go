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
