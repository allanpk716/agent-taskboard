package ops_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

// 镜像同步先看镜像：已镜像的附件不再查询后端，缺的才 Open。
func Test镜像同步先查镜像不再查询后端(t *testing.T) {
	ctx := context.Background()
	mem := attachments.NewMemoryStorage()
	target := t.TempDir()
	b, clk, dataDir := newBackups(t, target)
	b.SetAttachmentStorage(mem)
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	defer st.Close()
	if _, err := st.DB.Exec(`INSERT INTO tickets (seq, number, kind, title, created_at)
		VALUES (1, '20261016-001', 'task', '镜像查询工单', ?)`,
		clk.Now().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2} {
		if _, err := st.DB.Exec(`INSERT INTO attachments
			(ticket_seq, filename, size, state, attached_to, uploaded_at)
			VALUES (1, ?, 6, 'attached', '说明', ?)`,
			"附件"+strconv.FormatInt(id, 10)+".txt", clk.Now().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		if err := mem.Put(ctx, id, strings.NewReader("附件内容"), int64(len("附件内容")),
			attachments.PutMeta{ContentType: "text/plain"}); err != nil {
			t.Fatal(err)
		}
	}

	before := mem.StatCalls
	first, err := b.BackupNow(ctx, ops.KindDaily)
	if err != nil {
		t.Fatalf("第一次备份失败：%v", err)
	}
	if first.FilesAdded != 2 || mem.StatCalls != before {
		t.Fatalf("第一次备份：新增 %d、后端 Stat %d 次，想镜像 2 个且不先 Stat 后端",
			first.FilesAdded, mem.StatCalls-before)
	}
	clk.Advance(time.Hour)
	second, err := b.BackupNow(ctx, ops.KindDaily)
	if err != nil {
		t.Fatalf("第二次备份失败：%v", err)
	}
	if second.FilesAdded != 0 || mem.StatCalls != before {
		t.Fatalf("第二次备份：新增 %d、后端 Stat %d 次，想全部跳过且不再查询后端",
			second.FilesAdded, mem.StatCalls-before)
	}
}
