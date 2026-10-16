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
	// 取回全部引用的元数据（计数需要按扩展名判定插图）。
	rows := make(map[string]AttachmentInfo, len(refs))
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
			return notUsable(ref.ID)
		}
		a.AttachedTo = attachedTo.String
		if a.TicketSeq != ticketSeq {
			return notUsable(ref.ID) // 别的工单上的附件引用不到
		}
		a.Uploader = Principal{MemberID: memberID.Int64, AgentID: agentID.Int64, LinkID: linkID.Int64}
		rows[ref.ID] = a
	}

	// 计数：插图与附件分开（同一 id 只计一次）。
	files, images := 0, 0
	for _, ref := range refs {
		if ref.ImageSyntax && IsImage(rows[ref.ID].Filename) {
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
			UPDATE attachments SET state = 'attached', attached_to = ?, claimed_at = ?
			WHERE id = ? AND state = 'pending'`,
			attachedTo, at.Format(time.RFC3339Nano), a.ID); err != nil {
			return fmt.Errorf("认领附件 %s 失败：%w", ref.ID, err)
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
		`SELECT id, uploaded_at FROM attachments WHERE state = 'pending'`)
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
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM attachments WHERE id = ? AND state = 'pending'`, p.id); err != nil {
			return 0, err
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
