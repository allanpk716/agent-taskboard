package server_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 备份不只是库快照（还镜像附件），转发文案的服务端密钥文件也随备份保存、
// 随恢复还原：恢复后链接不需要换新地址就能再次直接复制转发文案。
func Test备份恢复还原访客链接加密密钥文件(t *testing.T) {
	dataDir := t.TempDir()
	target := t.TempDir()
	s := tbtest.Start(t, tbtest.WithDataDir(dataDir), tbtest.WithBackup(config.Backup{
		Target: target, DailyAt: "03:00", KeepDaily: 30, KeepMonthly: 12, KeepPreupdate: 5,
	}))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "备份恢复后还能直接复制")
	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})

	rec, err := s.Backups().BackupNow(context.Background(), ops.KindDaily)
	if err != nil || !rec.Success() {
		t.Fatalf("备份应成功：%+v（err %v）", rec, err)
	}
	now := s.Clock().Now()
	s.Shutdown(t)

	// 灾难：数据目录里的密钥文件损坏，重启后平台生成新密钥，旧链接变成老链接。
	if err := os.WriteFile(filepath.Join(dataDir, "guest-link-key.bin"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	s = tbtest.Start(t, tbtest.WithDataDir(dataDir), tbtest.WithClockStart(now),
		tbtest.WithBackup(config.Backup{
			Target: target, DailyAt: "03:00", KeepDaily: 30, KeepMonthly: 12, KeepPreupdate: 5,
		}))
	web = s.Login("陈工", "secret-pass")
	var before struct {
		Links []struct {
			ShareTextAvailable bool `json:"share_text_available"`
		} `json:"links"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/guest-links", nil, &before)
	if len(before.Links) != 1 || before.Links[0].ShareTextAvailable {
		t.Fatalf("密钥损坏后应降级为老链接：%+v", before.Links)
	}
	s.Shutdown(t)

	// 服务停止后恢复备份：库和访客链接加密密钥都从备份回来。
	if _, err := ops.Restore(dataDir, 0, filepath.Join(target, rec.Filename), now); err != nil {
		t.Fatalf("恢复备份失败：%v", err)
	}
	s = tbtest.Start(t, tbtest.WithDataDir(dataDir), tbtest.WithClockStart(now))
	web = s.Login("陈工", "secret-pass")

	var after struct {
		Links []struct {
			ShareTextAvailable bool `json:"share_text_available"`
		} `json:"links"`
	}
	res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/guest-links", nil, &after)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("恢复后读访客链接列表状态码 = %d, 想 200", res.StatusCode)
	}
	if len(after.Links) != 1 || !after.Links[0].ShareTextAvailable {
		t.Fatalf("恢复后链接应仍可直接复制：%+v", after.Links)
	}
	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10)+"/share-text", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("恢复后再次复制状态码 = %d, 想 200", res.StatusCode)
	}
	var copied struct {
		ShareText string `json:"share_text"`
	}
	tbtest.DecodeJSON(t, res, &copied)
	if !strings.Contains(copied.ShareText, link.URL) {
		t.Fatalf("恢复后应还原同一条链接：%q\nwant %q", copied.ShareText, link.URL)
	}
}
