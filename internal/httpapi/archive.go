// 归档包的 HTTP 侧（#235）：预览、生成、列出暂存包与下载都只接受管理员网页会话。
package httpapi

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/archive"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

type archiveSelectionRequest struct {
	ClosedMonths  *int     `json:"closed_months"`
	TicketNumbers []string `json:"ticket_numbers"`
}

func (r archiveSelectionRequest) months() (int, bool) {
	if r.ClosedMonths == nil {
		return defaultClosedMonths, true
	}
	return *r.ClosedMonths, validClosedMonths(*r.ClosedMonths)
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
	if writeNotArchivableError(w, err) {
		return
	}
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusOK, newArchivePreviewView(preview))
}

// handleAdminArchivePackages 处理：
//
//	GET  /api/v1/admin/archive/packages        列出未过期暂存包
//	POST /api/v1/admin/archive/packages       生成归档包
//	GET  /api/v1/admin/archive/packages/{name}/download 下载
//	POST /api/v1/admin/archive/packages/{name}/remove  移除包内工单
func (a *API) handleAdminArchivePackages(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/admin/archive/packages" {
		if strings.HasSuffix(r.URL.Path, "/remove") {
			a.removeArchivePackage(w, r)
			return
		}
		a.downloadArchivePackage(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"归档包集合支持 GET 列表和 POST 生成", "")
		return
	}
	viewer, ok := a.adminWebSessionFromRequest(w, r, "生成归档包只对管理员开放")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		packages, err := a.archive.List(r.Context())
		if err != nil {
			panic(err)
		}
		views := make([]archivePackageView, 0, len(packages))
		for _, pkg := range packages {
			views = append(views, newArchivePackageView(pkg))
		}
		writeJSON(w, http.StatusOK, struct {
			Count    int                  `json:"count"`
			Packages []archivePackageView `json:"packages"`
		}{Count: len(views), Packages: views})
		return
	}
	req, ok := a.decodeArchiveSelection(w, r)
	if !ok {
		return
	}
	months, _ := req.months()
	pkg, err := a.archive.Generate(r.Context(), req.TicketNumbers, months, viewer)
	if errors.Is(err, archive.ErrGenerateInProgress) {
		WriteError(w, http.StatusConflict, CodeArchiveGenerateConflict,
			"已有归档包正在生成", "请等当前生成完成后重试")
		return
	}
	if writeNotArchivableError(w, err) {
		return
	}
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusCreated, newArchivePackageView(pkg))
}

type archiveRemoveRequest struct {
	Saved *bool `json:"saved"`
}

// removeArchivePackage 处理 POST /api/v1/admin/archive/packages/{name}/remove。
func (a *API) removeArchivePackage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "移除归档工单用 POST", "")
		return
	}
	viewer, ok := a.adminWebSessionFromRequest(w, r, "从服务器移除归档工单只对管理员开放")
	if !ok {
		return
	}
	var req archiveRemoveRequest
	if !decodeJSON(w, r, &req, `{"saved":true}`) {
		return
	}
	if req.Saved == nil || !*req.Saved {
		WriteError(w, http.StatusBadRequest, CodeArchiveSavedRequired,
			"必须先确认“我已保存归档包”", "下载并保存归档包后再从服务器移除")
		return
	}
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/archive/packages/"), "/remove")
	if name == "" || strings.Contains(name, "/") {
		WriteError(w, http.StatusNotFound, CodeArchiveNotFound, "没有这个归档包", "")
		return
	}
	removal, err := a.archive.Remove(r.Context(), name, viewer)
	switch {
	case errors.Is(err, archive.ErrPackageNotFound):
		WriteError(w, http.StatusNotFound, CodeArchiveNotFound, "归档包不存在或已过期清除", "")
	case errors.Is(err, archive.ErrAlreadyRemoved):
		WriteError(w, http.StatusConflict, CodeArchiveRemoved,
			"这个归档包对应的工单已经从服务器移除", "请用已保存的归档包导入需要恢复的工单")
	case err != nil:
		panic(err)
	default:
		writeJSON(w, http.StatusOK, newArchiveRemoveView(removal))
	}
}

func (a *API) downloadArchivePackage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "下载归档包用 GET", "")
		return
	}
	if _, ok := a.adminWebSessionFromRequest(w, r, "下载归档包只对管理员开放"); !ok {
		return
	}
	suffix := "/download"
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/archive/packages/"), suffix)
	if name == "" || strings.HasSuffix(name, "/") || strings.Contains(name, "/") {
		WriteError(w, http.StatusNotFound, CodeArchiveNotFound, "没有这个归档包", "")
		return
	}
	file, _, err := a.archive.Open(name)
	if errors.Is(err, archive.ErrPackageNotFound) {
		WriteError(w, http.StatusNotFound, CodeArchiveNotFound, "归档包不存在或已过期清除", "")
		return
	}
	if err != nil {
		panic(err)
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}

func writeNotArchivableError(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, archive.ErrNotArchivable) {
		return false
	}
	WriteError(w, http.StatusBadRequest, CodeTicketNotArchivable,
		"选择里有不存在、未关闭或关闭未满 N 个月的工单", "请从存储统计列出的可归档月份中选择")
	return true
}

// handleAdminOperations 处理 GET /api/v1/admin/operations。
func (a *API) handleAdminOperations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "管理操作记录用 GET", "")
		return
	}
	if _, ok := a.adminWebSessionFromRequest(w, r, "读取管理操作记录只对管理员开放"); !ok {
		return
	}
	operations, err := a.archive.ListOperations(r.Context())
	if err != nil {
		panic(err)
	}
	views := make([]adminOperationView, 0, len(operations))
	for _, op := range operations {
		views = append(views, newAdminOperationView(op))
	}
	writeJSON(w, http.StatusOK, struct {
		Count      int                  `json:"count"`
		Operations []adminOperationView `json:"operations"`
	}{Count: len(views), Operations: views})
}

func (a *API) decodeArchiveSelection(w http.ResponseWriter, r *http.Request) (archiveSelectionRequest, bool) {
	var req archiveSelectionRequest
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

type archivePackageView struct {
	Name            string   `json:"name"`
	TicketCount     int      `json:"ticket_count"`
	AttachmentBytes int64    `json:"attachment_bytes"`
	GeneratedAt     string   `json:"generated_at"`
	ExpiresAt       string   `json:"expires_at"`
	TicketNumbers   []string `json:"ticket_numbers"`
	DownloadURL     string   `json:"download_url"`
	OperatorName    string   `json:"operator_name"`
}

func newArchivePackageView(pkg archive.PackageInfo) archivePackageView {
	if pkg.TicketNumbers == nil {
		pkg.TicketNumbers = []string{}
	}
	return archivePackageView{Name: pkg.Name, TicketCount: pkg.TicketCount,
		AttachmentBytes: pkg.AttachmentBytes, GeneratedAt: pkg.GeneratedAt.Format(time.RFC3339),
		ExpiresAt: pkg.ExpiresAt.Format(time.RFC3339), TicketNumbers: pkg.TicketNumbers,
		DownloadURL:  "/api/v1/admin/archive/packages/" + url.PathEscape(pkg.Name) + "/download",
		OperatorName: pkg.OperatorName}
}

type archiveRemoveView struct {
	TicketCount     int                    `json:"ticket_count"`
	AttachmentBytes int64                  `json:"attachment_bytes"`
	PackageName     string                 `json:"package_name"`
	RemovedAt       string                 `json:"removed_at"`
	Tombstones      []archiveTombstoneView `json:"tombstones"`
}

type archiveTombstoneView struct {
	Seq          int64  `json:"seq"`
	Number       string `json:"number"`
	Title        string `json:"title"`
	ClosedAt     string `json:"closed_at"`
	ArchivedAt   string `json:"archived_at"`
	OperatorName string `json:"operator_name"`
	PackageName  string `json:"package_name"`
}

func newArchiveRemoveView(removal archive.Removal) archiveRemoveView {
	tombstones := make([]archiveTombstoneView, 0, len(removal.Tombstones))
	for _, t := range removal.Tombstones {
		tombstones = append(tombstones, archiveTombstoneView{
			Seq: t.Seq, Number: t.Number, Title: t.Title,
			ClosedAt: t.ClosedAt.Format(time.RFC3339), ArchivedAt: t.ArchivedAt.Format(time.RFC3339),
			OperatorName: t.OperatorName, PackageName: t.PackageName,
		})
	}
	return archiveRemoveView{TicketCount: len(tombstones), AttachmentBytes: removal.AttachmentBytes,
		PackageName: removal.PackageName, RemovedAt: removal.RemovedAt.Format(time.RFC3339),
		Tombstones: tombstones}
}

func writeArchivedTicket(w http.ResponseWriter, tombstone archive.Tombstone) {
	WriteErrorExtra(w, http.StatusGone, CodeTicketArchived, "这张工单已归档并从服务器移除",
		"需要查看时请联系管理员导入归档包 "+tombstone.PackageName, map[string]any{
			"archived_ticket": archiveTombstoneView{
				Seq: tombstone.Seq, Number: tombstone.Number, Title: tombstone.Title,
				ClosedAt: tombstone.ClosedAt.Format(time.RFC3339), ArchivedAt: tombstone.ArchivedAt.Format(time.RFC3339),
				OperatorName: tombstone.OperatorName, PackageName: tombstone.PackageName,
			},
		})
}

func (a *API) writeArchivedCredential(w http.ResponseWriter, r *http.Request, credential string) bool {
	tombstone, err := a.archive.TombstoneByCredentialHash(r.Context(), identity.CredentialHash(credential))
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		panic(err)
	}
	writeArchivedTicket(w, tombstone)
	return true
}

type adminOperationView struct {
	ID               int64    `json:"id"`
	OperationType    string   `json:"operation_type"`
	OperatorMemberID int64    `json:"operator_member_id"`
	OperatorName     string   `json:"operator_name"`
	At               string   `json:"at"`
	TicketNumbers    []string `json:"ticket_numbers"`
	PackageName      string   `json:"package_name"`
}

func newAdminOperationView(op archive.Operation) adminOperationView {
	if op.TicketNumbers == nil {
		op.TicketNumbers = []string{}
	}
	return adminOperationView{ID: op.ID, OperationType: op.OperationType,
		OperatorMemberID: op.OperatorMemberID, OperatorName: op.OperatorName, At: op.At.Format(time.RFC3339),
		TicketNumbers: op.TicketNumbers, PackageName: op.PackageName}
}
