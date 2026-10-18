package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/allanpk716/agent-taskboard/internal/updater"
)

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
	writeJSON(w, http.StatusOK, clientLatestView{
		Version: pkg.Version, Sha256: pkg.Sha256, Size: pkg.Size,
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
