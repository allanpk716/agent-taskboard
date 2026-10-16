//go:build !windows

// Package fslink 把 web/ 做成指向 web-<version>/ 的目录链接；linux 上用
// 符号链接（不需要特权）。语义与 Windows 版一致，见 fslink_windows.go。
package fslink

import (
	"fmt"
	"os"
	"path/filepath"
)

// Make 把 link 创建为指向 target 的符号链接。link 已存在时会先被移除。
func Make(link, target string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("移除旧链接 %s：%w", link, err)
	}
	if err := os.Symlink(abs, link); err != nil {
		return fmt.Errorf("创建符号链接（%s → %s）：%w", link, abs, err)
	}
	return nil
}
