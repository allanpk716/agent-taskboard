package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/release"
	"github.com/allanpk716/agent-taskboard/internal/version"
)

// GitHubRepoName 是桌面客户端独立分发使用的 GitHub 仓库。
const GitHubRepoName = "allanpk716/agent-taskboard"

const defaultClientUpdateBase = "https://github.com/" + GitHubRepoName

// UpdateProcess 是自动更新启动出的新客户端进程。
type UpdateProcess interface {
	Stop() error
}

// UpdateProcessManager 抽象自动更新专用的进程和进程间就绪信号。
type UpdateProcessManager interface {
	Start(path string, args []string) (UpdateProcess, error)
	Ready() <-chan struct{}
	Quit() <-chan struct{}
	SendReady() error
	SendQuit() error
	ResetQuit() error
}

func updateBaseURL() string {
	if base := strings.TrimRight(os.Getenv("TASKBOARD_CLIENT_UPDATE_BASE"), "/"); base != "" {
		return base
	}
	return defaultClientUpdateBase
}

func (a *App) updateJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := a.streamClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("GET %s 返回 %d：%s", url, res.StatusCode, strings.TrimSpace(string(data)))
	}
	return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out)
}

// checkUpdate 只检查并提示；成员点击 Toast 或托盘菜单后才执行 installUpdate。
func (a *App) checkUpdate(ctx context.Context) bool {
	if a.deps.Update == nil || a.deps.Lifecycle == nil {
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var latest release.Latest
	if err := a.updateJSON(checkCtx, updateBaseURL()+"/releases/download/channel-stable/latest.json", &latest); err != nil {
		return false
	}
	if latest.Client == nil || !version.Less(a.cfg.Version, latest.Client.Version) {
		return false
	}
	a.mu.Lock()
	previous := a.availableUpdate
	a.availableUpdate = *latest.Client
	_, notified := a.notifiedUpdates[latest.Client.Version]
	a.mu.Unlock()
	if previous.Tag == latest.Client.Tag && previous.Version == latest.Client.Version && notified {
		return false
	}
	a.deps.Tray.SetUpdateVersion(latest.Client.Version)
	a.mu.Lock()
	if a.notifiedUpdates == nil {
		a.notifiedUpdates = make(map[string]struct{})
	}
	a.notifiedUpdates[latest.Client.Version] = struct{}{}
	a.mu.Unlock()
	a.deps.Toaster.Show(Notification{
		Reason: "update-available",
		Text:   "Taskboard 客户端有新版本 " + latest.Client.Version + "，点击更新",
	})
	return false
}

// installUpdate 下载 manifest 指定的 exe，校验后沿用现有替换和重启机制。
func (a *App) installUpdate(ctx context.Context) bool {
	if a.deps.Update == nil || a.deps.Lifecycle == nil {
		return false
	}
	a.mu.Lock()
	if a.updating || a.availableUpdate.Tag == "" {
		a.mu.Unlock()
		return false
	}
	latest := a.availableUpdate
	a.updating = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.updating = false
		a.mu.Unlock()
	}()
	installCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var manifest release.Manifest
	if err := a.updateJSON(installCtx, latest.ManifestURL, &manifest); err != nil {
		a.showUpdateError("读取更新清单失败：" + err.Error())
		return false
	}
	entry := manifest.FileEntry("taskboard-client.exe")
	if entry == nil {
		a.showUpdateError("更新清单里没有 taskboard-client.exe")
		return false
	}
	exe, err := a.deps.Lifecycle.Files.Executable()
	if err != nil {
		a.showUpdateError("读取客户端路径失败：" + err.Error())
		return false
	}
	temp := exe + ".download"
	sum, size, err := a.downloadClient(installCtx, updateBaseURL()+"/releases/download/"+latest.Tag+"/taskboard-client.exe", temp)
	if err != nil {
		_ = os.Remove(temp)
		a.showUpdateError("下载客户端失败：" + err.Error())
		return false
	}
	if size != entry.Size || hex.EncodeToString(sum[:]) != entry.Sha256 {
		_ = os.Remove(temp)
		reason := "SHA256 不符"
		if size != entry.Size {
			reason = fmt.Sprintf("大小不符（清单 %d 字节，下载 %d 字节）", entry.Size, size)
		}
		a.showUpdateError("客户端下载内容校验失败：" + reason + "，已保留当前版本")
		return false
	}
	if a.replaceExecutable(ctx, exe, temp) {
		a.mu.Lock()
		a.availableUpdate = release.LatestEntry{}
		a.mu.Unlock()
		a.deps.Tray.SetUpdateVersion("")
		return true
	}
	a.showUpdateError("启动更新后的客户端失败，已回滚")
	return false
}

func (a *App) showUpdateError(message string) {
	a.deps.Toaster.Show(Notification{Reason: "update-error", Text: message})
}

func (a *App) downloadClient(ctx context.Context, rawURL, dest string) ([32]byte, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return [32]byte{}, 0, err
	}
	res, err := a.streamClient.Do(req)
	if err != nil {
		return [32]byte{}, 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return [32]byte{}, 0, fmt.Errorf("client download: status %d: %s", res.StatusCode, data)
	}
	f, err := os.Create(dest)
	if err != nil {
		return [32]byte{}, 0, err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, hash), res.Body)
	closeErr := f.Close()
	if err != nil {
		return [32]byte{}, 0, err
	}
	if closeErr != nil {
		return [32]byte{}, 0, closeErr
	}
	return [32]byte(hash.Sum(nil)), size, nil
}

func (a *App) replaceExecutable(ctx context.Context, exe, temp string) bool {
	prev := exe + ".prev"
	_ = os.Remove(prev)
	if err := os.Rename(exe, prev); err != nil {
		_ = os.Remove(temp)
		return false
	}
	if err := os.Rename(temp, exe); err != nil {
		_ = os.Rename(prev, exe)
		_ = os.Remove(temp)
		return false
	}
	var process UpdateProcess
	process, err := a.deps.Update.Start(exe, []string{"--after-update"})
	if err == nil {
		select {
		case <-a.deps.Update.Ready():
			if a.shutdown != nil {
				a.shutdown()
			}
			return true
		case <-a.quitSignal:
			if a.shutdown != nil {
				a.shutdown()
			}
			return true
		case <-a.deps.Clock.After(30 * time.Second):
		case <-ctx.Done():
		}
	}
	a.rollback(exe, prev, process)
	return false
}

func (a *App) rollback(exe, prev string, process UpdateProcess) {
	if process != nil {
		_ = process.Stop()
	}
	_ = os.Remove(exe)
	_ = os.Rename(prev, exe)
}
