package server_test

// #274 收件箱「协作中」与建单指定协作者（抄送）：
// collaborator=me 只列本人作为协作者的工单；建单/建单草稿可一并指定协作者，
// 复用协作者的现有事件与通知，非法成员在建单前被拒绝。

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// memberIDsByName 读管理员视角的成员名单，换成 姓名 → id 的映射（测试不依赖
// 自增 id 的具体值）。
func memberIDsByName(t *testing.T, s *tbtest.Server, admin *http.Client) map[string]int64 {
	t.Helper()
	var body struct {
		Members []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"members"`
	}
	res := s.Do(t, admin, http.MethodGet, "/api/v1/members", nil, &body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读成员名单状态码 = %d, 想 200", res.StatusCode)
	}
	out := map[string]int64{}
	for _, m := range body.Members {
		out[m.Name] = m.ID
	}
	return out
}

func queryCollaboratorMe(t *testing.T, s *tbtest.Server, c *http.Client, extra string) listBody {
	t.Helper()
	var body listBody
	res := s.Do(t, c, http.MethodGet, "/api/v1/tickets?collaborator=me"+extra, nil, &body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/tickets?collaborator=me%s 状态码 = %d, 想 200", extra, res.StatusCode)
	}
	return body
}

func ticketNumbers(items []listItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Number)
	}
	return out
}

func Test工单列表collaboratorMe只列本人协作的工单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	for _, name := range []string{"创建者", "指派人", "协作人", "旁观者"} {
		s.CreateMember(name, "secret-pass")
	}
	admin := s.Login("管理员", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	collaborator := s.Login("协作人", "secret-pass")
	outsider := s.Login("旁观者", "secret-pass")
	ids := memberIDsByName(t, s, admin)

	// 抄送给协作人；指派给协作人；协作人自己创建——后两张都不算「协作中」。
	var cc createTicketResponse
	res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "抄送协作人", "brief_md": "请知情。", "labels": []string{"机型:UD-2300"},
		"assignee": "指派人", "collaborators": []any{"协作人"},
	}, &cc)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单（带协作者）状态码 = %d, 想 201", res.StatusCode)
	}
	assignedToMe := mustCreateTicket(t, s, creator, "指派给协作人")
	s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+assignedToMe+"/assign",
		map[string]string{"assignee": "协作人"}, nil)
	createdByMe := mustCreateTicket(t, s, collaborator, "协作人自建")
	if createdByMe == "" {
		t.Fatal("前置工单编号为空")
	}

	got := queryCollaboratorMe(t, s, collaborator, "")
	if got.Total != 1 || len(got.Items) != 1 || got.Items[0].Number != cc.Number {
		t.Fatalf("collaborator=me 结果 = %v, 想只有 %s", ticketNumbers(got.Items), cc.Number)
	}
	if v, ok := got.Filters["collaborator"]; !ok || v != "me" {
		t.Fatalf("filters 回显里的 collaborator = %v, 想 me", got.Filters)
	}
	if closed := queryCollaboratorMe(t, s, collaborator, "&status=closed"); closed.Total != 0 {
		t.Fatalf("status=closed 组合的协作中数量 = %d, 想 0", closed.Total)
	}

	// 成员 Agent 使用同一条件时等同主人。
	_, token := s.IssueAgentToken(t, collaborator, "工位")
	agent := s.AgentClient(token)
	if agentGot := queryCollaboratorMe(t, s, agent, ""); agentGot.Total != 1 ||
		agentGot.Items[0].Number != cc.Number {
		t.Fatalf("成员 Agent 的 collaborator=me 结果 = %v", ticketNumbers(agentGot.Items))
	}

	// 可见范围照常生效：旁观者不是协作者，看不见这张工单。
	if outsiderGot := queryCollaboratorMe(t, s, outsider, ""); outsiderGot.Total != 0 {
		t.Fatalf("旁观者的 collaborator=me 数量 = %d, 想 0", outsiderGot.Total)
	}

	// 被移出协作后不再出现。
	res = s.Do(t, creator, http.MethodDelete,
		"/api/v1/tickets/"+cc.Number+"/collaborators/"+itoa(ids["协作人"]), nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("移出协作者状态码 = %d, 想 200", res.StatusCode)
	}
	if afterRemove := queryCollaboratorMe(t, s, collaborator, ""); afterRemove.Total != 0 {
		t.Fatalf("移出后的 collaborator=me 数量 = %d, 想 0", afterRemove.Total)
	}

	// 重新加入后再被改成指派人：按现有规则随即不算协作者。
	if res := addCollaborator(t, s, creator, cc.Number, "协作人"); res.StatusCode != http.StatusCreated {
		t.Fatalf("重新加入协作者状态码 = %d, 想 201", res.StatusCode)
	}
	s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+cc.Number+"/assign",
		map[string]string{"assignee": "协作人"}, nil)
	if afterAssign := queryCollaboratorMe(t, s, collaborator, ""); afterAssign.Total != 0 {
		t.Fatalf("改为指派人后的 collaborator=me 数量 = %d, 想 0", afterAssign.Total)
	}

	// 只接受 me；其他取值按参数校验风格返回 422。
	var bad errorBody
	res = s.Do(t, collaborator, http.MethodGet, "/api/v1/tickets?collaborator=协作人", nil, &bad)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("collaborator=协作人 状态码 = %d, 想 422", res.StatusCode)
	}
	if bad.Error.Code != "invalid_collaborator" {
		t.Fatalf("collaborator 非法取值错误码 = %q, 想 invalid_collaborator", bad.Error.Code)
	}
}

func Test建单时指定协作者加入名单并通知(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	for _, name := range []string{"创建者", "指派人", "王工", "李工"} {
		s.CreateMember(name, "secret-pass")
	}
	admin := s.Login("管理员", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	wang := s.Login("王工", "secret-pass")
	li := s.Login("李工", "secret-pass")
	ids := memberIDsByName(t, s, admin)

	var before listBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets?status=all&limit=1", nil, &before)

	// 混用姓名、数字 id 与数字字符串；指派人和创建者按现有规则不算协作者。
	var created createTicketResponse
	res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "建单时抄送", "brief_md": "请两位知情。", "labels": []string{"机型:UD-2300"},
		"assignee":      "指派人",
		"collaborators": []any{"王工", ids["李工"], itoa(ids["李工"]), "创建者", "指派人"},
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单（带协作者）状态码 = %d, 想 201", res.StatusCode)
	}
	var got ticketResponseBody
	s.Do(t, creator, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	if len(got.Collaborators) != 2 || got.Collaborators[0].Name != "王工" || got.Collaborators[1].Name != "李工" {
		t.Fatalf("建单后的协作者名单不对：%+v", got.Collaborators)
	}
	addedEvents := 0
	for _, ev := range got.Timeline {
		if ev.Type == "collaborator_added" {
			addedEvents++
			if ev.Collaborator == "" || ev.Actor.Member == nil || ev.Actor.Member.Name != "创建者" {
				t.Fatalf("建单时的 collaborator_added 事件不对：%+v", ev)
			}
		}
	}
	if addedEvents != 2 {
		t.Fatalf("collaborator_added 事件数 = %d, 想 2", addedEvents)
	}

	var wangNotes notificationsBody
	s.Do(t, wang, http.MethodGet, "/api/v1/notifications", nil, &wangNotes)
	found := false
	for _, n := range wangNotes.Items {
		if n.Reason == "collaborator_added" && strings.Contains(n.Text, "建单时抄送") {
			found = true
		}
	}
	if !found {
		t.Fatalf("王工没有收到「加入协作」通知：%+v", wangNotes.Items)
	}
	var liNotes notificationsBody
	s.Do(t, li, http.MethodGet, "/api/v1/notifications", nil, &liNotes)
	if len(liNotes.Items) != 1 || liNotes.Items[0].Reason != "collaborator_added" {
		t.Fatalf("李工的加入协作通知不对：%+v", liNotes.Items)
	}

	// 停用成员：整个建单请求被拒绝，且不留下工单。
	s.Do(t, admin, http.MethodPost, "/api/v1/members/"+itoa(ids["李工"])+"/deactivate", nil, nil)
	var errBody errorBody
	res = s.Do(t, creator, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "停用协作者", "labels": []string{"机型:UD-2300"}, "collaborators": []any{"李工"},
	}, &errBody)
	if res.StatusCode != http.StatusUnprocessableEntity || errBody.Error.Code != "collaborator_deactivated" {
		t.Fatalf("停用成员建单 = %d/%s, 想 422 collaborator_deactivated", res.StatusCode, errBody.Error.Code)
	}
	// 不存在的成员与写错的类型同样拒绝。
	for name, body := range map[string]any{
		"不存在":    []any{"没有这位成员"},
		"类型错误":   "王工",
		"元素类型错误": []any{true},
	} {
		res = s.Do(t, creator, http.MethodPost, "/api/v1/tickets", map[string]any{
			"title": "非法协作者 " + name, "labels": []string{"机型:UD-2300"}, "collaborators": body,
		}, &errBody)
		wantCode := "invalid_collaborators"
		if name == "不存在" {
			wantCode = "unknown_member"
		}
		if res.StatusCode != http.StatusUnprocessableEntity || errBody.Error.Code != wantCode {
			t.Fatalf("%s建单 = %d/%s, 想 422 %s", name, res.StatusCode, errBody.Error.Code, wantCode)
		}
	}
	var after listBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets?status=all&limit=1", nil, &after)
	if after.Total != before.Total+1 {
		t.Fatalf("非法协作者请求后的工单总数 = %d, 想 %d（只多出成功那一张）", after.Total, before.Total+1)
	}
}

type createDraftCollaboratorsBody struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	Request struct {
		Title         string   `json:"title"`
		Labels        []string `json:"labels"`
		Assignee      string   `json:"assignee"`
		Collaborators []string `json:"collaborators"`
	} `json:"request"`
	Result struct {
		Number string `json:"number"`
	} `json:"result"`
}

func Test建单草稿保存协作者并在发出后生效(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	s.CreateMember("创建者", "secret-pass")
	s.CreateMember("王工", "secret-pass")
	admin := s.Login("管理员", "secret-pass")
	web := s.Login("创建者", "secret-pass")
	wang := s.Login("王工", "secret-pass")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")

	var accepted draftAcceptedBody
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "草稿抄送王工", "brief_md": "请确认后发出。",
		"labels":        []string{"机型:UD-2300"},
		"collaborators": []any{"王工"},
	}, &accepted)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("Agent 建单草稿状态码 = %d, 想 202", res.StatusCode)
	}
	var saved createDraftCollaboratorsBody
	s.Do(t, web, http.MethodGet, "/api/v1/drafts/"+accepted.Draft.ID, nil, &saved)
	if len(saved.Request.Collaborators) != 1 || saved.Request.Collaborators[0] != "王工" {
		t.Fatalf("草稿保存的协作者 = %+v, 想 [王工]", saved.Request.Collaborators)
	}

	// 成员可以在草稿上改协作者（这里同时验证成员 id 写法与重复项原样保存）。
	ids := memberIDsByName(t, s, admin)
	var updated createDraftCollaboratorsBody
	res = s.Do(t, web, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID, map[string]any{
		"base_version": accepted.Draft.Version,
		"request": map[string]any{
			"title": "草稿抄送王工", "brief_md": "成员确认的说明。", "labels": []string{"机型:UD-2300"},
			"collaborators": []string{"王工", itoa(ids["王工"])},
		},
	}, &updated)
	if res.StatusCode != http.StatusOK || updated.Version != 2 {
		t.Fatalf("改草稿状态码/版本 = %d/%d, 想 200/2", res.StatusCode, updated.Version)
	}
	if len(updated.Request.Collaborators) != 2 {
		t.Fatalf("改后的草稿协作者 = %+v", updated.Request.Collaborators)
	}

	var sent struct {
		Result struct {
			Number string `json:"number"`
		} `json:"result"`
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": 2}, &sent)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("发出建单草稿状态码 = %d, 想 201", res.StatusCode)
	}
	number := sent.Result.Number
	if number == "" {
		t.Fatal("发出响应缺少工单编号")
	}
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	if len(got.Collaborators) != 1 || got.Collaborators[0].Name != "王工" {
		t.Fatalf("草稿发出后的协作者名单不对：%+v", got.Collaborators)
	}
	var notes notificationsBody
	s.Do(t, wang, http.MethodGet, "/api/v1/notifications", nil, &notes)
	if len(notes.Items) != 1 || notes.Items[0].Reason != "collaborator_added" {
		t.Fatalf("草稿发出后的加入协作通知不对：%+v", notes.Items)
	}
}
