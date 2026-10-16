// 健康检查（#45）：GET /api/v1/health 公开返回服务状态与前后端版本。更新
// 流程里“新版本启动时执行迁移，再做健康检查”的自检、Caddy/NSSM 的探活
// 都用这个地址；不需要登录。
package httpapi

import (
	"net/http"

	"github.com/allanpk716/agent-taskboard/internal/updater"
	"github.com/allanpk716/agent-taskboard/internal/version"
)

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	writeJSON(w, http.StatusOK, healthView{
		Status: "ok",
		Server: version.Server,
		Web:    updater.WebVersion(a.cfg.WebDir),
	})
}

// healthView 是 GET /api/v1/health 的响应。
type healthView struct {
	Status string `json:"status"`
	Server string `json:"server_version"`
	Web    string `json:"web_version"`
}
