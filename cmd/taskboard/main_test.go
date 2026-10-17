package main_test

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// buildBinary 在测试前编译一次真实的 taskboard 二进制。
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "taskboard")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "github.com/allanpk716/agent-taskboard/cmd/taskboard")
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("编译 taskboard 失败：%v\n%s", err, out)
	}
	return bin
}

func projectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(dir)) // cmd/taskboard/ → 仓库根
}

func runTaskboard(t *testing.T, bin string, args ...string) string {
	t.Helper()
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("taskboard %s 失败：%v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// taskboard 命令行创建管理员后，可以启动服务并在网页会话层登录。
func Test命令行创建管理员后可以启动服务并登录(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译并启动真实二进制")
	}
	bin := buildBinary(t)
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	webDir := filepath.Join(dir, "web")
	if err := os.MkdirAll(webDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!doctype html><title>ok</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	configPath := filepath.Join(dir, "config.toml")
	configToml := fmt.Sprintf("data_dir = '%s'\nport = %d\nweb_dir = '%s'\n", filepath.ToSlash(dataDir), port, filepath.ToSlash(webDir))
	if err := os.WriteFile(configPath, []byte(configToml), 0o644); err != nil {
		t.Fatal(err)
	}

	// 创建管理员。
	out := runTaskboard(t, bin, "create-admin", "-config", configPath, "-username", "admin", "-password", "secret-pass")
	if !strings.Contains(out, "已创建管理员 admin") {
		t.Fatalf("create-admin 输出不对：%q", out)
	}
	// 重复创建同名管理员被拒绝，且退出码非 0。
	if dup := exec.Command(bin, "create-admin", "-config", configPath, "-username", "admin", "-password", "x"); dup.Run() == nil {
		t.Fatal("重复创建同名管理员应当失败")
	}

	// 启动服务，只监听 127.0.0.1。
	serve := exec.Command(bin, "serve", "-config", configPath)
	serve.Stdout = os.Stdout
	serve.Stderr = os.Stderr
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Windows 上无法向子进程投递 SIGTERM，直接 Kill；优雅停机由 serve 内部的信号处理负责。
		serve.Process.Kill()
		serve.Wait()
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitReady(t, base)

	// 网页会话层登录成功。
	res, err := http.Post(base+"/api/v1/session", "application/json",
		strings.NewReader(`{"username":"admin","password":"secret-pass"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("登录状态码 = %d, 想 200", res.StatusCode)
	}
}

func waitReady(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		res, err := http.Get(base + "/")
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
			last = fmt.Sprintf("状态码 %d", res.StatusCode)
		} else {
			last = err.Error()
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("服务在 15 秒内没有就绪：%s（最后：%v）", base, last)
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
