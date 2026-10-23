// Package config 读取 config.toml。
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/allanpk716/agent-taskboard/internal/ops"
)

// Config 是 config.toml 的内容。后续工单会追加 [backup]、[update] 等小节。
type Config struct {
	// DataDir 是数据目录：SQLite 库和附件都放在这里。
	DataDir string `toml:"data_dir"`
	// Port 是监听端口，只监听 127.0.0.1。
	Port int `toml:"port"`
	// WebDir 是前端静态文件目录（构建产物）。
	WebDir string `toml:"web_dir"`
	// Server 是对外地址等部署侧配置。
	Server Server `toml:"server"`
	// Attachments 是附件上限（#33；默认值见 ApplyDefaults，
	// 上限以 docs/deploy-and-release.md「附件上限与显示」为准）。
	Attachments Attachments `toml:"attachments"`
	// Backup 是备份配置（#43；默认每天 03:00，保留 30 份每日、12 份每月、
	// 5 份更新前备份；target 用 UNC 路径，空则存 data_dir\backups\）。
	Backup Backup `toml:"backup"`
	// Update 是更新源配置（#45；sources 为空时不检查更新）。
	Update Update `toml:"update"`
	// Intake 是反馈入口匿名上传的防护配置。
	Intake Intake `toml:"intake"`
}

// Server 是 [server] 小节：部署对外信息。
type Server struct {
	// PublicURL 是用户访问本平台的对外根地址（如 https://taskboard.urithub.com，
	// 不带结尾斜杠）。配置后回给用户的访客链接与 share_text 用绝对地址，
	//「复制链接」拿到即可发送（评审 P2-7）；留空（默认）时保持相对路径，
	// 前端复制时用当前页面的 origin 拼绝对地址。
	PublicURL string `toml:"public_url"`
	// MinClientVersion 是平台要求的最低桌面客户端版本；空字符串表示不限制。
	MinClientVersion string `toml:"min_client_version"`
	// Deploy 是部署方式：空（默认）表示原地部署；container 表示运行在
	// 容器里，更新由替换镜像完成。
	Deploy string `toml:"deploy"`
	// ClientImageDir 是镜像内自带桌面客户端安装包的目录（manifest.json +
	// taskboard-client.exe）。容器模式下启动时装入 data_dir/client。
	ClientImageDir string `toml:"client_image_dir"`
}

// Attachments 是 [attachments] 小节：服务端只检查上限，不压缩图片
// （压缩在前端做；spec「附件显示与安全」）。
type Attachments struct {
	// MaxFileMB 是单个文件的上限（MB），超过返回 413 attachment_too_large。
	MaxFileMB int `toml:"max_file_mb"`
	// MaxFilesPerPost 是每条说明或回复的附件个数上限（插图另计）。
	MaxFilesPerPost int `toml:"max_files_per_post"`
	// MaxImagesPerPost 是每条说明或回复的插图张数上限。
	MaxImagesPerPost int `toml:"max_images_per_post"`
	// MaxTicketMB 是整张工单附件合计的上限（MB），待用附件也计入。
	MaxTicketMB int `toml:"max_ticket_mb"`
}

// Intake 是 [intake] 小节：入口匿名附件上传的限流与临时配额。
type Intake struct {
	UploadRatePerMin int `toml:"upload_rate_per_min"`
	PendingQuotaMB   int `toml:"pending_quota_mb"`
}

// Backup 是 [backup] 小节：备份目标与保留策略（#43，docs/deploy-and-release.md
// 「备份与恢复」）。
type Backup struct {
	// Target 是备份目标（UNC 路径，如 '\\\\nas01\\taskboard-backup'）；
	// 空串时存到 data_dir\backups\，管理页提示“备份未离开本机”。
	Target string `toml:"target"`
	// DailyAt 是每日定时备份的时刻（北京时间，"HH:MM"）。
	DailyAt string `toml:"daily_at"`
	// KeepDaily 是每日备份的保留份数。
	KeepDaily int `toml:"keep_daily"`
	// KeepMonthly 是每月备份（每月 1 日那份）的保留份数。
	KeepMonthly int `toml:"keep_monthly"`
	// KeepPreupdate 是更新前备份的保留份数。
	KeepPreupdate int `toml:"keep_preupdate"`
}

// Update 是 [update] 小节（#45，docs/deploy-and-release.md「更新」）：
// sources 按顺序回退，默认（留空）不检查更新——避免开发/测试环境意外
// 连真实源。GitHub 源只用 releases/download/ 下的固定地址，支持镜像前缀
// 和代理（按顺序尝试镜像，最后直连）。
type Update struct {
	// Sources 是更新源名列表，如 ["updatehub", "github"]，按顺序回退。
	Sources []string `toml:"sources"`
	// UpdateHub 是内网 UpdateHub 源（走 SHA1 → nupkg → SHA256 双校验）。
	UpdateHub UpdateHub `toml:"updatehub"`
	// GitHub 是 GitHub Release 固定地址源。
	GitHub UpdateGitHub `toml:"github"`
}

// UpdateHub 是 [update.updatehub] 小节。
type UpdateHub struct {
	// BaseURL 形如 https://updatehub.example.com，不带结尾斜杠。
	BaseURL string `toml:"base_url"`
	// Channel 是通道名，默认 stable。
	Channel string `toml:"channel"`
}

// UpdateGitHub 是 [update.github] 小节。
type UpdateGitHub struct {
	// Repo 是 OWNER/REPO。
	Repo string `toml:"repo"`
	// Mirror 是镜像前缀列表（形如 https://<镜像>/），按顺序尝试，最后直连。
	Mirror []string `toml:"mirror"`
	// Proxy 是 http:// 或 socks5:// 代理；为空时读 HTTPS_PROXY 环境变量。
	Proxy string `toml:"proxy"`
}

// BackupOptions 把 [backup] 配置换成 ops.BackupOptions（cmd/taskboard、
// internal/server、internal/updater 三处的映射收口在这里，评审 P3-16）。
func (b Backup) BackupOptions() ops.BackupOptions {
	return ops.BackupOptions{
		Target: b.Target, DailyAt: b.DailyAt,
		KeepDaily: b.KeepDaily, KeepMonthly: b.KeepMonthly,
		KeepPreupdate: b.KeepPreupdate,
	}
}

// Load 从 path 读取配置并补默认值。
func Load(path string) (Config, error) {
	var cfg Config
	if _, err := os.Stat(path); err != nil {
		return cfg, fmt.Errorf("读取配置 %s 失败：%w（先用 config.example.toml 复制一份）", path, err)
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, fmt.Errorf("解析 %s 失败：%w", path, err)
	}
	// 单反斜杠开头（\nas\share）在 Windows 上是“当前盘根下的目录”：备份会
	// 悄悄留在本机，管理页却显示已离开本机（#46）。
	if t := cfg.Backup.Target; strings.HasPrefix(t, `\`) && !strings.HasPrefix(t, `\\`) {
		return cfg, fmt.Errorf(`解析 %s 失败：[backup] target = %q 只有一个反斜杠开头；UNC 路径要以 \\ 开头，如 '\\nas01\taskboard-backup'`, path, t)
	}
	ApplyDefaults(&cfg)
	return cfg, nil
}

// ApplyDefaults 补上缺省值。附件上限的默认值是 2026-10-16 调整后的新值
// （单文件 5MB、每帖 5 个附件 10 张插图、整单 50MB）。
func ApplyDefaults(cfg *Config) {
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if cfg.WebDir == "" {
		cfg.WebDir = "web"
	}
	if cfg.Server.ClientImageDir == "" {
		cfg.Server.ClientImageDir = "client-package"
	}
	if cfg.Attachments.MaxFileMB == 0 {
		cfg.Attachments.MaxFileMB = 5
	}
	if cfg.Attachments.MaxFilesPerPost == 0 {
		cfg.Attachments.MaxFilesPerPost = 5
	}
	if cfg.Attachments.MaxImagesPerPost == 0 {
		cfg.Attachments.MaxImagesPerPost = 10
	}
	if cfg.Attachments.MaxTicketMB == 0 {
		cfg.Attachments.MaxTicketMB = 50
	}
	if cfg.Backup.DailyAt == "" {
		cfg.Backup.DailyAt = "03:00"
	}
	if cfg.Backup.KeepDaily == 0 {
		cfg.Backup.KeepDaily = 30
	}
	if cfg.Backup.KeepMonthly == 0 {
		cfg.Backup.KeepMonthly = 12
	}
	if cfg.Backup.KeepPreupdate == 0 {
		cfg.Backup.KeepPreupdate = 5
	}
	if cfg.Update.UpdateHub.Channel == "" {
		cfg.Update.UpdateHub.Channel = "stable"
	}
	if cfg.Intake.UploadRatePerMin == 0 {
		cfg.Intake.UploadRatePerMin = 20
	}
	if cfg.Intake.PendingQuotaMB == 0 {
		cfg.Intake.PendingQuotaMB = 200
	}
}
