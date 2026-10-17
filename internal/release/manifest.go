// Package release 实现发布流水线的可测试部分：Release 清单（manifest.json）、
// channel-stable 的 latest.json 合并，以及转发到 UpdateHub 的 nupkg 打包。
//
// 约定见 docs/deploy-and-release.md 的「发布」一节。
package release

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// 组件名：后端 server，前端 web。tag 形如 server/v1.2.3、web/v1.4.0。
const (
	ComponentServer = "server"
	ComponentWeb    = "web"
)

// semverPattern 只接受 X.Y.Z 形式（可带 v 前缀），与 tag 命名一致。
var semverPattern = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)$`)

// FileEntry 是发布产物（一个文件）的清单条目。
type FileEntry struct {
	Name   string `json:"name"`
	Sha256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest 是附在每个 Release 上的 manifest.json。更新器下载产物前先取它，
// 按 Sha256 校验；web 组件还用 MinServer 判断是否需要先升级后端。
type Manifest struct {
	Component string      `json:"component"` // server 或 web
	Version   string      `json:"version"`   // 不带 v 前缀，如 1.2.3
	MinServer string      `json:"min_server"`
	Files     []FileEntry `json:"files"`
}

// NormalizeVersion 校验并归一化版本号：去掉 v 前缀。
func NormalizeVersion(v string) (string, error) {
	m := semverPattern.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return "", fmt.Errorf("版本号 %q 不是 X.Y.Z 形式", v)
	}
	return m[1], nil
}

// FileEntry 返回清单里名为 name 的产物条目；没有时返回 nil。
func (m *Manifest) FileEntry(name string) *FileEntry {
	for i := range m.Files {
		if m.Files[i].Name == name {
			return &m.Files[i]
		}
	}
	return nil
}

// BuildManifest 为 dir 下的产物生成清单。names 为 nil 时取目录下全部普通文件
// （跳过已有的 manifest.json），按名称排序，保证重复构建结果一致。
func BuildManifest(component, version, minServer, dir string, names []string) (*Manifest, error) {
	if component != ComponentServer && component != ComponentWeb {
		return nil, fmt.Errorf("未知组件 %q（只能是 %s 或 %s）", component, ComponentServer, ComponentWeb)
	}
	version, err := NormalizeVersion(version)
	if err != nil {
		return nil, err
	}
	minServer, err = NormalizeVersion(minServer)
	if err != nil {
		return nil, fmt.Errorf("min_server %w", err)
	}
	if names == nil {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("读取产物目录 %s：%w", dir, err)
		}
		for _, e := range entries {
			if e.Type().IsRegular() && e.Name() != "manifest.json" {
				names = append(names, e.Name())
			}
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("产物目录 %s 下没有可发布的文件", dir)
	}
	m := &Manifest{Component: component, Version: version, MinServer: minServer}
	for _, name := range names {
		if strings.ContainsAny(name, `/\`) {
			return nil, fmt.Errorf("产物名 %q 不能包含路径分隔符", name)
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("读取产物 %s：%w", name, err)
		}
		sum := sha256.Sum256(data)
		m.Files = append(m.Files, FileEntry{
			Name:   name,
			Sha256: hex.EncodeToString(sum[:]),
			Size:   int64(len(data)),
		})
	}
	return m, nil
}
