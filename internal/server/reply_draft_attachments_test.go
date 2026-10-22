package server_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test草稿占用附件并在新版替换后释放清理(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "草稿附件")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")

	replaced := mustUploadAttachment(t, s, agent, number, "第一版.txt", []byte("first"))
	kept := mustUploadAttachment(t, s, agent, number, "第二版.txt", []byte("second"))
	var accepted draftAcceptedBody
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "第一版。", "attachment_ids": []string{replaced.ID}}, &accepted)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("保存带附件草稿 = %d, 想 202", res.StatusCode)
	}
	var updated draftBody
	res = s.Do(t, agent, http.MethodPut, "/api/v1/drafts/"+accepted.Draft.ID,
		map[string]any{
			"base_version": accepted.Draft.Version,
			"request":      map[string]any{"body_md": "第二版。", "attachment_ids": []string{kept.ID}},
		}, &updated)
	if res.StatusCode != http.StatusOK || updated.Version != 2 {
		t.Fatalf("替换草稿 = %d/%+v, 想 200 第 2 版", res.StatusCode, updated)
	}

	s.Clock().Advance(25 * time.Hour)
	removed, err := s.Attachments().CleanupUnreferenced(context.Background())
	if err != nil {
		t.Fatalf("清理附件失败：%v", err)
	}
	if removed != 1 {
		t.Fatalf("清理了 %d 个附件, 想 1（只有新版不再引用的第一版）", removed)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir(), "attachments", "1")); !os.IsNotExist(err) {
		t.Errorf("第一版附件文件应被清理（err=%v）", err)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir(), "attachments", "2")); err != nil {
		t.Errorf("第二版附件文件应保留：%v", err)
	}
}
