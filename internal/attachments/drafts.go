package attachments

import (
	"context"
	"database/sql"
	"os"
)

// ReserveDraftTx 把草稿最新版引用的待用附件标记为草稿占用。引用规则与
// 回复认领一致：必须在本单、待用、由同一方上传；同一草稿替换版本时可
// 反复占用自己的附件。
func (m *Manager) ReserveDraftTx(ctx context.Context, tx *sql.Tx, draftID, ticketSeq int64,
	principal Principal, refs []PostRef) error {
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
			otherSeq   int64
			memberID   sql.NullInt64
			linkID     sql.NullInt64
			otherDraft sql.NullInt64
		)
		err = tx.QueryRowContext(ctx, `
			SELECT id, ticket_seq, filename, size, state,
			       uploader_member_id, uploader_link_id, draft_id
			FROM attachments WHERE id = ?`, id).
			Scan(&a.ID, &otherSeq, &a.Filename, &a.Size, &a.State,
				&memberID, &linkID, &otherDraft)
		if err != nil || otherSeq != ticketSeq || a.State != "pending" ||
			(otherDraft.Valid && otherDraft.Int64 != draftID) ||
			!principal.owns(memberID.Int64, linkID.Int64) {
			return notUsable(ref.ID)
		}
		rows[ref.ID] = a
	}

	files, images := 0, 0
	for _, ref := range refs {
		row := rows[ref.ID]
		if ref.ImageSyntax && IsImage(row.Filename) {
			images++
			continue
		}
		files++
	}
	if files > m.limits.MaxFilesPerPost {
		return &CountExceededError{Kind: "files", Count: files, Limit: m.limits.MaxFilesPerPost}
	}
	if images > m.limits.MaxImagesPerPost {
		return &CountExceededError{Kind: "images", Count: images, Limit: m.limits.MaxImagesPerPost}
	}
	for id := range rows {
		parsed, err := ParseRef(id)
		if err != nil {
			return notUsable(id)
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE attachments SET draft_id = ?
			WHERE id = ? AND state = 'pending' AND (draft_id IS NULL OR draft_id = ?)`,
			draftID, parsed, draftID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return notUsable(id)
		}
	}
	return nil
}

// ReleaseDraftTx 把一份草稿当前占用的附件恢复成普通待用状态。
func (m *Manager) ReleaseDraftTx(ctx context.Context, tx *sql.Tx, draftID int64) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE attachments SET draft_id = NULL WHERE draft_id = ? AND state = 'pending'`, draftID)
	return err
}

// ByDraft 列出被草稿最新版占用的附件。
func (m *Manager) ByDraft(ctx context.Context, draftID int64) ([]AttachmentInfo, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, ticket_seq, filename, size, state, attached_to, draft_id,
		       uploader_member_id, uploader_agent_id, uploader_link_id, uploaded_at, claimed_at
		FROM attachments WHERE draft_id = ? ORDER BY id`, draftID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAttachments(rows)
}

// DeleteDraftTx 删除草稿当前占用的附件行，返回附件数字 id。调用方提交事务
// 后再 RemoveFiles 删除文件本体，避免事务回滚后文件先丢。
func (m *Manager) DeleteDraftTx(ctx context.Context, tx *sql.Tx, draftID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM attachments WHERE draft_id = ? AND state = 'pending'`, draftID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM attachments WHERE draft_id = ? AND state = 'pending'`, draftID); err != nil {
		return nil, err
	}
	return ids, nil
}

// RemoveFiles 删除附件文件本体；行已经不存在时文件也应该尽快消失。
func (m *Manager) RemoveFiles(ids []int64) {
	for _, id := range ids {
		_ = os.Remove(m.Path(id))
	}
}
