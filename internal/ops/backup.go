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
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/guestlinkkey"
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
	Location    Location
}

// Location 表示副本最终写在哪里：target 是配置的目标（未配目标时即本机
// backups 目录），local 是目标不可达时的本机兜底副本。
type Location string

const (
	LocationTarget Location = "target"
	LocationLocal  Location = "local"
)

// Success 报告这次备份是否整体成功（产出了通过校验的副本）。
func (r Record) Success() bool { return r.Err == "" && r.IntegrityOK }

// Status 是管理页 / GET /api/v1/admin/backups 需要的备份状态。
type Status struct {
	// LocalOnly 报告是否没有配置备份目标（备份都在本机 data_dir 下），
	// 管理页据此提示“备份未离开本机”。
	LocalOnly bool
	// LastSuccessAt 是最近一次成功备份的时间；从未成功过为 nil。
	LastSuccessAt          *time.Time
	LastLocalFallbackAt    *time.Time
	TargetUnreachable      bool
	TargetUnreachableError string
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
	verify  func(path string) (bool, error)

	// 调度状态（CheckSchedule / RunSchedule 用）。
	mu       sync.Mutex
	started  bool
	nextDue  time.Time
	notifier BackupNotifier
}

// BackupNotifier 是备份模块向系统通知模块发出的告警接口；测试可注入假实现。
type BackupNotifier interface {
	LastBackupAlertAt(ctx context.Context, since time.Time) (time.Time, bool, error)
	NotifyBackupFailing(ctx context.Context, startedAt time.Time, lastSuccessAt *time.Time, fallback bool, cause string) error
	NotifyBackupStillFailing(ctx context.Context, startedAt time.Time, hours int) error
	NotifyBackupRecovered(ctx context.Context, startedAt time.Time, text string) error
}

// SetNotifier 注入系统通知实现；不注入时备份照常运行，只写记录和日志。
func (b *Backups) SetNotifier(n BackupNotifier) { b.notifier = n }

// NewBackups 建备份管理器。daily_at 写错（不是 HH:MM）会返回错误。
func NewBackups(db *sql.DB, dataDir string, opts BackupOptions, c clock.Clock) (*Backups, error) {
	b := &Backups{db: db, clk: c, dataDir: dataDir, opts: opts, verify: verifyCopy}
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
	rec := Record{At: now, Kind: kind, Location: LocationTarget}

	if b.opts.Target == "" {
		rec, err := b.backupLocalOnly(ctx, now, kind)
		b.notifyResult(ctx, rec, false)
		return rec, err
	}

	localDir := filepath.Join(b.dataDir, "backups")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return b.fail(ctx, rec, fmt.Errorf("建本机兜底目录 %s 失败：%w", localDir, err))
	}
	name := BackupFileName(now, kind)
	staging := filepath.Join(localDir, "."+name+".part")
	if err := b.export(ctx, staging); err != nil {
		_ = os.Remove(staging)
		return b.fail(ctx, rec, err)
	}
	rec.Filename = name
	if info, err := os.Stat(staging); err == nil {
		rec.Size = info.Size()
	}
	ok, err := b.verify(staging)
	if err != nil || !ok {
		_ = os.Rename(staging, staging+".bad")
		if err != nil {
			return b.fail(ctx, rec, fmt.Errorf("副本 %s 完整性校验出错：%w", rec.Filename, err))
		}
		return b.fail(ctx, rec, fmt.Errorf("副本 %s 完整性校验未通过", rec.Filename))
	}

	if err := b.writeTarget(ctx, staging, name); err != nil {
		return b.fallbackLocal(ctx, rec, staging, localDir, name, err)
	}

	added, err := b.syncMirror()
	if err != nil {
		_ = os.Remove(filepath.Join(b.opts.Target, name))
		return b.fallbackLocal(ctx, rec, staging, localDir, name, err)
	}
	if err := b.syncGuestLinkKey(b.opts.Target); err != nil {
		_ = os.Remove(filepath.Join(b.opts.Target, name))
		return b.fallbackLocal(ctx, rec, staging, localDir, name, err)
	}
	rec.FilesAdded = added
	rec.IntegrityOK = true

	episodeStart, hadFailure := b.failureEpisodeStart(ctx)
	if err := b.insertRecord(ctx, rec); err != nil {
		return rec, err
	}
	b.sweepAndRotate(b.opts.Target, rec.Filename)
	_ = os.Remove(staging)
	if hadFailure {
		b.notifyRecovered(ctx, episodeStart)
	}
	return rec, nil
}

// fallbackLocal 在目标失败后尝试把已校验的暂存副本转成本机兜底副本；同一天
// 已兜底过则只记录失败，不再生成第二份。
func (b *Backups) fallbackLocal(ctx context.Context, rec Record, staging, localDir, name string, cause error) (Record, error) {
	wrapped := fmt.Errorf("备份目标不可达 %s：%w", b.opts.Target, cause)
	if b.localFallbackDone(ctx, rec.At, rec.Kind, name) {
		_ = os.Remove(staging)
		rec.Filename = ""
		rec.Size = 0
		return b.fail(ctx, rec, wrapped)
	}
	if err := os.Rename(staging, filepath.Join(localDir, name)); err != nil {
		_ = os.Remove(staging)
		return b.fail(ctx, rec, fmt.Errorf("备份目标不可达 %s：%w；本机兜底也失败：%v", b.opts.Target, cause, err))
	}
	if err := b.syncGuestLinkKey(localDir); err != nil {
		return b.fail(ctx, rec, fmt.Errorf("备份目标不可达 %s：%w；本机兜底访客链接加密密钥失败：%v", b.opts.Target, cause, err))
	}
	rec.Location = LocationLocal
	rec.Err = wrapped.Error()
	rec.IntegrityOK = true
	if err := b.insertRecord(ctx, rec); err != nil {
		return rec, err
	}
	b.sweepAndRotate(localDir, rec.Filename)
	b.notifyResult(ctx, rec, false)
	return rec, nil
}

func (b *Backups) backupLocalOnly(ctx context.Context, now time.Time, kind Kind) (Record, error) {
	rec := Record{At: now, Kind: kind, Location: LocationTarget}
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
	if err := b.syncGuestLinkKey(dir); err != nil {
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

func (b *Backups) writeTarget(ctx context.Context, staging, name string) error {
	dir := b.TargetDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	final := filepath.Join(dir, name)
	if err := copyFile(staging, final+".part"); err != nil {
		_ = os.Remove(final + ".part")
		return err
	}
	if err := os.Rename(final+".part", final); err != nil {
		_ = os.Remove(final + ".part")
		return err
	}
	ok, err := verifyCopy(final)
	if err != nil || !ok {
		_ = os.Remove(final)
		if err != nil {
			return err
		}
		return fmt.Errorf("目标副本 %s 完整性校验未通过", name)
	}
	return nil
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
	b.notifyResult(ctx, rec, false)
	return rec, cause
}

func (b *Backups) localFallbackDone(ctx context.Context, now time.Time, kind Kind, name string) bool {
	var where string
	var args []any
	if kind == KindPreupdate {
		where = "location = 'local' AND filename = ?"
		args = []any{name}
	} else {
		where = "location = 'local' AND kind = ? AND substr(at, 1, 10) = ?"
		args = []any{string(kind), now.Format("2006-01-02")}
	}
	var n int
	if err := b.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backup_records WHERE `+where, args...).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

func (b *Backups) notifyResult(ctx context.Context, rec Record, recovered bool) {
	if recovered || rec.Err == "" || rec.Kind == KindPreupdate || b.notifier == nil {
		return
	}
	start, ok := b.failureEpisodeStartLocked(ctx)
	if !ok {
		return
	}
	var lastSuccess *time.Time
	if at, ok := b.lastTargetSuccess(ctx); ok {
		lastSuccess = &at
	}
	lastAlert, alerted, err := b.notifier.LastBackupAlertAt(ctx, start)
	if err != nil {
		log.Printf("查备份告警节奏失败：%v", err)
		return
	}
	if !alerted {
		if err := b.notifier.NotifyBackupFailing(ctx, start, lastSuccess, rec.Location == LocationLocal, rec.Err); err != nil {
			log.Printf("发备份失败告警失败：%v", err)
		}
		return
	}
	if elapsed := b.clk.Now().Sub(lastAlert); elapsed >= 24*time.Hour {
		hours := int(math.Ceil(b.clk.Now().Sub(start).Hours()))
		if err := b.notifier.NotifyBackupStillFailing(ctx, start, hours); err != nil {
			log.Printf("发备份持续失败提醒失败：%v", err)
		}
	}
}

func (b *Backups) notifyRecovered(ctx context.Context, start time.Time) {
	if b.notifier == nil {
		return
	}
	if err := b.notifier.NotifyBackupRecovered(ctx, start, "备份已恢复，最新副本已写到目标"); err != nil {
		log.Printf("发备份恢复通知失败：%v", err)
	}
}

func (b *Backups) failureEpisodeStartLocked(ctx context.Context) (time.Time, bool) {
	success := ""
	if at, ok := b.lastTargetSuccess(ctx); ok {
		success = at.Format(time.RFC3339Nano)
	}
	var at string
	if err := b.db.QueryRowContext(ctx, `
		SELECT at FROM backup_records
		WHERE error IS NOT NULL AND kind != 'preupdate' AND at > ?
		ORDER BY at ASC, id ASC LIMIT 1`, success).Scan(&at); err != nil {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing)
	return t, err == nil
}

func (b *Backups) failureEpisodeStart(ctx context.Context) (time.Time, bool) {
	return b.failureEpisodeStartLocked(ctx)
}

func (b *Backups) lastTargetSuccess(ctx context.Context) (time.Time, bool) {
	var at string
	if err := b.db.QueryRowContext(ctx, `
		SELECT at FROM backup_records
		WHERE error IS NULL AND integrity_ok = 1 AND location = 'target'
		ORDER BY at DESC, id DESC LIMIT 1`).Scan(&at); err != nil {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing)
	return t, err == nil
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

// syncGuestLinkKey 把数据目录里的访客链接加密密钥文件复制到备份目录（原子替换），
// 与库副本放在一起。密钥文件还不存在（更早版本的数据目录，或启动时降级）时
// 跳过：没有密钥可备份，备份本身不因此失败。
func (b *Backups) syncGuestLinkKey(dir string) error {
	from := filepath.Join(b.dataDir, guestlinkkey.FileName)
	info, err := os.Stat(from)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读访客链接加密密钥 %s 失败：%w", from, err)
	}
	if info.IsDir() {
		return fmt.Errorf("访客链接加密密钥 %s 是个目录，不是密钥文件", from)
	}
	return copyFile(from, filepath.Join(dir, guestlinkkey.FileName))
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
		INSERT INTO backup_records (at, kind, filename, size, integrity_ok, files_added, error, location)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.At.Format(time.RFC3339Nano), string(r.Kind), r.Filename, r.Size, ok, r.FilesAdded, errText, string(r.Location))
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
		WHERE error IS NULL AND integrity_ok = 1 AND location = 'target'
		ORDER BY at DESC, id DESC LIMIT 1`).Scan(&at); err == nil {
		if t, err := time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing); err == nil {
			s.LastSuccessAt = &t
		}
	}
	rows, err := b.db.QueryContext(ctx, `
		SELECT at, kind, filename, size, integrity_ok, files_added, error, location
		FROM backup_records ORDER BY at DESC, id DESC LIMIT 20`)
	if err != nil {
		return s
	}
	defer rows.Close()
	for rows.Next() {
		var (
			r        Record
			at       string
			kind     string
			ok       int
			errText  sql.NullString
			location string
		)
		if err := rows.Scan(&at, &kind, &r.Filename, &r.Size, &ok, &r.FilesAdded, &errText, &location); err != nil {
			return s
		}
		r.At, _ = time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing)
		r.Kind = Kind(kind)
		r.IntegrityOK = ok == 1
		r.Err = errText.String
		r.Location = Location(location)
		if !r.Success() && s.LastFailure == nil {
			f := r
			s.LastFailure = &f
		}
		s.Records = append(s.Records, r)
	}
	var fallbackAt string
	if err := b.db.QueryRowContext(ctx, `
		SELECT at FROM backup_records WHERE location = 'local'
		ORDER BY at DESC, id DESC LIMIT 1`).Scan(&fallbackAt); err == nil {
		if t, err := time.ParseInLocation(time.RFC3339Nano, fallbackAt, clock.Beijing); err == nil {
			s.LastLocalFallbackAt = &t
		}
	}
	var targetErr sql.NullString
	var latestLocation string
	if err := b.db.QueryRowContext(ctx, `
		SELECT error, location FROM backup_records
		ORDER BY at DESC, id DESC LIMIT 1`).Scan(&targetErr, &latestLocation); err == nil {
		errText := targetErr.String
		if errText != "" && (latestLocation == string(LocationLocal) || strings.HasPrefix(errText, "备份目标不可达")) {
			s.TargetUnreachable = true
			s.TargetUnreachableError = errText
		}
	}
	if s.LastSuccessAt == nil {
		s.Overdue = true // 从未成功备份过
	} else if d := b.clk.Now().Sub(*s.LastSuccessAt); d > OverdueAfter {
		s.Overdue = true
	}
	return s
}
