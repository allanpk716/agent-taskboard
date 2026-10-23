package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestHealthcheck按配置端口请求服务(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	port := serverPort(t, server)
	path := writeHealthcheckConfig(t, port)

	if err := cmdHealthcheck([]string{"-config", path}); err != nil {
		t.Fatalf("healthcheck 应成功：%v", err)
	}
}

func TestHealthcheck非200时失败(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	port := serverPort(t, server)
	path := writeHealthcheckConfig(t, port)

	if err := cmdHealthcheck([]string{"-config", path}); err == nil {
		t.Fatal("HTTP 503 时 healthcheck 应失败")
	}
}

func serverPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func writeHealthcheckConfig(t *testing.T, port int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	body := fmt.Sprintf("port = %d\nweb_dir = 'web'\n", port)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
