package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// intakeView 是 GET /api/v1/intakes/{slug}（匿名）的响应结构。
type intakeView struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Kind     string `json:"kind"`
	Template struct {
		Key          string   `json:"key"`
		Name         string   `json:"name"`
		Version      int64    `json:"version"`
		Kind         string   `json:"kind"`
		PresetLabels []string `json:"preset_labels"`
		Fields       []struct {
			Key       string   `json:"key"`
			Title     string   `json:"title"`
			Guide     string   `json:"guide"`
			Example   string   `json:"example"`
			Required  bool     `json:"required"`
			Type      string   `json:"type"`
			Options   []string `json:"options"`
			Dimension string   `json:"dimension"`
		} `json:"fields"`
	} `json:"template"`
}

// createIntake 用成员凭证创建一个反馈入口，返回原始响应。
func createIntake(t testing.TB, s *tbtest.Server, c *http.Client, body map[string]any) *http.Response {
	t.Helper()
	return s.Do(t, c, http.MethodPost, "/api/v1/intakes", body, nil)
}

// intakeSubmitBody 是提交响应结构（测试侧）。
type intakeSubmitBody struct {
	Ticket struct {
		Number string `json:"number"`
		Seq    int64  `json:"seq"`
		Title  string `json:"title"`
		URL    string `json:"url"`
	} `json:"ticket"`
	GuestLink struct {
		URL          string `json:"url"`
		Note         string `json:"note"`
		ExpiresAt    string `json:"expires_at"`
		CanEditBrief bool   `json:"can_edit_brief"`
	} `json:"guest_link"`
	Next string `json:"next"`
}

// submitToIntake 匿名向入口提交一张反馈，返回响应结构（要求 201）。
func submitToIntake(t testing.TB, s *tbtest.Server, slug string, body map[string]any) intakeSubmitBody {
	t.Helper()
	res := s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/intakes/"+slug+"/tickets", body, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("入口提交状态码 = %d, 想 201", res.StatusCode)
	}
	var out intakeSubmitBody
	tbtest.DecodeJSON(t, res, &out)
	return out
}

// 匿名经入口提交反馈：拿到新工单和一条带「可填写说明」权限的 30 天访客链接；
// 预设标签与维度字段的值自动打上；创建者记为访客；时间线记 created 与
// guest_link_created；提交者用链接能读工单、也能修订说明（视同创建者）。
func Test经入口提交反馈拿到工单与访客链接(t *testing.T) {
	s := tbtest.Start(t) // 假时钟起点：2026-10-16 09:00（北京时间）
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})

	fields := bugFields()
	submitted := submitToIntake(t, s, "bug", map[string]any{
		"title":       "UD-2300 质控时报警 E-203",
		"fields":      fields,
		"author_name": "赵六",
	})
	if submitted.Ticket.Number != "20261016-001" || submitted.Ticket.Seq != 1 {
		t.Errorf("工单 = %q/#%d, 想 20261016-001/#1", submitted.Ticket.Number, submitted.Ticket.Seq)
	}
	if submitted.Ticket.Title != "UD-2300 质控时报警 E-203" {
		t.Errorf("响应里的标题 = %q", submitted.Ticket.Title)
	}
	// 交给提交者的访客链接：30 天、带「可填写说明」。
	if !strings.HasPrefix(submitted.GuestLink.URL, "/g/tbg_") {
		t.Errorf("访客链接 = %q, 想形如 /g/tbg_…", submitted.GuestLink.URL)
	}
	if !submitted.GuestLink.CanEditBrief {
		t.Error("返回的访客链接应带「可填写说明」权限")
	}
	if want := "2026-11-15T09:00:00+08:00"; submitted.GuestLink.ExpiresAt != want {
		t.Errorf("访客链接有效期 = %q, 想 %q（30 天）", submitted.GuestLink.ExpiresAt, want)
	}
	if submitted.GuestLink.Note != "反馈入口「部门 Bug 反馈」提交人" {
		t.Errorf("链接备注 = %q", submitted.GuestLink.Note)
	}

	// 成员视角：类型是反馈、预设标签与维度字段的值自动打上、创建者是访客。
	var member ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+submitted.Ticket.Number, nil, &member)
	if member.Kind != "report" {
		t.Errorf("kind = %q, 想 report（由入口的模板决定）", member.Kind)
	}
	for _, want := range []string{"待初筛", "机型:UD-2300"} {
		assertContains(t, member.Labels, want)
	}
	if member.Creator.Member != nil {
		t.Errorf("创建者应是访客（member 为 null）：%v", member.Creator.Member)
	}
	if member.Creator.LinkNote == nil || *member.Creator.LinkNote != "反馈入口「部门 Bug 反馈」提交人" {
		t.Errorf("创建者 link_note = %v, 想反馈入口备注", member.Creator.LinkNote)
	}
	if member.Creator.AuthorName == nil || *member.Creator.AuthorName != "赵六" {
		t.Errorf("创建者 author_name = %v, 想赵六（提交者自报）", member.Creator.AuthorName)
	}
	// 时间线：created（访客作者）与 guest_link_created，都推进变更序号。
	if len(member.Timeline) != 2 {
		t.Fatalf("时间线有 %d 条, 想 2（created + guest_link_created）：%+v", len(member.Timeline), member.Timeline)
	}
	if member.Timeline[0].Type != "created" || member.Timeline[1].Type != "guest_link_created" {
		t.Fatalf("事件类型 = %s/%s, 想 created/guest_link_created", member.Timeline[0].Type, member.Timeline[1].Type)
	}
	if member.Timeline[1].Note != "反馈入口「部门 Bug 反馈」提交人" {
		t.Errorf("guest_link_created 的备注 = %q", member.Timeline[1].Note)
	}
	if member.Timeline[1].Change <= member.Timeline[0].Change {
		t.Error("guest_link_created 的全局变更序号应大于 created")
	}

	// 提交者用这条链接读工单（访客视角：无 labels），并修订说明（视同创建者）。
	guest := s.GuestClient(submitted.GuestLink.URL[len("/g/"):])
	var asGuest ticketResponseBody
	s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+submitted.Ticket.Number, nil, &asGuest)
	if asGuest.Title != "UD-2300 质控时报警 E-203" {
		t.Errorf("访客读到的标题 = %q", asGuest.Title)
	}
	if asGuest.Labels != nil {
		t.Errorf("访客不应看到 labels：%v", asGuest.Labels)
	}
	if !asGuest.Viewer.Can.EditBrief {
		t.Error("提交者的 can.edit_brief 应为 true")
	}
	if asGuest.Viewer.Role != "creator" {
		t.Errorf("提交者的角色 = %q, 想 creator（视同创建者）", asGuest.Viewer.Role)
	}

	title := "UD-2300 质控时报警 E-203（已补充）"
	var revised struct {
		Revision int64 `json:"revision"`
	}
	res := s.Do(t, guest, http.MethodPatch, "/api/v1/tickets/"+submitted.Ticket.Number, map[string]any{
		"title":         title,
		"fields":        map[string]string{"你的猜测或补充？": "可能是液位传感器没识别到"},
		"base_revision": 1,
		"author_name":   "赵六",
	}, &revised)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("提交者修订说明状态码 = %d, 想 200", res.StatusCode)
	}
	if revised.Revision != 2 {
		t.Errorf("修订后版本 = %d, 想 2", revised.Revision)
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+submitted.Ticket.Number, nil, &member)
	if member.Title != title {
		t.Errorf("修订后的标题 = %q, 想 %q", member.Title, title)
	}
	if !strings.Contains(member.Brief.Markdown, "可能是液位传感器没识别到") {
		t.Errorf("修订后的说明应含新填的字段：\n%s", member.Brief.Markdown)
	}
	// 修订记入时间线，作者是访客（带链接备注与自报署名）。
	last := member.Timeline[len(member.Timeline)-1]
	if last.Type != "brief_revised" || last.Actor.Member != nil {
		t.Errorf("修订事件 = %s/%+v, 想 brief_revised、作者为访客", last.Type, last.Actor)
	}
	if last.Actor.AuthorName == nil || *last.Actor.AuthorName != "赵六" {
		t.Errorf("修订事件的 author_name = %v", last.Actor.AuthorName)
	}
}

// 成员创建反馈入口（绑定反馈类型的模板）；匿名打开入口拿到最新版模板与
// 维度候选值；不存在的入口 404 intake_not_found；管理入口只对成员开放。
func Test成员创建反馈入口匿名读到模板与维度候选值(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	anon := s.NewClient()

	res := createIntake(t, s, web, map[string]any{
		"slug":     "bug",
		"name":     "部门 Bug 反馈",
		"template": "bug-report",
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("创建入口状态码 = %d, 想 201", res.StatusCode)
	}

	// 匿名读取：入口信息 + 模板最新版本 + 维度候选值（来自字典，末尾带「不确定」）。
	var view intakeView
	res = s.Do(t, anon, http.MethodGet, "/api/v1/intakes/bug", nil, &view)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("匿名读入口状态码 = %d, 想 200", res.StatusCode)
	}
	if view.Slug != "bug" || view.Name != "部门 Bug 反馈" {
		t.Errorf("入口 slug/name = %q/%q, 想 bug/部门 Bug 反馈", view.Slug, view.Name)
	}
	if view.URL != "/i/bug" {
		t.Errorf("入口地址 = %q, 想 /i/bug", view.URL)
	}
	if view.Kind != "report" {
		t.Errorf("入口 kind = %q, 想 report（由模板决定）", view.Kind)
	}
	if view.Template.Key != "bug-report" || view.Template.Name != "Bug 反馈" {
		t.Errorf("模板 = %q/%q, 想 bug-report/Bug 反馈", view.Template.Key, view.Template.Name)
	}
	if view.Template.Version != 1 {
		t.Errorf("模板版本 = %d, 想 1", view.Template.Version)
	}
	containsString := func(list []string, want string) bool {
		for _, v := range list {
			if v == want {
				return true
			}
		}
		return false
	}
	if !containsString(view.Template.PresetLabels, "待初筛") {
		t.Errorf("预设标签里缺 待初筛：%v", view.Template.PresetLabels)
	}
	var machine *struct {
		Key       string   `json:"key"`
		Title     string   `json:"title"`
		Guide     string   `json:"guide"`
		Example   string   `json:"example"`
		Required  bool     `json:"required"`
		Type      string   `json:"type"`
		Options   []string `json:"options"`
		Dimension string   `json:"dimension"`
	}
	for i := range view.Template.Fields {
		if view.Template.Fields[i].Key == "哪台仪器？" {
			machine = &view.Template.Fields[i]
		}
	}
	if machine == nil {
		t.Fatalf("模板字段里没有「哪台仪器？」：%v", view.Template.Fields)
	}
	if machine.Type != "dimension" || machine.Dimension != "机型" || !machine.Required {
		t.Errorf("哪台仪器？应为 机型 维度的必填字段：%+v", machine)
	}
	if !containsString(machine.Options, "UD-2300") {
		t.Errorf("机型候选值里缺 UD-2300：%v", machine.Options)
	}
	if !containsString(machine.Options, "不确定") {
		t.Errorf("机型候选值末尾应带「不确定」：%v", machine.Options)
	}
	if machine.Guide == "" {
		t.Error("维度字段应带引导说明（guide）")
	}

	// 不存在（或已停用）的入口 404 intake_not_found。
	res = s.Do(t, anon, http.MethodGet, "/api/v1/intakes/no-such", nil, nil)
	var err errorBody
	tbtest.DecodeJSON(t, res, &err)
	if res.StatusCode != http.StatusNotFound || err.Error.Code != "intake_not_found" {
		t.Errorf("未知入口 = %d/%s, 想 404/intake_not_found", res.StatusCode, err.Error.Code)
	}

	// 管理侧：列表对成员可见，对匿名/访客不开放。
	var list struct {
		Intakes []struct {
			Slug     string `json:"slug"`
			Name     string `json:"name"`
			Template string `json:"template"`
			Active   bool   `json:"active"`
			URL      string `json:"url"`
		} `json:"intakes"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/intakes", nil, &list)
	if len(list.Intakes) != 1 || list.Intakes[0].Slug != "bug" || !list.Intakes[0].Active {
		t.Fatalf("入口列表 = %+v, 想 1 个活跃的 bug", list.Intakes)
	}
	if list.Intakes[0].Template != "bug-report" {
		t.Errorf("列表项 template = %q, 想 bug-report", list.Intakes[0].Template)
	}
	res = s.Do(t, anon, http.MethodGet, "/api/v1/intakes", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("匿名读入口列表 = %d, 想 401", res.StatusCode)
	}
	res = createIntake(t, s, anon, map[string]any{"slug": "x", "template": "bug-report"})
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("匿名创建入口 = %d, 想 401", res.StatusCode)
	}
}

// 入口提交是严格校验：缺必填项 422 missing_fields 拦下（与成员建单只 warnings
// 相反），hint 逐项说明缺什么、怎么填；「不确定」算已填；结构性错误照样拦。
func Test入口提交缺必填被拦下且逐项提示(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})

	// 两个必填没填：哪台仪器？（维度）与 你做了哪些操作？（文本）。
	res := s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/intakes/bug/tickets", map[string]any{
		"title": "UD-2300 报警",
		"fields": map[string]string{
			"什么时候发生的？": "今天上午",
			"实际看到了什么？": "弹出“E-203”",
			"你期望看到什么？": "正常开始",
			"出现得频繁吗？":  "每次",
		},
		"author_name": "赵六",
	}, nil)
	var err intakeErrorBody
	tbtest.DecodeJSON(t, res, &err)
	if res.StatusCode != http.StatusUnprocessableEntity || err.Error.Code != "missing_fields" {
		t.Fatalf("缺必填 = %d/%s, 想 422/missing_fields", res.StatusCode, err.Error.Code)
	}
	byField := map[string]struct {
		Hint    string `json:"hint"`
		Example string `json:"example"`
	}{}
	for _, f := range err.Error.Fields {
		byField[f.Field] = struct {
			Hint    string `json:"hint"`
			Example string `json:"example"`
		}{f.Hint, f.Example}
	}
	if len(err.Error.Fields) != 2 {
		t.Fatalf("missing fields = %+v, 想恰好两项", err.Error.Fields)
	}
	if h, ok := byField["哪台仪器？"]; !ok {
		t.Errorf("缺「哪台仪器？」的提示：%+v", err.Error.Fields)
	} else {
		if !strings.Contains(h.Hint, "不确定") || !strings.Contains(h.Hint, "机型") {
			t.Errorf("「哪台仪器？」的提示应说明可从机型里选、可选「不确定」：%q", h.Hint)
		}
	}
	if h, ok := byField["你做了哪些操作？"]; !ok {
		t.Errorf("缺「你做了哪些操作？」的提示：%+v", err.Error.Fields)
	} else {
		if h.Hint == "" {
			t.Error("「你做了哪些操作？」的提示不能为空")
		}
		if !strings.Contains(h.Example, "开机自检") {
			t.Errorf("提示应带上模板里的示例：%q", h.Example)
		}
	}
	// 汇总 hint 逐项说明（每一项都出现）。
	for _, want := range []string{"哪台仪器？", "你做了哪些操作？"} {
		if !strings.Contains(err.Error.Hint, want) {
			t.Errorf("hint 里应提到 %q：\n%s", want, err.Error.Hint)
		}
	}
	// 被拦下时不产生工单。
	var list struct {
		Intakes []struct {
			Active bool `json:"active"`
		} `json:"intakes"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/intakes", nil, &list)
	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/1", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("被拦下的提交不应产生工单（/tickets/1 = %d, 想 404）", res.StatusCode)
	}

	// 「不确定」算已填（维度字段不打标签，但不再报 missing_fields）。
	fields := bugFields()
	fields["哪台仪器？"] = "不确定"
	fields["你做了哪些操作？"] = "1. 开机 2. 自检"
	fields["出现得频繁吗？"] = "每次" // 单选字段用合法选项
	for _, k := range []string{"序列号和软件版本是多少？", "实际看到了什么？", "你期望看到什么？"} {
		fields[k] = "略"
	}
	res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/intakes/bug/tickets",
		map[string]any{"title": "不知道机型的报警", "fields": fields}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("「不确定」算已填：状态码 = %d, 想 201", res.StatusCode)
	}
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/1", nil, &got)
	for _, l := range got.Labels {
		if strings.HasPrefix(l, "机型:") {
			t.Errorf("「不确定」不应打机型标签：%v", got.Labels)
		}
	}

	// 没有标题 → 422 title_required；结构性错误：未知字段键、单选值不在选项里、
	// 维度值不在字典里（带 suggestions）。
	cases := []struct {
		name string
		body map[string]any
		code string
	}{
		{"无标题", map[string]any{"fields": bugFields()}, "title_required"},
		{"未知字段", map[string]any{"title": "t", "fields": withExtra(bugFields(), "不存在的问句？", "x")}, "unknown_field"},
		{"单选值不在选项里", map[string]any{"title": "t", "fields": withExtra(bugFields(), "出现得频繁吗？", "偶尔中的偶尔")}, "invalid_field_option"},
		{"维度值不在字典里", map[string]any{"title": "t", "fields": withExtra(bugFields(), "哪台仪器？", "BH-9999")}, "unknown_label_value"},
	}
	for _, tc := range cases {
		res := s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/intakes/bug/tickets", tc.body, nil)
		var e intakeErrorBody
		tbtest.DecodeJSON(t, res, &e)
		if res.StatusCode != http.StatusUnprocessableEntity || e.Error.Code != tc.code {
			t.Errorf("%s = %d/%s, 想 422/%s", tc.name, res.StatusCode, e.Error.Code, tc.code)
		}
	}
}

// withExtra 返回加了额外字段值的副本（不影响原 map）。
func withExtra(fields map[string]string, key, value string) map[string]string {
	out := map[string]string{}
	for k, v := range fields {
		out[k] = v
	}
	out[key] = value
	return out
}

// intakeErrorBody 是入口接口可能带附加字段（missing_fields 的 fields、
// unknown_label_value 的 suggestions）的错误结构。
type intakeErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
		Fields  []struct {
			Field   string `json:"field"`
			Hint    string `json:"hint"`
			Example string `json:"example"`
		} `json:"fields"`
		Suggestions []string `json:"suggestions"`
	} `json:"error"`
}

// 停用的入口和轮换前的旧 slug 都不能再提交（也不能再匿名读取）；
// 轮换后的新 slug 立即可用，已提交的工单不受影响。
func Test停用的入口与旧slug不能再提交(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})
	submitted := submitToIntake(t, s, "bug", map[string]any{
		"title": "第一张反馈", "fields": bugFields(),
	})

	// 停用：匿名读与提交都按不存在处理；列表里带 active: false。
	res := s.Do(t, web, http.MethodPost, "/api/v1/intakes/bug/deactivate", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("停用状态码 = %d, 想 200", res.StatusCode)
	}
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/intakes/bug"},
		{http.MethodPost, "/api/v1/intakes/bug/tickets"},
	} {
		res := s.Do(t, s.NewClient(), tc.method, tc.path, map[string]any{"title": "x", "fields": bugFields()}, nil)
		var e intakeErrorBody
		tbtest.DecodeJSON(t, res, &e)
		if res.StatusCode != http.StatusNotFound || e.Error.Code != "intake_not_found" {
			t.Errorf("%s 停用后 = %d/%s, 想 404/intake_not_found", tc.method, res.StatusCode, e.Error.Code)
		}
	}
	// 再次停用幂等。
	res = s.Do(t, web, http.MethodPost, "/api/v1/intakes/bug/deactivate", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Errorf("重复停用 = %d, 想 200（幂等）", res.StatusCode)
	}

	// 轮换：先再建一个入口验证轮换路径（上面的已停用，轮换一个活跃的）。
	createIntake(t, s, web, map[string]any{"slug": "check", "name": "检验科反馈", "template": "bug-report"})
	rotated := submitToIntake(t, s, "check", map[string]any{
		"title": "轮换前提交", "fields": bugFields(),
	})
	res = s.Do(t, web, http.MethodPost, "/api/v1/intakes/check/rotate", map[string]any{"slug": "check-2027"}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("轮换状态码 = %d, 想 200", res.StatusCode)
	}
	// 旧 slug 立即失效，新 slug 可用。
	res = s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/intakes/check", nil, nil)
	var e intakeErrorBody
	tbtest.DecodeJSON(t, res, &e)
	if res.StatusCode != http.StatusNotFound || e.Error.Code != "intake_not_found" {
		t.Errorf("旧 slug 读取 = %d/%s, 想 404/intake_not_found", res.StatusCode, e.Error.Code)
	}
	res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/intakes/check/tickets",
		map[string]any{"title": "x", "fields": bugFields()}, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("旧 slug 提交 = %d, 想 404", res.StatusCode)
	}
	res = s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/intakes/check-2027", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Errorf("新 slug 读取 = %d, 想 200", res.StatusCode)
	}
	submitToIntake(t, s, "check-2027", map[string]any{"title": "新 slug 提交", "fields": bugFields()})

	// 轮换到已占用的 slug → 409 slug_taken；非法 slug → 422 invalid_slug。
	res = s.Do(t, web, http.MethodPost, "/api/v1/intakes/check-2027/rotate", map[string]any{"slug": "bug"}, nil)
	var e2 intakeErrorBody
	tbtest.DecodeJSON(t, res, &e2)
	if res.StatusCode != http.StatusConflict || e2.Error.Code != "slug_taken" {
		t.Errorf("占用 slug = %d/%s, 想 409/slug_taken", res.StatusCode, e2.Error.Code)
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/intakes/check-2027/rotate", map[string]any{"slug": "检验科 反馈"}, nil)
	tbtest.DecodeJSON(t, res, &e2)
	if res.StatusCode != http.StatusUnprocessableEntity || e2.Error.Code != "invalid_slug" {
		t.Errorf("非法 slug = %d/%s, 想 422/invalid_slug", res.StatusCode, e2.Error.Code)
	}

	// 停用与轮换不影响已经提交的工单和提交者手里的访客链接。
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+rotated.Ticket.Number, nil, &got)
	if got.Title != "轮换前提交" {
		t.Errorf("已提交的工单不受停用/轮换影响：%q", got.Title)
	}
	res = s.Do(t, s.GuestClient(rotated.GuestLink.URL[len("/g/"):]),
		http.MethodGet, "/api/v1/tickets/"+rotated.Ticket.Number, nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Errorf("提交者的访客链接在停用/轮换后应继续可用：%d", res.StatusCode)
	}
	// 第一张反馈的链接同样可用。
	res = s.Do(t, s.GuestClient(submitted.GuestLink.URL[len("/g/"):]),
		http.MethodGet, "/api/v1/tickets/"+submitted.Ticket.Number, nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Errorf("提交者的访客链接在入口停用后应继续可用：%d", res.StatusCode)
	}
}

// 用非反馈类型的模板绑定入口时被拒绝；不存在的模板与已停用的模板同样被拒；
// 入口创建本身的参数校验（slug）与占用冲突。
func Test绑定非反馈类型的模板被拒绝(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	cases := []struct {
		name   string
		body   map[string]any
		status int
		code   string
	}{
		{"任务模板", map[string]any{"slug": "task", "name": "派活", "template": "assign-task"},
			http.StatusUnprocessableEntity, "template_not_report"},
		{"问题咨询模板（也是任务）", map[string]any{"slug": "ask", "name": "咨询", "template": "question"},
			http.StatusUnprocessableEntity, "template_not_report"},
		{"不存在的模板", map[string]any{"slug": "x", "name": "x", "template": "no-such"},
			http.StatusNotFound, "template_not_found"},
		{"没写名称", map[string]any{"slug": "y", "template": "bug-report"},
			http.StatusUnprocessableEntity, "name_required"},
		{"没写 slug", map[string]any{"name": "n", "template": "bug-report"},
			http.StatusUnprocessableEntity, "slug_required"},
		{"非法 slug", map[string]any{"slug": "Bug 反馈", "name": "n", "template": "bug-report"},
			http.StatusUnprocessableEntity, "invalid_slug"},
	}
	for _, tc := range cases {
		res := createIntake(t, s, web, tc.body)
		var e intakeErrorBody
		tbtest.DecodeJSON(t, res, &e)
		if res.StatusCode != tc.status || e.Error.Code != tc.code {
			t.Errorf("%s = %d/%s, 想 %d/%s", tc.name, res.StatusCode, e.Error.Code, tc.status, tc.code)
		}
	}

	// 正常创建后，slug 不能复用（409 slug_taken）。
	res := createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("创建反馈入口 = %d, 想 201", res.StatusCode)
	}
	res = createIntake(t, s, web, map[string]any{"slug": "bug", "name": "另一个", "template": "bug-report"})
	var e intakeErrorBody
	tbtest.DecodeJSON(t, res, &e)
	if res.StatusCode != http.StatusConflict || e.Error.Code != "slug_taken" {
		t.Errorf("重复 slug = %d/%s, 想 409/slug_taken", res.StatusCode, e.Error.Code)
	}

	// 停用模板后不能再新建入口，已有入口也不能再提交（始终用最新版，且要在用）。
	res = s.Do(t, web, http.MethodPost, "/api/v1/templates/bug-report/deactivate", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("停用模板状态码 = %d", res.StatusCode)
	}
	res = createIntake(t, s, web, map[string]any{"slug": "bug2", "name": "再一个", "template": "bug-report"})
	tbtest.DecodeJSON(t, res, &e)
	if res.StatusCode != http.StatusUnprocessableEntity || e.Error.Code != "template_inactive" {
		t.Errorf("停用模板绑定 = %d/%s, 想 422/template_inactive", res.StatusCode, e.Error.Code)
	}
	res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/intakes/bug/tickets",
		map[string]any{"title": "x", "fields": bugFields()}, nil)
	tbtest.DecodeJSON(t, res, &e)
	if res.StatusCode != http.StatusUnprocessableEntity || e.Error.Code != "template_inactive" {
		t.Errorf("停用模板后入口提交 = %d/%s, 想 422/template_inactive", res.StatusCode, e.Error.Code)
	}
}

// 入口始终使用绑定模板的最新版本：模板修改出新版本后，匿名读取回显新版本，
// 提交按新版本的字段校验；已提交的工单仍保存创建时版本的快照。
func Test入口始终使用模板的最新版本(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})

	// 新版本把「你的猜测或补充？」改成必填（其余字段保持不变）。
	markdown := "---\n名称: Bug 反馈\n类型: 反馈\n预设标签: [待初筛]\n---\n" +
		"## 哪台仪器？ {维度:机型 必填}\n铭牌上有型号。\n" +
		"## 序列号和软件版本是多少？\n设置里可以看到。\n" +
		"## 什么时候发生的？ {必填}\n大概时间。\n" +
		"## 你做了哪些操作？ {必填}\n按顺序写。\n" +
		"## 实际看到了什么？ {必填}\n原样照抄。\n" +
		"## 你期望看到什么？ {必填}\n本来应该发生什么。\n" +
		"## 出现得频繁吗？ {单选: 每次 | 经常 | 偶尔 | 仅一次 必填}\n" +
		"## 对工作有影响吗？ {单选: 无法出报告 | 能绕过 | 不影响}\n" +
		"## 你的猜测或补充？ {必填}\n猜错也没关系。\n"
	res := s.Do(t, web, http.MethodPatch, "/api/v1/templates/bug-report", map[string]any{"markdown": markdown}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("修改模板状态码 = %d, 想 200", res.StatusCode)
	}

	var view intakeView
	s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/intakes/bug", nil, &view)
	if view.Template.Version != 2 {
		t.Errorf("入口应回显模板最新版本：version = %d, 想 2", view.Template.Version)
	}
	found := false
	for _, f := range view.Template.Fields {
		if f.Key == "你的猜测或补充？" {
			found = true
			if !f.Required {
				t.Error("新版本里「你的猜测或补充？」应是必填")
			}
		}
	}
	if !found {
		t.Fatalf("新版本的字段没出现在入口里：%v", view.Template.Fields)
	}

	// 按新版本校验：没填新的必填项 → missing_fields 提到这一项。
	res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/intakes/bug/tickets", map[string]any{
		"title": "新版本下提交", "fields": bugFields(), // bugFields 没填「你的猜测或补充？」
	}, nil)
	var e intakeErrorBody
	tbtest.DecodeJSON(t, res, &e)
	if res.StatusCode != http.StatusUnprocessableEntity || e.Error.Code != "missing_fields" {
		t.Fatalf("新版本校验 = %d/%s, 想 422/missing_fields", res.StatusCode, e.Error.Code)
	}
	if len(e.Error.Fields) != 1 || e.Error.Fields[0].Field != "你的猜测或补充？" {
		t.Errorf("缺的应是新必填项：%+v", e.Error.Fields)
	}
	fields := withExtra(bugFields(), "你的猜测或补充？", "可能是传感器")
	submitted := submitToIntake(t, s, "bug", map[string]any{"title": "新版本下提交", "fields": fields})

	// 匿名读取也提供 ?format=markdown 的填写指南（给访客的 Agent 自查，用户故事 86）。
	res = s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/intakes/bug?format=markdown", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("填写指南状态码 = %d", res.StatusCode)
	}
	body := string(readAll(t, res))
	for _, want := range []string{"你的猜测或补充？", "不确定", "模板"} {
		if !strings.Contains(body, want) {
			t.Errorf("填写指南里应包含 %q：\n%s", want, body)
		}
	}

	// 提交后的工单保留第 2 版的快照：修订时按第 2 版字段校验。
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+submitted.Ticket.Number, nil, &got)
	if got.Brief.Template == nil || *got.Brief.Template != "bug-report" {
		t.Fatalf("工单的模板 = %v", got.Brief.Template)
	}
}

// 入口提交支持 Idempotency-Key：同一个 key 的重放返回首次结果，不重复建单。
func Test入口提交的幂等重放(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})

	headers := map[string]string{"Idempotency-Key": "submit-once"}
	body := map[string]any{"title": "网络抖动重试", "fields": bugFields(), "author_name": "赵六"}
	first := s.DoWithHeaders(t, s.NewClient(), http.MethodPost, "/api/v1/intakes/bug/tickets", headers, body, nil)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("首次提交 = %d, 想 201", first.StatusCode)
	}
	firstBody := readAll(t, first)
	replay := s.DoWithHeaders(t, s.NewClient(), http.MethodPost, "/api/v1/intakes/bug/tickets", headers, body, nil)
	if replay.StatusCode != http.StatusCreated {
		t.Fatalf("重放状态码 = %d, 想 201", replay.StatusCode)
	}
	if got := readAll(t, replay); string(got) != string(firstBody) {
		t.Errorf("重放应逐字节返回首次结果：\n%s\n%s", firstBody, got)
	}
	// 只建了一张工单（#2 不存在）。
	res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/2", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("重放不应再建单（/tickets/2 = %d, 想 404）", res.StatusCode)
	}
}

// 对外主线：入口提交 → 初筛（成员回复追问）轮到访客 → 访客回复后轮回本部门。
// 提交者（角色 creator，视同创建者）的 Markdown「下一步」按访客给指引。
func Test入口提交的反馈在初筛与访客之间轮转(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})
	submitted := submitToIntake(t, s, "bug", map[string]any{
		"title": "UD-2300 报警", "fields": bugFields(), "author_name": "赵六",
	})
	guest := s.GuestClient(submitted.GuestLink.URL[len("/g/"):])

	// 尚无回复：反馈轮到本部门（未指派，原因「尚未指派」）。
	var asMember ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+submitted.Ticket.Number, nil, &asMember)
	if asMember.WaitingOn.Party != "department" {
		t.Errorf("刚提交的反馈应轮到本部门：%+v", asMember.WaitingOn)
	}

	// 初筛 Agent 用普通回复逐项追问（不改写说明），之后轮到访客。
	var reply struct {
		WaitingOn struct {
			Party string `json:"party"`
		} `json:"waiting_on"`
	}
	res := s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+submitted.Ticket.Number+"/replies",
		map[string]any{"body_md": "请补充试剂批号。"}, &reply)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("成员回复状态码 = %d", res.StatusCode)
	}
	if reply.WaitingOn.Party != "guest" {
		t.Errorf("成员回复后应轮到访客：%+v", reply.WaitingOn)
	}
	var asGuest ticketResponseBody
	s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+submitted.Ticket.Number, nil, &asGuest)
	if !asGuest.Viewer.YourTurn {
		t.Error("成员追问后应轮到提交者（your_turn = true）")
	}
	if asGuest.Viewer.Role != "creator" {
		t.Errorf("提交者角色 = %q, 想 creator", asGuest.Viewer.Role)
	}
	// Markdown 视角：发起人记为访客（经入口链接），「下一步」按访客给回复指引。
	res = s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+submitted.Ticket.Number+"?format=markdown", nil, nil)
	md := string(readAll(t, res))
	for _, want := range []string{
		"赵六（经「反馈入口「部门 Bug 反馈」提交人」链接）",
		"现在轮到你（访客）回复",
		"author_name",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("提交者的 Markdown 里应包含 %q：\n%s", want, md)
		}
	}

	// 访客补充（回复自报署名），之后轮回本部门（待初筛队列又能查到它）。
	res = s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+submitted.Ticket.Number+"/replies",
		map[string]any{"body_md": "批号 20260901。", "author_name": "赵六"}, &reply)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("访客回复状态码 = %d", res.StatusCode)
	}
	if reply.WaitingOn.Party != "department" {
		t.Errorf("访客回复后应轮回本部门：%+v", reply.WaitingOn)
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+submitted.Ticket.Number, nil, &asMember)
	if asMember.WaitingOn.Reason != "访客回复了" {
		t.Errorf("轮回本部门的原因 = %q, 想 访客回复了", asMember.WaitingOn.Reason)
	}
}
