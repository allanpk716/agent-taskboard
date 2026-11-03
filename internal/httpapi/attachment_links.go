package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachmentlink"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// handleAttachmentLink 处理 GET /api/v1/attachment-links/{att_N}。
// 它只给成员 Agent 换取短时效下载地址；地址本身不带 Bearer token。
func (a *API) handleAttachmentLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "获取附件链接用 GET")
		return
	}
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	if viewer.Kind != identity.ViewerMember || viewer.Agent == nil {
		WriteError(w, http.StatusForbidden, CodeAgentsOnly, "附件链接只限成员 Agent 使用",
			"网页会话请直接打开附件地址；访客请使用自己的附件地址")
		return
	}
	ref := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/attachment-links/"), "/")
	if !strings.HasPrefix(ref, "att_") || strings.Contains(ref, "/") {
		WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "没有这个附件",
			"附件引用从上传响应或工单读取接口拿，形如 att_1")
		return
	}
	att, err := a.atts.Get(r.Context(), ref)
	if err != nil {
		WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "没有这个附件",
			"附件引用从上传响应或工单读取接口拿")
		return
	}
	if _, err = a.tickets.ForViewer(r.Context(), viewer, strconv.FormatInt(att.TicketSeq, 10)); err != nil {
		a.writeTicketError(w, r, ref, err)
		return
	}
	if att.State != "attached" {
		WriteError(w, http.StatusNotFound, CodeAttachmentNotFound, "附件还在待用状态，不能下载",
			"先在说明或回复里引用它（attachment_ids 或正文 att: 引用）")
		return
	}

	expiresAt := a.clk.Now().Add(attachmentlink.TTL)
	query := url.Values{}
	query.Set("agent", strconv.FormatInt(viewer.Agent.ID, 10))
	query.Set("exp", strconv.FormatInt(expiresAt.Unix(), 10))
	query.Set("sig", a.attachmentLinks.Sign(att.Ref(), viewer.Agent.ID, expiresAt))
	path := attachmentBasePath(att.Ref()) + url.PathEscape(att.Filename)
	writeJSON(w, http.StatusOK, attachmentLinkView{
		URL:       a.requestBaseURL(r) + path + "?" + query.Encode(),
		ExpiresAt: expiresAt.Format(time.RFC3339),
		Mime:      contentTypeOf(att.Filename),
		Size:      att.Size,
		Filename:  att.Filename,
	})
}

type attachmentLinkView struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
	Mime      string `json:"mime"`
	Size      int64  `json:"size"`
	Filename  string `json:"filename"`
}

// SetAttachmentLinkSigner 装配附件链接签名密钥（生产由 server 包持久化加载）。
func (a *API) SetAttachmentLinkSigner(signer *attachmentlink.Signer) { a.attachmentLinks = signer }

// viewerFromSignedAttachment 在下载请求只有签名地址参数时重建成员 Agent 身份。
// 任何一项失败都按“无凭证”返回 401，不泄露具体原因。
func (a *API) viewerFromSignedAttachment(w http.ResponseWriter, r *http.Request, attRef string) (identity.Viewer, bool) {
	if a.attachmentLinks == nil {
		a.writeUnauthenticatedAttachment(w, r)
		return identity.Viewer{}, false
	}
	values := r.URL.Query()
	agentID, agentErr := strconv.ParseInt(values.Get("agent"), 10, 64)
	issuedAt := values.Get("exp")
	expiresAt, expErr := strconv.ParseInt(issuedAt, 10, 64)
	if agentErr != nil || expErr != nil || agentID <= 0 {
		a.writeUnauthenticatedAttachment(w, r)
		return identity.Viewer{}, false
	}
	if !a.attachmentLinks.Verify(attRef, agentID, expiresAt, values.Get("sig")) ||
		!a.clk.Now().Before(time.Unix(expiresAt, 0)) {
		a.writeUnauthenticatedAttachment(w, r)
		return identity.Viewer{}, false
	}
	viewer, err := a.ident.ViewerFromAgentID(r.Context(), agentID)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCredentials) {
			a.writeUnauthenticatedAttachment(w, r)
			return identity.Viewer{}, false
		}
		panic(err)
	}
	return viewer, true
}

func (a *API) writeUnauthenticatedAttachment(w http.ResponseWriter, r *http.Request) {
	WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "未登录", a.authHint(r))
}
