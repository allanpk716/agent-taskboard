package slim

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/adminops"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/packagefile"
	"github.com/allanpk716/agent-taskboard/internal/version"
)

const (
	FormatVersion = 1
	Retention     = 7 * 24 * time.Hour
)

var (
	ErrPackageNotFound    = errors.New("slim package not found")
	ErrGenerateInProgress = errors.New("slim package generation in progress")
)

type PackageInfo struct {
	Name                   string
	TicketNumbers          []string
	TicketCount            int
	AttachmentCount        int
	AttachmentBytes        int64
	GeneratedAt            time.Time
	ExpiresAt              time.Time
	OperatorName           string
	RemovedAttachmentCount int
}

type manifest struct {
	FormatVersion   int            `json:"format_version"`
	PlatformVersion string         `json:"platform_version"`
	PackageName     string         `json:"package_name"`
	GeneratedAt     string         `json:"generated_at"`
	Operator        manifestActor  `json:"operator"`
	AttachmentCount int            `json:"attachment_count"`
	AttachmentBytes int64          `json:"attachment_bytes"`
	Files           []manifestFile `json:"files"`
}

type manifestActor struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

type manifestFile struct {
	packagefile.ManifestFile
	TicketNumber string `json:"ticket_number"`
	AttachedTo   string `json:"attached_to"`
	AttachmentID string `json:"attachment_id"`
	Filename     string `json:"filename"`
}

func (s *Service) stageDir() string { return filepath.Join(s.dataDir, "slim-packages") }

func (s *Service) Generate(ctx context.Context, ticketNumbers []string, closedMonths int,
	operator identity.Viewer) (PackageInfo, error) {
	if !s.generateMu.TryLock() {
		return PackageInfo{}, ErrGenerateInProgress
	}
	defer s.generateMu.Unlock()
	preview, err := s.Preview(ctx, ticketNumbers, closedMonths)
	if err != nil {
		return PackageInfo{}, err
	}
	if err := os.MkdirAll(s.stageDir(), 0o755); err != nil {
		return PackageInfo{}, err
	}
	now := s.clk.Now()
	name, err := newPackageName(now)
	if err != nil {
		return PackageInfo{}, err
	}
	temp, err := os.CreateTemp(s.stageDir(), ".generating-*.zip.tmp")
	if err != nil {
		return PackageInfo{}, err
	}
	tempName := temp.Name()
	defer func() {
		if temp != nil {
			_ = temp.Close()
		}
		if tempName != "" {
			_ = os.Remove(tempName)
		}
	}()

	files := make([]manifestFile, 0)
	zw := zip.NewWriter(temp)
	for _, ticket := range preview.Tickets {
		for _, att := range ticket.attachments {
			path, err := packagefile.SafePath(ticketAttachmentPath(ticket.Number, att))
			if err != nil {
				_ = zw.Close()
				return PackageInfo{}, err
			}
			src, actualSize, err := s.atts.Storage().Open(ctx, att.ID)
			if err != nil {
				_ = zw.Close()
				return PackageInfo{}, fmt.Errorf("读取附件 %s 失败：%w", att.Ref, err)
			}
			if actualSize != att.Size {
				_ = src.Close()
				_ = zw.Close()
				return PackageInfo{}, fmt.Errorf("附件 %s 大小不符：数据库 %d 字节，存储 %d 字节", att.Ref, att.Size, actualSize)
			}
			entry, err := packagefile.AddStreamEntry(zw, path, src, att.Size)
			if err != nil {
				_ = src.Close()
				_ = zw.Close()
				return PackageInfo{}, err
			}
			if err := src.Close(); err != nil {
				_ = zw.Close()
				return PackageInfo{}, err
			}
			files = append(files, manifestFile{ManifestFile: entry, TicketNumber: ticket.Number,
				AttachedTo: att.AttachedTo, AttachmentID: att.Ref, Filename: att.Filename})
		}
	}
	m := manifest{FormatVersion: FormatVersion, PlatformVersion: version.Server,
		PackageName: name, GeneratedAt: now.Format(time.RFC3339Nano),
		Operator: manifestActor{ID: operator.Member.ID, Username: operator.Member.Username,
			Name: operator.Member.Name},
		AttachmentCount: preview.AttachmentCount(), AttachmentBytes: preview.AttachmentBytes(),
		Files: files}
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		_ = zw.Close()
		return PackageInfo{}, err
	}
	if err := packagefile.AddEntry(zw, "manifest.json", append(encoded, '\n')); err != nil {
		_ = zw.Close()
		return PackageInfo{}, err
	}
	if err := zw.Close(); err != nil {
		return PackageInfo{}, err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		temp = nil
		return PackageInfo{}, err
	}
	if err := temp.Close(); err != nil {
		temp = nil
		return PackageInfo{}, err
	}
	temp = nil
	final := filepath.Join(s.stageDir(), name)
	if err := os.Rename(tempName, final); err != nil {
		return PackageInfo{}, err
	}
	tempName = ""

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		_ = os.Remove(final)
		return PackageInfo{}, err
	}
	if err := adminops.InsertTx(ctx, tx, "slim_generate", operator.Member.ID, now,
		numbersOf(preview.Tickets), name); err != nil {
		_ = tx.Rollback()
		_ = os.Remove(final)
		return PackageInfo{}, err
	}
	if err := tx.Commit(); err != nil {
		_ = os.Remove(final)
		return PackageInfo{}, err
	}
	return PackageInfo{Name: name, TicketNumbers: numbersOf(preview.Tickets),
		TicketCount: preview.TicketCount(), AttachmentCount: preview.AttachmentCount(),
		AttachmentBytes: preview.AttachmentBytes(), GeneratedAt: now, ExpiresAt: now.Add(Retention),
		OperatorName: operator.Member.Name}, nil
}

func (s *Service) List(ctx context.Context) ([]PackageInfo, error) {
	if err := s.CleanupExpired(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.stageDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []PackageInfo{}, nil
		}
		return nil, err
	}
	removedCounts, err := s.removedCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := []PackageInfo{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		info, err := s.infoFromZip(entry.Name())
		if errors.Is(err, ErrPackageNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if s.clk.Now().After(info.ExpiresAt) {
			continue
		}
		info.RemovedAttachmentCount = removedCounts[info.Name]
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GeneratedAt.After(out[j].GeneratedAt) })
	return out, nil
}

func (s *Service) removedCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT slim_package_name, COUNT(*) FROM attachments WHERE slim_removed_at IS NOT NULL GROUP BY slim_package_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			return nil, err
		}
		out[name] = count
	}
	return out, rows.Err()
}

func (s *Service) Open(name string) (*os.File, PackageInfo, error) {
	if name == "" || filepath.Base(name) != name || !strings.HasSuffix(name, ".zip") ||
		strings.HasPrefix(name, ".") {
		return nil, PackageInfo{}, ErrPackageNotFound
	}
	path := filepath.Join(s.stageDir(), name)
	file, err := os.Open(path)
	if err != nil {
		return nil, PackageInfo{}, ErrPackageNotFound
	}
	info, err := s.infoFromZip(name)
	if err != nil {
		_ = file.Close()
		return nil, PackageInfo{}, err
	}
	if s.clk.Now().After(info.ExpiresAt) {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, PackageInfo{}, ErrPackageNotFound
	}
	return file, info, nil
}

func (s *Service) CleanupExpired() error {
	if !s.generateMu.TryLock() {
		return nil
	}
	defer s.generateMu.Unlock()
	entries, err := os.ReadDir(s.stageDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(s.stageDir(), name)
		if strings.HasPrefix(name, ".generating-") && strings.HasSuffix(name, ".tmp") {
			_ = os.Remove(path)
			continue
		}
		if strings.HasPrefix(name, ".restore-") && strings.HasSuffix(name, ".zip.tmp") {
			// 恢复上传的临时文件正常会在请求结束时删除；这里兜底清理
			// 因中断泄漏的旧文件，按 mtime 超过保留期判定。
			s.removeIfOlderThanRetention(entry, path)
			continue
		}
		if !strings.HasSuffix(name, ".zip") {
			continue
		}
		info, err := s.infoFromZip(name)
		if errors.Is(err, ErrPackageNotFound) {
			// 无法解析的暂存包不再永久跳过：按 mtime 超过保留期删除。
			s.removeIfOlderThanRetention(entry, path)
			continue
		}
		if err != nil || s.clk.Now().After(info.ExpiresAt) {
			_ = os.Remove(path)
		}
	}
	return nil
}

func (s *Service) removeIfOlderThanRetention(entry os.DirEntry, path string) {
	info, err := entry.Info()
	if err != nil {
		return
	}
	if !s.clk.Now().Add(-Retention).Before(info.ModTime()) {
		_ = os.Remove(path)
	}
}

func (s *Service) infoFromZip(name string) (PackageInfo, error) {
	file, err := os.Open(filepath.Join(s.stageDir(), name))
	if err != nil {
		return PackageInfo{}, ErrPackageNotFound
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return PackageInfo{}, ErrPackageNotFound
	}
	zr, err := zip.NewReader(file, info.Size())
	if err != nil {
		return PackageInfo{}, ErrPackageNotFound
	}
	rc, err := zr.Open("manifest.json")
	if err != nil {
		return PackageInfo{}, ErrPackageNotFound
	}
	defer rc.Close()
	var m manifest
	if err := json.NewDecoder(rc).Decode(&m); err != nil || m.FormatVersion != FormatVersion {
		return PackageInfo{}, ErrPackageNotFound
	}
	generated, err := time.ParseInLocation(time.RFC3339Nano, m.GeneratedAt, clock.Beijing)
	if err != nil || m.PackageName != name {
		return PackageInfo{}, ErrPackageNotFound
	}
	numbers := make([]string, 0)
	seen := map[string]bool{}
	var bytes int64
	for _, file := range m.Files {
		if !seen[file.TicketNumber] {
			seen[file.TicketNumber] = true
			numbers = append(numbers, file.TicketNumber)
		}
		bytes += file.Size
	}
	return PackageInfo{Name: name, TicketNumbers: numbers, TicketCount: len(numbers),
		AttachmentCount: len(m.Files), AttachmentBytes: bytes, GeneratedAt: generated,
		ExpiresAt: generated.Add(Retention), OperatorName: m.Operator.Name}, nil
}

func ticketAttachmentPath(number string, att Attachment) string {
	return "tickets/" + number + "/attachments/" + att.Ref + "/" + att.Filename
}

func newPackageName(now time.Time) (string, error) {
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("slim-%d-%s.zip", now.UnixNano(), hex.EncodeToString(buf)), nil
}

func numbersOf(tickets []TicketSummary) []string {
	out := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		out = append(out, ticket.Number)
	}
	return out
}
