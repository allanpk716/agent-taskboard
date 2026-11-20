package server_test

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/server"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

func Test服务装配按webhook开关构造门铃推送器(t *testing.T) {
	t.Setenv("TASKBOARD_OUTLET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	disabled := newOutletStartupServer(t, config.Webhook{})
	if disabled.Outlet() != nil {
		t.Fatal("webhook.enabled=false 时不应构造门铃推送器")
	}

	enabled := newOutletStartupServer(t, config.Webhook{
		Enabled: true, AllowedBases: []string{"https://bot.example.test"},
	})
	if enabled.Outlet() == nil {
		t.Fatal("webhook.enabled=true 且主密钥有效时应构造门铃推送器")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := enabled.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func Test开启门铃但缺少主密钥时拒绝启动(t *testing.T) {
	t.Setenv("TASKBOARD_OUTLET_KEY", "")
	_, err := tryNewOutletStartupServer(t, config.Webhook{
		Enabled: true, AllowedBases: []string{"https://bot.example.test"},
	})
	if err == nil || !strings.Contains(err.Error(), "TASKBOARD_OUTLET_KEY") {
		t.Fatalf("err = %v，想提示主密钥缺失", err)
	}
}

func newOutletStartupServer(t *testing.T, webhook config.Webhook) *server.Server {
	t.Helper()
	srv, err := tryNewOutletStartupServer(t, webhook)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func tryNewOutletStartupServer(t *testing.T, webhook config.Webhook) (*server.Server, error) {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "data")
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!doctype html><title>tb</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DataDir: dataDir, WebDir: webDir}
	config.ApplyDefaults(&cfg)
	cfg.Webhook.Enabled = webhook.Enabled
	cfg.Webhook.AllowedBases = webhook.AllowedBases
	return server.New(cfg, st, clock.Real{})
}
