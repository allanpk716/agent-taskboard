package attachments

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// LocalStorage 是默认的本地后端：沿用 data_dir/attachments/<id> 的目录布局，
// 按 id 推路径的逻辑只留在这个实现内部。
type LocalStorage struct {
	dir string
}

// NewLocalStorage 创建附件目录并返回本地存储。
func NewLocalStorage(dataDir string) (*LocalStorage, error) {
	dir := filepath.Join(dataDir, "attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建附件目录 %s 失败：%w", dir, err)
	}
	return &LocalStorage{dir: dir}, nil
}

func (s *LocalStorage) path(id int64) string {
	return filepath.Join(s.dir, strconv.FormatInt(id, 10))
}

// Put 写入对象。读到的大小与声明不符时删除半截文件并返回错误，不留对象。
func (s *LocalStorage) Put(ctx context.Context, id int64, src io.Reader, size int64, meta PutMeta) error {
	_ = meta // 本地文件没有对象元数据可写
	dst, err := os.OpenFile(s.path(id), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开附件文件失败：%w", err)
	}
	written, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		_ = os.Remove(s.path(id))
		return fmt.Errorf("写附件文件失败：%w", copyErr)
	}
	if written != size {
		_ = os.Remove(s.path(id))
		return fmt.Errorf("附件文件大小不符：应为 %d 字节，实际写入 %d", size, written)
	}
	if closeErr != nil {
		_ = os.Remove(s.path(id))
		return fmt.Errorf("关闭附件文件失败：%w", closeErr)
	}
	return nil
}

func (s *LocalStorage) Open(ctx context.Context, id int64) (io.ReadCloser, int64, error) {
	file, err := os.Open(s.path(id))
	if os.IsNotExist(err) {
		return nil, 0, ErrObjectNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("打开附件文件失败：%w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, fmt.Errorf("读取附件文件信息失败：%w", err)
	}
	return file, info.Size(), nil
}

func (s *LocalStorage) Delete(ctx context.Context, id int64) error {
	if err := os.Remove(s.path(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除附件文件失败：%w", err)
	}
	return nil
}

func (s *LocalStorage) Stat(ctx context.Context, id int64) (int64, bool) {
	info, err := os.Stat(s.path(id))
	if err != nil {
		return 0, false
	}
	return info.Size(), true
}

func (s *LocalStorage) List(ctx context.Context, fn func(id int64, size int64, modTime time.Time) error) error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("读取附件目录失败：%w", err)
	}
	ids := make([]int64, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		id, err := strconv.ParseInt(entry.Name(), 10, 64)
		if err != nil {
			continue // 目录里混入别的文件时跳过，不阻断对账
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		size, ok := s.Stat(ctx, id)
		if !ok {
			continue
		}
		info, err := os.Stat(s.path(id))
		if err != nil {
			return fmt.Errorf("读取附件文件信息失败：%w", err)
		}
		if err := fn(id, size, info.ModTime()); err != nil {
			return err
		}
	}
	return nil
}

func (s *LocalStorage) DownloadURL(ctx context.Context, id int64, opts DownloadURLOpts) (string, bool, error) {
	return "", false, nil
}
