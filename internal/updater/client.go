package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/release"
)

const (
	clientDirName      = "client"
	clientFileName     = "taskboard-client.exe"
	clientMetadataName = "client.json"
)

// ClientPackage 是服务端当前代管的桌面客户端。
type ClientPackage struct {
	Version string `json:"version"`
	Sha256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Path    string `json:"-"`
}

type clientMetadata struct {
	Current  *ClientPackage `json:"current"`
	Previous *ClientPackage `json:"previous,omitempty"`
}

// ClientVersion 只读 client.json，不校验整包内容；下载路径仍按需校验。
func ClientVersion(dataDir string) string {
	meta, err := readClientMetadata(dataDir)
	if err != nil || meta.Current == nil {
		return ""
	}
	return meta.Current.Version
}

// CurrentClientPackage 读出当前代管包并校验磁盘内容，供未命中缓存的下载请求使用。
func CurrentClientPackage(dataDir string) (*ClientPackage, error) {
	meta, err := readClientMetadata(dataDir)
	if err != nil {
		return nil, err
	}
	if meta.Current == nil {
		return nil, os.ErrNotExist
	}
	if err := verifyClientPackage(meta.Current); err != nil {
		return nil, err
	}
	return meta.Current, nil
}

// CurrentClientMetadata 只读元数据，不读取整包内容。
func CurrentClientMetadata(dataDir string) (*ClientPackage, error) {
	meta, err := readClientMetadata(dataDir)
	if err != nil {
		return nil, err
	}
	if meta.Current == nil {
		return nil, os.ErrNotExist
	}
	return meta.Current, nil
}

// PreviousClientPackage 返回上一版代管包；尚未有上一版时返回 os.ErrNotExist。
func PreviousClientPackage(dataDir string) (*ClientPackage, error) {
	meta, err := readClientMetadata(dataDir)
	if err != nil {
		return nil, err
	}
	if meta.Previous == nil {
		return nil, os.ErrNotExist
	}
	return meta.Previous, nil
}

func readClientMetadata(dataDir string) (*clientMetadata, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, clientDirName, clientMetadataName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	var meta clientMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("解析代管客户端清单：%w", err)
	}
	dir := filepath.Join(dataDir, clientDirName)
	if meta.Current != nil {
		meta.Current.Path = clientVersionPath(dir, meta.Current.Version)
	}
	if meta.Previous != nil {
		meta.Previous.Path = clientVersionPath(dir, meta.Previous.Version)
	}
	return &meta, nil
}

// InstallClient 校验新包，按版本落盘，并原子切换 client.json。当前和上一版
// 使用不同文件名，下载请求不会看到两次 rename 之间的缺文件状态。
func InstallClient(dataDir string, data []byte, pkg ClientPackage) error {
	if err := release.CheckExecutable(data, "windows", "amd64"); err != nil {
		return fmt.Errorf("客户端不是 windows/amd64 可执行文件，拒绝代管：%w", err)
	}
	if err := verifyClient(data, pkg); err != nil {
		return err
	}
	dir := filepath.Join(dataDir, clientDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	meta, _ := readClientMetadata(dataDir)
	current := ClientPackage{Version: pkg.Version, Sha256: pkg.Sha256, Size: pkg.Size}
	current.Path = clientVersionPath(dir, pkg.Version)
	next := clientMetadata{Current: &current}
	if meta != nil && meta.Current != nil && meta.Current.Version != pkg.Version {
		previous := *meta.Current
		next.Previous = &previous
	} else if meta != nil {
		next.Previous = meta.Previous
	}
	newExe := clientVersionPath(dir, pkg.Version)
	tempExe := newExe + ".new"
	if err := writeFileSync(tempExe, data); err != nil {
		_ = os.Remove(tempExe)
		return err
	}
	if err := os.Rename(tempExe, newExe); err != nil {
		_ = os.Remove(tempExe)
		return err
	}
	raw, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	metadata := filepath.Join(dir, clientMetadataName)
	newMetadata := metadata + ".new"
	if err := os.WriteFile(newMetadata, append(raw, '\n'), 0o644); err != nil {
		_ = os.Remove(newMetadata)
		return err
	}
	if err := os.Rename(newMetadata, metadata); err != nil {
		_ = os.Remove(newMetadata)
		return err
	}
	removeOldClientVersions(dir, next)
	return nil
}

func verifyClientPackage(pkg *ClientPackage) error {
	data, err := os.ReadFile(pkg.Path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != pkg.Sha256 || int64(len(data)) != pkg.Size {
		return errors.New("代管客户端与清单不符，拒绝提供下载")
	}
	return nil
}

func verifyClient(data []byte, pkg ClientPackage) error {
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != pkg.Sha256 {
		return fmt.Errorf("客户端 %s 的 SHA256 不符：清单里是 %s，下载到的是 %s，拒绝更新",
			clientFileName, pkg.Sha256, got)
	}
	if pkg.Size > 0 && int64(len(data)) != pkg.Size {
		return fmt.Errorf("客户端 %s 的大小不符：清单里是 %d 字节，下载到 %d 字节",
			clientFileName, pkg.Size, len(data))
	}
	return nil
}

func clientVersionPath(dir, version string) string {
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, version)
	if safe == "" || safe == "." || safe == ".." {
		safe = "unknown"
	}
	return filepath.Join(dir, "taskboard-client-"+safe+".exe")
}

func removeOldClientVersions(dir string, keep clientMetadata) {
	keepPaths := map[string]bool{}
	if keep.Current != nil {
		keepPaths[keep.Current.Path] = true
	}
	if keep.Previous != nil {
		keepPaths[keep.Previous.Path] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "taskboard-client-") ||
			!strings.HasSuffix(entry.Name(), ".exe") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if !keepPaths[path] {
			_ = os.Remove(path)
		}
	}
}
