package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// ErrAlreadyRemoved 表示归档包对应的工单已经移除。
var ErrAlreadyRemoved = errors.New("archive package already removed")

// Tombstone 是一张已移出平台的工单留下的线索。
type Tombstone struct {
	Seq          int64
	Number       string
	Title        string
	ClosedAt     time.Time
	ArchivedAt   time.Time
	OperatorName string
	PackageName  string
}

// Removal 是一次成功移除的外部结果。
type Removal struct {
	PackageName     string
	Tombstones      []Tombstone
	AttachmentBytes int64
	RemovedAt       time.Time
}

// TombstoneByRef 用工单编号或全局序号查墓碑；没有墓碑时返回 sql.ErrNoRows。
func (s *Service) TombstoneByRef(ctx context.Context, ref string) (Tombstone, error) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	var (
		query string
		arg   any
	)
	if strings.Contains(ref, "-") {
		query = `SELECT t.seq, t.number, t.title, t.closed_at, t.archived_at, m.name, t.package_name
			FROM archived_tickets t JOIN members m ON m.id = t.operator_member_id
			WHERE t.number = ?`
		arg = ref
	} else {
		seq, err := strconv.ParseInt(ref, 10, 64)
		if err != nil {
			return Tombstone{}, sql.ErrNoRows
		}
		query = `SELECT t.seq, t.number, t.title, t.closed_at, t.archived_at, m.name, t.package_name
			FROM archived_tickets t JOIN members m ON m.id = t.operator_member_id
			WHERE t.seq = ?`
		arg = seq
	}
	var t Tombstone
	var operatorName string
	var closedAt, archivedAt string
	err := s.db.QueryRowContext(ctx, query, arg).Scan(&t.Seq, &t.Number, &t.Title,
		&closedAt, &archivedAt, &operatorName, &t.PackageName)
	if err != nil {
		return Tombstone{}, err
	}
	return parseTombstone(t, operatorName, closedAt, archivedAt)
}

// TombstoneByCredentialHash 用来让已删除的访客链接与已认领设备仍能明确提示
// “工单已归档”。
func (s *Service) TombstoneByCredentialHash(ctx context.Context, hash string) (Tombstone, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.seq, t.number, t.title, t.closed_at, t.archived_at, m.name, t.package_name,
		       t.guest_key_hashes, t.guest_device_hashes
		FROM archived_tickets t JOIN members m ON m.id = t.operator_member_id`)
	if err != nil {
		return Tombstone{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var t Tombstone
		var operatorName, closedAt, archivedAt, keyHashes, deviceHashes string
		if err := rows.Scan(&t.Seq, &t.Number, &t.Title, &closedAt, &archivedAt,
			&operatorName, &t.PackageName, &keyHashes, &deviceHashes); err != nil {
			return Tombstone{}, err
		}
		var keys, devices []string
		if err := json.Unmarshal([]byte(keyHashes), &keys); err != nil {
			return Tombstone{}, fmt.Errorf("墓碑 %s 的访客链接哈希无效：%w", t.Number, err)
		}
		if err := json.Unmarshal([]byte(deviceHashes), &devices); err != nil {
			return Tombstone{}, fmt.Errorf("墓碑 %s 的访客设备哈希无效：%w", t.Number, err)
		}
		if containsString(keys, hash) || containsString(devices, hash) {
			out, err := parseTombstone(t, operatorName, closedAt, archivedAt)
			if err != nil {
				return Tombstone{}, err
			}
			return out, nil
		}
	}
	if err := rows.Err(); err != nil {
		return Tombstone{}, err
	}
	return Tombstone{}, sql.ErrNoRows
}

// Remove 校验“我已保存”确认后，在一个事务里删除归档包对应的工单全部关联
// 数据并写墓碑；提交后再删除附件主文件、备份镜像与暂存包。
func (s *Service) Remove(ctx context.Context, packageName string, operator identity.Viewer) (Removal, error) {
	s.generateMu.Lock()
	defer s.generateMu.Unlock()

	_, pkg, err := s.Open(packageName)
	if errors.Is(err, ErrPackageNotFound) {
		return Removal{}, err
	}
	if err != nil {
		return Removal{}, err
	}
	now := s.clk.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Removal{}, err
	}
	defer tx.Rollback()
	// tickets.creator_link_id 与 guest_links.ticket_seq 形成环状外键；
	// 事务内延后检查，等两边都删除后再统一判定。
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return Removal{}, err
	}

	tickets, attachmentIDs, attachmentBytes, credentials, err := s.removalState(ctx, tx, pkg.TicketNumbers, pkg.GeneratedAt)
	if err != nil {
		return Removal{}, err
	}
	tombstones := make([]Tombstone, 0, len(tickets))
	for _, t := range tickets {
		tombstones = append(tombstones, Tombstone{Seq: t.Seq, Number: t.Number, Title: t.Title,
			ClosedAt: t.ClosedAt, ArchivedAt: now, OperatorName: operator.Member.Name, PackageName: pkg.Name})
		if err := insertTombstoneTx(ctx, tx, t.Seq, t.Number, t.Title, t.ClosedAt, now,
			operator.Member.ID, pkg.Name, credentials[t.Seq].GuestKeys, credentials[t.Seq].GuestDevices); err != nil {
			return Removal{}, err
		}
	}
	if err := deleteTicketsTx(ctx, tx, tickets); err != nil {
		return Removal{}, err
	}
	if err := insertRemoveOperation(ctx, tx, operator.Member.ID, now, numbersOf(tickets), pkg.Name); err != nil {
		return Removal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Removal{}, err
	}

	s.atts.RemoveFiles(attachmentIDs)
	for _, id := range attachmentIDs {
		if err := os.Remove(filepath.Join(s.backupDir, "attachments", strconv.FormatInt(id, 10))); err != nil && !os.IsNotExist(err) {
			return Removal{}, err
		}
	}
	if err := os.Remove(filepath.Join(s.stageDir(), pkg.Name)); err != nil && !os.IsNotExist(err) {
		return Removal{}, err
	}
	return Removal{PackageName: pkg.Name, Tombstones: tombstones,
		AttachmentBytes: attachmentBytes, RemovedAt: now}, nil
}

func (s *Service) removalState(ctx context.Context, tx *sql.Tx, numbers []string, generatedAt time.Time) (
	[]TicketSummary, []int64, int64, map[int64]removalCredentials, error) {
	args := make([]any, 0, len(numbers))
	for _, number := range numbers {
		args = append(args, number)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT t.seq, t.number, t.title, t.status, (
			SELECT e.at FROM timeline_events e
			WHERE e.ticket_seq = t.seq AND e.type = 'closed'
			ORDER BY e.seq DESC LIMIT 1
		)
		FROM tickets t WHERE t.number IN (`+inPlaceholders(len(args))+`)`, args...)
	if err != nil {
		return nil, nil, 0, nil, err
	}
	byNumber := map[string]TicketSummary{}
	for rows.Next() {
		var t TicketSummary
		var status string
		var closedAt sql.NullString
		if err := rows.Scan(&t.Seq, &t.Number, &t.Title, &status, &closedAt); err != nil {
			rows.Close()
			return nil, nil, 0, nil, err
		}
		if status != "closed" || !closedAt.Valid {
			continue
		}
		at, err := time.ParseInLocation(time.RFC3339Nano, closedAt.String, clock.Beijing)
		if err != nil {
			continue
		}
		if at.After(generatedAt) {
			continue
		}
		t.ClosedAt = at
		byNumber[t.Number] = t
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, 0, nil, err
	}
	rows.Close()

	out := make([]TicketSummary, 0, len(numbers))
	for _, number := range numbers {
		t, ok := byNumber[number]
		if !ok {
			return nil, nil, 0, nil, fmt.Errorf("%w：%s", ErrAlreadyRemoved, number)
		}
		out = append(out, t)
	}
	seqArgs := make([]any, 0, len(out))
	for _, t := range out {
		seqArgs = append(seqArgs, t.Seq)
	}
	attRows, err := tx.QueryContext(ctx, `
		SELECT id, COALESCE(size, 0) FROM attachments
		WHERE ticket_seq IN (`+inPlaceholders(len(seqArgs))+`)`, seqArgs...)
	if err != nil {
		return nil, nil, 0, nil, err
	}
	ids := []int64{}
	var bytes int64
	for attRows.Next() {
		var id, size int64
		if err := attRows.Scan(&id, &size); err != nil {
			attRows.Close()
			return nil, nil, 0, nil, err
		}
		ids = append(ids, id)
		bytes += size
	}
	if err := attRows.Err(); err != nil {
		attRows.Close()
		return nil, nil, 0, nil, err
	}
	attRows.Close()

	hashRows, err := tx.QueryContext(ctx, `
		SELECT l.ticket_seq, l.key_hash, d.token_hash FROM guest_links l
		LEFT JOIN guest_devices d ON d.guest_link_id = l.id
		WHERE l.ticket_seq IN (`+inPlaceholders(len(seqArgs))+`)`, seqArgs...)
	if err != nil {
		return nil, nil, 0, nil, err
	}
	credentials := map[int64]removalCredentials{}
	for hashRows.Next() {
		var seq int64
		var keyHash string
		var deviceHash sql.NullString
		if err := hashRows.Scan(&seq, &keyHash, &deviceHash); err != nil {
			hashRows.Close()
			return nil, nil, 0, nil, err
		}
		credential := credentials[seq]
		credential.GuestKeys = append(credential.GuestKeys, keyHash)
		if deviceHash.Valid {
			credential.GuestDevices = append(credential.GuestDevices, deviceHash.String)
		}
		credentials[seq] = credential
	}
	if err := hashRows.Err(); err != nil {
		hashRows.Close()
		return nil, nil, 0, nil, err
	}
	hashRows.Close()
	return out, ids, bytes, credentials, nil
}

type removalCredentials struct {
	GuestKeys    []string
	GuestDevices []string
}

func insertTombstoneTx(ctx context.Context, tx *sql.Tx, seq int64, number, title string,
	closedAt, archivedAt time.Time, operatorID int64, packageName string,
	guestKeys, guestDevices []string) error {
	if guestKeys == nil {
		guestKeys = []string{}
	}
	if guestDevices == nil {
		guestDevices = []string{}
	}
	encodedKeys, err := json.Marshal(guestKeys)
	if err != nil {
		return err
	}
	encodedDevices, err := json.Marshal(guestDevices)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO archived_tickets(seq, number, title, closed_at, archived_at,
			operator_member_id, package_name, guest_key_hashes, guest_device_hashes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, seq, number, title,
		closedAt.Format(time.RFC3339Nano), archivedAt.Format(time.RFC3339Nano),
		operatorID, packageName, string(encodedKeys), string(encodedDevices))
	return err
}

func deleteTicketsTx(ctx context.Context, tx *sql.Tx, tickets []TicketSummary) error {
	args := make([]any, 0, len(tickets))
	for _, t := range tickets {
		args = append(args, t.Seq)
	}
	placeholder := inPlaceholders(len(args))
	_, err := tx.ExecContext(ctx, `DELETE FROM draft_versions WHERE draft_id IN (
		SELECT id FROM drafts WHERE ticket_seq IN (`+placeholder+`))`, args...)
	if err != nil {
		return err
	}
	for _, statement := range []string{
		`DELETE FROM drafts WHERE ticket_seq IN (` + placeholder + `)`,
		`DELETE FROM attachments WHERE ticket_seq IN (` + placeholder + `)`,
		`DELETE FROM brief_versions WHERE ticket_seq IN (` + placeholder + `)`,
		`DELETE FROM access_records WHERE ticket_seq IN (` + placeholder + `)`,
		`DELETE FROM ticket_collaborators WHERE ticket_seq IN (` + placeholder + `)`,
		`DELETE FROM ticket_labels WHERE ticket_seq IN (` + placeholder + `)`,
		`DELETE FROM ticket_reads WHERE ticket_seq IN (` + placeholder + `)`,
		`DELETE FROM timeline_events WHERE ticket_seq IN (` + placeholder + `)`,
		`DELETE FROM tickets WHERE seq IN (` + placeholder + `)`,
		`DELETE FROM guest_devices WHERE guest_link_id IN (
			SELECT id FROM guest_links WHERE ticket_seq IN (` + placeholder + `))`,
		`DELETE FROM guest_links WHERE ticket_seq IN (` + placeholder + `)`,
	} {
		if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
			return err
		}
	}
	return nil
}

func insertRemoveOperation(ctx context.Context, tx *sql.Tx, operatorID int64, at time.Time,
	ticketNumbers []string, packageName string) error {
	numbers, err := json.Marshal(ticketNumbers)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO admin_operations(operation_type, operator_member_id, at, ticket_numbers, package_name)
		VALUES ('archive_remove', ?, ?, ?, ?)`, operatorID,
		at.Format(time.RFC3339Nano), string(numbers), packageName)
	return err
}

func parseTombstone(t Tombstone, operatorName, closedAt, archivedAt string) (Tombstone, error) {
	var err error
	if t.ClosedAt, err = time.ParseInLocation(time.RFC3339Nano, closedAt, clock.Beijing); err != nil {
		return Tombstone{}, fmt.Errorf("墓碑 %s 的关闭时间无效：%w", t.Number, err)
	}
	if t.ArchivedAt, err = time.ParseInLocation(time.RFC3339Nano, archivedAt, clock.Beijing); err != nil {
		return Tombstone{}, fmt.Errorf("墓碑 %s 的归档时间无效：%w", t.Number, err)
	}
	t.OperatorName = operatorName
	return t, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
