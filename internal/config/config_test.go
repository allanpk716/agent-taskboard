package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/config"
)

func Test任务板部署环境变量覆盖配置文件(t *testing.T) {
	path := writeConfig(t, `[server]
deploy = ''
`)
	t.Setenv("TASKBOARD_DEPLOY", "container")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("container 环境变量应能加载：%v", err)
	}
	if cfg.Server.Deploy != config.DeployContainer {
		t.Fatalf("Deploy = %q, 想 container", cfg.Server.Deploy)
	}
}

func Test任务板部署环境变量非法时拒绝(t *testing.T) {
	path := writeConfig(t, `[server]
deploy = ''
`)
	t.Setenv("TASKBOARD_DEPLOY", "docker")
	if _, err := config.Load(path); err == nil || !strings.Contains(err.Error(), "TASKBOARD_DEPLOY") {
		t.Fatalf("非法环境变量应拒绝并提示变量名：%v", err)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// 备份目标只写了一个反斜杠（\nas\share）时，Windows 会把它当成当前盘根下的
// 本地目录：备份悄悄留在本机，管理页却显示“已离开本机”（#46 首次部署时
// 踩到）。加载配置时就拒绝，提示 UNC 要以 \\ 开头。
func Test备份目标只有一个反斜杠时拒绝加载(t *testing.T) {
	path := writeConfig(t, `[backup]
target = '\nas01\taskboard-backup'
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("单反斜杠开头的备份目标应拒绝加载")
	}
	if !strings.Contains(err.Error(), `\\`) {
		t.Errorf("错误应提示 UNC 以 \\ 开头：%v", err)
	}
}

func Test备份目标是UNC路径时正常加载(t *testing.T) {
	path := writeConfig(t, `[backup]
target = '\\nas01\taskboard-backup'
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("UNC 目标应能加载：%v", err)
	}
	if cfg.Backup.Target != `\\nas01\taskboard-backup` {
		t.Errorf("Target = %q", cfg.Backup.Target)
	}
}
