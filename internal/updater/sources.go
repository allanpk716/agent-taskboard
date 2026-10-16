// Package updater 实现「更新」的共用核心（#45，docs/deploy-and-release.md
// 「更新」一节）：管理页按钮、taskboard update 命令和管理 API 三个入口共用。
//
// 更新源按 [update].sources 顺序回退，用第一个连得上的源并记录实际用的
// 是哪个。GitHub 源只用 releases/download/ 下的固定地址（不调用 api.github.com），
// 支持镜像前缀（按顺序尝试，最后直连）和代理（[update.github].proxy 或
// HTTPS_PROXY 环境变量）。UpdateHub 源按 feed 里的 SHA1 校验 nupkg，解包后
// 再按 manifest 里的 SHA256 校验产物。
package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // UpdateHub/Velopack feed 协议使用 SHA1
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/release"
	"github.com/allanpk716/agent-taskboard/internal/updatehub"
)

// Latest 是一次成功的源查询：两个组件各自的最新清单，以及实际用的源。
// 源上还没有某组件时对应字段为 nil。
type Latest struct {
	Source string
	Server *Component
	Web    *Component
}

// Component 是一个组件在某个源上的最新版本。File 返回产物内容并按
// manifest 里的 SHA256 校验（UpdateHub 源还会先按 feed 的 SHA1 校验 nupkg）。
type Component struct {
	Manifest *release.Manifest
	File     func(ctx context.Context, name string) ([]byte, error)
}

// Source 是一个更新源：返回两个组件的最新版本。
type Source interface {
	Name() string
	Latest(ctx context.Context) (*Latest, error)
}

// resolveLatest 按 cfg.Sources 的顺序尝试，用第一个连得上的源；错误里带上
// 每个源的失败原因。httpClient 为 nil 时用默认客户端。
func resolveLatest(ctx context.Context, cfg config.Update, httpClient *http.Client) (*Latest, error) {
	if len(cfg.Sources) == 0 {
		return nil, fmt.Errorf("没有配置更新源（config.toml 的 [update].sources，如 [\"updatehub\", \"github\"]）")
	}
	var attempts []string
	for _, name := range cfg.Sources {
		src, err := sourceByName(cfg, name, httpClient)
		if err != nil {
			return nil, err
		}
		latest, err := src.Latest(ctx)
		if err == nil {
			return latest, nil
		}
		attempts = append(attempts, fmt.Sprintf("%s：%v", name, err))
	}
	return nil, fmt.Errorf("所有更新源都连不上（%s）", strings.Join(attempts, "；"))
}

// sourceByName 按配置里的源名构造 Source。
func sourceByName(cfg config.Update, name string, httpClient *http.Client) (Source, error) {
	switch name {
	case "updatehub":
		if cfg.UpdateHub.BaseURL == "" {
			return nil, fmt.Errorf("updatehub 源需要 [update.updatehub].base_url")
		}
		return &UpdateHubSource{BaseURL: cfg.UpdateHub.BaseURL, Channel: cfg.UpdateHub.Channel, HTTP: httpClient}, nil
	case "github":
		if cfg.GitHub.Repo == "" {
			return nil, fmt.Errorf("github 源需要 [update.github].repo")
		}
		return &GitHubSource{Repo: cfg.GitHub.Repo, Mirror: cfg.GitHub.Mirror, Proxy: cfg.GitHub.Proxy, HTTP: httpClient}, nil
	default:
		return nil, fmt.Errorf("未知更新源 %q（可用：updatehub、github）", name)
	}
}

/* ---------------- GitHub 源 ---------------- */

// GitHubSource 用 GitHub Release 的固定下载地址（不调用 api.github.com）：
// latest.json 在 channel-stable Release 下，manifest 和产物在各 tag 下，
// 都可以直接加镜像前缀。镜像按顺序尝试，最后直连；直连和镜像都走
// proxy（配置了 [update.github].proxy 时）或 HTTPS_PROXY 环境变量。
type GitHubSource struct {
	Repo   string
	Mirror []string
	Proxy  string
	HTTP   *http.Client
}

func (s *GitHubSource) Name() string { return "github" }

func (s *GitHubSource) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: proxyTransport(s.Proxy)}
}

// Latest 先取 channel-stable 的 latest.json，再取两个组件的 manifest.json。
func (s *GitHubSource) Latest(ctx context.Context) (*Latest, error) {
	client := s.client()
	const latestPath = "releases/download/channel-stable/latest.json"
	// latest.json 本身按镜像顺序取；哪个前缀可用，后续请求就用哪个。
	raw, prefix, err := fetchWithFallback(ctx, client, s.Mirror, s.fixedURL(latestPath))
	if err != nil {
		return nil, err
	}
	var latest release.Latest
	if err := json.Unmarshal(raw, &latest); err != nil {
		return nil, fmt.Errorf("解析 latest.json：%w", err)
	}
	out := &Latest{Source: s.Name()}
	component := func(entry *release.LatestEntry) (*Component, error) {
		if entry == nil || entry.ManifestURL == "" {
			return nil, nil
		}
		path := fixedPath(entry.ManifestURL)
		if path == "" {
			return nil, fmt.Errorf("latest.json 里的 manifest 地址不是固定下载地址：%s", entry.ManifestURL)
		}
		manifestRaw, _, err := fetchWithFallback(ctx, client, []string{prefix}, s.fixedURL(path))
		if err != nil {
			return nil, fmt.Errorf("下载 manifest.json：%w", err)
		}
		var m release.Manifest
		if err := json.Unmarshal(manifestRaw, &m); err != nil {
			return nil, fmt.Errorf("解析 manifest.json：%w", err)
		}
		return &Component{
			Manifest: &m,
			File: func(ctx context.Context, name string) ([]byte, error) {
				data, _, err := fetchWithFallback(ctx, client, []string{prefix}, s.fixedURL("releases/download/"+entry.Tag+"/"+name))
				if err != nil {
					return nil, fmt.Errorf("下载产物 %s：%w", name, err)
				}
				if err := verifySHA256(name, data, m.FileEntry(name)); err != nil {
					return nil, err
				}
				return data, nil
			},
		}, nil
	}
	if out.Server, err = component(latest.Server); err != nil {
		return nil, err
	}
	if out.Web, err = component(latest.Web); err != nil {
		return nil, err
	}
	if out.Server == nil && out.Web == nil {
		return nil, fmt.Errorf("latest.json 里还没有任何组件的版本")
	}
	return out, nil
}

// fixedURL 返回镜像前缀 prefix 拼上 GitHub 固定地址后的完整 URL。
func (s *GitHubSource) fixedURL(path string) string {
	return "https://github.com/" + s.Repo + "/" + path
}

// fetchWithFallback 依次用 prefixes 里的前缀（以及空前缀=直连）请求 url，
// 返回第一个成功的内容和它的前缀。prefixes 为空时只直连一次。
func fetchWithFallback(ctx context.Context, client *http.Client, prefixes []string, url string) ([]byte, string, error) {
	try := []string{}
	for _, p := range prefixes {
		if p != "" {
			try = append(try, p)
		}
	}
	if !containsString(try, "") {
		try = append(try, "") // 最后直连
	}
	var lastErr error
	for _, prefix := range try {
		full := prefix + url
		data, err := httpGet(ctx, client, full)
		if err == nil {
			return data, prefix, nil
		}
		lastErr = fmt.Errorf("%s：%w", describePrefix(prefix, full), err)
	}
	return nil, "", fmt.Errorf("镜像与直连都失败：%w", lastErr)
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func describePrefix(prefix, full string) string {
	if prefix == "" {
		return "直连"
	}
	return "镜像 " + strings.TrimRight(prefix, "/")
}

// fixedPath 校验 url 是 github.com/<repo>/releases/download/... 形式的固定
// 地址并返回 releases/ 之后的部分；不是则返回空串（客户端只用固定地址）。
func fixedPath(manifestURL string) string {
	i := strings.Index(manifestURL, "releases/download/")
	if i < 0 || !strings.HasPrefix(manifestURL, "https://github.com/") {
		return ""
	}
	return manifestURL[i:]
}

// isHTTPStatus 报告 err 是否是 httpGet 对某状态码的失败。
func isHTTPStatus(err error, status int) bool {
	var se *statusError
	if errors.As(err, &se) {
		return se.status == status
	}
	return false
}

type statusError struct {
	status int
}

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d", e.status) }

func httpGet(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		return nil, fmt.Errorf("GET %s 返回 %d：%s（%w）", redactURL(url), res.StatusCode, strings.TrimSpace(string(body)), &statusError{status: res.StatusCode})
	}
	// 产物可能上百 MB；上限放得比 UpdateHub 单次上传大一些。
	return io.ReadAll(io.LimitReader(res.Body, 400<<20))
}

// redactURL 把镜像前缀拼出的长 URL 缩短一点，便于报错阅读。
func redactURL(url string) string {
	if i := strings.Index(url, "https://github.com/"); i > 0 {
		return "…" + url[i:]
	}
	if i := strings.Index(url, "http://github.com/"); i > 0 {
		return "…" + url[i:]
	}
	return url
}

// proxyTransport 返回按 proxy 设置的 Transport；proxy 为空时用环境变量
// （HTTPS_PROXY 等，即 http.ProxyFromEnvironment）。
func proxyTransport(proxy string) *http.Transport {
	t := &http.Transport{}
	if proxy != "" {
		t.Proxy = http.ProxyURL(mustParseURL(proxy))
	} else {
		t.Proxy = http.ProxyFromEnvironment
	}
	return t
}

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	return u
}

// verifySHA256 按清单条目校验产物内容。
func verifySHA256(name string, data []byte, entry *release.FileEntry) error {
	if entry == nil {
		return fmt.Errorf("manifest 里没有产物 %s", name)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != entry.Sha256 {
		return fmt.Errorf("产物 %s 的 SHA256 不符：manifest 里是 %s，下载到的是 %s，拒绝更新", name, entry.Sha256, got)
	}
	if entry.Size > 0 && int64(len(data)) != entry.Size {
		return fmt.Errorf("产物 %s 的大小不符：manifest 里是 %d 字节，下载到 %d 字节", name, entry.Size, len(data))
	}
	return nil
}

/* ---------------- UpdateHub 源 ---------------- */

// UpdateHubSource 走内网 UpdateHub（Velopack 协议）：feed 是
// GET {base}/{appId}/{channel}/releases.win.json（不需要认证），按 FileName
// 下载 nupkg；先校验 feed 里的 SHA1，解包后再按 manifest 里的 SHA256 校验
// payload/ 下的产物。
type UpdateHubSource struct {
	BaseURL string
	Channel string
	HTTP    *http.Client
}

func (s *UpdateHubSource) Name() string { return "updatehub" }

func (s *UpdateHubSource) latestFeed(ctx context.Context, component string) (*Component, error) {
	appID, err := updatehub.AppIDForComponent(component)
	if err != nil {
		return nil, err
	}
	channel := s.Channel
	if channel == "" {
		channel = "stable"
	}
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	feedURL := fmt.Sprintf("%s/%s/%s/releases.win.json", strings.TrimRight(s.BaseURL, "/"), appID, channel)
	feedRaw, err := httpGet(ctx, client, feedURL)
	if err != nil {
		// feed 还不存在（该组件从未发布过）不算源不可用，只说明没有这个组件。
		if isHTTPStatus(err, http.StatusNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var feed release.ReleaseFeed
	if err := json.Unmarshal(feedRaw, &feed); err != nil {
		return nil, fmt.Errorf("解析 feed %s：%w", feedURL, err)
	}
	var asset *release.ReleaseFeedAsset
	for i := range feed.Assets {
		a := &feed.Assets[i]
		if strings.EqualFold(a.PackageId, appID) && (a.Type == "" || a.Type == "Full") {
			asset = a
			break
		}
	}
	if asset == nil || asset.FileName == "" {
		return nil, nil // 这个组件在源上还没有版本
	}

	// nupkg 只下载一次（manifest 和产物都在里面），下载与校验懒执行。
	var once sync.Once
	var nupkgFiles map[string][]byte
	var nupkgManifest *release.Manifest
	var loadErr error
	load := func() error {
		once.Do(func() {
			nupkgURL := fmt.Sprintf("%s/%s/%s/%s", strings.TrimRight(s.BaseURL, "/"), appID, channel, asset.FileName)
			data, err := httpGet(ctx, client, nupkgURL)
			if err != nil {
				loadErr = fmt.Errorf("下载 nupkg %s：%w", asset.FileName, err)
				return
			}
			if asset.SHA1 != "" { // feed 里的 SHA1 校验 nupkg 整包
				sum := sha1.Sum(data) //nolint:gosec
				if got := hex.EncodeToString(sum[:]); got != strings.ToLower(asset.SHA1) {
					loadErr = fmt.Errorf("nupkg %s 的 SHA1 不符：feed 里是 %s，下载到的是 %s，拒绝更新", asset.FileName, asset.SHA1, got)
					return
				}
			}
			files, manifest, err := unpackNupkg(data)
			if err != nil {
				loadErr = err
				return
			}
			nupkgFiles, nupkgManifest = files, manifest
		})
		return loadErr
	}
	if err := load(); err != nil {
		return nil, err
	}
	return &Component{
		Manifest: nupkgManifest,
		File: func(ctx context.Context, name string) ([]byte, error) {
			if err := load(); err != nil {
				return nil, err
			}
			data, ok := nupkgFiles["payload/"+name]
			if !ok {
				return nil, fmt.Errorf("nupkg 里没有产物 %s", name)
			}
			if err := verifySHA256(name, data, nupkgManifest.FileEntry(name)); err != nil {
				return nil, err
			}
			return data, nil
		},
	}, nil
}

// Latest 读取两个组件的 feed；两个都还没有版本时是错误（源不可用）。
func (s *UpdateHubSource) Latest(ctx context.Context) (*Latest, error) {
	server, err := s.latestFeed(ctx, release.ComponentServer)
	if err != nil {
		return nil, err
	}
	web, err := s.latestFeed(ctx, release.ComponentWeb)
	if err != nil {
		return nil, err
	}
	if server == nil && web == nil {
		return nil, fmt.Errorf("UpdateHub 源上还没有任何组件的版本")
	}
	return &Latest{Source: s.Name(), Server: server, Web: web}, nil
}

// unpackNupkg 解开 nupkg（就是 zip）：读出 manifest.json 和 payload/ 下的
// 产物文件。nupkg 由本仓库的 publish-updatehub 打包，布局见 internal/release。
func unpackNupkg(data []byte) (map[string][]byte, *release.Manifest, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, fmt.Errorf("解包 nupkg：%w", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, err
		}
		content, err := io.ReadAll(io.LimitReader(rc, 400<<20))
		rc.Close()
		if err != nil {
			return nil, nil, err
		}
		files[f.Name] = content
	}
	manifestRaw, ok := files["manifest.json"]
	if !ok {
		return nil, nil, fmt.Errorf("nupkg 里没有 manifest.json")
	}
	var manifest release.Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return nil, nil, fmt.Errorf("解析 nupkg 里的 manifest.json：%w", err)
	}
	return files, &manifest, nil
}
