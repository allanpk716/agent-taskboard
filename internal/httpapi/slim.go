// 瘦身包的 HTTP 侧（#283/#285）：预览、生成、列出、下载、确认移出与恢复都只接受管理员。
package httpapi

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
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
	info, err := file.Stat()
	if err != nil {
		panic(err)
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	signedURL, hasExternalURL, err := a.slim.PackageObjectURL(r.Context(), name, disposition)
	if err != nil {
		panic(err)
	}
	if hasExternalURL {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Location", signedURL)
		w.WriteHeader(http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", disposition)
	http.ServeContent(w, r, name, info.ModTime(), file)
}

const slimRestoreMaxBytes = 1 << 30

type slimRestoreUpload struct {
	File     *os.File
	Selected []string
}

// handleAdminSlimRestore 处理 POST /api/v1/admin/slim/restore。
func (a *API) handleAdminSlimRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "恢复瘦身包用 POST", "")
		return
	}
	viewer, ok := a.adminWebSessionFromRequest(w, r, "恢复瘦身包只对管理员开放")
	if !ok {
		return
	}
	upload, closeUpload, ok := a.slimRestoreUploadFile(w, r)
	if !ok {
		return
	}
	defer closeUpload()
	readerAt, size, ok := archiveUploadReader(w, upload.File)
	if !ok {
		return
	}
	result, err := a.slim.Restore(r.Context(), readerAt, size, upload.Selected, viewer)
	if errors.Is(err, slim.ErrTicketSelectionRequired) {
		WriteError(w, http.StatusBadRequest, CodeSlimSelectionRequired,
			"恢复瘦身包时必须勾选至少一张工单", "按工单逐张选择要恢复的附件")
		return
	}
	if writeSlimRestorePackageError(w, err) {
		return
	}
	if errors.Is(err, slim.ErrPackageStale) {
		WriteError(w, http.StatusConflict, CodeSlimPackageStale,
			"恢复过程中工单或附件状态发生变化", "重新检查瘦身包后再恢复")
		return
	}
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusOK, newSlimRestoreResultView(result))
}

// handleAdminSlimRestoreInspect 处理 POST /api/v1/admin/slim/restore/inspect。
func (a *API) handleAdminSlimRestoreInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "检查瘦身包用 POST", "")
		return
	}
	if _, ok := a.adminWebSessionFromRequest(w, r, "检查瘦身包只对管理员开放"); !ok {
		return
	}
	upload, closeUpload, ok := a.slimRestoreUploadFile(w, r)
	if !ok {
		return
	}
	defer closeUpload()
	readerAt, size, ok := archiveUploadReader(w, upload.File)
	if !ok {
		return
	}
	inspection, err := a.slim.InspectRestore(r.Context(), readerAt, size)
	if writeSlimRestorePackageError(w, err) {
		return
	}
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusOK, newSlimRestoreInspectionView(inspection))
}

func (a *API) slimRestoreUploadFile(w http.ResponseWriter, r *http.Request) (*slimRestoreUpload, func(), bool) {
	r.Body = http.MaxBytesReader(w, r.Body, slimRestoreMaxBytes)
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "上传瘦身包的 multipart 表单无效",
			"用 multipart/form-data 上传，文件字段名 package；文件最大 1 GB")
		return nil, func() {}, false
	}
	reader := multipart.NewReader(r.Body, params["boundary"])
	var upload slimRestoreUpload
	cleanup := func() {
		if upload.File != nil {
			name := upload.File.Name()
			_ = upload.File.Close()
			_ = os.Remove(name)
			upload.File = nil
		}
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				WriteError(w, http.StatusRequestEntityTooLarge, CodeAttachmentTooLarge,
					"上传瘦身包不能超过 1 GB", "")
			} else {
				WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "上传瘦身包的 multipart 表单无效",
					"用 multipart/form-data 上传，文件字段名 package；文件最大 1 GB")
			}
			return nil, func() {}, false
		}
		field := part.FormName()
		if field == "package" {
			if upload.File != nil {
				part.Close()
				cleanup()
				WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "multipart 里 package 字段重复", "")
				return nil, func() {}, false
			}
			upload.File, err = a.slim.CreateUploadFile()
			if err == nil {
				_, err = io.Copy(upload.File, part)
			}
			closeErr := part.Close()
			if err == nil {
				err = closeErr
			}
			if err == nil {
				err = upload.File.Sync()
			}
			if err == nil {
				_, err = upload.File.Seek(0, io.SeekStart)
			}
			if err != nil {
				cleanup()
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					WriteError(w, http.StatusRequestEntityTooLarge, CodeAttachmentTooLarge,
						"上传瘦身包不能超过 1 GB", "")
				} else {
					WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "保存上传的瘦身包失败", "")
				}
				return nil, func() {}, false
			}
			continue
		}
		if field == "ticket_numbers" {
			value, readErr := readArchiveFormPart(part)
			closeErr := part.Close()
			if readErr == nil {
				readErr = closeErr
			}
			if readErr != nil {
				cleanup()
				WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "读取 ticket_numbers 字段失败", "")
				return nil, func() {}, false
			}
			for _, item := range strings.Split(value, ",") {
				if item = strings.TrimSpace(item); item != "" {
					upload.Selected = append(upload.Selected, item)
				}
			}
			continue
		}
		_ = part.Close()
	}
	if upload.File == nil {
		cleanup()
		WriteError(w, http.StatusUnprocessableEntity, CodeFileRequired, "multipart 里缺少瘦身包文件",
			"文件字段名必须是 package")
		return nil, func() {}, false
	}
	return &upload, cleanup, true
}

func writeSlimRestorePackageError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, slim.ErrRestorePackageInvalid):
		WriteError(w, http.StatusUnprocessableEntity, CodeSlimPackageInvalid, "瘦身包校验失败", err.Error())
	case errors.Is(err, slim.ErrRestoreFormatUnsupported):
		WriteError(w, http.StatusUnprocessableEntity, CodeSlimFormatUnsupported,
			"瘦身包格式版本太高", err.Error())
	default:
		return false
	}
	return true
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
	Drafts          []slim.DraftSummary     `json:"drafts"`
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
	drafts := p.Drafts
	if drafts == nil {
		drafts = []slim.DraftSummary{}
	}
	return slimPreviewView{TicketCount: p.TicketCount(), AttachmentCount: p.AttachmentCount(),
		AttachmentBytes: p.AttachmentBytes(), Tickets: tickets, Drafts: drafts}
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

type slimRestoreCandidateView struct {
	Number          string `json:"number"`
	Title           string `json:"title"`
	AttachmentCount int    `json:"attachment_count"`
	AttachmentBytes int64  `json:"attachment_bytes"`
	Restorable      bool   `json:"restorable"`
	Reason          string `json:"reason"`
}

type slimRestoreInspectionView struct {
	FormatVersion   int                        `json:"format_version"`
	PackageName     string                     `json:"package_name"`
	GeneratedAt     string                     `json:"generated_at"`
	TicketCount     int                        `json:"ticket_count"`
	AttachmentCount int                        `json:"attachment_count"`
	AttachmentBytes int64                      `json:"attachment_bytes"`
	Tickets         []slimRestoreCandidateView `json:"tickets"`
}

type slimRestoreResultView struct {
	PackageName string                     `json:"package_name"`
	RestoredAt  string                     `json:"restored_at"`
	Restored    []slimRestoreCandidateView `json:"restored"`
	Rejected    []slimRestoreCandidateView `json:"rejected"`
}

func newSlimRestoreCandidateView(ticket slim.RestoreTicket) slimRestoreCandidateView {
	return slimRestoreCandidateView{Number: ticket.Number, Title: ticket.Title,
		AttachmentCount: ticket.AttachmentCount, AttachmentBytes: ticket.AttachmentBytes,
		Restorable: ticket.Restorable, Reason: ticket.Reason}
}

func newSlimRestoreInspectionView(inspection slim.RestoreInspection) slimRestoreInspectionView {
	tickets := make([]slimRestoreCandidateView, 0, len(inspection.Tickets))
	for _, ticket := range inspection.Tickets {
		tickets = append(tickets, newSlimRestoreCandidateView(ticket))
	}
	return slimRestoreInspectionView{FormatVersion: inspection.FormatVersion,
		PackageName: inspection.PackageName, GeneratedAt: inspection.GeneratedAt.Format(time.RFC3339),
		TicketCount: len(tickets), AttachmentCount: inspection.AttachmentCount,
		AttachmentBytes: inspection.AttachmentBytes, Tickets: tickets}
}

func newSlimRestoreResultView(result slim.RestoreResult) slimRestoreResultView {
	restored := make([]slimRestoreCandidateView, 0, len(result.Restored))
	for _, ticket := range result.Restored {
		restored = append(restored, newSlimRestoreCandidateView(ticket))
	}
	rejected := make([]slimRestoreCandidateView, 0, len(result.Rejected))
	for _, ticket := range result.Rejected {
		rejected = append(rejected, newSlimRestoreCandidateView(ticket))
	}
	return slimRestoreResultView{PackageName: result.PackageName,
		RestoredAt: result.RestoredAt.Format(time.RFC3339), Restored: restored, Rejected: rejected}
}
