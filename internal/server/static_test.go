package server_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 写一个最小前端目录：index.html + 带哈希的 assets 文件。
func writeWebDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"index.html":              "<!doctype html><html><body>app-shell</body></html>",
		"assets/index-AbC123.js":  "console.log('app')",
		"assets/index-AbC123.css": "body{}",
		"favicon.svg":             "<svg/>",
	}
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// index.html 不缓存，带哈希的资源文件长期缓存，并带 nosniff。
func Test静态文件缓存策略(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithWebDir(writeWebDir(t)))

	cases := []struct {
		name      string
		path      string
		wantCache string
	}{
		{"根路径是 index.html", "/", "no-cache"},
		{"index.html 本身", "/index.html", "no-cache"},
		{"带哈希的 js", "/assets/index-AbC123.js", "public, max-age=31536000, immutable"},
		{"带哈希的 css", "/assets/index-AbC123.css", "public, max-age=31536000, immutable"},
		{"不带哈希的根文件", "/favicon.svg", "no-cache"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := s.Do(t, s.NewClient(), http.MethodGet, tc.path, nil, nil)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("状态码 = %d, 想 200", res.StatusCode)
			}
			if got := res.Header.Get("Cache-Control"); got != tc.wantCache {
				t.Errorf("Cache-Control = %q, 想 %q", got, tc.wantCache)
			}
			if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, 想 nosniff", got)
			}
		})
	}
}

// 未知页面路径回退到 index.html（前端路由如 /t/20261016-001）。
func Test未知路径回退到index(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithWebDir(writeWebDir(t)))

	res := s.Do(t, s.NewClient(), http.MethodGet, "/t/20261016-001", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 想 200", res.StatusCode)
	}
	// 反馈入口的网页路由 /i/<slug> 同样回退（提交页在 #39）。
	res = s.Do(t, s.NewClient(), http.MethodGet, "/i/bug", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/i/bug 状态码 = %d, 想 200（SPA 回退）", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "<!doctype html><html><body>app-shell</body></html>" {
		t.Errorf("回退内容不对：%q", body)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("回退响应 Cache-Control = %q, 想 no-cache", got)
	}
}

// 路径穿越不能逃出前端目录。
func Test静态路径穿越被拒绝(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithWebDir(writeWebDir(t)))

	for _, p := range []string{"/../secret.txt", "/%2e%2e/secret.txt"} {
		res := s.Do(t, s.NewClient(), http.MethodGet, p, nil, nil)
		// 逃不出去：要么 404，要么被当成 SPA 路径回退 index.html，反正拿不到目录外文件。
		if res.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(res.Body)
			if string(body) != "<!doctype html><html><body>app-shell</body></html>" {
				t.Errorf("%s 返回了目录外内容：%q", p, body)
			}
		}
	}
}

// /api/v1 下不存在的接口返回统一错误格式，而不是前端的 index.html。
func Test未知API接口返回统一错误格式(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithWebDir(writeWebDir(t)))

	res := s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/nope", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("状态码 = %d, 想 404", res.StatusCode)
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "not_found" {
		t.Errorf("错误码 = %q, 想 not_found", got.Error.Code)
	}
}
