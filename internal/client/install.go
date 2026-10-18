package client

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/clientstamp"
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

// LifecycleDependencies 是安装、卸载和开机自启需要的窄适配器。
type LifecycleDependencies struct {
	Config    ServerConfigStore
	Autostart AutostartManager
	Toast     ToastRegistrar
	Files     FileSystem
	Process   ProcessManager
}

// ResolveServerURL 按参数、环境变量、已保存地址和自身尾部戳解析平台地址。
func ResolveServerURL(argServer, envServer string, config ServerConfigStore, executable string) (string, error) {
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
	if executable != "" {
		stamped, ok, err := clientstamp.Read(executable)
		if err != nil {
			return "", err
		}
		if ok {
			return normalizeServerURL(stamped)
		}
	}
	return "", errors.New("缺少 Taskboard 平台地址：请用 -server 指定，或从平台网页重新下载客户端")
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
	if _, err := os.Stat(installed); err == nil {
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
	if err := deps.Process.QuitExisting(); err != nil {
		return false, fmt.Errorf("通知正在运行的客户端退出失败：%w", err)
	}
	if err := deps.Process.Start(installed, []string{"--autostart"}); err != nil {
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
