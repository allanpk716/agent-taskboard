// 运维模块（#43 备份与恢复）的黑盒测试：只观察磁盘上的备份文件、镜像目录
// 和对外结构（Record / Status），不碰内部表。定时、轮换、恢复见同包其他文件。
package ops_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/guestcredentials"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/store"
	_ "modernc.org/sqlite"
)

// newBackups 在临时 data_dir 上开一个真实库并建备份管理器；target 为空时
// 用默认（data_dir\backups\）。
func newBackups(t *testing.T, target string) (*ops.Backups, *clock.Fake, string) {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	t.Cleanup(func() { st.Close() })
	opts := ops.BackupOptions{DailyAt: "03:00", KeepDaily: 30, KeepMonthly: 12, KeepPreupdate: 5}
	if target != "" {
		opts.Target = target
	}
	clk := clock.NewFake(beijing(2026, 10, 16, 3, 0, 5))
	b, err := ops.NewBackups(st.DB, dataDir, opts, clk)
	if err != nil {
		t.Fatalf("建备份管理器失败：%v", err)
	}
	return b, clk, dataDir
}

func beijing(y int, mo time.Month, d, h, mi, s int) time.Time {
	return time.Date(y, mo, d, h, mi, s, 0, clock.Beijing)
}

// newBackupsWithKeep 用指定的保留份数建备份管理器（轮换测试用）。
func newBackupsWithKeep(t *testing.T, dataDir, target string, keepDaily, keepMonthly, keepPreupdate int) (*ops.Backups, *clock.Fake) {
	t.Helper()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	t.Cleanup(func() { st.Close() })
	clk := clock.NewFake(beijing(2026, 10, 16, 3, 0, 5))
	b, err := ops.NewBackups(st.DB, dataDir, ops.BackupOptions{
		Target: target, DailyAt: "03:00",
		KeepDaily: keepDaily, KeepMonthly: keepMonthly, KeepPreupdate: keepPreupdate,
	}, clk)
	if err != nil {
		t.Fatalf("建备份管理器失败：%v", err)
	}
	return b, clk
}

// 备份文件命名正确（taskboard-YYYYMMDD-HHMMSS-<kind>.db），副本是库的真实
// 快照并通过完整性校验，结果（时间、大小、是否通过、附件新增文件数）写库。
func Test备份文件命名正确并通过完整性校验(t *testing.T) {
	target := t.TempDir()
	b, clk, _ := newBackups(t, target)

	rec, err := b.BackupNow(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatalf("备份失败：%v", err)
	}
	name := "taskboard-20261016-030005-daily.db"
	if rec.Filename != name {
		t.Fatalf("文件名 = %q, 想 %q", rec.Filename, name)
	}
	if rec.At != clk.Now() {
		t.Fatalf("记录时间 %v 不等于备份时刻 %v", rec.At, clk.Now())
	}
	if !rec.IntegrityOK || !rec.Success() {
		t.Fatalf("副本应通过完整性校验：%+v", rec)
	}
	if rec.Size <= 0 {
		t.Fatalf("副本大小应大于 0：%d", rec.Size)
	}
	// 副本是库的真实快照：里面有迁移建出的表。
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(target, name))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'tickets'`).Scan(&n); err != nil {
		t.Fatalf("读副本失败：%v", err)
	}
	if n != 1 {
		t.Fatal("副本里没有 tickets 表，不是库的快照")
	}
	// 结果写库：Status 里能看到这一条。
	status := b.Status(context.Background())
	if status.LastSuccessAt == nil || !status.LastSuccessAt.Equal(clk.Now()) {
		t.Fatalf("最近一次成功备份时间 = %v, 想 %v", status.LastSuccessAt, clk.Now())
	}
	if status.Overdue {
		t.Fatal("刚备份完不应超过 36 小时")
	}
	if len(status.Records) != 1 || status.Records[0].Filename != name {
		t.Fatalf("记录里应有这一条备份：%+v", status.Records)
	}
}

func Test备份恢复覆盖访客凭证持有者和设备(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	seed := []string{
		`INSERT INTO members (id, username, name, password_hash, is_admin, created_at)
		 VALUES (1,'wang','王工','x',1,'2026-11-02T09:00:00+08:00')`,
		`INSERT INTO tickets (seq, number, kind, title, status, brief_md, creator_member_id,
		 assignee_id, created_at, last_change)
		 VALUES (1,'20261102-001','task','备份访客凭证','open','说明',1,1,
		         '2026-11-02T09:00:00+08:00',1)`,
		`INSERT INTO guest_links (id, ticket_seq, key_hash, note, expires_at, can_edit_brief,
		                         created_by_member_id, created_at)
		 VALUES (1,1,'key-hash','检验科-张三','2099-01-01T00:00:00+08:00',1,1,
		         '2026-11-02T09:00:00+08:00')`,
	}
	for _, query := range seed {
		if _, err := st.DB.Exec(query); err != nil {
			t.Fatalf("造备份前数据失败：%v\n%s", err, query)
		}
	}
	holder, err := guestcredentials.CreateLinkHolder(ctx, st.DB, 1, beijing(2026, 11, 2, 9, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := guestcredentials.Claim(ctx, st.DB, holder.ID, "张三", beijing(2026, 11, 2, 10, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ops.NewBackups(st.DB, dataDir, ops.BackupOptions{
		DailyAt: "03:00", KeepDaily: 30, KeepMonthly: 12, KeepPreupdate: 5,
	},
		clock.NewFake(beijing(2026, 11, 2, 11, 0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	record, err := b.BackupNow(ctx, ops.KindDaily)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	freshData := t.TempDir()
	if _, err := ops.Restore(freshData, 0, filepath.Join(dataDir, "backups", record.Filename),
		beijing(2026, 11, 2, 11, 1, 0)); err != nil {
		t.Fatalf("恢复备份失败：%v", err)
	}
	restored, err := store.Open(freshData)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	idm := identity.NewManager(restored.DB, clock.NewFake(beijing(2026, 11, 2, 12, 0, 0)))
	viewer, err := idm.ViewerFromGuestDevice(ctx, claim.DeviceToken)
	if err != nil || viewer.GuestLink == nil || viewer.GuestLink.ClaimName != "张三" {
		t.Fatalf("恢复后的访客设备凭证不可用：viewer=%+v err=%v", viewer, err)
	}
}

// 每月 1 日的备份标记为 monthly，其余为 daily（KindForDate 按北京时间判）。
func Test每月一日的那份备份是monthly(t *testing.T) {
	if got := ops.KindForDate(beijing(2026, 11, 1, 3, 0, 0)); got != ops.KindMonthly {
		t.Fatalf("1 日 = %q, 想 monthly", got)
	}
	if got := ops.KindForDate(beijing(2026, 11, 2, 3, 0, 0)); got != ops.KindDaily {
		t.Fatalf("2 日 = %q, 想 daily", got)
	}
	target := t.TempDir()
	b, clk, _ := newBackups(t, target)
	clk.Set(beijing(2026, 11, 1, 3, 0, 5))
	rec, err := b.BackupNow(context.Background(), ops.KindMonthly)
	if err != nil {
		t.Fatalf("备份失败：%v", err)
	}
	if rec.Filename != "taskboard-20261101-030005-monthly.db" {
		t.Fatalf("文件名 = %q", rec.Filename)
	}
}

// 附件做增量镜像：新增文件按次统计；未引用附件清除只删主存储，镜像仍保留。
// 归档移除会另行删除对应镜像（#236）。
func Test附件镜像同步不删除并统计新增文件数(t *testing.T) {
	target := t.TempDir()
	b, clk, dataDir := newBackups(t, target)
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	defer st.Close()
	if _, err := st.DB.Exec(`INSERT INTO tickets (seq, number, kind, title, created_at)
		VALUES (1, '20261016-001', 'task', '镜像工单', ?)`,
		clk.Now().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	atts := filepath.Join(dataDir, "attachments")
	writeFile := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(atts, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(atts, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("1", "one")
	writeFile("2", "two")
	if _, err := st.DB.Exec(`INSERT INTO attachments
		(ticket_seq, filename, size, state, attached_to, uploaded_at, claimed_at)
		VALUES (1, ?, 3, 'attached', '说明', ?, ?), (1, ?, 3, 'attached', '说明', ?, ?)`,
		"one.txt", clk.Now().Format(time.RFC3339Nano), clk.Now().Format(time.RFC3339Nano),
		"two.txt", clk.Now().Format(time.RFC3339Nano), clk.Now().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	rec, err := b.BackupNow(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatal(err)
	}
	if rec.FilesAdded != 2 {
		t.Fatalf("第一次镜像应新增 2 个文件，得到 %d", rec.FilesAdded)
	}
	mirror := filepath.Join(target, "attachments")
	if read(t, filepath.Join(mirror, "1")) != "one" || read(t, filepath.Join(mirror, "2")) != "two" {
		t.Fatal("镜像里应有附件 1 和 2 的内容")
	}

	// 第二次只同步新文件。
	writeFile("3", "three")
	if _, err := st.DB.Exec(`INSERT INTO attachments
		(ticket_seq, filename, size, state, attached_to, uploaded_at, claimed_at)
		VALUES (1, 'three.txt', 5, 'attached', '说明', ?, ?)`,
		clk.Now().Format(time.RFC3339Nano), clk.Now().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour)
	rec, err = b.BackupNow(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatal(err)
	}
	if rec.FilesAdded != 1 {
		t.Fatalf("第二次镜像应只新增 1 个文件，得到 %d", rec.FilesAdded)
	}

	// 主存储删除后同步不删镜像；归档移除才会删除对应镜像。
	if err := os.Remove(filepath.Join(atts, "1")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DELETE FROM attachments WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour)
	if _, err := b.BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(mirror, "1")) != "one" {
		t.Fatal("镜像不应删除主存储里已不存在的附件")
	}
}

// 未配置备份目标时备份存到 data_dir\backups\，并在 Status 里标注“备份未离开本机”
// （local_only；管理页据此提示）。
func Test未配置目标时备份存数据目录并标注未离开本机(t *testing.T) {
	b, _, dataDir := newBackups(t, "")
	if _, err := b.BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	if got := b.TargetDir(); got != filepath.Join(dataDir, "backups") {
		t.Fatalf("未配置目标时应落到 data_dir\\backups\\，得到 %q", got)
	}
	name := filepath.Join(dataDir, "backups", "taskboard-20261016-030005-daily.db")
	if _, err := os.Stat(name); err != nil {
		t.Fatalf("默认目标里没有备份文件：%v", err)
	}
	status := b.Status(context.Background())
	if !status.LocalOnly {
		t.Fatal("未配置目标时 Status 应标注 local_only")
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", path, err)
	}
	return string(b)
}

// 备份恢复覆盖访客本体与访客级凭证：恢复后这台设备仍以同一位长期访客
// 身份通行其名下链接。
func Test备份恢复覆盖访客与访客级凭证(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	seed := []string{
		`INSERT INTO members (id, username, name, password_hash, is_admin, created_at)
		 VALUES (1,'wang','王工','x',1,'2026-11-02T09:00:00+08:00')`,
		`INSERT INTO tickets (seq, number, kind, title, status, brief_md, creator_member_id,
		 assignee_id, created_at, last_change)
		 VALUES (1,'20261102-001','task','备份访客身份','open','说明',1,1,
		         '2026-11-02T09:00:00+08:00',1)`,
		`INSERT INTO guests (id, name, created_by_member_id, created_at,
		                     home_key_hash, home_key_ciphertext)
		 VALUES (1,'检验科-张三',1,'2026-11-02T09:00:00+08:00',
		         'home-key-hash','home-key-ciphertext')`,
		`INSERT INTO guest_links (id, ticket_seq, guest_id, key_hash, note, expires_at,
		                         can_edit_brief, created_by_member_id, created_at)
		 VALUES (1,1,1,'key-hash','请补充','2099-01-01T00:00:00+08:00',1,1,
		         '2026-11-02T09:00:00+08:00')`,
	}
	for _, query := range seed {
		if _, err := st.DB.Exec(query); err != nil {
			t.Fatalf("造备份前数据失败：%v\n%s", err, query)
		}
	}
	holder, err := guestcredentials.CreateGuestHolder(ctx, st.DB, 1, beijing(2026, 11, 2, 9, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE guest_links SET credential_holder_id = ? WHERE id = 1`, holder.ID); err != nil {
		t.Fatal(err)
	}
	claim, err := guestcredentials.Claim(ctx, st.DB, holder.ID, "张三", beijing(2026, 11, 2, 10, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ops.NewBackups(st.DB, dataDir, ops.BackupOptions{
		DailyAt: "03:00", KeepDaily: 30, KeepMonthly: 12, KeepPreupdate: 5,
	}, clock.NewFake(beijing(2026, 11, 2, 11, 0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	record, err := b.BackupNow(ctx, ops.KindDaily)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	freshData := t.TempDir()
	if _, err := ops.Restore(freshData, 0, filepath.Join(dataDir, "backups", record.Filename),
		beijing(2026, 11, 2, 11, 1, 0)); err != nil {
		t.Fatalf("恢复备份失败：%v", err)
	}
	restored, err := store.Open(freshData)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var guestName string
	if err := restored.DB.QueryRow(`SELECT name FROM guests WHERE id = 1`).Scan(&guestName); err != nil || guestName != "检验科-张三" {
		t.Fatalf("恢复后的访客 = %q (err=%v)，想 检验科-张三", guestName, err)
	}
	var homeHash, homeCipher string
	if err := restored.DB.QueryRow(
		`SELECT home_key_hash, home_key_ciphertext FROM guests WHERE id = 1`).
		Scan(&homeHash, &homeCipher); err != nil || homeHash != "home-key-hash" || homeCipher != "home-key-ciphertext" {
		t.Fatalf("恢复后的主页密钥 = %q/%q (err=%v)，想原哈希与密文", homeHash, homeCipher, err)
	}
	idm := identity.NewManager(restored.DB, clock.NewFake(beijing(2026, 11, 2, 12, 0, 0)))
	viewer, err := idm.ViewerFromGuestDevice(ctx, claim.DeviceToken)
	if err != nil || viewer.Guest == nil || viewer.Guest.Name != "检验科-张三" || viewer.Guest.ClaimName != "张三" {
		t.Fatalf("恢复后的访客级设备凭证不可用：viewer=%+v err=%v", viewer, err)
	}
}
