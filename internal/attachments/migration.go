package attachments

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/crc64"
	"io"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// MigrationOptions 控制一次离线附件迁移。
type MigrationOptions struct {
	// Target 是迁移完成后要写入数据库的实际后端标记。
	Target BackendMarker
	// DataDir 是目标为 local 时的数据目录，用于检查目标分区剩余空间。
	DataDir string
	// FreeSpace 供测试注入磁盘剩余空间检测；生产路径留空使用系统实现。
	FreeSpace func(path string) (uint64, error)
	// AcceptMissing 表示源端缺失的附件按缺失清单切换；复制失败仍会阻止切换。
	AcceptMissing bool
	// DryRun 只统计迁移计划，不写目标对象，也不改数据库标记。
	DryRun bool
	// Progress 在实际迁移时每处理 50 个附件调用一次。多个迁移 worker 会
	// 并发调用它，实现必须自己保证线程安全。
	Progress func(done, total int)
}

// MigrationResult 是迁移汇总；清单里的 id 按升序排列。
type MigrationResult struct {
	Copied        int
	Planned       int
	Skipped       int
	Failed        int
	Missing       int
	TotalSize     int64
	DryRun        bool
	MissingIDs    []int64
	FailedIDs     []int64
	SourceOnlyIDs []int64
}

// migrationWorkerCount 是实际迁移时的并发 worker 数。
const migrationWorkerCount = 4

// crc64Table 是附件迁移校验用的 CRC64（ECMA-182）表。
var crc64Table = crc64.MakeTable(crc64.ECMA)

type migrationRecord struct {
	id       int64
	filename string
	size     int64
}

// EnsureServiceStopped 按配置端口确认 taskboard 服务不在运行；离线命令
// 操作数据库和附件对象前先调用它。
func EnsureServiceStopped(port int) error {
	if port <= 0 {
		return nil
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond)
	if err != nil {
		return nil
	}
	_ = conn.Close()
	return fmt.Errorf("127.0.0.1:%d 仍有服务在监听：先停止 taskboard 服务（如 nssm stop <服务名>）再执行离线命令", port)
}

// MigrateAttachments 把附件对象从 source 复制到 target，全部校验通过后
// 在一个事务里写入 target marker。源端对象保留不删。
func MigrateAttachments(ctx context.Context, db *sql.DB, source, target Storage, opts MigrationOptions) (MigrationResult, error) {
	var res MigrationResult
	if opts.Target.Backend != AttachmentBackendLocal && opts.Target.Backend != AttachmentBackendOSS {
		return res, fmt.Errorf("迁移目标后端 %q 无效（只能是 local 或 oss）", opts.Target.Backend)
	}
	records, err := migrationRecords(ctx, db)
	if err != nil {
		return res, err
	}
	res.DryRun = opts.DryRun
	for _, record := range records {
		res.TotalSize += record.size
	}
	res.SourceOnlyIDs, err = sourceOnlyIDs(ctx, source, records)
	if err != nil {
		return res, err
	}
	if opts.DryRun {
		for _, record := range records {
			needsCopy, err := targetAlreadyMatches(ctx, source, target, record)
			switch {
			case errors.Is(err, ErrObjectNotFound):
				res.Missing++
				res.MissingIDs = append(res.MissingIDs, record.id)
			case err != nil:
				res.Failed++
				res.FailedIDs = append(res.FailedIDs, record.id)
			case needsCopy:
				res.Planned++
			default:
				res.Skipped++
			}
		}
		if res.Failed != 0 || (res.Missing != 0 && !opts.AcceptMissing) {
			return res, fmt.Errorf("附件迁移演练发现问题：失败 %d 个、源端缺失 %d 个", res.Failed, res.Missing)
		}
		return res, nil
	}
	if opts.Target.Backend == AttachmentBackendLocal {
		if err := checkLocalFreeSpace(opts, res.TotalSize); err != nil {
			return res, err
		}
	}
	type migrationOutcome struct {
		record migrationRecord
		copied bool
		err    error
	}
	jobs := make(chan migrationRecord)
	outcomes := make(chan migrationOutcome, len(records))
	var workers sync.WaitGroup
	var processed int64
	for i := 0; i < migrationWorkerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for record := range jobs {
				copied, err := migrateAttachment(ctx, record, source, target)
				outcomes <- migrationOutcome{record: record, copied: copied, err: err}
				done := int(atomic.AddInt64(&processed, 1))
				if done%50 == 0 && opts.Progress != nil {
					opts.Progress(done, len(records))
				}
			}
		}()
	}
	for _, record := range records {
		jobs <- record
	}
	close(jobs)
	workers.Wait()
	close(outcomes)
	for outcome := range outcomes {
		switch {
		case outcome.err == nil && outcome.copied:
			res.Copied++
		case outcome.err == nil:
			res.Skipped++
		case errors.Is(outcome.err, ErrObjectNotFound):
			res.Missing++
			res.MissingIDs = append(res.MissingIDs, outcome.record.id)
		default:
			res.Failed++
			res.FailedIDs = append(res.FailedIDs, outcome.record.id)
		}
	}
	sort.Slice(res.MissingIDs, func(i, j int) bool { return res.MissingIDs[i] < res.MissingIDs[j] })
	sort.Slice(res.FailedIDs, func(i, j int) bool { return res.FailedIDs[i] < res.FailedIDs[j] })
	if res.Failed != 0 || (res.Missing != 0 && !opts.AcceptMissing) {
		return res, fmt.Errorf("附件迁移未完成：失败 %d 个、源端缺失 %d 个", res.Failed, res.Missing)
	}
	if err := WriteBackendMarker(ctx, db, opts.Target); err != nil {
		return res, err
	}
	return res, nil
}

func sourceOnlyIDs(ctx context.Context, source Storage, records []migrationRecord) ([]int64, error) {
	known := make(map[int64]bool, len(records))
	for _, record := range records {
		known[record.id] = true
	}
	ids := []int64{}
	err := source.List(ctx, func(id int64, size int64, modTime time.Time) error {
		if !known[id] {
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("列举源端附件失败：%w", err)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// targetAlreadyMatches 读出源端对象并计算 CRC64；目标端已有同大小且校验
// 一致的对象时返回 true。dry-run 用它判断是否需要复制，实际迁移先判断、
// 不一致再走 putVerified，两处共用同一条“是否已一致”的口径。
func targetAlreadyMatches(ctx context.Context, source, target Storage, record migrationRecord) (bool, error) {
	body, size, err := source.Open(ctx, record.id)
	if err != nil {
		return false, err
	}
	hash := crc64.New(crc64Table)
	_, readErr := io.Copy(io.Discard, io.TeeReader(body, hash))
	closeErr := body.Close()
	if readErr != nil {
		return false, readErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	targetSize, exists, statErr := target.Stat(ctx, record.id)
	if statErr != nil {
		return false, statErr
	}
	if !exists || targetSize != size {
		return false, nil
	}
	targetSum, err := objectChecksum(ctx, target, record.id)
	if err != nil {
		return false, err
	}
	return targetSum == hash.Sum64(), nil
}

func checkLocalFreeSpace(opts MigrationOptions, total int64) error {
	required := total + total/10
	if required < total {
		return fmt.Errorf("磁盘剩余空间检查失败：附件总大小 %d 字节溢出", total)
	}
	freeSpace := opts.FreeSpace
	if freeSpace == nil {
		freeSpace = diskFreeSpace
	}
	path := filepath.Join(opts.DataDir, "attachments")
	free, err := freeSpace(path)
	if err != nil {
		return fmt.Errorf("读取 %s 所在分区剩余空间失败：%w", path, err)
	}
	if free < uint64(required) {
		return fmt.Errorf("磁盘剩余空间不足：%s 需要 %d 字节（含 10%% 余量），实际只有 %d 字节",
			path, required, free)
	}
	return nil
}

func migrationRecords(ctx context.Context, db *sql.DB) ([]migrationRecord, error) {
	// 瘦身移出的附件对象已删除、库记录仍在：它们不算“应存在”，不能计成
	// 源端缺失，也不需要复制。
	rows, err := db.QueryContext(ctx, `
		SELECT id, filename, size FROM attachments WHERE slim_removed_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("读取附件记录失败：%w", err)
	}
	defer rows.Close()
	records := []migrationRecord{}
	for rows.Next() {
		var record migrationRecord
		if err := rows.Scan(&record.id, &record.filename, &record.size); err != nil {
			return nil, fmt.Errorf("读取附件记录失败：%w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取附件记录失败：%w", err)
	}
	return records, nil
}

func migrateAttachment(ctx context.Context, record migrationRecord, source, target Storage) (bool, error) {
	matches, err := targetAlreadyMatches(ctx, source, target, record)
	if err != nil {
		return false, fmt.Errorf("附件 %d 迁移失败：%w", record.id, err)
	}
	if matches {
		return false, nil
	}
	if err := putVerified(ctx, record, source, target); err != nil {
		return false, fmt.Errorf("附件 %d 迁移失败：%w", record.id, err)
	}
	return true, nil
}

// putVerified 把源端对象复制到目标端，并用 CRC64 与大小双重校验。
func putVerified(ctx context.Context, record migrationRecord, source, target Storage) error {
	body, size, err := source.Open(ctx, record.id)
	if err != nil {
		return err
	}
	hash := crc64.New(crc64Table)
	putErr := target.Put(ctx, record.id, io.TeeReader(body, hash), size,
		PutMeta{ContentType: ContentTypeOf(record.filename)})
	closeErr := body.Close()
	if putErr != nil {
		return putErr
	}
	if closeErr != nil {
		return closeErr
	}
	targetSum, err := objectChecksum(ctx, target, record.id)
	if err != nil {
		return err
	}
	if targetSum != hash.Sum64() {
		return fmt.Errorf("CRC64 校验不符：源端 %d，目标端 %d", hash.Sum64(), targetSum)
	}
	targetSize, exists, statErr := target.Stat(ctx, record.id)
	if statErr != nil {
		return statErr
	}
	if !exists || targetSize != size {
		return fmt.Errorf("目标对象大小不符：应为 %d 字节，实际 %d", size, targetSize)
	}
	return nil
}

func objectChecksum(ctx context.Context, storage Storage, id int64) (uint64, error) {
	if withChecksum, ok := storage.(interface {
		ObjectChecksum(ctx context.Context, id int64) (uint64, bool, error)
	}); ok {
		sum, exists, err := withChecksum.ObjectChecksum(ctx, id)
		if err != nil {
			return 0, err
		}
		if !exists {
			return 0, ErrObjectNotFound
		}
		return sum, nil
	}
	body, _, err := storage.Open(ctx, id)
	if err != nil {
		return 0, err
	}
	defer body.Close()
	hash := crc64.New(crc64Table)
	if _, err := io.Copy(hash, body); err != nil {
		return 0, err
	}
	return hash.Sum64(), nil
}
