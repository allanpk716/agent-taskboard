package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// feedbackCreateResponse 是带 guest_link 建单（#64「请人反馈」）的响应结构。
type feedbackCreateResponse struct {
	createTicketResponse
	ShareText *string            `json:"share_text"`
	GuestLink *feedbackGuestLink `json:"guest_link"`
}

// feedbackGuestLink 是建单响应里嵌套的访客链接（含面向访客的转发文案）。
type feedbackGuestLink struct {
	ID           int64  `json:"id"`
	URL          string `json:"url"`
	Note         string `json:"note"`
	ExpiresAt    string `json:"expires_at"`
	CanEditBrief bool   `json:"can_edit_brief"`
	ShareText    string `json:"share_text"`
}

// createWithGuestLink 发一个带 guest_link 的建单请求，返回原始响应。
func createWithGuestLink(t testing.TB, s *tbtest.Server, c *http.Client, body map[string]any) *http.Response {
	t.Helper()
	return s.Do(t, c, http.MethodPost, "/api/v1/tickets", body, nil)
}

// 带 guest_link 建单（「请人反馈」）：一次请求同时建好工单和带「可填写说明」
// 权限的访客链接；响应嵌套 guest_link（地址、有效期、转发文案），时间线依次是
// created、assigned（有指派人时）、guest_link_created；模板的预设标签照常打上。
func Test请人反馈建单同时生成访客链接(t *testing.T) {
	s := tbtest.Start(t) // 假时钟起点：2026-10-16 09:00（北京时间）
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	res := createWithGuestLink(t, s, web, map[string]any{
		"template": "bug-report",
		"title":    "UD-2300 质控时报警 E-203",
		"fields":   map[string]string{},
		"labels":   []string{},
		"assignee": "李工",
		"guest_link": map[string]any{
			"note":           "检验科-张三",
			"can_edit_brief": true,
		},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}
	var created feedbackCreateResponse
	tbtest.DecodeJSON(t, res, &created)

	if created.GuestLink == nil {
		t.Fatal("响应缺少嵌套的 guest_link")
	}
	link := created.GuestLink
	if !strings.HasPrefix(link.URL, "/g/tbg_") {
		t.Errorf("guest_link.url = %q, 想形如 /g/tbg_…", link.URL)
	}
	if link.Note != "检验科-张三" {
		t.Errorf("guest_link.note = %q, 想 检验科-张三", link.Note)
	}
	// 不填 valid_days 默认 30 天：从创建时刻起算。
	if want := "2026-11-15T09:00:00+08:00"; link.ExpiresAt != want {
		t.Errorf("guest_link.expires_at = %q, 想 %q", link.ExpiresAt, want)
	}
	if !link.CanEditBrief {
		t.Error("guest_link.can_edit_brief 应为 true（请求里带的权限）")
	}
	// 转发文案：称呼对方、含标题与链接。
	if !strings.Contains(link.ShareText, "检验科-张三") {
		t.Errorf("guest_link.share_text 应称呼对方（备注）：%q", link.ShareText)
	}
	if !strings.Contains(link.ShareText, "UD-2300 质控时报警 E-203") {
		t.Errorf("guest_link.share_text 应包含标题：%q", link.ShareText)
	}
	if !strings.Contains(link.ShareText, link.URL) {
		t.Errorf("guest_link.share_text 应包含链接：%q", link.ShareText)
	}
	// 指派给他人时，顶层 share_text（给被指派人看的那句）照旧，不与访客文案混用。
	if created.ShareText == nil || !strings.Contains(*created.ShareText, "/t/") {
		t.Errorf("顶层 share_text 应照旧给被指派人：%v", created.ShareText)
	}
	if strings.Contains(*created.ShareText, "/g/") {
		t.Errorf("顶层 share_text 不应混入访客链接：%q", *created.ShareText)
	}

	// 时间线依次是 created、assigned、guest_link_created。
	var got ticketResponseBody
	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读工单状态码 = %d", res.StatusCode)
	}
	var types []string
	for _, ev := range got.Timeline {
		types = append(types, ev.Type)
	}
	want := []string{"created", "assigned", "guest_link_created"}
	if len(types) != len(want) {
		t.Fatalf("时间线事件 = %v, 想 %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("时间线事件 = %v, 想 %v", types, want)
		}
	}
	for _, ev := range got.Timeline {
		if ev.Type == "guest_link_created" && ev.Note != "检验科-张三" {
			t.Errorf("guest_link_created 事件的备注 = %q, 想 检验科-张三", ev.Note)
		}
	}
	// 模板的预设标签照常打上；免维度标签（没打任何维度标签也建成了）。
	assertContains(t, got.Labels, "待初筛")

	// 链接可用：访客凭它读到工单，且带「可填写说明」权限。
	guest := s.GuestClient(strings.TrimPrefix(link.URL, "/g/"))
	var guestView ticketResponseBody
	res = s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &guestView)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客读工单状态码 = %d, 想 200", res.StatusCode)
	}
	if !guestView.Viewer.Can.EditBrief {
		t.Error("访客的 can.edit_brief 应为 true")
	}
	if guestView.Viewer.LinkNote == nil || *guestView.Viewer.LinkNote != "检验科-张三" {
		t.Errorf("访客 link_note = %v, 想 检验科-张三", guestView.Viewer.LinkNote)
	}
}

// 用「请人反馈」生成的链接，访客能读到工单并 PATCH 修订说明（自由格式的
// report 工单；模板工单的 fields 修订同一链接同样可用）。
func Test用请人反馈的链接能读工单并修订说明(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	// 自由格式：不用模板的 report 工单，说明为空。
	res := createWithGuestLink(t, s, web, map[string]any{
		"kind":  "report",
		"title": "生化仪打印异常",
		"guest_link": map[string]any{
			"note":           "检验科-李四",
			"can_edit_brief": true,
		},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}
	var created feedbackCreateResponse
	tbtest.DecodeJSON(t, res, &created)
	guest := s.GuestClient(strings.TrimPrefix(created.GuestLink.URL, "/g/"))

	// 访客能读到工单。
	var got ticketResponseBody
	res = s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客读工单状态码 = %d, 想 200", res.StatusCode)
	}
	if got.Kind != "report" || got.Template != nil {
		t.Errorf("自由格式应是 report 且不用模板：kind=%q template=%v", got.Kind, got.Template)
	}

	// 访客 PATCH 修订说明（带自报署名），版本推进到 2。
	res = s.Do(t, guest, http.MethodPatch, "/api/v1/tickets/"+created.Number, map[string]any{
		"brief_md":      "今早开机后打印机不吐纸，屏幕报 E-12。",
		"base_revision": 1,
		"author_name":   "李四",
	}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客修订说明状态码 = %d, 想 200", res.StatusCode)
	}
	res = s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("重读工单状态码 = %d", res.StatusCode)
	}
	if got.Brief.Revision != 2 || !strings.Contains(got.Brief.Markdown, "E-12") {
		t.Errorf("修订后应到第 2 版并写入新说明：%+v", got.Brief)
	}
	revised := 0
	for _, ev := range got.Timeline {
		if ev.Type == "brief_revised" {
			revised++
		}
	}
	if revised != 1 {
		t.Errorf("时间线上应有 1 条 brief_revised，实际 %d", revised)
	}
}

// 说明署名取最后一次修订说明的人：未修订时是创建者；访客修订后显示链接备注；
// 创建者再修订后回到成员。成员与访客读取同一份元数据（#129）。
func Test说明的最后修订者随修订人变化(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	res := createWithGuestLink(t, s, web, map[string]any{
		"kind":  "report",
		"title": "生化仪打印异常",
		"guest_link": map[string]any{
			"note":           "检验科-王工",
			"can_edit_brief": true,
		},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d，想 201", res.StatusCode)
	}
	var created feedbackCreateResponse
	tbtest.DecodeJSON(t, res, &created)
	guest := s.GuestClient(strings.TrimPrefix(created.GuestLink.URL, "/g/"))

	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	if got.Brief.LastRevised.Type != "member" || got.Brief.LastRevised.DisplayName != "陈工" ||
		got.Brief.LastRevised.Revision != 1 {
		t.Errorf("未修订时最后修订者应是创建者：%+v", got.Brief.LastRevised)
	}
	createdAt := got.Brief.LastRevised.At

	s.Clock().Advance(time.Minute)
	res = s.Do(t, guest, http.MethodPatch, "/api/v1/tickets/"+created.Number, map[string]any{
		"brief_md":      "今早开机后打印机不吐纸。",
		"base_revision": 1,
		"author_name":   "王工本人自报",
	}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客修订状态码 = %d，想 200", res.StatusCode)
	}

	s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	if got.Brief.LastRevised.Type != "guest" || got.Brief.LastRevised.DisplayName != "检验科-王工" ||
		got.Brief.LastRevised.Revision != 2 {
		t.Errorf("访客修订后最后修订者应显示链接备注：%+v", got.Brief.LastRevised)
	}
	if !strings.Contains(got.Brief.LastRevised.At, "T09:01:00+08:00") {
		t.Errorf("访客修订时间 = %q，想建单一分钟后", got.Brief.LastRevised.At)
	}

	s.Clock().Advance(time.Minute)
	res = s.Do(t, web, http.MethodPatch, "/api/v1/tickets/"+created.Number, map[string]any{
		"brief_md":      "成员补充了处理要求。",
		"base_revision": 2,
	}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("成员修订状态码 = %d，想 200", res.StatusCode)
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	if got.Brief.LastRevised.Type != "member" || got.Brief.LastRevised.DisplayName != "陈工" ||
		got.Brief.LastRevised.Revision != 3 || got.Brief.LastRevised.At == createdAt {
		t.Errorf("成员修订后最后修订者应回到成员：%+v（创建时间 %s）", got.Brief.LastRevised, createdAt)
	}
}

// 免维度标签只限「带 guest_link 且 can_edit_brief 为真」：带链接但没有
// 「可填写说明」权限、或不带链接时，照旧 422 dimension_label_required；
// 免维度时其余校验照常（标签仍须是字典里在用的值）。
func Test免维度标签只限带可填写说明权限的链接(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	for name, tc := range map[string]struct {
		body map[string]any
		code string
	}{
		"带链接但无可填写说明权限": {map[string]any{
			"kind": "report", "title": "x", "labels": []string{},
			"guest_link": map[string]any{"note": "检验科-张三", "can_edit_brief": false},
		}, "dimension_label_required"},
		"不带链接": {map[string]any{
			"kind": "report", "title": "x", "labels": []string{},
		}, "dimension_label_required"},
		"带链接可填写但标签非法": {map[string]any{
			"kind": "report", "title": "x", "labels": []string{"机型:BH-730"},
			"guest_link": map[string]any{"note": "检验科-张三", "can_edit_brief": true},
		}, "unknown_label_value"},
	} {
		res := createWithGuestLink(t, s, web, tc.body)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("[%s] 状态码 = %d, 想 422", name, res.StatusCode)
			continue
		}
		var got errorBodyWithExtra
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != tc.code {
			t.Errorf("[%s] 错误码 = %q, 想 %q", name, got.Error.Code, tc.code)
		}
	}

	// 对照：带 can_edit_brief 权限、没有维度标签 → 201。
	res := createWithGuestLink(t, s, web, map[string]any{
		"kind": "report", "title": "免维度的建单", "labels": []string{},
		"guest_link": map[string]any{"note": "检验科-张三", "can_edit_brief": true},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("带 can_edit_brief 权限应免维度标签，状态码 = %d, 想 201", res.StatusCode)
	}
}

// 建单带 guest_link 的校验失败：422 且不留下工单（校验在建单之前完成；
// 同一事务的另一半失败同样回滚）。note 必填复用单建链接的错误码。
func Test带链接建单校验失败返回422且不留下工单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	for name, tc := range map[string]struct {
		link map[string]any
		code string
	}{
		"非法天数":  {map[string]any{"note": "x", "valid_days": 14, "can_edit_brief": true}, "invalid_valid_days"},
		"永久零天":  {map[string]any{"note": "x", "valid_days": 0, "can_edit_brief": true}, "invalid_valid_days"},
		"带过期日期": {map[string]any{"note": "x", "expires_at": "2026-12-01", "can_edit_brief": true}, "invalid_valid_days"},
		"缺备注":   {map[string]any{"can_edit_brief": true}, "note_required"},
		"空白备注":  {map[string]any{"note": "  ", "can_edit_brief": true}, "note_required"},
	} {
		res := createWithGuestLink(t, s, web, map[string]any{
			"kind": "report", "title": "不该留下的工单", "labels": []string{},
			"guest_link": tc.link,
		})
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

	// 库里没有半张工单：全局第 1 张不存在。
	if res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/1", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("校验失败不应留下工单：GET #1 状态码 = %d, 想 404", res.StatusCode)
	}

	// 补上合法请求后，第一张工单从 001 开始（编号没有因失败的请求跳号）。
	res := createWithGuestLink(t, s, web, map[string]any{
		"kind": "report", "title": "补上的第一张", "labels": []string{},
		"guest_link": map[string]any{"note": "检验科-张三", "valid_days": 7, "can_edit_brief": true},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("补上的建单状态码 = %d, 想 201", res.StatusCode)
	}
	var created feedbackCreateResponse
	tbtest.DecodeJSON(t, res, &created)
	if created.Number != "20261016-001" {
		t.Errorf("工单编号 = %q, 想 20261016-001", created.Number)
	}
	if want := "2026-10-23T09:00:00+08:00"; created.GuestLink.ExpiresAt != want {
		t.Errorf("valid_days=7 的 expires_at = %q, 想 %q", created.GuestLink.ExpiresAt, want)
	}
}

// 「请人反馈」建单沿用幂等中间件：同一个 Idempotency-Key 重放返回首次结果
//（含只出现一次的链接地址），只有一张工单、一条链接。
func Test请人反馈建单的幂等重放只有一张工单一条链接(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	key := map[string]string{"Idempotency-Key": "feedback-once"}
	body := map[string]any{
		"kind": "report", "title": "网络抖动也只建一张", "labels": []string{},
		"guest_link": map[string]any{"note": "检验科-张三", "can_edit_brief": true},
	}
	first := s.DoWithHeaders(t, web, http.MethodPost, "/api/v1/tickets", key, body, nil)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("首次建单状态码 = %d, 想 201", first.StatusCode)
	}
	firstBody := readAll(t, first)
	second := s.DoWithHeaders(t, web, http.MethodPost, "/api/v1/tickets", key, body, nil)
	if second.StatusCode != http.StatusCreated || string(firstBody) != string(readAll(t, second)) {
		t.Fatalf("重放应逐字节返回首次结果：%d vs %d", first.StatusCode, second.StatusCode)
	}
	var created feedbackCreateResponse
	if err := json.Unmarshal(firstBody, &created); err != nil {
		t.Fatalf("建单响应不是合法 JSON：%s", firstBody)
	}

	// 只有一张工单：全局第 2 张不存在。
	if res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/2", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("重放不应建出第二张工单：GET #2 状态码 = %d, 想 404", res.StatusCode)
	}
	// 只有一条链接。
	var links struct {
		Links []struct {
			ID int64 `json:"id"`
		} `json:"links"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number+"/guest-links", nil, &links)
	if len(links.Links) != 1 {
		t.Errorf("重放不应再建访客链接，现有 %d 条", len(links.Links))
	}
	// 时间线上 guest_link_created 也只有一条。
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	events := 0
	for _, ev := range got.Timeline {
		if ev.Type == "guest_link_created" {
			events++
		}
	}
	if events != 1 {
		t.Errorf("时间线上应有 1 条 guest_link_created，实际 %d", events)
	}
}

// 配置了 public_url 时，建单响应里的 guest_link.url（以及转发文案里的链接）
// 是绝对地址（评审 P2-7，与单建链接一致）。
func Test请人反馈链接在配置publicUrl时是绝对地址(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithPublicURL("https://tb.example.com/"))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	res := createWithGuestLink(t, s, web, map[string]any{
		"kind": "report", "title": "绝对地址的链接", "labels": []string{},
		"guest_link": map[string]any{"note": "检验科-张三", "can_edit_brief": true},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}
	var created feedbackCreateResponse
	tbtest.DecodeJSON(t, res, &created)
	if !strings.HasPrefix(created.GuestLink.URL, "https://tb.example.com/g/tbg_") {
		t.Errorf("guest_link.url = %q, 想 https://tb.example.com/g/tbg_…", created.GuestLink.URL)
	}
	if !strings.Contains(created.GuestLink.ShareText, created.GuestLink.URL) {
		t.Errorf("share_text 应包含绝对链接：%q", created.GuestLink.ShareText)
	}
}
