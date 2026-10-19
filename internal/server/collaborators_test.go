package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type collaboratorBody struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Deactivated *bool  `json:"deactivated,omitempty"`
	AddedAt     string `json:"added_at"`
}

type collaboratorsBody struct {
	Collaborators []collaboratorBody `json:"collaborators"`
}

func addCollaborator(t testing.TB, s *tbtest.Server, c *http.Client, ref, member string) *http.Response {
	t.Helper()
	return s.Do(t, c, http.MethodPost, "/api/v1/tickets/"+ref+"/collaborators",
		map[string]string{"member": member}, nil)
}

func Test创建者可以加入协作者并记录时间线和通知(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	chen := s.Login("陈工", "secret-pass")
	li := s.Login("李工", "secret-pass")
	number := mustCreateTicket(t, s, chen, "请李工协助排查")

	res := addCollaborator(t, s, chen, number, "李工")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("加入协作者状态码 = %d, 想 201", res.StatusCode)
	}
	var added collaboratorsBody
	tbtest.DecodeJSON(t, res, &added)
	if len(added.Collaborators) != 1 || added.Collaborators[0].Name != "李工" || added.Collaborators[0].AddedAt == "" {
		t.Fatalf("加入后的协作者名单不对：%+v", added)
	}

	var got ticketResponseBody
	s.Do(t, chen, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	if len(got.Timeline) != 2 {
		t.Fatalf("时间线有 %d 条, 想 2", len(got.Timeline))
	}
	event := got.Timeline[1]
	if event.Type != "collaborator_added" || event.Collaborator != "李工" || event.At == "" {
		t.Fatalf("协作者加入事件不对：%+v", event)
	}
	if event.Actor.Member == nil || event.Actor.Member.Name != "陈工" {
		t.Fatalf("协作者加入事件操作者不对：%+v", event.Actor)
	}

	var notes notificationsBody
	s.Do(t, li, http.MethodGet, "/api/v1/notifications", nil, &notes)
	if len(notes.Items) != 1 {
		t.Fatalf("李工通知数 = %d, 想 1", len(notes.Items))
	}
	note := notes.Items[0]
	if note.Kind != "fyi" || note.Reason != "collaborator_added" || note.Summary != "" {
		t.Fatalf("加入协作通知不对：%+v", note)
	}
	if note.Text != "陈工 把你加入了工单「请李工协助排查」的协作" {
		t.Errorf("加入协作通知文案 = %q", note.Text)
	}
}

func Test协作者加入权限矩阵(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	for _, name := range []string{"创建者", "指派人", "初筛人", "旁观者", "协作人"} {
		s.CreateMember(name, "secret-pass")
	}
	admin := s.Login("管理员", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	assignee := s.Login("指派人", "secret-pass")
	triager := s.Login("初筛人", "secret-pass")
	outsider := s.Login("旁观者", "secret-pass")
	collaborator := s.Login("协作人", "secret-pass")
	mustSetTriager(t, s, admin, 4, true)

	create := func(assignee string) string {
		t.Helper()
		body := map[string]any{"title": "权限矩阵 " + assignee, "labels": []string{"机型:UD-2300"}}
		if assignee != "" {
			body["assignee"] = assignee
		}
		res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets", body, nil)
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("建单状态码 = %d", res.StatusCode)
		}
		var created createTicketResponse
		tbtest.DecodeJSON(t, res, &created)
		return created.Number
	}
	assigned := create("指派人")
	unassigned := create("")
	if res := addCollaborator(t, s, assignee, assigned, "协作人"); res.StatusCode != http.StatusCreated {
		t.Fatalf("指派人加入状态码 = %d, 想 201", res.StatusCode)
	}
	if res := addCollaborator(t, s, admin, assigned, "旁观者"); res.StatusCode != http.StatusCreated {
		t.Fatalf("管理员加入状态码 = %d, 想 201", res.StatusCode)
	}
	if res := addCollaborator(t, s, triager, unassigned, "协作人"); res.StatusCode != http.StatusCreated {
		t.Fatalf("未指派时初筛人加入状态码 = %d, 想 201", res.StatusCode)
	}
	assigned2 := create("指派人")
	for name, c := range map[string]*http.Client{"已指派初筛人": triager, "普通成员": outsider, "协作者": collaborator} {
		res := addCollaborator(t, s, c, assigned2, "旁观者")
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("%s加入状态码 = %d, 想 403", name, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "collaborator_not_allowed" {
			t.Errorf("%s错误码 = %q, 想 collaborator_not_allowed", name, got.Error.Code)
		}
	}
}

func Test系统操作者在成员网页和Markdown时间线正常显示(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("创建者", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	number := mustCreateTicket(t, s, creator, "系统操作者显示")
	if res := addCollaborator(t, s, creator, number, "李工"); res.StatusCode != http.StatusCreated {
		t.Fatalf("加入协作者状态码 = %d", res.StatusCode)
	}
	if _, err := s.Store().DB.Exec(`
		UPDATE timeline_events
		SET actor_system=1, actor_member_id=NULL, actor_agent_id=NULL
		WHERE ticket_seq=1 AND type='collaborator_added'`); err != nil {
		t.Fatal(err)
	}

	var got ticketResponseBody
	s.Do(t, creator, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	found := false
	for _, event := range got.Timeline {
		if event.Type != "collaborator_added" {
			continue
		}
		found = true
		if event.Actor.Member != nil || event.Actor.Agent != nil ||
			event.Actor.AuthorName == nil || *event.Actor.AuthorName != "系统" {
			t.Fatalf("网页时间线的系统操作者不对：%+v", event.Actor)
		}
	}
	if !found {
		t.Fatal("时间线上没有协作者加入事件")
	}

	md := string(readAll(t, s.Do(t, creator, http.MethodGet,
		"/api/v1/tickets/"+number+"?format=markdown", nil, nil)))
	if !strings.Contains(md, "系统 把 李工 加入协作") {
		t.Fatalf("Markdown 时间线的系统操作者不对：\n%s", md)
	}
}

func Test协作者可以退出且创建者指派人不能用移除操作退出(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("创建者", "secret-pass")
	s.CreateMember("指派人", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	assignee := s.Login("指派人", "secret-pass")
	li := s.Login("李工", "secret-pass")
	res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "协作者退出", "labels": []string{"机型:UD-2300"}, "assignee": "指派人",
	}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)
	addCollaborator(t, s, creator, created.Number, "李工")

	res = s.Do(t, li, http.MethodPost, "/api/v1/tickets/"+created.Number+"/collaborators/leave", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("本人退出状态码 = %d, 想 200", res.StatusCode)
	}
	var left collaboratorsBody
	tbtest.DecodeJSON(t, res, &left)
	if len(left.Collaborators) != 0 {
		t.Fatalf("退出后名单不对：%+v", left)
	}

	addCollaborator(t, s, creator, created.Number, "李工")
	res = s.Do(t, creator, http.MethodDelete, "/api/v1/tickets/"+created.Number+"/collaborators/3", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("创建者移除状态码 = %d, 想 200", res.StatusCode)
	}
	res = s.Do(t, creator, http.MethodDelete, "/api/v1/tickets/"+created.Number+"/collaborators/1", nil, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("移除创建者状态码 = %d, 想 409", res.StatusCode)
	}
	res = s.Do(t, assignee, http.MethodDelete, "/api/v1/tickets/"+created.Number+"/collaborators/2", nil, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("移除指派人状态码 = %d, 想 409", res.StatusCode)
	}

	var got ticketResponseBody
	s.Do(t, creator, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	types := []string{got.Timeline[3].Type, got.Timeline[5].Type}
	if types[0] != "collaborator_left" || types[1] != "collaborator_removed" {
		t.Fatalf("退出/移除事件不对：%+v", got.Timeline)
	}
}

func Test指派协作者后自动移出且改派走不回名单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("创建者", "secret-pass")
	s.CreateMember("旧指派人", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	s.CreateMember("新指派人", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "指派联动", "labels": []string{"机型:UD-2300"}, "assignee": "旧指派人",
	}, nil)
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)
	addCollaborator(t, s, creator, created.Number, "李工")

	s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+created.Number+"/assign",
		map[string]string{"assignee": "李工"}, nil)
	var assigned ticketResponseBody
	s.Do(t, creator, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &assigned)
	if len(assigned.Collaborators) != 0 {
		t.Fatalf("指派后协作者名单不对：%+v", assigned.Collaborators)
	}

	s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+created.Number+"/assign",
		map[string]string{"assignee": "新指派人"}, nil)
	var reassigned ticketResponseBody
	s.Do(t, creator, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &reassigned)
	if len(reassigned.Collaborators) != 0 {
		t.Fatalf("改派走后不应自动成为协作者：%+v", reassigned.Collaborators)
	}
	for _, ev := range reassigned.Timeline {
		if ev.Type == "collaborator_removed" {
			t.Error("指派联动不应伪造手动移除事件")
		}
	}
}

func Test停用成员不能新加但保留名单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	s.CreateMember("创建者", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	s.CreateMember("王工", "secret-pass")
	admin := s.Login("管理员", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	number := mustCreateTicket(t, s, creator, "停用成员协作")
	addCollaborator(t, s, creator, number, "李工")

	res := s.Do(t, admin, http.MethodPost, "/api/v1/members/3/deactivate", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("停用状态码 = %d", res.StatusCode)
	}
	s.Do(t, admin, http.MethodPost, "/api/v1/members/4/deactivate", nil, nil)
	var got ticketResponseBody
	s.Do(t, creator, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	if len(got.Collaborators) != 1 || got.Collaborators[0].Name != "李工" || got.Collaborators[0].Deactivated == nil {
		t.Fatalf("停用后的协作者名单不对：%+v", got.Collaborators)
	}
	res = addCollaborator(t, s, creator, number, "李工")
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("重复加入停用成员状态码 = %d, 想 422", res.StatusCode)
	}
	res = addCollaborator(t, s, creator, number, "王工")
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("加入停用成员状态码 = %d, 想 422", res.StatusCode)
	}
	var got2 errorBody
	tbtest.DecodeJSON(t, res, &got2)
	if got2.Error.Code != "collaborator_deactivated" {
		t.Fatalf("加入停用成员错误码 = %q", got2.Error.Code)
	}
}

func Test关闭工单仍可加入和移除协作者(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("创建者", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	number := mustCreateTicket(t, s, creator, "关闭后协作")
	closeTicket(t, s, creator, number, closeBody{Reason: "done"})

	if res := addCollaborator(t, s, creator, number, "李工"); res.StatusCode != http.StatusCreated {
		t.Fatalf("关闭后加入状态码 = %d, 想 201", res.StatusCode)
	}
	res := s.Do(t, creator, http.MethodDelete, "/api/v1/tickets/"+number+"/collaborators/2", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("关闭后移除状态码 = %d, 想 200", res.StatusCode)
	}
}

func Test成员Agent按主人权限管理协作者(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("创建者", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	li := s.Login("李工", "secret-pass")
	_, token := s.IssueAgentToken(t, creator, "笔记本")
	creatorAgent := s.AgentClient(token)
	number := mustCreateTicket(t, s, creator, "Agent 管理协作")

	res := addCollaborator(t, s, creatorAgent, number, "李工")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("Agent 加入状态码 = %d, 想 201", res.StatusCode)
	}
	var got ticketResponseBody
	s.Do(t, creator, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	last := got.Timeline[len(got.Timeline)-1]
	if last.Actor.Member == nil || last.Actor.Member.Name != "创建者" || last.Actor.Agent == nil || last.Actor.Agent.Name != "笔记本" {
		t.Fatalf("Agent 时间线署名不对：%+v", last.Actor)
	}

	res = s.Do(t, creatorAgent, http.MethodDelete, "/api/v1/tickets/"+number+"/collaborators/2", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Agent 移除状态码 = %d, 想 200", res.StatusCode)
	}
	_ = li
}

func Test协作者收到加入回复关闭重开移除通知且收件箱不变(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	s.CreateMember("创建者", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	li := s.Login("李工", "secret-pass")
	number := mustCreateTicket(t, s, creator, "协作者通知")
	addCollaborator(t, s, creator, number, "李工")

	var inbox inboxBody
	s.Do(t, li, http.MethodGet, "/api/v1/inbox", nil, &inbox)
	for _, item := range inbox.Items {
		if item.Number == number {
			t.Fatalf("加入协作不应改变收件箱：%+v", inbox.Items)
		}
	}

	reply(t, s, creator, number, "我补了一份资料。")
	closeTicket(t, s, creator, number, closeBody{Reason: "done"})
	s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+number+"/reopen", nil, nil)
	res := s.Do(t, creator, http.MethodDelete, "/api/v1/tickets/"+number+"/collaborators/3", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("移除状态码 = %d, 想 200", res.StatusCode)
	}

	var got notificationsBody
	s.Do(t, li, http.MethodGet, "/api/v1/notifications?limit=200", nil, &got)
	wantReasons := []string{"collaborator_removed", "reopened", "closed", "collaborator_replied", "collaborator_added"}
	if len(got.Items) != len(wantReasons) {
		t.Fatalf("李工通知数 = %d, 想 %d：%+v", len(got.Items), len(wantReasons), got.Items)
	}
	for i, want := range wantReasons {
		if got.Items[i].Reason != want {
			t.Fatalf("通知 %d reason = %q, 想 %q", i, got.Items[i].Reason, want)
		}
		if want == "collaborator_replied" {
			if got.Items[i].Summary != "我补了一份资料。" {
				t.Fatalf("协作者回复通知应带正文摘要：%+v", got.Items[i])
			}
		} else if got.Items[i].Summary != "" {
			t.Fatalf("通知 %q 不应带摘要：%+v", want, got.Items[i])
		}
	}
	removed := got.Items[0]
	if removed.Text != "创建者 把你移出了工单「协作者通知」的协作" {
		t.Errorf("移除通知文案 = %q", removed.Text)
	}
}

func Test访客看不到协作者名单和事件(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("创建者", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	number := mustCreateTicket(t, s, creator, "访客隐藏协作者")
	addCollaborator(t, s, creator, number, "李工")
	link := s.CreateGuestLink(t, creator, number, map[string]any{"note": "检验科-张三"})

	var raw map[string]any
	res := s.Do(t, s.GuestClient(link.Key(t)), http.MethodGet, "/api/v1/tickets/"+number, nil, &raw)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客读工单状态码 = %d", res.StatusCode)
	}
	if _, ok := raw["collaborators"]; ok {
		t.Error("访客响应不应包含 collaborators 字段")
	}
	events := raw["timeline"].([]any)
	for _, item := range events {
		event := item.(map[string]any)
		if event["type"] == "collaborator_added" {
			t.Error("访客不应看到协作者加入事件")
		}
	}

	res = addCollaborator(t, s, s.GuestClient(link.Key(t)), number, "李工")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("访客管理协作者状态码 = %d, 想 403", res.StatusCode)
	}
}
