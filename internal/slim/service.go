// Package slim 负责工单附件瘦身（#281）：只导出和移出附件，工单本体留在平台。
package slim

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
)

// ErrNotSlimmable 表示选择里没有可瘦身的附件，或工单不满足关闭月数。
var ErrNotSlimmable = errors.New("ticket not slimmable")

// TicketSummary 是一次瘦身选择里的工单。
type TicketSummary struct {
	Seq             int64
	Number          string
	Title           string
	ClosedAt        time.Time
	AttachmentCount int
	AttachmentBytes int64
	attachments     []Attachment
}

// Attachment 是仍留在服务器上的一个可瘦身附件。
type Attachment struct {
	ID         int64
	Ref        string
	Filename   string
	Size       int64
	AttachedTo string
}

// Preview 是管理员确认生成瘦身包前的汇总。
type Preview struct {
	Tickets []TicketSummary
	Drafts  []DraftSummary
}

// DraftSummary 是瘦身预览里提示管理员的一份未发出草稿（与归档预览
// 同一字段口径；瘦身本身不修改、不丢弃草稿）。
type DraftSummary struct {
	ID            string   `json:"id"`
	TicketNumber  string   `json:"ticket_number"`
	Kind          string   `json:"kind"`
	MemberName    string   `json:"member_name"`
	AgentName     string   `json:"agent_name"`
	Version       int64    `json:"version"`
	UpdatedAt     string   `json:"updated_at"`
	AttachmentIDs []string `json:"attachment_ids"`
}

// MonthCandidates 是管理页筛选结果里的一个关闭月份。
type MonthCandidates struct {
	Month           string
	TicketCount     int
	AttachmentCount int
	AttachmentBytes int64
	Tickets         []TicketSummary
}

// TicketCount 返回选中工单数。
func (p Preview) TicketCount() int { return len(p.Tickets) }

// AttachmentCount 返回仍可瘦身的附件数。
func (p Preview) AttachmentCount() int {
	var total int
	for _, t := range p.Tickets {
		total += t.AttachmentCount
	}
	return total
}

// AttachmentBytes 返回仍可瘦身的附件字节数。
func (p Preview) AttachmentBytes() int64 {
	var total int64
	for _, t := range p.Tickets {
		total += t.AttachmentBytes
	}
	return total
}

// Service 提供瘦身预览、包生成、暂存、下载与移出。
type Service struct {
	db             *sql.DB
	clk            clock.Clock
	dataDir        string
	attachmentsDir string
	backupDir      string
	atts           *attachments.Manager
	generateMu     sync.Mutex
}

// NewService 建瘦身服务。
func NewService(db *sql.DB, dataDir, attachmentsDir, backupDir string,
	atts *attachments.Manager, c clock.Clock) *Service {
	return &Service{db: db, clk: c, dataDir: dataDir, attachmentsDir: attachmentsDir,
		backupDir: backupDir, atts: atts}
}

// Preview 校验管理员选择并汇总仍留在服务器上的附件。
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

// Candidates 找出已关闭满 N 个月且仍有附件在服务器上的工单。
func (s *Service) Candidates(ctx context.Context, closedMonths int) ([]MonthCandidates, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.seq, t.number, t.title, (
			SELECT e.at FROM timeline_events e
			WHERE e.ticket_seq = t.seq AND e.type = 'closed'
			ORDER BY e.seq DESC LIMIT 1
		)
		FROM tickets t WHERE t.status = 'closed'`)
	if err != nil {
		return nil, err
	}
	type closedTicket struct {
		summary  TicketSummary
		closedAt string
	}
	closed := []closedTicket{}
	for rows.Next() {
		var item closedTicket
		var at sql.NullString
		if err := rows.Scan(&item.summary.Seq, &item.summary.Number, &item.summary.Title, &at); err != nil {
			rows.Close()
			return nil, err
		}
		if at.Valid {
			item.closedAt = at.String
			closed = append(closed, item)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	cutoff := s.clk.Now().AddDate(0, -closedMonths, 0)
	byMonth := map[string]*MonthCandidates{}
	for _, item := range closed {
		at, err := time.ParseInLocation(time.RFC3339Nano, item.closedAt, clock.Beijing)
		if err != nil || at.After(cutoff) {
			continue
		}
		atts, err := s.slimmableAttachments(ctx, item.summary.Seq)
		if err != nil {
			return nil, err
		}
		if len(atts) == 0 {
			continue
		}
		summary := item.summary
		summary.ClosedAt = at
		summary.attachments = atts
		summary.AttachmentCount = len(atts)
		for _, att := range atts {
			summary.AttachmentBytes += att.Size
		}
		month := at.In(clock.Beijing).Format("2006-01")
		stat := byMonth[month]
		if stat == nil {
			stat = &MonthCandidates{Month: month}
			byMonth[month] = stat
		}
		stat.TicketCount++
		stat.AttachmentCount += summary.AttachmentCount
		stat.AttachmentBytes += summary.AttachmentBytes
		stat.Tickets = append(stat.Tickets, summary)
	}
	out := make([]MonthCandidates, 0, len(byMonth))
	for _, stat := range byMonth {
		sort.Slice(stat.Tickets, func(i, j int) bool { return stat.Tickets[i].Number < stat.Tickets[j].Number })
		out = append(out, *stat)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Month > out[j].Month })
	return out, nil
}

func (s *Service) selection(ctx context.Context, ticketNumbers []string, closedMonths int) ([]TicketSummary, error) {
	if len(ticketNumbers) == 0 {
		return nil, fmt.Errorf("%w：没有选择工单", ErrNotSlimmable)
	}
	seen := map[string]bool{}
	args := make([]any, 0, len(ticketNumbers))
	for _, number := range ticketNumbers {
		number = strings.TrimSpace(number)
		if number == "" || seen[number] {
			return nil, fmt.Errorf("%w：编号为空或重复", ErrNotSlimmable)
		}
		seen[number] = true
		args = append(args, number)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.seq, t.number, t.title, (
			SELECT e.at FROM timeline_events e
			WHERE e.ticket_seq = t.seq AND e.type = 'closed'
			ORDER BY e.seq DESC LIMIT 1
		)
		FROM tickets t
		WHERE t.status = 'closed' AND t.number IN (`+inPlaceholders(len(args))+`)`, args...)
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
	for _, raw := range ticketNumbers {
		number := strings.TrimSpace(raw)
		t, ok := byNumber[number]
		if !ok || t.ClosedAt.After(cutoff) {
			return nil, fmt.Errorf("%w：%s", ErrNotSlimmable, number)
		}
		atts, err := s.slimmableAttachments(ctx, t.Seq)
		if err != nil {
			return nil, err
		}
		if len(atts) == 0 {
			return nil, fmt.Errorf("%w：%s 没有仍在服务器上的附件", ErrNotSlimmable, number)
		}
		t.attachments = atts
		t.AttachmentCount = len(atts)
		for _, att := range atts {
			t.AttachmentBytes += att.Size
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *Service) slimmableAttachments(ctx context.Context, ticketSeq int64) ([]Attachment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, filename, size, COALESCE(attached_to, '')
		FROM attachments
		WHERE ticket_seq = ? AND state = 'attached' AND draft_id IS NULL
		  AND slim_removed_at IS NULL
		ORDER BY id`, ticketSeq)
	if err != nil {
		return nil, err
	}
	all := make([]Attachment, 0)
	for rows.Next() {
		var att Attachment
		if err := rows.Scan(&att.ID, &att.Filename, &att.Size, &att.AttachedTo); err != nil {
			rows.Close()
			return nil, err
		}
		att.Ref = fmt.Sprintf("att_%d", att.ID)
		all = append(all, att)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(all) == 0 {
		return []Attachment{}, nil
	}

	inline, err := s.atts.InlineImageRefs(ctx, ticketSeq)
	if err != nil {
		return nil, err
	}
	out := make([]Attachment, 0, len(all))
	for _, att := range all {
		if attachments.InlineImage(inline, att.Ref, att.Filename) {
			continue
		}
		out = append(out, att)
	}
	return out, nil
}

// pendingDrafts 与归档预览同一口径：列出所选工单上未发出的草稿，
// 供管理页按同样的方式提示（spec 故事 40）。
func (s *Service) pendingDrafts(ctx context.Context, tickets []TicketSummary) ([]DraftSummary, error) {
	args := make([]any, 0, len(tickets))
	for _, t := range tickets {
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
		WHERE d.status = 'pending' AND d.ticket_seq IN (`+inPlaceholders(len(args))+`)
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
		if err := rows.Scan(&id, &d.Kind, &d.Version, &d.UpdatedAt, &request,
			new(int64), &d.TicketNumber, &d.MemberName, &d.AgentName); err != nil {
			return nil, err
		}
		d.ID = fmt.Sprintf("dft_%d", id)
		var req struct {
			AttachmentIDs []string `json:"attachment_ids"`
		}
		_ = json.Unmarshal([]byte(request), &req)
		d.AttachmentIDs = req.AttachmentIDs
		if d.AttachmentIDs == nil {
			d.AttachmentIDs = []string{}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func inPlaceholders(n int) string {
	if n == 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
