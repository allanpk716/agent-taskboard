// Package ops 是「运维」模块（spec「建议的模块划分」）：备份调度（VACUUM INTO →
// 附件增量镜像 → PRAGMA integrity_check → 结果写库 → 轮换）与恢复。更新与回滚
// 在 #45 加入，更新前备份复用这里的 Preupdate。
//
// 备份目标用 UNC 路径配置（[backup].target）；没有配置时存到 data_dir\backups\，
// Status 里以 local_only 标注“备份未离开本机”。目标连不上时记一次失败，调度器
// 每小时重试一次；备份失败不影响服务运行。
package ops

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/store"
	_ "modernc.org/sqlite" // 副本的完整性校验用
)

// Kind 是备份的类别，出现在文件名里：taskboard-YYYYMMDD-HHMMSS-<kind>.db。
type Kind string

const (
	// KindDaily 是每日定时备份（保留 keep_daily 份）。
	KindDaily Kind = "daily"
	// KindMonthly 是每月 1 日的那份定时备份（保留 keep_monthly 份）。
	KindMonthly Kind = "monthly"
	// KindPreupdate 是更新前备份（保留 keep_preupdate 份；#45 调用）。
	KindPreupdate Kind = "preupdate"
)

// KindForDate 判定某天的定时备份是 daily 还是 monthly：每月 1 日是 monthly
// （“每月 1 日那份”），其余是 daily。
func KindForDate(t time.Time) Kind {
	if t.Day() == 1 {
		return KindMonthly
	}
	return KindDaily
}

// BackupFileName 生成副本文件名：taskboard-YYYYMMDD-HHMMSS-<kind>.db。
func BackupFileName(at time.Time, kind Kind) string {
	return "taskboard-" + at.Format("20060102") + "-" + at.Format("150405") + "-" + string(kind) + ".db"
}

// BackupOptions 是 config.toml [backup] 小节的运维侧形式。
type BackupOptions struct {
	// Target 是备份目标（UNC 或本地路径）；空串 → data_dir\backups\。
	Target string
	// DailyAt 是每日定时备份的时刻（"HH:MM"，北京时间）。
	DailyAt string
	// KeepDaily / KeepMonthly / KeepPreupdate 是各类别的保留份数。
	KeepDaily     int
	KeepMonthly   int
	KeepPreupdate int
}

// Record 是一次备份的结果，也是写进 backup_records 表的内容。
type Record struct {
	At          time.Time
	Kind        Kind
	Filename    string // 副本文件名；没产出副本的失败行为空
	Size        int64
	IntegrityOK bool // PRAGMA integrity_check 是否通过
	FilesAdded  int  // 本次附件镜像新增文件数
	Err         string
}

// Success 报告这次备份是否整体成功（产出了通过校验的副本）。
func (r Record) Success() bool { return r.Err == "" && r.IntegrityOK }

// Status 是管理页 / GET /api/v1/admin/backups 需要的备份状态。
type Status struct {
	// LocalOnly 报告是否没有配置备份目标（备份都在本机 data_dir 下），
	// 管理页据此提示“备份未离开本机”。
	LocalOnly bool
	// LastSuccessAt 是最近一次成功备份的时间；从未成功过为 nil。
	LastSuccessAt *time.Time
	// Overdue 报告最近一次成功备份是否已超过 36 小时（或从未成功过）。
	Overdue bool
	// LastFailure 是最近一次失败的记录；没有失败过为 nil。
	LastFailure *Record
	// Records 是最近的备份记录（新→旧）。
	Records []Record
}

// OverdueAfter 是“最近一次成功备份超过 36 小时”的阈值。
const OverdueAfter = 36 * time.Hour

// RetryEvery 是备份失败后的重试间隔（每小时重试一次，直到成功）。
const RetryEvery = time.Hour

// DefaultScheduleTick 是调度器两次检查之间的真实时间间隔。备份时刻本身由
// 注入时钟判定，所以间隔只影响发现时刻的延迟。
const DefaultScheduleTick = 15 * time.Second

// 备份文件名（含 .bad 改名后不带 .db 的部分）的解析。
var backupNameRe = regexp.MustCompile(`^taskboard-(\d{8})-(\d{6})-(daily|monthly|preupdate)\.db$`)

// Backups 是备份与恢复的入口：手动备份（Now）、更新前备份（Preupdate，#45）、
// 内置定时调度（RunSchedule / CheckSchedule）和状态查询（Status）。
type Backups struct {
	db      *sql.DB
	clk     clock.Clock
	dataDir string
	opts    BackupOptions
	atHour  int
	atMin   int

	// 调度状态（CheckSchedule / RunSchedule 用）。
	mu      sync.Mutex
	started bool
	nextDue time.Time
}

// NewBackups 建备份管理器。daily_at 写错（不是 HH:MM）会返回错误。
func NewBackups(db *sql.DB, dataDir string, opts BackupOptions, c clock.Clock) (*Backups, error) {
	b := &Backups{db: db, clk: c, dataDir: dataDir, opts: opts}
	h, m, err := parseDailyAt(opts.DailyAt)
	if err != nil {
		return nil, err
	}
	b.atHour, b.atMin = h, m
	return b, nil
}

func parseDailyAt(s string) (int, int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("[backup] daily_at 要写成 \"HH:MM\"，当前是 %q", s)
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("[backup] daily_at 要写成 \"HH:MM\"（0-23:0-59），当前是 %q", s)
	}
	return h, m, nil
}

// TargetDir 返回备份落盘目录：配置了目标就是目标，否则 data_dir\backups\。
func (b *Backups) TargetDir() string {
	if b.opts.Target != "" {
		return b.opts.Target
	}
	return filepath.Join(b.dataDir, "backups")
}

// attachmentsDir 返回附件主存储目录（data_dir\attachments）。
func (b *Backups) attachmentsDir() string { return filepath.Join(b.dataDir, "attachments") }

// Now 立刻做一次备份（taskboard backup、定时任务和更新前备份共用）：
// 导出（VACUUM INTO）→ 附件增量镜像（只增不删）→ 对副本 PRAGMA integrity_check
// → 结果写库 → 清点既有副本（损坏的改名 .bad）→ 按保留份数轮换。
// 失败也返回记录（已写库）和错误；调用方的服务照常运行。
func (b *Backups) BackupNow(ctx context.Context, kind Kind) (Record, error) {
	now := b.clk.Now()
	rec := Record{At: now, Kind: kind}

	dir := b.TargetDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return b.fail(ctx, rec, fmt.Errorf("连不上备份目标 %s：%w", dir, err))
	}
	path := filepath.Join(dir, BackupFileName(now, kind))
	existed := fileExists(path)
	if err := b.export(ctx, path); err != nil {
		if !existed {
			_ = os.Remove(path) // 自己没写完的半个副本不留
		}
		return b.fail(ctx, rec, err)
	}
	rec.Filename = filepath.Base(path)
	if info, err := os.Stat(path); err == nil {
		rec.Size = info.Size()
	}

	added, err := b.syncMirror()
	if err != nil {
		return b.fail(ctx, rec, err)
	}
	rec.FilesAdded = added

	ok, err := verifyCopy(path)
	if err != nil || !ok {
		// 校验不通过的副本改名 .bad 保留：不算成功，也不参与轮换，
		// 所以不会因此淘汰旧的好副本。
		_ = os.Rename(path, path+".bad")
		if err != nil {
			return b.fail(ctx, rec, fmt.Errorf("副本 %s 完整性校验出错：%w", rec.Filename, err))
		}
		return b.fail(ctx, rec, fmt.Errorf("副本 %s 完整性校验未通过", rec.Filename))
	}
	rec.IntegrityOK = true

	if err := b.insertRecord(ctx, rec); err != nil {
		return rec, err
	}
	b.sweepAndRotate(dir, rec.Filename)
	return rec, nil
}

// Preupdate 是更新前备份（#45 的更新流程调用；失败时更新必须中止）。
func (b *Backups) Preupdate(ctx context.Context) (Record, error) {
	return b.BackupNow(ctx, KindPreupdate)
}

// fail 把失败也记一行并返回错误。
func (b *Backups) fail(ctx context.Context, rec Record, cause error) (Record, error) {
	rec.Err = cause.Error()
	if err := b.insertRecord(ctx, rec); err != nil {
		return rec, fmt.Errorf("%v（且记录失败：%w）", cause, err)
	}
	return rec, cause
}

// export 用 VACUUM INTO 做在线备份，不直接复制运行中的库文件。
func (b *Backups) export(ctx context.Context, path string) error {
	if fileExists(path) {
		return fmt.Errorf("备份文件 %s 已存在（同一秒备份过一次？）", filepath.Base(path))
	}
	if _, err := b.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("VACUUM INTO %s 失败：%w", filepath.Base(path), err)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// syncMirror 把附件主存储增量镜像到目标下的 attachments\，只增不删：
// 已存在且同大小的文件跳过（附件写入后不再修改；大小不一致说明上次拷贝
// 没写完整，重拷一份）。返回本次新增的文件数。
func (b *Backups) syncMirror() (int, error) {
	src := b.attachmentsDir()
	dst := filepath.Join(b.TargetDir(), "attachments")
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // 还没有任何附件
		}
		return 0, fmt.Errorf("读附件目录 %s 失败：%w", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return 0, fmt.Errorf("连不上备份目标 %s：%w", dst, err)
	}
	added := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		from := filepath.Join(src, e.Name())
		to := filepath.Join(dst, e.Name())
		if info, err := os.Stat(to); err == nil {
			if srcInfo, serr := e.Info(); serr == nil && info.Size() == srcInfo.Size() {
				continue // 已镜像过（只增不删：主存储删掉的文件镜像保留）
			}
		}
		if err := copyFile(from, to); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}

// sweepAndRotate 清点目标里既有的副本：损坏的改名 .bad（不参与轮换，好副本
// 不会被它挤掉），再按各类保留份数删掉最旧的。刚生成的副本（skip）已校验
// 过，不重复校验，但要计入保留名额。
func (b *Backups) sweepAndRotate(dir, skip string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // 目标都读不了，下一轮备份会记录失败
	}
	byKind := map[Kind][]string{}
	for _, e := range entries {
		name := e.Name()
		m := backupNameRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		if name == skip {
			byKind[Kind(m[3])] = append(byKind[Kind(m[3])], name)
			continue
		}
		path := filepath.Join(dir, name)
		if ok, err := verifyCopy(path); err != nil || !ok {
			_ = os.Rename(path, path+".bad")
			continue
		}
		byKind[Kind(m[3])] = append(byKind[Kind(m[3])], name)
	}
	keep := map[Kind]int{KindDaily: b.opts.KeepDaily, KindMonthly: b.opts.KeepMonthly, KindPreupdate: b.opts.KeepPreupdate}
	for kind, names := range byKind {
		// 文件名里的时间戳保证字典序就是时间序。
		sort.Sort(sort.Reverse(sort.StringSlice(names)))
		if len(names) > keep[kind] {
			for _, name := range names[keep[kind]:] {
				_ = os.Remove(filepath.Join(dir, name))
			}
		}
	}
}

// verifyCopy 对一个数据库副本执行 PRAGMA integrity_check；返回是否通过。
func verifyCopy(path string) (bool, error) {
	db, err := sql.Open("sqlite", store.FileURI(path)+"?mode=ro")
	if err != nil {
		return false, err
	}
	defer db.Close()
	var result string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return false, err
	}
	return result == "ok", nil
}

// copyFile 先写 .part 再改名，中途失败不留半截文件（附件镜像、恢复换库、
// 恢复补附件共用）。
func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return fmt.Errorf("读 %s 失败：%w", from, err)
	}
	defer in.Close()
	tmp := to + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("写 %s 失败：%w", to, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("拷贝 %s → %s 失败：%w", from, to, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("写 %s 失败：%w", to, err)
	}
	return os.Rename(tmp, to)
}

// insertRecord 把一次备份的结果（成功或失败）写入 backup_records。
func (b *Backups) insertRecord(ctx context.Context, r Record) error {
	var errText any
	if r.Err != "" {
		errText = r.Err
	}
	ok := 0
	if r.IntegrityOK {
		ok = 1
	}
	_, err := b.db.ExecContext(ctx, `
		INSERT INTO backup_records (at, kind, filename, size, integrity_ok, files_added, error)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.At.Format(time.RFC3339Nano), string(r.Kind), r.Filename, r.Size, ok, r.FilesAdded, errText)
	if err != nil {
		return fmt.Errorf("写备份记录失败：%w", err)
	}
	return nil
}

// Status 汇总备份状态：最近一次成功备份的时间、是否超过 36 小时、是否
// “备份未离开本机”，以及最近的备份记录。
func (b *Backups) Status(ctx context.Context) Status {
	s := Status{LocalOnly: b.opts.Target == ""}
	// 最近一次成功备份单独查（不能只看最新 20 条：连续失败时成功记录会被挤出去）。
	var at string
	if err := b.db.QueryRowContext(ctx, `
		SELECT at FROM backup_records
		WHERE error IS NULL AND integrity_ok = 1
		ORDER BY at DESC, id DESC LIMIT 1`).Scan(&at); err == nil {
		if t, err := time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing); err == nil {
			s.LastSuccessAt = &t
		}
	}
	rows, err := b.db.QueryContext(ctx, `
		SELECT at, kind, filename, size, integrity_ok, files_added, error
		FROM backup_records ORDER BY at DESC, id DESC LIMIT 20`)
	if err != nil {
		return s
	}
	defer rows.Close()
	for rows.Next() {
		var (
			r       Record
			at      string
			kind    string
			ok      int
			errText sql.NullString
		)
		if err := rows.Scan(&at, &kind, &r.Filename, &r.Size, &ok, &r.FilesAdded, &errText); err != nil {
			return s
		}
		r.At, _ = time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing)
		r.Kind = Kind(kind)
		r.IntegrityOK = ok == 1
		r.Err = errText.String
		if !r.Success() && s.LastFailure == nil {
			f := r
			s.LastFailure = &f
		}
		s.Records = append(s.Records, r)
	}
	if s.LastSuccessAt == nil {
		s.Overdue = true // 从未成功备份过
	} else if d := b.clk.Now().Sub(*s.LastSuccessAt); d > OverdueAfter {
		s.Overdue = true
	}
	return s
}
