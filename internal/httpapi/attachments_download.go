// 附件下载（#33）：GET /api/v1/attachments/{att_N}/{filename}。
// 与工单相同的凭证（会话 cookie / Agent token / 访客链接密钥）；访客链接拿到的
// 地址与该链接绑定（签名地址，见 attachments/guest_url.go），链接失效后随之失效。
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
		// 待用附件还没有挂到说明或回复上，不提供下载。
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
		// 标准凭证：与读工单相同（成员会话、Agent token、访客链接密钥）。
		viewer, ok := a.viewerFromRequest(w, r)
		if !ok {
			return
		}
		if viewer.Kind == identity.ViewerGuest && viewer.GuestLink.TicketSeq != att.TicketSeq {
			WriteError(w, http.StatusNotFound, CodeTicketNotFound, "没有这张工单",
				"访客链接只能访问它所指向的那张工单")
			return
		}
	} else if q := r.URL.Query(); q.Has("sig") {
		// 签名地址（<img src> 带不了 Bearer 头）：链接 id + 到期 + 签名。
		linkID, exp, err1 := parseGuestURLQuery(q)
		if err1 != "" {
			WriteError(w, http.StatusUnauthorized, CodeInvalidToken, "附件地址的参数不完整",
				"重新读取工单，用响应里给出的附件地址")
			return
		}
		if err := a.atts.VerifyGuestURL(r.Context(), att, linkID, exp, q.Get("sig")); err != nil {
			switch {
			case errors.Is(err, attachments.ErrURLExpired):
				if recErr := a.accessRecords.RecordGuestLinkDenied(r.Context(), att.TicketSeq, linkID, "expired", att); recErr != nil {
					panic(recErr)
				}
				WriteError(w, http.StatusUnauthorized, CodeAttachmentURLExpired, err.Error(),
					"重新读取工单，响应里会给出新的附件地址")
			case errors.Is(err, attachments.ErrLinkRevoked):
				if recErr := a.accessRecords.RecordGuestLinkDenied(r.Context(), att.TicketSeq, linkID, "revoked", att); recErr != nil {
					panic(recErr)
				}
				WriteError(w, http.StatusUnauthorized, CodeLinkRevoked, "访客链接已被撤销",
					"请联系发链接的成员重新获取一条新链接")
			case errors.Is(err, attachments.ErrLinkExpired):
				if recErr := a.accessRecords.RecordGuestLinkDenied(r.Context(), att.TicketSeq, linkID, "expired", att); recErr != nil {
					panic(recErr)
				}
				WriteError(w, http.StatusUnauthorized, CodeLinkExpired, "访客链接已过期",
					"请联系发链接的成员延期，或重新获取一条新链接")
			case errors.Is(err, attachments.ErrBadSignature), errors.Is(err, attachments.ErrNotFound):
				WriteError(w, http.StatusUnauthorized, CodeInvalidToken, "附件地址无效", "")
			default:
				panic(err)
			}
			return
		}
	} else {
		// 无 Bearer 也无签名：试会话 cookie（成员网页直接 <img src> 就走这条）。
		if _, ok := a.viewerFromRequest(w, r); !ok {
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
		if _, ok := bearerToken(r); ok {
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
		} else if q := r.URL.Query(); q.Has("sig") {
			linkID, _, _ := parseGuestURLQuery(q)
			if err := a.accessRecords.RecordGuestLinkDownload(r.Context(), att, linkID); err != nil {
				panic(err)
			}
		} else {
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
	}

	disposition := "inline"
	if !attachments.IsInline(att.Filename) {
		disposition = "attachment"
	}
	// FormatMediaType 会按 RFC 2231/5987 编码非 ASCII 文件名（filename*=utf-8''…）。
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

// parseGuestURLQuery 取出签名地址的 link 与 exp 参数（缺失时报错误文案）。
func parseGuestURLQuery(q url.Values) (linkID, exp int64, errText string) {
	linkID, err := strconv.ParseInt(q.Get("link"), 10, 64)
	if err != nil || linkID <= 0 {
		return 0, 0, "link"
	}
	exp, err = strconv.ParseInt(q.Get("exp"), 10, 64)
	if err != nil {
		return 0, 0, "exp"
	}
	return linkID, exp, ""
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
