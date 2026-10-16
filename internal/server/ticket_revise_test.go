package server_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// revise 发一个修订说明请求，返回原始响应。
func revise(t testing.TB, s *tbtest.Server, c *http.Client, ref string, body map[string]any) *http.Response {
	t.Helper()
	return s.Do(t, c, http.MethodPatch, "/api/v1/tickets/"+ref, body, nil)
}

// 创建者（本人或其 Agent）修订说明：版本 +1、每次修订记一条 brief_revised 事件。
// 只改标题的修订同样推进版本。
func Test创建者修订说明推进版本并记事件(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)
	number := mustCreateTicket(t, s, web, "升级 UD-2300 固件")

	// 本人修订正文与标题。
	res := revise(t, s, web, number, map[string]any{
		"title": "升级 UD-2300 固件（含自检）", "brief_md": "背景：……\n要求：……\n补充：升级后自检", "base_revision": 1,
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("修订状态码 = %d, 想 200", res.StatusCode)
	}
	var revised struct {
		Revision int64 `json:"revision"`
	}
	tbtest.DecodeJSON(t, res, &revised)
	if revised.Revision != 2 {
		t.Errorf("revision = %d, 想 2", revised.Revision)
	}

	// 创建者的 Agent 只改标题：同样推进版本并记事件。
	res = revise(t, s, agent, number, map[string]any{"title": "升级 UD-2300 固件（含自检与清理）", "base_revision": 2})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Agent 修订状态码 = %d, 想 200（创建者的 Agent 可修订）", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &revised)
	if revised.Revision != 3 {
		t.Errorf("title-only 修订后的 revision = %d, 想 3", revised.Revision)
	}

	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	if got.Title != "升级 UD-2300 固件（含自检与清理）" {
		t.Errorf("title = %q", got.Title)
	}
	if !strings.Contains(got.Brief.Markdown, "补充：升级后自检") {
		t.Errorf("brief.markdown = %q", got.Brief.Markdown)
	}
	if got.Brief.Revision != 3 {
		t.Errorf("brief.revision = %d, 想 3", got.Brief.Revision)
	}
	if len(got.Timeline) != 3 {
		t.Fatalf("时间线有 %d 条, 想 3", len(got.Timeline))
	}
	for i, ev := range got.Timeline[1:] {
		if ev.Type != "brief_revised" {
			t.Errorf("事件 %d 类型 = %q, 想 brief_revised", i+2, ev.Type)
		}
	}
	if got.Timeline[1].Revision != 2 || got.Timeline[2].Revision != 3 {
		t.Errorf("修订事件的 revision 不对：%+v %+v", got.Timeline[1], got.Timeline[2])
	}
	if got.Timeline[2].Actor.Agent == nil || got.Timeline[2].Actor.Agent.Name != "笔记本" {
		t.Errorf("Agent 修订事件的作者不对：%+v", got.Timeline[2].Actor)
	}

	// Markdown 视图能看到新版本号和修订事件。
	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"?format=markdown", nil, nil)
	mdBytes, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	md := string(mdBytes)
	for _, want := range []string{"## 说明（第 3 版）", "修订说明（第 2 版）", "修订说明（第 3 版）"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown 缺少 %q：\n%s", want, md)
		}
	}
}

// 两个请求带同一个 base_revision 先后提交：后提交的收到 409 revision_conflict，
// 并附 current_revision 提示重读合并。
func Test同一baseRevision先后修订后者409(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "升级 UD-2300 固件")

	if res := revise(t, s, web, number, map[string]any{"brief_md": "第一份修订", "base_revision": 1}); res.StatusCode != http.StatusOK {
		t.Fatalf("第一次修订状态码 = %d, 想 200", res.StatusCode)
	}
	res := revise(t, s, web, number, map[string]any{"brief_md": "第二份修订", "base_revision": 1})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("第二次修订状态码 = %d, 想 409", res.StatusCode)
	}
	var got errorBodyWithExtra
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "revision_conflict" {
		t.Errorf("错误码 = %q, 想 revision_conflict", got.Error.Code)
	}
	if got.Error.CurrentRevision != 2 {
		t.Errorf("current_revision = %d, 想 2", got.Error.CurrentRevision)
	}
	// 冲突的修订不落地。
	var after ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &after)
	if after.Brief.Markdown != "第一份修订" || after.Brief.Revision != 2 {
		t.Errorf("冲突修订不应落地：%q 第 %d 版", after.Brief.Markdown, after.Brief.Revision)
	}
}

// 修订说明必须带 base_revision：缺失返回 422 base_revision_required。
func Test修订缺baseRevision返回422(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "升级固件")

	res := revise(t, s, web, number, map[string]any{"brief_md": "没有版本号"})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "base_revision_required" {
		t.Errorf("错误码 = %q, 想 base_revision_required", got.Error.Code)
	}
}

// 不是创建者的成员不能修订说明：403 not_brief_owner。
func Test非创建者修订说明被拒(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateAdmin("李工", "secret-pass")
	chen := s.Login("陈工", "secret-pass")
	li := s.Login("李工", "secret-pass")
	number := mustCreateTicket(t, s, chen, "升级 UD-2300 固件")

	res := revise(t, s, li, number, map[string]any{"brief_md": "李工的修改", "base_revision": 1})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("状态码 = %d, 想 403", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "not_brief_owner" {
		t.Errorf("错误码 = %q, 想 not_brief_owner", got.Error.Code)
	}
	// 李工自己的单可以改。
	mine := mustCreateTicket(t, s, li, "另一件事")
	if res := revise(t, s, li, mine, map[string]any{"brief_md": "李工的修改", "base_revision": 1}); res.StatusCode != http.StatusOK {
		t.Errorf("创建者修订自己的单状态码 = %d, 想 200", res.StatusCode)
	}
}

// 修订时把标题改成空白：422 title_required。
func Test修订空标题返回422(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "升级固件")

	res := revise(t, s, web, number, map[string]any{"title": "   ", "base_revision": 1})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "title_required" {
		t.Errorf("错误码 = %q, 想 title_required", got.Error.Code)
	}
}
