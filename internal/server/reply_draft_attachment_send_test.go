package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test草稿发出时附件挂到回复(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "草稿附件发出")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")
	uploaded := mustUploadAttachment(t, s, agent, number, "结果.txt", []byte("result"))

	var accepted draftAcceptedBody
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "结果见附件。", "attachment_ids": []string{uploaded.ID}}, &accepted)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("保存带附件草稿 = %d, 想 202", res.StatusCode)
	}
	var before ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &before)
	if before.MyDraft == nil || len(before.MyDraft.Attachments) != 1 ||
		before.MyDraft.Attachments[0].Filename != "结果.txt" {
		t.Fatalf("草稿卡片附件 = %+v, 想 1 个结果.txt", before.MyDraft)
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/drafts/"+accepted.Draft.ID+"/send",
		map[string]any{"version": accepted.Draft.Version}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("发出带附件草稿 = %d, 想 201", res.StatusCode)
	}

	var got attachmentsTicketBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	if len(got.Attachments) != 1 || got.Attachments[0].AttachedTo != "回复 1" {
		t.Fatalf("发出后附件列表 = %+v, 想 1 个挂在 回复 1", got.Attachments)
	}
}
