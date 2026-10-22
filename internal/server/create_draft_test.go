package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type createDraftRequest struct {
	Kind      string            `json:"kind"`
	Title     string            `json:"title"`
	BriefMD   string            `json:"brief_md"`
	Fields    map[string]string `json:"fields"`
	Labels    []string          `json:"labels"`
	Assignee  string            `json:"assignee"`
	GuestLink *struct {
		Note         string `json:"note"`
		ValidDays    int    `json:"valid_days"`
		CanEditBrief bool   `json:"can_edit_brief"`
	} `json:"guest_link"`
}

func TestRequiredAgent建单草稿不占编号且通知指向预览页(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")

	var before notificationsBody
	s.Do(t, web, http.MethodGet, "/api/v1/notifications", nil, &before)
	var accepted draftAcceptedBody
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "Agent 起草的建单", "brief_md": "请先确认这份草稿。",
		"labels": []string{"机型:UD-2300"}, "assignee": "李工",
		"guest_link": map[string]any{"note": "检验科-张三", "can_edit_brief": true},
	}, &accepted)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("Agent 建单状态码 = %d/%+v, 想 202", res.StatusCode, accepted)
	}
	if accepted.Draft.Kind != "create" || accepted.Draft.Status != "pending" || accepted.Draft.Version != 1 {
		t.Fatalf("建单草稿 = %+v", accepted.Draft)
	}
	if accepted.Draft.PreviewURL != "/drafts/"+accepted.Draft.ID ||
		accepted.PreviewURL != "/drafts/"+accepted.Draft.ID {
		t.Fatalf("preview_url = %q/%q, 想 /drafts/%s", accepted.Draft.PreviewURL, accepted.PreviewURL, accepted.Draft.ID)
	}

	number := mustCreateTicket(t, s, web, "正常建单")
	if number[len(number)-3:] != "001" {
		t.Fatalf("草稿后的正常建单编号 = %q, 想当日 001", number)
	}

	var got notificationsBody
	s.Do(t, web, http.MethodGet, "/api/v1/notifications", nil, &got)
	if len(got.Items) != len(before.Items)+1 || got.UnreadCount != before.UnreadCount {
		t.Fatalf("建单草稿通知/未读：%d/%d → %d/%d", len(before.Items), before.UnreadCount,
			len(got.Items), got.UnreadCount)
	}
	note := got.Items[0]
	if note.Kind != "draft" || note.Reason != "draft_pending" ||
		note.Text != "你的 Agent · 笔记本 写了建单草稿待确认：Agent 起草的建单" ||
		note.URL != "/drafts/"+accepted.Draft.ID {
		t.Fatalf("建单草稿通知 = %+v", note)
	}
}

func Test模板建单草稿带必填警告(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")

	var accepted draftAcceptedBody
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets", map[string]any{
		"template": "bug-report", "title": "模板建单草稿", "fields": map[string]string{},
		"guest_link": map[string]any{"note": "检验科-张三", "can_edit_brief": true},
	}, &accepted)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("模板建单草稿状态码 = %d/%+v, 想 202", res.StatusCode, accepted)
	}
	if len(accepted.Draft.Warnings) == 0 {
		t.Fatalf("模板建单草稿 warnings = %+v", accepted.Draft)
	}
}

func Test成员修改建单草稿后发出并只在响应拿到访客链接(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	li := s.Login("李工", "secret-pass")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")

	var accepted draftAcceptedBody
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "建单草稿发出", "brief_md": "Agent 起草的说明。",
		"labels": []string{"机型:UD-2300"}, "assignee": "陈工",
	}, &accepted)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("Agent 建单草稿状态码 = %d, 想 202", res.StatusCode)
	}
	var direct draftBody
	s.Do(t, web, http.MethodGet, "/api/v1/drafts/"+accepted.Draft.ID, nil, &direct)
	if direct.ID == "" {
		t.Fatalf("成员读建单草稿 %s 失败", accepted.Draft.ID)
	}

	var updated draftBody
	res = s.Do(t, web, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID, map[string]any{
		"base_version": accepted.Draft.Version,
		"request": map[string]any{
			"title": "建单草稿发出", "brief_md": "成员确认的说明。",
			"labels": []string{"机型:UD-2300", "机型:US-1680"}, "assignee": "李工",
			"guest_link": map[string]any{"note": "检验科-张三", "can_edit_brief": true},
		},
	}, &updated)
	if res.StatusCode != http.StatusOK || updated.Version != 2 {
		t.Fatalf("成员改建单草稿 %s = %d/%+v, 想 200 第 2 版", accepted.Draft.ID, res.StatusCode, updated)
	}

	var sent struct {
		Result struct {
			Number   string   `json:"number"`
			Seq      int64    `json:"seq"`
			URL      string   `json:"url"`
			Warnings []string `json:"warnings"`
			Assignee struct {
				Name string `json:"name"`
			} `json:"assignee"`
			GuestLink struct {
				URL       string `json:"url"`
				ShareText string `json:"share_text"`
			} `json:"guest_link"`
		} `json:"result"`
		Signature string `json:"signature"`
		Drafting  []struct {
			Version int64 `json:"version"`
		} `json:"drafting"`
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": updated.Version}, &sent)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("发出建单草稿状态码 = %d/%+v, 想 201", res.StatusCode, sent)
	}
	if sent.Result.Number == "" || sent.Result.URL != "/t/"+sent.Result.Number ||
		sent.Result.Assignee.Name != "李工" || sent.Result.GuestLink.URL == "" ||
		!strings.Contains(sent.Result.GuestLink.ShareText, sent.Result.GuestLink.URL) {
		t.Fatalf("发出结果 = %+v", sent.Result)
	}
	if sent.Signature != "经陈工修改并确认" || len(sent.Drafting) != 2 {
		t.Fatalf("署名/起草过程 = %q/%+v", sent.Signature, sent.Drafting)
	}

	var got ticketResponseBody
	s.Do(t, li, http.MethodGet, "/api/v1/tickets/"+sent.Result.Number, nil, &got)
	if got.Title != "建单草稿发出" || got.Brief.Markdown != "成员确认的说明。" {
		t.Fatalf("发出后的工单内容 = %+v", got)
	}
	if got.Assignee.Member.Name != "李工" || len(got.Labels) != 2 {
		t.Fatalf("发出后的指派/标签 = %+v/%v", got.Assignee, got.Labels)
	}

	var agentDraft draftBody
	s.Do(t, agent, http.MethodGet, "/api/v1/drafts/"+accepted.Draft.ID, nil, &agentDraft)
	if agentDraft.Status != "sent" || agentDraft.Ticket == nil || agentDraft.Ticket.Number != sent.Result.Number {
		t.Fatalf("Agent 读已发出草稿 = %+v", agentDraft)
	}
}

func Test丢弃建单草稿不留工单且他人不可见(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	li := s.Login("李工", "secret-pass")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")

	var accepted draftAcceptedBody
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "会被丢弃的建单草稿", "labels": []string{"机型:UD-2300"},
		"guest_link": map[string]any{"note": "检验科-张三"},
	}, &accepted)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("Agent 建单草稿状态码 = %d, 想 202", res.StatusCode)
	}
	if res := s.Do(t, li, http.MethodGet, "/api/v1/drafts/"+accepted.Draft.ID, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("他人读建单草稿状态码 = %d, 想 404", res.StatusCode)
	}
	res = s.Do(t, web, http.MethodDelete, "/api/v1/drafts/"+accepted.Draft.ID, nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("丢弃建单草稿状态码 = %d, 想 204", res.StatusCode)
	}
	if res := s.Do(t, agent, http.MethodGet, "/api/v1/drafts/"+accepted.Draft.ID, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("丢弃后读建单草稿状态码 = %d, 想 404", res.StatusCode)
	}
	number := mustCreateTicket(t, s, web, "丢弃后的正常建单")
	if number[len(number)-3:] != "001" {
		t.Fatalf("丢弃后的正常建单编号 = %q, 想当日 001", number)
	}
}
