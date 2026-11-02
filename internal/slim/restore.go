package slim

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/adminops"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/packagefile"
)

var (
	// ErrRestorePackageInvalid 表示上传的瘦身包损坏、被篡改或结构不完整。
	ErrRestorePackageInvalid = errors.New("slim package invalid")
	// ErrRestoreFormatUnsupported 表示瘦身包格式版本高于当前平台支持版本。
	ErrRestoreFormatUnsupported = errors.New("slim format unsupported")
	// ErrTicketSelectionRequired 表示执行恢复时没有勾选任何工单。
	ErrTicketSelectionRequired = errors.New("slim ticket selection required")
)

const (
	maxRestoreJSONBytes        int64 = 16 << 20
	maxRestoreUncompressedSize int64 = 1 << 30
)

// RestoreTicket 是检查瘦身包时的一张候选工单。
type RestoreTicket struct {
	Number          string
	Title           string
	AttachmentCount int
	AttachmentBytes int64
	Restorable      bool
	Reason          string
}

// RestoreInspection 是一次检查瘦身包的结果。
type RestoreInspection struct {
	FormatVersion   int
	PackageName     string
	GeneratedAt     time.Time
	Tickets         []RestoreTicket
	AttachmentCount int
	AttachmentBytes int64
}

// RestoreResult 是一次按工单恢复的结果。
type RestoreResult struct {
	PackageName string
	RestoredAt  time.Time
	Restored    []RestoreTicket
	Rejected    []RestoreTicket
}

type parsedRestorePackage struct {
	Manifest  manifest
	Files     map[string]*zip.File
	Generated time.Time
	Numbers   []string
	ByNumber  map[string][]manifestFile
	Invalid   map[string]bool
}

type restoreQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// CreateUploadFile 为恢复包上传创建暂存区里的临时文件；解析与恢复都从它
// 流式读取，避免把整个 zip 解到内存。
func (s *Service) CreateUploadFile() (*os.File, error) {
	if err := os.MkdirAll(s.stageDir(), 0o755); err != nil {
		return nil, err
	}
	return os.CreateTemp(s.stageDir(), ".restore-*.zip.tmp")
}

// InspectRestore 校验上传包的格式、清单与附件校验和，并逐张判断能否恢复。
func (s *Service) InspectRestore(ctx context.Context, readerAt io.ReaderAt, size int64) (RestoreInspection, error) {
	parsed, err := parseRestorePackage(readerAt, size)
	if err != nil {
		return RestoreInspection{}, err
	}
	out := RestoreInspection{FormatVersion: parsed.Manifest.FormatVersion,
		PackageName: parsed.Manifest.PackageName, GeneratedAt: parsed.Generated,
		Tickets: []RestoreTicket{}}
	for _, number := range parsed.Numbers {
		ticket, err := s.restoreCandidate(ctx, s.db, parsed, number)
		if err != nil {
			return RestoreInspection{}, err
		}
		out.Tickets = append(out.Tickets, ticket)
		out.AttachmentCount += ticket.AttachmentCount
		out.AttachmentBytes += ticket.AttachmentBytes
	}
	return out, nil
}

// Restore 校验上传包后，在一个事务里恢复选中的工单附件；文件先按原 ID 写回，
// 再清除瘦身状态并提交数据库，失败时回滚并清理这次写入的文件。
func (s *Service) Restore(ctx context.Context, readerAt io.ReaderAt, size int64,
	selected []string, operator identity.Viewer) (RestoreResult, error) {
	s.generateMu.Lock()
	defer s.generateMu.Unlock()

	parsed, err := parseRestorePackage(readerAt, size)
	if err != nil {
		return RestoreResult{}, err
	}
	want := make(map[string]bool, len(selected))
	for _, number := range selected {
		if number = strings.TrimSpace(number); number != "" {
			want[number] = true
		}
	}
	if len(want) == 0 {
		// spec 故事 27：按工单逐张选择恢复；空选择不是「恢复整包」。
		return RestoreResult{}, ErrTicketSelectionRequired
	}

	now := s.clk.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RestoreResult{}, err
	}
	defer tx.Rollback()
	result := RestoreResult{PackageName: parsed.Manifest.PackageName, RestoredAt: now,
		Restored: []RestoreTicket{}, Rejected: []RestoreTicket{}}
	written := []string{}
	restoredNumbers := []string{}
	for _, number := range parsed.Numbers {
		if !want[number] {
			continue
		}
		candidate, err := s.restoreCandidate(ctx, tx, parsed, number)
		if err != nil {
			removeRestoreFiles(written)
			return RestoreResult{}, err
		}
		if !candidate.Restorable {
			result.Rejected = append(result.Rejected, candidate)
			continue
		}
		for _, file := range parsed.ByNumber[number] {
			id, _ := attachmentNumericID(file.AttachmentID)
			target := filepath.Join(s.attachmentsDir, strconv.FormatInt(id, 10))
			if err := writeRestoreAttachment(parsed.Files[file.Path], target); err != nil {
				removeRestoreFiles(written)
				return RestoreResult{}, err
			}
			written = append(written, target)
			res, err := tx.ExecContext(ctx, `
				UPDATE attachments
				SET slim_removed_at = NULL, slim_package_name = ''
				WHERE id = ? AND ticket_seq = (SELECT seq FROM tickets WHERE number = ?)
				  AND filename = ? AND size = ? AND state = 'attached' AND draft_id IS NULL
				  AND slim_removed_at IS NOT NULL AND slim_package_name = ?`,
				id, file.TicketNumber, file.Filename, file.Size, parsed.Manifest.PackageName)
			if err != nil {
				removeRestoreFiles(written)
				return RestoreResult{}, err
			}
			affected, err := res.RowsAffected()
			if err != nil || affected != 1 {
				removeRestoreFiles(written)
				return RestoreResult{}, fmt.Errorf("%w：%s", ErrPackageStale, file.AttachmentID)
			}
		}
		candidate.Restorable = true
		result.Restored = append(result.Restored, candidate)
		restoredNumbers = append(restoredNumbers, candidate.Number)
	}
	for number := range want {
		if _, exists := parsed.ByNumber[number]; !exists {
			result.Rejected = append(result.Rejected, RestoreTicket{
				Number: number, Restorable: false, Reason: "瘦身包里没有这张工单"})
		}
	}
	if len(result.Restored) > 0 {
		if err := adminops.InsertTx(ctx, tx, "slim_restore", operator.Member.ID, now,
			restoredNumbers, parsed.Manifest.PackageName); err != nil {
			removeRestoreFiles(written)
			return RestoreResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		removeRestoreFiles(written)
		return RestoreResult{}, err
	}
	return result, nil
}

func parseRestorePackage(readerAt io.ReaderAt, size int64) (*parsedRestorePackage, error) {
	zr, err := zip.NewReader(readerAt, size)
	if err != nil {
		return nil, fmt.Errorf("%w：不是合法 zip：%v", ErrRestorePackageInvalid, err)
	}
	entries := make(map[string]*zip.File, len(zr.File))
	var manifestEntry *zip.File
	for _, entry := range zr.File {
		if err := safeRestoreEntryName(entry.Name); err != nil {
			return nil, err
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if _, exists := entries[entry.Name]; exists {
			return nil, fmt.Errorf("%w：路径 %s 重复", ErrRestorePackageInvalid, entry.Name)
		}
		if entry.Name == "manifest.json" {
			manifestEntry = entry
		}
		entries[entry.Name] = entry
	}
	if manifestEntry == nil {
		return nil, fmt.Errorf("%w：缺少 manifest.json", ErrRestorePackageInvalid)
	}
	manifestBody, err := readBoundedRestoreEntry(manifestEntry, maxRestoreJSONBytes)
	if err != nil {
		return nil, err
	}
	var m manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return nil, fmt.Errorf("%w：清单不是合法 JSON：%v", ErrRestorePackageInvalid, err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("%w：清单后有多余内容", ErrRestorePackageInvalid)
	}
	if m.FormatVersion < 1 {
		return nil, fmt.Errorf("%w：格式版本 %d 无效", ErrRestorePackageInvalid, m.FormatVersion)
	}
	if m.FormatVersion > FormatVersion {
		return nil, fmt.Errorf("%w：包格式版本 %d 高于当前支持的 %d",
			ErrRestoreFormatUnsupported, m.FormatVersion, FormatVersion)
	}
	if m.PackageName == "" || filepath.Base(m.PackageName) != m.PackageName ||
		!strings.HasSuffix(m.PackageName, ".zip") || len(m.Files) == 0 {
		return nil, fmt.Errorf("%w：清单内容不完整", ErrRestorePackageInvalid)
	}
	if m.PlatformVersion == "" || m.Operator.Username == "" || m.Operator.Name == "" {
		return nil, fmt.Errorf("%w：清单缺少平台版本或操作人", ErrRestorePackageInvalid)
	}
	generated, err := time.ParseInLocation(time.RFC3339Nano, m.GeneratedAt, clock.Beijing)
	if err != nil {
		return nil, fmt.Errorf("%w：生成时间无效：%v", ErrRestorePackageInvalid, err)
	}

	listed := make(map[string]manifestFile, len(m.Files))
	byNumber := map[string][]manifestFile{}
	invalid := map[string]bool{}
	totalSize := int64(0)
	seenIDs := map[int64]string{}
	for _, file := range m.Files {
		if err := validateRestoreManifestFile(file); err != nil {
			return nil, err
		}
		if _, exists := listed[file.Path]; exists {
			return nil, fmt.Errorf("%w：清单路径 %s 重复", ErrRestorePackageInvalid, file.Path)
		}
		id, _ := attachmentNumericID(file.AttachmentID)
		if owner, exists := seenIDs[id]; exists {
			return nil, fmt.Errorf("%w：附件 %s 同时属于 %s 和 %s",
				ErrRestorePackageInvalid, file.AttachmentID, owner, file.TicketNumber)
		}
		seenIDs[id] = file.TicketNumber
		listed[file.Path] = file
		byNumber[file.TicketNumber] = append(byNumber[file.TicketNumber], file)
		if file.Size < 0 || file.Size > maxRestoreUncompressedSize-totalSize {
			return nil, fmt.Errorf("%w：清单声明的解压总量超过上限 %d 字节",
				ErrRestorePackageInvalid, maxRestoreUncompressedSize)
		}
		totalSize += file.Size
	}
	if len(listed)+1 != len(entries) {
		return nil, fmt.Errorf("%w：清单列出 %d 个文件，包里有 %d 个", ErrRestorePackageInvalid,
			len(listed), len(entries)-1)
	}
	for name, entry := range entries {
		if name == "manifest.json" {
			continue
		}
		file, ok := listed[name]
		if !ok {
			return nil, fmt.Errorf("%w：包里的 %s 不在清单中", ErrRestorePackageInvalid, name)
		}
		if err := verifyRestoreEntry(entry, file); err != nil {
			invalid[file.TicketNumber] = true
		}
	}
	if m.AttachmentCount != len(m.Files) || m.AttachmentBytes != totalSize {
		return nil, fmt.Errorf("%w：清单附件汇总与文件列表不一致", ErrRestorePackageInvalid)
	}

	numbers := make([]string, 0, len(byNumber))
	for number := range byNumber {
		numbers = append(numbers, number)
	}
	sort.Strings(numbers)
	return &parsedRestorePackage{Manifest: m, Files: entries, Generated: generated,
		Numbers: numbers, ByNumber: byNumber, Invalid: invalid}, nil
}

func (s *Service) restoreCandidate(ctx context.Context, q restoreQueryer,
	pkg *parsedRestorePackage, number string) (RestoreTicket, error) {
	files := pkg.ByNumber[number]
	ticket := RestoreTicket{Number: number, AttachmentCount: len(files)}
	for _, file := range files {
		ticket.AttachmentBytes += file.Size
	}
	reason := ""
	for _, file := range files {
		if pkg.Invalid[number] {
			reason = firstReason(reason, "包内附件哈希或大小不符")
			continue
		}
		why, err := s.restoreFileReason(ctx, q, pkg.Manifest.PackageName, file)
		if err != nil {
			return RestoreTicket{}, err
		}
		if why != "" && ticket.Title == "" {
			ticket.Title = s.restoreTicketTitle(ctx, q, number)
		}
		reason = firstReason(reason, why)
	}
	if ticket.Title == "" {
		ticket.Title = s.restoreTicketTitle(ctx, q, number)
	}
	ticket.Restorable, ticket.Reason = reason == "", reason
	return ticket, nil
}

func (s *Service) restoreFileReason(ctx context.Context, q restoreQueryer,
	packageName string, file manifestFile) (string, error) {
	id, err := attachmentNumericID(file.AttachmentID)
	if err != nil || id <= 0 {
		return "瘦身包里的附件 ID 无效", nil
	}
	var (
		attID       int64
		filename    string
		size        int64
		attachedTo  string
		state       string
		draftID     sql.NullInt64
		slimmedAt   sql.NullString
		slimPackage sql.NullString
		number      sql.NullString
		title       sql.NullString
	)
	err = q.QueryRowContext(ctx, `
		SELECT a.id, a.filename, a.size, COALESCE(a.attached_to, ''), a.state,
		       a.draft_id, a.slim_removed_at, a.slim_package_name, t.number, t.title
		FROM attachments a LEFT JOIN tickets t ON t.seq = a.ticket_seq
		WHERE a.id = ?`, id).
		Scan(&attID, &filename, &size, &attachedTo, &state, &draftID, &slimmedAt,
			&slimPackage, &number, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return "工单已不在平台", nil
	}
	if err != nil {
		return "", err
	}
	if !number.Valid || number.String != file.TicketNumber {
		return "工单已不在平台", nil
	}
	if filename != file.Filename || size != file.Size || attachedTo != file.AttachedTo ||
		state != "attached" || draftID.Valid {
		return "附件与瘦身包清单不匹配", nil
	}
	path := filepath.Join(s.attachmentsDir, strconv.FormatInt(id, 10))
	if _, statErr := os.Stat(path); statErr == nil {
		return "附件已在服务器上", nil
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	if !slimmedAt.Valid {
		return "附件不在服务器上，也没有这个包的瘦身标记", nil
	}
	if !slimPackage.Valid || slimPackage.String != packageName {
		return "附件不由这个瘦身包移出", nil
	}
	return "", nil
}

func (s *Service) restoreTicketTitle(ctx context.Context, q restoreQueryer, number string) string {
	var title string
	if err := q.QueryRowContext(ctx, `SELECT title FROM tickets WHERE number = ?`, number).Scan(&title); err != nil {
		return ""
	}
	return title
}

func validateRestoreManifestFile(file manifestFile) error {
	if err := safeRestoreEntryName(file.Path); err != nil {
		return err
	}
	parts := strings.Split(file.Path, "/")
	if len(parts) != 5 || parts[0] != "tickets" || parts[2] != "attachments" ||
		parts[1] == "" || parts[4] == "" {
		return fmt.Errorf("%w：文件路径 %q 不是瘦身包附件路径", ErrRestorePackageInvalid, file.Path)
	}
	if parts[3] != file.AttachmentID || !strings.HasPrefix(file.AttachmentID, "att_") {
		return fmt.Errorf("%w：%s 的路径与附件 ID 不匹配", ErrRestorePackageInvalid, file.Path)
	}
	id, err := attachmentNumericID(file.AttachmentID)
	if err != nil || id <= 0 {
		return fmt.Errorf("%w：附件 ID %q 无效", ErrRestorePackageInvalid, file.AttachmentID)
	}
	if parts[4] != file.Filename || file.TicketNumber == "" || file.Filename == "" || file.AttachedTo == "" {
		return fmt.Errorf("%w：%s 的清单归属不完整", ErrRestorePackageInvalid, file.Path)
	}
	if len(file.SHA256) != sha256.Size*2 {
		return fmt.Errorf("%w：%s 的 SHA-256 长度不对", ErrRestorePackageInvalid, file.Path)
	}
	if _, err := hex.DecodeString(file.SHA256); err != nil {
		return fmt.Errorf("%w：%s 的 SHA-256 不是十六进制", ErrRestorePackageInvalid, file.Path)
	}
	return nil
}

func safeRestoreEntryName(name string) error {
	if name == "" {
		return fmt.Errorf("%w：包内路径为空", ErrRestorePackageInvalid)
	}
	clean, err := packagefile.SafePath(name)
	if err != nil || clean != name {
		return fmt.Errorf("%w：文件路径 %q 不安全", ErrRestorePackageInvalid, name)
	}
	return nil
}

func readBoundedRestoreEntry(entry *zip.File, limit int64) ([]byte, error) {
	if entry == nil {
		return nil, fmt.Errorf("%w：缺少 zip 条目", ErrRestorePackageInvalid)
	}
	rc, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("%w：读取 %s 失败：%v", ErrRestorePackageInvalid, entry.Name, err)
	}
	body, err := io.ReadAll(io.LimitReader(rc, limit+1))
	closeErr := rc.Close()
	if err != nil {
		return nil, fmt.Errorf("%w：读取 %s 失败：%v", ErrRestorePackageInvalid, entry.Name, err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("%w：关闭 %s 失败：%v", ErrRestorePackageInvalid, entry.Name, closeErr)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w：%s 超过单条目读取上限 %d 字节",
			ErrRestorePackageInvalid, entry.Name, limit)
	}
	return body, nil
}

func verifyRestoreEntry(entry *zip.File, want manifestFile) error {
	rc, err := entry.Open()
	if err != nil {
		return fmt.Errorf("%w：读取 %s 失败：%v", ErrRestorePackageInvalid, entry.Name, err)
	}
	sum := sha256.New()
	written, copyErr := io.Copy(sum, io.LimitReader(rc, want.Size+1))
	closeErr := rc.Close()
	if copyErr != nil {
		return fmt.Errorf("%w：读取 %s 失败：%v", ErrRestorePackageInvalid, entry.Name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("%w：关闭 %s 失败：%v", ErrRestorePackageInvalid, entry.Name, closeErr)
	}
	if written != want.Size || !strings.EqualFold(hex.EncodeToString(sum.Sum(nil)), want.SHA256) {
		return fmt.Errorf("%w：%s 大小或 SHA-256 不符", ErrRestorePackageInvalid, entry.Name)
	}
	return nil
}

func firstReason(current, next string) string {
	if current != "" {
		return current
	}
	return next
}

func writeRestoreAttachment(entry *zip.File, target string) error {
	rc, err := entry.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(dst, rc)
	syncErr := dst.Sync()
	closeErr := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if entry.FileInfo().Size() != 0 && written != entry.FileInfo().Size() {
		return fmt.Errorf("附件 %s 写回大小不符：%d/%d", entry.Name, written, entry.FileInfo().Size())
	}
	return nil
}

func removeRestoreFiles(files []string) {
	for _, path := range files {
		_ = os.Remove(path)
	}
}
