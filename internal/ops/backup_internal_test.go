package ops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

func Test完整性校验失败时不做本机兜底(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	clk := clock.NewFake(time.Date(2026, 10, 16, 3, 0, 5, 0, clock.Beijing))
	b, err := NewBackups(st.DB, dataDir, BackupOptions{
		Target: t.TempDir(), DailyAt: "03:00", KeepDaily: 1,
	}, clk)
	if err != nil {
		t.Fatal(err)
	}
	b.verify = func(string) (bool, error) { return false, nil }

	rec, err := b.BackupNow(context.Background(), KindDaily)
	if err == nil || rec.IntegrityOK || rec.Location != LocationTarget {
		t.Fatalf("完整性校验失败应普通失败且不兜底：%+v, err=%v", rec, err)
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".db" {
			t.Fatalf("校验失败不应留下兜底副本：%s", e.Name())
		}
	}
}
