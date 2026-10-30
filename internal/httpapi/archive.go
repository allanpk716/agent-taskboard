// 归档包的 HTTP 侧（#235）：预览、生成、列出暂存包与下载都只接受管理员网页会话。
package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/archive"
)

type archiveSelectionRequest struct {
	ClosedMonths  *int     `json:"closed_months"`
	TicketNumbers []string `json:"ticket_numbers"`
}

func (r archiveSelectionRequest) months() (int, bool) {
	if r.ClosedMonths == nil {
		return 3, true
	}
	return *r.ClosedMonths, *r.ClosedMonths >= 1 && *r.ClosedMonths <= 120
}

// handleAdminArchivePreview 处理 POST /api/v1/admin/archive/preview。
func (a *API) handleAdminArchivePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "预览归档用 POST", "")
		return
	}
	if _, ok := a.adminWebSessionFromRequest(w, r, "生成归档包只对管理员开放"); !ok {
		return
	}
	req, ok := a.decodeArchiveSelection(w, r)
	if !ok {
		return
	}
	months, _ := req.months()
	preview, err := a.archive.Preview(r.Context(), req.TicketNumbers, months)
	if errors.Is(err, archive.ErrNotArchivable) {
		WriteError(w, http.StatusBadRequest, CodeTicketNotArchivable,
			"选择里有不存在、未关闭或关闭未满 N 个月的工单", "请从存储统计列出的可归档月份中选择")
		return
	}
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusOK, newArchivePreviewView(preview))
}

func (a *API) decodeArchiveSelection(w http.ResponseWriter, r *http.Request) (archiveSelectionRequest, bool) {
	var req archiveSelectionRequest
	if !decodeJSON(w, r, &req, `{"closed_months":3,"ticket_numbers":["20261016-001"]}`) {
		return req, false
	}
	if _, ok := req.months(); !ok {
		WriteError(w, http.StatusBadRequest, CodeInvalidClosedMonths,
			"closed_months 必须是 1–120 的整数", "不传时默认 3（已关闭满 3 个月）")
		return req, false
	}
	return req, true
}

type archivePreviewView struct {
	TicketCount     int                    `json:"ticket_count"`
	AttachmentBytes int64                  `json:"attachment_bytes"`
	Tickets         []archiveTicketView    `json:"tickets"`
	Drafts          []archive.DraftSummary `json:"drafts"`
}

type archiveTicketView struct {
	Number          string `json:"number"`
	Title           string `json:"title"`
	ClosedAt        string `json:"closed_at"`
	AttachmentBytes int64  `json:"attachment_bytes"`
}

func newArchivePreviewView(p archive.Preview) archivePreviewView {
	tickets := make([]archiveTicketView, 0, len(p.Tickets))
	for _, t := range p.Tickets {
		tickets = append(tickets, archiveTicketView{
			Number: t.Number, Title: t.Title, ClosedAt: t.ClosedAt.Format(time.RFC3339),
			AttachmentBytes: t.AttachmentBytes,
		})
	}
	drafts := p.Drafts
	if drafts == nil {
		drafts = []archive.DraftSummary{}
	}
	return archivePreviewView{TicketCount: p.TicketCount(), AttachmentBytes: p.AttachmentBytes(),
		Tickets: tickets, Drafts: drafts}
}
