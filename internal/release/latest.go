package release

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// LatestEntry 是 latest.json 里一个组件的条目，指向该组件最新 Release 的
// manifest.json 固定下载地址（客户端不调用 api.github.com）。
type LatestEntry struct {
	Version     string `json:"version"`
	Tag         string `json:"tag"`
	ManifestURL string `json:"manifest_url"`
}

// Latest 是 channel-stable Release 里 latest.json 的内容。
type Latest struct {
	Server *LatestEntry `json:"server,omitempty"`
	Web    *LatestEntry `json:"web,omitempty"`
	Client *LatestEntry `json:"client,omitempty"`
}

// MergeLatestJSON 读改写 latest.json：只替换 component 自己的条目，
// 另一个组件的条目原样保留。existing 为空（nil 或空白）时从 {} 开始。
func MergeLatestJSON(existing []byte, component string, entry LatestEntry) ([]byte, error) {
	var l Latest
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &l); err != nil {
			return nil, fmt.Errorf("解析现有 latest.json：%w", err)
		}
	}
	switch component {
	case ComponentServer:
		l.Server = &entry
	case ComponentWeb:
		l.Web = &entry
	case ComponentClient:
		l.Client = &entry
	default:
		return nil, fmt.Errorf("未知组件 %q（只能是 %s、%s 或 %s）", component, ComponentServer, ComponentWeb, ComponentClient)
	}
	return json.MarshalIndent(l, "", "  ")
}

// ManifestURL 返回 GitHub Release 固定下载地址形式的 manifest.json 地址，
// 可直接加镜像前缀。
func ManifestURL(repo, tag string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/manifest.json", repo, tag)
}
