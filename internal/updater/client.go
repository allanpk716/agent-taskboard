package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/allanpk716/agent-taskboard/internal/release"
)

const (
	clientDirName      = "client"
	clientFileName     = "taskboard-client.exe"
	clientMetadataName = "client.json"
	clientFile         = clientDirName + "/" + clientFileName
)

// ClientPackage 是服务端当前代管的桌面客户端。
type ClientPackage struct {
	Version string `json:"version"`
	Sha256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Path    string `json:"-"`
}

// ClientVersion 返回当前代管版本；尚未发布时为空串。
func ClientVersion(dataDir string) string {
	pkg, err := CurrentClientPackage(dataDir)
	if err != nil {
		return ""
	}
	return pkg.Version
}

// CurrentClientPackage 读出当前代管包，并再次校验磁盘内容，供下载接口使用。
func CurrentClientPackage(dataDir string) (*ClientPackage, error) {
	dir := filepath.Join(dataDir, clientDirName)
	raw, err := os.ReadFile(filepath.Join(dir, clientMetadataName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	var pkg ClientPackage
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return nil, fmt.Errorf("解析代管客户端清单：%w", err)
	}
	pkg.Path = filepath.Join(dir, clientFileName)
	data, err := os.ReadFile(pkg.Path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != pkg.Sha256 || int64(len(data)) != pkg.Size {
		return nil, errors.New("代管客户端与清单不符，拒绝提供下载")
	}
	return &pkg, nil
}

// InstallClient 校验并把新的 Windows/amd64 客户端放进数据目录，旧版本保留为
// taskboard-client.exe.prev。调用前应已经按发布 manifest 校验过 SHA256。
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
	current := filepath.Join(dir, clientFileName)
	prev := current + ".prev"
	newExe := current + ".new"
	metadata := filepath.Join(dir, clientMetadataName)
	newMetadata := metadata + ".new"
	raw, err := json.MarshalIndent(ClientPackage{Version: pkg.Version, Sha256: pkg.Sha256, Size: pkg.Size}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileSync(newExe, data); err != nil {
		_ = os.Remove(newExe)
		return err
	}
	if err := os.WriteFile(newMetadata, append(raw, '\n'), 0o644); err != nil {
		_ = os.Remove(newExe)
		_ = os.Remove(newMetadata)
		return err
	}
	hadCurrent := false
	if _, err := os.Stat(current); err == nil {
		hadCurrent = true
		if err := os.Remove(prev); err != nil && !os.IsNotExist(err) {
			_ = os.Remove(newExe)
			_ = os.Remove(newMetadata)
			return fmt.Errorf("删除旧客户端上一版失败：%w", err)
		}
		if err := os.Rename(current, prev); err != nil {
			_ = os.Remove(newExe)
			_ = os.Remove(newMetadata)
			return fmt.Errorf("把当前客户端改为上一版失败：%w", err)
		}
	}
	if err := os.Rename(newExe, current); err != nil {
		_ = os.Remove(newExe)
		return err
	}
	if err := os.Rename(newMetadata, metadata); err != nil {
		_ = os.Remove(newMetadata)
		if hadCurrent {
			if rb := os.Remove(current); rb == nil {
				_ = os.Rename(prev, current)
			}
		} else {
			_ = os.Remove(current)
		}
		return err
	}
	return nil
}

func verifyClient(data []byte, pkg ClientPackage) error {
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != pkg.Sha256 {
		return fmt.Errorf("客户端 %s 的 SHA256 不符：清单里是 %s，下载到的是 %s，拒绝更新", clientFileName, pkg.Sha256, got)
	}
	if pkg.Size > 0 && int64(len(data)) != pkg.Size {
		return fmt.Errorf("客户端 %s 的大小不符：清单里是 %d 字节，下载到 %d 字节", clientFileName, pkg.Size, len(data))
	}
	return nil
}
