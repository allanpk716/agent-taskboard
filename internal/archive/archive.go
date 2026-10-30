// Package archive 负责生成工单归档包（#235）。本包只做选择校验、预览与
// 包内容组装；HTTP 鉴权和响应视图在 internal/httpapi。
package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
)

// ErrNotArchivable 表示选择里混入不存在、未关闭或关闭未满 N 个月的工单。
var ErrNotArchivable = errors.New("ticket not archivable")

// ErrGenerateInProgress 表示同一个服务进程里已有一次归档生成正在执行。
var ErrGenerateInProgress = errors.New("archive generation in progress")

// TicketSummary 是预览与归档清单里的一张可归档工单。
type TicketSummary struct {
	Seq             int64
	Number          string
	Title           string
	ClosedAt        time.Time
	AttachmentBytes int64
}

// DraftSummary 是归档前将被丢弃的一份未发出草稿。
type DraftSummary struct {
	ID            string   `json:"id"`
	TicketNumber  string   `json:"ticket_number"`
	Kind          string   `json:"kind"`
	MemberName    string   `json:"member_name"`
	AgentName     string   `json:"agent_name"`
	Version       int64    `json:"version"`
	UpdatedAt     string   `json:"updated_at"`
	AttachmentIDs []string `json:"attachment_ids"`
	seq           int64
	ticketSeq     int64
}

// Preview 是一次归档选择的汇总。
type Preview struct {
	Tickets []TicketSummary
	Drafts  []DraftSummary
}

// TicketCount 返回选中工单数。
func (p Preview) TicketCount() int { return len(p.Tickets) }

// AttachmentBytes 返回选中工单的附件总大小。
func (p Preview) AttachmentBytes() int64 {
	var total int64
	for _, t := range p.Tickets {
		total += t.AttachmentBytes
	}
	return total
}

// Service 提供归档预览、生成、下载与暂存清除。
type Service struct {
	db             *sql.DB
	clk            clock.Clock
	dataDir        string
	attachmentsDir string
	atts           *attachments.Manager
	generateMu     sync.Mutex
}

// NewService 建归档服务。
func NewService(db *sql.DB, dataDir, attachmentsDir string, atts *attachments.Manager, c clock.Clock) *Service {
	return &Service{db: db, clk: c, dataDir: dataDir, attachmentsDir: attachmentsDir, atts: atts}
}

func (s *Service) stageDir() string { return filepath.Join(s.dataDir, "archive-packages") }

// Preview 校验选择并把工单与未发出草稿汇总给管理员确认。
func (s *Service) Preview(ctx context.Context, ticketNumbers []string, closedMonths int) (Preview, error) {
	tickets, err := s.selection(ctx, ticketNumbers, closedMonths)
	if err != nil {
		return Preview{}, err
	}
	drafts, err := s.pendingDrafts(ctx, tickets)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Tickets: tickets, Drafts: drafts}, nil
}

// selection 按工单编号读取选择，逐张确认当前已关闭且最近一次关闭不晚于 cutoff。
func (s *Service) selection(ctx context.Context, ticketNumbers []string, closedMonths int) ([]TicketSummary, error) {
	if len(ticketNumbers) == 0 {
		return nil, fmt.Errorf("%w：没有选择工单", ErrNotArchivable)
	}
	seen := make(map[string]bool, len(ticketNumbers))
	placeholders := make([]string, 0, len(ticketNumbers))
	args := make([]any, 0, len(ticketNumbers))
	for _, number := range ticketNumbers {
		number = strings.TrimSpace(number)
		if number == "" || seen[number] {
			return nil, fmt.Errorf("%w：编号为空或重复", ErrNotArchivable)
		}
		seen[number] = true
		placeholders = append(placeholders, "?")
		args = append(args, number)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.seq, t.number, t.title, (
			SELECT e.at FROM timeline_events e
			WHERE e.ticket_seq = t.seq AND e.type = 'closed'
			ORDER BY e.seq DESC LIMIT 1
		)
		FROM tickets t
		WHERE t.status = 'closed' AND t.number IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	byNumber := map[string]TicketSummary{}
	for rows.Next() {
		var t TicketSummary
		var closedAt sql.NullString
		if err := rows.Scan(&t.Seq, &t.Number, &t.Title, &closedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if !closedAt.Valid {
			continue
		}
		at, err := time.ParseInLocation(time.RFC3339Nano, closedAt.String, clock.Beijing)
		if err != nil {
			continue
		}
		t.ClosedAt = at
		byNumber[t.Number] = t
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	cutoff := s.clk.Now().AddDate(0, -closedMonths, 0)
	out := make([]TicketSummary, 0, len(ticketNumbers))
	for _, number := range ticketNumbers {
		t, ok := byNumber[strings.TrimSpace(number)]
		if !ok || t.ClosedAt.After(cutoff) {
			return nil, fmt.Errorf("%w：%s", ErrNotArchivable, number)
		}
		out = append(out, t)
	}
	sizes, err := s.attachmentBytes(ctx, out)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].AttachmentBytes = sizes[out[i].Seq]
	}
	return out, nil
}

func (s *Service) attachmentBytes(ctx context.Context, tickets []TicketSummary) (map[int64]int64, error) {
	placeholders := make([]string, 0, len(tickets))
	args := make([]any, 0, len(tickets))
	for _, t := range tickets {
		placeholders = append(placeholders, "?")
		args = append(args, t.Seq)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.ticket_seq, COALESCE(SUM(a.size), 0)
		FROM attachments a
		WHERE a.ticket_seq IN (`+strings.Join(placeholders, ",")+`) AND a.draft_id IS NULL
		GROUP BY a.ticket_seq`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var seq, size int64
		if err := rows.Scan(&seq, &size); err != nil {
			return nil, err
		}
		out[seq] = size
	}
	return out, rows.Err()
}

type draftRequest struct {
	BodyMD        string   `json:"body_md"`
	AttachmentIDs []string `json:"attachment_ids"`
}

func (s *Service) pendingDrafts(ctx context.Context, tickets []TicketSummary) ([]DraftSummary, error) {
	placeholders := make([]string, 0, len(tickets))
	args := make([]any, 0, len(tickets))
	for _, t := range tickets {
		placeholders = append(placeholders, "?")
		args = append(args, t.Seq)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.kind, d.version, d.updated_at, d.request_json, t.seq, t.number, m.name,
		       COALESCE((
		           SELECT a.name FROM draft_versions v
		           LEFT JOIN agent_tokens a ON a.id = v.author_agent_id
		           WHERE v.draft_id = d.id ORDER BY v.version DESC LIMIT 1
		       ), '')
		FROM drafts d
		JOIN tickets t ON t.seq = d.ticket_seq
		JOIN members m ON m.id = d.member_id
		WHERE d.status = 'pending' AND d.ticket_seq IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY d.ticket_seq, d.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DraftSummary{}
	for rows.Next() {
		var d DraftSummary
		var id int64
		var request string
		if err := rows.Scan(&id, &d.Kind, &d.Version, &d.UpdatedAt, &request, &d.ticketSeq, &d.TicketNumber,
			&d.MemberName, &d.AgentName); err != nil {
			return nil, err
		}
		d.ID = fmt.Sprintf("dft_%d", id)
		d.seq = id
		var req draftRequest
		_ = json.Unmarshal([]byte(request), &req)
		d.AttachmentIDs = req.AttachmentIDs
		if d.AttachmentIDs == nil {
			d.AttachmentIDs = []string{}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
