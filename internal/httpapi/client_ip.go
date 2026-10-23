package httpapi

import (
	"net"
	"net/http"
	"strings"
)

// clientIP 只信任回环反代带来的 X-Forwarded-For，并取最右侧一段（Caddy 追加
// 的客户端地址）；直连时忽略该头。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			last := strings.TrimSpace(parts[len(parts)-1])
			if parsed := net.ParseIP(last); parsed != nil {
				return parsed.String()
			}
		}
	}
	if ip != nil {
		return ip.String()
	}
	return host
}
