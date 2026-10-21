package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
	"github.com/allanpk716/agent-taskboard/internal/template"
	templatespkg "github.com/allanpk716/agent-taskboard/internal/templates"
)

const scenarioTemplate = `---
名称: 巡检提醒
类型: 任务
适用场景: 需要定期巡检并记录结论
---
## 巡检哪条产品线？ {维度:产品线 必填}
从字典里选。
`

const plainScenarioTemplate = `---
名称: 普通任务
类型: 任务
---
## 要做什么？ {必填}
一句话写清楚。
`

func Test模板适用场景贯通API与建单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	for key, md := range map[string]string{
		"scenario": scenarioTemplate,
		"plain":    plainScenarioTemplate,
	} {
		res := s.Do(t, web, http.MethodPost, "/api/v1/templates",
			map[string]string{"key": key, "markdown": md}, nil)
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("新建 %s 状态码 = %d, 想 201", key, res.StatusCode)
		}
	}

	var list struct {
		Templates []templateSummary `json:"templates"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/templates", nil, &list)
	scenarios := map[string]string{}
	for _, tpl := range list.Templates {
		scenarios[tpl.Key] = tpl.Scenario
	}
	if scenarios["scenario"] != "需要定期巡检并记录结论" {
		t.Errorf("列表 scenario 的适用场景 = %q", scenarios["scenario"])
	}
	if scenarios["plain"] != "" {
		t.Errorf("没写适用场景的 plain 应返回空值，得到 %q", scenarios["plain"])
	}

	var detail templateBody
	s.Do(t, web, http.MethodGet, "/api/v1/templates/scenario", nil, &detail)
	if detail.Scenario != "需要定期巡检并记录结论" {
		t.Errorf("详情适用场景 = %q", detail.Scenario)
	}

	res := s.Do(t, web, http.MethodGet, "/api/v1/templates/scenario?format=markdown", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("填写指南状态码 = %d", res.StatusCode)
	}
	guide, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(guide), "- 适用场景：需要定期巡检并记录结论") {
		t.Errorf("填写指南缺少适用场景：\n%s", guide)
	}

	created := createFromTemplate(t, s, web, map[string]any{
		"template": "plain",
		"title":    "没有适用场景的模板也能建单",
		"fields":   map[string]string{"要做什么？": "整理巡检记录"},
		"labels":   []string{"机型:UD-2300"},
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("用没有适用场景的模板建单状态码 = %d, 想 201", created.StatusCode)
	}

	bad := s.Do(t, web, http.MethodPost, "/api/v1/templates",
		map[string]string{"key": "bad-scenario", "markdown": "---\n名称: 错误\n类型: 任务\n用途: 不存在\n---\n## 问？\n答。\n"}, nil)
	if bad.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("未知键状态码 = %d, 想 422", bad.StatusCode)
	}
	var errBody errorBody
	tbtest.DecodeJSON(t, bad, &errBody)
	if !strings.Contains(errBody.Error.Message, "适用场景") {
		t.Errorf("未知键报错应列出可用键「适用场景」：%q", errBody.Error.Message)
	}
}

func Test新库预置模板带适用场景(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	want := map[string]string{
		"assign-task":     "你已经想清楚要交付什么、怎么做，请对方照着完成",
		"request":         "你清楚问题、手上的输入和想要的效果，怎么做交给对方决定",
		"question":        "只想得到一个答案或结论，不需要交付物",
		"bug-report":      "仪器或软件坏了、行为不对",
		"bug-report-data": "仪器或软件坏了、行为不对；需要用成对的正确、错误例子复现",
	}
	var list struct {
		Templates []templateSummary `json:"templates"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/templates", nil, &list)
	got := map[string]string{}
	for _, tpl := range list.Templates {
		got[tpl.Key] = tpl.Scenario
	}
	if len(got) != len(want) {
		t.Fatalf("预置模板数 = %d, 想 %d：%+v", len(got), len(want), got)
	}
	for key, scenario := range want {
		if got[key] != scenario {
			t.Errorf("%s 的适用场景 = %q, 想 %q", key, got[key], scenario)
		}
	}
}

func Test新库预置提需求模板(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	var got templateBody
	res := s.Do(t, web, http.MethodGet, "/api/v1/templates/request", nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读提需求模板状态码 = %d", res.StatusCode)
	}
	if got.Name != "提需求" || got.Kind != "task" {
		t.Errorf("模板头不对：%+v", got)
	}
	if got.Scenario != "你清楚问题、手上的输入和想要的效果，怎么做交给对方决定" {
		t.Errorf("适用场景 = %q", got.Scenario)
	}
	wantRequired := map[string]bool{
		"为什么要做？有什么背景？":          true,
		"输入是什么？":                true,
		"期望得到什么输出？":             true,
		"怎样算满足？验收标准或可参考的范例（竞品）": true,
		"期望什么时候完成？":             false,
		"补充":                    false,
	}
	if len(got.Fields) != len(wantRequired) {
		t.Fatalf("字段数 = %d, 想 %d：%+v", len(got.Fields), len(wantRequired), got.Fields)
	}
	for _, field := range got.Fields {
		want, ok := wantRequired[field.Key]
		if !ok {
			t.Fatalf("意外字段 %q：%+v", field.Key, got.Fields)
		}
		if field.Required != want {
			t.Errorf("字段 %q required = %v, 想 %v", field.Key, field.Required, want)
		}
		if field.Guide == "" || field.Example == "" {
			t.Errorf("字段 %q 应有引导和示例：%+v", field.Key, field)
		}
	}
	if got.AgentNote == "" {
		t.Error("提需求应预置「给 Agent 的说明」")
	}
}

func Test预置模板适用场景一次性升级(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	oldMarkdown := map[string]string{
		"assign-task":     templatespkg.LegacyScenarioAssignTaskMarkdown,
		"question":        templatespkg.LegacyScenarioQuestionMarkdown,
		"bug-report":      templatespkg.LegacyScenarioBugReportMarkdown,
		"bug-report-data": templatespkg.LegacyScenarioBugReportDataMarkdown,
	}
	oldMarkdown["assign-task"] = strings.Replace(
		oldMarkdown["assign-task"], "要达成什么目标？", "要达成目标？", 1)
	setPresetCurrentBeforeScenario(t, s, oldMarkdown)

	url := restartServer(t, s)

	for key, wantVersion := range map[string]int64{
		"assign-task":     1,
		"question":        2,
		"bug-report":      2,
		"bug-report-data": 2,
	} {
		var got templateBody
		res := doURL(t, web, http.MethodGet, url+"/api/v1/templates/"+key, nil, &got)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("读 %s 状态码 = %d", key, res.StatusCode)
		}
		if got.Version != wantVersion {
			t.Errorf("%s 升级后版本 = %d, 想 %d", key, got.Version, wantVersion)
		}
	}
	var assign templateBody
	doURL(t, web, http.MethodGet, url+"/api/v1/templates/assign-task", nil, &assign)
	if assign.Scenario != "" || !strings.Contains(assign.Markdown, "## 要达成目标？") {
		t.Errorf("成员改过的 assign-task 不应升级：scenario=%q markdown=%s", assign.Scenario, assign.Markdown)
	}
	var question templateBody
	doURL(t, web, http.MethodGet, url+"/api/v1/templates/question", nil, &question)
	if question.Scenario != "只想得到一个答案或结论，不需要交付物" {
		t.Errorf("question 升级后适用场景 = %q", question.Scenario)
	}
	var questionVersions struct {
		Versions []struct {
			Version  int64  `json:"version"`
			Markdown string `json:"markdown"`
		} `json:"versions"`
	}
	doURL(t, web, http.MethodGet, url+"/api/v1/templates/question/versions", nil, &questionVersions)
	if len(questionVersions.Versions) != 2 ||
		questionVersions.Versions[0].Version != 2 ||
		questionVersions.Versions[1].Version != 1 {
		t.Fatalf("question 升级后的版本链不对：%+v", questionVersions.Versions)
	}
	if questionVersions.Versions[1].Markdown != oldMarkdown["question"] {
		t.Error("升级前的版本 1 原文应逐字节保留")
	}

	url2 := restartServer(t, s)
	for _, key := range []string{"assign-task", "question", "bug-report", "bug-report-data"} {
		var versions struct {
			Versions []struct {
				Version int64 `json:"version"`
			} `json:"versions"`
		}
		doURL(t, web, http.MethodGet, url2+"/api/v1/templates/"+key+"/versions", nil, &versions)
		want := map[string]int{"assign-task": 1, "question": 2, "bug-report": 2, "bug-report-data": 2}[key]
		if len(versions.Versions) != want {
			t.Errorf("%s 第二次重启后版本数 = %d, 想 %d", key, len(versions.Versions), want)
		}
	}
}

func Test适用场景升级后成员删除不再补回(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	setPresetCurrentBeforeScenario(t, s, map[string]string{
		"assign-task":     templatespkg.LegacyScenarioAssignTaskMarkdown,
		"question":        templatespkg.LegacyScenarioQuestionMarkdown,
		"bug-report":      templatespkg.LegacyScenarioBugReportMarkdown,
		"bug-report-data": templatespkg.LegacyScenarioBugReportDataMarkdown,
	})
	restartServer(t, s)

	res := s.Do(t, web, http.MethodPatch, "/api/v1/templates/question",
		map[string]string{"markdown": templatespkg.LegacyScenarioQuestionMarkdown}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("成员删除适用场景后保存状态码 = %d", res.StatusCode)
	}

	url := restartServer(t, s)
	var got templateBody
	doURL(t, web, http.MethodGet, url+"/api/v1/templates/question", nil, &got)
	if got.Version != 3 || got.Scenario != "" || got.Markdown != templatespkg.LegacyScenarioQuestionMarkdown {
		t.Fatalf("重启后不应补回适用场景：version=%d scenario=%q markdown=%q", got.Version, got.Scenario, got.Markdown)
	}
	var versions struct {
		Versions []struct{ Version int64 } `json:"versions"`
	}
	doURL(t, web, http.MethodGet, url+"/api/v1/templates/question/versions", nil, &versions)
	if len(versions.Versions) != 3 {
		t.Fatalf("重启后版本数 = %d, 想 3", len(versions.Versions))
	}
}

func Test提需求模板一次性升级(t *testing.T) {
	t.Run("缺 request 时导入且重复启动不新增版本", func(t *testing.T) {
		s := tbtest.Start(t)
		s.CreateAdmin("陈工", "secret-pass")
		web := s.Login("陈工", "secret-pass")

		oldMarkdown := map[string]string{
			"assign-task":     templatespkg.LegacyScenarioAssignTaskMarkdown,
			"question":        templatespkg.LegacyScenarioQuestionMarkdown,
			"bug-report":      templatespkg.LegacyScenarioBugReportMarkdown,
			"bug-report-data": templatespkg.LegacyScenarioBugReportDataMarkdown,
		}
		setPresetCurrentBeforeScenario(t, s, oldMarkdown)
		if _, err := s.Store().DB.Exec(`DELETE FROM template_versions WHERE template_key = 'request'`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Store().DB.Exec(`DELETE FROM templates WHERE key = 'request'`); err != nil {
			t.Fatal(err)
		}

		url := restartServer(t, s)
		var request templateBody
		res := doURL(t, web, http.MethodGet, url+"/api/v1/templates/request", nil, &request)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("升级后读 request 状态码 = %d", res.StatusCode)
		}
		if request.Version != 1 || request.Kind != "task" || request.Scenario != "你清楚问题、手上的输入和想要的效果，怎么做交给对方决定" {
			t.Errorf("导入的 request 不对：%+v", request)
		}

		url2 := restartServer(t, s)
		var versions struct {
			Versions []struct{ Version int64 } `json:"versions"`
		}
		doURL(t, web, http.MethodGet, url2+"/api/v1/templates/request/versions", nil, &versions)
		if len(versions.Versions) != 1 || versions.Versions[0].Version != 1 {
			t.Errorf("重复启动后 request 版本链 = %+v, 想 [1]", versions.Versions)
		}
	})

	t.Run("成员改过的 request 不动", func(t *testing.T) {
		s := tbtest.Start(t)
		s.CreateAdmin("陈工", "secret-pass")
		web := s.Login("陈工", "secret-pass")

		var current templateBody
		s.Do(t, web, http.MethodGet, "/api/v1/templates/request", nil, &current)
		modified := strings.Replace(current.Markdown, "## 补充\n", "## 其他补充\n", 1)
		res := s.Do(t, web, http.MethodPatch, "/api/v1/templates/request",
			map[string]string{"markdown": modified}, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("修改 request 状态码 = %d", res.StatusCode)
		}

		url := restartServer(t, s)
		var got templateBody
		doURL(t, web, http.MethodGet, url+"/api/v1/templates/request", nil, &got)
		if got.Version != 2 || !strings.Contains(got.Markdown, "## 其他补充\n") {
			t.Errorf("成员改过的 request 不应升级：version=%d markdown=%s", got.Version, got.Markdown)
		}
	})
}

func TestAgent说明书说明怎么选模板(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")

	res := s.Do(t, s.NewClient(), http.MethodGet, "/agent.md", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读 Agent 说明书状态码 = %d", res.StatusCode)
	}
	guide, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## 怎么选模板",
		"先看各模板的适用场景",
		"发起人自己想不想定怎么做？",
		"已经想清楚交付物和做法的用分配任务",
		"只清楚问题、输入和想要的效果，做法交给接收人的用提需求",
		"提需求不要求发起人写怎么做，分配任务要求发起人写清交付物和做法",
		"只想要答案的用问题咨询",
		"东西坏了的用 Bug 反馈",
		"拿不准时用回复问发起人",
	} {
		if !strings.Contains(string(guide), want) {
			t.Errorf("Agent 说明书缺少 %q：\n%s", want, guide)
		}
	}
}

func setPresetCurrentBeforeScenario(t *testing.T, s *tbtest.Server, markdowns map[string]string) {
	t.Helper()
	for key, md := range markdowns {
		def, err := template.Parse(md)
		if err != nil {
			t.Fatalf("解析 %s 旧原文失败：%v", key, err)
		}
		fields, err := json.Marshal(def.Fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Store().DB.Exec(`UPDATE template_versions
			SET markdown = ?, scenario = '', fields = ? WHERE template_key = ? AND version = 1`,
			md, string(fields), key); err != nil {
			t.Fatalf("回置 %s 旧原文失败：%v", key, err)
		}
	}
}
