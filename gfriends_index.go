package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const gfriendsIndexVersion = "1"

func (service *ProviderService) loadGfriendsIndex(ctx context.Context) (*sql.DB, error) {
	service.indexMu.Lock()
	defer service.indexMu.Unlock()
	if service.indexReady {
		return service.indexDB, nil
	}
	if err := os.MkdirAll(service.cacheDir, 0o750); err != nil {
		return nil, fmt.Errorf("创建 Gfriends 缓存目录失败：%w", err)
	}

	treePath := filepath.Join(service.cacheDir, "gfriends-filetree.json")
	data, downloaded, err := service.readGfriendsTree(ctx, treePath)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	sourceHash := hex.EncodeToString(hash[:])

	indexPath := filepath.Join(service.cacheDir, "gfriends-index.db")
	db, err := openGfriendsIndexDB(indexPath)
	if err != nil {
		return nil, err
	}
	if err := ensureGfriendsIndexSchema(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	ready, err := gfriendsIndexMatches(ctx, db, sourceHash)
	if err != nil {
		db.Close()
		return nil, err
	}
	if !ready {
		fmt.Println("正在构建 Gfriends SQLite 演员名称索引……")
		count, err := rebuildGfriendsIndex(ctx, db, data, sourceHash)
		if err != nil {
			db.Close()
			return nil, err
		}
		fmt.Printf("Gfriends 名称索引已就绪：%d 个名称\n", count)
	}
	if downloaded || service.refresh {
		if err := writeFileAtomic(treePath, data, 0o640); err != nil {
			db.Close()
			return nil, fmt.Errorf("保存 Gfriends 文件树缓存失败：%w", err)
		}
	}
	if err := os.Chmod(indexPath, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("设置 Gfriends 索引权限失败：%w", err)
	}
	service.indexDB = db
	service.indexReady = true
	return db, nil
}

func (service *ProviderService) readGfriendsTree(ctx context.Context, path string) ([]byte, bool, error) {
	if !service.refresh {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 && info.Size() <= defaultBodyLimit && time.Since(info.ModTime()) < 24*time.Hour {
			data, readErr := os.ReadFile(path)
			if readErr == nil && json.Valid(data) {
				return data, false, nil
			}
		}
	}
	data, err := service.upstream.Get(ctx, gfriendsTreeURL, map[string]string{"Accept": "application/json"}, defaultBodyLimit)
	if err != nil {
		return nil, false, err
	}
	if !json.Valid(data) {
		return nil, false, fmt.Errorf("Gfriends 文件树不是有效 JSON")
	}
	return data, true, nil
}

func openGfriendsIndexDB(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析 Gfriends 索引路径失败：%w", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(absolute))
	if err != nil {
		return nil, fmt.Errorf("打开 Gfriends SQLite 索引失败：%w", err)
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("初始化 Gfriends SQLite 索引失败：%w", err)
		}
	}
	return db, nil
}

func ensureGfriendsIndexSchema(ctx context.Context, db *sql.DB) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS gfriends_aliases (
			name_norm TEXT NOT NULL,
			alias TEXT NOT NULL,
			image_path TEXT NOT NULL,
			PRIMARY KEY (name_norm, image_path)
		) WITHOUT ROWID`,
		`CREATE TABLE IF NOT EXISTS gfriends_index_meta (
			key TEXT PRIMARY KEY NOT NULL,
			value TEXT NOT NULL
		) WITHOUT ROWID`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("创建 Gfriends SQLite 索引结构失败：%w", err)
		}
	}
	return nil
}

func gfriendsIndexMatches(ctx context.Context, db *sql.DB, sourceHash string) (bool, error) {
	values := map[string]string{}
	rows, err := db.QueryContext(ctx, "SELECT key, value FROM gfriends_index_meta WHERE key IN ('source_hash', 'index_version')")
	if err != nil {
		return false, fmt.Errorf("读取 Gfriends 索引版本失败：%w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return false, fmt.Errorf("读取 Gfriends 索引版本失败：%w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("读取 Gfriends 索引版本失败：%w", err)
	}
	return values["source_hash"] == sourceHash && values["index_version"] == gfriendsIndexVersion, nil
}

func rebuildGfriendsIndex(ctx context.Context, db *sql.DB, data []byte, sourceHash string) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("开始重建 Gfriends 名称索引失败：%w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM gfriends_aliases"); err != nil {
		return 0, fmt.Errorf("清理旧 Gfriends 名称索引失败：%w", err)
	}
	insert, err := tx.PrepareContext(ctx, "INSERT OR IGNORE INTO gfriends_aliases (name_norm, alias, image_path) VALUES (?, ?, ?)")
	if err != nil {
		return 0, fmt.Errorf("准备 Gfriends 索引写入失败：%w", err)
	}
	defer insert.Close()
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decodeGfriendsTree(ctx, decoder, insert); err != nil {
		return 0, fmt.Errorf("解析 Gfriends 文件树失败：%w", err)
	}
	var count int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM gfriends_aliases").Scan(&count); err != nil {
		return 0, fmt.Errorf("统计 Gfriends 名称索引失败：%w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gfriends_index_meta (key, value) VALUES ('source_hash', ?), ('index_version', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, sourceHash, gfriendsIndexVersion); err != nil {
		return 0, fmt.Errorf("保存 Gfriends 索引版本失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("保存 Gfriends 名称索引失败：%w", err)
	}
	return count, nil
}

func decodeGfriendsTree(ctx context.Context, decoder *json.Decoder, insert *sql.Stmt) error {
	start, err := decoder.Token()
	if err != nil {
		return err
	}
	if start != json.Delim('{') {
		return fmt.Errorf("根节点不是对象")
	}
	contentFound := false
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("根节点包含非文本键")
		}
		valueToken, err := decoder.Token()
		if err != nil {
			return err
		}
		if key == "Content" {
			contentFound = true
			if valueToken != json.Delim('{') {
				return fmt.Errorf("Content 节点不是对象")
			}
			if err := indexGfriendsObjectBody(ctx, decoder, []string{"Content"}, insert); err != nil {
				return err
			}
			continue
		}
		if err := skipJSONValue(decoder, valueToken); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if !contentFound {
		return fmt.Errorf("缺少 Content 节点")
	}
	return nil
}

func indexGfriendsObjectBody(ctx context.Context, decoder *json.Decoder, parents []string, insert *sql.Stmt) error {
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("Content 节点包含非文本键")
		}
		valueToken, err := decoder.Token()
		if err != nil {
			return err
		}
		if key == "Information" {
			if err := skipJSONValue(decoder, valueToken); err != nil {
				return err
			}
			continue
		}
		switch value := valueToken.(type) {
		case json.Delim:
			if value == '{' {
				if err := indexGfriendsObjectBody(ctx, decoder, appendPath(parents, key), insert); err != nil {
					return err
				}
			} else if err := skipJSONValue(decoder, valueToken); err != nil {
				return err
			}
		case string:
			alias := strings.TrimSuffix(strings.TrimSuffix(key, ".jpg"), ".png")
			if normalized := gfriendsNormalizeName(alias); normalized != "" {
				imagePath := strings.Join(appendPath(parents, value), "/")
				if _, err := insert.ExecContext(ctx, normalized, alias, imagePath); err != nil {
					return fmt.Errorf("写入名称 %q 失败：%w", alias, err)
				}
			}
		}
	}
	_, err := decoder.Token()
	return err
}

func skipJSONValue(decoder *json.Decoder, token json.Token) error {
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		for decoder.More() {
			if _, err := decoder.Token(); err != nil {
				return err
			}
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := skipJSONValue(decoder, value); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	case '[':
		for decoder.More() {
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := skipJSONValue(decoder, value); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	default:
		return fmt.Errorf("无法跳过 JSON 分隔符 %q", delimiter)
	}
}

func appendPath(path []string, value string) []string {
	result := make([]string, len(path)+1)
	copy(result, path)
	result[len(path)] = value
	return result
}

func gfriendsNormalizeName(value string) string {
	value = norm.NFKC.String(strings.ToLower(value))
	var result strings.Builder
	for _, char := range value {
		if char >= 'ァ' && char <= 'ヶ' {
			char -= 0x60
		}
		if unicode.IsSpace(char) || unicode.IsPunct(char) || unicode.IsSymbol(char) {
			continue
		}
		result.WriteRune(char)
	}
	return result.String()
}
