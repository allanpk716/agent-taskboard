package outlet_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/outlet"
)

func Test出口可读取末四位并暂停恢复删除(t *testing.T) {
	s := openOutletTestStore(t)
	insertOutletMember(t, s.DB, 1, "wang")
	cipher, err := outlet.NewCipher([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 11, 20, 9, 0, 0, 0, clock.Beijing))
	manager := outlet.NewManager(s.DB, clk, cipher)

	saved, err := manager.Save(context.Background(), 1, "bot01", "secret-1234")
	if err != nil {
		t.Fatal(err)
	}
	got, err := manager.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.BotID != "bot01" || got.KeyLast4 != "1234" || got.Status != "active" ||
		got.LastPushedID != saved.LastPushedID || got.Generation != saved.Generation {
		t.Fatalf("读取出口 = %+v，想只带末四位的非敏感配置", got)
	}

	if _, err := manager.Pause(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	insertSystemNotification(t, s.DB, 1, "暂停期间的通知")
	paused, err := manager.Get(context.Background(), 1)
	if err != nil || paused.Status != "paused" || paused.LastPushedID != saved.LastPushedID {
		t.Fatalf("暂停后的出口 = %+v err=%v，想游标不动", paused, err)
	}
	if _, err := manager.Resume(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	resumed, err := manager.Get(context.Background(), 1)
	if err != nil || resumed.Status != "active" || resumed.LastPushedID != saved.LastPushedID {
		t.Fatalf("恢复后的出口 = %+v err=%v，想游标仍不动", resumed, err)
	}

	if err := manager.Delete(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Get(context.Background(), 1); !errors.Is(err, outlet.ErrOutletNotFound) {
		t.Fatalf("删除后读取 err = %v，想 %v", err, outlet.ErrOutletNotFound)
	}
	if err := manager.Delete(context.Background(), 1); !errors.Is(err, outlet.ErrOutletNotFound) {
		t.Fatalf("重复删除 err = %v，想 %v", err, outlet.ErrOutletNotFound)
	}

	rebuilt, err := manager.Save(context.Background(), 1, "bot02", "new-secret-5678")
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.LastPushedID != saved.LastPushedID+1 {
		t.Fatalf("删除后重建基线 = %d，想删除期间通知 MAX(id)=%d", rebuilt.LastPushedID, saved.LastPushedID+1)
	}
	again, err := manager.Get(context.Background(), 1)
	if err != nil || again.KeyLast4 != "5678" {
		t.Fatalf("重建后的出口 = %+v err=%v，想新 key 末四位", again, err)
	}
}
