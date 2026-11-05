package attachments

import (
	"context"
	"database/sql"
	"fmt"
)

// BackendMarker 是数据库设置表记录的附件实际后端。
type BackendMarker struct {
	Backend string
	Bucket  string
	Prefix  string
}

// ReadBackendMarker 读取附件后端标记；设置行缺失时第二个返回值为 false。
func ReadBackendMarker(ctx context.Context, db *sql.DB) (BackendMarker, bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM settings
		WHERE key IN ('attachments_backend', 'attachments_backend_bucket', 'attachments_backend_prefix')`)
	if err != nil {
		return BackendMarker{}, false, fmt.Errorf("读取附件后端标记失败：%w", err)
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return BackendMarker{}, false, fmt.Errorf("读取附件后端标记失败：%w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return BackendMarker{}, false, fmt.Errorf("读取附件后端标记失败：%w", err)
	}
	backend, exists := values["attachments_backend"]
	if !exists {
		return BackendMarker{}, false, nil
	}
	return BackendMarker{
		Backend: backend, Bucket: values["attachments_backend_bucket"], Prefix: values["attachments_backend_prefix"],
	}, true, nil
}

// WriteBackendMarker 在一个事务里改写附件实际后端。local 时不保留 OSS 的
// bucket / prefix 标记；OSS 时三者一起写入。
func WriteBackendMarker(ctx context.Context, db *sql.DB, marker BackendMarker) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("改写附件后端标记失败：%w", err)
	}
	defer tx.Rollback()
	upsert := func(key, value string) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES (?, ?, datetime('now'))
			ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value)
		return err
	}
	if err := upsert("attachments_backend", marker.Backend); err != nil {
		return fmt.Errorf("改写附件后端标记失败：%w", err)
	}
	if marker.Backend == AttachmentBackendOSS {
		if err := upsert("attachments_backend_bucket", marker.Bucket); err != nil {
			return fmt.Errorf("改写附件后端标记失败：%w", err)
		}
		if err := upsert("attachments_backend_prefix", marker.Prefix); err != nil {
			return fmt.Errorf("改写附件后端标记失败：%w", err)
		}
	} else if _, err := tx.ExecContext(ctx, `DELETE FROM settings
		WHERE key IN ('attachments_backend_bucket', 'attachments_backend_prefix')`); err != nil {
		return fmt.Errorf("改写附件后端标记失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("改写附件后端标记失败：%w", err)
	}
	return nil
}
