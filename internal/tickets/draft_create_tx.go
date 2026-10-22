package tickets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/template"
)

type PreparedCreate struct {
	Input        CreateInput
	Snapshot     *template.Snapshot
	AssigneeID   int64
	AssigneeName string
	Warnings     []FieldWarning
}

func (m *Manager) PrepareCreate(ctx context.Context, in CreateInput) (PreparedCreate, error) {
	validated, warnings, err := m.ValidateCreate(ctx, in)
	if err != nil {
		return PreparedCreate{}, err
	}
	in = validated
	var snapshot *template.Snapshot
	if in.TemplateKey != "" {
		tmpl, err := m.templates.Get(ctx, in.TemplateKey)
		if err != nil {
			return PreparedCreate{}, err
		}
		in.Kind = tmpl.Kind
		snapshot = &template.Snapshot{Key: tmpl.Key, Version: tmpl.Version, Definition: tmpl.Definition}
	}
	prepared := PreparedCreate{Input: in, Snapshot: snapshot, Warnings: warnings}
	if strings.TrimSpace(in.Assignee) != "" {
		prepared.AssigneeID, prepared.AssigneeName, err = m.resolveAssignee(ctx, in.Assignee)
		if err != nil {
			return PreparedCreate{}, err
		}
	}
	return prepared, nil
}

func (m *Manager) CreateTx(ctx context.Context, tx *sql.Tx, prepared PreparedCreate) (CreateResult, []int64, error) {
	in := prepared.Input
	snapshot := prepared.Snapshot
	assigneeID, assigneeName := prepared.AssigneeID, prepared.AssigneeName
	warnings := prepared.Warnings
	now := m.clk.Now()
	date := now.Format("20060102")
	var today int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tickets WHERE number LIKE ?`, date+"-%").Scan(&today); err != nil {
		return CreateResult{}, nil, err
	}
	daily := today + 1
	number := date + "-"
	if daily > 999 {
		number += fmt.Sprintf("%d", daily)
	} else {
		number += fmt.Sprintf("%03d", daily)
	}
	creator := Author{MemberID: in.Creator.Member.ID, MemberName: in.Creator.Member.Name}
	if in.Actor != nil {
		creator = *in.Actor
	} else if in.Creator.Agent != nil {
		creator.AgentID, creator.AgentName = in.Creator.Agent.ID, in.Creator.Agent.Name
	}
	var agentID any
	if creator.AgentID != 0 {
		agentID = creator.AgentID
	}
	t := Ticket{Number: number, Kind: in.Kind, Title: in.Title, BriefMD: in.BriefMD,
		BriefRevision: 1, Status: "open", Creator: creator, AssigneeID: assigneeID,
		AssigneeName: assigneeName, CreatedAt: now, TemplateKey: in.TemplateKey,
		TemplateDef: snapshot, Fields: in.Fields}
	var templateKey, snapshotJSON, fieldsJSON any
	if snapshot != nil {
		t.BriefMD = template.RenderBrief(in.Fields, snapshot.Fields)
		templateKey = snapshot.Key
		if buf, err := json.Marshal(snapshot); err != nil {
			return CreateResult{}, nil, fmt.Errorf("序列化模板快照失败：%w", err)
		} else {
			snapshotJSON = string(buf)
		}
		if buf, err := json.Marshal(in.Fields); err != nil {
			return CreateResult{}, nil, fmt.Errorf("序列化字段值失败：%w", err)
		} else {
			fieldsJSON = string(buf)
		}
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO tickets (number, kind, title, status, brief_md, brief_revision,
		                     creator_member_id, creator_agent_id, assignee_id, created_at, last_change,
		                     template_key, template_version, template_snapshot, fields_json)
		VALUES (?, ?, ?, 'open', ?, 1, ?, ?, ?, ?, 0, ?, ?, ?, ?)
		RETURNING seq`, number, in.Kind, in.Title, t.BriefMD, creator.MemberID, agentID,
		nullInt(assigneeID), now.Format(time.RFC3339Nano), templateKey, snapshotVersion(snapshot),
		snapshotJSON, fieldsJSON).Scan(&t.Seq); err != nil {
		return CreateResult{}, nil, fmt.Errorf("写入工单失败：%w", err)
	}
	_, change, err := m.appendEvent(ctx, tx, t.Seq, now, creator, "created")
	if err != nil {
		return CreateResult{}, nil, err
	}
	if err := m.insertBriefVersionTx(ctx, tx, t, creator, now); err != nil {
		return CreateResult{}, nil, err
	}
	t.LastChange = change
	if _, err := tx.ExecContext(ctx, `UPDATE tickets SET last_change = ? WHERE seq = ?`, change, t.Seq); err != nil {
		return CreateResult{}, nil, err
	}
	assignedSeq := int64(0)
	if assigneeID != 0 {
		assignedSeq, _, err = m.insertAssignedEvent(ctx, tx, t.Seq, creator, "", assigneeName, now)
		if err != nil {
			return CreateResult{}, nil, err
		}
	}
	for _, l := range in.Labels {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ticket_labels (ticket_seq, label) VALUES (?, ?)`, t.Seq, l); err != nil {
			return CreateResult{}, nil, fmt.Errorf("写入标签 %s 失败：%w", l, err)
		}
	}
	var linkCreated *GuestLinkCreated
	if in.GuestLink != nil {
		linkCreated, err = m.createGuestLinkTx(ctx, tx, t.Seq, authorOf(in.Creator), *in.GuestLink, now)
		if err != nil {
			return CreateResult{}, nil, err
		}
	}
	var notified []int64
	if m.notifier != nil {
		after := Full{Ticket: t}
		after.Timeline = []Event{{Seq: 1, At: now, Actor: creator, Type: "created", Change: t.LastChange}}
		event := after.Timeline[0]
		if assignedSeq != 0 {
			assigned := Event{Seq: assignedSeq, At: now, Actor: creator, Type: "assigned",
				AssignTo: assigneeName, Change: t.LastChange}
			after.Timeline = append(after.Timeline, assigned)
			event = assigned
		}
		notified, err = m.notifier.NotifyTx(ctx, tx, NotificationInput{After: after, Actor: creator, Event: event})
		if err != nil {
			return CreateResult{}, nil, err
		}
	}
	return CreateResult{Full: Full{Ticket: t}, Warnings: warnings, GuestLink: linkCreated}, notified, nil
}
