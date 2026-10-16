package fslink

import "os"

// IsLink 报告 path 本身是不是一个链接（junction / 符号链接）。
func IsLink(path string) bool {
	_, err := os.Lstat(path)
	if err != nil {
		return false
	}
	_, err = os.Readlink(path)
	return err == nil
}
