// 附件的 HTTP 侧（#33）：两步上传的第一步 POST …/attachments（multipart）。
// 下载接口（GET /api/v1/attachments/{att_N}/{filename}）见 attachments_download.go。
package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// uploadAttachmentMaxMemory 是 multipart 表单在内存里的缓冲上限（超出部分落临时文件）。
const uploadAttachmentMaxMemory = 8 << 20

// handleTicketAttachments 处理：
//
//	POST /api/v1/tickets/{id}/attachments  上传一个待用附件（两步上传的第一步）
func (a *API) handleTicketAttachments(w http.ResponseWriter, r *http.Request, ref string) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "上传附件用 POST（multipart 字段 file）")
		return
	}
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	full, ok := a.loadTicketForViewer(w, r, viewer, ref)
	if !ok {
		return
	}
	// 访客链接在关闭的工单上只读（上传同回复）；成员仍可上传（决策 2）。
	if !capsOf(viewer, full).Upload {
		WriteError(w, http.StatusConflict, CodeTicketClosed, "工单已关闭，访客链接只读",
			"如需继续，请联系成员重新开启工单（成员回复不会重开工单）")
		return
	}

	// MaxBytesReader 做粗粒度兜底（multipart 信封也有开销）；精确的单文件
	// 上限按实际字节数在附件模块里检查。
	r.Body = http.MaxBytesReader(w, r.Body, a.atts.Limits().MaxFileBytes+(1<<20))
	if err := r.ParseMultipartForm(uploadAttachmentMaxMemory); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			WriteError(w, http.StatusRequestEntityTooLarge, CodeAttachmentTooLarge,
				"单个文件不能超过 "+strconv.Itoa(a.atts.Limits().MaxFileMB())+" MB",
				nasHint(full.Number, ""))
			return
		}
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "请求体不是合法的 multipart 表单",
			`用 multipart/form-data 上传，文件字段名 file；示例：curl -F "file=@截图.png"`)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		WriteError(w, http.StatusUnprocessableEntity, CodeFileRequired, "缺少文件字段",
			`multipart 里要有名为 file 的文件字段（curl -F "file=@文件名"）`)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "读文件内容失败："+err.Error(), "")
		return
	}

	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		info, markdown, err := a.atts.Upload(r.Context(), full.Seq, header.Filename, content, attachments.PrincipalOf(viewer))
		if err != nil {
			a.writeAttachmentError(w, err, full.Number, header.Filename)
			return
		}
		writeJSON(w, http.StatusCreated, attachmentUploadView{
			ID:       info.Ref(),
			Filename: info.Filename,
			Size:     info.Size,
			State:    info.State,
			Markdown: markdown,
			Note:     "附件处于待用状态：在回复或说明里引用它（正文或 attachment_ids）；24 小时内未引用会被清除",
		})
	})
}

// attachmentUploadView 是上传成功的响应（201）。
type attachmentUploadView struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	State    string `json:"state"`
	Markdown string `json:"markdown"`
	Note     string `json:"note"`
}

// writeAttachmentError 把附件模块的错误映射成 HTTP 错误；三种 413 的 hint
// 都提示改用 NAS 路径（外部文件引用，见 GLOSSARY）。
func (a *API) writeAttachmentError(w http.ResponseWriter, err error, ticketNumber, filename string) {
	var tooLarge *attachments.TooLargeError
	var quota *attachments.QuotaExceededError
	var count *attachments.CountExceededError
	switch {
	case errors.As(err, &tooLarge):
		WriteError(w, http.StatusRequestEntityTooLarge, CodeAttachmentTooLarge,
			err.Error(), nasHint(ticketNumber, filename))
	case errors.As(err, &quota):
		WriteErrorExtra(w, http.StatusRequestEntityTooLarge, CodeTicketQuotaExceeded,
			err.Error(), nasHint(ticketNumber, filename),
			map[string]any{"used_mb": a.atts.Limits().UsedMB(quota.Used), "ticket_limit_mb": a.atts.Limits().MaxTicketMB()})
	case errors.As(err, &count):
		WriteError(w, http.StatusRequestEntityTooLarge, CodeAttachmentCountExceeded,
			err.Error(), nasHint(ticketNumber, ""))
	case errors.Is(err, attachments.ErrNotUsable):
		WriteError(w, http.StatusUnprocessableEntity, CodeAttachmentNotUsable,
			err.Error(), "先调上传接口拿到 att: 引用，再在自己发的回复或说明里引用它")
	case errors.Is(err, attachments.ErrInvalidFilename):
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidFilename, err.Error(), "")
	case errors.Is(err, attachments.ErrFileRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeFileRequired, err.Error(), "")
	case errors.Is(err, attachments.ErrNotFound):
		WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, err.Error(), "")
	default:
		panic(err)
	}
}

// nasHint 是三种 413 的统一提示：大文件改用 NAS 路径（外部文件引用）。
func nasHint(ticketNumber, filename string) string {
	if filename == "" {
		return "把文件放到 NAS，在说明或回复正文中写路径，例如 \\\\nas01\\共享\\工单\\" + ticketNumber
	}
	return "把文件放到 NAS，在说明或回复正文中写路径，例如 \\\\nas01\\共享\\工单\\" + ticketNumber + "\\" + filename
}

// attachmentBasePath 是附件下载地址的路径部分（filename 由调用方转义拼上）。
func attachmentBasePath(ref string) string { return "/api/v1/attachments/" + ref + "/" }

// attachmentProjection 组装工单 GET 里的附件列表与用量（#33）。列表只含已挂上的
// （attached），用量把待用的也算进去。访客凭证拿到的 url 是与链接绑定的短时效
// 签名地址（浏览器 <img> 带不了 Bearer 头，orchestrator 决议 9）。
func (a *API) attachmentProjection(ctx context.Context, full tickets.Full, viewer identity.Viewer) ([]attachmentView, attachmentUsage, error) {
	atts, err := a.atts.ByTicketState(ctx, full.Seq, "attached")
	if err != nil {
		return nil, attachmentUsage{}, err
	}
	used, _, err := a.atts.Usage(ctx, full.Seq)
	if err != nil {
		return nil, attachmentUsage{}, err
	}
	limits := a.atts.Limits()
	usage := attachmentUsage{
		UsedBytes: used, UsedMB: limits.UsedMB(used),
		TicketLimitMB: limits.MaxTicketMB(), FileLimitMB: limits.MaxFileMB(),
		MaxFilesPerPost: limits.MaxFilesPerPost, MaxImagesPerPost: limits.MaxImagesPerPost,
	}
	views := make([]attachmentView, 0, len(atts))
	for _, att := range atts {
		url := attachmentBasePath(att.Ref()) + url.PathEscape(att.Filename)
		if viewer.Kind == identity.ViewerGuest {
			if url, err = a.atts.GuestURL(ctx, att, viewer.GuestLink.ID); err != nil {
				return nil, attachmentUsage{}, err
			}
		}
		views = append(views, attachmentView{
			ID: att.Ref(), Filename: att.Filename, Size: att.Size, URL: url, AttachedTo: att.AttachedTo,
		})
	}
	return views, usage, nil
}
