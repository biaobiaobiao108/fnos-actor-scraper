package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

func TestErrorPreviewValidUTF8(t *testing.T) {
	// 构造一个在 256 字节边界附近切断多字节中文字符的字符串
	// 每个中文字符 3 字节，85 个字符 = 255 字节，第 86 个字符跨越 256 字节
	chars := strings.Repeat("测", 100)
	preview := errorPreview([]byte(chars))
	if !utf8.ValidString(preview) {
		t.Fatalf("errorPreview 生成了非法的 UTF-8 字符串：%q", preview)
	}
	if !strings.HasSuffix(preview, "…") {
		t.Fatalf("超长字符串未添加省略号：%q", preview)
	}
}

func TestScrapeOneNameOnDemandSources(t *testing.T) {
	dir := t.TempDir()
	// 写入 Gfriends 本地文件树
	writeGfriendsTestTree(t, dir, gfriendsTestTree(t, map[string]map[string]string{
		"FolderA": {"有头像演员.jpg": "有头像演员.jpg"},
	}))

	var javdbCalls int32
	var wikiCalls int32
	var wikidataCalls int32

	mockTransport := gfriendsRoundTripper(func(r *http.Request) (*http.Response, error) {
		urlStr := r.URL.String()
		switch {
		case strings.Contains(urlStr, "/search?f=actor"):
			atomic.AddInt32(&javdbCalls, 1)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<html></html>`)), Request: r}, nil
		case strings.Contains(urlStr, "wikipedia.org"):
			atomic.AddInt32(&wikiCalls, 1)
			body := `{"query":{"pages":[{"title":"有头像演员","extract":"这是来自维基百科的简介","thumbnail":{"source":"https://upload.wikimedia.org/test.jpg"}}]}}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		case strings.Contains(urlStr, "wikidata.org"):
			atomic.AddInt32(&wikidataCalls, 1)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"search":[]}`)), Request: r}, nil
		default:
			return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`not found`)), Request: r}, nil
		}
	})

	upstream := &Upstream{client: &http.Client{Transport: mockTransport}}
	service := NewProviderService(upstream, dir, false)
	defer service.Close()

	// 1. 测试：Gfriends 有头像 + Wikipedia 有简介 -> 应该跳过 JavDB 和 Wikidata
	profile, err := service.scrapeOneName(context.Background(), "有头像演员")
	if err != nil {
		t.Fatalf("scrapeOneName 失败：%v", err)
	}
	if profile == nil || len(profile.ImageCandidates) == 0 || profile.Biography == "" {
		t.Fatalf("资料未完整收集：%+v", profile)
	}
	if atomic.LoadInt32(&javdbCalls) != 0 {
		t.Fatalf("Gfriends 已有头像时应跳过 JavDB，但调用了 %d 次", javdbCalls)
	}
	if atomic.LoadInt32(&wikidataCalls) != 0 {
		t.Fatalf("资料已完整时应跳过 Wikidata，但调用了 %d 次", wikidataCalls)
	}
	if atomic.LoadInt32(&wikiCalls) == 0 {
		t.Fatal("未调用 Wikipedia 获取简介")
	}

	// 2. 测试：无头像且维基百科无条目的演员 -> 应该按需尝试 JavDB 和 Wikidata
	atomic.StoreInt32(&javdbCalls, 0)
	atomic.StoreInt32(&wikiCalls, 0)
	atomic.StoreInt32(&wikidataCalls, 0)

	_, _ = service.scrapeOneName(context.Background(), "无头像缺简介演员")
	if atomic.LoadInt32(&javdbCalls) == 0 {
		t.Fatal("Gfriends 无头像时应尝试 JavDB，但未调用")
	}
	if atomic.LoadInt32(&wikiCalls) == 0 {
		t.Fatal("应尝试 Wikipedia 获取简介，但未调用")
	}
	if atomic.LoadInt32(&wikidataCalls) == 0 {
		t.Fatal("仍缺少资料时应尝试 Wikidata 作为兜底，但未调用")
	}
}

func TestWikidataPriorLanguageFailureNotPollutingSuccess(t *testing.T) {
	mockTransport := gfriendsRoundTripper(func(r *http.Request) (*http.Response, error) {
		urlStr := r.URL.String()
		switch {
		case strings.Contains(urlStr, "language=zh"):
			// 模拟 zh 搜索临时网络错误
			return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`server error`)), Request: r}, nil
		case strings.Contains(urlStr, "language=ja"):
			// ja 搜索成功找到唯一匹配实体
			body := `{"search":[{"id":"Q12345","label":"テスト俳優","aliases":[]}]}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		case strings.Contains(urlStr, "Special:EntityData/Q12345.json"):
			body := `{"entities":{"Q12345":{"descriptions":{"ja":{"value":"日本の俳優"}},"aliases":{},"claims":{}}}}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		default:
			return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`not found`)), Request: r}, nil
		}
	})

	upstream := &Upstream{client: &http.Client{Transport: mockTransport}}
	profile, err := scrapeWikidata(context.Background(), upstream, "テスト俳優")
	if err != nil {
		t.Fatalf("前置语言的偶发失败不应污染成功结果，得到 err: %v", err)
	}
	if profile == nil || profile.Biography != "日本の俳優" {
		t.Fatalf("未正确获取实体资料：%+v", profile)
	}
}
