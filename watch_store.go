package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

type watchStore struct {
	db *sql.DB
}

func openWatchStore(path string) (*watchStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("创建监控状态目录失败：%w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析监控状态数据库路径失败：%w", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(absolute))
	if err != nil {
		return nil, fmt.Errorf("打开监控状态数据库失败：%w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(context.Background(), "PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("设置监控状态数据库锁等待失败：%w", err)
	}
	if _, err := db.ExecContext(context.Background(), "PRAGMA journal_mode = WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("启用监控状态数据库 WAL 失败：%w", err)
	}
	if _, err := db.ExecContext(context.Background(), "PRAGMA synchronous = NORMAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("设置监控状态数据库同步模式失败：%w", err)
	}
	const tableSchema = `
CREATE TABLE IF NOT EXISTS watch_actor_state (
	guid TEXT PRIMARY KEY NOT NULL,
	actor_name TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('pending', 'done')),
	attempts INTEGER NOT NULL DEFAULT 0,
	last_attempt_at INTEGER NOT NULL DEFAULT 0,
	next_attempt_at INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	updated_at INTEGER NOT NULL
	);`
	if _, err := db.ExecContext(context.Background(), tableSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化监控状态数据库结构失败：%w", err)
	}
	// 老状态没有检索版本；空版本会触发一次受保护的重新评估。
	rows, err := db.Query("PRAGMA table_info(watch_actor_state)")
	if err != nil {
		db.Close()
		return nil, err
	}
	hasRevision := false
	for rows.Next() {
		var cid, notnull, primary int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &primary); err != nil {
			rows.Close()
			db.Close()
			return nil, err
		}
		if name == "matching_revision" {
			hasRevision = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		db.Close()
		return nil, err
	}
	if !hasRevision {
		if _, err := db.Exec("ALTER TABLE watch_actor_state ADD COLUMN matching_revision TEXT NOT NULL DEFAULT ''"); err != nil {
			db.Close()
			return nil, fmt.Errorf("迁移监控检索版本失败：%w", err)
		}
	}
	if err := os.Chmod(absolute, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("设置监控状态数据库权限失败：%w", err)
	}
	return &watchStore{db: db}, nil
}

func (store *watchStore) Close() error { return store.db.Close() }

func (store *watchStore) ShouldProcess(ctx context.Context, guid string, now time.Time, revision string, hasMissingFields bool) (bool, error) {
	var status string
	var previousRevision string
	var nextAttempt int64
	err := store.db.QueryRowContext(ctx,
		"SELECT status, next_attempt_at, matching_revision FROM watch_actor_state WHERE guid = ?", guid,
	).Scan(&status, &nextAttempt, &previousRevision)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("读取演员监控状态失败：%w", err)
	}
	if previousRevision != revision {
		return true, nil
	}
	if status == "done" && !hasMissingFields || nextAttempt > now.Unix() {
		return false, nil
	}
	return true, nil
}

func (store *watchStore) MarkDone(ctx context.Context, guid, name string, now time.Time, revision string) error {
	_, err := store.db.ExecContext(ctx, `
INSERT INTO watch_actor_state (guid, actor_name, status, attempts, last_attempt_at, next_attempt_at, last_error, updated_at, matching_revision)
VALUES (?, ?, 'done', 0, ?, ?, '', ?, ?)
ON CONFLICT(guid) DO UPDATE SET
	actor_name = excluded.actor_name,
	status = 'done',
	attempts = 0,
	last_attempt_at = excluded.last_attempt_at,
	next_attempt_at = excluded.next_attempt_at,
	last_error = '',
	updated_at = excluded.updated_at,
	matching_revision = excluded.matching_revision`, guid, name, now.Unix(), now.Add(7*24*time.Hour).Unix(), now.Unix(), revision)
	if err != nil {
		return fmt.Errorf("保存演员完成状态失败：%w", err)
	}
	return nil
}

func (store *watchStore) MarkFailure(ctx context.Context, guid, name string, cause error, now time.Time, revision string) (time.Duration, error) {
	if cause == nil {
		return 0, fmt.Errorf("不能将空错误记录为演员处理失败")
	}
	message := strings.TrimSpace(cause.Error())
	if len(message) > 2048 {
		message = message[:2048]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	const query = `
INSERT INTO watch_actor_state (guid, actor_name, status, attempts, last_attempt_at, next_attempt_at, last_error, updated_at, matching_revision)
VALUES (?, ?, 'pending', 1, ?, ?, ?, ?, ?)
ON CONFLICT(guid) DO UPDATE SET
	actor_name = excluded.actor_name,
	status = 'pending',
	attempts = CASE WHEN watch_actor_state.matching_revision != excluded.matching_revision THEN 1 ELSE watch_actor_state.attempts + 1 END,
	last_attempt_at = excluded.last_attempt_at,
		next_attempt_at = excluded.last_attempt_at + CASE WHEN watch_actor_state.matching_revision != excluded.matching_revision OR watch_actor_state.attempts + 1 = 1 THEN 60 WHEN watch_actor_state.attempts + 1 = 2 THEN 120 WHEN watch_actor_state.attempts + 1 = 3 THEN 300 WHEN watch_actor_state.attempts + 1 = 4 THEN 900 WHEN watch_actor_state.attempts + 1 = 5 THEN 1800 WHEN watch_actor_state.attempts + 1 = 6 THEN 3600 WHEN watch_actor_state.attempts + 1 = 7 THEN 21600 ELSE 86400 END,
	last_error = excluded.last_error,
	updated_at = excluded.updated_at,
	matching_revision = excluded.matching_revision
RETURNING attempts, next_attempt_at`
	var attempts int
	var nextAttempt int64
	err := store.db.QueryRowContext(ctx, query,
		guid, name, now.Unix(), now.Unix()+60, message, now.Unix(), revision,
	).Scan(&attempts, &nextAttempt)
	if err != nil {
		return 0, fmt.Errorf("保存演员重试状态失败：%w", err)
	}
	return time.Duration(nextAttempt-now.Unix()) * time.Second, nil
}
