//go:build !windows

package desktop

import (
	"errors"

	"github.com/allanpk716/agent-taskboard/internal/client"
)

// ErrUnsupportedDesktop 表示当前只支持 Windows 桌面适配层。
var ErrUnsupportedDesktop = errors.New("taskboard 桌面客户端只支持 Windows")

// NewSystem 在非 Windows 平台返回明确的不支持错误，保证核心和命令可交叉编译检查。
type System struct{}

func NewSystem() (*System, error) { return nil, ErrUnsupportedDesktop }

func (*System) Dependencies() client.Dependencies { return client.Dependencies{} }
