// 归档导入的 HTTP 侧（#237）：检查包与导入都只接受管理员网页会话。
package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/archive"
)

const archiveImportMaxBytes = 1 << 30

// handleAdminArchiveImport 处理 POST /api/v1/admin/archive/import。
func (a *API) handleAdminArchiveImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "导入归档包用 POST", "")
		return
	}
	viewer, ok := a.adminWebSessionFromRequest(w, r, "导入归档包只对管理员开放")
	if !ok {
		return
	}
	file, closeFile, ok := a.archiveUploadFile(w, r, "导入")
	if !ok {
		return
	}
	defer closeFile()
	readerAt, size, ok := archiveUploadReader(w, file)
	if !ok {
		return
	}
	var selected []string
	if values := r.Form["ticket_numbers"]; len(values) > 0 {
		for _, value := range values {
			selected = append(selected, strings.Split(value, ",")...)
		}
	}
	result, err := a.archive.Import(r.Context(), readerAt, size, selected, viewer)
	if writeArchivePackageError(w, err) {
		return
	}
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusOK, newArchiveImportResultView(result))
}

// handleAdminArchiveImportInspect 处理 POST /api/v1/admin/archive/import/inspect。
func (a *API) handleAdminArchiveImportInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "检查归档包用 POST", "")
		return
	}
	if _, ok := a.adminWebSessionFromRequest(w, r, "检查归档包只对管理员开放"); !ok {
		return
	}
	file, closeFile, ok := a.archiveUploadFile(w, r, "检查")
	if !ok {
		return
	}
	defer closeFile()
	readerAt, size, ok := archiveUploadReader(w, file)
	if !ok {
		return
	}
	inspection, err := a.archive.InspectImport(r.Context(), readerAt, size)
	if writeArchivePackageError(w, err) {
		return
	}
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusOK, newArchiveImportInspectionView(inspection))
}

func (a *API) archiveUploadFile(w http.ResponseWriter, r *http.Request, action string) (multipartFile, func(), bool) {
	r.Body = http.MaxBytesReader(w, r.Body, archiveImportMaxBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "上传归档包的 multipart 表单无效",
			"用 multipart/form-data 上传，文件字段名 package；文件最大 1 GB")
		return nil, func() {}, false
	}
	file, header, err := r.FormFile("package")
	if err != nil || header == nil {
		WriteError(w, http.StatusBadRequest, CodeFileRequired, "multipart 里缺少归档包文件",
			"文件字段名必须是 package")
		return nil, func() {}, false
	}
	return file, func() { _ = file.Close() }, true
}

type multipartFile interface {
	io.Reader
	io.ReaderAt
	io.Seeker
}

func archiveUploadReader(w http.ResponseWriter, file multipartFile) (io.ReaderAt, int64, bool) {
	end, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "读取上传的归档包失败", "")
		return nil, 0, false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "读取上传的归档包失败", "")
		return nil, 0, false
	}
	return file, end, true
}

func writeArchivePackageError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, archive.ErrPackageInvalid):
		WriteError(w, http.StatusUnprocessableEntity, CodeArchivePackageInvalid, "归档包校验失败", err.Error())
	case errors.Is(err, archive.ErrFormatUnsupported):
		WriteError(w, http.StatusUnprocessableEntity, CodeArchiveFormatUnsupported,
			"归档包格式版本太高", err.Error())
	default:
		return false
	}
	return true
}

type archiveImportCandidateView struct {
	Seq             int64  `json:"seq"`
	Number          string `json:"number"`
	Title           string `json:"title"`
	ClosedAt        string `json:"closed_at"`
	AttachmentBytes int64  `json:"attachment_bytes"`
	Importable      bool   `json:"importable"`
	Reason          string `json:"reason"`
}

type archiveImportInspectionView struct {
	FormatVersion   int                          `json:"format_version"`
	PlatformVersion string                       `json:"platform_version"`
	PackageName     string                       `json:"package_name"`
	GeneratedAt     string                       `json:"generated_at"`
	TicketCount     int                          `json:"ticket_count"`
	AttachmentBytes int64                        `json:"attachment_bytes"`
	Tickets         []archiveImportCandidateView `json:"tickets"`
}

func newArchiveImportInspectionView(inspection archive.ImportInspection) archiveImportInspectionView {
	tickets := make([]archiveImportCandidateView, 0, len(inspection.Tickets))
	for _, ticket := range inspection.Tickets {
		tickets = append(tickets, archiveImportCandidateView{
			Seq: ticket.Seq, Number: ticket.Number, Title: ticket.Title,
			ClosedAt: ticket.ClosedAt.Format(time.RFC3339), AttachmentBytes: ticket.AttachmentBytes,
			Importable: ticket.Importable, Reason: ticket.Reason,
		})
	}
	return archiveImportInspectionView{FormatVersion: inspection.FormatVersion,
		PlatformVersion: inspection.PlatformVersion, PackageName: inspection.PackageName,
		GeneratedAt: inspection.GeneratedAt.Format(time.RFC3339), TicketCount: len(tickets),
		AttachmentBytes: inspection.AttachmentBytes, Tickets: tickets}
}

type archiveImportResultView struct {
	PackageName string                       `json:"package_name"`
	ImportedAt  string                       `json:"imported_at"`
	Imported    []archiveImportCandidateView `json:"imported"`
	Rejected    []archiveImportCandidateView `json:"rejected"`
}

func newArchiveImportResultView(result archive.ImportResult) archiveImportResultView {
	imported := make([]archiveImportCandidateView, 0, len(result.Imported))
	for _, ticket := range result.Imported {
		imported = append(imported, newArchiveImportCandidateView(ticket))
	}
	rejected := make([]archiveImportCandidateView, 0, len(result.Rejected))
	for _, ticket := range result.Rejected {
		rejected = append(rejected, newArchiveImportCandidateView(ticket))
	}
	return archiveImportResultView{PackageName: result.PackageName,
		ImportedAt: result.ImportedAt.Format(time.RFC3339), Imported: imported, Rejected: rejected}
}

func newArchiveImportCandidateView(ticket archive.ImportTicket) archiveImportCandidateView {
	return archiveImportCandidateView{Seq: ticket.Seq, Number: ticket.Number, Title: ticket.Title,
		ClosedAt: ticket.ClosedAt.Format(time.RFC3339), AttachmentBytes: ticket.AttachmentBytes,
		Importable: ticket.Importable, Reason: ticket.Reason}
}
