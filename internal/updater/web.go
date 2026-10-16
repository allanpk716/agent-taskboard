// 前端更新（#45）：解压到 web-<version>/ 后切换 web/ 指向它。切换是重建
// web/ 这个目录链接（Windows junction / linux 符号链接，见 internal/fslink）：
// 服务打开的是链接指向的目标目录，链接本身随时可以重建，所以切换不需要
// 重启服务，也不会出现半个新版本（新版本先完整解压，再一瞬切换）。
package updater

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/fslink"
)

// webVersionFile 是前端目录里的版本标记文件名（更新器解压新版本时写入；
// 服务启动时也会用它报告当前前端版本）。手动部署的首个前端没有这个文件，
// 当前版本视为未知（任何已发布版本都比它新）。
const webVersionFile = "version.json"

// webVersionJSON 是 version.json 的内容。
type webVersionJSON struct {
	Component string `json:"component"`
	Version   string `json:"version"`
}

// WebVersion 返回 webDir（可以是目录链接）当前的前端版本；没有标记文件或
// 读取失败时返回空串（未知）。
func WebVersion(webDir string) string {
	data, err := os.ReadFile(filepath.Join(webDir, webVersionFile))
	if err != nil {
		return ""
	}
	var v webVersionJSON
	if err := json.Unmarshal(data, &v); err != nil {
		return ""
	}
	return v.Version
}

// extractWeb 把 zip 内容安全地解压到 <webDir 同级>/web-<version>/，并写入
// version.json 标记。目录已存在时先整个删掉重来（上次解压了一半）。
// 返回解压出的目录路径。
func extractWeb(webDir, version string, zipData []byte) (string, error) {
	parent := filepath.Dir(webDir)
	dir := filepath.Join(parent, "web-"+version)
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("清掉旧的 %s 失败：%w", filepath.Base(dir), err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", fmt.Errorf("解压前端 zip：%w", err)
	}
	for _, f := range zr.File {
		name, err := safeZipName(f.Name)
		if err != nil {
			return "", err
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return "", err
		}
	}
	marker, err := json.Marshal(webVersionJSON{Component: "web", Version: version})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, webVersionFile), append(marker, '\n'), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

// safeZipName 校验 zip 条目名并归一化为斜杠路径：拒绝绝对路径、盘符和 ..
// （发布 zip 由 CI 打包，这里仍按不可信输入处理）。
func safeZipName(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("zip 里的路径 %q 非法", name)
	}
	if len(name) >= 2 && name[1] == ':' {
		return "", fmt.Errorf("zip 里的路径 %q 非法（盘符）", name)
	}
	cleaned := path.Clean(name)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned == "." {
		return "", fmt.Errorf("zip 里的路径 %q 逃出了目标目录", name)
	}
	return cleaned, nil
}

// SwitchWebDir 把 webDir 切换成指向 newDir 的目录链接，并清理旧版本目录
// （保留最近几份，见 keepWebVersions）。
func SwitchWebDir(webDir, newDir string) error {
	if fslink.IsLink(webDir) {
		// 链接随时可以重建：先删链接（不动任何版本目录），再指向新版本。
		if err := os.Remove(webDir); err != nil {
			return fmt.Errorf("移除旧的前端链接失败：%w", err)
		}
	} else if _, err := os.Stat(webDir); err == nil {
		// web/ 还是普通目录（服务从未启动过、没做过启动时的链接化）：
		// 挪到一边留档，再建链接。
		retired := filepath.Join(filepath.Dir(webDir), "web-retired-"+time.Now().Format("20060102-150405"))
		if err := os.Rename(webDir, retired); err != nil {
			return fmt.Errorf("挪开旧的前端目录失败（服务运行中会占用它，先停服务或用管理页更新）：%w", err)
		}
	}
	if err := fslink.Make(webDir, newDir); err != nil {
		return err
	}
	pruneWebVersions(filepath.Dir(webDir), keepWebVersions)
	return nil
}

// keepWebVersions 是磁盘上保留的 web-* 版本目录数（当前 + 最近两版，正好
// 供静态服务给「翻页翻到一半的旧页面」补旧资源，见 internal/webapp）。
const keepWebVersions = 3

// pruneWebVersions 删掉 web-* 目录里最旧的，只保留 keep 份新的（按修改时间
// 排序，最近解压的最新）。
func pruneWebVersions(parent string, keep int) {
	matches, err := filepath.Glob(filepath.Join(parent, "web-*"))
	if err != nil || len(matches) <= keep {
		return
	}
	type entry struct {
		path string
		mod  time.Time
	}
	entries := make([]entry, 0, len(matches))
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil {
			entries = append(entries, entry{path: m, mod: info.ModTime()})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })
	for _, old := range entries[keep:] {
		_ = os.RemoveAll(old.path)
	}
}
