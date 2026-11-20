package outlet_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/outlet"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

func Test保存出口配置在同一事务写入成员通知基线并递增代数(t *testing.T) {
	s := openOutletTestStore(t)
	ctx := context.Background()
	insertOutletMember(t, s.DB, 1, "wang")
	insertOutletMember(t, s.DB, 2, "li")
	insertSystemNotification(t, s.DB, 1, "王工的第一条")
	insertSystemNotification(t, s.DB, 2, "李工的第一条")

	clk := clock.NewFake(time.Date(2026, 11, 20, 9, 0, 0, 0, clock.Beijing))
	cipher, err := outlet.NewCipher([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	manager := outlet.NewManager(s.DB, clk, cipher)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// 这条通知与保存配置同事务：基线必须包含它。
	if err := insertSystemNotificationTx(ctx, tx, 1, "保存配置同事务的新通知"); err != nil {
		t.Fatal(err)
	}
	saved, err := manager.SaveTx(ctx, tx, 1, "bot01", "webhook-secret")
	if err != nil {
		t.Fatal(err)
	}
	// 保存之后同事务再来的通知属于下一响，不能被基线吞掉。
	if err := insertSystemNotificationTx(ctx, tx, 1, "保存配置之后的新通知"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var baseline int64
	if err := s.DB.QueryRow(`SELECT last_pushed_id FROM outlet WHERE member_id=1`).Scan(&baseline); err != nil {
		t.Fatal(err)
	}
	if saved.LastPushedID != baseline || baseline != 3 {
		t.Fatalf("基线 = %d（返回 %d），想保存事务内的 MAX(id)=3", baseline, saved.LastPushedID)
	}
	if saved.Generation != 1 {
		t.Fatalf("首次保存 generation = %d，想 1", saved.Generation)
	}

	if _, err := s.DB.Exec(`UPDATE outlet SET status='paused',failure_count=3,pause_reason='bad key' WHERE member_id=1`); err != nil {
		t.Fatal(err)
	}
	insertSystemNotification(t, s.DB, 2, "李工的第二条")
	saved, err = manager.Save(ctx, 1, "bot02", "new-webhook-secret")
	if err != nil {
		t.Fatal(err)
	}
	var (
		botID, status, cipherText string
		generation, failures      int64
		pauseReason               sql.NullString
	)
	if err := s.DB.QueryRow(`SELECT bot_id,key_cipher,last_pushed_id,generation,status,failure_count,pause_reason
		FROM outlet WHERE member_id=1`).Scan(&botID, &cipherText, &baseline, &generation, &status, &failures, &pauseReason); err != nil {
		t.Fatal(err)
	}
	if botID != "bot02" || baseline != 4 || generation != 2 || status != "active" || failures != 0 || pauseReason.Valid {
		t.Fatalf("重建后的 outlet = %s/%d/%d/%s/%d/%v", botID, baseline, generation, status, failures, pauseReason)
	}
	if saved.Generation != generation {
		t.Fatalf("返回 generation = %d，库内 = %d", saved.Generation, generation)
	}
	if strings.Contains(cipherText, "new-webhook-secret") {
		t.Fatal("key_cipher 包含 webhook key 明文")
	}
	plain, err := cipher.Decrypt(cipherText)
	if err != nil || plain != "new-webhook-secret" {
		t.Fatalf("新密文解密失败或明文不符：err=%v", err)
	}
}

func Test保存出口配置拒绝非法botID缺失key和非法成员(t *testing.T) {
	s := openOutletTestStore(t)
	insertOutletMember(t, s.DB, 1, "wang")
	cipher, err := outlet.NewCipher([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	manager := outlet.NewManager(s.DB, clock.NewFake(time.Date(2026, 11, 20, 9, 0, 0, 0, clock.Beijing)), cipher)

	cases := []struct {
		name       string
		memberID   int64
		botID      string
		webhookKey string
		want       error
	}{
		{name: "botID大小写非法", memberID: 1, botID: "Bot01", webhookKey: "key", want: outlet.ErrInvalidBotID},
		{name: "botID路径非法", memberID: 1, botID: "../bot", webhookKey: "key", want: outlet.ErrInvalidBotID},
		{name: "webhookKey为空", memberID: 1, botID: "bot01", webhookKey: "", want: outlet.ErrInvalidWebhookKey},
		{name: "成员不存在", memberID: 99, botID: "bot01", webhookKey: "key", want: outlet.ErrMemberNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := manager.Save(context.Background(), tc.memberID, tc.botID, tc.webhookKey)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v，想 %v", err, tc.want)
			}
		})
	}
	var count int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM outlet`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("非法配置不应落库：count=%d err=%v", count, err)
	}

	noKeyManager := outlet.NewManager(s.DB, clock.Real{}, nil)
	if _, err := noKeyManager.Save(context.Background(), 1, "bot01", "key"); !errors.Is(err, outlet.ErrMasterKeyMissing) {
		t.Fatalf("缺失主密钥 err = %v，想 %v", err, outlet.ErrMasterKeyMissing)
	}
}

func openOutletTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func insertOutletMember(t *testing.T, db *sql.DB, id int64, username string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO members (id,username,name,password_hash,is_admin,created_at)
		VALUES (?,?,?,'x',0,'2026-11-20T09:00:00+08:00')`, id, username, username+"-name"); err != nil {
		t.Fatal(err)
	}
}

func insertSystemNotification(t *testing.T, db *sql.DB, memberID int64, text string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO notifications (member_id,kind,reason,text,created_at)
		VALUES (?,?,'backup_failing',?,'2026-11-20T09:00:00+08:00')`, memberID, "fyi", text)
	if err != nil {
		t.Fatal(err)
	}
}

func insertSystemNotificationTx(ctx context.Context, tx *sql.Tx, memberID int64, text string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO notifications (member_id,kind,reason,text,created_at)
		VALUES (?,?,'backup_failing',?,'2026-11-20T09:00:00+08:00')`, memberID, "fyi", text)
	return err
}
