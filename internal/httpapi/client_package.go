package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/allanpk716/agent-taskboard/internal/clientstamp"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/publicbase"
	"github.com/allanpk716/agent-taskboard/internal/updater"
)

type stampedClient struct {
	sha256  string
	size    int64
	trailer []byte
}

// handleClientLatest 处理 GET /api/v1/client/latest：成员会话、成员 Agent
// token 和客户端凭证都可查询服务端当前代管的桌面客户端。
func (a *API) handleClientLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	if !a.clientPackageViewer(w, r) {
		return
	}
	pkg, err := updater.CurrentClientMetadata(a.cfg.DataDir)
	if err != nil {
		a.writeClientUnavailable(w, err)
		return
	}
	stamped, err := a.stampedClient(pkg, a.publicURL(r))
	if err != nil {
		a.writeClientUnavailable(w, err)
		return
	}
	writeJSON(w, http.StatusOK, clientLatestView{
		Version: pkg.Version, Sha256: stamped.sha256, Size: stamped.size,
		DownloadURL: "/api/v1/client/download",
	})
}

func (a *API) handleClientDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	if !a.clientPackageViewer(w, r) {
		return
	}
	pkg, err := updater.CurrentClientMetadata(a.cfg.DataDir)
	if err != nil {
		a.writeClientUnavailable(w, err)
		return
	}
	stamped, err := a.stampedClient(pkg, a.publicURL(r))
	if err != nil {
		a.writeClientUnavailable(w, err)
		return
	}
	f, err := os.Open(pkg.Path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="taskboard-client.exe"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		// 响应已开始，只能中断连接；客户端会按 SHA256 重新校验。
		return
	}
	_, _ = w.Write(stamped.trailer)
}

func (a *API) clientPackageViewer(w http.ResponseWriter, r *http.Request) bool {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return false
	}
	if viewer.Kind != identity.ViewerMember {
		WriteError(w, http.StatusForbidden, CodeMembersOnly, "桌面客户端只对成员开放", "")
		return false
	}
	if viewer.Agent != nil {
		WriteError(w, http.StatusForbidden, CodeAgentNotAllowed,
			"成员 Agent 不能查询或下载桌面客户端", "请在网页登录后下载，或使用客户端凭证")
		return false
	}
	return true
}

func (a *API) publicURL(r *http.Request) string {
	return publicbase.FromRequest(a.cfg.Server.PublicURL, r)
}

func (a *API) stampedClient(pkg *updater.ClientPackage, rawURL string) (*stampedClient, error) {
	trailer, err := clientstamp.Append(nil, rawURL)
	if err != nil {
		return nil, err
	}
	key := pkg.Version + "\x00" + pkg.Sha256 + "\x00" + rawURL
	a.stampedClientMu.Lock()
	cached, ok := a.stampedClients[key]
	a.stampedClientMu.Unlock()
	if ok {
		return cached, nil
	}
	f, err := os.Open(pkg.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, f)
	if err != nil {
		return nil, err
	}
	gotSha := hex.EncodeToString(hash.Sum(nil))
	if gotSha != pkg.Sha256 || size != pkg.Size {
		return nil, errors.New("代管客户端与清单不符，拒绝提供下载")
	}
	_, _ = hash.Write(trailer)
	out := &stampedClient{
		sha256: hex.EncodeToString(hash.Sum(nil)), size: size + int64(len(trailer)), trailer: trailer,
	}
	a.stampedClientMu.Lock()
	if len(a.stampedClients) >= 8 {
		a.stampedClients = make(map[string]*stampedClient)
	}
	a.stampedClients[key] = out
	a.stampedClientMu.Unlock()
	return out, nil
}

func (a *API) writeClientUnavailable(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrNotExist) {
		WriteError(w, http.StatusNotFound, CodeClientNotAvailable, "管理员尚未发布客户端", "请联系管理员执行平台更新后再下载")
		return
	}
	WriteError(w, http.StatusInternalServerError, CodeInternal, "读取代管客户端失败", err.Error())
}

type clientLatestView struct {
	Version     string `json:"version"`
	Sha256      string `json:"sha256"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"download_url"`
}
