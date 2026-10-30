// Package storage 提供管理页「存储统计」（#234）：数据库、附件目录、备份目录
// 三类空间占用，以及按关闭月份汇总的「已关闭满 N 个月」可归档工单。
// 统计只读：不改数据库 schema，不移动、不删除任何文件。
package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
)

// MonthStat 是一个关闭月份（北京时间 YYYY-MM）的可归档汇总。
type MonthStat struct {
	Month           string
	TicketCount     int64
	AttachmentBytes int64
}

// Stats 是 GET /api/v1/admin/storage 需要的全部统计结果。
type Stats struct {
	DatabaseBytes    int64
	AttachmentsBytes int64
	BackupsBytes     int64
	ClosedMonths     int
	Cutoff           time.Time
	Archivable       []MonthStat
}

// Service 汇总存储统计。目录都由装配处传入：附件目录来自附件模块，
// 备份目录来自备份模块的 TargetDir（配置 [backup] 留空时是 data_dir/backups），
// 统计口径因此与实际生效的配置一致。
type Service struct {
	db             *sql.DB
	clk            clock.Clock
	dataDir        string
	attachmentsDir string
	backupsDir     string
}

// NewService 建存储统计服务。
func NewService(db *sql.DB, dataDir, attachmentsDir, backupsDir string, c clock.Clock) *Service {
	return &Service{db: db, clk: c, dataDir: dataDir, attachmentsDir: attachmentsDir, backupsDir: backupsDir}
}

// Stats 返回三类占用与可归档工单汇总。closedMonths 是「已关闭满 N 个月」的 N
// （1–120，HTTP 层负责校验）。备份目录不可读时 backups_bytes 为 0，不让接口报错。
func (s *Service) Stats(ctx context.Context, closedMonths int) (Stats, error) {
	cutoff := s.clk.Now().AddDate(0, -closedMonths, 0)
	archivable, err := s.ArchivableByMonth(ctx, cutoff)
	if err != nil {
		return Stats{}, err
	}
	return Stats{
		DatabaseBytes:    DatabaseBytes(s.dataDir),
		AttachmentsBytes: DirBytes(s.attachmentsDir, s.backupsDir),
		BackupsBytes:     DirBytes(s.backupsDir, s.attachmentsDir),
		ClosedMonths:     closedMonths,
		Cutoff:           cutoff,
		Archivable:       archivable,
	}, nil
}

// ArchivableByMonth 找出当前状态为已关闭、且最近一次关闭时间 <= cutoff 的工单，
// 按关闭时间的北京时间月份分组（新月份在前）。重新开启后又关闭的工单取
// 时间线上最近一次 closed 事件；当前未关闭的工单无论多老都不算。
func (s *Service) ArchivableByMonth(ctx context.Context, cutoff time.Time) ([]MonthStat, error) {
	// SQLite 连接池只有一条写连接：先读完关单，再查附件合计，
	// 两条查询不能同时占着连接。
	type closedTicket struct {
		seq      int64
		closedAt sql.NullString // 理论上关单与 closed 事件同事务写入；缺事件时跳过而不是整页报错
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.seq, (
			SELECT e.at FROM timeline_events e
			WHERE e.ticket_seq = t.seq AND e.type = 'closed'
			ORDER BY e.seq DESC LIMIT 1
		) AS closed_at
		FROM tickets t
		WHERE t.status = 'closed'`)
	if err != nil {
		return nil, err
	}
	var closed []closedTicket
	for rows.Next() {
		var ct closedTicket
		if err := rows.Scan(&ct.seq, &ct.closedAt); err != nil {
			rows.Close()
			return nil, err
		}
		closed = append(closed, ct)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	attachmentBytes, err := s.attachmentBytesByTicket(ctx)
	if err != nil {
		return nil, err
	}

	byMonth := map[string]*MonthStat{}
	for _, ct := range closed {
		if !ct.closedAt.Valid {
			continue
		}
		at, err := time.ParseInLocation(time.RFC3339Nano, ct.closedAt.String, clock.Beijing)
		if err != nil || at.After(cutoff) {
			continue // 关闭时间晚于 cutoff：还没满 N 个月
		}
		month := at.In(clock.Beijing).Format("2006-01")
		stat := byMonth[month]
		if stat == nil {
			stat = &MonthStat{Month: month}
			byMonth[month] = stat
		}
		stat.TicketCount++
		stat.AttachmentBytes += attachmentBytes[ct.seq]
	}

	months := make([]MonthStat, 0, len(byMonth))
	for _, stat := range byMonth {
		months = append(months, *stat)
	}
	sort.Slice(months, func(i, j int) bool { return months[i].Month > months[j].Month })
	return months, nil
}

// attachmentBytesByTicket 汇总每张工单的附件字节（含待用附件，与附件配额同口径）。
// 附件的存储文件按 id 落盘（data_dir/attachments/<id>），一行就是一个存储文件；
// 说明各版本与回复里的引用只存 id 列表，因此按行求和天然按存储文件去重。
func (s *Service) attachmentBytesByTicket(ctx context.Context) (map[int64]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ticket_seq, COALESCE(SUM(size), 0)
		FROM attachments WHERE ticket_seq IS NOT NULL
		GROUP BY ticket_seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sizes := map[int64]int64{}
	for rows.Next() {
		var seq, total int64
		if err := rows.Scan(&seq, &total); err != nil {
			return nil, err
		}
		sizes[seq] = total
	}
	return sizes, rows.Err()
}

// DirBytes 递归统计目录下所有普通文件的大小之和。目录不存在或不可读按 0；
// excludes 里列出的目录（及其子树）不计入——附件目录与备份目录互相嵌套时
// 各计各的，不重复计入。符号链接不是普通文件，跳过。
func DirBytes(dir string, excludes ...string) int64 {
	skip := make([]string, 0, len(excludes))
	for _, ex := range excludes {
		if ex == "" {
			continue
		}
		if abs, err := filepath.Abs(ex); err == nil {
			ex = abs
		}
		skip = append(skip, filepath.Clean(ex))
	}
	var total int64
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 不可读的子树按 0 处理，不影响其余部分
		}
		if d.IsDir() {
			if path == dir {
				return nil
			}
			if abs, err := filepath.Abs(path); err == nil {
				for _, ex := range skip {
					if abs == ex || strings.HasPrefix(abs+string(filepath.Separator), ex+string(filepath.Separator)) {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// DatabaseBytes 统计 dataDir 下 taskboard.db、taskboard.db-wal 与
// taskboard.db-shm 三个文件的大小之和（缺的按 0）。
func DatabaseBytes(dataDir string) int64 {
	var total int64
	for _, name := range []string{"taskboard.db", "taskboard.db-wal", "taskboard.db-shm"} {
		if info, err := os.Stat(filepath.Join(dataDir, name)); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total
}
