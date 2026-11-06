package ops_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/guestlinkkey"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

func TestOSS备份地址有效期一小时(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	mem.DownloadURLPrefix = "https://files.example.test/signed"
	name := "taskboard-20261105-120000-daily.db"

	target, err := ops.BackupObjectURL(context.Background(), mem, name)
	if err != nil {
		t.Fatalf("生成备份地址失败：%v", err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/signed/backups/"+name || parsed.Query().Get("ttl") != "3600" {
		t.Fatalf("备份地址 = %s，想指向 backups/ 下的一小时签名地址", target)
	}
}

func Test从OSS下载latest备份并恢复可用数据库(t *testing.T) {
	ctx := context.Background()
	mem := attachments.NewMemoryStorage()
	older := "taskboard-20261015-120000-daily.db"
	if err := mem.PutObject(ctx, attachments.BackupRelKey(older),
		strings.NewReader("older backup"), int64(len("older backup")),
		attachments.PutMeta{ContentType: "application/octet-stream"}); err != nil {
		t.Fatalf("造旧备份对象失败：%v", err)
	}
	if err := mem.PutObject(ctx, attachments.BackupRelKey(guestlinkkey.FileName),
		strings.NewReader("old guest key"), int64(len("old guest key")),
		attachments.PutMeta{ContentType: "application/octet-stream"}); err != nil {
		t.Fatalf("造密钥对象失败：%v", err)
	}
	target := t.TempDir()
	b, _, dataDir := newBackups(t, target)
	b.SetObjectStorage(mem)
	if err := os.WriteFile(filepath.Join(dataDir, guestlinkkey.FileName), []byte("guest-link-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	record, err := b.BackupNow(ctx, ops.KindDaily)
	if err != nil {
		t.Fatalf("生成并上传备份失败：%v", err)
	}
	st.Close()
	latest, err := ops.LatestBackupFromStorage(ctx, mem)
	if err != nil {
		t.Fatalf("解析 latest 失败：%v", err)
	}
	if latest != record.Filename {
		t.Fatalf("latest = %q，想 %q", latest, record.Filename)
	}

	localBackupDir := filepath.Join(t.TempDir(), "backups")
	backupFile, err := ops.DownloadBackupFromOSS(ctx, mem, localBackupDir, latest)
	if err != nil {
		t.Fatalf("从 OSS 下载备份失败：%v", err)
	}
	if filepath.Base(backupFile) != record.Filename {
		t.Fatalf("下载的备份 = %q，想 %q", backupFile, record.Filename)
	}
	key, err := os.ReadFile(filepath.Join(localBackupDir, guestlinkkey.FileName))
	if err != nil || string(key) != "guest-link-key" {
		t.Fatalf("下载的密钥 = (%q, %v)，想随备份下载", key, err)
	}

	freshData := t.TempDir()
	want := attachments.BackendMarker{
		Backend: attachments.AttachmentBackendOSS, Bucket: "files", Prefix: "agent-taskboard/test/",
	}
	result, err := ops.RestoreWithStorageAndMarker(freshData, 0, backupFile,
		beijing(2026, 11, 5, 12, 1, 0), attachments.NewMemoryStorage(), want)
	if err != nil {
		t.Fatalf("恢复 OSS 备份失败：%v", err)
	}
	if !result.GuestLinkKeyRestored {
		t.Fatalf("恢复结果 = %+v，想还原访客链接密钥", result)
	}
	restored, err := store.Open(freshData)
	if err != nil {
		t.Fatalf("恢复后的库不可用：%v", err)
	}
	defer restored.Close()
}
