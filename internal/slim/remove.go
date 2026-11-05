package slim

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/adminops"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

var (
	ErrPackageStale   = errors.New("slim package stale")
	ErrAlreadyRemoved = errors.New("slim package already removed")
)

type Removal struct {
	PackageName     string
	TicketNumbers   []string
	AttachmentCount int
	AttachmentBytes int64
	RemovedAt       time.Time
	CleanupFailures int
}

type removalAttachment struct {
	Attachment
	TicketNumber string
}

type removalQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Service) Remove(ctx context.Context, packageName string, operator identity.Viewer) (Removal, error) {
	s.generateMu.Lock()
	defer s.generateMu.Unlock()

	file, pkg, err := s.Open(packageName)
	if err != nil {
		return Removal{}, err
	}
	defer file.Close()
	manifest, err := readPackageManifest(file)
	if err != nil {
		return Removal{}, err
	}
	now := s.clk.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Removal{}, err
	}
	defer tx.Rollback()
	atts, err := s.removalState(ctx, tx, manifest, pkg.GeneratedAt)
	if err != nil {
		return Removal{}, err
	}
	for _, att := range atts {
		result, err := tx.ExecContext(ctx, `
			UPDATE attachments
			SET slim_removed_at = ?, slim_package_name = ?
			WHERE id = ? AND ticket_seq = (SELECT seq FROM tickets WHERE number = ?)
			  AND filename = ? AND size = ? AND state = 'attached'
			  AND draft_id IS NULL AND slim_removed_at IS NULL`,
			now.Format(time.RFC3339Nano), pkg.Name, att.ID, att.TicketNumber, att.Filename, att.Size)
		if err != nil {
			return Removal{}, err
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return Removal{}, fmt.Errorf("%w：%s", ErrPackageStale, att.Ref)
		}
	}
	if err := adminops.InsertTx(ctx, tx, "slim_remove", operator.Member.ID, now,
		pkg.TicketNumbers, pkg.Name); err != nil {
		return Removal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Removal{}, err
	}

	freed, failures := s.cleanupFiles(ctx, atts)
	return Removal{PackageName: pkg.Name, TicketNumbers: pkg.TicketNumbers,
		AttachmentCount: len(atts), AttachmentBytes: freed, RemovedAt: now,
		CleanupFailures: failures}, nil
}

func (s *Service) removalState(ctx context.Context, q removalQueryer, m *manifest,
	generatedAt time.Time) ([]removalAttachment, error) {
	if len(m.Files) == 0 {
		return nil, fmt.Errorf("%w：瘦身包没有附件", ErrPackageStale)
	}
	removed := 0
	for _, file := range m.Files {
		var at sql.NullString
		id, err := attachmentNumericID(file.AttachmentID)
		if err == nil {
			err = q.QueryRowContext(ctx, `
				SELECT slim_removed_at FROM attachments
				WHERE id = ? AND slim_package_name = ?`, id, m.PackageName).Scan(&at)
		}
		if err == nil && at.Valid {
			removed++
		}
	}
	if removed == len(m.Files) {
		return nil, ErrAlreadyRemoved
	}

	out := make([]removalAttachment, 0, len(m.Files))
	for _, file := range m.Files {
		var (
			att       removalAttachment
			status    string
			closedAt  sql.NullString
			draftID   sql.NullInt64
			slimmedAt sql.NullString
		)
		numericID, idErr := attachmentNumericID(file.AttachmentID)
		err := q.QueryRowContext(ctx, `
			SELECT a.id, a.filename, a.size, COALESCE(a.attached_to, ''), t.number,
			       t.status, (
			           SELECT e.at FROM timeline_events e
			           WHERE e.ticket_seq = t.seq AND e.type = 'closed'
			           ORDER BY e.seq DESC LIMIT 1
			       ), a.draft_id, a.slim_removed_at
			FROM attachments a JOIN tickets t ON t.seq = a.ticket_seq
			WHERE a.id = ?`, numericID).
			Scan(&att.ID, &att.Filename, &att.Size, &att.AttachedTo, &att.TicketNumber,
				&status, &closedAt, &draftID, &slimmedAt)
		if idErr != nil || err != nil || att.TicketNumber != file.TicketNumber || att.Filename != file.Filename ||
			attachmentID(att.ID) != file.AttachmentID || att.AttachedTo != file.AttachedTo ||
			att.Size != file.Size || status != "closed" || draftID.Valid || slimmedAt.Valid {
			return nil, fmt.Errorf("%w：%s", ErrPackageStale, file.AttachmentID)
		}
		if !closedAt.Valid {
			return nil, fmt.Errorf("%w：%s", ErrPackageStale, file.TicketNumber)
		}
		at, err := time.ParseInLocation(time.RFC3339Nano, closedAt.String, clock.Beijing)
		if err != nil || at.After(generatedAt) {
			return nil, fmt.Errorf("%w：%s", ErrPackageStale, file.TicketNumber)
		}
		if size, exists := s.atts.Storage().Stat(ctx, att.ID); !exists || size != att.Size {
			return nil, fmt.Errorf("%w：%s 的服务器文件已不在", ErrPackageStale, attachmentID(att.ID))
		}
		att.Ref = file.AttachmentID
		out = append(out, att)
	}
	return out, nil
}

func (s *Service) cleanupFiles(ctx context.Context, atts []removalAttachment) (int64, int) {
	var freed int64
	failures := 0
	for _, att := range atts {
		objects := s.atts.Storage()
		if _, exists := objects.Stat(ctx, att.ID); exists {
			if err := objects.Delete(ctx, att.ID); err != nil {
				log.Printf("瘦身移出后删除附件对象 att_%d 失败：%v", att.ID, err)
				failures++
			}
		}
		_, failed := s.mirror.RemoveForCleanup(att.ID)
		failures += failed
		// 释放字节数与预览口径一致：只按附件本身大小计，不把备份镜像
		// 重复计入（镜像删除失败只进 failures）。
		freed += att.Size
	}
	return freed, failures
}

func readPackageManifest(file *os.File) (*manifest, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(file, info.Size())
	if err != nil {
		return nil, ErrPackageStale
	}
	rc, err := zr.Open("manifest.json")
	if err != nil {
		return nil, ErrPackageStale
	}
	defer rc.Close()
	var m manifest
	if err := json.NewDecoder(rc).Decode(&m); err != nil || m.FormatVersion != FormatVersion {
		return nil, ErrPackageStale
	}
	return &m, nil
}

func attachmentID(id int64) string {
	return "att_" + strconv.FormatInt(id, 10)
}

func attachmentNumericID(ref string) (int64, error) {
	return strconv.ParseInt(strings.TrimPrefix(ref, "att_"), 10, 64)
}
