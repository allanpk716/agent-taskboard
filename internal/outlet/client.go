package outlet

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultTimeout  = 10 * time.Second
	maxResponseBody = 1024
)

// Client 是通知出口共用的出站 HTTP 客户端：不跟随重定向、10 秒超时、
// 保持默认 TLS 校验，且最多把响应 body 的前 1KB 交给调用方。
type Client struct {
	HTTP *http.Client
}

// Response 只携带状态码和有界响应 body，调用方不得把 Body 写入日志。
type Response struct {
	StatusCode int
	Body       []byte
}

// NewClient 构造带通知出口纪律的默认 HTTP 客户端。
func NewClient() *Client {
	return NewClientWithTimeout(defaultTimeout)
}

// NewClientWithTimeout 构造可注入更短超时（用于测试）的出口客户端。
func NewClientWithTimeout(timeout time.Duration) *Client {
	if timeout <= 0 || timeout > defaultTimeout {
		timeout = defaultTimeout
	}
	return &Client{HTTP: &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// Post 发送 JSON POST 请求。headers 由调用方提供鉴权和幂等键，错误信息不包含响应 body。
func (c *Client) Post(ctx context.Context, endpoint string, headers http.Header, payload []byte) (Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("构造通知出口请求失败：%w", err)
	}
	if headers != nil {
		for key, values := range headers {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("通知出口请求失败：%w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return Response{StatusCode: resp.StatusCode}, fmt.Errorf("读取通知出口响应失败：%w", err)
	}
	return Response{StatusCode: resp.StatusCode, Body: body}, nil
}

// IsSuccess 判断目标平台是否返回 2xx；3xx/4xx/5xx 都由调用方按失败处理。
func (r Response) IsSuccess() bool {
	return r.StatusCode >= 200 && r.StatusCode < 300
}
