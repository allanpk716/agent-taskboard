// 内部白盒测试：不开 HTTP 服务，直接驱动 acquire/resolve，确定性地复现
// 「请求拿到 v2 后链接切回 v3（v3 从 retired 取出成为 current），该请求再取
// 只在 v3 里有的哈希资源」——补取不能漏掉比请求版本更新的 current。
package webapp

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/fslink"
)

// makeVersion 造一个 web-<ver> 版本目录：index.html 标版本，assets/app-<ver>.js
// 是每版独有的哈希资源名。返回目录路径。
func makeVersion(t *testing.T, parent, ver string) string {
	t.Helper()
	dir := filepath.Join(parent, "web-"+ver)
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"index.html":                "<!doctype html><title>v" + ver + "</title>",
		"assets/app-" + ver + ".js": "console.log('" + ver + "')",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// relink 把 web 链接重建为指向 target（模拟更新器切换版本）。
func relink(t *testing.T, web, target string) {
	t.Helper()
	if err := os.Remove(web); err != nil {
		t.Fatal(err)
	}
	if err := fslink.Make(web, target); err != nil {
		t.Fatal(err)
	}
}

// acquireOnce 用一次 acquire/release 触发链接变化的切换（模拟另一个请求
// 走完整个生命周期，把 current 换到链接指向的版本）。
func acquireOnce(t *testing.T, h *Handler) {
	t.Helper()
	h.release(h.acquire())
}

func Test在途请求能取到切回current版本的哈希资源(t *testing.T) {
	parent := t.TempDir()
	v2 := makeVersion(t, parent, "2.0.0")
	v3 := makeVersion(t, parent, "3.0.0")
	web := filepath.Join(parent, "web")
	if err := fslink.Make(web, v3); err != nil {
		t.Fatal(err)
	}
	h, err := New(web)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	// 切到 v2：v3 进 retired。此后请求 A acquire 到 v2 并一直在途。
	relink(t, web, v2)
	acquireOnce(t, h)
	vrA := h.acquire()

	// 链接切回 v3（v3 从 retired 取出成为 current，v2 进 retired）。
	relink(t, web, v3)
	acquireOnce(t, h)

	// 在途的 A 取只在 v3 里有的哈希资源：应命中 v3 的文件，而不是回退成
	// v2 的 index.html。
	name, f, err := h.resolve(vrA, "/assets/app-3.0.0.js")
	if err != nil {
		t.Fatalf("resolve 出错：%v", err)
	}
	defer f.Close()
	if name != "assets/app-3.0.0.js" {
		t.Fatalf("应命中哈希资源文件，实际 %q", name)
	}
	body, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "console.log('3.0.0')"; got != want {
		t.Fatalf("应取到 v3 的资源内容 %q，实际 %q", want, got)
	}

	h.release(vrA)
}

// 普通目录是文件系统不支持目录链接时的回退形态（#239 的容器 overlayfs）。
// Handler 必须仍以这个目录本身为站点根，而不是以它的父目录为根。
func Test普通目录回退时仍从目录本身服务SPA路由(t *testing.T) {
	parent := t.TempDir()
	web := makeVersion(t, parent, "initial")
	h, err := New(web)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	// Windows 单测文件系统支持链接，New 会成功链接化。这里显式模拟
	// overlayfs rename EXDEV 后的普通目录回退形态。
	h.linkMode = false
	for _, urlPath := range []string{"/", "/login", "/assets/app-initial.js"} {
		vr := h.acquire()
		name, f, err := h.resolve(vr, urlPath)
		if err != nil {
			t.Fatalf("resolve %s 失败：%v", urlPath, err)
		}
		defer f.Close()
		h.release(vr)
		want := "index.html"
		if urlPath == "/assets/app-initial.js" {
			want = strings.TrimPrefix(urlPath, "/")
		}
		if name != want {
			t.Fatalf("resolve %s = %q，想 %q", urlPath, name, want)
		}
	}
}
