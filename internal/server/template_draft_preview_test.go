package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// createPreviewTemplate 建一份小模板：一个必填文本字段、一个机型维度字段和一个
// 单选字段，供模板建单草稿的预览测试用。
func createPreviewTemplate(t *testing.T, s *tbtest.Server, web *http.Client) {
	t.Helper()
	res := s.Do(t, web, http.MethodPost, "/api/v1/templates", map[string]any{
		"key": "lab-check",
		"markdown": "---\n名称: 检验申请\n类型: 任务\n---\n" +
			"## 要检查什么项目？ {必填}\n写项目名。\n" +
			"## 哪台仪器？ {维度:机型}\n选择仪器。\n" +
			"## 结果多急？ {单选: 今天 | 本周}\n",
	}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建模板状态码 = %d, 想 201", res.StatusCode)
	}
}

type templateDraftView struct {
	draftBody
	RenderedBriefMD string `json:"rendered_brief_md"`
	TemplateWarning string `json:"template_warning"`
	TemplateFields  []struct {
		Key       string   `json:"key"`
		Title     string   `json:"title"`
		Required  bool     `json:"required"`
		Type      string   `json:"type"`
		Options   []string `json:"options"`
		Dimension string   `json:"dimension"`
	} `json:"template_fields"`
}

func Test模板建单草稿预览返回服务端渲染的说明与字段定义(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	createPreviewTemplate(t, s, web)

	var accepted draftAcceptedBody
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets", map[string]any{
		"template": "lab-check", "title": "模板建单草稿预览",
		"fields": map[string]string{
			"要检查什么项目？": "血常规五分类",
			"哪台仪器？":    "UD-2300",
			"结果多急？":    "今天",
		},
	}, &accepted)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("模板建单草稿状态码 = %d, 想 202", res.StatusCode)
	}

	var view templateDraftView
	s.Do(t, web, http.MethodGet, "/api/v1/drafts/"+accepted.Draft.ID, nil, &view)
	// 期望值来自模板定义与 RenderBrief 的格式（### 问句 + 内容，空行分隔）。
	want := "### 要检查什么项目？\n\n血常规五分类\n\n### 哪台仪器？\n\nUD-2300\n\n### 结果多急？\n\n今天"
	if view.RenderedBriefMD != want {
		t.Fatalf("rendered_brief_md = %q, 想 %q", view.RenderedBriefMD, want)
	}
	if view.TemplateWarning != "" {
		t.Fatalf("模板存在时不应有 warning = %q", view.TemplateWarning)
	}
	if len(view.TemplateFields) != 3 {
		t.Fatalf("template_fields = %+v, 想 3 个字段", view.TemplateFields)
	}
	f := view.TemplateFields
	if f[0].Key != "要检查什么项目？" || f[0].Title != "要检查什么项目？" || !f[0].Required || f[0].Type != "text" {
		t.Fatalf("第一个字段 = %+v", f[0])
	}
	if f[1].Type != "dimension" || f[1].Dimension != "机型" ||
		!containsString(f[1].Options, "UD-2300") || !containsString(f[1].Options, "不确定") {
		t.Fatalf("维度字段 = %+v", f[1])
	}
	if f[2].Type != "radio" || f[2].Required || len(f[2].Options) != 2 ||
		f[2].Options[0] != "今天" || f[2].Options[1] != "本周" {
		t.Fatalf("单选字段 = %+v", f[2])
	}

	// 成员 PUT 只替换 request；视图里的只读计算字段即使回传也被忽略，改 fields 后重算。
	var updated templateDraftView
	res = s.Do(t, web, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID, map[string]any{
		"base_version": accepted.Draft.Version,
		"request": map[string]any{
			"template": "lab-check", "title": "模板建单草稿预览",
			"fields": map[string]string{
				"要检查什么项目？": "尿常规",
				"哪台仪器？":    "UD-2300",
				"结果多急？":    "本周",
			},
			"rendered_brief_md": "调用方伪造的说明", "template_fields": []any{map[string]any{"key": "伪造"}},
		},
	}, &updated)
	if res.StatusCode != http.StatusOK || updated.Version != 2 {
		t.Fatalf("成员改模板草稿 = %d/第 %d 版, 想 200 第 2 版", res.StatusCode, updated.Version)
	}
	if want := "### 要检查什么项目？\n\n尿常规\n\n### 哪台仪器？\n\nUD-2300\n\n### 结果多急？\n\n本周"; updated.RenderedBriefMD != want {
		t.Fatalf("改后 rendered_brief_md = %q, 想 %q", updated.RenderedBriefMD, want)
	}

	// 发出后工单说明与预览逐字一致。
	var sent struct {
		Result struct {
			Number string `json:"number"`
		} `json:"result"`
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": updated.Version}, &sent)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("发出状态码 = %d, 想 201", res.StatusCode)
	}
	var ticket ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+sent.Result.Number, nil, &ticket)
	if ticket.Brief.Markdown != updated.RenderedBriefMD {
		t.Fatalf("发出后的工单说明 = %q, 预览 = %q, 想逐字一致", ticket.Brief.Markdown, updated.RenderedBriefMD)
	}
}

func Test模板建单草稿模板缺失时预览退化为键值并告警(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	createPreviewTemplate(t, s, web)

	var accepted draftAcceptedBody
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets", map[string]any{
		"template": "lab-check", "title": "模板丢失的草稿",
		"fields": map[string]string{
			"要检查什么项目？": "血常规五分类",
			"哪台仪器？":    "UD-2300",
			"结果多急？":    "今天",
		},
	}, &accepted)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("模板建单草稿状态码 = %d, 想 202", res.StatusCode)
	}

	// 模板只能停用不能删除；这里从库里直接删掉，模拟模板彻底丢失的存量数据。
	if _, err := s.Store().DB.Exec(
		`DELETE FROM template_versions WHERE template_key = 'lab-check';
		 DELETE FROM templates WHERE key = 'lab-check'`); err != nil {
		t.Fatalf("删除模板失败：%v", err)
	}

	var view templateDraftView
	s.Do(t, web, http.MethodGet, "/api/v1/drafts/"+accepted.Draft.ID, nil, &view)
	// 退化按字段键排序拼出：键（问句）作标题，只拼已填字段。
	want := "### 哪台仪器？\n\nUD-2300\n\n### 结果多急？\n\n今天\n\n### 要检查什么项目？\n\n血常规五分类"
	if view.RenderedBriefMD != want {
		t.Fatalf("模板缺失时 rendered_brief_md = %q, 想 %q", view.RenderedBriefMD, want)
	}
	if len(view.TemplateFields) != 0 {
		t.Fatalf("模板缺失时 template_fields = %+v, 想为空", view.TemplateFields)
	}
	if view.TemplateWarning == "" || !strings.Contains(view.TemplateWarning, "lab-check") {
		t.Fatalf("模板缺失时 template_warning = %q, 想提示模板已不存在", view.TemplateWarning)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
