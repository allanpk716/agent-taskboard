// 存储统计的 HTTP 侧（#234）：GET /api/v1/admin/storage 返回数据库、附件目录、
// 备份目录的字节数，以及按关闭月份分组的可归档工单。统计查询与目录清点在
// internal/storage，本文件只做鉴权、参数校验与视图组装。
package httpapi

import (
	"net/http"
	"strconv"
	"time"
)

// handleAdminStorage 处理：
//
//	GET /api/v1/admin/storage?closed_months=N  存储统计（仅管理员网页会话）
func (a *API) handleAdminStorage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	if _, ok := a.adminWebSessionFromRequest(w, r,
		"存储统计只对管理员开放；访客请通过成员处理工单"); !ok {
		return
	}
	closedMonths := defaultClosedMonths
	if raw := r.URL.Query().Get("closed_months"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || !validClosedMonths(n) {
			WriteError(w, http.StatusBadRequest, CodeInvalidClosedMonths,
				closedMonthsMessage, closedMonthsHint)
			return
		}
		closedMonths = n
	}
	stats, err := a.storage.Stats(r.Context(), closedMonths)
	if err != nil {
		panic(err)
	}
	months := make([]storageMonthView, 0, len(stats.Archivable))
	for _, m := range stats.Archivable {
		view := storageMonthView{
			Month: m.Month, TicketCount: m.TicketCount, AttachmentBytes: m.AttachmentBytes,
			Tickets: make([]storageTicketView, 0, len(m.Tickets)),
		}
		for _, ticket := range m.Tickets {
			view.Tickets = append(view.Tickets, storageTicketView{
				Number: ticket.Number, Title: ticket.Title, AttachmentBytes: ticket.AttachmentBytes,
			})
		}
		months = append(months, view)
	}
	writeJSON(w, http.StatusOK, storageStatsView{
		DatabaseBytes:    stats.DatabaseBytes,
		AttachmentsBytes: stats.AttachmentsBytes,
		BackupsBytes:     stats.BackupsBytes,
		ClosedMonths:     stats.ClosedMonths,
		Cutoff:           stats.Cutoff.Format(time.RFC3339),
		Archivable:       months,
	})
}

// storageStatsView 是 GET /api/v1/admin/storage 的响应（archivable 新月份在前）。
type storageStatsView struct {
	DatabaseBytes    int64              `json:"database_bytes"`
	AttachmentsBytes int64              `json:"attachments_bytes"`
	BackupsBytes     int64              `json:"backups_bytes"`
	ClosedMonths     int                `json:"closed_months"`
	Cutoff           string             `json:"cutoff"`
	Archivable       []storageMonthView `json:"archivable"`
}

// storageMonthView 是一个关闭月份的可归档汇总。
type storageMonthView struct {
	Month           string              `json:"month"`
	TicketCount     int64               `json:"ticket_count"`
	AttachmentBytes int64               `json:"attachment_bytes"`
	Tickets         []storageTicketView `json:"tickets"`
}

type storageTicketView struct {
	Number          string `json:"number"`
	Title           string `json:"title"`
	AttachmentBytes int64  `json:"attachment_bytes"`
}
