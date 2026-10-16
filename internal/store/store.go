// Package store 打开并迁移 data_dir 下的 SQLite（WAL）数据库。
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // 纯 Go 驱动，CGO_ENABLED=0 交叉构建可用
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store 持有打开的数据库。后续工单的仓储都建在它之上。
type Store struct {
	DB *sql.DB
}

// Open 确保 dataDir 存在，打开 taskboard.db，开启 WAL 并执行迁移。
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录 %s 失败：%w", dataDir, err)
	}
	dsn := "file:" + filepath.ToSlash(filepath.Join(dataDir, "taskboard.db"))
	dsn += "?" + strings.Join([]string{
		// database/sql 连接池里每条连接都要生效的 PRAGMA 走 DSN。
		"_pragma=busy_timeout(10000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=foreign_keys(1)",
		"_pragma=synchronous(NORMAL)",
	}, "&")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败：%w", err)
	}
	// modernc.org/sqlite 单写者：限制成一条写连接，避免 database is locked。
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

// migrate 按文件名顺序执行未应用的 migrations/*.sql，每个文件一个事务。
func (s *Store) migrate() error {
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("建 schema_migrations 失败：%w", err)
	}

	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	for _, name := range names {
		var version int
		if _, err := fmt.Sscanf(filepath.Base(name), "%d_", &version); err != nil {
			return fmt.Errorf("迁移文件名 %q 无法解析出版本号：%w", name, err)
		}
		var applied int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}
		body, err := migrationsFS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("迁移 %s 失败：%w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, datetime('now'))`, version); err != nil {
			tx.Rollback()
			return fmt.Errorf("记录迁移 %s 失败：%w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
