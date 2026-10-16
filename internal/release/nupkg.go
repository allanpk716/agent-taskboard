package release

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// nupkgFixedTime 固定 zip 条目的时间戳，保证同样输入产出字节一致的包
// （便于复核 SHA1）。
var nupkgFixedTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// PayloadFile 是打进 nupkg 的一个发布产物。
type PayloadFile struct {
	Name    string
	Content []byte
}

// PackInput 描述一次 nupkg 打包。
type PackInput struct {
	AppID       string // UpdateHub 应用名，也是 nuspec 的 id 和 feed 的 PackageId
	Version     string
	Title       string
	Authors     string
	Description string
	Manifest    []byte // Release 的 manifest.json，原样打进包里
	Files       []PayloadFile
}

// NuspecXML 生成 nupkg 根上的 {AppID}.nuspec。字段沿用 NuGet/Velopack 的
// 最小集合：id 必须等于 appId，version 与发布版本一致。
func NuspecXML(id, version, title, authors, description string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	b.WriteString(`<package xmlns="http://schemas.microsoft.com/packaging/2010/07/nuspec.xsd">` + "\n")
	b.WriteString("  <metadata>\n")
	fmt.Fprintf(&b, "    <id>%s</id>\n", xmlEscape(id))
	fmt.Fprintf(&b, "    <version>%s</version>\n", xmlEscape(version))
	fmt.Fprintf(&b, "    <title>%s</title>\n", xmlEscape(title))
	fmt.Fprintf(&b, "    <authors>%s</authors>\n", xmlEscape(authors))
	b.WriteString("    <requireLicenseAcceptance>false</requireLicenseAcceptance>\n")
	fmt.Fprintf(&b, "    <description>%s</description>\n", xmlEscape(description))
	b.WriteString("  </metadata>\n")
	b.WriteString("</package>\n")
	return []byte(b.String())
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// PackNupkg 把 manifest.json 和产物打包成 Velopack 风格的 nupkg（zip）：
//
//	{AppID}.nuspec      包元数据，id 与 appId 一致
//	manifest.json       Release 的清单（含每个产物的 SHA256）
//	payload/{name}      产物本身
//
// 不依赖 .NET 的 vpk，更新器按 manifest.json 校验 payload/ 下的文件。
func PackNupkg(in PackInput) ([]byte, error) {
	if in.AppID == "" || in.Version == "" {
		return nil, fmt.Errorf("打包 nupkg 需要 AppID 和 Version")
	}
	if len(in.Manifest) == 0 {
		return nil, fmt.Errorf("打包 nupkg 需要 manifest.json")
	}
	if len(in.Files) == 0 {
		return nil, fmt.Errorf("打包 nupkg 至少需要一个产物文件")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, content []byte) error {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: nupkgFixedTime}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = w.Write(content)
		return err
	}
	if err := write(in.AppID+".nuspec", NuspecXML(in.AppID, in.Version, in.Title, in.Authors, in.Description)); err != nil {
		return nil, err
	}
	if err := write("manifest.json", in.Manifest); err != nil {
		return nil, err
	}
	for _, f := range in.Files {
		if strings.ContainsAny(f.Name, `/\`) || f.Name == "" {
			return nil, fmt.Errorf("产物名 %q 不能包含路径分隔符", f.Name)
		}
		if err := write("payload/"+f.Name, f.Content); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// NupkgFileName 按 Velopack 命名规则生成：{appId}-{version}-full.nupkg。
// UpdateHub 从文件名里提取版本号，不要改动各段的位置。
func NupkgFileName(appID, version string) string {
	return fmt.Sprintf("%s-%s-full.nupkg", appID, version)
}

// ReleaseFeedAsset 与 UpdateHub 的 releases.{os}.json 条目一致。
type ReleaseFeedAsset struct {
	PackageId string `json:"PackageId"`
	Version   string `json:"Version"`
	Type      string `json:"Type"` // "Full" 或 "Delta"
	FileName  string `json:"FileName"`
	SHA1      string `json:"SHA1"`
	Size      int64  `json:"Size"`
}

// ReleaseFeed 是 Velopack 的 feed 结构。
type ReleaseFeed struct {
	Assets []ReleaseFeedAsset `json:"Assets"`
}

// ReleaseFeedJSON 生成 releases.win.json：单条 Full 记录，PackageId 与 appId
// 一致，SHA1/Size 对应刚打好的 nupkg。UpdateHub 上传时会整份替换 feed，
// 所以这里只写最新一条是符合预期的。
func ReleaseFeedJSON(appID, version string, nupkg []byte) ([]byte, error) {
	sum := sha1.Sum(nupkg)
	feed := ReleaseFeed{Assets: []ReleaseFeedAsset{{
		PackageId: appID,
		Version:   version,
		Type:      "Full",
		FileName:  NupkgFileName(appID, version),
		SHA1:      hex.EncodeToString(sum[:]),
		Size:      int64(len(nupkg)),
	}}}
	return json.MarshalIndent(feed, "", "  ")
}
