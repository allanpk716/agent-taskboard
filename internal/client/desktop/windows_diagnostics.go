//go:build windows

package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/client"
	"golang.org/x/sys/windows"
)

const dialogTitle = "Taskboard 桌面客户端"

type windowsFatalReporter struct {
	mu   sync.Mutex
	path string
	file *os.File
}

var (
	fatalReporterOnce sync.Once
	fatalReporter     *windowsFatalReporter
)

// NewFatalReporter 返回进程级日志和致命错误对话框实现；日志初始化失败不会阻止程序继续。
func NewFatalReporter() client.FatalReporter {
	fatalReporterOnce.Do(func() {
		fatalReporter = &windowsFatalReporter{}
		fatalReporter.initialize()
	})
	return fatalReporter
}

func (r *windowsFatalReporter) initialize() {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		root = os.TempDir()
	}
	r.path = filepath.Join(root, "taskboard-client", "client.log")
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return
	}
	if info, err := os.Stat(r.path); err == nil && info.Size() > 1024*1024 {
		_ = os.Remove(r.path + ".1")
		if err := os.Rename(r.path, r.path+".1"); err != nil {
			_ = os.Truncate(r.path, 0)
		}
	}
	r.file, _ = os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

func (r *windowsFatalReporter) Infof(format string, args ...any) {
	r.log("INFO", fmt.Sprintf(format, args...))
}

func (r *windowsFatalReporter) Fatalf(exitCode int, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	stack := debugStack()
	r.log("FATAL", message+"\n"+stack)
	body := message + "\n\n日志文件：" + r.path +
		"\n请查看日志并重新下载客户端，或联系管理员处理。点击确定后客户端将退出。"
	showDialog(body, windows.MB_OK|windows.MB_ICONERROR)
	os.Exit(exitCode)
}

func (r *windowsFatalReporter) log(level, message string) {
	line := time.Now().Format("2006-01-02 15:04:05.000") + " " + level + " " + message + "\n"
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file != nil {
		_, _ = r.file.WriteString(line)
	}
}

func guard(name string, fn func()) {
	defer func() {
		if err := recover(); err != nil {
			NewFatalReporter().Fatalf(1, "%s发生内部错误：%v", name, err)
		}
	}()
	fn()
}

func debugStack() string { return string(debug.Stack()) }

type firstInstallNotifier struct{}

func (firstInstallNotifier) NotifyFirstInstall() error {
	body := "Taskboard 桌面客户端已安装。\n\n接下来会打开浏览器请你登录；以后它会在右下角托盘运行并开机自启。"
	_, err := showDialog(body, windows.MB_OK|windows.MB_ICONINFORMATION)
	return err
}

func ShowAlreadyRunning() {
	body := "Taskboard 桌面客户端已经在运行，请在右下角托盘查看。"
	_, _ = showDialog(body, windows.MB_OK|windows.MB_ICONINFORMATION)
}

func showDialog(body string, flags uint32) (int32, error) {
	text, err := windows.UTF16PtrFromString(body)
	if err != nil {
		return 0, err
	}
	title, err := windows.UTF16PtrFromString(dialogTitle)
	if err != nil {
		return 0, err
	}
	return windows.MessageBox(0, text, title, flags)
}
