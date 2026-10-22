package tickets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/template"
)

func (m *Manager) ReplyTx(ctx context.Context, tx *sql.Tx, full Full, viewer identity.Viewer, in ReplyInput) (ReplyResult, []int64, error) {
	now := m.clk.Now()
	author := authorOf(viewer)
	if in.Actor != nil {
		author = *in.Actor
	}
	refs := attachments.ParsePostRefs(in.BodyMD, in.AttachmentIDs)
	if viewer.Kind == identity.ViewerGuest {
		author = guestAuthorOf(viewer, in.AuthorName, in.ViaAgent)
	}
	var replies int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM timeline_events WHERE ticket_seq = ? AND type = 'reply'`, full.Seq).
		Scan(&replies); err != nil {
		return ReplyResult{}, nil, err
	}
	replyNo := int64(replies) + 1
	if err := m.atts.ClaimTx(ctx, tx, full.Seq, attachments.PrincipalOf(viewer), refs,
		attachments.ClaimAllPending, "回复 "+strconv.FormatInt(replyNo, 10), now); err != nil {
		return ReplyResult{}, nil, err
	}
	eventSeq, change, err := m.appendEvent(ctx, tx, full.Seq, now, author, "reply")
	if err != nil {
		return ReplyResult{}, nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE timeline_events SET body_md = ?, reply_no = ?, deliver = ?, attachment_ids = ? WHERE ticket_seq = ? AND seq = ?`,
		in.BodyMD, replyNo, boolToInt(in.Deliver), attachmentIDsJSON(refs), full.Seq, eventSeq); err != nil {
		return ReplyResult{}, nil, fmt.Errorf("写入回复失败：%w", err)
	}
	if err := touchLastChange(ctx, tx, full.Seq, change); err != nil {
		return ReplyResult{}, nil, err
	}
	var notified []int64
	if m.notifier != nil {
		after := full
		after.LastChange = change
		after.Timeline = append(append([]Event{}, full.Timeline...), Event{
			Seq: eventSeq, Change: change, At: now, Actor: author, Type: "reply",
			BodyMD: in.BodyMD, ReplyNo: replyNo, Deliver: in.Deliver,
		})
		notified, err = m.notifier.NotifyTx(ctx, tx, NotificationInput{
			Before: full, After: after, Actor: author, Event: after.Timeline[len(after.Timeline)-1],
		})
		if err != nil {
			return ReplyResult{}, nil, err
		}
	}
	return ReplyResult{ReplyNo: replyNo, TimelineSeq: eventSeq, Deliver: in.Deliver,
		AuthorName: author.DisplayName()}, notified, nil
}

func (m *Manager) ReviseTx(ctx context.Context, tx *sql.Tx, full Full, viewer identity.Viewer, in ReviseInput) (Ticket, []int64, error) {
	author := authorOf(viewer)
	if in.Actor != nil {
		author = *in.Actor
	}
	if viewer.Kind == identity.ViewerGuest {
		if full.Status != "open" {
			return Ticket{}, nil, ErrTicketClosed
		}
		if !viewer.GuestLink.CanEditBrief {
			return Ticket{}, nil, ErrLinkCannotEditBrief
		}
		author = guestAuthorOf(viewer, in.AuthorName, in.ViaAgent)
	} else if viewer.Member.ID != full.Creator.MemberID {
		return Ticket{}, nil, ErrNotBriefOwner
	}
	if in.BaseRevision == 0 {
		return Ticket{}, nil, ErrBaseRevisionRequired
	}
	if in.BaseRevision != full.BriefRevision {
		return Ticket{}, nil, &RevisionConflictError{BaseRevision: in.BaseRevision, CurrentRevision: full.BriefRevision}
	}
	title := full.Title
	if in.Title != nil {
		title = strings.TrimSpace(*in.Title)
		if title == "" {
			return Ticket{}, nil, ErrTitleRequired
		}
	}
	brief := full.BriefMD
	if in.BriefMD != nil {
		if full.TemplateDef != nil {
			return Ticket{}, nil, ErrBriefMDWithTemplate
		}
		brief = *in.BriefMD
	}
	fields := full.Fields
	if in.Fields != nil {
		if full.TemplateDef == nil {
			return Ticket{}, nil, ErrFieldsWithoutTemplate
		}
		if err := validateFieldsAgainst(full.TemplateDef.Fields, in.Fields); err != nil {
			return Ticket{}, nil, err
		}
		for _, f := range full.TemplateDef.Fields {
			if f.Type != template.TypeDimension {
				continue
			}
			v := strings.TrimSpace(in.Fields[f.Key])
			if v == "" || v == template.UnsureOption {
				continue
			}
		}
		fields = make(map[string]string, len(full.Fields)+len(in.Fields))
		for k, v := range full.Fields {
			fields[k] = v
		}
		for k, v := range in.Fields {
			fields[k] = v
		}
		brief = template.RenderBrief(fields, full.TemplateDef.Fields)
	}
	attachmentIDs := full.BriefAttachmentIDs
	if in.AttachmentIDs != nil {
		attachmentIDs = append([]string{}, in.AttachmentIDs...)
	}
	briefRefs := attachments.ParsePostRefs(brief, attachmentIDs)
	now := m.clk.Now()
	var notified []int64
	revision := full.BriefRevision + 1
	var err error
	var fieldsJSON, attachmentIDsJSON any
	if attachmentIDs != nil {
		if buf, err := json.Marshal(attachmentIDs); err != nil {
			return Ticket{}, nil, fmt.Errorf("序列化说明附件失败：%w", err)
		} else {
			attachmentIDsJSON = string(buf)
		}
	}
	var result sql.Result
	if in.Fields != nil {
		if buf, err := json.Marshal(fields); err != nil {
			return Ticket{}, nil, fmt.Errorf("序列化字段值失败：%w", err)
		} else {
			fieldsJSON = string(buf)
		}
		result, err = tx.ExecContext(ctx,
			`UPDATE tickets SET title = ?, brief_md = ?, fields_json = ?, brief_revision = ?, brief_attachment_ids = ?
			 WHERE seq = ? AND brief_revision = ?`,
			title, brief, fieldsJSON, revision, attachmentIDsJSON, full.Seq, in.BaseRevision)
	} else {
		result, err = tx.ExecContext(ctx,
			`UPDATE tickets SET title = ?, brief_md = ?, brief_revision = ?, brief_attachment_ids = ?
			 WHERE seq = ? AND brief_revision = ?`,
			title, brief, revision, attachmentIDsJSON, full.Seq, in.BaseRevision)
	}
	if err != nil {
		return Ticket{}, nil, fmt.Errorf("写入修订失败：%w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return Ticket{}, nil, &RevisionConflictError{BaseRevision: in.BaseRevision, CurrentRevision: revision}
	}
	if err := m.atts.ClaimTx(ctx, tx, full.Seq, attachments.PrincipalOf(viewer), briefRefs,
		attachments.ClaimOnlyPending, "说明", now); err != nil {
		return Ticket{}, nil, err
	}
	if err := m.atts.ValidateBriefIDsTx(ctx, tx, full.Seq, attachments.PrincipalOf(viewer), attachmentIDs); err != nil {
		return Ticket{}, nil, err
	}
	eventSeq, change, err := m.appendEvent(ctx, tx, full.Seq, now, author, "brief_revised")
	if err != nil {
		return Ticket{}, nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE timeline_events SET revision = ?, attachment_ids = ? WHERE ticket_seq = ? AND seq = ?`,
		revision, attachmentIDsJSON, full.Seq, eventSeq); err != nil {
		return Ticket{}, nil, fmt.Errorf("写入修订事件失败：%w", err)
	}
	if err := touchLastChange(ctx, tx, full.Seq, change); err != nil {
		return Ticket{}, nil, err
	}
	snapshot := full.Ticket
	snapshot.Title, snapshot.BriefMD, snapshot.Fields = title, brief, fields
	snapshot.BriefRevision, snapshot.BriefAttachmentIDs = revision, attachmentIDs
	if err := m.insertBriefVersionTx(ctx, tx, snapshot, author, now); err != nil {
		return Ticket{}, nil, err
	}
	if m.notifier != nil {
		after := full
		after.Title, after.BriefMD, after.Fields = title, brief, fields
		after.BriefRevision, after.BriefAttachmentIDs = revision, attachmentIDs
		after.LastChange = change
		after.Timeline = append(append([]Event{}, full.Timeline...), Event{
			Seq: eventSeq, Change: change, At: now, Actor: author, Type: "brief_revised",
			Revision: revision, AttachmentIDs: attachmentIDs,
		})
		notified, err = m.notifier.NotifyTx(ctx, tx, NotificationInput{
			Before: full, After: after, Actor: author, Event: after.Timeline[len(after.Timeline)-1],
		})
		if err != nil {
			return Ticket{}, nil, err
		}
	}
	full.Title, full.BriefMD, full.Fields = title, brief, fields
	full.BriefRevision, full.BriefAttachmentIDs, full.LastChange = revision, attachmentIDs, change
	return full.Ticket, notified, nil
}
