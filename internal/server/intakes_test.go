package server_test

import (
	"net/http"
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
