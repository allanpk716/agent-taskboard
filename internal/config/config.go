// Package config 读取 config.toml。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
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
	// Webhook 是通知出口的部署级配置（#371）。总开关缺省关闭。
	Webhook Webhook `toml:"webhook"`
}

// Server 是 [server] 小节：部署对外信息。
type Server struct {
	// PublicURL 是用户访问本平台的对外根地址（如 https://taskboard.urithub.com，
	// 不带结尾斜杠）。配置后回给用户的访客链接与 share_text 用绝对地址，
	//「复制链接」拿到即可发送（评审 P2-7）；留空（默认）时保持相对路径，
	// 前端复制时用当前页面的 origin 拼绝对地址。
	PublicURL string `toml:"public_url"`
	// Deploy 是部署方式：空（默认）表示原地部署；container 表示运行在
	// 容器里，更新由替换镜像完成。
	Deploy string `toml:"deploy"`
}

// 部署方式。空值表示配置文件中的默认原地部署。
const (
	DeployInPlace   = ""
	DeployContainer = "container"
)

// Attachments 是 [attachments] 小节：服务端只检查上限，不压缩图片
// （压缩在前端做；spec「附件显示与安全」）。
type Attachments struct {
	// Backend 是附件对象后端：local（默认）或 oss。凭证不进配置文件。
	Backend string `toml:"backend"`
	// MaxFileMB 是单个文件的上限（MB），超过返回 413 attachment_too_large。
	MaxFileMB int `toml:"max_file_mb"`
	// MaxFilesPerPost 是每条说明或回复的附件个数上限（插图另计）。
	MaxFilesPerPost int `toml:"max_files_per_post"`
	// MaxImagesPerPost 是每条说明或回复的插图张数上限。
	MaxImagesPerPost int `toml:"max_images_per_post"`
	// MaxTicketMB 是整张工单附件合计的上限（MB），待用附件也计入。
	MaxTicketMB int `toml:"max_ticket_mb"`
	// OSS 是 backend = "oss" 时的连接参数；local 时也可以保留，供迁移命令使用。
	OSS OSS `toml:"oss"`
}

// OSS 是 [attachments.oss] 小节：只放连接参数，不放 AccessKey。
type OSS struct {
	Region           string `toml:"region"`
	Bucket           string `toml:"bucket"`
	Prefix           string `toml:"prefix"`
	InternalEndpoint string `toml:"internal_endpoint"`
	SigningHost      string `toml:"signing_host"`
}

// Intake 是 [intake] 小节：入口匿名附件上传的限流与临时配额。
type Intake struct {
	UploadRatePerMin int `toml:"upload_rate_per_min"`
	PendingQuotaMB   int `toml:"pending_quota_mb"`
	DailyLimit       int `toml:"daily_limit"`
}

// Webhook 是 [webhook] 小节：通知出口的部署级开关、地址白名单与推送窗口。
// webhook key 与加密主密钥都不在这里，只能分别由成员提交和环境变量注入。
type Webhook struct {
	// Enabled 表示是否启用通知出口；缺省 false，新部署不应有意外外呼。
	Enabled bool `toml:"enabled"`
	// AllowedBases 是部署者认可的目标 bot 平台根地址。成员只提交 bot_id，
	// 服务端用这个白名单拼接最终 webhook 地址。
	AllowedBases []string `toml:"allowed_bases"`
	// WindowStart 是推送窗口起点（含），北京时间 HH:MM。
	WindowStart string `toml:"window_start"`
	// WindowEnd 是推送窗口终点（不含），北京时间 HH:MM。
	WindowEnd string `toml:"window_end"`
}

// TimeWindow 把配置中的 HH:MM 解析成固定北京时间的时间值，供推送循环判断窗口。
func (w Webhook) TimeWindow() (time.Time, time.Time, error) {
	start, err := parseWebhookClock(w.WindowStart, "window_start")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := parseWebhookClock(w.WindowEnd, "window_end")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return start, end, nil
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
	md, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return cfg, fmt.Errorf("解析 %s 失败：%w", path, err)
	}
	if md.IsDefined("server", "client_image_dir") {
		fmt.Printf("警告：config.toml 的 [server].client_image_dir 已废弃；服务端不再代管桌面客户端，该配置已忽略\n")
	}
	// 单反斜杠开头（\nas\share）在 Windows 上是“当前盘根下的目录”：备份会
	// 悄悄留在本机，管理页却显示已离开本机（#46）。
	if t := cfg.Backup.Target; strings.HasPrefix(t, `\`) && !strings.HasPrefix(t, `\\`) {
		return cfg, fmt.Errorf(`解析 %s 失败：[backup] target = %q 只有一个反斜杠开头；UNC 路径要以 \\ 开头，如 '\\nas01\taskboard-backup'`, path, t)
	}
	if cfg.Attachments.Backend == "" {
		cfg.Attachments.Backend = attachments.AttachmentBackendLocal
	}
	if cfg.Attachments.Backend != attachments.AttachmentBackendLocal && cfg.Attachments.Backend != attachments.AttachmentBackendOSS {
		return cfg, fmt.Errorf("解析 %s 失败：[attachments] backend = %q 无效（只能是 local 或 oss）",
			path, cfg.Attachments.Backend)
	}
	// TASKBOARD_DEPLOY 优先于 [server] deploy，供镜像内置默认值覆盖仍沿用原地
	// 部署的配置文件；未设置时完全使用配置文件。
	deploy, ok := os.LookupEnv("TASKBOARD_DEPLOY")
	if ok {
		if deploy != DeployContainer && deploy != DeployInPlace {
			return cfg, fmt.Errorf("环境变量 TASKBOARD_DEPLOY = %q 无效（只能是 container 或空）", deploy)
		}
		cfg.Server.Deploy = deploy
	}
	ApplyDefaults(&cfg)
	if err := validateWebhook(cfg.Webhook); err != nil {
		return cfg, fmt.Errorf("解析 %s 失败：%w", path, err)
	}
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
	if cfg.Attachments.Backend == "" {
		cfg.Attachments.Backend = attachments.AttachmentBackendLocal
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
	if cfg.Intake.DailyLimit == 0 {
		cfg.Intake.DailyLimit = 200
	}
	if cfg.Webhook.WindowStart == "" {
		cfg.Webhook.WindowStart = "08:00"
	}
	if cfg.Webhook.WindowEnd == "" {
		cfg.Webhook.WindowEnd = "20:00"
	}
}

func validateWebhook(webhook Webhook) error {
	for _, raw := range webhook.AllowedBases {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
			parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawFragment != "" ||
			!safeWebhookBasePath(parsed.Path) {
			return fmt.Errorf("[webhook] allowed_bases = %q 无效（只能是带主机的 https 根地址，不能带用户信息、查询或片段）", raw)
		}
	}
	if webhook.Enabled && len(webhook.AllowedBases) == 0 {
		return fmt.Errorf("[webhook] enabled = true 时 allowed_bases 不能为空")
	}
	start, err := parseWebhookClock(webhook.WindowStart, "window_start")
	if err != nil {
		return err
	}
	end, err := parseWebhookClock(webhook.WindowEnd, "window_end")
	if err != nil {
		return err
	}
	if !start.Before(end) {
		return fmt.Errorf("[webhook] 推送窗口起点 %s 必须早于终点 %s（北京时间）",
			webhook.WindowStart, webhook.WindowEnd)
	}
	return nil
}

func safeWebhookBasePath(path string) bool {
	if path == "" || path == "/" {
		return true
	}
	if !strings.HasPrefix(path, "/") {
		return false
	}
	for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func parseWebhookClock(raw, field string) (time.Time, error) {
	if len(raw) != 5 || raw[2] != ':' {
		return time.Time{}, fmt.Errorf("[webhook] %s = %q 无效（北京时间，必须是 HH:MM）", field, raw)
	}
	parsed, err := time.ParseInLocation("15:04", raw, clock.Beijing)
	if err != nil {
		return time.Time{}, fmt.Errorf("[webhook] %s = %q 无效（北京时间，必须是 HH:MM）", field, raw)
	}
	return parsed, nil
}

// StorageConfig 把 [attachments] 小节转成附件后端的装配参数（#314）。
func (a Attachments) StorageConfig(dataDir string) attachments.StorageConfig {
	return attachments.StorageConfig{
		Backend: a.Backend, DataDir: dataDir,
		OSS: attachments.OSSConfig{
			Region: a.OSS.Region, Bucket: a.OSS.Bucket, Prefix: a.OSS.Prefix,
			InternalEndpoint: a.OSS.InternalEndpoint, SigningHost: a.OSS.SigningHost,
		},
	}
}
