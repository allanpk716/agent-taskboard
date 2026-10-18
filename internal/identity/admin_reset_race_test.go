package identity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

// 模拟停用落在“检查已通过之后、密码更新之前”的并发交错：重置必须失败，
// 不能给一个在同一事务里已停用的账号发初始密码。
func Test重置密码不能越过事务内的停用(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	m := identity.NewManager(st.DB, clock.NewFake(time.Date(2026, 10, 18, 9, 0, 0, 0, clock.Beijing)))
	admin, err := m.CreateAdmin(ctx, "admin", "管理员", "admin-pass")
	if err != nil {
		t.Fatal(err)
	}
	member, err := m.CreateMember(ctx, "wang", "王工", "member-pass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `
CREATE TRIGGER deactivate_during_reset
AFTER UPDATE OF password_hash ON members
WHEN NEW.id = 2
BEGIN
	UPDATE members SET deactivated_at = '2026-10-18T09:01:00+08:00' WHERE id = NEW.id;
END`); err != nil {
		t.Fatal(err)
	}

	password, err := m.AdminResetMemberPassword(ctx, admin.ID, member.ID)
	if !errors.Is(err, identity.ErrMemberInactive) {
		t.Fatalf("重置结果 = (%q, %v)，想 ErrMemberInactive", password, err)
	}
	if password != "" {
		t.Fatalf("失败的Reset仍返回了初始密码：%q", password)
	}
}
