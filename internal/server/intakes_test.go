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
