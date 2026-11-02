// 瘦身包的 HTTP 侧（#283）：预览、生成、列出、下载与确认移出都只接受管理员。
package httpapi

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/slim"
)

type slimSelectionRequest struct {
	ClosedMonths  *int     `json:"closed_months"`
	TicketNumbers []string `json:"ticket_numbers"`
}

// handleAdminSlimCandidates 处理 GET /api/v1/admin/slim/candidates。
func (a *API) handleAdminSlimCandidates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "瘦身候选用 GET", "")
		return
	}
	if _, ok := a.adminWebSessionFromRequest(w, r, "查看瘦身候选只对管理员开放"); !ok {
		return
	}
	months := defaultClosedMonths
	if raw := r.URL.Query().Get("closed_months"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || !validClosedMonths(n) {
			WriteError(w, http.StatusBadRequest, CodeInvalidClosedMonths, closedMonthsMessage, closedMonthsHint)
			return
		}
		months = n
	}
	candidates, err := a.slim.Candidates(r.Context(), months)
	if err != nil {
		panic(err)
	}
	views := make([]slimCandidateMonthView, 0, len(candidates))
	for _, month := range candidates {
		views = append(views, newSlimCandidateMonthView(month))
	}
	writeJSON(w, http.StatusOK, slimCandidatesView{ClosedMonths: months, Months: views})
}

// handleAdminSlimPackages 处理：
//
//	GET  /api/v1/admin/slim/packages       列出未过期暂存包
//	POST /api/v1/admin/slim/packages       生成瘦身包
//	GET  /api/v1/admin/slim/packages/{name}/download 下载
func (a *API) handleAdminSlimPackages(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/admin/slim/packages" {
		if strings.HasSuffix(r.URL.Path, "/remove") {
			a.removeSlimPackage(w, r)
			return
		}
		a.downloadSlimPackage(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"瘦身包集合支持 GET 列表和 POST 生成", "")
		return
	}
	viewer, ok := a.adminWebSessionFromRequest(w, r, "瘦身只对管理员开放")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		packages, err := a.slim.List(r.Context())
		if err != nil {
			panic(err)
		}
		views := make([]slimPackageView, 0, len(packages))
		for _, pkg := range packages {
			views = append(views, newSlimPackageView(pkg))
		}
		writeJSON(w, http.StatusOK, struct {
			Count    int               `json:"count"`
			Packages []slimPackageView `json:"packages"`
		}{Count: len(views), Packages: views})
		return
	}
	req, ok := a.decodeSlimSelection(w, r)
	if !ok {
		return
	}
	months, _ := req.months()
	pkg, err := a.slim.Generate(r.Context(), req.TicketNumbers, months, viewer)
	if errors.Is(err, slim.ErrGenerateInProgress) {
		WriteError(w, http.StatusConflict, CodeSlimGenerateConflict,
			"已有瘦身包正在生成", "请等当前生成完成后重试")
		return
	}
	if writeNotSlimmableError(w, err) {
		return
	}
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusCreated, newSlimPackageView(pkg))
}

type slimRemoveRequest struct {
	Saved *bool `json:"saved"`
}

func (a *API) removeSlimPackage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "确认瘦身移出用 POST", "")
		return
	}
	viewer, ok := a.adminWebSessionFromRequest(w, r, "确认瘦身移出只对管理员开放")
	if !ok {
		return
	}
	var req slimRemoveRequest
	if !decodeJSON(w, r, &req, `{"saved":true}`) {
		return
	}
	if req.Saved == nil || !*req.Saved {
		WriteError(w, http.StatusBadRequest, CodeSlimSavedRequired,
			"必须先确认“我已保存瘦身包”", "下载并保存瘦身包后再从服务器移出附件")
		return
	}
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/slim/packages/"), "/remove")
	if name == "" || strings.Contains(name, "/") {
		WriteError(w, http.StatusNotFound, CodeSlimNotFound, "没有这个瘦身包", "")
		return
	}
	removal, err := a.slim.Remove(r.Context(), name, viewer)
	switch {
	case errors.Is(err, slim.ErrPackageNotFound):
		WriteError(w, http.StatusNotFound, CodeSlimNotFound, "瘦身包不存在或已过期清除", "")
	case errors.Is(err, slim.ErrAlreadyRemoved):
		WriteError(w, http.StatusConflict, CodeSlimRemoved,
			"这个瘦身包对应的附件已经移出", "如需取回附件，请使用已保存的瘦身包恢复")
	case errors.Is(err, slim.ErrPackageStale):
		WriteError(w, http.StatusConflict, CodeSlimPackageStale,
			"工单或附件在生成瘦身包后有变化", "请重新生成瘦身包后再确认移出")
	case err != nil:
		panic(err)
	default:
		writeJSON(w, http.StatusOK, newSlimRemoveView(removal))
	}
}

func (a *API) downloadSlimPackage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "下载瘦身包用 GET", "")
		return
	}
	if _, ok := a.adminWebSessionFromRequest(w, r, "下载瘦身包只对管理员开放"); !ok {
		return
	}
	suffix := "/download"
	if !strings.HasSuffix(r.URL.Path, suffix) {
		WriteError(w, http.StatusNotFound, CodeSlimNotFound, "没有这个瘦身包", "")
		return
	}
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/slim/packages/"), suffix)
	if name == "" || strings.HasSuffix(name, "/") || strings.Contains(name, "/") {
		WriteError(w, http.StatusNotFound, CodeSlimNotFound, "没有这个瘦身包", "")
		return
	}
	file, _, err := a.slim.Open(name)
	if errors.Is(err, slim.ErrPackageNotFound) {
		WriteError(w, http.StatusNotFound, CodeSlimNotFound, "瘦身包不存在或已过期清除", "")
		return
	}
	if err != nil {
		panic(err)
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+url.PathEscape(name)+`"`)
	if _, err := io.Copy(w, file); err != nil {
		panic(err)
	}
}

func (r slimSelectionRequest) months() (int, bool) {
	if r.ClosedMonths == nil {
		return defaultClosedMonths, true
	}
	return *r.ClosedMonths, validClosedMonths(*r.ClosedMonths)
}

// handleAdminSlimPreview 处理 POST /api/v1/admin/slim/preview。
func (a *API) handleAdminSlimPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "预览瘦身用 POST", "")
		return
	}
	if _, ok := a.adminWebSessionFromRequest(w, r, "瘦身只对管理员开放"); !ok {
		return
	}
	req, ok := a.decodeSlimSelection(w, r)
	if !ok {
		return
	}
	months, _ := req.months()
	preview, err := a.slim.Preview(r.Context(), req.TicketNumbers, months)
	if writeNotSlimmableError(w, err) {
		return
	}
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusOK, newSlimPreviewView(preview))
}

func (a *API) decodeSlimSelection(w http.ResponseWriter, r *http.Request) (slimSelectionRequest, bool) {
	var req slimSelectionRequest
	if !decodeJSON(w, r, &req, `{"closed_months":3,"ticket_numbers":["20261016-001"]}`) {
		return req, false
	}
	if _, ok := req.months(); !ok {
		WriteError(w, http.StatusBadRequest, CodeInvalidClosedMonths,
			closedMonthsMessage, closedMonthsHint)
		return req, false
	}
	return req, true
}

func writeNotSlimmableError(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, slim.ErrNotSlimmable) {
		return false
	}
	WriteError(w, http.StatusUnprocessableEntity, CodeNotSlimmable,
		"选择里有不可瘦身的工单", err.Error())
	return true
}

type slimPreviewView struct {
	TicketCount     int                     `json:"ticket_count"`
	AttachmentCount int                     `json:"attachment_count"`
	AttachmentBytes int64                   `json:"attachment_bytes"`
	Tickets         []slimPreviewTicketView `json:"tickets"`
}

type slimPreviewTicketView struct {
	Number          string `json:"number"`
	Title           string `json:"title"`
	ClosedAt        string `json:"closed_at"`
	AttachmentCount int    `json:"attachment_count"`
	AttachmentBytes int64  `json:"attachment_bytes"`
}

type slimCandidatesView struct {
	ClosedMonths int                      `json:"closed_months"`
	Months       []slimCandidateMonthView `json:"months"`
}

type slimCandidateMonthView struct {
	Month           string                    `json:"month"`
	TicketCount     int                       `json:"ticket_count"`
	AttachmentCount int                       `json:"attachment_count"`
	AttachmentBytes int64                     `json:"attachment_bytes"`
	Tickets         []slimCandidateTicketView `json:"tickets"`
}

type slimCandidateTicketView struct {
	Number          string `json:"number"`
	Title           string `json:"title"`
	AttachmentCount int    `json:"attachment_count"`
	AttachmentBytes int64  `json:"attachment_bytes"`
}

func newSlimCandidateMonthView(month slim.MonthCandidates) slimCandidateMonthView {
	tickets := make([]slimCandidateTicketView, 0, len(month.Tickets))
	for _, ticket := range month.Tickets {
		tickets = append(tickets, slimCandidateTicketView{Number: ticket.Number, Title: ticket.Title,
			AttachmentCount: ticket.AttachmentCount, AttachmentBytes: ticket.AttachmentBytes})
	}
	return slimCandidateMonthView{Month: month.Month, TicketCount: month.TicketCount,
		AttachmentCount: month.AttachmentCount, AttachmentBytes: month.AttachmentBytes, Tickets: tickets}
}

func newSlimPreviewView(p slim.Preview) slimPreviewView {
	tickets := make([]slimPreviewTicketView, 0, len(p.Tickets))
	for _, ticket := range p.Tickets {
		tickets = append(tickets, slimPreviewTicketView{
			Number: ticket.Number, Title: ticket.Title, ClosedAt: ticket.ClosedAt.Format(time.RFC3339),
			AttachmentCount: ticket.AttachmentCount, AttachmentBytes: ticket.AttachmentBytes,
		})
	}
	return slimPreviewView{TicketCount: p.TicketCount(), AttachmentCount: p.AttachmentCount(),
		AttachmentBytes: p.AttachmentBytes(), Tickets: tickets}
}

type slimPackageView struct {
	Name                   string   `json:"name"`
	TicketNumbers          []string `json:"ticket_numbers"`
	TicketCount            int      `json:"ticket_count"`
	AttachmentCount        int      `json:"attachment_count"`
	AttachmentBytes        int64    `json:"attachment_bytes"`
	GeneratedAt            string   `json:"generated_at"`
	ExpiresAt              string   `json:"expires_at"`
	OperatorName           string   `json:"operator_name"`
	DownloadURL            string   `json:"download_url"`
	RemovedAttachmentCount int      `json:"removed_attachment_count"`
}

func newSlimPackageView(pkg slim.PackageInfo) slimPackageView {
	if pkg.TicketNumbers == nil {
		pkg.TicketNumbers = []string{}
	}
	return slimPackageView{Name: pkg.Name, TicketNumbers: pkg.TicketNumbers,
		TicketCount: pkg.TicketCount, AttachmentCount: pkg.AttachmentCount,
		AttachmentBytes: pkg.AttachmentBytes, GeneratedAt: pkg.GeneratedAt.Format(time.RFC3339),
		ExpiresAt: pkg.ExpiresAt.Format(time.RFC3339), OperatorName: pkg.OperatorName,
		DownloadURL:            "/api/v1/admin/slim/packages/" + url.PathEscape(pkg.Name) + "/download",
		RemovedAttachmentCount: pkg.RemovedAttachmentCount}
}

type slimRemoveView struct {
	PackageName     string   `json:"package_name"`
	TicketNumbers   []string `json:"ticket_numbers"`
	AttachmentCount int      `json:"attachment_count"`
	AttachmentBytes int64    `json:"attachment_bytes"`
	RemovedAt       string   `json:"removed_at"`
	CleanupFailures int      `json:"cleanup_failures"`
}

func newSlimRemoveView(removal slim.Removal) slimRemoveView {
	if removal.TicketNumbers == nil {
		removal.TicketNumbers = []string{}
	}
	return slimRemoveView{PackageName: removal.PackageName, TicketNumbers: removal.TicketNumbers,
		AttachmentCount: removal.AttachmentCount, AttachmentBytes: removal.AttachmentBytes,
		RemovedAt: removal.RemovedAt.Format(time.RFC3339), CleanupFailures: removal.CleanupFailures}
}
