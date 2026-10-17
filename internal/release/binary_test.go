package release_test

import (
	"os"
	"runtime"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/release"
)

// 测试二进制自身就是一份本平台的可执行文件：能通过检查，末尾追加数据也不影响。
func Test本平台可执行文件通过检查(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := release.CheckExecutable(data, runtime.GOOS, runtime.GOARCH); err != nil {
		t.Fatalf("测试二进制应通过检查：%v", err)
	}
	if err := release.CheckExecutable(append(data, []byte("\ntaskboard 1.2.3")...), runtime.GOOS, runtime.GOARCH); err != nil {
		t.Fatalf("末尾追加数据后应仍通过：%v", err)
	}
}

// 换到别的平台检查就不通过：Windows 上构建的 exe 不是 ELF，反之亦然。
// server/v0.1.4 正是“名叫 taskboard.exe 的 Linux 二进制”。
func Test别的平台的可执行文件不通过检查(t *testing.T) {
	self, _ := os.Executable()
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	other := map[string]string{"windows": "linux", "linux": "windows", "darwin": "linux"}[runtime.GOOS]
	if other == "" {
		t.Skip("本平台没有对照")
	}
	if err := release.CheckExecutable(data, other, runtime.GOARCH); err == nil {
		t.Fatalf("%s 的二进制在 %s 上检查应失败", runtime.GOOS, other)
	}
	if err := release.CheckExecutable([]byte("new windows exe 1.2.3"), runtime.GOOS, runtime.GOARCH); err == nil {
		t.Fatal("纯文本不应通过检查")
	}
}
