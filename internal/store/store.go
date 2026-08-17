// Package store 负责 SQLite 持久化与内容寻址的附件存储。
package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // 纯 Go 驱动，无 cgo，自带 FTS5
)

type Store struct {
	DB      *sql.DB
	dataDir string
}

// Open 打开（必要时创建）数据目录下的 pe.db，并把 schema 迁移到最新版本。
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "blobs"), 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}

	dsn := "file:" + url.PathEscape(filepath.Join(dataDir, "pe.db")) +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// 单用户、局域网、负载极轻。限制为单连接可以彻底避免 SQLite 的写锁竞争，
	// 代价（查询串行化）在这个量级下观察不到。
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	s := &Store{DB: db, dataDir: dataDir}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) DataDir() string { return s.dataDir }
