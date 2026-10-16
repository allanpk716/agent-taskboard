package release

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

// UpdateHub（Velopack 协议）只登记 .nupkg：一个 zip，根上放 {PackageId}.nuspec。
// 我们的更新器从 nupkg 里读 manifest.json 并按它校验 payload/ 下每个产物。
// feed（releases.win.json）里的 PackageId 必须与 appId 一致（不区分大小写）。

const testManifest = `{"component":"server","version":"1.2.3","min_server":"1.2.3","files":[]}`

type nuspecMetadata struct {
	ID          string `xml:"id"`
	Version     string `xml:"version"`
	Title       string `xml:"title"`
	Authors     string `xml:"authors"`
	Description string `xml:"description"`
}

type nuspecXML struct {
	Metadata nuspecMetadata `xml:"metadata"`
}

func unzip(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("打开 nupkg：%v", err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("打开 zip 条目 %s：%v", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("读取 zip 条目 %s：%v", f.Name, err)
		}
		out[f.Name] = content
	}
	return out
}

func TestPackNupkg的布局包含nuspec和manifest和产物(t *testing.T) {
	nupkg, err := PackNupkg(PackInput{
		AppID:       "agent-taskboard-server",
		Version:     "1.2.3",
		Title:       "Agent Taskboard Server",
		Authors:     "agent-taskboard 维护者",
		Description: "部门工单平台后端",
		Manifest:    []byte(testManifest),
		Files: []PayloadFile{
			{Name: "taskboard.exe", Content: []byte("windows-binary\n")},
			{Name: "taskboard-linux-amd64", Content: []byte("linux-binary\n")},
		},
	})
	if err != nil {
		t.Fatalf("PackNupkg：%v", err)
	}
	entries := unzip(t, nupkg)
	if len(entries) != 4 {
		t.Fatalf("期望 4 个条目，得到 %d：%v", len(entries), keysOf(entries))
	}
	// manifest.json 原样打进包里。
	if string(entries["manifest.json"]) != testManifest {
		t.Fatalf("manifest.json 内容变了：%s", entries["manifest.json"])
	}
	// 产物放在 payload/ 下，内容不变。
	if string(entries["payload/taskboard.exe"]) != "windows-binary\n" {
		t.Fatalf("payload/taskboard.exe 内容不对：%q", entries["payload/taskboard.exe"])
	}
	if string(entries["payload/taskboard-linux-amd64"]) != "linux-binary\n" {
		t.Fatalf("payload/taskboard-linux-amd64 内容不对：%q", entries["payload/taskboard-linux-amd64"])
	}
	// .nuspec 是合法 XML，id 与 appId 一致，版本正确。
	var ns nuspecXML
	if err := xml.Unmarshal(entries["agent-taskboard-server.nuspec"], &ns); err != nil {
		t.Fatalf("解析 .nuspec：%v\n%s", err, entries["agent-taskboard-server.nuspec"])
	}
	if ns.Metadata.ID != "agent-taskboard-server" || ns.Metadata.Version != "1.2.3" {
		t.Fatalf("nuspec 的 id/version 不对：%+v", ns.Metadata)
	}
	if ns.Metadata.Title != "Agent Taskboard Server" || ns.Metadata.Description != "部门工单平台后端" {
		t.Fatalf("nuspec 的 title/description 不对：%+v", ns.Metadata)
	}
}

func TestPackNupkg同名输入产物保持稳定可复现(t *testing.T) {
	input := PackInput{
		AppID: "agent-taskboard-web", Version: "1.4.0",
		Title: "t", Authors: "a", Description: "d",
		Manifest: []byte(testManifest),
		Files:    []PayloadFile{{Name: "taskboard-web-1.4.0.zip", Content: []byte("web\n")}},
	}
	first, err := PackNupkg(input)
	if err != nil {
		t.Fatalf("第一次打包：%v", err)
	}
	second, err := PackNupkg(input)
	if err != nil {
		t.Fatalf("第二次打包：%v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("同样输入应产出字节一致的 nupkg（时间戳固定），否则 SHA1 无法复核")
	}
}

func TestPackNupkg拒绝路径分隔符(t *testing.T) {
	err := func() error {
		_, err := PackNupkg(PackInput{
			AppID: "agent-taskboard-web", Version: "1.4.0",
			Manifest: []byte(testManifest),
			Files:    []PayloadFile{{Name: "../evil", Content: nil}},
		})
		return err
	}()
	if err == nil || !strings.Contains(err.Error(), "../evil") {
		t.Fatalf("产物名不允许路径分隔符：%v", err)
	}
}

type feedAsset struct {
	PackageId string `json:"PackageId"`
	Version   string `json:"Version"`
	Type      string `json:"Type"`
	FileName  string `json:"FileName"`
	SHA1      string `json:"SHA1"`
	Size      int64  `json:"Size"`
}

func TestReleaseFeedJSON的PackageId与appId一致并带SHA1(t *testing.T) {
	appID, version := "agent-taskboard-server", "1.2.3"
	nupkg := []byte("pretend this is a zip")
	data, err := ReleaseFeedJSON(appID, version, nupkg)
	if err != nil {
		t.Fatalf("ReleaseFeedJSON：%v", err)
	}
	var feed struct {
		Assets []feedAsset `json:"Assets"`
	}
	if err := json.Unmarshal(data, &feed); err != nil {
		t.Fatalf("解析 feed：%v\n%s", err, data)
	}
	if len(feed.Assets) != 1 {
		t.Fatalf("feed 应只有一个条目：%s", data)
	}
	a := feed.Assets[0]
	sum := sha1.Sum(nupkg)
	if a.PackageId != appID {
		t.Fatalf("PackageId %q 应等于 appId %q", a.PackageId, appID)
	}
	if a.Version != version || a.Type != "Full" {
		t.Fatalf("Version/Type 不对：%+v", a)
	}
	if a.FileName != "agent-taskboard-server-1.2.3-full.nupkg" {
		t.Fatalf("FileName 不对：%s", a.FileName)
	}
	if a.SHA1 != hex.EncodeToString(sum[:]) {
		t.Fatalf("SHA1 与 nupkg 内容不符：%s", a.SHA1)
	}
	if a.Size != int64(len(nupkg)) {
		t.Fatalf("Size %d 不等于 nupkg 大小 %d", a.Size, len(nupkg))
	}
}

func keysOf(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
