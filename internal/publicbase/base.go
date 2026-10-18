// Package publicbase 按服务配置与请求头统一推导对外绝对基址。
package publicbase

import (
	"net/http"
	"strings"
)

// FromRequest 返回不带尾部斜杠的对外基址；[server] public_url 优先。
func FromRequest(publicURL string, r *http.Request) string {
	if configured := strings.TrimRight(publicURL, "/"); configured != "" {
		return configured
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if s := r.Header.Get("X-Forwarded-Proto"); s == "http" || s == "https" {
		scheme = s
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = forwarded
	}
	return scheme + "://" + host
}
