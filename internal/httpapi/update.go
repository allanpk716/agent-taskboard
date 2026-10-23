// 管理员更新接口（#45，docs/deploy-and-release.md「更新」）：检查更新供管理
// 页提示有新版本（服务绝不自动安装）；执行更新与 taskboard update、CLI 共用
// internal/updater 的同一套逻辑。换后端 exe 的更新写完响应后进程退出，由
// NSSM 拉起新版本（新版本启动时自动迁移并做健康检查）。
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/updater"
)

// osExit 是进程退出（后端更新后交给 NSSM 拉起）；变量化便于将来测试替换。
var osExit = os.Exit

// updateOptions 按当前配置构造更新选项；更新前备份直接用服务里在跑的备份
// 管理器（与每日定时备份同一段代码）。
func (a *API) updateOptions() updater.Options {
	return updater.Options{
		Update: a.cfg.Update, DataDir: a.cfg.DataDir, Backup: a.cfg.Backup,
		WebDir: a.cfg.WebDir, Port: a.cfg.Port,
		Preupdate: func(ctx context.Context) (updater.PreupdateBackup, error) {
			rec, err := a.backups.Preupdate(ctx)
			if err != nil {
				return updater.PreupdateBackup{}, err
			}
			return updater.PreupdateBackup{Filename: rec.Filename, Local: rec.Location == ops.LocationLocal}, nil
		},
	}
}

// handleAdminUpdate 处理：
//
//	GET   /api/v1/admin/update  检查更新（仅管理员）
//	POST  /api/v1/admin/update  执行更新（仅管理员）
func (a *API) handleAdminUpdate(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.adminFromRequestFor(w, r, "更新由管理员在管理页或命令行触发（taskboard update）；访客请联系管理员",
		"更新由管理员在管理页或命令行触发（taskboard update）"); !ok {
		return
	}
	if a.cfg.Server.Deploy == config.DeployContainer {
		WriteError(w, http.StatusConflict, CodeContainerUpdateUnsupported,
			"容器部署，请升级镜像", "平台运行在容器里，不能替换容器内的后端与前端文件。")
		return
	}
	switch r.Method {
	case http.MethodGet:
		status, err := updater.Check(r.Context(), a.updateOptions())
		if err != nil {
			a.writeUpdateError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updateStatusView{
			SourceUsed: status.SourceUsed,
			Server: componentStatusView{
				Current: status.ServerCurrent, Latest: status.ServerLatest,
				UpdateAvailable: status.ServerUpdate,
			},
			Web: componentStatusView{
				Current: status.WebCurrent, Latest: status.WebLatest,
				UpdateAvailable: status.WebUpdate, MinServer: status.WebMinServer,
			},
			Client: componentStatusView{
				Current: status.ClientCurrent, Latest: status.ClientLatest,
				UpdateAvailable: status.ClientUpdate,
			},
			WebNeedsNewerServer: status.WebNeedsNewerServer,
		})
	case http.MethodPost:
		res, err := updater.Apply(r.Context(), a.updateOptions())
		if err != nil {
			a.writeUpdateError(w, err)
			return
		}
		view := updateResultView{
			SourceUsed: res.SourceUsed, Steps: res.Steps,
			ServerUpdated: res.ServerUpdated, WebUpdated: res.WebUpdated,
			ClientUpdated: res.ClientUpdated,
			ClientError:   res.ClientError,
			RestartNeeded: res.RestartNeeded, PreupdateBackup: res.PreupdateBackup,
			PreupdateBackupLocal: res.PreupdateBackupLocal,
		}
		if len(res.Steps) == 0 {
			view.Message = "已是最新，无需更新"
		} else if res.RestartNeeded {
			view.Message = "后端已更新：服务即将退出，由 NSSM 拉起新版本（新版本启动时会自动迁移并做健康检查）"
		}
		if res.PreupdateBackupLocal && view.Message != "" {
			view.Message += "；更新前备份只在本机"
		}
		writeJSON(w, http.StatusOK, view)
		if res.RestartNeeded {
			// 写完响应再退出：给响应一点时间到达浏览器，剩下的交给 NSSM。
			go func() {
				time.Sleep(500 * time.Millisecond)
				osExit(0)
			}()
		}
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
	}
}

// writeUpdateError 区分更新源问题（502，可稍后重试）与更新本身的失败
// （500，比如哈希不符、更新前备份失败）。
func (a *API) writeUpdateError(w http.ResponseWriter, err error) {
	var srcErr *updater.SourceError
	if errors.As(err, &srcErr) {
		WriteError(w, http.StatusBadGateway, CodeUpdateSourceUnreachable, "更新源不可用", srcErr.Error())
		return
	}
	WriteError(w, http.StatusInternalServerError, CodeUpdateFailed, "更新失败", err.Error())
}

// updateStatusView 是 GET /api/v1/admin/update 的响应。
type updateStatusView struct {
	SourceUsed          string              `json:"source_used"`
	Server              componentStatusView `json:"server"`
	Web                 componentStatusView `json:"web"`
	Client              componentStatusView `json:"client"`
	WebNeedsNewerServer bool                `json:"web_needs_newer_server"`
}

// componentStatusView 是一个组件的版本状态。
type componentStatusView struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	MinServer       string `json:"min_server,omitempty"` // 仅前端：需要的最低后端版本
}

// updateResultView 是 POST /api/v1/admin/update 的响应。
type updateResultView struct {
	SourceUsed           string   `json:"source_used"`
	Steps                []string `json:"steps"`
	Message              string   `json:"message,omitempty"`
	ServerUpdated        bool     `json:"server_updated"`
	WebUpdated           bool     `json:"web_updated"`
	ClientUpdated        bool     `json:"client_updated"`
	ClientError          string   `json:"client_error,omitempty"`
	RestartNeeded        bool     `json:"restart_needed"`
	PreupdateBackup      string   `json:"preupdate_backup,omitempty"`
	PreupdateBackupLocal bool     `json:"preupdate_backup_local"`
}
