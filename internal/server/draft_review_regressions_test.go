package server_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test说明修订草稿确认新事件后可发出(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	li := s.Login("李工", "secret-pass")
	number := mustCreateTicket(t, s, web, "固件升级")
	s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/assign", map[string]any{"assignee": "李工"}, nil)
	_, agent := issueRequiredAgent(t, s, web, "笔记本")

	var accepted draftAcceptedBody
	s.Do(t, agent, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "Agent 修订后的说明。", "base_revision": 1}, &accepted)
	s.Do(t, li, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "草稿写成后的新变化。"}, nil)

	var conflict struct {
		Error struct {
			Code       string `json:"code"`
			ThroughSeq int64  `json:"through_seq"`
		} `json:"error"`
	}
	res := s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": accepted.Draft.Version}, &conflict)
	if res.StatusCode != http.StatusConflict || conflict.Error.Code != "ticket_changed" {
		t.Fatalf("未确认新事件发出说明修订 = %d/%+v, 想 409 ticket_changed", res.StatusCode, conflict)
	}

	var sent struct {
		Result struct {
			Revision int64 `json:"revision"`
		} `json:"result"`
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": accepted.Draft.Version, "confirm_through_seq": conflict.Error.ThroughSeq}, &sent)
	if res.StatusCode != http.StatusCreated || sent.Result.Revision != 2 {
		t.Fatalf("带 confirm_through_seq 发出说明修订 = %d/%+v, 想 201/第 2 版", res.StatusCode, sent)
	}
}

func Test后续Agent版本不冲掉成员修改并确认署名(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "固件升级")
	s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/assign", map[string]any{"assignee": "陈工"}, nil)
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	accepted := mustReplyDraft(t, s, agent, number)

	var memberVersion draftBody
	s.Do(t, web, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"base_version": 1, "request": map[string]any{"body_md": "成员改过的第二版。"}}, &memberVersion)
	var agentVersion draftBody
	s.Do(t, agent, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"base_version": memberVersion.Version, "request": map[string]any{"body_md": "Agent 的第三版。"}}, &agentVersion)

	var sent struct {
		Signature string `json:"signature"`
	}
	res := s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": agentVersion.Version}, &sent)
	if res.StatusCode != http.StatusCreated || sent.Signature != "经陈工修改并确认" {
		t.Fatalf("Agent v3 后发出 = %d/%q, 想 201/成员修改并确认", res.StatusCode, sent.Signature)
	}
}

func Test草稿版本冲突附带last_author(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "固件升级")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	accepted := mustReplyDraft(t, s, agent, number)

	var first draftBody
	s.Do(t, web, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"base_version": 1, "request": map[string]any{"body_md": "成员改过的版本。"}}, &first)
	var conflict struct {
		Error struct {
			Code       string `json:"code"`
			LastAuthor string `json:"last_author"`
		} `json:"error"`
	}
	res := s.Do(t, agent, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"base_version": 1, "request": map[string]any{"body_md": "过期版本。"}}, &conflict)
	if res.StatusCode != http.StatusConflict || conflict.Error.Code != "draft_version_conflict" {
		t.Fatalf("版本冲突 = %d/%+v", res.StatusCode, conflict)
	}
	if conflict.Error.LastAuthor != "陈工" {
		t.Fatalf("last_author = %q, 想 陈工", conflict.Error.LastAuthor)
	}
}

func Test网页读取草稿作者与已发出结果(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "固件升级")
	s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/assign", map[string]any{"assignee": "陈工"}, nil)
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	accepted := mustReplyDraft(t, s, agent, number)

	var got struct {
		MyDraft *struct {
			Author struct {
				Agent *struct {
					Name string `json:"name"`
				} `json:"agent"`
			} `json:"author"`
		} `json:"my_draft"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	if got.MyDraft == nil || got.MyDraft.Author.Agent == nil || got.MyDraft.Author.Agent.Name != "笔记本" {
		t.Fatalf("网页读取 my_draft.author.agent = %+v, 想执笔 Agent 笔记本", got.MyDraft)
	}

	var sent struct {
		Result struct {
			ReplyNo     int64 `json:"reply_no"`
			TimelineSeq int64 `json:"timeline_seq"`
		} `json:"result"`
	}
	res := s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": accepted.Draft.Version}, &sent)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("发出回复草稿状态码 = %d, 想 201", res.StatusCode)
	}
	var sentDraft draftBody
	s.Do(t, agent, http.MethodGet, "/api/v1/drafts/"+accepted.Draft.ID, nil, &sentDraft)
	if sentDraft.Status != "sent" || sentDraft.Result == nil {
		t.Fatalf("已发出回复草稿 = %+v, 想 sent 且带 result", sentDraft)
	}
	result, ok := sentDraft.Result.(map[string]any)
	if !ok || result["reply_no"] == nil || result["timeline_seq"] == nil {
		t.Fatalf("已发出回复草稿 result = %#v, 想回复编号和时间线 seq", sentDraft.Result)
	}
}

func TestMarkdown草稿种类与已发出说明修订结果(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "固件升级")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	var accepted draftAcceptedBody
	s.Do(t, agent, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "Agent 修订后的说明。", "base_revision": 1}, &accepted)

	res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"?format=markdown", nil, nil)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "- 说明修订草稿 · 第 1 版 · 未发出") {
		t.Fatalf("markdown 草稿种类错误：%d\n%s", res.StatusCode, body)
	}

	s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": accepted.Draft.Version}, nil)
	var sentDraft draftBody
	s.Do(t, agent, http.MethodGet, "/api/v1/drafts/"+accepted.Draft.ID, nil, &sentDraft)
	result, ok := sentDraft.Result.(map[string]any)
	if sentDraft.Status != "sent" || !ok || result["revision"] == nil {
		t.Fatalf("已发出说明修订草稿 = %+v/%#v, 想 sent 且带 revision", sentDraft, sentDraft.Result)
	}
}

func Test交付与说明修订草稿通知文案(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "固件升级")
	s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/assign", map[string]any{"assignee": "陈工"}, nil)
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	var before notificationsBody
	s.Do(t, web, http.MethodGet, "/api/v1/notifications", nil, &before)

	var delivery draftAcceptedBody
	s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "交付草稿。", "deliver": true}, &delivery)
	var got notificationsBody
	s.Do(t, web, http.MethodGet, "/api/v1/notifications", nil, &got)
	if len(got.Items) != len(before.Items)+1 || got.Items[0].Text != "你的 Agent · 笔记本 写了交付草稿待确认" {
		t.Fatalf("交付草稿通知 = %+v", got.Items)
	}
	s.Do(t, web, http.MethodDelete, "/api/v1/drafts/"+delivery.Draft.ID, nil, nil)

	var brief draftAcceptedBody
	s.Do(t, agent, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "说明修订草稿。", "base_revision": 1}, &brief)
	s.Do(t, web, http.MethodGet, "/api/v1/notifications", nil, &got)
	if len(got.Items) != len(before.Items)+2 || got.Items[0].Text != "你的 Agent · 笔记本 写了说明修订草稿待确认" {
		t.Fatalf("说明修订草稿通知 = %+v", got.Items)
	}
}

func Test建单草稿保留指定访客链接到期日(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	var accepted draftAcceptedBody
	s.Do(t, agent, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "建单草稿访客链接", "brief_md": "说明", "labels": []string{"机型:UD-2300"},
		"guest_link": map[string]any{"note": "检验科-张三", "can_edit_brief": true},
	}, &accepted)
	var updated draftBody
	s.Do(t, web, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID, map[string]any{
		"base_version": 1,
		"request": map[string]any{
			"title": "建单草稿访客链接", "brief_md": "说明", "labels": []string{"机型:UD-2300"},
			"guest_link": map[string]any{"note": "检验科-张三", "expires_at": "2999-01-01", "can_edit_brief": true},
		},
	}, &updated)

	var sent struct {
		Result struct {
			GuestLink struct {
				ExpiresAt string `json:"expires_at"`
			} `json:"guest_link"`
		} `json:"result"`
	}
	res := s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": updated.Version}, &sent)
	if res.StatusCode != http.StatusCreated || !strings.HasPrefix(sent.Result.GuestLink.ExpiresAt, "2999-01-01") {
		t.Fatalf("建单草稿发出后的访客链接到期日 = %d/%q", res.StatusCode, sent.Result.GuestLink.ExpiresAt)
	}
}

func Test并发丢弃草稿时列表不失败(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	for i := 0; i < 30; i++ {
		number := mustCreateTicket(t, s, web, fmt.Sprintf("草稿 %d", i))
		accepted := mustReplyDraft(t, s, agent, number)
		var wg sync.WaitGroup
		for j := 0; j < 8; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := web.Get(s.URL() + "/api/v1/drafts")
				if err == nil {
					_, _ = io.Copy(io.Discard, res.Body)
					_ = res.Body.Close()
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodDelete, s.URL()+"/api/v1/drafts/"+accepted.Draft.ID, nil)
			res, err := web.Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, res.Body)
				_ = res.Body.Close()
			}
		}()
		wg.Wait()
		var list struct {
			Count int `json:"count"`
		}
		res := s.Do(t, web, http.MethodGet, "/api/v1/drafts", nil, &list)
		if res.StatusCode != http.StatusOK || list.Count != 0 {
			t.Fatalf("并发丢弃后列表 = %d/%d", res.StatusCode, list.Count)
		}
	}
}
