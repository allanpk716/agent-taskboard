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

// MovePlainDirAside 把普通目录 dir 挪到 target 留档（web/ 首次链接化挪成
// web-initial、更新器切换链接时挪成 web-retired-<时间戳> 共用这一处，
// 评审 P3-16）。dir 本身是链接或不存在时不做任何事（链接随时可以重建，
// 由调用方另行处理）；返回是否真的挪了。
func MovePlainDirAside(dir, target string) (bool, error) {
	if IsLink(dir) {
		return false, nil
	}
	if _, err := os.Stat(dir); err != nil {
		return false, nil
	}
	return true, os.Rename(dir, target)
}
