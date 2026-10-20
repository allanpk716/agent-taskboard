package updater

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

func Test更新前备份目标失败时返回本机标记(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	backup, err := defaultPreupdate(context.Background(), &Options{DataDir: dataDir,
		Backup: config.Backup{Target: filepath.Join(blocker, "backups"), DailyAt: "03:00", KeepPreupdate: 1}})
	if err != nil {
		t.Fatalf("兜底成功时更新应继续：%v", err)
	}
	if !backup.Local || backup.Filename == "" {
		t.Fatalf("更新前备份结果应标明只在本机：%+v", backup)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "backups", backup.Filename)); err != nil {
		t.Fatalf("本机兜底副本不存在：%v", err)
	}
	target := filepath.Join(blocker, "backups")
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "taskboard-20200101-000000-preupdate.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := latestPreupdateBackup(Options{DataDir: dataDir, Backup: config.Backup{Target: target}})
	if err != nil || got.Path != filepath.Join(dataDir, "backups", backup.Filename) {
		t.Fatalf("回滚应优先更新的本机兜底：%+v, err=%v", got, err)
	}
}
