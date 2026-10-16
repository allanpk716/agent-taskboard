// Package updatehub 实现发布产物转发到内网 UpdateHub 的客户端与编排。
//
// UpdateHub 实现 Velopack 协议，上传接口是
// POST /api/apps/{appId}/channels/{channel}/releases（Bearer token，multipart，
// 一次带 .nupkg 和 releases.win.json，单次不超过 200MB）；feed 查询是
// GET /{appId}/{channel}/releases.{os}.json，不需要认证。
// 本仓库是公开的，UpdateHub 的地址和 token 只从环境变量
// UPDATEHUB_BASE_URL、UPDATEHUB_TOKEN 读取。
package updatehub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/release"
)

// maxUploadBytes 是 UpdateHub 单次上传的上限（handler/upload.go 里
// ParseMultipartForm(200 << 20)）。
const maxUploadBytes = 200 << 20

// Client 是 UpdateHub 的 HTTP 客户端。
type Client struct {
	// BaseURL 形如 https://updatehub.example.com，不带结尾斜杠。
	BaseURL string
	// Token 是 Bearer token（开发机环境变量 UPDATEHUB_TOKEN）。
	Token string
	HTTP  *http.Client
}

// New 创建指向 baseURL 的客户端。
func New(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: http.DefaultClient}
}

// UploadFile 是 multipart 里待上传的一个文件。
type UploadFile struct {
	Name    string
	Content []byte
}

// UploadResult 对应 UpdateHub 上传接口的响应。
type UploadResult struct {
	Channel       string   `json:"channel"`
	FilesReceived int      `json:"files_received"`
	VersionsAdded []string `json:"versions_added"`
}

// Upload 把 nupkg 和 feed 登记到 appID 应用的 channel 通道。
// files 里的名字会同时用作 multipart 的文件名（UpdateHub 按文件名识别
// feed 和 nupkg）。notes 会作为表单值一起发送，展示在 UpdateHub 的版本列表里。
func (c *Client) Upload(ctx context.Context, appID, channel string, files []UploadFile, notes string) (*UploadResult, error) {
	if c.Token == "" {
		return nil, fmt.Errorf("缺少 UpdateHub token（环境变量 UPDATEHUB_TOKEN）")
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("没有可上传的文件")
	}
	var total int64
	for _, f := range files {
		total += int64(len(f.Content))
	}
	if total > maxUploadBytes {
		return nil, fmt.Errorf("上传内容共 %d 字节，超过 UpdateHub 单次 %d 字节的上限", total, maxUploadBytes)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, f := range files {
		part, err := mw.CreateFormFile(f.Name, f.Name)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(f.Content); err != nil {
			return nil, err
		}
	}
	if notes != "" {
		if err := mw.WriteField("notes", notes); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/api/apps/%s/channels/%s/releases", c.BaseURL, appID, channel)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("上传到 %s：%w", url, err)
	}
	defer res.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("UpdateHub 返回 %d：%s", res.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var out UploadResult
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("解析 UpdateHub 响应：%w（%s）", err, respBody)
	}
	return &out, nil
}

// FetchFeed 读取 appID/channel 公开的 Velopack feed（releases.{os}.json，
// 如 os=win），用于上传后确认版本已登记。不需要认证。
func (c *Client) FetchFeed(ctx context.Context, appID, channel, goos string) (*release.ReleaseFeed, error) {
	url := fmt.Sprintf("%s/%s/%s/releases.%s.json", c.BaseURL, appID, channel, goos)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("读取 feed %s：%w", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		return nil, fmt.Errorf("读取 feed %s 返回 %d：%s", url, res.StatusCode, strings.TrimSpace(string(body)))
	}
	var feed release.ReleaseFeed
	if err := json.NewDecoder(res.Body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("解析 feed %s：%w", url, err)
	}
	return &feed, nil
}
