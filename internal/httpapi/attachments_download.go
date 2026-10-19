// 附件下载（#33）：GET /api/v1/attachments/{att_N}/{filename}。
// 与工单相同的凭证（会话 cookie / Agent token / 访客设备凭证）。
// Content-Disposition：图片、PDF、.txt、.log 以 inline 返回，其余（含 HTML、SVG）
// 一律 attachment；所有响应都带 X-Content-Type-Options: nosniff。
package httpapi

import (
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// handleAttachmentDownload 处理 GET /api/v1/attachments/{att_N}/{filename}。
func (a *API) handleAttachmentDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "下载附件用 GET")
		return
	}
	segs := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/attachments/"), "/")
	if len(segs) != 2 || segs[0] == "" || segs[1] == "" {
		WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "附件地址形如 /api/v1/attachments/att_1/<文件名>",
			"地址从工单读取接口的 attachments[].url 拿，不要自己拼")
		return
	}
	ref, rawName := segs[0], segs[1]
	filename, err := url.PathUnescape(rawName)
	if err != nil {
		filename = rawName
	}
	att, err := a.atts.Get(r.Context(), ref)
	if err != nil {
		WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "没有这个附件",
			"附件引用从上传响应或工单读取接口拿")
		return
	}
	if att.State != "attached" {
		WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "附件还在待用状态，不能下载",
			"先在说明或回复里引用它（attachment_ids 或正文 att: 引用）")
		return
	}
	if filename != att.Filename {
		WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "地址里的文件名与附件不符",
			"用工单读取接口 attachments[].url 里的完整地址")
		return
	}

	if _, ok := bearerToken(r); ok {
		viewer, ok := a.viewerFromRequest(w, r)
		if !ok {
			return
		}
		if viewer.Kind == identity.ViewerGuest && viewer.GuestLink.TicketSeq != att.TicketSeq {
			WriteError(w, http.StatusNotFound, CodeTicketNotFound, "没有这张工单",
				"访客链接只能访问它所指向的那张工单")
			return
		}
		if !a.attachmentTicketVisible(w, r, viewer, att) {
			return
		}
	} else {
		viewer, ok := a.viewerFromRequest(w, r)
		if !ok {
			return
		}
		if viewer.Kind == identity.ViewerGuest && viewer.GuestLink.TicketSeq != att.TicketSeq {
			WriteError(w, http.StatusNotFound, CodeTicketNotFound, "没有这张工单",
				"访客链接只能访问它所指向的那张工单")
			return
		}
		if !a.attachmentTicketVisible(w, r, viewer, att) {
			return
		}
	}

	content, err := os.ReadFile(a.atts.Path(att.ID))
	if err != nil {
		if os.IsNotExist(err) {
			WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "附件文件缺失", "")
			return
		}
		panic(err)
	}
	if r.Method == http.MethodGet {
		viewer, ok := viewerFromContext(r.Context())
		if !ok {
			viewer, ok = a.viewerFromRequest(w, r)
			if !ok {
				return
			}
		}
		if err := a.accessRecords.RecordDownload(r.Context(), att, viewer); err != nil {
			panic(err)
		}
	}

	disposition := "inline"
	if !attachments.IsInline(att.Filename) {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", contentTypeOf(att.Filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType(disposition, map[string]string{"filename": att.Filename}))
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// attachmentTicketVisible 把附件地址反查到工单后走统一可见范围判断；被拒
// 响应与不存在的附件地址一致。
func (a *API) attachmentTicketVisible(w http.ResponseWriter, r *http.Request, viewer identity.Viewer, att attachments.AttachmentInfo) bool {
	if viewer.Kind != identity.ViewerMember {
		return true
	}
	full, err := a.tickets.ByRef(r.Context(), strconv.FormatInt(att.TicketSeq, 10))
	if err != nil {
		a.writeTicketError(w, r, att.Ref(), err)
		return false
	}
	visible, err := a.tickets.CanSee(r.Context(), viewer, full)
	if err != nil {
		panic(err)
	}
	if !visible {
		if err := a.accessRecords.RecordDenied(r.Context(), att.TicketSeq, viewer, att); err != nil {
			panic(err)
		}
		WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "没有这个附件",
			"附件引用从上传响应或工单读取接口拿")
		return false
	}
	return true
}

// contentTypeOf 按扩展名给 Content-Type；文本类补 charset，未知的用
// application/octet-stream（配合 nosniff，浏览器不会自作聪明地嗅探）。
func contentTypeOf(filename string) string {
	ext := ""
	if i := strings.LastIndexByte(filename, '.'); i >= 0 {
		ext = strings.ToLower(filename[i:])
	}
	switch ext {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".txt", ".log":
		return "text/plain; charset=utf-8"
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}
