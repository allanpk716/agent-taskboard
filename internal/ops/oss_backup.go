package ops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/guestlinkkey"
)

// BackupURLTTL 是 CLI backup-url 签出备份下载地址的有效期。
const BackupURLTTL = time.Hour

// BackupObjectURL 返回一个备份对象的一小时签名下载地址。
func BackupObjectURL(ctx context.Context, objects attachments.ObjectStorage, name string) (string, error) {
	if !backupNameRe.MatchString(name) {
		return "", fmt.Errorf("备份名 %q 无效，应形如 taskboard-YYYYMMDD-HHMMSS-daily.db", name)
	}
	disposition := fmt.Sprintf("attachment; filename=%q", name)
	target, ok, err := objects.ObjectURL(ctx, attachments.BackupRelKey(name), BackupURLTTL, disposition)
	if err != nil {
		return "", fmt.Errorf("签出 OSS 备份 %s 失败：%w", name, err)
	}
	if !ok {
		return "", fmt.Errorf("当前对象后端不支持备份签名地址")
	}
	return target, nil
}

// LatestBackupFileName 按备份记录找出最近一份成功写到目标的库副本，供
// restore -from-oss latest 使用。
func LatestBackupFileName(ctx context.Context, db *sql.DB) (string, error) {
	var name string
	err := db.QueryRowContext(ctx, `
		SELECT filename FROM backup_records
		WHERE error IS NULL AND integrity_ok = 1 AND location = 'target'
		ORDER BY at DESC, id DESC LIMIT 1`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("库里没有成功备份记录，无法解析 latest；请用 restore -from-oss <备份名>")
	}
	if err != nil {
		return "", fmt.Errorf("读取最近备份记录失败：%w", err)
	}
	return name, nil
}

// DownloadBackupFromOSS 把库副本和访客链接密钥下载到本地备份目录，返回库
// 副本路径；附件镜像不上传到 OSS，恢复时由当前附件后端本身提供。
func DownloadBackupFromOSS(ctx context.Context, objects attachments.ObjectStorage, dir, name string) (string, error) {
	if !backupNameRe.MatchString(name) {
		return "", fmt.Errorf("备份名 %q 无效，应形如 taskboard-YYYYMMDD-HHMMSS-daily.db", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("建本地备份目录 %s 失败：%w", dir, err)
	}

	dbPath := filepath.Join(dir, name)
	if err := downloadObject(ctx, objects, attachments.BackupRelKey(name), dbPath); err != nil {
		return "", fmt.Errorf("下载 OSS 库副本 %s 失败：%w", name, err)
	}
	key := filepath.Join(dir, guestlinkkey.FileName)
	err := downloadObject(ctx, objects, attachments.BackupRelKey(guestlinkkey.FileName), key)
	if errors.Is(err, attachments.ErrObjectNotFound) {
		return dbPath, nil
	}
	if err != nil {
		return dbPath, fmt.Errorf("下载 OSS 访客链接加密密钥失败：%w", err)
	}
	return dbPath, nil
}

func downloadObject(ctx context.Context, objects attachments.ObjectStorage, relKey, path string) error {
	body, size, err := objects.OpenObject(ctx, relKey)
	if err != nil {
		return err
	}
	defer body.Close()
	if err := copyReaderTo(body, path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() != size {
		_ = os.Remove(path)
		return fmt.Errorf("对象大小不符：应为 %d 字节，实际下载 %d 字节", size, info.Size())
	}
	return nil
}
