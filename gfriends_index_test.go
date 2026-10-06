package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type gfriendsRoundTripper func(*http.Request) (*http.Response, error)

func (f gfriendsRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func gfriendsTestTree(t *testing.T, folders map[string]map[string]string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"Content": folders})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeGfriendsTestTree(t *testing.T, directory string, data []byte) {
	t.Helper()
	path := filepath.Join(directory, "gfriends-filetree.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	modified := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}

func expireGfriendsCheck(service *ProviderService) {
	service.indexMu.Lock()
	service.indexChecked = time.Now().Add(-2 * time.Minute)
	service.indexMu.Unlock()
}

func TestGfriendsPortraitGroupingAndAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		folders map[string]map[string]string
		count   int
	}{
		{"不同目录同名及修复图片", map[string]map[string]string{
			"a": {"佐山愛.jpg": "佐山愛.jpg"},
			"b": {"佐山愛.png": "AI-Fix-佐山愛.PNG?raw=true"},
			"c": {"佐山愛.jpg": "佐山愛.jpg?version=2"},
		}, 3},
		{"别名指向不同姓名", map[string]map[string]string{
			"a": {"佐山愛.jpg": "佐山愛.jpg"}, "b": {"佐山愛.jpg": "別人.jpg"},
		}, 0},
		{"不跨不同假名姓名归组", map[string]map[string]string{
			"a": {"佐山愛.jpg": "あい.jpg"}, "b": {"佐山愛.jpg": "アイ.jpg"},
		}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			writeGfriendsTestTree(t, directory, gfriendsTestTree(t, tc.folders))
			service := NewProviderService(nil, directory, false)
			t.Cleanup(func() { service.Close() })
			profile, err := service.scrapeGfriends(context.Background(), "佐山愛")
			if err != nil {
				t.Fatal(err)
			}
			if tc.count == 0 {
				if profile != nil {
					t.Fatal("歧义姓名返回了头像")
				}
				return
			}
			if profile == nil || len(profile.ImageCandidates) != tc.count {
				t.Fatalf("候选数错误：%+v", profile)
			}
			if profile.ImageURL != profile.ImageCandidates[0].URL {
				t.Fatal("首图不是第一优先候选")
			}
		})
	}
}

func TestGfriendsCandidateLimitDoesNotHideAmbiguity(t *testing.T) {
	folders := make(map[string]map[string]string)
	for i := 0; i < 20; i++ {
		folders[fmt.Sprintf("%02d", i)] = map[string]string{"佐山愛.jpg": "佐山愛.jpg"}
	}
	directory := t.TempDir()
	writeGfriendsTestTree(t, directory, gfriendsTestTree(t, folders))
	service := NewProviderService(nil, directory, false)
	t.Cleanup(func() { service.Close() })
	profile, err := service.scrapeGfriends(context.Background(), "佐山愛")
	if err != nil {
		t.Fatal(err)
	}
	if profile == nil || len(profile.ImageCandidates) != maxGfriendsPortraitCandidates {
		t.Fatalf("候选上限错误：%+v", profile)
	}
	for i, candidate := range profile.ImageCandidates {
		if !strings.Contains(candidate.URL, fmt.Sprintf("/%02d/", i)) {
			t.Fatalf("候选没有按路径排序：%s", candidate.URL)
		}
	}
	folders["99"] = map[string]string{"佐山愛.jpg": "另一演员.jpg"}
	writeGfriendsTestTree(t, directory, gfriendsTestTree(t, folders))
	expireGfriendsCheck(service)
	profile, err = service.scrapeGfriends(context.Background(), "佐山愛")
	if err != nil {
		t.Fatal(err)
	}
	if profile != nil {
		t.Fatal("候选上限以外的歧义被遗漏")
	}
}

func TestGfriendsEmptyRebuildRollsBack(t *testing.T) {
	db, err := openGfriendsIndexDB(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := ensureGfriendsIndexSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	data := gfriendsTestTree(t, map[string]map[string]string{"a": {"佐山愛.jpg": "佐山愛.jpg"}})
	if _, err := rebuildGfriendsIndex(ctx, db, data, "healthy"); err != nil {
		t.Fatal(err)
	}
	if _, err := rebuildGfriendsIndex(ctx, db, []byte(`{"Content":{}}`), "empty"); err == nil {
		t.Fatal("空索引被接受")
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM gfriends_aliases").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("健康数据被覆盖：%d", count)
	}
	matched, err := gfriendsIndexMatches(ctx, db, "healthy")
	if err != nil || !matched {
		t.Fatalf("旧版本元数据未保留：%v", err)
	}
}

func TestGfriendsMigratesV1(t *testing.T) {
	db, err := openGfriendsIndexDB(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE gfriends_aliases (name_norm TEXT NOT NULL, alias TEXT NOT NULL, image_path TEXT NOT NULL, PRIMARY KEY(name_norm,image_path)) WITHOUT ROWID`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := ensureGfriendsIndexSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	data := gfriendsTestTree(t, map[string]map[string]string{"a": {"佐山愛.jpg": "佐山愛.jpg"}})
	if _, err := rebuildGfriendsIndex(ctx, db, data, "new"); err != nil {
		t.Fatal(err)
	}
	var identity string
	if err := db.QueryRow("SELECT portrait_identity FROM gfriends_aliases").Scan(&identity); err != nil {
		t.Fatal(err)
	}
	if identity != normalizeName("佐山愛") {
		t.Fatalf("迁移后的身份错误：%q", identity)
	}
}

func TestGfriendsLongRunningRefreshAndFailureRecovery(t *testing.T) {
	directory := t.TempDir()
	data := gfriendsTestTree(t, map[string]map[string]string{"a": {"佐山愛.jpg": "佐山愛.jpg"}})
	writeGfriendsTestTree(t, directory, data)
	calls := 0
	status := http.StatusForbidden
	responseBody := "拒绝"
	upstream := &Upstream{client: &http.Client{Transport: gfriendsRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(responseBody)), Header: make(http.Header), Request: r}, nil
	})}}
	service := NewProviderService(upstream, directory, false)
	t.Cleanup(func() { service.Close() })
	ctx := context.Background()
	db, err := service.loadGfriendsIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	updated := gfriendsTestTree(t, map[string]map[string]string{"b": {"優木あおい.jpg": "優木あおい.jpg"}})
	writeGfriendsTestTree(t, directory, updated)
	if _, err := service.loadGfriendsIndex(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM gfriends_aliases WHERE alias = '佐山愛'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("一分钟内应复用已加载索引：%d %v", count, err)
	}
	expireGfriendsCheck(service)
	refreshed, err := service.loadGfriendsIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed != db {
		t.Fatal("刷新关闭或替换了正在共享的连接")
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM gfriends_aliases WHERE alias = '優木あおい'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("文件变化没有触发重建：%d %v", count, err)
	}
	path := filepath.Join(directory, "gfriends-filetree.json")
	old := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	expireGfriendsCheck(service)
	if _, err := service.loadGfriendsIndex(ctx); err == nil || !isRetryableUpstreamError(err) {
		t.Fatalf("过期索引拉取失败应保留重试：%v", err)
	}
	if calls != 1 {
		t.Fatalf("过期缓存未拉取：%d", calls)
	}
	if _, err := service.loadGfriendsIndex(ctx); err == nil {
		t.Fatal("冷却期错误被吞掉")
	}
	if calls != 1 {
		t.Fatal("失败冷却期重复请求")
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM gfriends_aliases").Scan(&count); err != nil || count != 1 {
		t.Fatalf("失败损坏旧索引：%d %v", count, err)
	}
	status, responseBody = http.StatusOK, `{"Content":{}}`
	expireGfriendsCheck(service)
	if _, err := service.loadGfriendsIndex(ctx); err == nil {
		t.Fatal("空下载树被接受")
	}
	cached, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(cached) != string(updated) {
		t.Fatal("空下载树覆盖了健康缓存")
	}
	status, responseBody = http.StatusOK, string(data)
	expireGfriendsCheck(service)
	if _, err := service.loadGfriendsIndex(ctx); err != nil {
		t.Fatalf("暂时失败后无法恢复：%v", err)
	}
	if _, err := service.scrapeGfriends(ctx, "佐山愛"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM gfriends_aliases WHERE alias = '佐山愛'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("恢复后未更新索引：%d %v", count, err)
	}
}
