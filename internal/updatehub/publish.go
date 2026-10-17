package updatehub

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/release"
)

// tagPattern 匹配发布 tag：server/v1.2.3、web/v1.4.0。
var tagPattern = regexp.MustCompile(`^(server|web)/v?(\d+\.\d+\.\d+)$`)

// AppIDForComponent 返回 UpdateHub 上的应用名：agent-taskboard-server /
// agent-taskboard-web（appId 只能字母数字和连字符）。
func AppIDForComponent(component string) (string, error) {
	switch component {
	case release.ComponentServer, release.ComponentWeb:
		return "agent-taskboard-" + component, nil
	default:
		return "", fmt.Errorf("未知组件 %q", component)
	}
}

// PublishOptions 描述一次「下载 GitHub Release 产物 → 打包 nupkg → 上传
// UpdateHub」的转发。仓库公开，地址和 token 都由调用方（CLI）从环境变量
// 取得，不落盘。
type PublishOptions struct {
	// DownloadBaseURL 是 Release 固定下载地址的基址，形如
	// https://github.com/{repo}/releases/download/{tag}/（可加镜像前缀）。
	DownloadBaseURL string
	// Tag 形如 server/v1.2.3。
	Tag string
	// Channel 是 UpdateHub 通道，默认 stable。
	Channel string
	// Notes 附在 UpdateHub 版本记录上的备注。
	Notes string
	// OutDir 保存下载的产物、nupkg 和 feed，便于复核；为空时用临时目录。
	OutDir string
	// HubBaseURL、HubToken 来自环境变量 UPDATEHUB_BASE_URL / UPDATEHUB_TOKEN。
	HubBaseURL string
	HubToken   string
	// GitHubToken 可选：私有仓库或避免匿名限流时使用。
	GitHubToken string
	// DryRun 只下载和打包，不上传。
	DryRun bool
	// HTTP 可注入的 HTTP 客户端（测试用）。
	HTTP *http.Client
	// Log 输出进度；为空时静默。
	Log func(format string, args ...any)
}

// PublishResult 是一次转发的结果。
type PublishResult struct {
	AppID     string
	Channel   string
	Version   string
	Component string
	NupkgName string
	NupkgSHA1 string
	NupkgSize int64
	Files     []string // 下载并校验过的产物
	OutDir    string
}

// Publish 执行转发：
//
//  1. 从 DownloadBaseURL 下载 manifest.json，核对它与 tag 一致；
//  2. 下载全部产物并按 SHA256 校验；
//  3. 生成 .nuspec，和 manifest.json、产物一起打成 nupkg；
//  4. 生成 releases.win.json（PackageId 与 appId 一致）；
//  5. 上传到 UpdateHub，再读公开 feed 确认已登记。
func Publish(ctx context.Context, opt PublishOptions) (*PublishResult, error) {
	m := tagPattern.FindStringSubmatch(strings.TrimSpace(opt.Tag))
	if m == nil {
		return nil, fmt.Errorf("tag %q 应形如 server/v1.2.3 或 web/v1.4.0", opt.Tag)
	}
	component, version := m[1], m[2]
	appID, err := AppIDForComponent(component)
	if err != nil {
		return nil, err
	}
	channel := opt.Channel
	if channel == "" {
		channel = "stable"
	}
	logf := opt.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	httpClient := opt.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	get := func(name string) ([]byte, error) {
		return download(ctx, httpClient, strings.TrimRight(opt.DownloadBaseURL, "/")+"/"+name, opt.GitHubToken)
	}

	if opt.OutDir == "" {
		dir, err := os.MkdirTemp("", "publish-updatehub-")
		if err != nil {
			return nil, err
		}
		opt.OutDir = dir
	}
	if err := os.MkdirAll(opt.OutDir, 0o755); err != nil {
		return nil, err
	}

	// 1. 清单。
	logf("下载 %s/manifest.json", opt.Tag)
	manifestRaw, err := get("manifest.json")
	if err != nil {
		return nil, fmt.Errorf("下载 manifest.json：%w", err)
	}
	var manifest release.Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return nil, fmt.Errorf("解析 manifest.json：%w", err)
	}
	if manifest.Component != component || manifest.Version != version {
		return nil, fmt.Errorf("manifest.json（%s %s）与 tag %s 不一致，拒绝转发", manifest.Component, manifest.Version, opt.Tag)
	}
	if len(manifest.Files) == 0 {
		return nil, fmt.Errorf("manifest.json 里没有产物")
	}

	// 2. 产物 + SHA256 校验。
	var payloads []release.PayloadFile
	for _, fe := range manifest.Files {
		logf("下载 %s（%d 字节）", fe.Name, fe.Size)
		data, err := get(fe.Name)
		if err != nil {
			return nil, fmt.Errorf("下载产物 %s：%w", fe.Name, err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != fe.Sha256 {
			return nil, fmt.Errorf("产物 %s 的 SHA256 不符：manifest 里是 %s，下载到的是 %s", fe.Name, fe.Sha256, got)
		}
		if err := os.WriteFile(filepath.Join(opt.OutDir, fe.Name), data, 0o644); err != nil {
			return nil, err
		}
		payloads = append(payloads, release.PayloadFile{Name: fe.Name, Content: data})
	}

	// 3. nupkg。
	title := "Agent Taskboard Server"
	if component == release.ComponentWeb {
		title = "Agent Taskboard Web"
	}
	nupkg, err := release.PackNupkg(release.PackInput{
		AppID:       appID,
		Version:     version,
		Title:       title,
		Authors:     "agent-taskboard 维护者",
		Description: fmt.Sprintf("%s %s（GitHub Release %s）", title, version, opt.Tag),
		Manifest:    manifestRaw,
		Files:       payloads,
	})
	if err != nil {
		return nil, err
	}
	nupkgName := release.NupkgFileName(appID, version)
	feed, err := release.ReleaseFeedJSON(appID, version, nupkg)
	if err != nil {
		return nil, err
	}
	nupkgPath := filepath.Join(opt.OutDir, nupkgName)
	if err := os.WriteFile(nupkgPath, nupkg, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(opt.OutDir, "releases.win.json"), feed, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(opt.OutDir, "manifest.json"), manifestRaw, 0o644); err != nil {
		return nil, err
	}
	sum1 := sha256Sum1(nupkg)
	logf("打包 %s（%d 字节，SHA1 %s）", nupkgName, len(nupkg), sum1)

	res := &PublishResult{
		AppID:     appID,
		Channel:   channel,
		Version:   version,
		Component: component,
		NupkgName: nupkgName,
		NupkgSHA1: sum1,
		NupkgSize: int64(len(nupkg)),
		OutDir:    opt.OutDir,
	}
	for _, fe := range manifest.Files {
		res.Files = append(res.Files, fe.Name)
	}

	// 4. 上传 + 确认。
	if opt.DryRun {
		logf("dry-run：不上传，产物在 %s", opt.OutDir)
		return res, nil
	}
	if opt.HubBaseURL == "" {
		return nil, fmt.Errorf("缺少 UpdateHub 地址（环境变量 UPDATEHUB_BASE_URL）")
	}
	client := &Client{BaseURL: opt.HubBaseURL, Token: opt.HubToken, HTTP: httpClient}
	upload, err := client.Upload(ctx, appID, channel, []UploadFile{
		{Name: "releases.win.json", Content: feed},
		{Name: nupkgName, Content: nupkg},
	}, opt.Notes)
	if err != nil {
		return nil, fmt.Errorf("上传 UpdateHub：%w", err)
	}
	logf("已上传到 %s/%s：%d 个文件，登记版本 %v", appID, channel, upload.FilesReceived, upload.VersionsAdded)

	gotFeed, err := client.FetchFeed(ctx, appID, channel, "win")
	if err != nil {
		return nil, fmt.Errorf("上传后核对 feed：%w", err)
	}
	for _, a := range gotFeed.Assets {
		if a.FileName == nupkgName && a.Version == version && strings.EqualFold(a.PackageId, appID) &&
			a.SHA1 == sum1 && a.Size == int64(len(nupkg)) {
			logf("feed 已登记 %s %s", a.PackageId, a.Version)
			return res, nil
		}
	}
	return nil, fmt.Errorf("上传后 feed 里没有找到 %s %s 的记录", appID, version)
}

func download(ctx context.Context, httpClient *http.Client, url, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		return nil, fmt.Errorf("GET %s 返回 %d：%s", url, res.StatusCode, strings.TrimSpace(string(body)))
	}
	// 产物可能上百 MB，边读边限制在 UpdateHub 单次上传上限附近。
	return io.ReadAll(io.LimitReader(res.Body, maxUploadBytes+1))
}

func sha256Sum1(data []byte) string {
	// SHA1 只用于 Velopack feed 的登记与核对（SHA256 才是产物校验）。
	//nolint:gosec // Velopack 协议使用 SHA1
	sum := sha1.Sum(data)
	return hex.EncodeToString(sum[:])
}
