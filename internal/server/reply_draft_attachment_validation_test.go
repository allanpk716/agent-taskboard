package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test草稿引用不可用附件时按原规则报错(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	li := s.Login("李工", "secret-pass")
	number := mustCreateTicket(t, s, web, "草稿附件校验")
	s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/assign", map[string]any{"assignee": "李工"}, nil)
	_, agent := issueRequiredAgent(t, s, web, "笔记本")

	others := mustUploadAttachment(t, s, li, number, "别人的附件.txt", []byte("other"))
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "想挂别人的附件。", "attachment_ids": []string{others.ID}}, nil)
	var errBody errorBody
	tbtest.DecodeJSON(t, res, &errBody)
	if res.StatusCode != http.StatusUnprocessableEntity || errBody.Error.Code != "attachment_not_usable" {
		t.Fatalf("引用别人的附件 = %d/%+v, 想 422 attachment_not_usable", res.StatusCode, errBody)
	}

	claimed := mustUploadAttachment(t, s, li, number, "已认领附件.txt", []byte("claimed"))
	res = s.Do(t, li, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"attachment_ids": []string{claimed.ID}}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("先认领附件 = %d, 想 201", res.StatusCode)
	}
	res = s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "想重复认领。", "attachment_ids": []string{claimed.ID}}, nil)
	tbtest.DecodeJSON(t, res, &errBody)
	if res.StatusCode != http.StatusUnprocessableEntity || errBody.Error.Code != "attachment_not_usable" {
		t.Fatalf("引用已认领附件 = %d/%+v, 想 422 attachment_not_usable", res.StatusCode, errBody)
	}
}
