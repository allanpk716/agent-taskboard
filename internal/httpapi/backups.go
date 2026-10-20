// 备份状态的 HTTP 侧（#43）：GET /api/v1/admin/backups 返回最近一次成功备份
// 的时间、是否超过 36 小时、是否“备份未离开本机”，以及最近的备份记录。
// 管理页（#45）据此显示与标红；“立即备份”入口是 taskboard backup 子命令
// 和更新流程共用的 ops.Backups.Now，不在 HTTP 层。
package httpapi

import (
	"net/http"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/ops"
)

// handleAdminBackups 处理：
//
//	GET /api/v1/admin/backups  备份状态（仅管理员）
func (a *API) handleAdminBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	if _, ok := a.adminFromRequestFor(w, r, "备份状态只对管理员开放；访客请通过成员处理工单",
		"备份状态与恢复演练见 docs/deploy-and-release.md「备份与恢复」"); !ok {
		return
	}
	status := a.backups.Status(r.Context())
	records := make([]backupRecordView, 0, len(status.Records))
	for _, rec := range status.Records {
		records = append(records, newBackupRecordView(rec))
	}
	var lastFailure *backupRecordView
	if status.LastFailure != nil {
		v := newBackupRecordView(*status.LastFailure)
		lastFailure = &v
	}
	var lastSuccessAt *string
	if status.LastSuccessAt != nil {
		s := status.LastSuccessAt.Format(time.RFC3339Nano)
		lastSuccessAt = &s
	}
	var lastLocalFallbackAt *string
	if status.LastLocalFallbackAt != nil {
		v := status.LastLocalFallbackAt.Format(time.RFC3339Nano)
		lastLocalFallbackAt = &v
	}
	writeJSON(w, http.StatusOK, backupsStatusView{
		LocalOnly:              status.LocalOnly,
		LastSuccessAt:          lastSuccessAt,
		LastLocalFallbackAt:    lastLocalFallbackAt,
		TargetUnreachable:      status.TargetUnreachable,
		TargetUnreachableError: status.TargetUnreachableError,
		Overdue:                status.Overdue,
		OverdueAfter:           int(ops.OverdueAfter.Hours()),
		LastFailure:            lastFailure,
		Records:                records,
	})
}

// backupsStatusView 是 GET /api/v1/admin/backups 的响应。local_only 为 true 时
// 管理页显示“备份未离开本机”；overdue 为 true 时（超过 36 小时或从未成功）
// 标红。
type backupsStatusView struct {
	LocalOnly              bool               `json:"local_only"`
	LastSuccessAt          *string            `json:"last_success_at"`
	LastLocalFallbackAt    *string            `json:"last_local_fallback_at"`
	TargetUnreachable      bool               `json:"target_unreachable"`
	TargetUnreachableError string             `json:"target_unreachable_error,omitempty"`
	Overdue                bool               `json:"overdue"`
	OverdueAfter           int                `json:"overdue_after_hours"`
	LastFailure            *backupRecordView  `json:"last_failure"`
	Records                []backupRecordView `json:"records"`
}

// backupRecordView 是一条备份记录的对外结构。
type backupRecordView struct {
	At          string `json:"at"`
	Kind        string `json:"kind"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	IntegrityOK bool   `json:"integrity_ok"`
	FilesAdded  int    `json:"files_added"`
	Location    string `json:"location"`
	Error       string `json:"error,omitempty"`
	Success     bool   `json:"success"`
}

func newBackupRecordView(r ops.Record) backupRecordView {
	return backupRecordView{
		At: r.At.Format(time.RFC3339Nano), Kind: string(r.Kind), Filename: r.Filename,
		Size: r.Size, IntegrityOK: r.IntegrityOK, FilesAdded: r.FilesAdded,
		Location: string(r.Location),
		Error:    r.Err, Success: r.Success(),
	}
}
