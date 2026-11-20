package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
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

func Test废弃的客户端镜像目录配置仍可加载并忽略(t *testing.T) {
	path := writeConfig(t, `[server]
public_url = 'https://taskboard.example.com'
client_image_dir = '/app/client-package'
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("旧配置应能加载：%v", err)
	}
	if cfg.Server.PublicURL != "https://taskboard.example.com" {
		t.Fatalf("其它配置被影响：%+v", cfg.Server)
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

func Test附件存储后端默认为本地并可配置OSS(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, ""))
	if err != nil {
		t.Fatalf("空配置应能加载：%v", err)
	}
	if cfg.Attachments.Backend != attachments.AttachmentBackendLocal {
		t.Fatalf("默认后端 = %q, 想 local", cfg.Attachments.Backend)
	}

	path := writeConfig(t, `[attachments]
backend = 'oss'

[attachments.oss]
region = 'cn-hangzhou'
bucket = 'urithub-files'
prefix = 'agent-taskboard/prod/'
internal_endpoint = 'oss-cn-hangzhou-internal.aliyuncs.com'
signing_host = 'files.example.test'
`)
	cfg, err = config.Load(path)
	if err != nil {
		t.Fatalf("OSS 配置应能加载：%v", err)
	}
	if cfg.Attachments.Backend != attachments.AttachmentBackendOSS ||
		cfg.Attachments.OSS.Region != "cn-hangzhou" ||
		cfg.Attachments.OSS.Bucket != "urithub-files" ||
		cfg.Attachments.OSS.Prefix != "agent-taskboard/prod/" ||
		cfg.Attachments.OSS.InternalEndpoint != "oss-cn-hangzhou-internal.aliyuncs.com" ||
		cfg.Attachments.OSS.SigningHost != "files.example.test" {
		t.Fatalf("OSS 配置 = %+v", cfg.Attachments)
	}
}

func Test附件存储后端值非法时拒绝加载(t *testing.T) {
	path := writeConfig(t, `[attachments]
backend = 'minio'
`)
	_, err := config.Load(path)
	if err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("非法后端应拒绝并提示 backend：%v", err)
	}
}

func Test通知出口配置默认关闭并可解析部署白名单与北京时间窗口(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, ""))
	if err != nil {
		t.Fatalf("空配置应能加载：%v", err)
	}
	if cfg.Webhook.Enabled {
		t.Fatal("webhook 缺省应关闭，避免新部署意外外呼")
	}
	if len(cfg.Webhook.AllowedBases) != 0 {
		t.Fatalf("默认白名单 = %v，想为空", cfg.Webhook.AllowedBases)
	}
	if cfg.Webhook.WindowStart != "08:00" || cfg.Webhook.WindowEnd != "20:00" {
		t.Fatalf("默认窗口 = %s-%s，想 08:00-20:00（北京时间）",
			cfg.Webhook.WindowStart, cfg.Webhook.WindowEnd)
	}

	path := writeConfig(t, `[webhook]
enabled = true
allowed_bases = ['https://bot.example.test', 'https://bot2.example.test/base/']
window_start = '09:30'
window_end = '21:00'
`)
	cfg, err = config.Load(path)
	if err != nil {
		t.Fatalf("合法 webhook 配置应能加载：%v", err)
	}
	if !cfg.Webhook.Enabled || len(cfg.Webhook.AllowedBases) != 2 ||
		cfg.Webhook.AllowedBases[1] != "https://bot2.example.test/base/" ||
		cfg.Webhook.WindowStart != "09:30" || cfg.Webhook.WindowEnd != "21:00" {
		t.Fatalf("webhook 配置 = %+v", cfg.Webhook)
	}
}

func Test通知出口开启时拒绝缺失白名单和非法窗口(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "缺失白名单",
			body: "[webhook]\nenabled = true\n",
			want: "allowed_bases",
		},
		{
			name: "非法起点",
			body: "[webhook]\nenabled = true\nallowed_bases = ['https://bot.example.test']\nwindow_start = '24:00'\n",
			want: "window_start",
		},
		{
			name: "非法终点",
			body: "[webhook]\nenabled = true\nallowed_bases = ['https://bot.example.test']\nwindow_end = '8:00'\n",
			want: "window_end",
		},
		{
			name: "起点不早于终点",
			body: "[webhook]\nenabled = true\nallowed_bases = ['https://bot.example.test']\nwindow_start = '20:00'\nwindow_end = '08:00'\n",
			want: "推送窗口",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误应包含 %q：%v", tc.want, err)
			}
		})
	}
}

func Test通知出口窗口解析固定为北京时间(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `[webhook]
window_start = '09:30'
window_end = '21:00'
`))
	if err != nil {
		t.Fatal(err)
	}
	start, end, err := cfg.Webhook.TimeWindow()
	if err != nil {
		t.Fatal(err)
	}
	if start.Location() != clock.Beijing || end.Location() != clock.Beijing ||
		start.Hour() != 9 || start.Minute() != 30 || end.Hour() != 21 || end.Minute() != 0 {
		t.Fatalf("窗口 = %s-%s，想北京时间 09:30-21:00", start.Format(time.Kitchen), end.Format(time.Kitchen))
	}
}

func Test通知出口白名单拒绝非HTTPS和带凭据查询片段地址(t *testing.T) {
	for _, base := range []string{
		"http://bot.example.test",
		"https://user:pass@bot.example.test",
		"https://bot.example.test?next=/admin",
		"https://bot.example.test#fragment",
		"https://bot.example.test/../other",
	} {
		_, err := config.Load(writeConfig(t, `[webhook]
allowed_bases = ['`+base+`']
`))
		if err == nil || !strings.Contains(err.Error(), "allowed_bases") {
			t.Fatalf("base %q 应拒绝：%v", base, err)
		}
	}
}

func Test通知出口关闭时仍拒绝无法解析的窗口(t *testing.T) {
	_, err := config.Load(writeConfig(t, `[webhook]
enabled = false
window_start = '8:00'
`))
	if err == nil || !strings.Contains(err.Error(), "window_start") {
		t.Fatalf("关闭状态下 malformed window_start 也应拒绝：%v", err)
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
