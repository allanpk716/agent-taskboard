// 认领（两步上传的第二步）与 24 小时清除。认领发生在回复或说明修订的同一个
// 事务里：引用的附件从 pending 转成 attached，挂到「说明」或「回复 N」上。
package attachments

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"
)

// IntakeClaim identifies the anonymous upload batch: all pending rows uploaded
// through one intake with the submission's Idempotency-Key.
type IntakeClaim struct {
	IntakeID  int64
	UploadKey string
}

// ClaimMode 是认领模式。
type ClaimMode int

const (
	// ClaimAllPending 用于回复：引用的每个附件都必须存在、待用、且是本次
	// 凭证上传的（prototype 的 claimAttachments 语义）。
	ClaimAllPending ClaimMode = iota
	// ClaimOnlyPending 用于说明修订：只认领其中待用的；已挂到别处的不动
	//（修订后的说明可以重复引用已有插图）。
	ClaimOnlyPending
)

// ClaimTx 在 tx 里认领 refs 指向的附件（两步上传的第二步），并按上限检查
// 这条说明或回复的附件个数与插图张数（插图 = 插图语法引用的图片文件，
// 按扩展名判定；附件 = attachment_ids 与正文引用的其余文件）。
// 计数对 refs 的全集做（说明修订时含已挂在别处的引用）。
func (m *Manager) ClaimTx(ctx context.Context, tx *sql.Tx, ticketSeq int64,
	principal Principal, refs []PostRef, mode ClaimMode, attachedTo string, at time.Time) error {
	if len(refs) == 0 {
		return nil
	}
	// 取回全部引用的元数据（计数需要按扩展名判定插图）。说明修订
	//（ClaimOnlyPending）沿用原型的语义：查不到、不在本单、已挂到别处的引用
	// 一律跳过（只认领其中待用的）；回复（ClaimAllPending）则每个引用都必须可用。
	rows := make(map[string]AttachmentInfo, len(refs))
	for _, ref := range refs {
		if _, ok := rows[ref.ID]; ok {
			continue
		}
		id, err := ParseRef(ref.ID)
		if err != nil {
			if mode == ClaimOnlyPending {
				continue
			}
			return notUsable(ref.ID)
		}
		var (
			a          AttachmentInfo
			attachedTo sql.NullString
			memberID   sql.NullInt64
			agentID    sql.NullInt64
			linkID     sql.NullInt64
			uploadedAt string
		)
		err = tx.QueryRowContext(ctx, `
			SELECT id, ticket_seq, filename, size, state, attached_to,
			       uploader_member_id, uploader_agent_id, uploader_link_id, uploaded_at
			FROM attachments WHERE id = ?`, id).
			Scan(&a.ID, &a.TicketSeq, &a.Filename, &a.Size, &a.State, &attachedTo,
				&memberID, &agentID, &linkID, &uploadedAt)
		if err != nil {
			if mode == ClaimOnlyPending {
				continue
			}
			return notUsable(ref.ID)
		}
		a.AttachedTo = attachedTo.String
		if a.TicketSeq != ticketSeq {
			if mode == ClaimOnlyPending {
				continue // 别的工单上的附件引用不到：说明修订里跳过
			}
			return notUsable(ref.ID)
		}
		a.Uploader = Principal{MemberID: memberID.Int64, AgentID: agentID.Int64, LinkID: linkID.Int64}
		rows[ref.ID] = a
	}

	// 计数：插图与附件分开（同一 id 只计一次；跳过取不到元数据的引用）。
	files, images := 0, 0
	for _, ref := range refs {
		row, ok := rows[ref.ID]
		if !ok {
			continue
		}
		if ref.ImageSyntax && IsImage(row.Filename) {
			images++
		} else {
			files++
		}
	}
	if files > m.limits.MaxFilesPerPost {
		return &CountExceededError{Kind: "files", Count: files, Limit: m.limits.MaxFilesPerPost}
	}
	if images > m.limits.MaxImagesPerPost {
		return &CountExceededError{Kind: "images", Count: images, Limit: m.limits.MaxImagesPerPost}
	}

	// 认领：待用的必须是本次凭证上传的；回复模式要求全部待用。
	claimed := map[string]bool{}
	for _, ref := range refs {
		if claimed[ref.ID] {
			continue
		}
		claimed[ref.ID] = true
		a := rows[ref.ID]
		if a.State != "pending" {
			if mode == ClaimAllPending {
				return notUsable(ref.ID)
			}
			continue
		}
		if !principal.owns(a.Uploader.MemberID, a.Uploader.LinkID) {
			return notUsable(ref.ID)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE attachments SET state = 'attached', attached_to = ?, claimed_at = ?, draft_id = NULL
			WHERE id = ? AND state = 'pending'`,
			attachedTo, at.Format(time.RFC3339Nano), a.ID); err != nil {
			return fmt.Errorf("认领附件 %s 失败：%w", ref.ID, err)
		}
	}
	return nil
}

// ClaimIntakeTx 在建单事务里认领入口附件。只有本入口、本次 Idempotency-Key
// 上传且仍未认领的附件可用；引用一个不匹配的附件会整体拒绝，防止串用。
// 认领后的工单附件合计同样受 max_ticket_mb 限制。
func (m *Manager) ClaimIntakeTx(ctx context.Context, tx *sql.Tx, claim IntakeClaim,
	ticketSeq int64, refs []PostRef, at time.Time) error {
	if len(refs) == 0 {
		return nil
	}
	rows := make(map[string]AttachmentInfo, len(refs))
	files, images := 0, 0
	var total int64
	for _, ref := range refs {
		if _, ok := rows[ref.ID]; ok {
			continue
		}
		id, err := ParseRef(ref.ID)
		if err != nil {
			return notUsable(ref.ID)
		}
		var (
			a          AttachmentInfo
			ticket     sql.NullInt64
			attachedTo sql.NullString
			intakeID   sql.NullInt64
			uploadKey  sql.NullString
		)
		err = tx.QueryRowContext(ctx, `
			SELECT id, ticket_seq, filename, size, state, attached_to,
			       uploader_intake_id, intake_upload_key
			FROM attachments WHERE id = ?`, id).
			Scan(&a.ID, &ticket, &a.Filename, &a.Size, &a.State, &attachedTo, &intakeID, &uploadKey)
		if err != nil || ticket.Valid || a.State != "pending" ||
			intakeID.Int64 != claim.IntakeID || uploadKey.String != claim.UploadKey {
			return notUsable(ref.ID)
		}
		rows[ref.ID] = a
		total += a.Size
	}
	for _, ref := range refs {
		row := rows[ref.ID]
		if ref.ImageSyntax && IsImage(row.Filename) {
			images++
		} else {
			files++
		}
	}
	if files > m.limits.MaxFilesPerPost {
		return &CountExceededError{Kind: "files", Count: files, Limit: m.limits.MaxFilesPerPost}
	}
	if images > m.limits.MaxImagesPerPost {
		return &CountExceededError{Kind: "images", Count: images, Limit: m.limits.MaxImagesPerPost}
	}
	if total > m.limits.MaxTicketBytes {
		return &QuotaExceededError{Used: 0, Size: total, Limit: m.limits.MaxTicketBytes}
	}
	for id := range rows {
		parsed, err := ParseRef(id)
		if err != nil {
			return notUsable(id)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE attachments SET ticket_seq = ?, state = 'attached',
			       attached_to = '说明', claimed_at = ?
			WHERE id = ? AND state = 'pending'
			  AND ticket_seq IS NULL AND uploader_intake_id = ?
			  AND intake_upload_key = ?`,
			ticketSeq, at.Format(time.RFC3339Nano), parsed, claim.IntakeID, claim.UploadKey); err != nil {
			return fmt.Errorf("认领入口附件 %s 失败：%w", id, err)
		}
	}
	return nil
}

// ValidateBriefIDsTx 校验说明 attachment_ids 里的每个附件：要么是本次凭证
// 上传的待用附件，要么已经挂在这张工单的说明上（从列表删掉后仍可再挂回）。
func (m *Manager) ValidateBriefIDsTx(ctx context.Context, tx *sql.Tx, ticketSeq int64,
	principal Principal, ids []string) error {
	for _, id := range ids {
		parsed, err := ParseRef(id)
		if err != nil {
			return notUsable(id)
		}
		var (
			state       string
			attachedTo  sql.NullString
			memberID    sql.NullInt64
			linkID      sql.NullInt64
			otherTicket int64
		)
		err = tx.QueryRowContext(ctx, `
			SELECT ticket_seq, state, attached_to, uploader_member_id, uploader_link_id
			FROM attachments WHERE id = ?`, parsed).
			Scan(&otherTicket, &state, &attachedTo, &memberID, &linkID)
		if err != nil || otherTicket != ticketSeq {
			return notUsable(id)
		}
		if state == "pending" {
			if !principal.owns(memberID.Int64, linkID.Int64) {
				return notUsable(id)
			}
			continue
		}
		if state != "attached" || attachedTo.String != "说明" {
			return notUsable(id)
		}
	}
	return nil
}

func notUsable(id string) error {
	return fmt.Errorf("%w（%s）", ErrNotUsable, id)
}

// CleanupUnreferenced 清除上传超过 24 小时仍未引用的待用附件（行 + 文件），
// 返回清除的个数。备份镜像只增不删，镜像里可能残留这类文件，可以接受。
// 服务定时调用（时钟可注入，测试不睡真实时间）。
func (m *Manager) CleanupUnreferenced(ctx context.Context) (int, error) {
	cutoff := m.clk.Now().Add(-24 * time.Hour)
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, uploaded_at FROM attachments WHERE state = 'pending' AND draft_id IS NULL`)
	if err != nil {
		return 0, err
	}
	type pending struct {
		id         int64
		uploadedAt string
	}
	var stale []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.uploadedAt); err != nil {
			rows.Close()
			return 0, err
		}
		if stale0, err := parseTime(p.uploadedAt); err != nil {
			rows.Close()
			return 0, err
		} else if !stale0.After(cutoff) {
			stale = append(stale, p)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, p := range stale {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM attachments WHERE id = ? AND state = 'pending'`, p.id)
		if err != nil {
			return 0, err
		}
		// 并发窗口里被认领（state 已变 attached）的行删不掉，文件也绝不能删。
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		if n == 0 {
			continue
		}
		// 行删掉之后再删文件；删失败只留下孤儿文件，不影响配额（下次清除会再试）。
		if err := os.Remove(m.Path(p.id)); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(stale), nil
}
