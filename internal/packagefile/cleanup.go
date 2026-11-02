package packagefile

import (
	"log"
	"os"
)

// RemoveForCleanup 尽力删除一个服务器文件，返回释放的字节数和是否失败。
func RemoveForCleanup(path string) (int64, int) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, 0
	}
	if err != nil {
		log.Printf("包移出后检查文件 %s 失败：%v", path, err)
		return 0, 1
	}
	if err := os.Remove(path); err != nil {
		log.Printf("包移出后删除文件 %s 失败：%v", path, err)
		return 0, 1
	}
	return info.Size(), 0
}
