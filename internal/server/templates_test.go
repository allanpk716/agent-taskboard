package server_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// templateSummary 是 GET /api/v1/templates 列表项的解码结构。
type templateSummary struct {
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Version      int64    `json:"version"`
	Active       bool     `json:"active"`
	PresetLabels []string `json:"preset_labels"`
	URL          string   `json:"url"`
}

type templateFieldBody struct {
	Key           string              `json:"key"`
	Title         string              `json:"title"`
	Guide         string              `json:"guide"`
	Example       string              `json:"example"`
	Required      bool                `json:"required"`
	Type          string              `json:"type"`
	Options       []string            `json:"options"`
	Dimension     string              `json:"dimension"`
	OptionAliases map[string][]string `json:"option_aliases"` // #39：下拉框按别名搜索
}

// templateBody 是 GET /api/v1/templates/{key} 的解码结构。
type templateBody struct {
	Key          string              `json:"key"`
	Name         string              `json:"name"`
	Kind         string              `json:"kind"`
	Version      int64               `json:"version"`
	Active       bool                `json:"active"`
	PresetLabels []string            `json:"preset_labels"`
	AgentNote    string              `json:"agent_note"`
	Fields       []templateFieldBody `json:"fields"`
	Markdown     string              `json:"markdown"`
	UpdatedBy    *authorBody         `json:"updated_by"`
}

// 上线时预置三份模板（spec 用户故事 36）：Bug 反馈（预设 待初筛）、分配任务、
// 问题咨询。列表对成员和成员 Agent 都开放。
func Test预置三份模板(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	var list struct {
		Templates []templateSummary `json:"templates"`
	}
	res := s.Do(t, web, http.MethodGet, "/api/v1/templates", nil, &list)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("列表状态码 = %d, 想 200", res.StatusCode)
	}
	if len(list.Templates) != 3 {
		t.Fatalf("预置模板数 = %d, 想 3：%+v", len(list.Templates), list.Templates)
	}
	byKey := map[string]templateSummary{}
	for _, tpl := range list.Templates {
		byKey[tpl.Key] = tpl
	}
	bug := byKey["bug-report"]
	if bug.Name != "Bug 反馈" || bug.Kind != "report" || !bug.Active || bug.Version != 1 {
		t.Errorf("bug-report 不对：%+v", bug)
	}
	if len(bug.PresetLabels) != 1 || bug.PresetLabels[0] != "待初筛" {
		t.Errorf("bug-report 预设标签 = %v, 想 [待初筛]", bug.PresetLabels)
	}
	if bug.URL != "/api/v1/templates/bug-report" {
		t.Errorf("bug-report url = %q", bug.URL)
	}
	if assign := byKey["assign-task"]; assign.Name != "分配任务" || assign.Kind != "task" {
		t.Errorf("assign-task 不对：%+v", assign)
	}
	if q := byKey["question"]; q.Name != "问题咨询" || q.Kind != "task" {
		t.Errorf("question 不对：%+v", q)
	}

	// 成员 Agent 也能读模板（spec 用户故事 51）。
	_, token := s.IssueAgentToken(t, web, "笔记本")
	res = s.Do(t, s.AgentClient(token), http.MethodGet, "/api/v1/templates", nil, &list)
	if res.StatusCode != http.StatusOK || len(list.Templates) != 3 {
		t.Fatalf("Agent 读模板列表状态码 = %d, 数量 = %d, 想 200 / 3", res.StatusCode, len(list.Templates))
	}
}

// 读一份模板：字段定义齐全；维度字段带上字典里的候选值和「不确定」选项。
func Test读模板维度字段带候选值和不确定(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	var got templateBody
	res := s.Do(t, web, http.MethodGet, "/api/v1/templates/bug-report", nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读模板状态码 = %d, 想 200", res.StatusCode)
	}
	if got.Name != "Bug 反馈" || got.Kind != "report" {
		t.Errorf("模板头不对：%+v", got)
	}
	fields := map[string]templateFieldBody{}
	for _, f := range got.Fields {
		fields[f.Key] = f
	}
	model, ok := fields["哪台仪器？"]
	if !ok {
		t.Fatalf("Bug 反馈应有「哪台仪器？」字段：%+v", got.Fields)
	}
	if model.Type != "dimension" || model.Dimension != "机型" || !model.Required {
		t.Errorf("机型字段不对：%+v", model)
	}
	// 候选值来自字典（种子里的机型），末尾带「不确定」。
	assertContains(t, model.Options, "UD-2300")
	assertContains(t, model.Options, "BM-10vet")
	if model.Options[len(model.Options)-1] != "不确定" {
		t.Errorf("候选值末尾应是 不确定：%v", model.Options)
	}
	freq, ok := fields["出现得频繁吗？"]
	if !ok {
		t.Fatalf("Bug 反馈应有「出现得频繁吗？」字段：%+v", got.Fields)
	}
	if freq.Type != "radio" || !freq.Required {
		t.Errorf("频率字段不对：%+v", freq)
	}
	assertContains(t, freq.Options, "每次")
	assertContains(t, freq.Options, "仅一次")
	// 必填/选填、引导与示例都在字段定义里。
	steps, _ := fields["你做了哪些操作？"]
	if !steps.Required || steps.Guide == "" || steps.Example == "" {
		t.Errorf("操作步骤字段不对：%+v", steps)
	}
	ver, _ := fields["序列号和软件版本是多少？"]
	if ver.Required {
		t.Errorf("序列号/版本应为选填：%+v", ver)
	}
	if got.AgentNote == "" {
		t.Error("Bug 反馈应预置「给 Agent 的说明」")
	}
	if !strings.Contains(got.Markdown, "名称: Bug 反馈") {
		t.Errorf("响应应带回模板原文 markdown：%q", got.Markdown)
	}

	// 分配任务模板：member-handoff 原型的六字段 + 给 Agent 的说明。
	var assign templateBody
	s.Do(t, web, http.MethodGet, "/api/v1/templates/assign-task", nil, &assign)
	assignFields := map[string]bool{}
	for _, f := range assign.Fields {
		assignFields[f.Key] = true
	}
	for _, want := range []string{"要达成什么目标？", "为什么要做？有什么背景？", "交付什么？", "怎样算完成（验收标准）？", "期限？", "相关资料在哪里？"} {
		if !assignFields[want] {
			t.Errorf("分配任务缺少字段 %q：%v", want, assignFields)
		}
	}
	if assign.AgentNote == "" {
		t.Error("分配任务应预置「给 Agent 的说明」")
	}

	// 问题咨询模板：想问什么/背景/已经试过什么/期望回复时间。
	var question templateBody
	s.Do(t, web, http.MethodGet, "/api/v1/templates/question", nil, &question)
	questionFields := map[string]bool{}
	for _, f := range question.Fields {
		questionFields[f.Key] = true
	}
	for _, want := range []string{"想问什么？", "有什么背景？", "已经试过什么？", "期望什么时候得到回复？"} {
		if !questionFields[want] {
			t.Errorf("问题咨询缺少字段 %q：%v", want, questionFields)
		}
	}

	// 不存在的模板：404 template_not_found。
	res = s.Do(t, web, http.MethodGet, "/api/v1/templates/no-such", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("未知模板状态码 = %d, 想 404", res.StatusCode)
	}
	var errBody errorBody
	tbtest.DecodeJSON(t, res, &errBody)
	if errBody.Error.Code != "template_not_found" {
		t.Errorf("错误码 = %q, 想 template_not_found", errBody.Error.Code)
	}
}

// ?format=markdown 给 Agent 的填写指南：问句、引导、示例、候选值，
// 以及「给 Agent 的说明」（验收标准）。
func Test模板填写指南包含给Agent的说明(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)

	read := func(c *http.Client) string {
		t.Helper()
		res := s.Do(t, c, http.MethodGet, "/api/v1/templates/bug-report?format=markdown", nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("指南状态码 = %d, 想 200", res.StatusCode)
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
			t.Fatalf("Content-Type = %q, 想 text/markdown", ct)
		}
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}

	md := read(agent)
	for _, want := range []string{
		"# 模板 Bug 反馈（bug-report）",
		"- 类型：反馈 · 版本：1",
		"- 预设标签：待初筛",
		`{"template": "bug-report"`,
		"## 哪台仪器？ {维度:机型 必填}",
		"在仪器背面铭牌上可以看到型号",
		"- 候选值（维度 机型）：",
		"不确定",
		"## 你做了哪些操作？ {必填}",
		"> 示例：",
		"## 出现得频繁吗？ {单选: 每次 | 经常 | 偶尔 | 仅一次 必填}",
		"## 给 Agent 的说明",
		"POST /api/v1/tickets",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("填写指南缺少 %q：\n%s", want, md)
		}
	}
	// 成员本人也能读同一份指南。
	if md2 := read(web); md2 != md {
		t.Errorf("成员与 Agent 读到的指南应相同")
	}
}

// 未登录不能读模板。
func Test未登录读模板返回401(t *testing.T) {
	s := tbtest.Start(t)
	res := s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/templates", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录列表状态码 = %d, 想 401", res.StatusCode)
	}
	res = s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/templates/bug-report", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录读模板状态码 = %d, 想 401", res.StatusCode)
	}
}


// #39：补全说明与反馈入口提交页共用的字段表单需要维度字段的候选值与别名
//（下拉框按别名搜索）。工单视图（访客用「补全说明」）此前只回字段快照、不带
// 候选值；匿名入口视图与成员模板视图此前只有候选值、没有别名。

// 持「可填写说明」链接的访客读工单：brief.template_fields 里维度字段带上
// 字典候选值、「不确定」和各候选值的别名（别名随字典变化，读取时现取）。
func Test访客读工单说明字段带维度候选值与别名(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	// 给 UD-2300 挂一个别名（下拉框输入它能搜到标准值）。
	res := s.Do(t, web, http.MethodPost, "/api/v1/labels/aliases",
		map[string]any{"label": "机型:UD-2300", "alias": "2300血球仪"}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("加别名状态码 = %d, 想 201", res.StatusCode)
	}

	// 建一张 Bug 反馈模板工单（维度字段留空），发一条带「可填写说明」的链接。
	created := s.Do(t, web, http.MethodPost, "/api/v1/tickets", map[string]any{
		"template": "bug-report",
		"title":    "UD-2300 质控时报警 E-203",
		"fields":   map[string]string{"哪台仪器？": "UD-2300", "什么时候发生的？": "今天上午 9 点左右"},
	}, nil)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", created.StatusCode)
	}
	var createBody struct {
		Number string `json:"number"`
	}
	tbtest.DecodeJSON(t, created, &createBody)

	link := s.CreateGuestLink(t, web, createBody.Number, map[string]any{
		"note": "检验科-张三", "valid_days": 30, "can_edit_brief": true,
	})
	guest := s.GuestClient(link.Key(t))

	var view ticketResponseBody
	got := s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+createBody.Number, nil, &view)
	if got.StatusCode != http.StatusOK {
		t.Fatalf("访客读工单状态码 = %d, 想 200", got.StatusCode)
	}
	if view.Brief.TemplateFields == nil {
		t.Fatal("模板工单应带 brief.template_fields")
	}
	model := findField(t, view.Brief.TemplateFields, "哪台仪器？")
	if model.Type != "dimension" {
		t.Fatalf("「哪台仪器？」应是维度字段：%+v", model)
	}
	assertContains(t, model.Options, "UD-2300")
	assertContains(t, model.Options, "BM-10vet")
	if len(model.Options) == 0 || model.Options[len(model.Options)-1] != "不确定" {
		t.Errorf("候选值末尾应是「不确定」，得到 %v", model.Options)
	}
	if got := model.OptionAliases["UD-2300"]; len(got) != 1 || got[0] != "2300血球仪" {
		t.Errorf("UD-2300 的别名 = %v, 想 [2300血球仪]", got)
	}
}

// 匿名读反馈入口：模板的维度字段同样带候选值、「不确定」与别名（提交页
// 的下拉框按别名搜索；字典端点仅成员可用，别名随入口视图下发）。
func Test匿名读入口模板维度字段带别名(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	res := s.Do(t, web, http.MethodPost, "/api/v1/labels/aliases",
		map[string]any{"label": "机型:UD-2300", "alias": "2300血球仪"}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("加别名状态码 = %d, 想 201", res.StatusCode)
	}
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})

	var view intakeView
	anon := s.NewClient()
	got := s.Do(t, anon, http.MethodGet, "/api/v1/intakes/bug", nil, &view)
	if got.StatusCode != http.StatusOK {
		t.Fatalf("匿名读入口状态码 = %d, 想 200", got.StatusCode)
	}
	model := findField(t, view.Template.Fields, "哪台仪器？")
	assertContains(t, model.Options, "UD-2300")
	if len(model.Options) == 0 || model.Options[len(model.Options)-1] != "不确定" {
		t.Errorf("候选值末尾应是「不确定」，得到 %v", model.Options)
	}
	if got := model.OptionAliases["UD-2300"]; len(got) != 1 || got[0] != "2300血球仪" {
		t.Errorf("UD-2300 的别名 = %v, 想 [2300血球仪]", got)
	}

	// 成员读模板视图同样带别名（与入口视图同一结构）。
	var tpl templateBody
	s.Do(t, web, http.MethodGet, "/api/v1/templates/bug-report", nil, &tpl)
	m2 := findField(t, tpl.Fields, "哪台仪器？")
	if got := m2.OptionAliases["UD-2300"]; len(got) != 1 || got[0] != "2300血球仪" {
		t.Errorf("模板视图里 UD-2300 的别名 = %v, 想 [2300血球仪]", got)
	}
}

// findField 在字段定义里按标题（问句原文）找字段。
func findField(t testing.TB, fields []templateFieldBody, title string) templateFieldBody {
	t.Helper()
	for _, f := range fields {
		if f.Title == title {
			return f
		}
	}
	t.Fatalf("找不到字段 %q：%+v", title, fields)
	return templateFieldBody{}
}
