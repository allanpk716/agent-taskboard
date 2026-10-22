package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type draftBody struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Version int64  `json:"version"`
	Request struct {
		BodyMD        string            `json:"body_md"`
		Title         *string           `json:"title"`
		BriefMD       *string           `json:"brief_md"`
		Fields        map[string]string `json:"fields"`
		AttachmentIDs []string          `json:"attachment_ids"`
		Deliver       bool              `json:"deliver"`
		BaseRevision  int64             `json:"base_revision"`
	} `json:"request"`
	Attachments []attachmentViewBody `json:"attachments"`
	Ticket      *struct {
		Number string `json:"number"`
		Seq    int64  `json:"seq"`
		URL    string `json:"url"`
	} `json:"ticket"`
	Author struct {
		Member struct {
			Name string `json:"name"`
		} `json:"member"`
		Agent *struct {
			Name string `json:"name"`
		} `json:"agent"`
	} `json:"author"`
	Versions []struct {
		Version int64  `json:"version"`
		Author  string `json:"author"`
	} `json:"versions"`
	Base struct {
		TimelineSeq   int64 `json:"timeline_seq"`
		BriefRevision int64 `json:"brief_revision"`
	} `json:"base"`
	NewEvents  int64           `json:"new_events"`
	PreviewURL string          `json:"preview_url"`
	URL        string          `json:"url"`
	Result     any             `json:"result"`
	Warnings   []ticketWarning `json:"warnings"`
}

type draftAcceptedBody struct {
	Draft      draftBody `json:"draft"`
	PreviewURL string    `json:"preview_url"`
	Message    string    `json:"message"`
	Next       string    `json:"next"`
}

type draftPendingError struct {
	Error struct {
		Code  string `json:"code"`
		Draft struct {
			ID         string `json:"id"`
			PreviewURL string `json:"preview_url"`
			URL        string `json:"url"`
		} `json:"draft"`
		Hint string `json:"hint"`
	} `json:"error"`
}

func issueRequiredAgent(t *testing.T, s *tbtest.Server, web *http.Client, name string) (int64, *http.Client) {
	t.Helper()
	var issued struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	res := s.Do(t, web, http.MethodPost, "/api/v1/me/agent-tokens",
		map[string]any{"name": name, "draft_mode": "required"}, &issued)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("签发 required Agent 状态码 = %d", res.StatusCode)
	}
	return issued.ID, s.AgentClient(issued.Token)
}

func mustReplyDraft(t *testing.T, s *tbtest.Server, agent *http.Client, number string) draftAcceptedBody {
	t.Helper()
	var got draftAcceptedBody
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "Agent 起草的回复。"}, &got)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("Agent 回复状态码 = %d, 想 202", res.StatusCode)
	}
	return got
}

func TestRequiredAgent回复保存为草稿且发出前零影响(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "固件升级")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")

	var before ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &before)
	beforeAccess := readAccessRecords(t, s, web, number)
	var beforeNotes notificationsBody
	s.Do(t, web, http.MethodGet, "/api/v1/notifications", nil, &beforeNotes)
	headers := map[string]string{"Idempotency-Key": "draft-once"}
	first := s.DoWithHeaders(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies", headers,
		map[string]any{"body_md": "Agent 起草的回复。"}, nil)
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("首次回复状态码 = %d, 想 202", first.StatusCode)
	}
	var accepted draftAcceptedBody
	tbtest.DecodeJSON(t, first, &accepted)
	second := s.DoWithHeaders(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies", headers,
		map[string]any{"body_md": "Agent 起草的回复。"}, nil)
	if second.StatusCode != http.StatusAccepted {
		var replayErr errorBody
		tbtest.DecodeJSON(t, second, &replayErr)
		t.Fatalf("幂等重放状态码 = %d/%+v, 想 202", second.StatusCode, replayErr)
	}
	afterAccess := readAccessRecords(t, s, web, number)
	if len(afterAccess.Records) != len(beforeAccess.Records) {
		t.Fatalf("草稿改变访问记录：%d → %d", len(beforeAccess.Records), len(afterAccess.Records))
	}
	var afterNotes notificationsBody
	s.Do(t, web, http.MethodGet, "/api/v1/notifications", nil, &afterNotes)
	if len(afterNotes.Items) != len(beforeNotes.Items)+1 || afterNotes.UnreadCount != beforeNotes.UnreadCount {
		t.Fatalf("草稿通知/未读：%d/%d → %d/%d", len(beforeNotes.Items), beforeNotes.UnreadCount,
			len(afterNotes.Items), afterNotes.UnreadCount)
	}

	var after ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &after)
	if len(after.Timeline) != len(before.Timeline) || after.WaitingOn != before.WaitingOn {
		t.Fatalf("草稿改变了工单：事件 %d→%d，等待方 %+v→%+v",
			len(before.Timeline), len(after.Timeline), before.WaitingOn, after.WaitingOn)
	}
	if after.MyDraft == nil || after.MyDraft.ID != accepted.Draft.ID {
		t.Fatalf("my_draft = %+v, 想 %s", after.MyDraft, accepted.Draft.ID)
	}
	if after.MyDraft.Request.BodyMD != "Agent 起草的回复。" {
		t.Fatalf("my_draft 正文 = %q", after.MyDraft.Request.BodyMD)
	}
	var drafts struct {
		Count  int         `json:"count"`
		Drafts []draftBody `json:"drafts"`
	}
	s.Do(t, agent, http.MethodGet, "/api/v1/drafts", nil, &drafts)
	if drafts.Count != 1 || len(drafts.Drafts) != 1 || drafts.Drafts[0].ID != accepted.Draft.ID {
		t.Fatalf("GET /drafts = %+v", drafts)
	}
}

func Test草稿校验版本乐观锁与三种调用者pending(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "固件升级")
	_, required := issueRequiredAgent(t, s, web, "笔记本")
	directID, directToken := s.IssueAgentToken(t, web, "工位")
	_ = directID
	direct := s.AgentClient(directToken)

	var errBody errorBody
	res := s.Do(t, required, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": " "}, &errBody)
	if res.StatusCode != http.StatusUnprocessableEntity || errBody.Error.Code != "empty_reply" {
		t.Fatalf("保存时校验 = %d/%+v, 想 422 empty_reply", res.StatusCode, errBody)
	}

	accepted := mustReplyDraft(t, s, required, number)
	t.Logf("draft version = %d id = %s", accepted.Draft.Version, accepted.Draft.ID)
	for name, c := range map[string]*http.Client{"required Agent": required, "direct Agent": direct, "本人网页": web} {
		var pending draftPendingError
		res := s.Do(t, c, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
			map[string]any{"body_md": "直接回复。"}, &pending)
		if res.StatusCode != http.StatusConflict || pending.Error.Code != "draft_pending" {
			t.Fatalf("%s 直接回复 = %d/%+v, 想 409 draft_pending", name, res.StatusCode, pending)
		}
		if pending.Error.Draft.ID != accepted.Draft.ID ||
			pending.Error.Draft.PreviewURL != "/t/"+number+"#draft" ||
			pending.Error.Draft.URL != "/api/v1/drafts/"+accepted.Draft.ID {
			t.Fatalf("%s draft_pending 附带草稿 = %+v", name, pending.Error.Draft)
		}
		wantHint := "先在草稿处发出或丢弃"
		if name != "本人网页" {
			wantHint = "要改内容就在这份草稿上改：PUT /api/v1/drafts/" + accepted.Draft.ID +
				"（带 base_version）；要直接发出或丢弃，请成员打开 /t/" + number + "#draft"
		}
		if pending.Error.Hint != wantHint {
			t.Fatalf("%s draft_pending hint = %q, 想 %q", name, pending.Error.Hint, wantHint)
		}
	}
	res = s.Do(t, web, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "新说明", "base_revision": 1}, &errBody)
	if res.StatusCode != http.StatusConflict || errBody.Error.Code != "draft_pending" {
		t.Fatalf("本人修订 = %d/%+v, 想 409 draft_pending", res.StatusCode, errBody)
	}

	res = s.Do(t, required, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"request": map[string]any{"body_md": "缺少版本。"}}, &errBody)
	if res.StatusCode != http.StatusUnprocessableEntity || errBody.Error.Code != "base_version_required" {
		t.Fatalf("缺 base_version = %d/%+v", res.StatusCode, errBody)
	}
	var updated draftBody
	res = s.Do(t, required, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"base_version": accepted.Draft.Version, "request": map[string]any{"body_md": "第二版。"}}, &updated)
	if res.StatusCode != http.StatusOK || updated.Version != 2 || updated.Request.BodyMD != "第二版。" || len(updated.Versions) != 2 {
		t.Fatalf("PUT = %d/%+v", res.StatusCode, updated)
	}
	if len(updated.Versions[1].Author) == 0 || !strings.Contains(updated.Versions[1].Author, "笔记本") {
		t.Fatalf("第二版作者 = %q, 想具名 Agent", updated.Versions[1].Author)
	}
	var stale errorBody
	res = s.Do(t, required, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"base_version": accepted.Draft.Version, "request": map[string]any{"body_md": "第三版。"}}, &stale)
	if res.StatusCode != http.StatusConflict || stale.Error.Code != "draft_version_conflict" {
		t.Fatalf("旧版本 PUT = %d/%+v", res.StatusCode, stale)
	}
}

func Test草稿发出丢弃与Agent禁区(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateAdmin("李工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	li := s.Login("李工", "secret-pass")
	number := mustCreateTicket(t, s, web, "固件升级")
	s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/assign", map[string]any{"assignee": "陈工"}, nil)
	tokenID, agent := issueRequiredAgent(t, s, web, "笔记本")
	accepted := mustReplyDraft(t, s, agent, number)
	var updated draftBody
	s.Do(t, agent, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"base_version": accepted.Draft.Version, "request": map[string]any{"body_md": "第二版。"}}, &updated)
	s.Do(t, li, http.MethodPost, "/api/v1/tickets/"+number+"/replies", map[string]any{"body_md": "工单有新变化。"}, nil)

	var forbidden errorBody
	for _, op := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"发出", http.MethodPost, "/api/v1/drafts/" + accepted.Draft.ID + "/send", map[string]any{"version": accepted.Draft.Version}},
		{"丢弃", http.MethodDelete, "/api/v1/drafts/" + accepted.Draft.ID, nil},
		{"改模式", http.MethodPatch, "/api/v1/me/agent-tokens/" + itoa(tokenID), map[string]any{"draft_mode": "direct"}},
	} {
		res := s.Do(t, agent, op.method, op.path, op.body, &forbidden)
		if res.StatusCode != http.StatusForbidden || forbidden.Error.Code != "web_session_required" {
			t.Fatalf("Agent %s = %d/%+v, 想 403 web_session_required", op.name, res.StatusCode, forbidden)
		}
	}
	if res := s.Do(t, li, http.MethodGet, "/api/v1/drafts/"+accepted.Draft.ID, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("他人读草稿状态码 = %d, 想 404", res.StatusCode)
	}

	res := s.Do(t, web, http.MethodPatch, "/api/v1/me/agent-tokens/"+itoa(tokenID),
		map[string]any{"draft_mode": "direct"}, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("网页改模式状态码 = %d, 想 204", res.StatusCode)
	}
	var conflict struct {
		errorBody
		ThroughSeq int64 `json:"through_seq"`
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": accepted.Draft.Version}, &conflict)
	if res.StatusCode != http.StatusConflict || conflict.Error.Code != "draft_version_conflict" {
		t.Fatalf("旧版本发出 = %d/%+v", res.StatusCode, conflict)
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": updated.Version}, &conflict)
	if res.StatusCode != http.StatusConflict || conflict.Error.Code != "ticket_changed" {
		t.Fatalf("无确认发出 = %d/%+v", res.StatusCode, conflict)
	}

	var sent struct {
		Result    replyBody `json:"result"`
		Signature string    `json:"signature"`
		Drafting  []struct {
			Version int64 `json:"version"`
		} `json:"drafting"`
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": updated.Version, "deliver": true, "confirm_through_seq": 3}, &sent)
	if res.StatusCode != http.StatusCreated || sent.Result.ReplyNo != 2 || !sent.Result.Deliver {
		t.Fatalf("发出 = %d/%+v", res.StatusCode, sent)
	}
	if sent.Signature != "经陈工修改并确认" || len(sent.Drafting) != 2 {
		t.Fatalf("署名/起草过程 = %q/%+v", sent.Signature, sent.Drafting)
	}
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	if len(got.Timeline) != 4 || got.MyDraft != nil {
		t.Fatalf("发出后工单 = %+v", got)
	}
	var sentDraft draftBody
	s.Do(t, agent, http.MethodGet, "/api/v1/drafts/"+accepted.Draft.ID, nil, &sentDraft)
	if sentDraft.Status != "sent" || sentDraft.Request.BodyMD != "第二版。" {
		t.Fatalf("发出后的草稿 = %+v", sentDraft)
	}

	number2 := mustCreateTicket(t, s, web, "第二张")
	s.Do(t, web, http.MethodPatch, "/api/v1/me/agent-tokens/"+itoa(tokenID),
		map[string]any{"draft_mode": "required"}, nil)
	accepted2 := mustReplyDraft(t, s, agent, number2)
	res = s.Do(t, web, http.MethodDelete, "/api/v1/drafts/"+accepted2.Draft.ID, nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("丢弃状态码 = %d, 想 204", res.StatusCode)
	}
	if res := s.Do(t, agent, http.MethodGet, "/api/v1/drafts/"+accepted2.Draft.ID, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("丢弃后读取状态码 = %d, 想 404", res.StatusCode)
	}
	var after ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number2, nil, &after)
	if len(after.Timeline) != 1 || after.MyDraft != nil {
		t.Fatalf("丢弃后工单 = %+v", after)
	}
}

func Test关闭后的回复草稿不能修改或发出(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "关闭草稿")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	accepted := mustReplyDraft(t, s, agent, number)
	s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/close", map[string]any{"reason": "done"}, nil)

	var errBody errorBody
	res := s.Do(t, agent, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"base_version": accepted.Draft.Version, "request": map[string]any{"body_md": "关闭后的版本。"}}, &errBody)
	if res.StatusCode != http.StatusConflict || errBody.Error.Code != "ticket_closed" {
		t.Fatalf("关闭后修改草稿 = %d/%+v, 想 409 ticket_closed", res.StatusCode, errBody)
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": accepted.Draft.Version}, &errBody)
	if res.StatusCode != http.StatusConflict || errBody.Error.Code != "ticket_closed" {
		t.Fatalf("关闭后发出草稿 = %d/%+v, 想 409 ticket_closed", res.StatusCode, errBody)
	}
}

func Test草稿首次写成时只通知主人一次(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateAdmin("李工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	li := s.Login("李工", "secret-pass")
	number := mustCreateTicket(t, s, web, "草稿通知")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")

	var before notificationsBody
	s.Do(t, web, http.MethodGet, "/api/v1/notifications", nil, &before)
	accepted := mustReplyDraft(t, s, agent, number)
	var first draftBody
	s.Do(t, agent, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"base_version": 1, "request": map[string]any{"body_md": "第二版。"}}, &first)
	var second draftBody
	s.Do(t, agent, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"base_version": first.Version, "request": map[string]any{"body_md": "第三版。"}}, &second)

	var got notificationsBody
	s.Do(t, web, http.MethodGet, "/api/v1/notifications", nil, &got)
	if len(got.Items) != len(before.Items)+1 {
		t.Fatalf("通知数 %d → %d，想只新增 1 条", len(before.Items), len(got.Items))
	}
	note := got.Items[0]
	if note.Kind != "draft" || note.Reason != "draft_pending" ||
		note.Text != "你的 Agent · 笔记本 写了回复草稿待确认" {
		t.Fatalf("草稿通知 = %+v", note)
	}
	if note.Ticket.Number != number || note.URL != "/t/"+number+"#draft" {
		t.Fatalf("草稿通知工单/地址 = %+v", note)
	}
	if got.UnreadCount != before.UnreadCount {
		t.Fatalf("未读数 %d → %d，想不变", before.UnreadCount, got.UnreadCount)
	}
	var liNotes notificationsBody
	s.Do(t, li, http.MethodGet, "/api/v1/notifications", nil, &liNotes)
	for _, note := range liNotes.Items {
		if note.Kind == "draft" {
			t.Fatalf("其他参与者也收到草稿通知：%+v", note)
		}
	}
	var agentNotes notificationsBody
	s.Do(t, agent, http.MethodGet, "/api/v1/notifications", nil, &agentNotes)
	if len(agentNotes.Items) != 0 {
		t.Fatalf("Agent 读到草稿通知：%+v", agentNotes.Items)
	}
}

func Test成员读取已发出回复的起草过程(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	outsider := s.Login("李工", "secret-pass")
	number := mustCreateTicket(t, s, web, "起草过程可见性")
	s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/assign", map[string]any{"assignee": "陈工"}, nil)
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	accepted := mustReplyDraft(t, s, agent, number)
	var second draftBody
	s.Do(t, agent, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"base_version": accepted.Draft.Version, "request": map[string]any{"body_md": "Agent 的第二版。"}}, &second)
	var third draftBody
	s.Do(t, web, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"base_version": second.Version, "request": map[string]any{"body_md": "成员确认前的第三版。"}}, &third)
	var sent struct {
		Result replyBody `json:"result"`
	}
	s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": third.Version}, &sent)

	var got struct {
		Versions []struct {
			Version    int64  `json:"version"`
			BodyMD     string `json:"body_md"`
			Author     string `json:"author"`
			At         string `json:"at"`
			AuthorKind string `json:"author_kind"`
		} `json:"versions"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/drafting/1", nil, &got)
	if len(got.Versions) != 3 {
		t.Fatalf("起草过程版本数 = %d, 想 3", len(got.Versions))
	}
	wantBodies := []string{"Agent 起草的回复。", "Agent 的第二版。", "成员确认前的第三版。"}
	wantAuthors := []string{"陈工 的 Agent · 笔记本", "陈工 的 Agent · 笔记本", "陈工"}
	for i, v := range got.Versions {
		if v.Version != int64(i+1) || v.BodyMD != wantBodies[i] || v.Author != wantAuthors[i] || v.At == "" {
			t.Fatalf("第 %d 版 = %+v, 想 %q/%q", i+1, v, wantAuthors[i], wantBodies[i])
		}
	}

	link := s.CreateGuestLink(t, web, number, map[string]any{"note": "检验科-张三"})
	guest := s.GuestClient(link.Key(t))
	if res := s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number+"/drafting/1", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("访客读取起草过程状态码 = %d, 想 404", res.StatusCode)
	}
	if res := s.Do(t, outsider, http.MethodGet, "/api/v1/tickets/"+number+"/drafting/1", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("可见范围外成员读取状态码 = %d, 想 404", res.StatusCode)
	}
}
