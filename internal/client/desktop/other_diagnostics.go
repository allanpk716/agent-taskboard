//go:build !windows

package desktop

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/allanpk716/agent-taskboard/internal/client"
)

type fatalReporterFallback struct{}

func NewFatalReporter() client.FatalReporter { return fatalReporterFallback{} }

func ShowAlreadyRunning() {
	fmt.Println("Taskboard 桌面客户端已经在运行，请在右下角托盘查看。")
}

func (fatalReporterFallback) Infof(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}

func (fatalReporterFallback) Fatalf(exitCode int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n%s", append(args, string(debug.Stack()))...)
	os.Exit(exitCode)
}
