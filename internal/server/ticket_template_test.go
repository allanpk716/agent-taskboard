package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// createFromTemplate 用模板建单，返回原始响应。
func createFromTemplate(t testing.TB, s *tbtest.Server, c *http.Client, body map[string]any) *http.Response {
	t.Helper()
	return s.Do(t, c, http.MethodPost, "/api/v1/tickets", body, nil)
}

// bugFields 是 Bug 反馈模板的一套完整字段值（全必填都填上）。
func bugFields() map[string]string {
	return map[string]string{
		"哪台仪器？":        "UD-2300",
		"序列号和软件版本是多少？": "序列号 2300-2024-0107，软件 V3.1.0",
		"什么时候发生的？":     "今天上午 9 点左右",
		"你做了哪些操作？":     "1. 开机自检完成 2. 进入质控界面 3. 点击“开始”",
		"出错的例子":        "例 1：导入“样本清单.xlsx”（见附件）→ 第 3 行姓名显示成乱码 → 应显示“张三”",
		"出现得频繁吗？":      "每次",
		"对工作有影响吗？":     "无法出报告",
		"你的猜测或补充？":     "",
	}
}

// 用模板建单：类型由模板决定、预设标签自动打上、维度字段的值同时成为标签并
// 以文字写进说明；读取同时返回 fields 和拼好的 brief_md。
func Test用模板建单类型标签与说明(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	fields := bugFields()
	delete(fields, "你的猜测或补充？") // 选填不填
	res := createFromTemplate(t, s, web, map[string]any{
		"template": "bug-report",
		"title":    "UD-2300 质控时报警 E-203",
		"fields":   fields,
		"labels":   []string{},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)
	if len(created.Warnings) != 0 {
		t.Errorf("必填全填时不应有 warnings：%+v", created.Warnings)
	}

	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	if got.Kind != "report" {
		t.Errorf("kind = %q, 想 report（由模板决定）", got.Kind)
	}
	if got.Template == nil || *got.Template != "bug-report" {
		t.Errorf("template = %v, 想 bug-report", got.Template)
	}
	// 预设标签自动打上；维度字段的值同时成为标签。
	for _, want := range []string{"待初筛", "机型:UD-2300"} {
		assertContains(t, got.Labels, want)
	}
	// 读取同时返回 fields 和拼好的 brief_md。
	if got.Brief.Fields == nil {
		t.Fatalf("brief.fields 应为字段值 map")
	}
	if v, ok := (*got.Brief.Fields)["哪台仪器？"]; !ok || v != "UD-2300" {
		t.Errorf("brief.fields[哪台仪器？] = %q", v)
	}
	if got.Brief.Template == nil || *got.Brief.Template != "bug-report" {
		t.Errorf("brief.template = %v", got.Brief.Template)
	}
	// 维度字段的值以文字写进说明正文（访客看不到标签）。
	for _, want := range []string{
		"### 哪台仪器？", "UD-2300",
		"### 你做了哪些操作？", "1. 开机自检完成 2. 进入质控界面 3. 点击“开始”",
		"### 出现得频繁吗？", "每次",
	} {
		if !strings.Contains(got.Brief.Markdown, want) {
			t.Errorf("brief.markdown 缺少 %q：\n%s", want, got.Brief.Markdown)
		}
	}
	// 没填的选填字段不进说明；说明 revision 从 1 起。
	if strings.Contains(got.Brief.Markdown, "猜测或补充") {
		t.Errorf("没填的字段不应出现在说明里：\n%s", got.Brief.Markdown)
	}
	if got.Brief.Revision != 1 {
		t.Errorf("brief.revision = %d, 想 1", got.Brief.Revision)
	}
}

// 用模板建单时缺少必填项：工单照常创建，响应只带 warnings 逐项列出
// （验收标准；「不确定」算已填）。
func Test缺必填项只得到warnings工单照常创建(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	res := createFromTemplate(t, s, web, map[string]any{
		"template": "bug-report",
		"title":    "UD-2300 报警，先建单",
		"fields": map[string]string{
			"哪台仪器？": "UD-2300",
		},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("缺必填时建单状态码 = %d, 想 201（只 warnings）", res.StatusCode)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)
	warned := map[string]bool{}
	for _, w := range created.Warnings {
		warned[w.Field] = true
		if w.Message == "" {
			t.Errorf("warning %q 缺少说明文字", w.Field)
		}
	}
	for _, want := range []string{
		"什么时候发生的？", "你做了哪些操作？", "出错的例子", "出现得频繁吗？",
	} {
		if !warned[want] {
			t.Errorf("warnings 缺少必填项 %q：%+v", want, created.Warnings)
		}
	}
	if warned["哪台仪器？"] {
		t.Errorf("已填的必填项不应出现在 warnings 里：%+v", created.Warnings)
	}
	if warned["序列号和软件版本是多少？"] || warned["对工作有影响吗？"] {
		t.Errorf("选填项不应出现在 warnings 里：%+v", created.Warnings)
	}

	// 不用模板的建单响应同样带空 warnings（结构稳定）。
	res = createTicket(t, s, web, createTicketBody{Title: "自由说明", Labels: []string{"机型:UD-2300"}})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("不用模板建单状态码 = %d", res.StatusCode)
	}
	var plain createTicketResponse
	tbtest.DecodeJSON(t, res, &plain)
	if plain.Warnings == nil || len(plain.Warnings) != 0 {
		t.Errorf("不用模板的 warnings 应为空数组：%+v", plain.Warnings)
	}
}

// 「不确定」算已填、不打该维度的标签（orchestrator 决议 6）；此时如果
// 再没有别的维度标签，建单被 422 dimension_label_required 拦下。
func Test不确定算已填且不打标签(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	fields := bugFields()
	fields["哪台仪器？"] = "不确定"
	res := createFromTemplate(t, s, web, map[string]any{
		"template": "bug-report",
		"title":    "型号没看清的报警",
		"fields":   fields,
		"labels":   []string{"部门:研发部"}, // 不确定不打标签，补一个维度标签
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)
	for _, w := range created.Warnings {
		if w.Field == "哪台仪器？" {
			t.Errorf("选「不确定」算已填，不应出现在 warnings：%+v", created.Warnings)
		}
	}
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	for _, l := range got.Labels {
		if strings.HasPrefix(l, "机型:") {
			t.Errorf("「不确定」不应打出机型标签：%v", got.Labels)
		}
	}
	assertContains(t, got.Labels, "部门:研发部")
	if !strings.Contains(got.Brief.Markdown, "不确定") {
		t.Errorf("「不确定」也应写进说明正文：\n%s", got.Brief.Markdown)
	}

	// 只有「不确定」维度字段、又没补维度标签：dimension_label_required。
	res = createFromTemplate(t, s, web, map[string]any{
		"template": "bug-report",
		"title":    "没有任何维度标签",
		"fields":   fields,
	})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
	}
	var err errorBody
	tbtest.DecodeJSON(t, res, &err)
	if err.Error.Code != "dimension_label_required" {
		t.Errorf("错误码 = %q, 想 dimension_label_required", err.Error.Code)
	}
}

// 成员 Agent 按模板建单：填指南里的问句键即可；创建者署名为具名 Agent。
func Test成员Agent按模板建单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "初筛")
	agent := s.AgentClient(token)

	res := createFromTemplate(t, s, agent, map[string]any{
		"template": "question",
		"title":    "V3.3 固件什么时候推送",
		"fields":   map[string]string{"想问什么？": "新固件 V3.3 预计什么时候推送到 UD-2300？"},
		// question 模板没有维度字段，创建时要显式打一个维度标签。
		"labels": []string{"机型:UD-2300"},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("Agent 建单状态码 = %d, 想 201", res.StatusCode)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)
	if len(created.Warnings) != 0 {
		t.Errorf("question 模板只有「想问什么？」必填，已填则无 warnings：%+v", created.Warnings)
	}
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	if got.Creator.Agent == nil || got.Creator.Agent.Name != "初筛" {
		t.Errorf("创建者应为具名 Agent：%+v", got.Creator)
	}
	assertContains(t, got.Labels, "机型:UD-2300")
	// question 模板没有预设标签：只有维度字段的值。
	for _, l := range got.Labels {
		if l == "待初筛" {
			t.Errorf("question 模板不应预设待初筛：%v", got.Labels)
		}
	}
}

// 模板工单的字段值校验：未知字段键、单选/维度字段的非法值、未知模板、
// 已停用模板。
func Test模板建单的字段校验(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	t.Run("未知字段", func(t *testing.T) {
		res := createFromTemplate(t, s, web, map[string]any{
			"template": "question",
			"title":    "x",
			"fields":   map[string]string{"想问什么？": "…", "不存在的字段？": "…"},
		})
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "unknown_field" {
			t.Errorf("错误码 = %q, 想 unknown_field", got.Error.Code)
		}
		if !strings.Contains(got.Error.Message, "不存在的字段？") {
			t.Errorf("message 应指出哪个字段未知：%q", got.Error.Message)
		}
	})

	t.Run("单选值不在选项里", func(t *testing.T) {
		fields := bugFields()
		fields["出现得频繁吗？"] = "总是"
		res := createFromTemplate(t, s, web, map[string]any{
			"template": "bug-report", "title": "x", "fields": fields,
		})
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "invalid_field_option" {
			t.Errorf("错误码 = %q, 想 invalid_field_option", got.Error.Code)
		}
	})

	t.Run("维度值不在字典里", func(t *testing.T) {
		fields := bugFields()
		fields["哪台仪器？"] = "BH-730"
		res := createFromTemplate(t, s, web, map[string]any{
			"template": "bug-report", "title": "x", "fields": fields,
		})
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
		}
		var got errorBodyWithExtra
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "unknown_label_value" {
			t.Errorf("错误码 = %q, 想 unknown_label_value", got.Error.Code)
		}
		// 规范化后互相包含的近似值会出现在 suggestions（BH-730 ⊂ BH-7300）。
		assertContains(t, got.Error.Suggestions, "机型:BH-7300")
	})

	t.Run("未知模板", func(t *testing.T) {
		res := createFromTemplate(t, s, web, map[string]any{
			"template": "no-such", "title": "x", "fields": map[string]string{"想问什么？": "…"},
		})
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("状态码 = %d, 想 404", res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "template_not_found" {
			t.Errorf("错误码 = %q, 想 template_not_found", got.Error.Code)
		}
	})

	t.Run("已停用模板", func(t *testing.T) {
		if res := s.Do(t, web, http.MethodPost, "/api/v1/templates/question/deactivate", nil, nil); res.StatusCode != http.StatusOK {
			t.Fatalf("停用状态码 = %d", res.StatusCode)
		}
		res := createFromTemplate(t, s, web, map[string]any{
			"template": "question", "title": "x", "fields": map[string]string{"想问什么？": "…"},
		})
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "template_inactive" {
			t.Errorf("错误码 = %q, 想 template_inactive", got.Error.Code)
		}
	})
}
