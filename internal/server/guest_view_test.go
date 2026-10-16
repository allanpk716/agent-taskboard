package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 访客读模板工单：brief.template_fields 带创建时版本的字段定义（问句与顺序），
// 访客网页据此按字段渲染说明气泡、对未填字段显示「（未填写）」（#38）。
func Test访客读工单带创建时的模板字段定义(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	// 用预置 Bug 反馈模板建单，只填一个字段（其余未填，含必填项）。
	res := createFromTemplate(t, s, web, map[string]any{
		"template": "bug-report",
		"title":    "UD-2300 质控时报警 E-203",
		"fields":   map[string]string{"哪台仪器？": "UD-2300"},
		"labels":   []string{},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201（body=%v）", res.StatusCode, res.Body)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)

	link := mustCreateGuestLink(t, s, web, created.Number, map[string]any{"note": "检验科-张三"})
	guest := s.GuestClient(guestKeyOf(t, link))

	var got struct {
		Brief struct {
			TemplateFields []struct {
				Key       string `json:"key"`
				Title     string `json:"title"`
				Required  bool   `json:"required"`
				Type      string `json:"type"`
				Dimension string `json:"dimension"`
			} `json:"template_fields"`
		} `json:"brief"`
	}
	res = s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客读工单状态码 = %d, 想 200", res.StatusCode)
	}
	// 字段定义按模板顺序给出（第一个字段是维度字段「哪台仪器？」）。
	if len(got.Brief.TemplateFields) != 9 {
		t.Fatalf("brief.template_fields 有 %d 项, 想 9（Bug 反馈模板的全部字段，含未填的）", len(got.Brief.TemplateFields))
	}
	first := got.Brief.TemplateFields[0]
	if first.Title != "哪台仪器？" || first.Type != "dimension" || first.Dimension != "机型" || !first.Required {
		t.Errorf("第一个字段定义不对：%+v", first)
	}
	// 未填的必填字段也在定义里（网页显示「（未填写）」依据它）。
	found := false
	for _, f := range got.Brief.TemplateFields {
		if f.Title == "你做了哪些操作？" && f.Required {
			found = true
		}
	}
	if !found {
		t.Error("字段定义里应有「你做了哪些操作？」（必填，未填写）")
	}

	// 非模板工单没有 template_fields。
	plain := mustCreateTicket(t, s, web, "整理对照表")
	var plainView struct {
		Brief struct {
			TemplateFields []map[string]any `json:"template_fields"`
		} `json:"brief"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+plain, nil, &plainView)
	if len(plainView.Brief.TemplateFields) != 0 {
		t.Errorf("非模板工单不应有 template_fields：%+v", plainView.Brief.TemplateFields)
	}
}
