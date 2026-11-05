// 附件下载（#33）：GET /api/v1/attachments/{att_N}/{filename}。
// 与工单相同的凭证（会话 cookie / Agent token / 访客设备凭证）。
// Content-Disposition：图片、PDF、.txt、.log 以 inline 返回，其余（含 HTML、SVG）
// 一律 attachment；所有响应都带 X-Content-Type-Options: nosniff。
package httpapi

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
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
	var viewer identity.Viewer
	var ok bool
	if r.URL.Query().Get("sig") != "" && !requestHasCredential(r) {
		viewer, ok = a.viewerFromSignedAttachment(w, r, ref)
		if !ok {
			return
		}
	} else {
		viewer, ok = a.viewerFromRequest(w, r)
		if !ok {
			return
		}
	}
	_, err = a.tickets.ForViewer(r.Context(), viewer, strconv.FormatInt(att.TicketSeq, 10))
	if err != nil {
		if r.URL.Query().Get("sig") != "" && !requestHasCredential(r) {
			a.writeUnauthenticatedAttachment(w, r)
			return
		}
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
		return
	}
	draftOwner := att.State == "pending" && att.DraftID != 0 &&
		viewer.Member.ID != 0 && att.Uploader.MemberID == viewer.Member.ID
	if att.State != "attached" && !draftOwner {
		WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "附件还在待用状态，不能下载",
			"先在说明或回复里引用它（attachment_ids 或正文 att: 引用）")
		return
	}
	// 已瘦身移出的附件：说明去哪了（含包名），不返回笼统的 404（#284）。
	if att.SlimRemovedAt != nil {
		WriteErrorExtra(w, http.StatusGone, CodeAttachmentSlimmed,
			"这个附件已于 "+att.SlimRemovedAt.In(clock.Beijing).Format("2006-01-02")+
				" 瘦身移出服务器，见瘦身包 "+att.SlimPackageName,
			"需要这个文件时请联系管理员，用瘦身包恢复；Agent 看到瘦身标记时不要尝试下载",
			map[string]any{"slim_package_name": att.SlimPackageName, "slim_removed_at": *att.SlimRemovedAt})
		return
	}
	if filename != att.Filename {
		WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "地址里的文件名与附件不符",
			"用工单读取接口 attachments[].url 里的完整地址")
		return
	}

	disposition := "inline"
	if !attachments.IsInline(att.Filename) {
		disposition = "attachment"
	}
	signedURL, hasExternalURL, err := a.atts.Storage().DownloadURL(r.Context(), att.ID,
		attachments.DownloadURLOpts{Disposition: mime.FormatMediaType(disposition, map[string]string{"filename": att.Filename})})
	if err != nil {
		panic(err)
	}
	if hasExternalURL {
		if _, exists := a.atts.Storage().Stat(r.Context(), att.ID); !exists {
			WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "附件文件缺失", "")
			return
		}
		if err := a.accessRecords.RecordDownload(r.Context(), att, viewer); err != nil {
			panic(err)
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Location", signedURL)
		w.WriteHeader(http.StatusFound)
		return
	}

	body, size, err := a.atts.Storage().Open(r.Context(), att.ID)
	if err != nil {
		if errors.Is(err, attachments.ErrObjectNotFound) {
			WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "附件文件缺失", "")
			return
		}
		panic(err)
	}
	defer body.Close()
	if r.Method == http.MethodGet {
		if err := a.accessRecords.RecordDownload(r.Context(), att, viewer); err != nil {
			panic(err)
		}
	}
	w.Header().Set("Content-Type", contentTypeOf(att.Filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType(disposition, map[string]string{"filename": att.Filename}))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, body); err != nil {
		panic(err)
	}
}

func requestHasCredential(r *http.Request) bool {
	if _, ok := bearerToken(r); ok {
		return true
	}
	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		return true
	}
	cookie, err := r.Cookie(GuestDeviceCookieName)
	return err == nil && cookie.Value != ""
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
