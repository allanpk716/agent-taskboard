// 附件下载（#33）：GET /api/v1/attachments/{att_N}/{filename}。
// 与工单相同的凭证（会话 cookie / Agent token / 访客设备凭证）。
// Content-Disposition：图片、PDF、.txt、.log 以 inline 返回，其余（含 HTML、SVG）
// 一律 attachment；所有响应都带 X-Content-Type-Options: nosniff。
package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
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
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	_, err = a.tickets.ForViewer(r.Context(), viewer, strconv.FormatInt(att.TicketSeq, 10))
	var invisible *tickets.TicketInvisibleError
	switch {
	case errors.As(err, &invisible):
		if err := a.accessRecords.RecordDenied(r.Context(), invisible.Seq, invisible.Viewer, att); err != nil {
			panic(err)
		}
		WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "没有这个附件",
			"附件引用从上传响应或工单读取接口拿")
	case err != nil:
		a.writeTicketError(w, r, att.Ref(), err)
	}
	if err != nil {
		return
	}
	draftOwner := att.State == "pending" && att.DraftID != 0 &&
		viewer.Member.ID != 0 && att.Uploader.MemberID == viewer.Member.ID
	if att.State != "attached" && !draftOwner {
		WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "附件还在待用状态，不能下载",
			"先在说明或回复里引用它（attachment_ids 或正文 att: 引用）")
		return
	}
	if filename != att.Filename {
		WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "地址里的文件名与附件不符",
			"用工单读取接口 attachments[].url 里的完整地址")
		return
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
