package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/clientstamp"
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
	if _, ok := a.memberFromRequestFor(w, r, "桌面客户端只对成员开放"); !ok {
		return
	}
	pkg, err := updater.CurrentClientPackage(a.cfg.DataDir)
	if err != nil {
		a.writeClientUnavailable(w, err)
		return
	}
	stamped, err := a.stampedClient(pkg, a.publicURL(r))
	if err != nil {
		panic(err)
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
	if _, ok := a.memberFromRequestFor(w, r, "桌面客户端只对成员开放"); !ok {
		return
	}
	pkg, err := updater.CurrentClientPackage(a.cfg.DataDir)
	if err != nil {
		a.writeClientUnavailable(w, err)
		return
	}
	stamped, err := a.stampedClient(pkg, a.publicURL(r))
	if err != nil {
		panic(err)
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

func (a *API) publicURL(r *http.Request) string {
	if configured := a.cfg.Server.PublicURL; configured != "" {
		return strings.TrimRight(configured, "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if s := r.Header.Get("X-Forwarded-Proto"); s == "http" || s == "https" {
		scheme = s
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = forwarded
	}
	return scheme + "://" + host
}

func (a *API) stampedClient(pkg *updater.ClientPackage, rawURL string) (*stampedClient, error) {
	trailer, err := clientstamp.Append(nil, rawURL)
	if err != nil {
		return nil, err
	}
	key := pkg.Version + "\x00" + pkg.Sha256 + "\x00" + rawURL
	if cached, ok := a.stampedClients.Load(key); ok {
		return cached.(*stampedClient), nil
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
	_, _ = hash.Write(trailer)
	out := &stampedClient{
		sha256: hex.EncodeToString(hash.Sum(nil)), size: size + int64(len(trailer)), trailer: trailer,
	}
	actual, _ := a.stampedClients.LoadOrStore(key, out)
	return actual.(*stampedClient), nil
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
