// 备份状态接口（#43）：GET /api/v1/admin/backups 返回最近一次成功备份的时间、
// 是否超过 36 小时，以及是否“备份未离开本机”；备份失败不影响服务运行。
package server_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type backupsStatusBody struct {
	LocalOnly              bool              `json:"local_only"`
	LastSuccessAt          *string           `json:"last_success_at"`
	LastLocalFallbackAt    *string           `json:"last_local_fallback_at"`
	TargetUnreachable      bool              `json:"target_unreachable"`
	TargetUnreachableError string            `json:"target_unreachable_error"`
	Overdue                bool              `json:"overdue"`
	LastFailure            *backupRecordVue  `json:"last_failure"`
	Records                []backupRecordVue `json:"records"`
}

type backupRecordVue struct {
	At          string `json:"at"`
	Kind        string `json:"kind"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	IntegrityOK bool   `json:"integrity_ok"`
	FilesAdded  int    `json:"files_added"`
	Location    string `json:"location"`
	Error       string `json:"error"`
	Success     bool   `json:"success"`
}

// 管理员能看到最近一次成功备份的时间；超过 36 小时或从未成功时标 overdue。
func Test管理员可查询备份状态(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "pass-word")
	s.CreateMember("chen", "pass-word")
	admin := s.Login("admin", "pass-word")
	member := s.Login("chen", "pass-word")

	// 未登录 401；访客和普通成员 403。
	res := s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/admin/backups", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录状态码 = %d, 想 401", res.StatusCode)
	}
	guest := s.GuestClient(tbguestKey(t, s, admin))
	res = s.Do(t, guest, http.MethodGet, "/api/v1/admin/backups", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("访客状态码 = %d, 想 403", res.StatusCode)
	}
	res = s.Do(t, member, http.MethodGet, "/api/v1/admin/backups", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("普通成员状态码 = %d, 想 403", res.StatusCode)
	}

	// 从未备份：无最近成功时间，且 overdue。
	var status backupsStatusBody
	res = s.Do(t, admin, http.MethodGet, "/api/v1/admin/backups", nil, &status)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("管理员状态码 = %d, 想 200", res.StatusCode)
	}
	if status.LastSuccessAt != nil || !status.Overdue {
		t.Fatalf("从未备份时应无最近成功时间且 overdue：%+v", status)
	}

	// 备份一次：有最近成功时间，不再 overdue；没有配置目标 → 备份未离开本机。
	if _, err := s.Backups().BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	status = backupsStatusBody{}
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/backups", nil, &status)
	if status.LastSuccessAt == nil {
		t.Fatal("备份后应有最近成功时间")
	}
	if status.Overdue {
		t.Fatal("刚备份完不应 overdue")
	}
	if !status.LocalOnly {
		t.Fatal("未配置备份目标时应标注 local_only（备份未离开本机）")
	}
	if len(status.Records) != 1 || !status.Records[0].Success || status.Records[0].Filename == "" {
		t.Fatalf("记录里应有这次成功的备份：%+v", status.Records)
	}

	// 时钟推进 37 小时：最近成功备份超过 36 小时，标 overdue。
	s.Clock().Advance(37 * time.Hour)
	status = backupsStatusBody{}
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/backups", nil, &status)
	if !status.Overdue {
		t.Fatal("最近一次成功备份超过 36 小时应 overdue")
	}
}

// 备份目标连不上：记一次失败，服务照常运行。
func Test备份目标连不上时记录失败且服务照常运行(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := tbtest.Start(t, tbtest.WithBackup(config.Backup{
		Target: filepath.Join(blocker, "backups"), DailyAt: "03:00",
		KeepDaily: 30, KeepMonthly: 12, KeepPreupdate: 5,
	}))
	s.CreateAdmin("admin", "pass-word")
	admin := s.Login("admin", "pass-word")

	rec, err := s.Backups().BackupNow(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatalf("目标连不上但本机兜底成功时调用方应可继续：%v", err)
	}
	if rec.Success() || rec.Err == "" || rec.Location != ops.LocationLocal {
		t.Fatalf("目标失败应记入本机兜底记录：%+v", rec)
	}

	// 服务照常运行：登录、读会话都正常。
	res := s.Do(t, admin, http.MethodGet, "/api/v1/session", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("备份失败后服务应照常运行，状态码 = %d", res.StatusCode)
	}
	var status backupsStatusBody
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/backups", nil, &status)
	if status.LastSuccessAt != nil || !status.Overdue {
		t.Fatalf("失败后不应有最近成功时间：%+v", status)
	}
	if status.LastFailure == nil || status.LastFailure.Error == "" {
		t.Fatalf("应能看到最近一次失败：%+v", status.LastFailure)
	}
	if !status.TargetUnreachable || status.TargetUnreachableError == "" ||
		status.LastLocalFallbackAt == nil || status.Records[0].Location != "local" {
		t.Fatalf("目标不可达与兜底状态应进入 API：%+v", status)
	}
}

// tbguestKey 造一条访客链接拿访客凭证（管理员登录 c）。
func tbguestKey(t *testing.T, s *tbtest.Server, c *http.Client) string {
	t.Helper()
	res := s.Do(t, c, http.MethodPost, "/api/v1/tickets",
		map[string]any{"kind": "task", "title": "备份状态测试", "brief_md": "x", "labels": []string{"机型:UD-2300"}}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单失败：%d", res.StatusCode)
	}
	link := s.CreateGuestLink(t, c, "1", map[string]any{"note": "检验科-张三", "valid_days": 7})
	return link.Key(t)
}
