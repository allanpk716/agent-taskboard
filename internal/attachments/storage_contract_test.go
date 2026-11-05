package attachments_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
)

// runStorageContract 是存储接口的契约用例：同一套用例跑本地实现和内存假实现，
// 保证两者语义一致（#315）。用例只测接口行为，不碰任何文件路径。
func runStorageContract(t *testing.T, newStorage func(t *testing.T) attachments.Storage) {
	t.Helper()
	ctx := context.Background()

	t.Run("Put 后 Open 与 Stat 能拿到内容与大小", func(t *testing.T) {
		store := newStorage(t)
		content := []byte("附件内容")
		if err := store.Put(ctx, 7, bytes.NewReader(content), int64(len(content)),
			attachments.PutMeta{ContentType: "text/plain"}); err != nil {
			t.Fatalf("Put 失败：%v", err)
		}
		body, size, err := store.Open(ctx, 7)
		if err != nil {
			t.Fatalf("Open 失败：%v", err)
		}
		defer body.Close()
		got, err := io.ReadAll(body)
		if err != nil {
			t.Fatalf("读取内容失败：%v", err)
		}
		if !bytes.Equal(got, content) {
			t.Fatalf("读出内容 = %q, 想 %q", got, content)
		}
		if size != int64(len(content)) {
			t.Fatalf("Open 返回大小 = %d, 想 %d", size, len(content))
		}
		if statSize, ok, err := store.Stat(ctx, 7); err != nil || !ok || statSize != int64(len(content)) {
			t.Fatalf("Stat = (%d, %v, %v), 想 (%d, true, nil)", statSize, ok, err, len(content))
		}
	})

	t.Run("Open 不存在的对象返回 ErrObjectNotFound", func(t *testing.T) {
		store := newStorage(t)
		if _, _, err := store.Open(ctx, 404); !errors.Is(err, attachments.ErrObjectNotFound) {
			t.Fatalf("Open 不存在的对象 err = %v, 想 ErrObjectNotFound", err)
		}
	})

	t.Run("Stat 不存在的对象返回不存在", func(t *testing.T) {
		store := newStorage(t)
		if size, ok, err := store.Stat(ctx, 404); err != nil || ok || size != 0 {
			t.Fatalf("Stat 不存在的对象 = (%d, %v, %v), 想 (0, false, nil)", size, ok, err)
		}
	})

	t.Run("Put 覆盖已有对象", func(t *testing.T) {
		store := newStorage(t)
		put := func(content string) {
			t.Helper()
			if err := store.Put(ctx, 3, bytes.NewReader([]byte(content)), int64(len(content)),
				attachments.PutMeta{ContentType: "text/plain"}); err != nil {
				t.Fatalf("Put 失败：%v", err)
			}
		}
		put("旧内容")
		put("新的更长内容")
		body, size, err := store.Open(ctx, 3)
		if err != nil {
			t.Fatalf("Open 失败：%v", err)
		}
		defer body.Close()
		got, err := io.ReadAll(body)
		if err != nil {
			t.Fatalf("读取内容失败：%v", err)
		}
		if string(got) != "新的更长内容" || size != int64(len("新的更长内容")) {
			t.Fatalf("覆盖后 = (%q, %d), 想读回新内容", got, size)
		}
	})

	t.Run("Delete 删除对象且幂等", func(t *testing.T) {
		store := newStorage(t)
		content := []byte("待删")
		if err := store.Put(ctx, 9, bytes.NewReader(content), int64(len(content)), attachments.PutMeta{}); err != nil {
			t.Fatalf("Put 失败：%v", err)
		}
		if err := store.Delete(ctx, 9); err != nil {
			t.Fatalf("Delete 失败：%v", err)
		}
		if _, _, err := store.Open(ctx, 9); !errors.Is(err, attachments.ErrObjectNotFound) {
			t.Fatalf("删除后 Open err = %v, 想 ErrObjectNotFound", err)
		}
		if _, ok, err := store.Stat(ctx, 9); err != nil || ok {
			t.Fatal("删除后 Stat 应返回不存在")
		}
		// 删除不存在的对象不是错误：四处删除调用方都依赖这个幂等语义。
		if err := store.Delete(ctx, 9); err != nil {
			t.Fatalf("重复 Delete err = %v, 想 nil", err)
		}
	})

	t.Run("List 按 id 升序列出对象", func(t *testing.T) {
		store := newStorage(t)
		for _, id := range []int64{12, 3, 30} {
			content := []byte("内容")
			if err := store.Put(ctx, id, bytes.NewReader(content), int64(len(content)), attachments.PutMeta{}); err != nil {
				t.Fatalf("Put %d 失败：%v", id, err)
			}
		}
		var ids []int64
		err := store.List(ctx, func(id int64, size int64, modTime time.Time) error {
			if size != int64(len("内容")) {
				t.Errorf("List %d 的大小 = %d, 想 %d", id, size, len("内容"))
			}
			if modTime.IsZero() {
				t.Errorf("List %d 应带修改时间", id)
			}
			ids = append(ids, id)
			return nil
		})
		if err != nil {
			t.Fatalf("List 失败：%v", err)
		}
		if len(ids) != 3 || ids[0] != 3 || ids[1] != 12 || ids[2] != 30 {
			t.Fatalf("List 顺序 = %v, 想 [3 12 30]", ids)
		}

		stop := errors.New("停止遍历")
		if err := store.List(ctx, func(int64, int64, time.Time) error { return stop }); !errors.Is(err, stop) {
			t.Fatalf("回调出错时 List err = %v, 想 %v", err, stop)
		}
	})

	t.Run("Put 的大小与实际不符时不留对象", func(t *testing.T) {
		store := newStorage(t)
		if err := store.Put(ctx, 21, bytes.NewReader([]byte("短内容")), 100, attachments.PutMeta{}); err == nil {
			t.Fatal("声明 100 字节只给 6 字节应返回错误")
		}
		if _, ok, err := store.Stat(ctx, 21); err != nil || ok {
			t.Fatal("失败的 Put 不应留下对象")
		}
	})

	t.Run("DownloadURL 本地后端返回不可用", func(t *testing.T) {
		store := newStorage(t)
		url, ok, err := store.DownloadURL(ctx, 7, attachments.DownloadURLOpts{Disposition: "attachment"})
		if err != nil {
			t.Fatalf("DownloadURL err = %v, 想 nil", err)
		}
		if ok || url != "" {
			t.Fatalf("DownloadURL = (%q, %v), 想 (\"\", false)", url, ok)
		}
	})
}

func TestLocalStorage满足存储契约(t *testing.T) {
	runStorageContract(t, func(t *testing.T) attachments.Storage {
		t.Helper()
		store, err := attachments.NewLocalStorage(t.TempDir())
		if err != nil {
			t.Fatalf("建本地存储失败：%v", err)
		}
		return store
	})
}

func TestMemoryStorage满足存储契约(t *testing.T) {
	runStorageContract(t, func(t *testing.T) attachments.Storage {
		return attachments.NewMemoryStorage()
	})
}

// 内存假实现要保留对象元数据，黑盒测试靠它断言上传写进了什么。
func TestMemoryStorage保留对象元数据(t *testing.T) {
	store := attachments.NewMemoryStorage()
	content := []byte("内存里的附件")
	meta := attachments.PutMeta{ContentType: "text/plain; charset=utf-8"}
	if err := store.Put(context.Background(), 5, bytes.NewReader(content), int64(len(content)), meta); err != nil {
		t.Fatalf("Put 失败：%v", err)
	}
	obj, ok := store.Object(5)
	if !ok {
		t.Fatal("Put 后应能从测试侧查看对象")
	}
	if obj.Size != int64(len(content)) || !bytes.Equal(obj.Content, content) {
		t.Fatalf("对象 = (%d, %q), 想大小 %d 且内容一致", obj.Size, obj.Content, len(content))
	}
	if obj.ContentType != meta.ContentType {
		t.Fatalf("Content-Type = %q, 想 %q", obj.ContentType, meta.ContentType)
	}
	if ids := store.IDs(); len(ids) != 1 || ids[0] != 5 {
		t.Fatalf("IDs = %v, 想 [5]", ids)
	}
}
