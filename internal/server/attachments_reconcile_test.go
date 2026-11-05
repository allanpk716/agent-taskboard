package server_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 附件对账只删库里没有且已存在超过 24 小时的对象。待用附件虽然未被正文
// 引用，但仍有库记录，不能被这一步误删；删除必须经 Storage.Delete 走后端。
func Test附件对账清理超过二十四小时的孤儿对象_本地(t *testing.T) {
	s := tbtest.Start(t)
	s.Clock().Set(time.Now().In(clock.Beijing))
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, admin, "附件对账")

	ctx := context.Background()
	claimed := mustUploadAttachment(t, s, admin, number, "已引用.txt", []byte("keep"))
	pending := mustUploadAttachment(t, s, admin, number, "待用.txt", []byte("pending"))
	stale := mustUploadAttachment(t, s, admin, number, "上传失败残留.txt", []byte("stale-orphan"))
	fresh := mustUploadAttachment(t, s, admin, number, "新孤儿.txt", []byte("fresh-orphan"))
	staleID := attachmentNumericID(t, stale.ID)
	freshID := attachmentNumericID(t, fresh.ID)
	for _, id := range []int64{staleID, freshID} {
		if _, err := s.Store().DB.Exec(`DELETE FROM attachments WHERE id = ?`, id); err != nil {
			t.Fatalf("构造孤儿附件记录失败：%v", err)
		}
	}
	stalePath := filepath.Join(s.DataDir(), "attachments", strconv.FormatInt(staleID, 10))
	staleTime := s.Clock().Now().Add(-25 * time.Hour)
	if err := os.Chtimes(stalePath, staleTime, staleTime); err != nil {
		t.Fatalf("调整孤儿对象时间失败：%v", err)
	}
	if res := claimByReply(t, s, admin, number, map[string]any{"attachment_ids": []string{claimed.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d，想 201", res.StatusCode)
	}

	result, err := s.Attachments().ReconcileStorage(ctx)
	if err != nil {
		t.Fatalf("附件对账失败：%v", err)
	}
	if result.DeletedObjects != 1 {
		t.Fatalf("删除孤儿对象数 = %d，想 1", result.DeletedObjects)
	}
	if result.DeletedBytes != int64(len("stale-orphan")) {
		t.Fatalf("删除孤儿占用 = %d 字节，想 %d", result.DeletedBytes, len("stale-orphan"))
	}

	storage := s.AttachmentStorage()
	for name, id := range map[string]int{
		"已引用": int(attachmentNumericID(t, claimed.ID)),
		"待用":  int(attachmentNumericID(t, pending.ID)),
		"新孤儿": int(freshID),
	} {
		if _, ok := storage.Stat(ctx, int64(id)); !ok {
			t.Errorf("%s附件对象应保留", name)
		}
	}
	if _, ok := storage.Stat(ctx, staleID); ok {
		t.Error("超过 24 小时的孤儿对象应删除")
	}
}

func attachmentNumericID(t testing.TB, ref string) int64 {
	t.Helper()
	id, err := attachments.ParseRef(ref)
	if err != nil {
		t.Fatalf("解析附件引用 %q 失败：%v", ref, err)
	}
	return id
}
