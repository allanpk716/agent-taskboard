// Package webapp 同源托管磁盘上的前端目录（构建产物）：
// index.html 不缓存，带哈希的资源文件长期缓存，未知路径回退到 index.html（SPA 路由）。
package webapp

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// Handler 服务 root 目录下的静态文件。
type Handler struct {
	root *os.Root
}

// New 打开 root 目录作为站点根。root 不存在时返回错误（提示先构建前端）。
func New(root string) (*Handler, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("打开前端目录 %s 失败：%w（先执行 cd web-src && npm run build）", root, err)
	}
	return &Handler{root: r}, nil
}

// Close 释放前端目录句柄（Windows 上不关句柄会删不掉目录）。
func (h *Handler) Close() error { return h.root.Close() }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name, file, err := h.resolve(r.URL.Path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	seeker, ok := file.(io.ReadSeeker)
	if !ok {
		http.Error(w, "cannot serve", http.StatusInternalServerError)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", cacheControl(name))
	http.ServeContent(w, r, path.Base(name), st.ModTime(), seeker)
}

// resolve 把 URL 路径映射到文件；文件不存在时回退到 index.html（前端路由如 /t/123）。
func (h *Handler) resolve(urlPath string) (string, fs.File, error) {
	name := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if name == "" || strings.HasSuffix(name, "/") {
		name += "index.html"
	}
	f, err := h.root.Open(name)
	if err == nil {
		if st, statErr := f.Stat(); statErr == nil && !st.IsDir() {
			return name, f, nil
		}
		f.Close()
	}
	// SPA 回退。
	fallback, err := h.root.Open("index.html")
	if err != nil {
		return "", nil, err
	}
	if st, statErr := fallback.Stat(); statErr != nil || st.IsDir() {
		fallback.Close()
		return "", nil, fmt.Errorf("index.html 不可用")
	}
	return "index.html", fallback, nil
}

// cacheControl 决定缓存策略：
//
//	index.html 及其他根文件：no-cache（每次向服务器确认）
//	/assets/ 下带内容哈希的文件：public, max-age=31536000, immutable
func cacheControl(name string) string {
	if strings.HasPrefix(name, "assets/") {
		return "public, max-age=31536000, immutable"
	}
	return "no-cache"
}
