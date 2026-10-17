package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// panic 恢复（评审 P1-3）：未写过响应头时统一 500；已写过时不再补写（避免
// double-write），两种情况都带堆栈记日志。
func TestPanic恢复写出500并记日志(t *testing.T) {
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(nil)

	handler := recoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom-before-write")
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tickets", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d, 想 500", rec.Code)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应体不是统一错误格式：%s", rec.Body.String())
	}
	if env.Error.Code != CodeInternal {
		t.Errorf("错误码 = %q, 想 %q", env.Error.Code, CodeInternal)
	}

	out := logBuf.String()
	if !strings.Contains(out, "boom-before-write") || !strings.Contains(out, "/api/v1/tickets") {
		t.Errorf("日志应记录 panic 值与出事端点：%s", out)
	}
	if !strings.Contains(out, "middleware_test.go") {
		t.Errorf("日志应带堆栈（看不到测试帧）：%s", out)
	}
}

// 已写过响应头后再 panic：客户端拿到已写出的部分，恢复逻辑不再补写
// （补写会是 superfluous WriteHeader / 双份响应体）。
func TestPanic恢复不补写已开始的响应(t *testing.T) {
	log.SetOutput(&bytes.Buffer{})
	defer log.SetOutput(nil)

	handler := recoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("写了一半"))
		panic("boom-after-write")
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/intakes/bug?format=markdown", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("状态码 = %d, 想 200（已写出，不再改写）", rec.Code)
	}
	if body := rec.Body.String(); body != "写了一半" {
		t.Errorf("响应体应保持已写出的部分：%q", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type 被覆盖：%q", ct)
	}
}

// 客户端断开导致的 context.Canceled 不是服务器故障：只记一行、不带堆栈，
// 也不再往已断开的连接补写 500。
func TestPanic恢复客户端断开不刷堆栈(t *testing.T) {
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(nil)

	handler := recoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(fmt.Errorf("query inbox: %w", r.Context().Err()))
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/inbox", nil).WithContext(ctx))

	if rec.Body.Len() != 0 {
		t.Errorf("客户端已断开，不应补写响应：%s", rec.Body.String())
	}
	out := logBuf.String()
	if !strings.Contains(out, "/api/v1/inbox") {
		t.Errorf("仍应记一行出事端点：%q", out)
	}
	if strings.Contains(out, "middleware_test.go") {
		t.Errorf("客户端断开不应打堆栈：%s", out)
	}
}
