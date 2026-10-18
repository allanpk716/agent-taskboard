package server_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func TestSPA首页给Agent指路(t *testing.T) {
	webDir := t.TempDir()
	source, err := os.ReadFile(filepath.Join("..", "..", "web-src", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	s := tbtest.Start(t, tbtest.WithPublicURL("https://tb.example.com"), tbtest.WithWebDir(webDir))

	res := s.Do(t, s.NewClient(), http.MethodGet, "/", nil, nil)
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, "Agent 请读 /agent.md") {
		t.Fatalf("首页没有指路行：%q", got)
	}
	if !strings.Contains(got, `<link rel="alternate" type="text/markdown" href="/agent.md">`) {
		t.Fatalf("首页没有 alternate 链接：%q", got)
	}
}

func Test工单和访客路径按Accept返回Markdown或SPA(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithPublicURL("https://tb.example.com"))

	t.Run("工单路径返回Markdown指引", func(t *testing.T) {
		res := s.DoWithHeaders(t, s.NewClient(), http.MethodGet, "/t/20261016-001",
			map[string]string{"Accept": "text/markdown"}, nil, nil)
		assertMarkdownGuide(t, res, []string{
			"20261016-001",
			"https://tb.example.com/api/v1/tickets/20261016-001?format=markdown",
			"Authorization: Bearer $TOKEN",
			"我的 → Agent",
			"https://tb.example.com/agent.md",
		})
	})

	t.Run("访客路径返回Markdown指引", func(t *testing.T) {
		res := s.DoWithHeaders(t, s.NewClient(), http.MethodGet, "/g/tbg_fake",
			map[string]string{"Accept": "text/markdown"}, nil, nil)
		assertMarkdownGuide(t, res, []string{
			"链接本身就是访客凭证",
			"https://tb.example.com/api/v1/session",
			"$TICKET_API?format=markdown",
			"$TICKET_API/replies",
			"Authorization: Bearer $GUEST_TOKEN",
			"https://tb.example.com/agent.md",
		})
	})

	t.Run("浏览器默认Accept仍返回SPA", func(t *testing.T) {
		for _, path := range []string{"/t/20261016-001", "/g/tbg_fake"} {
			res := s.DoWithHeaders(t, s.NewClient(), http.MethodGet, path, map[string]string{
				"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			}, nil, nil)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("%s 状态码 = %d，想 200", path, res.StatusCode)
			}
			if got := res.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
				t.Fatalf("%s Content-Type = %q，想 SPA HTML", path, got)
			}
		}
	})

	t.Run("Markdown优先级不低于HTML时返回Markdown", func(t *testing.T) {
		res := s.DoWithHeaders(t, s.NewClient(), http.MethodGet, "/t/20261016-001", map[string]string{
			"Accept": "text/html;q=0.5,text/markdown",
		}, nil, nil)
		if got := res.Header.Get("Content-Type"); got != "text/markdown; charset=utf-8" {
			t.Fatalf("Content-Type = %q，想 Markdown", got)
		}
	})

	t.Run("HTML优先级更高时返回SPA", func(t *testing.T) {
		res := s.DoWithHeaders(t, s.NewClient(), http.MethodGet, "/g/tbg_fake", map[string]string{
			"Accept": "text/html;q=0.9,text/markdown;q=0.8",
		}, nil, nil)
		if got := res.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Fatalf("Content-Type = %q，想 SPA HTML", got)
		}
	})

	t.Run("Markdown质量为0时返回SPA", func(t *testing.T) {
		res := s.DoWithHeaders(t, s.NewClient(), http.MethodGet, "/t/20261016-001", map[string]string{
			"Accept": "text/html;q=0.5,text/markdown;q=0",
		}, nil, nil)
		if got := res.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Fatalf("Content-Type = %q，想 SPA HTML", got)
		}
	})

	t.Run("非GET仍交给静态模块处理", func(t *testing.T) {
		res := s.DoWithHeaders(t, s.NewClient(), http.MethodPost, "/t/20261016-001",
			map[string]string{"Accept": "text/markdown"}, nil, nil)
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("状态码 = %d，想 405", res.StatusCode)
		}
	})
}

func TestAgent路径地址没配置public_url时用请求地址(t *testing.T) {
	s := tbtest.Start(t)

	for _, path := range []string{"/t/20261016-001", "/g/tbg_fake"} {
		res := s.DoWithHeaders(t, s.NewClient(), http.MethodGet, path, map[string]string{
			"Accept":            "text/markdown",
			"X-Forwarded-Proto": "https",
		}, nil, nil)
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(s.URL(), "http://", "https://", 1) + "/agent.md"
		if !strings.Contains(string(body), want) {
			t.Fatalf("%s 指引没有请求基址 %q：%s", path, want, body)
		}
	}
}

func assertMarkdownGuide(t *testing.T, res *http.Response, wants []string) {
	t.Helper()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，想 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "text/markdown; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range wants {
		if !strings.Contains(string(body), want) {
			t.Fatalf("指引缺少 %q：%s", want, body)
		}
	}
}
