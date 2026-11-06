package ops_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/guestlinkkey"
	"github.com/allanpk716/agent-taskboard/internal/ops"
)

// OSS 上传失败只降级为“本地成功、异地补传”：本机副本照常记成功，下次
// 备份时把缺失的库副本和访客链接密钥补传上去。
func TestOSS上传失败时本地成功下次补传(t *testing.T) {
	ctx := context.Background()
	mem := attachments.NewMemoryStorage()
	target := t.TempDir()
	b, clk, dataDir := newBackups(t, target)
	b.SetObjectStorage(mem)
	if err := os.WriteFile(filepath.Join(dataDir, guestlinkkey.FileName), []byte("guest-key"), 0o600); err != nil {
		t.Fatal(err)
	}

	mem.PutObjectErr = errors.New("oss temporarily down")
	first, err := b.BackupNow(ctx, ops.KindDaily)
	if err != nil {
		t.Fatalf("上传失败不应让整次备份失败：%v", err)
	}
	if !first.Success() {
		t.Fatalf("第一次备份记录 = %+v，想本地成功", first)
	}
	if _, exists := mem.ObjectByKey(attachments.BackupRelKey(first.Filename)); exists {
		t.Fatal("注入上传故障时库副本不应已上传")
	}

	mem.PutObjectErr = nil
	clk.Advance(time.Hour)
	if _, err := b.BackupNow(ctx, ops.KindDaily); err != nil {
		t.Fatalf("第二次备份失败：%v", err)
	}
	if _, exists := mem.ObjectByKey(attachments.BackupRelKey(first.Filename)); !exists {
		t.Fatal("第二次备份应补传第一次的库副本")
	}
	if _, exists := mem.ObjectByKey(attachments.BackupRelKey(guestlinkkey.FileName)); !exists {
		t.Fatal("补传时应连同访客链接密钥一起上传")
	}
}

// 目标不可达时降级到本机的副本也会上传到 OSS；本地轮换删除后，OSS 上的
// 同名副本要跟着补删，不会永远残留。
func Test降级上传的远端副本随轮换删除(t *testing.T) {
	ctx := context.Background()
	mem := attachments.NewMemoryStorage()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, clk := newBackupsWithKeep(t, t.TempDir(), filepath.Join(blocker, "backups"), 1, 12, 5)
	b.SetObjectStorage(mem)

	first, err := b.BackupNow(ctx, ops.KindDaily)
	if err != nil {
		t.Fatalf("第一次降级备份失败：%v", err)
	}
	if first.Location != ops.LocationLocal {
		t.Fatalf("第一次备份位置 = %q，想降级到本机", first.Location)
	}
	if _, exists := mem.ObjectByKey(attachments.BackupRelKey(first.Filename)); !exists {
		t.Fatal("降级副本应上传到 OSS")
	}

	clk.Advance(24 * time.Hour)
	second, err := b.BackupNow(ctx, ops.KindDaily)
	if err != nil {
		t.Fatalf("第二次降级备份失败：%v", err)
	}
	if _, exists := mem.ObjectByKey(attachments.BackupRelKey(first.Filename)); exists {
		t.Fatal("本地轮换删除后，OSS 上的降级副本应补删")
	}
	if _, exists := mem.ObjectByKey(attachments.BackupRelKey(second.Filename)); !exists {
		t.Fatal("最新的降级副本应留在 OSS")
	}
}

// 补传只针对本地仍在的副本：已经被轮换删掉的旧备份不会因为远端对象缺失
// 而重新上传。
func Test补传不复活已轮换删除的旧备份(t *testing.T) {
	ctx := context.Background()
	mem := attachments.NewMemoryStorage()
	target := t.TempDir()
	b, clk := newBackupsWithKeep(t, t.TempDir(), target, 1, 12, 5)
	b.SetObjectStorage(mem)

	first, err := b.BackupNow(ctx, ops.KindDaily)
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(24 * time.Hour)
	if _, err := b.BackupNow(ctx, ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, first.Filename)); !os.IsNotExist(err) {
		t.Fatalf("旧副本应已被本地轮换删除：err=%v", err)
	}
	if _, exists := mem.ObjectByKey(attachments.BackupRelKey(first.Filename)); exists {
		t.Fatal("已轮换删除的旧备份不应被补传复活")
	}
}
