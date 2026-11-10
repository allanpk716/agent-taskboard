// 合并重复访客（#334）：B 的链接改归 A；B 的主页、访问码和设备永久作废。
package tickets

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

var (
	// ErrGuestMergeTargetRequired 表示合并请求没有给出目标访客。
	ErrGuestMergeTargetRequired = errors.New("请选择要并入的另一位访客")
	// ErrGuestMergeSelf 表示不能把访客并入自己。
	ErrGuestMergeSelf = errors.New("不能把访客合并到自己")
	// ErrGuestAlreadyMerged 表示访客已经并入另一位访客；合并不能撤回。
	ErrGuestAlreadyMerged = errors.New("这位访客已并入另一位访客，不能再操作")
	// ErrGuestMergeTargetMerged 表示目标访客自身已经并入别人。
	ErrGuestMergeTargetMerged = errors.New("目标访客已并入另一位访客，不能再接收链接")
	// ErrGuestMergeTargetDisabled 表示目标访客已停用，不能接收来源访客的链接。
	ErrGuestMergeTargetDisabled = errors.New("目标访客已停用，先恢复后再合并")
)

// GuestMergePreview 是成员确认合并前看到的影响面。
type GuestMergePreview struct {
	SourceGuestID   int64
	SourceGuestName string
	TargetGuestID   int64
	TargetGuestName string
	LinkCount       int
	DeviceCount     int
}

// GuestMergeResult 描述一次已完成的合并；LinkCount 是改归链接数，
// DeviceCount 是 B 被作废的设备数。
type GuestMergeResult struct {
	SourceGuestID int64
	TargetGuestID int64
	LinkCount     int
	DeviceCount   int
}

type guestMergeState struct {
	ID       int64
	Name     string
	HolderID int64
	Disabled bool
	Merged   bool
}

type mergeableGuestLink struct {
	ID        int64
	TicketSeq int64
	Note      string
}

// GuestMergePreviewForViewer 返回 B 并入 A 的影响预览。只有网页成员本人能看，
// 且必须同时能管理 A 和 B。
func (m *Manager) GuestMergePreviewForViewer(ctx context.Context, viewer identity.Viewer,
	sourceGuestID, targetGuestID int64) (GuestMergePreview, error) {
	if !viewer.IsWebSession() {
		return GuestMergePreview{}, ErrGuestManagementNotAllowed
	}
	if err := m.ensureGuestMergeManageable(ctx, viewer, sourceGuestID, targetGuestID); err != nil {
		return GuestMergePreview{}, err
	}
	var preview GuestMergePreview
	err := m.withGuestMergeState(ctx, sourceGuestID, targetGuestID,
		func(source, target guestMergeState, tx *sql.Tx) error {
			if err := validateGuestMerge(source, target); err != nil {
				return err
			}
			links, devices, err := guestMergeImpact(ctx, tx, source.ID)
			if err != nil {
				return err
			}
			preview = GuestMergePreview{
				SourceGuestID: source.ID, SourceGuestName: source.Name,
				TargetGuestID: target.ID, TargetGuestName: target.Name,
				LinkCount: links, DeviceCount: devices,
			}
			return nil
		})
	return preview, err
}

// MergeGuest 把 source（B）并入 target（A）。链接改归、B 凭证作废、B 状态
// 标记和每条链接的 merge 时间线事件在同一个事务里完成；不发通知，也不能撤回。
func (m *Manager) MergeGuest(ctx context.Context, viewer identity.Viewer,
	sourceGuestID, targetGuestID int64) (GuestMergeResult, error) {
	if !viewer.IsWebSession() {
		return GuestMergeResult{}, ErrGuestManagementNotAllowed
	}
	if targetGuestID == 0 {
		return GuestMergeResult{}, ErrGuestMergeTargetRequired
	}
	if err := m.ensureGuestMergeManageable(ctx, viewer, sourceGuestID, targetGuestID); err != nil {
		return GuestMergeResult{}, err
	}
	now := m.clk.Now()
	var result GuestMergeResult
	err := m.withGuestMergeState(ctx, sourceGuestID, targetGuestID,
		func(source, target guestMergeState, tx *sql.Tx) error {
			if err := validateGuestMerge(source, target); err != nil {
				return err
			}
			links, devices, err := guestMergeImpact(ctx, tx, source.ID)
			if err != nil {
				return err
			}
			rows, err := tx.QueryContext(ctx, `
				SELECT id, ticket_seq, note FROM guest_links
				WHERE guest_id = ? ORDER BY ticket_seq, id`, source.ID)
			if err != nil {
				return err
			}
			mergeLinks := []mergeableGuestLink{}
			for rows.Next() {
				var link mergeableGuestLink
				if err := rows.Scan(&link.ID, &link.TicketSeq, &link.Note); err != nil {
					rows.Close()
					return err
				}
				mergeLinks = append(mergeLinks, link)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			if _, err := tx.ExecContext(ctx, `
				UPDATE guest_links
				SET guest_id = ?, credential_holder_id = ?
				WHERE guest_id = ?`, target.ID, target.HolderID, source.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM guest_credential_devices WHERE credential_holder_id = ?`,
				source.HolderID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE guest_credential_holders
				SET access_code_hash = NULL, access_code_failures = 0, access_code_locked_at = NULL
				WHERE id = ?`, source.HolderID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE guests
				SET home_key_hash = NULL, home_key_ciphertext = NULL,
				    merged_into_guest_id = ?, merged_at = ?
				WHERE id = ?`, target.ID, now.Format(time.RFC3339Nano), source.ID); err != nil {
				return err
			}
			actor := authorOf(viewer)
			for _, link := range mergeLinks {
				if err := m.insertAssignmentEvent(ctx, tx, link.TicketSeq, actor,
					link.Note, target.Name, "merge", now); err != nil {
					return err
				}
			}
			result = GuestMergeResult{
				SourceGuestID: source.ID, TargetGuestID: target.ID,
				LinkCount: links, DeviceCount: devices,
			}
			return nil
		})
	return result, err
}

func (m *Manager) ensureGuestMergeManageable(ctx context.Context, viewer identity.Viewer,
	sourceGuestID, targetGuestID int64) error {
	if sourceGuestID == 0 || targetGuestID == 0 {
		return ErrGuestNotFound
	}
	if sourceGuestID == targetGuestID {
		return ErrGuestMergeSelf
	}
	for _, id := range []int64{sourceGuestID, targetGuestID} {
		if err := m.ensureGuestManageable(ctx, viewer, id); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) withGuestMergeState(ctx context.Context, sourceGuestID, targetGuestID int64,
	fn func(source, target guestMergeState, tx *sql.Tx) error) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	source, err := guestMergeStateByID(ctx, tx, sourceGuestID)
	if err != nil {
		return err
	}
	target, err := guestMergeStateByID(ctx, tx, targetGuestID)
	if err != nil {
		return err
	}
	if err := fn(source, target, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func guestMergeStateByID(ctx context.Context, tx *sql.Tx, guestID int64) (guestMergeState, error) {
	var (
		state      guestMergeState
		disabled   sql.NullString
		mergedAt   sql.NullString
		mergedInto sql.NullInt64
	)
	err := tx.QueryRowContext(ctx, `
		SELECT gu.id, gu.name, h.id, gu.disabled_at, gu.merged_at, gu.merged_into_guest_id
		FROM guests gu
		JOIN guest_credential_holders h ON h.owner_type = 'guest' AND h.owner_id = gu.id
		WHERE gu.id = ?`, guestID).
		Scan(&state.ID, &state.Name, &state.HolderID, &disabled, &mergedAt, &mergedInto)
	if errors.Is(err, sql.ErrNoRows) {
		return guestMergeState{}, ErrGuestNotFound
	}
	if err != nil {
		return guestMergeState{}, err
	}
	state.Disabled = disabled.Valid && disabled.String != ""
	state.Merged = mergedAt.Valid || mergedInto.Valid
	return state, nil
}

func validateGuestMerge(source, target guestMergeState) error {
	if source.ID == target.ID {
		return ErrGuestMergeSelf
	}
	if source.Merged {
		return ErrGuestAlreadyMerged
	}
	if target.Merged {
		return ErrGuestMergeTargetMerged
	}
	if target.Disabled {
		return ErrGuestMergeTargetDisabled
	}
	return nil
}

func guestMergeImpact(ctx context.Context, tx *sql.Tx, sourceGuestID int64) (links, devices int, err error) {
	if err = tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM guest_links WHERE guest_id = ?`, sourceGuestID).Scan(&links); err != nil {
		return 0, 0, err
	}
	var holderID int64
	if err = tx.QueryRowContext(ctx, `
		SELECT id FROM guest_credential_holders
		WHERE owner_type = 'guest' AND owner_id = ?`, sourceGuestID).Scan(&holderID); err != nil {
		return 0, 0, err
	}
	if err = tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM guest_credential_devices WHERE credential_holder_id = ?`,
		holderID).Scan(&devices); err != nil {
		return 0, 0, err
	}
	return links, devices, nil
}
