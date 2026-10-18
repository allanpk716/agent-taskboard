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
	"time"

	"github.com/allanpk716/agent-taskboard/internal/version"
)

// UpdateProcess 是自动更新启动出的新客户端进程。
type UpdateProcess interface {
	Stop() error
}

// UpdateProcessManager 抽象自动更新专用的进程和进程间就绪信号。
type UpdateProcessManager interface {
	Start(path string, args []string) (UpdateProcess, error)
	Ready() <-chan struct{}
	SendReady() error
}

type clientLatest struct {
	Version     string `json:"version"`
	Sha256      string `json:"sha256"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"download_url"`
}

func (a *App) checkUpdate(ctx context.Context, token string) bool {
	if a.deps.Update == nil || a.deps.Lifecycle == nil {
		return false
	}
	a.mu.Lock()
	if a.updating {
		a.mu.Unlock()
		return false
	}
	a.updating = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.updating = false
		a.mu.Unlock()
	}()

	latest, err := a.clientLatest(ctx, token)
	if err != nil || !version.Less(a.cfg.Version, latest.Version) {
		return false
	}
	exe, err := a.deps.Lifecycle.Files.Executable()
	if err != nil {
		return false
	}
	temp := exe + ".download"
	sum, size, err := a.downloadClient(ctx, token, latest.DownloadURL, temp)
	if err != nil {
		_ = os.Remove(temp)
		return false
	}
	if size != latest.Size || hex.EncodeToString(sum[:]) != latest.Sha256 {
		_ = os.Remove(temp)
		return false
	}
	return a.replaceExecutable(ctx, exe, temp)
}

func (a *App) clientLatest(ctx context.Context, token string) (clientLatest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.ServerURL+"/api/v1/client/latest", nil)
	if err != nil {
		return clientLatest{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", a.userAgent())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return clientLatest{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return clientLatest{}, fmt.Errorf("client latest: status %d: %s", res.StatusCode, data)
	}
	var latest clientLatest
	if err := json.NewDecoder(res.Body).Decode(&latest); err != nil {
		return clientLatest{}, err
	}
	return latest, nil
}

func (a *App) downloadClient(ctx context.Context, token, rawURL, dest string) ([32]byte, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.absolute(rawURL), nil)
	if err != nil {
		return [32]byte{}, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", a.userAgent())
	res, err := http.DefaultClient.Do(req)
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
