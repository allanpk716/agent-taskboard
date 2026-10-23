package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ServerConfigStore 保存已安装客户端连接的平台地址。
type ServerConfigStore interface {
	ServerURL() (string, bool, error)
	SaveServerURL(rawURL string) error
	DeleteServerURL() error
}

// AutostartManager 读写当前用户的 Windows Run 自启项。
type AutostartManager interface {
	Enable(command string) error
	Disable() error
}

// ToastRegistrar 注销 Toast 的 AUMID 注册。
type ToastRegistrar interface {
	Unregister() error
}

// FileSystem 执行用户目录内的安装复制。
type FileSystem interface {
	InstallDir() (string, error)
	Executable() (string, error)
	Exists(path string) (bool, error)
	CopyExecutable(dest string) error
	RenameExecutable(old, dest string) error
	RemoveExecutable(path string) error
}

// ProcessManager 启动安装后的进程并安排延迟删除。
type ProcessManager interface {
	Start(path string, args []string) error
	DeleteLater(path string) error
	QuitExisting() error
}

// InstallAlert 在首次复制安装完成后提示用户。对话框必须阻塞到用户确认。
type InstallAlert interface {
	NotifyFirstInstall() error
}

// ServerURLPrompter 弹出原生输入框，让成员填写或修改平台地址。probeErr
// 非空时对话框应显示上次探测失败原因并允许重填。
type ServerURLPrompter interface {
	PromptServerURL(current string, probeErr error) (string, bool, error)
}

// FatalReporter 把致命错误写进固定日志，并向用户展示系统对话框。
type FatalReporter interface {
	Infof(format string, args ...any)
	Fatalf(exitCode int, format string, args ...any)
}

// LifecycleDependencies 是安装、卸载和开机自启需要的窄适配器。
type LifecycleDependencies struct {
	Config       ServerConfigStore
	Autostart    AutostartManager
	Toast        ToastRegistrar
	Files        FileSystem
	Process      ProcessManager
	InstallAlert InstallAlert
}

// ErrServerURLRequired 表示参数、环境变量和本机配置都没有平台地址。
var ErrServerURLRequired = errors.New("缺少 Taskboard 平台地址")

// ResolveServerURL 按参数、环境变量和已保存地址解析平台地址。
func ResolveServerURL(argServer, envServer string, config ServerConfigStore) (string, error) {
	for _, candidate := range []string{strings.TrimSpace(argServer), strings.TrimSpace(envServer)} {
		if candidate != "" {
			return normalizeServerURL(candidate)
		}
	}
	if config != nil {
		saved, ok, err := config.ServerURL()
		if err != nil {
			return "", err
		}
		if ok && strings.TrimSpace(saved) != "" {
			return normalizeServerURL(saved)
		}
	}
	return "", ErrServerURLRequired
}

// ResolveInteractiveServerURL 按固定顺序解析地址；没有来源时弹框让成员填写，
// 探测通过后保存。取消时返回 false。
func ResolveInteractiveServerURL(ctx context.Context, argServer, envServer string, config ServerConfigStore, prompter ServerURLPrompter, httpClient *http.Client) (string, bool, error) {
	rawURL, err := ResolveServerURL(argServer, envServer, config)
	if err == nil {
		return rawURL, true, nil
	}
	if !errors.Is(err, ErrServerURLRequired) || prompter == nil {
		return "", false, err
	}
	current := ""
	if config != nil {
		if saved, ok, getErr := config.ServerURL(); getErr == nil && ok {
			current = saved
		}
	}
	var probeErr error
	for {
		input, ok, promptErr := prompter.PromptServerURL(current, probeErr)
		if promptErr != nil {
			return "", false, promptErr
		}
		if !ok {
			return "", false, nil
		}
		current = input // 重填时保留成员刚输入的内容
		rawURL, err = normalizeServerURL(input)
		if err != nil {
			probeErr = err
			continue
		}
		probeErr = ProbeServerURL(ctx, rawURL, httpClient)
		if probeErr != nil {
			continue
		}
		if config != nil {
			if err := config.SaveServerURL(rawURL); err != nil {
				return "", false, err
			}
		}
		return rawURL, true, nil
	}
}

// ProbeServerURL 验证地址确实是 taskboard 平台。
func ProbeServerURL(ctx context.Context, rawURL string, httpClient *http.Client) error {
	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL+"/api/v1/health", nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("连接失败：%w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("这个地址不是 taskboard 平台（HTTP %d）", res.StatusCode)
	}
	var body struct {
		ServerVersion string `json:"server_version"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil || body.ServerVersion == "" {
		return errors.New("这个地址不是 taskboard 平台")
	}
	return nil
}

// InstallIfNeeded 在安装目录外启动时完成自安装，返回 true 表示调用方应退出。
func InstallIfNeeded(deps LifecycleDependencies, serverURL string) (bool, error) {
	exe, err := deps.Files.Executable()
	if err != nil {
		return false, err
	}
	dir, err := deps.Files.InstallDir()
	if err != nil {
		return false, err
	}
	installed := filepath.Join(dir, "taskboard-client.exe")
	if samePath(exe, installed) {
		_ = deps.Files.RemoveExecutable(installed + ".old")
		if err := installUserState(deps, installed, serverURL); err != nil {
			return false, err
		}
		return false, nil
	}
	targetExists, err := deps.Files.Exists(installed)
	if err != nil {
		return false, fmt.Errorf("检查安装目标失败：%w", err)
	}
	if targetExists {
		if err := deps.Files.RenameExecutable(installed, installed+".old"); err != nil {
			return false, fmt.Errorf("备份正在运行的客户端失败：%w", err)
		}
	}
	if err := deps.Files.CopyExecutable(installed); err != nil {
		return false, fmt.Errorf("复制客户端到用户目录失败：%w", err)
	}
	if err := installUserState(deps, installed, serverURL); err != nil {
		return false, err
	}
	if deps.InstallAlert != nil {
		if err := deps.InstallAlert.NotifyFirstInstall(); err != nil {
			return false, fmt.Errorf("显示首次安装提示失败：%w", err)
		}
	}
	if err := deps.Process.QuitExisting(); err != nil {
		return false, fmt.Errorf("通知正在运行的客户端退出失败：%w", err)
	}
	if err := deps.Process.Start(installed, []string{"--after-install"}); err != nil {
		return false, fmt.Errorf("从安装位置重启客户端失败：%w", err)
	}
	return true, nil
}

func installUserState(deps LifecycleDependencies, installed, serverURL string) error {
	if err := deps.Config.SaveServerURL(serverURL); err != nil {
		return err
	}
	return deps.Autostart.Enable(`"` + installed + `" --autostart`)
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func normalizeServerURL(rawURL string) (string, error) {
	rawURL = strings.TrimRight(strings.TrimSpace(rawURL), "/")
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("Taskboard 平台地址必须是 http 或 https 绝对地址")
	}
	return rawURL, nil
}

func (a *App) uninstall() {
	a.mu.Lock()
	token := a.token
	cancel := a.streamCancel
	shutdown := a.shutdown
	a.mu.Unlock()
	_ = a.deleteJSON(context.Background(), token, "/api/v1/me/clients/self")
	_ = a.deps.Store.Delete()
	if lifecycle := a.deps.Lifecycle; lifecycle != nil {
		_ = lifecycle.Autostart.Disable()
		_ = lifecycle.Toast.Unregister()
		_ = lifecycle.Config.DeleteServerURL()
		if exe, err := lifecycle.Files.Executable(); err == nil {
			_ = lifecycle.Process.DeleteLater(exe)
		}
	}
	if cancel != nil {
		cancel()
	}
	if shutdown != nil {
		shutdown()
	}
}
