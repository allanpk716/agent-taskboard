package attachments

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ObjectStorage 按相对键操作附件以外的系统对象（归档包、备份等）。relKey
// 相对 OSS 根前缀（如 archive-packages/a.zip），实现负责拼出完整对象键。
type ObjectStorage interface {
	PutObject(ctx context.Context, relKey string, src io.Reader, size int64, meta PutMeta) error
	OpenObject(ctx context.Context, relKey string) (io.ReadCloser, int64, error)
	DeleteObject(ctx context.Context, relKey string) error
	ObjectURL(ctx context.Context, relKey string, ttl time.Duration, disposition string) (url string, ok bool, err error)
}

// ArchivePackageRelKey 是归档包与瘦身包在系统根前缀下的相对键。
func ArchivePackageRelKey(name string) string {
	return "archive-packages/" + name
}

// BackupRelKey 是库备份副本与随备份保存的密钥文件在系统根前缀下的相对键。
func BackupRelKey(name string) string {
	return "backups/" + name
}

// ObjectStorageOf 返回当前后端的非附件对象接口。本地后端的归档包只留在
// 暂存目录，因此不提供这个接口。
func ObjectStorageOf(storage Storage) ObjectStorage {
	if objects, ok := storage.(ObjectStorage); ok {
		return objects
	}
	return nil
}

// ValidateObjectRelKey 校验相对对象键，拒绝空键、路径穿越和空目录段。
func ValidateObjectRelKey(relKey string) error {
	if strings.TrimSpace(relKey) == "" {
		return errors.New("对象相对键不能为空")
	}
	if strings.HasPrefix(relKey, "/") || strings.HasSuffix(relKey, "/") {
		return fmt.Errorf("对象相对键 %q 不能以 / 开头或结尾", relKey)
	}
	for _, part := range strings.Split(relKey, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("对象相对键 %q 不是安全路径", relKey)
		}
	}
	return nil
}
