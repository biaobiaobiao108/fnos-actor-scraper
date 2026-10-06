package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCacheRevisionAndIncompleteExpiry(t *testing.T) {
	dir := t.TempDir()
	writeGfriendsTestTree(t, dir, gfriendsTestTree(t, map[string]map[string]string{"a": {"テスト.jpg": "テスト.jpg"}}))
	requests := 0
	upstream := &Upstream{client: &http.Client{Transport: gfriendsRoundTripper(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})}}
	service := NewProviderService(upstream, dir, false)
	defer service.Close()
	sum := sha256.Sum256([]byte("テスト"))
	cachePath := filepath.Join(dir, "actors", hex.EncodeToString(sum[:])+".json")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0700); err != nil {
		t.Fatal(err)
	}
	old := ActorProfile{Name: "テスト", Biography: "旧版本资料", ImageCandidates: []PortraitCandidate{{URL: "https://example.com/old.jpg"}}}
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	profile, err := cachedScrape(context.Background(), service, "テスト", nil, dir, false)
	if err != nil || profile == nil || profile.Biography != "" || requests == 0 {
		t.Fatalf("旧缓存未失效：%+v err=%v requests=%d", profile, err, requests)
	}
	if profile.LookupRevision != service.lookupRevision("テスト") {
		t.Fatal("新缓存未保存检索版本")
	}
	requests = 0
	if _, err := cachedScrape(context.Background(), service, "テスト", nil, dir, false); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatal("新鲜部分缓存被重复查询")
	}
	oldTime := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(cachePath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if _, err := cachedScrape(context.Background(), service, "テスト", nil, dir, false); err != nil {
		t.Fatal(err)
	}
	if requests == 0 {
		t.Fatal("部分缓存阻挡七天重新评估")
	}
}
