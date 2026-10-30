package archive

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

// ErrPackageStale 表示归档包生成后平台数据又发生了变化。
var ErrPackageStale = errors.New("archive package stale")

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
	CleanupFailures int
}

const tombstoneColumns = `t.seq, t.number, t.title, t.closed_at, t.archived_at, m.name, t.package_name`

// TombstoneByRef 用工单编号或全局序号查墓碑；没有墓碑时返回 sql.ErrNoRows。
func (s *Service) TombstoneByRef(ctx context.Context, ref string) (Tombstone, error) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	var (
		query string
		arg   any
	)
	if strings.Contains(ref, "-") {
		query = `SELECT ` + tombstoneColumns + `
			FROM archived_tickets t JOIN members m ON m.id = t.operator_member_id
			WHERE t.number = ?`
		arg = ref
	} else {
		seq, err := strconv.ParseInt(ref, 10, 64)
		if err != nil {
			return Tombstone{}, sql.ErrNoRows
		}
		query = `SELECT ` + tombstoneColumns + `
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

// TombstoneForViewer 按请求方能否看到原工单返回墓碑。成员额外允许历史通知
// 持有者点开；访客只允许自己原链接/设备凭证指向的那张工单。
func (s *Service) TombstoneForViewer(ctx context.Context, viewer identity.Viewer, ref string) (Tombstone, error) {
	tombstone, err := s.TombstoneByRef(ctx, ref)
	if err != nil {
		return Tombstone{}, err
	}
	allowed := false
	switch viewer.Kind {
	case identity.ViewerMember:
		allowed, err = s.CanSeeTombstone(ctx, tombstone, viewer.Member.ID)
		if err != nil {
			return Tombstone{}, err
		}
	case identity.ViewerGuest:
		allowed = viewer.GuestLink != nil && viewer.GuestLink.TicketSeq == tombstone.Seq
	}
	if !allowed {
		return Tombstone{}, sql.ErrNoRows
	}
	return tombstone, nil
}

// CanSeeTombstone 判断成员是否可读取墓碑：管理员、移除时的可见成员快照内
// 成员，以及仍持有该工单历史通知的成员（成员 Agent 与主人共用成员 ID）。
func (s *Service) CanSeeTombstone(ctx context.Context, tombstone Tombstone, memberID int64) (bool, error) {
	var allowed bool
	err := s.db.QueryRowContext(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM members WHERE id = ? AND is_admin = 1)
			OR EXISTS (SELECT 1 FROM archived_ticket_viewers
				WHERE seq = ? AND member_id = ?)
			OR EXISTS (SELECT 1 FROM notifications
				WHERE member_id = ? AND ticket_seq = ?)`,
		memberID, tombstone.Seq, memberID, memberID, tombstone.Seq).Scan(&allowed)
	return allowed, err
}

// TombstoneByCredentialHash 用来让已删除的访客链接与已认领设备仍能明确提示
// “工单已归档”。
func (s *Service) TombstoneByCredentialHash(ctx context.Context, hash string) (Tombstone, error) {
	var t Tombstone
	var operatorName, closedAt, archivedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT `+tombstoneColumns+`
		FROM archived_tickets t
		JOIN members m ON m.id = t.operator_member_id
		JOIN archived_ticket_credentials c ON c.seq = t.seq
		WHERE c.hash = ?
		ORDER BY t.seq LIMIT 1`, hash).Scan(&t.Seq, &t.Number, &t.Title,
		&closedAt, &archivedAt, &operatorName, &t.PackageName)
	if errors.Is(err, sql.ErrNoRows) {
		return Tombstone{}, sql.ErrNoRows
	}
	if err != nil {
		return Tombstone{}, err
	}
	return parseTombstone(t, operatorName, closedAt, archivedAt)
}

// Remove 校验“我已保存”确认和内容指纹后，在一个事务里删除归档包对应的工单
// 全部关联数据并写墓碑；提交后尽力清理附件主文件与备份镜像。
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
	// tickets.creator_link_id 与 guest_links.ticket_seq 形成环状外键；
	// 事务内延后检查，等两边都删除后再统一判定。
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return Removal{}, err
	}

	state, err := s.removalState(ctx, tx, pkg.TicketNumbers, pkg.GeneratedAt)
	if err != nil {
		return Removal{}, err
	}
	for _, t := range state.Tickets {
		current, err := exportTicket(ctx, tx, t)
		if err != nil {
			return Removal{}, err
		}
		fingerprint, err := ticketExportFingerprint(current)
		if err != nil {
			return Removal{}, err
		}
		expected := manifestContentHash(manifest, t.Number)
		if expected == "" || fingerprint != expected {
			return Removal{}, fmt.Errorf("%w：%s", ErrPackageStale, t.Number)
		}
	}
	tickets := state.Tickets
	tombstones := make([]Tombstone, 0, len(tickets))
	for _, t := range tickets {
		tombstones = append(tombstones, Tombstone{Seq: t.Seq, Number: t.Number, Title: t.Title,
			ClosedAt: t.ClosedAt, ArchivedAt: now, OperatorName: operator.Member.Name, PackageName: pkg.Name})
		record := tombstoneRecord{Ticket: t, ArchivedAt: now, OperatorID: operator.Member.ID,
			PackageName: pkg.Name, Credentials: state.Credentials[t.Seq], Viewers: state.Viewers[t.Seq]}
		if err := insertTombstoneTx(ctx, tx, record); err != nil {
			return Removal{}, err
		}
	}
	if err := deleteTicketsTx(ctx, tx, tickets); err != nil {
		return Removal{}, err
	}
	if err := insertArchiveOperation(ctx, tx, "archive_remove", operator.Member.ID, now,
		numbersOf(tickets), pkg.Name); err != nil {
		return Removal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Removal{}, err
	}

	freedBytes, cleanupFailures := s.cleanupRemovedFiles(state.AttachmentIDs)
	return Removal{PackageName: pkg.Name, Tombstones: tombstones,
		AttachmentBytes: freedBytes, RemovedAt: now, CleanupFailures: cleanupFailures}, nil
}

func (s *Service) cleanupRemovedFiles(ids []int64) (int64, int) {
	var freed int64
	failures := 0
	for _, id := range ids {
		text := strconv.FormatInt(id, 10)
		bytes, failed := removeFileForCleanup(s.atts.Path(id))
		freed += bytes
		failures += failed
		bytes, failed = removeFileForCleanup(filepath.Join(s.backupDir, "attachments", text))
		freed += bytes
		failures += failed
	}
	return freed, failures
}

func removeFileForCleanup(path string) (int64, int) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, 0
	}
	if err != nil {
		log.Printf("归档移除后检查文件 %s 失败：%v", path, err)
		return 0, 1
	}
	if err := os.Remove(path); err != nil {
		log.Printf("归档移除后清理文件 %s 失败：%v", path, err)
		return 0, 1
	}
	return info.Size(), 0
}

func readPackageManifest(file *os.File) (*manifest, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	reader, err := zip.NewReader(file, info.Size())
	if err != nil {
		return nil, fmt.Errorf("%w：读取清单失败：%v", ErrPackageInvalid, err)
	}
	entry, err := reader.Open("manifest.json")
	if err != nil {
		return nil, fmt.Errorf("%w：缺少 manifest.json：%v", ErrPackageInvalid, err)
	}
	defer entry.Close()
	var m manifest
	if err := json.NewDecoder(entry).Decode(&m); err != nil {
		return nil, fmt.Errorf("%w：清单不是合法 JSON：%v", ErrPackageInvalid, err)
	}
	return &m, nil
}

func manifestContentHash(m *manifest, number string) string {
	for _, ticket := range m.Tickets {
		if ticket.Number == number {
			return ticket.ContentSHA256
		}
	}
	return ""
}

func ticketExportFingerprint(export ticketExport) (string, error) {
	encoded, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return "", err
	}
	encoded = append(encoded, '\n')
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

type removalSnapshot struct {
	Tickets       []TicketSummary
	AttachmentIDs []int64
	Credentials   map[int64]removalCredentials
	Viewers       map[int64][]int64
}

func (s *Service) removalState(ctx context.Context, tx *sql.Tx, numbers []string, generatedAt time.Time) (removalSnapshot, error) {
	var out removalSnapshot
	args := make([]any, 0, len(numbers))
	for _, number := range numbers {
		args = append(args, number)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT t.seq, t.number, t.title, t.status, t.creator_member_id, t.assignee_id, (
			SELECT e.at FROM timeline_events e
			WHERE e.ticket_seq = t.seq AND e.type = 'closed'
			ORDER BY e.seq DESC LIMIT 1
		)
		FROM tickets t WHERE t.number IN (`+inPlaceholders(len(args))+`)`, args...)
	if err != nil {
		return removalSnapshot{}, err
	}
	byNumber := map[string]TicketSummary{}
	present := map[string]bool{}
	viewers := map[int64][]int64{}
	viewed := map[int64]map[int64]bool{}
	needTriagers := false
	addViewer := func(seq, memberID int64) {
		if memberID == 0 || viewed[seq] == nil {
			if memberID == 0 {
				return
			}
			viewed[seq] = map[int64]bool{}
		}
		if viewed[seq][memberID] {
			return
		}
		viewed[seq][memberID] = true
		viewers[seq] = append(viewers[seq], memberID)
	}
	for rows.Next() {
		var t TicketSummary
		var status string
		var creatorID, assigneeID sql.NullInt64
		var closedAt sql.NullString
		if err := rows.Scan(&t.Seq, &t.Number, &t.Title, &status, &creatorID, &assigneeID, &closedAt); err != nil {
			rows.Close()
			return removalSnapshot{}, err
		}
		present[t.Number] = true
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
		if creatorID.Valid {
			addViewer(t.Seq, creatorID.Int64)
		}
		if assigneeID.Valid {
			addViewer(t.Seq, assigneeID.Int64)
		} else {
			needTriagers = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return removalSnapshot{}, err
	}
	rows.Close()

	tickets := make([]TicketSummary, 0, len(numbers))
	for _, number := range numbers {
		t, ok := byNumber[number]
		if !ok {
			if present[number] {
				return removalSnapshot{}, fmt.Errorf("%w：%s", ErrNotArchivable, number)
			}
			return removalSnapshot{}, fmt.Errorf("%w：%s", ErrAlreadyRemoved, number)
		}
		tickets = append(tickets, t)
	}
	out.Tickets = tickets
	seqArgs := make([]any, 0, len(tickets))
	for _, t := range tickets {
		seqArgs = append(seqArgs, t.Seq)
	}

	collabRows, err := tx.QueryContext(ctx, `
		SELECT ticket_seq, member_id FROM ticket_collaborators
		WHERE ticket_seq IN (`+inPlaceholders(len(seqArgs))+`) ORDER BY ticket_seq, member_id`, seqArgs...)
	if err != nil {
		return removalSnapshot{}, err
	}
	for collabRows.Next() {
		var seq, memberID int64
		if err := collabRows.Scan(&seq, &memberID); err != nil {
			collabRows.Close()
			return removalSnapshot{}, err
		}
		addViewer(seq, memberID)
	}
	if err := collabRows.Err(); err != nil {
		collabRows.Close()
		return removalSnapshot{}, err
	}
	collabRows.Close()

	if needTriagers {
		triagers, err := effectiveTriagersTx(ctx, tx)
		if err != nil {
			return removalSnapshot{}, err
		}
		for _, t := range tickets {
			for _, memberID := range triagers {
				addViewer(t.Seq, memberID)
			}
		}
	}

	attRows, err := tx.QueryContext(ctx, `
		SELECT id, COALESCE(size, 0) FROM attachments
		WHERE ticket_seq IN (`+inPlaceholders(len(seqArgs))+`)`, seqArgs...)
	if err != nil {
		return removalSnapshot{}, err
	}
	out.AttachmentIDs = []int64{}
	for attRows.Next() {
		var id, size int64
		if err := attRows.Scan(&id, &size); err != nil {
			attRows.Close()
			return removalSnapshot{}, err
		}
		out.AttachmentIDs = append(out.AttachmentIDs, id)
	}
	if err := attRows.Err(); err != nil {
		attRows.Close()
		return removalSnapshot{}, err
	}
	attRows.Close()

	hashRows, err := tx.QueryContext(ctx, `
		SELECT l.ticket_seq, l.key_hash, d.token_hash FROM guest_links l
		LEFT JOIN guest_devices d ON d.guest_link_id = l.id
		WHERE l.ticket_seq IN (`+inPlaceholders(len(seqArgs))+`)`, seqArgs...)
	if err != nil {
		return removalSnapshot{}, err
	}
	out.Credentials = map[int64]removalCredentials{}
	for hashRows.Next() {
		var seq int64
		var keyHash string
		var deviceHash sql.NullString
		if err := hashRows.Scan(&seq, &keyHash, &deviceHash); err != nil {
			hashRows.Close()
			return removalSnapshot{}, err
		}
		credential := out.Credentials[seq]
		credential.GuestKeys = append(credential.GuestKeys, keyHash)
		if deviceHash.Valid {
			credential.GuestDevices = append(credential.GuestDevices, deviceHash.String)
		}
		out.Credentials[seq] = credential
	}
	if err := hashRows.Err(); err != nil {
		hashRows.Close()
		return removalSnapshot{}, err
	}
	hashRows.Close()

	out.Viewers = viewers
	return out, nil
}

func effectiveTriagersTx(ctx context.Context, tx *sql.Tx) ([]int64, error) {
	var explicit int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM members WHERE triager = 1 AND deactivated_at IS NULL`).Scan(&explicit); err != nil {
		return nil, err
	}
	column := "triager"
	if explicit == 0 {
		column = "is_admin"
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM members WHERE `+column+` = 1 AND deactivated_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type removalCredentials struct {
	GuestKeys    []string
	GuestDevices []string
}

type tombstoneRecord struct {
	Ticket      TicketSummary
	ArchivedAt  time.Time
	OperatorID  int64
	PackageName string
	Credentials removalCredentials
	Viewers     []int64
}

func insertTombstoneTx(ctx context.Context, tx *sql.Tx, record tombstoneRecord) error {
	t := record.Ticket
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO archived_tickets(seq, number, title, closed_at, archived_at,
			operator_member_id, package_name)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, t.Seq, t.Number, t.Title,
		t.ClosedAt.Format(time.RFC3339Nano), record.ArchivedAt.Format(time.RFC3339Nano),
		record.OperatorID, record.PackageName); err != nil {
		return err
	}
	for _, credential := range []struct {
		kind   string
		hashes []string
	}{
		{kind: "guest_key", hashes: record.Credentials.GuestKeys},
		{kind: "guest_device", hashes: record.Credentials.GuestDevices},
	} {
		for _, hash := range credential.hashes {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO archived_ticket_credentials(seq, kind, hash) VALUES (?, ?, ?)`,
				t.Seq, credential.kind, hash); err != nil {
				return err
			}
		}
	}
	for _, memberID := range record.Viewers {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO archived_ticket_viewers(seq, member_id) VALUES (?, ?)`,
			t.Seq, memberID); err != nil {
			return err
		}
	}
	return nil
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
