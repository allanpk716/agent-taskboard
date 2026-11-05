package attachments

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
)

// BackupMirror 是备份目标里的本地附件镜像。主存储对象仍一律通过 Storage
// 读写；这里只封装镜像文件在备份目标内的落盘路径。
type BackupMirror struct {
	dir string
}

// NewBackupMirror 在备份目标 root 下使用 attachments 镜像目录。
func NewBackupMirror(root string) *BackupMirror {
	return &BackupMirror{dir: filepath.Join(root, "attachments")}
}

func (m *BackupMirror) path(id int64) string {
	return filepath.Join(m.dir, strconv.FormatInt(id, 10))
}

// Ensure 创建镜像目录。
func (m *BackupMirror) Ensure() error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return fmt.Errorf("连不上备份目标 %s：%w", m.dir, err)
	}
	return nil
}

// Stat 返回镜像对象大小；不存在时 exists=false。
func (m *BackupMirror) Stat(id int64) (int64, bool) {
	info, err := os.Stat(m.path(id))
	if err != nil {
		return 0, false
	}
	return info.Size(), true
}

// Write 原子写入一个镜像对象，用于承接 Storage.Open 的流。
func (m *BackupMirror) Write(id int64, src io.Reader, size int64) error {
	to := m.path(id)
	tmp := to + ".part"
	dst, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("写 %s 失败：%w", to, err)
	}
	written, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("写 %s 失败：%w", to, copyErr)
	}
	if written != size {
		_ = os.Remove(tmp)
		return fmt.Errorf("附件镜像 att_%d 大小不符：应为 %d 字节，实际写入 %d", id, size, written)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("写 %s 失败：%w", to, closeErr)
	}
	if err := os.Rename(tmp, to); err != nil {
		return fmt.Errorf("写 %s 失败：%w", to, err)
	}
	return nil
}

// Open 打开镜像对象，恢复备份时交给当前 Storage.Put。
func (m *BackupMirror) Open(id int64) (io.ReadCloser, int64, error) {
	file, err := os.Open(m.path(id))
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}

// RemoveForCleanup 删除一个镜像对象，返回释放的字节数和失败数。
func (m *BackupMirror) RemoveForCleanup(id int64) (int64, int) {
	path := m.path(id)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, 0
	}
	if err != nil {
		log.Printf("包移出后检查附件镜像 att_%d 失败：%v", id, err)
		return 0, 1
	}
	if err := os.Remove(path); err != nil {
		log.Printf("包移出后删除附件镜像 att_%d 失败：%v", id, err)
		return 0, 1
	}
	return info.Size(), 0
}
