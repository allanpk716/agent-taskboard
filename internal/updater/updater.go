// 更新的共用核心（#45）：Check 只查不装（管理页提示有新版本用），Apply 执行
// 更新（校验哈希 → 更新前备份 → 换 exe / 切前端）。三个入口（管理页按钮、
// taskboard update、管理 API）都经由这里，保证行为一致。
package updater

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/version"
)

// Options 是一次 Check / Apply 的全部输入。
type Options struct {
	// Update 是 [update] 配置。
	Update config.Update
	// DataDir 是数据目录（更新前备份的库在这里）。
	DataDir string
	// Backup 是备份配置（更新前备份用，失败时更新中止）。
	Backup config.Backup
	// WebDir 是前端目录（切换目标）。
	WebDir string
	// ExePath 是要替换的后端 exe；空串用当前进程自己的 exe。
	ExePath string
	// ServerVersion 是当前后端版本；空串用编译时注入的 version.Server。
	ServerVersion string
	// Preupdate 做一次更新前备份，返回备份文件名；为 nil 时用默认实现
	// （打开 data_dir 的库调 ops.Backups.Preupdate）。
	Preupdate func(ctx context.Context) (string, error)
	// HTTP 可注入的客户端（测试用）。
	HTTP *http.Client
	// Log 输出进度；为空时静默。
	Log func(format string, args ...any)
	// Now 返回当前时间；为空时 time.Now。
	Now func() time.Time
	// Port 是服务监听端口；回滚时用来确认服务已停止（0 表示不检查）。
	Port int
}

func (o *Options) exePath() string {
	if o.ExePath != "" {
		return o.ExePath
	}
	exe, err := os.Executable()
	if err != nil {
		return "taskboard.exe"
	}
	return exe
}

func (o *Options) serverVersion() string {
	if o.ServerVersion != "" {
		return o.ServerVersion
	}
	return version.Server
}

func (o *Options) logf(format string, args ...any) {
	if o.Log != nil {
		o.Log(format, args...)
	}
}

func (o *Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Status 是 Check 的结果：两个组件的当前/最新版本与是否有更新。
type Status struct {
	// SourceUsed 是实际使用的更新源（按 sources 顺序第一个连得上的）。
	SourceUsed string
	// ServerCurrent / ServerLatest 是后端的当前/最新版本。
	ServerCurrent  string
	ServerLatest   string
	ServerUpdate   bool
	// WebCurrent / WebLatest / WebMinServer：前端当前/最新版本，以及最新
	// 前端要求的最低后端版本。
	WebCurrent   string
	WebLatest    string
	WebMinServer string
	WebUpdate    bool
	// WebNeedsNewerServer 报告“前端有新版但当前后端不够新”：更新时先升级后端。
	WebNeedsNewerServer bool
}

// Check 查询更新源并对比当前版本；不做任何安装动作。
func Check(ctx context.Context, opt Options) (*Status, error) {
	latest, err := resolveLatest(ctx, opt.Update, opt.HTTP)
	if err != nil {
		return nil, err
	}
	s := &Status{SourceUsed: latest.Source}
	s.ServerCurrent = opt.serverVersion()
	s.WebCurrent = WebVersion(opt.WebDir)
	if latest.Server != nil {
		s.ServerLatest = latest.Server.Manifest.Version
		s.ServerUpdate = version.Less(s.ServerCurrent, s.ServerLatest)
	}
	if latest.Web != nil {
		s.WebLatest = latest.Web.Manifest.Version
		s.WebMinServer = latest.Web.Manifest.MinServer
		s.WebUpdate = version.Less(s.WebCurrent, s.WebLatest)
	}
	if s.WebUpdate && s.WebMinServer != "" && version.Less(s.ServerCurrent, s.WebMinServer) {
		s.WebNeedsNewerServer = true
	}
	return s, nil
}

// Result 是一次 Apply 的结果（steps 按实际执行顺序记录）。
type Result struct {
	SourceUsed string
	// Steps 是按执行顺序记录的步骤描述，如「后端 0.0.0 → 1.2.3」「前端 0.0.0 → 1.4.0」。
	Steps []string
	// ServerUpdated / WebUpdated 报告是否真的替换了后端 / 前端。
	ServerUpdated bool
	WebUpdated    bool
	// RestartNeeded 报告是否需要重启服务（换了后端 exe 后由 NSSM 拉起新版本）。
	RestartNeeded bool
	// PreupdateBackup 是更新前备份的文件名。
	PreupdateBackup string
}

// Apply 检查并安装更新：
//
//  1. 按 [update].sources 顺序确定最新版本（记录实际用的源）；
//  2. 前端需要的 min_server 比当前后端新时，先升级后端；
//  3. 后端：下载校验哈希 → 更新前备份（失败即中止，不替换 exe）→ 把当前
//     exe 改名 .prev、放入新 exe；
//  4. 前端：下载校验哈希 → 解压到 web-<version>/ → 原子切换 web/。
//
// 已经是最新时是什么都不做的成功（Steps 为空）。
func Apply(ctx context.Context, opt Options) (*Result, error) {
	latest, err := resolveLatest(ctx, opt.Update, opt.HTTP)
	if err != nil {
		return nil, err
	}
	res := &Result{SourceUsed: latest.Source}
	serverCur := opt.serverVersion()
	webCur := WebVersion(opt.WebDir)

	needServer := latest.Server != nil && version.Less(serverCur, latest.Server.Manifest.Version)
	needWeb := latest.Web != nil && version.Less(webCur, latest.Web.Manifest.Version)
	if !needServer && !needWeb {
		return res, nil
	}
	if needWeb && !needServer && latest.Web.Manifest.MinServer != "" &&
		version.Less(serverCur, latest.Web.Manifest.MinServer) {
		return nil, fmt.Errorf("前端 %s 需要后端 ≥ %s，当前后端 %s 已是最新：先发布满足要求的后端版本", latest.Web.Manifest.Version, latest.Web.Manifest.MinServer, serverCur)
	}

	// 先下载并校验，全部通过后才动磁盘上的东西。
	var serverExe []byte
	if needServer {
		name := serverExeFile()
		data, err := latest.Server.File(ctx, name)
		if err != nil {
			return nil, err // 哈希不符在这里拒绝
		}
		serverExe = data
	}
	var webZip []byte
	if needWeb {
		for _, fe := range latest.Web.Manifest.Files {
			if stringsHasSuffix(fe.Name, ".zip") {
				data, err := latest.Web.File(ctx, fe.Name)
				if err != nil {
					return nil, err
				}
				webZip = data
			}
		}
		if webZip == nil {
			return nil, fmt.Errorf("web manifest 里没有 zip 产物")
		}
	}

	if needServer {
		backup, err := opt.preupdate(ctx)
		if err != nil {
			return nil, fmt.Errorf("更新前备份失败，更新中止（未替换 exe）：%w", err)
		}
		res.PreupdateBackup = backup
		if err := swapExe(opt.exePath(), serverExe); err != nil {
			return nil, err
		}
		res.ServerUpdated = true
		res.RestartNeeded = true
		res.Steps = append(res.Steps, fmt.Sprintf("后端 %s → %s", serverCur, latest.Server.Manifest.Version))
		opt.logf("后端已更新 %s → %s（更新前备份 %s）", serverCur, latest.Server.Manifest.Version, backup)
	}

	if needWeb {
		newDir, err := extractWeb(opt.WebDir, latest.Web.Manifest.Version, webZip)
		if err != nil {
			return nil, err
		}
		if err := SwitchWebDir(opt.WebDir, newDir); err != nil {
			return nil, err
		}
		res.WebUpdated = true
		res.Steps = append(res.Steps, fmt.Sprintf("前端 %s → %s", webCur, latest.Web.Manifest.Version))
		opt.logf("前端已更新 %s → %s（web-<%s> 已切换）", webCur, latest.Web.Manifest.Version, latest.Web.Manifest.Version)
	}
	return res, nil
}

// preupdate 执行更新前备份（与定时备份同一段代码，见 internal/ops）。
func (o *Options) preupdate(ctx context.Context) (string, error) {
	if o.Preupdate != nil {
		return o.Preupdate(ctx)
	}
	return defaultPreupdate(ctx, o)
}

func stringsHasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// serverExeFile 返回当前平台应下载的后端产物名（发布产物里 Windows 主部署，
// 另附 linux/amd64 版本）。
func serverExeFile() string {
	if os.IsPathSeparator('\\') && os.PathSeparator == '\\' {
		return "taskboard.exe"
	}
	return "taskboard-linux-amd64"
}

// Describe 把状态整理成命令行可读的几行文字（taskboard update -check 用）。
func (s *Status) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "实际使用源：%s\n", s.SourceUsed)
	serverNote := "已是最新"
	if s.ServerUpdate {
		serverNote = "有新版本"
	}
	fmt.Fprintf(&b, "后端：当前 %s，最新 %s（%s）\n", orUnknown(s.ServerCurrent), orUnknown(s.ServerLatest), serverNote)
	webNote := "已是最新"
	if s.WebUpdate {
		webNote = "有新版本"
		if s.WebMinServer != "" {
			webNote += fmt.Sprintf("，需要后端 ≥ %s", s.WebMinServer)
		}
		if s.WebNeedsNewerServer {
			webNote += "；当前后端不够，更新时会先升级后端"
		}
	}
	fmt.Fprintf(&b, "前端：当前 %s，最新 %s（%s）\n", orUnknown(s.WebCurrent), orUnknown(s.WebLatest), webNote)
	return b.String()
}

func orUnknown(v string) string {
	if v == "" {
		return "未知"
	}
	return v
}
