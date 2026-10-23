package httpapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
)

func TestClientIP只在回环时信任XFF(t *testing.T) {
	loopback, _ := http.NewRequest(http.MethodPost, "/", nil)
	loopback.RemoteAddr = "127.0.0.1:12345"
	loopback.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2")
	if got := clientIP(loopback); got != "10.0.0.2" {
		t.Fatalf("回环 XFF 最右段 = %q, 想 10.0.0.2", got)
	}
	direct, _ := http.NewRequest(http.MethodPost, "/", nil)
	direct.RemoteAddr = "192.0.2.10:12345"
	direct.Header.Set("X-Forwarded-For", "10.0.0.3")
	if got := clientIP(direct); got != "192.0.2.10" {
		t.Fatalf("非回环应忽略 XFF：%q", got)
	}
}

func Test入口上传限流按客户端IP重填令牌(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 20, 9, 0, 0, 0, clock.Beijing))
	limiter := newRequestRateLimiter(1, clk)
	loopback, _ := http.NewRequest(http.MethodPost, "/", nil)
	loopback.RemoteAddr = "127.0.0.1:1"
	if wait := limiter.allow(loopback.RemoteAddr); wait != 0 {
		t.Fatalf("首个令牌不应等待：%v", wait)
	}
	if wait := limiter.allow(loopback.RemoteAddr); wait <= 0 {
		t.Fatalf("第二个请求应等待：%v", wait)
	}
	clk.Advance(30 * time.Second)
	direct, _ := http.NewRequest(http.MethodPost, "/", nil)
	direct.RemoteAddr = "192.0.2.10:1"
	if wait := limiter.allow(direct.RemoteAddr); wait != 0 {
		t.Fatalf("另一个 IP 不受回环桶影响：%v", wait)
	}
}
