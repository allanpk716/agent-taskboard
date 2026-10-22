package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func TestRequiredAgent说明修订保存为草稿并发出(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "固件升级")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	file := mustUploadAttachment(t, s, agent, number, "现场记录.txt", []byte("record"))

	var accepted draftAcceptedBody
	res := s.Do(t, agent, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "Agent 修订后的说明。", "attachment_ids": []string{file.ID}, "base_revision": 1}, &accepted)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("Agent 修订说明状态码 = %d, 想 202", res.StatusCode)
	}
	if accepted.Draft.Kind != "brief_revision" || accepted.Draft.Request.BriefMD == nil ||
		*accepted.Draft.Request.BriefMD != "Agent 修订后的说明。" || accepted.Draft.Base.BriefRevision != 1 {
		t.Fatalf("说明修订草稿 = %+v", accepted.Draft)
	}
	var after ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &after)
	if after.Brief.Revision != 1 || after.Brief.Markdown == "Agent 修订后的说明。" || len(after.Timeline) != 1 {
		t.Fatalf("草稿改变说明或时间线：%+v", after)
	}
	if after.MyDraft == nil || after.MyDraft.ID != accepted.Draft.ID {
		t.Fatalf("my_draft = %+v, 想 %s", after.MyDraft, accepted.Draft.ID)
	}
	var second draftBody
	s.Do(t, web, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"base_version": accepted.Draft.Version,
			"request": map[string]any{"brief_md": "成员改过的说明。", "attachment_ids": []string{file.ID}, "base_revision": 1}}, &second)

	var sent struct {
		Result struct {
			Revision int64 `json:"revision"`
		} `json:"result"`
		Signature string `json:"signature"`
		Drafting  []struct {
			Version int64 `json:"version"`
		} `json:"drafting"`
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": second.Version}, &sent)
	if res.StatusCode != http.StatusCreated || sent.Result.Revision != 2 || sent.Signature != "经陈工修改并确认" {
		t.Fatalf("发出说明修订 = %d/%+v, 想 201/2/确认署名", res.StatusCode, sent)
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &after)
	if after.Brief.Revision != 2 || after.Brief.Markdown != "成员改过的说明。" || len(after.Timeline) != 2 {
		t.Fatalf("发出后的说明/时间线 = %+v", after)
	}
	if after.Timeline[1].Type != "brief_revised" || after.Timeline[1].Revision != 2 {
		t.Fatalf("修订事件 = %+v", after.Timeline[1])
	}
	var attachments attachmentsTicketBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &attachments)
	if len(attachments.Attachments) != 1 || attachments.Attachments[0].AttachedTo != "说明" {
		t.Fatalf("修订草稿附件应认领到说明：%+v", attachments.Attachments)
	}
	var history struct {
		Versions []struct {
			Revision int64      `json:"revision"`
			Title    string     `json:"title"`
			Markdown string     `json:"markdown"`
			Author   authorBody `json:"author"`
		} `json:"versions"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/brief-drafting/2", nil, &history)
	if len(history.Versions) != 2 || history.Versions[0].Revision != 1 ||
		history.Versions[0].Markdown != "Agent 修订后的说明。" || history.Versions[0].Author.Agent == nil ||
		history.Versions[1].Revision != 2 || history.Versions[1].Markdown != "成员改过的说明。" {
		t.Fatalf("说明修订起草过程 = %+v", history)
	}
}

func Test说明修订草稿校验与共用草稿位(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	outsider := s.Login("李工", "secret-pass")
	number := mustCreateTicket(t, s, web, "固件升级")
	s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/assign", map[string]any{"assignee": "李工"}, nil)
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	_, otherAgent := issueRequiredAgent(t, s, outsider, "外来的")

	var errBody errorBody
	res := s.Do(t, agent, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "缺少版本。"}, &errBody)
	if res.StatusCode != http.StatusUnprocessableEntity || errBody.Error.Code != "base_revision_required" {
		t.Fatalf("缺 base_revision = %d/%+v, 想 422", res.StatusCode, errBody)
	}
	res = s.Do(t, agent, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "过期版本。", "base_revision": 2}, &errBody)
	if res.StatusCode != http.StatusConflict || errBody.Error.Code != "revision_conflict" {
		t.Fatalf("过期 base_revision = %d/%+v, 想 409 revision_conflict", res.StatusCode, errBody)
	}
	res = s.Do(t, otherAgent, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "非创建者。", "base_revision": 1}, &errBody)
	if res.StatusCode != http.StatusForbidden || errBody.Error.Code != "not_brief_owner" {
		t.Fatalf("非创建者 = %d/%+v, 想 403 not_brief_owner", res.StatusCode, errBody)
	}

	var accepted draftAcceptedBody
	s.Do(t, agent, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "说明修订草稿。", "base_revision": 1}, &accepted)
	var pending draftPendingError
	res = s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "直接回复。"}, &pending)
	if res.StatusCode != http.StatusConflict || pending.Error.Code != "draft_pending" || pending.Error.Draft.ID != accepted.Draft.ID {
		t.Fatalf("有说明修订草稿时回复 = %d/%+v, 想 draft_pending", res.StatusCode, pending)
	}

	var stale errorBody
	res = s.Do(t, agent, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{"base_version": accepted.Draft.Version,
			"request": map[string]any{"brief_md": "第二版。", "base_revision": 2}}, &stale)
	if res.StatusCode != http.StatusConflict || stale.Error.Code != "revision_conflict" {
		t.Fatalf("PUT 过期 base_revision = %d/%+v, 想 409 revision_conflict", res.StatusCode, stale)
	}
}

func Test访客补全说明后说明修订草稿发出冲突(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "待补全")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	var accepted draftAcceptedBody
	s.Do(t, agent, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "Agent 修订。", "base_revision": 1}, &accepted)

	link := s.CreateGuestLink(t, web, number, map[string]any{"note": "检验科-张三", "can_edit_brief": true})
	guest := s.GuestClient(link.Key(t))
	res := s.Do(t, guest, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "访客补全后的说明。", "base_revision": 1}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客补全说明状态码 = %d, 想 200", res.StatusCode)
	}
	var errBody errorBody
	res = s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": accepted.Draft.Version}, &errBody)
	if res.StatusCode != http.StatusConflict || errBody.Error.Code != "revision_conflict" {
		t.Fatalf("发出过期说明修订 = %d/%+v, 想 409 revision_conflict", res.StatusCode, errBody)
	}
}
