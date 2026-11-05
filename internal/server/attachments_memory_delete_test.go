package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 清除任务删除对象走存储接口：待用的从内存后端消失，已引用的保留。
func Test内存假存储下清除待用附件删除对象(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	s := tbtest.Start(t, tbtest.WithAttachmentStorage(mem))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "假存储清理工单")

	keep := mustUploadAttachment(t, s, web, number, "保留.png", []byte("keep"))
	mustUploadAttachment(t, s, web, number, "清除.zip", []byte("drop"))
	if res := claimByReply(t, s, web, number,
		map[string]any{"attachment_ids": []string{keep.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d, 想 201", res.StatusCode)
	}

	s.Clock().Advance(25 * time.Hour)
	if n, err := s.Attachments().CleanupUnreferenced(context.Background()); err != nil || n != 1 {
		t.Fatalf("清除任务删了 %d 个（err=%v），想 1", n, err)
	}
	if _, ok := mem.Object(1); !ok {
		t.Fatal("已引用的附件对象应保留在存储里")
	}
	if _, ok := mem.Object(2); ok {
		t.Fatal("待用的附件对象应从存储里删除")
	}
}

// 丢弃草稿删除它占用的附件对象也走存储接口。
func Test内存假存储下丢弃草稿删除附件对象(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	s := tbtest.Start(t, tbtest.WithAttachmentStorage(mem))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "假存储丢弃草稿")
	_, agent := issueRequiredAgent(t, s, web, "笔记本")

	uploaded := mustUploadAttachment(t, s, agent, number, "草稿附件.txt", []byte("draft"))
	var accepted draftAcceptedBody
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "见附件。", "attachment_ids": []string{uploaded.ID}}, &accepted)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("保存带附件草稿 = %d, 想 202", res.StatusCode)
	}
	if res := s.Do(t, web, http.MethodDelete, "/api/v1/drafts/"+accepted.Draft.ID, nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("丢弃草稿 = %d, 想 204", res.StatusCode)
	}
	if ids := mem.IDs(); len(ids) != 0 {
		t.Fatalf("丢弃草稿后对象应删除：%v", ids)
	}
}
