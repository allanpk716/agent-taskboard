// Package webapp 同源托管磁盘上的前端目录（构建产物）：
// index.html 不缓存，带哈希的资源文件长期缓存，未知路径回退到 index.html（SPA 路由）。
//
// 前端独立更新（#45，ADR 0002）：web/ 是一个指向 web-<version>/ 的目录链接
// （Windows junction / linux 符号链接，见 internal/fslink）。New 时如果 web/
// 还是普通目录（手动部署的首个前端），会转成链接（原目录挪成 web-initial）。
// 服务持有的是打开的版本目录（链接目标），切换 = 重建链接：
//
//   - 每个请求先看链接指向有没有变，变了就打开新版本目录接着服务（不用重启）；
//   - 一个请求自始至终只用一个版本目录，不会半新半旧；它要的哈希资源若
//     不在拿到的版本里（页面来自切换前后的另一版），到 current 和保留的
//     旧版本里补取，页面与资源跨切换保持一致；
//   - 刚换下来的旧版本目录再保留两代：翻到一半的旧页面还能取到旧哈希资源。
//     切回保留期内的目录时复用已打开的 root（几个版本来回切不会反复开、关），
//     所以循环切换时旧哈希资源一直可取；
//   - 超过保留代数被淘汰的 root 带引用计数：在途请求用完（release）之前不
//     会真正关闭，切换期间不会冒出 404。
package webapp

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/allanpk716/agent-taskboard/internal/fslink"
)

// Handler 服务 web 链接（web/）指向的前端目录。
type Handler struct {
	// linkPath 是 web 目录的路径（可能是目录链接）。
	linkPath string
	// linkMode 为 true 时按链接目标的变化惰性切换版本目录。
	linkMode bool

	mu      sync.Mutex
	current *versionRoot
	retired []*versionRoot // 最近 keepRetired 代旧版本（按目录去重，给旧页面补旧哈希资源）
	dying   []*versionRoot // 已淘汰但还有在途请求在用的 root，引用归零时才真正关闭
	closed  bool
}

// versionRoot 是一个打开的前端版本目录。refs 是正在用它服务的请求数：
// root 被淘汰（从 current/retired 摘除）后要等 refs 归零才 Close，
// 否则正在用它取文件的请求会 404。refs 由 Handler.mu 保护。
type versionRoot struct {
	root *os.Root
	dir  string // 实际目录（链接目标）的绝对路径
	refs int    // 在途请求数
}

// keepRetired 是换下来还保持可服务的旧版本目录数（配合更新器在磁盘上
// 保留 web-<version> 的份数，见 internal/updater）。
const keepRetired = 2

// New 打开 root（web 目录）作为站点根。普通目录会被转成指向自身的目录链接
// （web-initial），之后更新前端就只是重建这个链接。root 不存在时返回错误
// （提示先构建前端）。
func New(root string) (*Handler, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	h := &Handler{linkPath: abs}
	if target, err := os.Readlink(abs); err == nil {
		// 已经是链接（之前更新过）：直接服务它指向的版本目录。
		vr, err := openVersion(target)
		if err != nil {
			return nil, err
		}
		h.current, h.linkMode = vr, true
		return h, nil
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("打开前端目录 %s 失败：%w（先执行 cd web-src && npm run build）", root, err)
	}
	if converted := convertToLink(abs); converted != "" {
		vr, err := openVersion(converted)
		if err != nil {
			return nil, err
		}
		h.current, h.linkMode = vr, true
		return h, nil
	}
	// 转换失败（文件系统不支持等）：按普通目录服务，更新前端时再试。
	vr, err := openVersion(abs)
	if err != nil {
		return nil, err
	}
	h.current = vr
	return h, nil
}

// convertToLink 把普通目录挪成 web-initial 并把原路径变成指向它的链接；
// 返回链接目标（失败返回空串）。挪目录与 updater.SwitchWebDir 共用
// fslink.MovePlainDirAside（评审 P3-16）。
func convertToLink(webDir string) string {
	for _, name := range []string{"web-initial", "web-initial-2", "web-initial-3"} {
		target := filepath.Join(filepath.Dir(webDir), name)
		if _, err := os.Stat(target); err == nil {
			continue // 已有（上次转换留下的？）换下一个名字
		}
		if moved, err := fslink.MovePlainDirAside(webDir, target); err != nil || !moved {
			return ""
		}
		if err := fslink.Make(webDir, target); err != nil {
			// 挪回去，维持普通目录。
			_ = os.Rename(target, webDir)
			return ""
		}
		return target
	}
	return ""
}

func openVersion(dir string) (*versionRoot, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("打开前端目录 %s 失败：%w（先执行 cd web-src && npm run build）", abs, err)
	}
	return &versionRoot{root: root, dir: abs}, nil
}

// Close 释放所有打开的版本目录句柄（Windows 上不关句柄会删不掉目录）。
func (h *Handler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	all := append([]*versionRoot{h.current}, h.retired...)
	all = append(all, h.dying...)
	var firstErr error
	for _, vr := range all {
		if vr == nil {
			continue
		}
		if err := vr.root.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	h.retired, h.dying = nil, nil
	return firstErr
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	vr := h.acquire()
	defer h.release(vr)
	name, file, err := h.resolve(vr, r.URL.Path)
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

// acquire 取本次请求要用的版本目录并计一次引用（ServeHTTP 结束时 release）。
// 链接模式下先看链接指向是否变了（更新器重建链接后，下一个请求就切到新
// 版本，不用重启服务）。引用计数和「root 是否还在被服务持有」在同一个锁
// 里更新，淘汰路径（retireLocked）不可能关掉刚被本请求拿到的 root。
func (h *Handler) acquire() *versionRoot {
	var target string
	var linkErr error
	if h.linkMode {
		target, linkErr = os.Readlink(h.linkPath)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	vr := h.current
	if h.linkMode && linkErr == nil && !h.closed && target != vr.dir {
		vr = h.switchVersion(target)
	}
	vr.refs++
	return vr
}

// release 归还一次引用：root 已被淘汰（进了 dying）且引用归零时才真正关闭。
func (h *Handler) release(vr *versionRoot) {
	h.mu.Lock()
	defer h.mu.Unlock()
	vr.refs--
	if vr.refs > 0 {
		return
	}
	for i, d := range h.dying {
		if d == vr {
			h.dying = append(h.dying[:i], h.dying[i+1:]...)
			vr.root.Close()
			return
		}
	}
}

// switchVersion 在持锁状态下把 current 换成 target 目录（调用方保证 target
// 与 current 不是同一目录、Handler 未关闭）：
//   - target 已在 retired 里：复用那个 root，从 retired 取出来当 current。
//     否则 A→B→C→A 循环切换时同一个目录会在 retired 里堆多份，把真正需要
//     保留的旧版本挤出去，旧页面的哈希资源就取不到了；
//   - 否则新开一个 root（打不开就继续用旧版本服务）。
//
// 换下来的旧 current 进 retired；retired 超过 keepRetired 时淘汰最旧的：
// 没有在途请求就立即关闭，否则标记进 dying，等引用归零（见 release）。
func (h *Handler) switchVersion(target string) *versionRoot {
	vr := h.current
	if i := h.retiredIndex(target); i >= 0 {
		vr = h.retired[i]
		h.retired = append(h.retired[:i], h.retired[i+1:]...)
	} else if fresh, err := openVersion(target); err == nil {
		vr = fresh
	} else {
		return h.current // 链接指到了打不开的目录：继续用旧版本服务
	}
	h.retireLocked(h.current)
	h.current = vr
	return vr
}

// retireLocked 把换下来的 root 放进 retired，超过 keepRetired 代就淘汰最旧的。
func (h *Handler) retireLocked(vr *versionRoot) {
	h.retired = append(h.retired, vr)
	if len(h.retired) <= keepRetired {
		return
	}
	old := h.retired[0]
	h.retired = h.retired[1:]
	if old.refs == 0 {
		old.root.Close()
		return
	}
	// 还有请求在用：先摘出来，等最后一个请求结束再关。
	h.dying = append(h.dying, old)
}

func (h *Handler) retiredIndex(dir string) int {
	for i, vr := range h.retired {
		if vr.dir == dir {
			return i
		}
	}
	return -1
}

// openFromOtherVersions 在本请求的版本（vr）之外还开着的版本目录里找一个
// 文件（旧页面的哈希资源）：h.current（若它已不是本请求的 vr——请求在途
// 期间链接又切走再切回，比 vr 更新的版本会回到 current 而不在 retired 里）
// 和 h.retired。current/retired 里的 root 一定还开着（淘汰时先摘出去再关）。
func (h *Handler) openFromOtherVersions(vr *versionRoot, name string) (fs.File, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current != nil && h.current != vr {
		if f, err := h.current.root.Open(name); err == nil {
			if st, statErr := f.Stat(); statErr == nil && !st.IsDir() {
				return f, true
			}
			f.Close()
		}
	}
	for i := len(h.retired) - 1; i >= 0; i-- {
		if f, err := h.retired[i].root.Open(name); err == nil {
			if st, statErr := f.Stat(); statErr == nil && !st.IsDir() {
				return f, true
			}
			f.Close()
		}
	}
	return nil, false
}

// resolve 把 URL 路径映射到文件；文件不存在时回退到 index.html（前端路由如 /t/123）。
// 带哈希的资源（assets/ 下）当前版本没有时，先到换下来的旧版本里找。
func (h *Handler) resolve(vr *versionRoot, urlPath string) (string, fs.File, error) {
	name := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if name == "" || strings.HasSuffix(name, "/") {
		name += "index.html"
	}
	if f, err := vr.root.Open(name); err == nil {
		if st, statErr := f.Stat(); statErr == nil && !st.IsDir() {
			return name, f, nil
		}
		f.Close()
	}
	// 哈希资源在本请求的版本里没有：页面可能来自更新的版本（本请求 acquire
	// 之后链接又切走/切回了），也可能来自更旧的版本（index.html 是 no-cache，
	// 浏览器刚拿到旧页面链接就切了版本）。到 current 和 retired 里补。
	if strings.HasPrefix(name, "assets/") {
		if f, ok := h.openFromOtherVersions(vr, name); ok {
			return name, f, nil
		}
	}
	// SPA 回退。
	fallback, err := vr.root.Open("index.html")
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
