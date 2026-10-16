package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 一份成员自建的巡检模板（带维度字段、单选和给 Agent 的说明）。
const checkTemplate = `---
名称: 巡检记录
类型: 任务
---
## 巡检哪条产品线？ {维度:产品线 必填}
从字典里选。
## 结论如何？ {单选: 正常 | 异常 必填}
## 给 Agent 的说明
异常时先别关单，回复里写明细节。
`

// 任何成员（含成员 Agent）都能新建模板；列表和读取立即可见。
func Test成员新建模板(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	res := s.Do(t, web, http.MethodPost, "/api/v1/templates",
		map[string]string{"key": "check", "markdown": checkTemplate}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("新建状态码 = %d, 想 201", res.StatusCode)
	}
	var created templateSummary
	tbtest.DecodeJSON(t, res, &created)
	if created.Name != "巡检记录" || created.Kind != "task" || created.Version != 1 || !created.Active {
		t.Errorf("新建响应不对：%+v", created)
	}
	var got templateBody
	s.Do(t, web, http.MethodGet, "/api/v1/templates/check", nil, &got)
	if got.AgentNote != "异常时先别关单，回复里写明细节。" {
		t.Errorf("给 Agent 的说明 = %q", got.AgentNote)
	}
	if len(got.Fields) != 2 {
		t.Errorf("字段数 = %d, 想 2", len(got.Fields))
	}

	// 未登录不能写。
	res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/templates",
		map[string]string{"key": "x", "markdown": checkTemplate}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("未登录新建状态码 = %d, 想 401", res.StatusCode)
	}
}

// 模板格式错误保存失败，错误信息里有行号（验收标准）。
func Test格式错误的模板保存失败并报出行号(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	cases := []struct {
		name, markdown string
		wantLine       string
	}{
		{"类型写错", "---\n名称: x\n类型: 问题\n---\n## 想问什么？\n一句话。\n", "第 3 行"},
		{"标记写错", "---\n名称: x\n类型: 任务\n---\n## 想问什么？ {必填 啥}\n一句话。\n", "第 5 行"},
		{"重复字段", "---\n名称: x\n类型: 任务\n---\n## 想问什么？\n一句话。\n## 想问什么？\n又一句。\n", "第 7 行"},
		{"没有YAML头", "## 想问什么？\n一句话。\n", "第 1 行"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := s.Do(t, web, http.MethodPost, "/api/v1/templates",
				map[string]string{"key": "bad", "markdown": tc.markdown}, nil)
			if res.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
			}
			var got errorBody
			tbtest.DecodeJSON(t, res, &got)
			if got.Error.Code != "template_format_error" {
				t.Errorf("错误码 = %q, 想 template_format_error", got.Error.Code)
			}
			if !strings.Contains(got.Error.Message, tc.wantLine) {
				t.Errorf("message 应含行号 %q：%q", tc.wantLine, got.Error.Message)
			}
			// 保存失败：模板没有落库。
			res = s.Do(t, web, http.MethodGet, "/api/v1/templates/bad", nil, nil)
			if res.StatusCode != http.StatusNotFound {
				t.Errorf("失败的模板不应落库，状态码 = %d", res.StatusCode)
			}
		})
	}
}

// key 校验：缺 key、key 里有空格或 /、与已有模板重名。
func Test模板key校验(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	for name, key := range map[string]string{"缺失": "", "带空格": "a b", "带斜杠": "a/b"} {
		t.Run(name, func(t *testing.T) {
			res := s.Do(t, web, http.MethodPost, "/api/v1/templates",
				map[string]string{"key": key, "markdown": checkTemplate}, nil)
			if res.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
			}
		})
	}
	// 重名：409（key 不能复用）。
	res := s.Do(t, web, http.MethodPost, "/api/v1/templates",
		map[string]string{"key": "bug-report", "markdown": checkTemplate}, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("重名状态码 = %d, 想 409", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "template_key_taken" {
		t.Errorf("错误码 = %q, 想 template_key_taken", got.Error.Code)
	}
}

// 修改模板产生新版本并记录修改人；已有工单的字段定义不变（验收标准：
// 模板改版后，旧工单仍按创建时版本的字段定义渲染和 PATCH）。
func Test修改模板产生新版本已有工单字段定义不变(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	s.CreateMember("李工", "another-pass")
	li := s.Login("李工", "another-pass")

	// 陈工先建一份 check 模板，再建单（版本 1 的字段）。
	if res := s.Do(t, web, http.MethodPost, "/api/v1/templates",
		map[string]string{"key": "check", "markdown": checkTemplate}, nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("建模板状态码 = %d, 想 201", res.StatusCode)
	}
	res := createFromTemplate(t, s, web, map[string]any{
		"template": "check",
		"title":    "例行巡检",
		"fields":   map[string]string{"巡检哪条产品线？": "血液", "结论如何？": "正常"},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201（先手工建 check 模板）", res.StatusCode)
	}

	// 李工修改模板：换掉一个问句、加一个字段。
	updated := strings.Replace(checkTemplate,
		"## 结论如何？ {单选: 正常 | 异常 必填}",
		"## 结论怎么样？ {单选: 正常 | 异常 | 待观察 必填}", 1)
	updated = strings.Replace(updated, "异常时先别关单，回复里写明细节。", "异常时按部门流程上报。", 1)
	res = s.Do(t, li, http.MethodPatch, "/api/v1/templates/check",
		map[string]string{"markdown": updated}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("修改状态码 = %d, 想 200", res.StatusCode)
	}
	var after templateBody
	s.Do(t, web, http.MethodGet, "/api/v1/templates/check", nil, &after)
	if after.Version != 2 {
		t.Errorf("版本 = %d, 想 2", after.Version)
	}
	if after.UpdatedBy == nil || after.UpdatedBy.Member.Name != "李工" || after.UpdatedBy.Agent != nil {
		t.Errorf("修改人应为李工本人：%+v", after.UpdatedBy)
	}
	if !strings.Contains(strings.Join(fieldKeys(after.Fields), ","), "结论怎么样？") {
		t.Errorf("新版字段应更新：%+v", after.Fields)
	}
	if after.AgentNote != "异常时按部门流程上报。" {
		t.Errorf("新版给 Agent 的说明 = %q", after.AgentNote)
	}

	// 已有工单：字段定义仍是版本 1（旧问句仍能渲染）。
	var old ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/20261016-001", nil, &old)
	if old.Brief.Fields == nil {
		t.Fatalf("旧工单应仍有字段值")
	}
	if _, ok := (*old.Brief.Fields)["结论如何？"]; !ok {
		t.Errorf("旧工单的字段键应保持版本 1：%v", old.Brief.Fields)
	}
	if !strings.Contains(old.Brief.Markdown, "### 结论如何？") {
		t.Errorf("旧工单说明仍按版本 1 渲染：\n%s", old.Brief.Markdown)
	}

	// 新工单按版本 2 建（新问句有效、旧问句变 unknown_field）。
	res = createFromTemplate(t, s, li, map[string]any{
		"template": "check",
		"title":    "又一次巡检",
		"fields":   map[string]string{"巡检哪条产品线？": "尿液", "结论怎么样？": "待观察"},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("按新版建单状态码 = %d, 想 201", res.StatusCode)
	}
	var newer ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/2", nil, &newer)
	if !strings.Contains(newer.Brief.Markdown, "### 结论怎么样？") {
		t.Errorf("新工单应按版本 2 渲染：\n%s", newer.Brief.Markdown)
	}
	res = createFromTemplate(t, s, li, map[string]any{
		"template": "check",
		"title":    "x",
		"fields":   map[string]string{"结论如何？": "正常"},
	})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("旧问句在新版里应报 unknown_field，状态码 = %d", res.StatusCode)
	}

	// 旧工单 PATCH 只认版本 1 的字段键（快照）。
	res = revise(t, s, web, "20261016-001", map[string]any{
		"fields":        map[string]string{"结论怎么样？": "异常"},
		"base_revision": 1,
	})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("旧工单不接受新问句，状态码 = %d", res.StatusCode)
	}
	res = revise(t, s, web, "20261016-001", map[string]any{
		"fields":        map[string]string{"结论如何？": "异常"},
		"base_revision": 1,
	})
	if res.StatusCode != http.StatusOK {
		t.Errorf("旧工单接受旧问句，状态码 = %d", res.StatusCode)
	}
}

func fieldKeys(fields []templateFieldBody) []string {
	keys := make([]string, len(fields))
	for i, f := range fields {
		keys[i] = f.Key
	}
	return keys
}

// 按字段 PATCH 只改动传入的字段，并记一条修订事件（验收标准）。
func Test按字段PATCH只改动传入的字段并记修订事件(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)

	res := createFromTemplate(t, s, web, map[string]any{
		"template": "bug-report",
		"title":    "UD-2300 质控报警",
		"fields": map[string]string{
			"哪台仪器？":        "UD-2300",
			"你做了哪些操作？":     "1. 开机 2. 进质控",
			"实际看到了什么？":     "E-203 报警",
			"你期望看到什么？":     "正常测试",
			"什么时候发生的？":     "上午",
			"出现得频繁吗？":      "每次",
			"对工作有影响吗？":     "无法出报告",
			"序列号和软件版本是多少？": "V3.1.0",
		},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)

	// 创建者的 Agent 只补一个字段：其余字段不动。
	res = revise(t, s, agent, created.Number, map[string]any{
		"fields":        map[string]string{"你的猜测或补充？": "可能是液位传感器"},
		"base_revision": 1,
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("按字段修订状态码 = %d, 想 200", res.StatusCode)
	}
	var revised struct {
		Revision int64 `json:"revision"`
	}
	tbtest.DecodeJSON(t, res, &revised)
	if revised.Revision != 2 {
		t.Errorf("revision = %d, 想 2", revised.Revision)
	}

	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	fields := *got.Brief.Fields
	if fields["你做了哪些操作？"] != "1. 开机 2. 进质控" {
		t.Errorf("未传的字段不应被改动：%q", fields["你做了哪些操作？"])
	}
	if fields["你的猜测或补充？"] != "可能是液位传感器" {
		t.Errorf("传入的字段应更新：%q", fields["你的猜测或补充？"])
	}
	for _, want := range []string{"### 你的猜测或补充？", "可能是液位传感器", "### 你做了哪些操作？"} {
		if !strings.Contains(got.Brief.Markdown, want) {
			t.Errorf("说明缺少 %q：\n%s", want, got.Brief.Markdown)
		}
	}
	// 记了一条 brief_revised 事件。
	found := false
	for _, ev := range got.Timeline {
		if ev.Type == "brief_revised" && ev.Revision == 2 {
			found = true
			if ev.Actor.Agent == nil || ev.Actor.Agent.Name != "笔记本" {
				t.Errorf("修订事件的作者应是具名 Agent：%+v", ev.Actor)
			}
		}
	}
	if !found {
		t.Errorf("应记一条 revision=2 的 brief_revised 事件：%+v", got.Timeline)
	}

	// 把字段清空也可以（修订不做必填校验）。
	res = revise(t, s, web, created.Number, map[string]any{
		"fields":        map[string]string{"你的猜测或补充？": ""},
		"base_revision": 2,
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("清空字段状态码 = %d, 想 200", res.StatusCode)
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	if strings.Contains(got.Brief.Markdown, "猜测或补充") {
		t.Errorf("清空的字段不应再出现在说明里：\n%s", got.Brief.Markdown)
	}

	// base_revision 冲突仍然拦截。
	res = revise(t, s, web, created.Number, map[string]any{
		"fields":        map[string]string{"你的猜测或补充？": "x"},
		"base_revision": 1,
	})
	if res.StatusCode != http.StatusConflict {
		t.Errorf("旧 base_revision 状态码 = %d, 想 409", res.StatusCode)
	}

	// 未知字段键：422。
	res = revise(t, s, web, created.Number, map[string]any{
		"fields":        map[string]string{"不存在的问句？": "x"},
		"base_revision": 3,
	})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("未知字段状态码 = %d, 想 422", res.StatusCode)
	}
	var err errorBody
	tbtest.DecodeJSON(t, res, &err)
	if err.Error.Code != "unknown_field" {
		t.Errorf("错误码 = %q, 想 unknown_field", err.Error.Code)
	}
}

// 字段值校验也适用于修订：单选值不在选项里、维度值不在字典里。
func Test按字段PATCH的值校验(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	res := createFromTemplate(t, s, web, map[string]any{
		"template": "bug-report", "title": "x", "fields": bugFields(),
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)

	for name, fields := range map[string]map[string]string{
		"单选非法": {"出现得频繁吗？": "总是"},
		"维度非法": {"哪台仪器？": "不存在机型"},
	} {
		t.Run(name, func(t *testing.T) {
			res := revise(t, s, web, created.Number, map[string]any{
				"fields": fields, "base_revision": 1,
			})
			if res.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
			}
			var got errorBody
			tbtest.DecodeJSON(t, res, &got)
			if name == "单选非法" && got.Error.Code != "invalid_field_option" {
				t.Errorf("错误码 = %q, 想 invalid_field_option", got.Error.Code)
			}
			if name == "维度非法" && got.Error.Code != "unknown_label_value" {
				t.Errorf("错误码 = %q, 想 unknown_label_value", got.Error.Code)
			}
		})
	}

	// 「不确定」可以填进维度字段（算已填；标签不跟着变——标签用标签接口管理）。
	res = revise(t, s, web, created.Number, map[string]any{
		"fields":        map[string]string{"哪台仪器？": "不确定"},
		"base_revision": 1,
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("填「不确定」状态码 = %d, 想 200", res.StatusCode)
	}
}

// 模板工单与非模板工单的修订互斥：fields 只用于模板工单，brief_md 只用于
// 非模板工单。
func Test字段修订与整块修订按工单类型互斥(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	number := mustCreateTicket(t, s, web, "自由说明的工单")
	res := revise(t, s, web, number, map[string]any{
		"fields":        map[string]string{"想问什么？": "x"},
		"base_revision": 1,
	})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("非模板工单传 fields 状态码 = %d, 想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "fields_without_template" {
		t.Errorf("错误码 = %q, 想 fields_without_template", got.Error.Code)
	}

	var created createTicketResponse
	res = createFromTemplate(t, s, web, map[string]any{
		"template": "bug-report", "title": "x", "fields": bugFields(),
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &created)
	res = revise(t, s, web, created.Number, map[string]any{
		"brief_md":      "整块说明",
		"base_revision": 1,
	})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("模板工单传 brief_md 状态码 = %d, 想 422", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "brief_md_with_template" {
		t.Errorf("错误码 = %q, 想 brief_md_with_template", got.Error.Code)
	}
}

// 模板只能停用、不能删除：停用幂等，读取仍可见（active=false），
// 没有删除接口。
func Test模板只能停用不能删除(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	for i := 0; i < 2; i++ {
		res := s.Do(t, web, http.MethodPost, "/api/v1/templates/question/deactivate", nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("第 %d 次停用状态码 = %d, 想 200（幂等）", i+1, res.StatusCode)
		}
	}
	var got templateBody
	s.Do(t, web, http.MethodGet, "/api/v1/templates/question", nil, &got)
	if got.Active {
		t.Errorf("停用后 active 应为 false")
	}
	// 列表里仍带 active 标记。
	var list struct {
		Templates []templateSummary `json:"templates"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/templates", nil, &list)
	for _, tpl := range list.Templates {
		if tpl.Key == "question" && tpl.Active {
			t.Errorf("列表里 question 应已停用")
		}
	}
	// 没有删除接口。
	res := s.Do(t, web, http.MethodDelete, "/api/v1/templates/question", nil, nil)
	if res.StatusCode != http.StatusMethodNotAllowed && res.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE 状态码 = %d, 想 405 或 404", res.StatusCode)
	}
	// 停用不存在的模板：404。
	res = s.Do(t, web, http.MethodPost, "/api/v1/templates/no-such/deactivate", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("停用未知模板状态码 = %d, 想 404", res.StatusCode)
	}
}

// 工单 Markdown 的「下一步」里出现模板给 Agent 的说明（spec 用户故事 35）。
func Test工单Markdown下一步包含模板给Agent的说明(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	var created createTicketResponse
	res := createFromTemplate(t, s, web, map[string]any{
		"template": "bug-report", "title": "x", "fields": bugFields(),
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &created)

	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number+"?format=markdown", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Markdown 状态码 = %d", res.StatusCode)
	}
	body := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			break
		}
	}
	md := string(body)
	for _, want := range []string{
		"## 下一步（给 Agent 的操作指引）",
		"Bug 反馈 模板给 Agent 的说明：",
		"摘掉「待初筛」标签",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown 缺少 %q：\n%s", want, md)
		}
	}
}
