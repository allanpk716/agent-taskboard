package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test丢弃草稿立即删除它占用的附件(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "丢弃草稿附件")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")

	uploaded := mustUploadAttachment(t, s, agent, number, "会被丢弃.txt", []byte("discard"))
	var accepted draftAcceptedBody
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "见附件。", "attachment_ids": []string{uploaded.ID}}, &accepted)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("保存带附件草稿 = %d, 想 202", res.StatusCode)
	}
	id := strings.TrimPrefix(uploaded.ID, "att_")
	file := filepath.Join(s.DataDir(), "attachments", id)
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("草稿附件文件应存在：%v", err)
	}

	res = s.Do(t, web, http.MethodDelete, "/api/v1/drafts/"+accepted.Draft.ID, nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("丢弃草稿 = %d, 想 204", res.StatusCode)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("丢弃后附件文件应删除（err=%v）", err)
	}
	if res := downloadAttachment(t, s, web, "/api/v1/attachments/"+uploaded.ID+"/"+uploaded.Filename); res.StatusCode != http.StatusNotFound {
		t.Fatalf("丢弃后下载附件 = %d, 想 404", res.StatusCode)
	}
}
