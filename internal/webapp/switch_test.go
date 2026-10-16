// 前端原子切换（#45）：web/ 是指向 web-<version>/ 的目录链接；切换就是
// 重建链接，服务不重启。测试直接打服务层：切换期间（包括正在翻页的页面）
// 请求只会看到某一个版本的完整文件，不会半新半旧。
package webapp_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/fslink"
	"github.com/allanpk716/agent-taskboard/internal/webapp"
)

// makeVersionDir 造一个版本目录：index.html 标版本，assets/app-<ver>.js 是
// 每版独有的哈希资源名。返回错误而不是直接 Fatal（切换协程里也要用）。
func makeVersionDir(parent, ver string) (string, error) {
	dir := filepath.Join(parent, "web-"+ver)
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		return "", err
	}
	files := map[string]string{
		"index.html":               fmt.Sprintf("<!doctype html><title>v%s</title>", ver),
		"assets/app-" + ver + ".js": fmt.Sprintf("console.log('%s')", ver),
		"version.json":             fmt.Sprintf(`{"component":"web","version":"%s"}`, ver),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// startStatic 启动真实目录的静态服务：New 会把普通目录转成目录链接
// （web → web-<v>），跟 serve 启动时的行为一致。
func startStatic(t *testing.T, versions ...string) (*httptest.Server, string) {
	t.Helper()
	parent := t.TempDir()
	first, err := makeVersionDir(parent, versions[0])
	if err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(parent, "web")
	if err := os.Rename(first, web); err != nil {
		t.Fatal(err)
	}
	for _, v := range versions[1:] {
		if _, err := makeVersionDir(parent, v); err != nil {
			t.Fatal(err)
		}
	}
	h, err := webapp.New(web)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	return server, web
}

func getBody(t *testing.T, url string) (int, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		return 0, err.Error()
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// 服务启动后，把 web/ 切到新版本目录（模拟更新器重建链接）：不重启服务，
// 下一个请求就看到新版本；旧版本的哈希资源仍可取（正在翻页的旧页面不破）。
func Test切换链接后不重启即生效且旧资源仍可取(t *testing.T) {
	server, web := startStatic(t, "1.0.0")

	if code, body := getBody(t, server.URL+"/"); code != 200 || !strings.Contains(body, "v1.0.0") {
		t.Fatalf("切换前应看到 1.0.0：%d %q", code, body)
	}
	// 切到 2.0.0：重建 web 链接。
	parent := filepath.Dir(web)
	v2, err := makeVersionDir(parent, "2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(web); err != nil {
		t.Fatal(err)
	}
	if err := fslink.Make(web, v2); err != nil {
		t.Fatal(err)
	}

	if code, body := getBody(t, server.URL+"/"); code != 200 || !strings.Contains(body, "v2.0.0") {
		t.Fatalf("切换后不重启就应看到 2.0.0：%d %q", code, body)
	}
	// 旧版本的哈希资源仍能取到（翻到一半的旧页面不会断）。
	if code, body := getBody(t, server.URL+"/assets/app-1.0.0.js"); code != 200 || body != "console.log('1.0.0')" {
		t.Fatalf("旧资源应仍可取：%d %q", code, body)
	}
	if code, body := getBody(t, server.URL+"/assets/app-2.0.0.js"); code != 200 || body != "console.log('2.0.0')" {
		t.Fatalf("新资源应可取：%d %q", code, body)
	}
}

// 并发切换期间，逐个请求只会看到某一个版本的完整内容；连续的「取页面 + 取
// 该页引用的资源」要么全旧要么全新，不会出现半新半旧。
func Test切换期间并发请求不会半新半旧(t *testing.T) {
	versions := []string{"1.0.0", "2.0.0", "3.0.0"}
	server, web := startStatic(t, versions...)
	parent := filepath.Dir(web)
	index := map[string]string{}
	for _, v := range versions {
		index[v] = fmt.Sprintf("<!doctype html><title>v%s</title>", v)
	}

	stop := make(chan struct{})
	switcherDone := make(chan []string)
	var readers sync.WaitGroup
	var mu sync.Mutex
	var problems []string

	// 后台不停地在几个版本之间切换链接。
	go func() {
		var errs []string
		defer func() { switcherDone <- errs }()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			v := versions[i%len(versions)]
			tmpLink := web + ".switching"
			_ = os.Remove(tmpLink)
			if err := fslink.Make(tmpLink, parent+filepath.FromSlash("/web-"+v)); err != nil {
				errs = append(errs, "建链接失败："+err.Error())
				return
			}
			// 换掉 web 链接；rename 失败（Windows 上链接被占用）就 remove+make。
			if err := os.Rename(tmpLink, web); err != nil {
				_ = os.Remove(web)
				if err2 := fslink.Make(web, parent+filepath.FromSlash("/web-"+v)); err2 != nil {
					errs = append(errs, "换链接失败："+err2.Error())
					return
				}
			}
		}
	}()

	// 并发的「页面加载」：GET / 看到哪个版本，随后取那个版本的哈希资源，
	// 必须成功且内容一致。
	for w := 0; w < 4; w++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 120; i++ {
				code, indexHTML := getBody(t, server.URL+"/")
				if code != 200 {
					mu.Lock()
					problems = append(problems, fmt.Sprintf("GET / = %d %q", code, indexHTML))
					mu.Unlock()
					continue
				}
				var ver string
				for _, v := range versions {
					if indexHTML == index[v] {
						ver = v
						break
					}
				}
				if ver == "" {
					mu.Lock()
					problems = append(problems, "index 混合了内容："+indexHTML)
					mu.Unlock()
					continue
				}
				acode, asset := getBody(t, server.URL+"/assets/app-"+ver+".js")
				if acode != 200 || asset != "console.log('"+ver+"')" {
					mu.Lock()
					problems = append(problems, fmt.Sprintf("页面 %s 的资源拿到 %d %q", ver, acode, asset))
					mu.Unlock()
				}
			}
		}()
	}
	readers.Wait()
	close(stop)
	for _, err := range <-switcherDone {
		t.Error(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, p := range problems {
		t.Error(p)
	}
}
