package outlet_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/outlet"
)

func Test出口HTTP客户端具备十秒无重定向默认纪律(t *testing.T) {
	client := outlet.NewClient()
	if client.HTTP.Timeout != 10*time.Second {
		t.Fatalf("默认超时 = %s，想 10s", client.HTTP.Timeout)
	}
	if err := client.HTTP.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("默认重定向策略 = %v，想不跟随", err)
	}
	if client.HTTP.Transport != nil {
		t.Fatal("出口客户端不应替换默认 Transport 或关闭 TLS 校验")
	}
}

func Test出口HTTP客户端POST请求并读取结果(t *testing.T) {
	var gotPath, gotMethod, gotKey, gotID string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotKey, gotID = r.Header.Get("X-Webhook-Key"), r.Header.Get("X-Idempotency-Key")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := outlet.NewClient()
	result, err := client.Post(context.Background(), server.URL+"/api/v1/bots/bot01/webhook", http.Header{
		"X-Webhook-Key":     []string{"secret-key"},
		"X-Idempotency-Key": []string{"taskboard:1:g1:2"},
	}, []byte(`{"event":"taskboard.test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/bots/bot01/webhook" ||
		gotKey != "secret-key" || gotID != "taskboard:1:g1:2" ||
		string(gotBody) != `{"event":"taskboard.test"}` {
		t.Fatalf("请求方法/地址/鉴权/幂等键/body 不符合预期")
	}
	if result.StatusCode != http.StatusOK || !result.IsSuccess() || string(result.Body) != `{"ok":true}` {
		t.Fatal("响应状态或 body 不符合预期")
	}
}

func Test出口HTTP客户端不跟随重定向且3xx视为失败(t *testing.T) {
	followed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		followed = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	result, err := outlet.NewClient().Post(context.Background(), server.URL+"/redirect", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if followed {
		t.Fatal("客户端不应跟随重定向")
	}
	if result.StatusCode != http.StatusFound || result.IsSuccess() {
		t.Fatalf("3xx 应记失败： %+v", result)
	}
}

func Test出口HTTP客户端只读取响应前一KB(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 2048))
	}))
	defer server.Close()

	result, err := outlet.NewClient().Post(context.Background(), server.URL, nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Body) != 1024 || strings.Repeat("x", 1024) != string(result.Body) {
		t.Fatalf("响应 body 长度 = %d，只想读前 1KB", len(result.Body))
	}
}

func Test出口HTTP客户端超时返回不含body的错误(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte("secret-response-body"))
	}))
	defer server.Close()

	client := outlet.NewClientWithTimeout(5 * time.Millisecond)
	_, err := client.Post(context.Background(), server.URL, nil, []byte(`{}`))
	if err == nil || strings.Contains(err.Error(), "secret-response-body") {
		t.Fatalf("超时错误 = %v，想包含超时信息且不含响应 body", err)
	}
}
