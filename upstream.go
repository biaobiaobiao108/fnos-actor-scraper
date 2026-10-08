package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const defaultBodyLimit int64 = 16 << 20

type Upstream struct {
	client *http.Client
	mu     sync.Mutex
	last   time.Time
	delay  time.Duration
}

type upstreamHTTPError struct {
	status int
	url    string
}

func (err *upstreamHTTPError) Error() string {
	return fmt.Sprintf("HTTP %d：%s", err.status, err.url)
}

func (err *upstreamHTTPError) Retryable() bool {
	return err.status == http.StatusTooManyRequests || err.status >= 500
}

func isRetryableUpstreamError(err error) bool {
	if err == nil {
		return false
	}
	var retryable interface{ Retryable() bool }
	if errors.As(err, &retryable) {
		return retryable.Retryable()
	}
	var networkErr net.Error
	var pathErr *os.PathError
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.As(err, &networkErr) || errors.As(err, &pathErr) || errors.Is(err, fs.ErrPermission)
}

func NewUpstream() *Upstream {
	delay := 2 * time.Second
	if value, err := strconv.Atoi(strings.TrimSpace(getenv("UPSTREAM_DELAY_MS", "2000"))); err == nil {
		if value < 500 {
			value = 500
		}
		if value > 60000 {
			value = 60000
		}
		delay = time.Duration(value) * time.Millisecond
	}
	client := &http.Client{Timeout: 25 * time.Second}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("上游重定向次数过多")
		}
		if request.URL.Scheme != "https" || !isPublicHost(request.URL.Hostname()) {
			return fmt.Errorf("上游重定向目标不是公网 HTTPS 地址")
		}
		return nil
	}
	return &Upstream{client: client, delay: delay}
}

func (u *Upstream) Get(ctx context.Context, target string, headers map[string]string, limit int64) ([]byte, error) {
	if limit <= 0 || limit > defaultBodyLimit {
		limit = defaultBodyLimit
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if wait := time.Until(u.last.Add(u.delay)); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		if request.Header.Get("User-Agent") == "" {
			request.Header.Set("User-Agent", "FnOS-Actor-Scraper/0.3 (+local media metadata utility)")
		}
		response, err := u.client.Do(request)
		if err != nil {
			u.last = time.Now()
			lastErr = err
			if attempt < 2 {
				if err := pause(ctx, backoff(attempt)); err != nil {
					return nil, err
				}
				continue
			}
			return nil, fmt.Errorf("上游请求失败：%w", err)
		}
		if isRetryable(response.StatusCode) && attempt < 2 {
			wait := retryDelay(response, attempt)
			response.Body.Close()
			u.last = time.Now()
			fmt.Printf("上游返回 HTTP %d，等待 %s 后重试\n", response.StatusCode, wait.Round(time.Second))
			if err := pause(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			preview, _ := io.ReadAll(io.LimitReader(response.Body, 512))
			response.Body.Close()
			u.last = time.Now()
			return nil, &upstreamHTTPError{status: response.StatusCode, url: target + errorPreview(preview)}
		}
		if response.ContentLength > limit {
			response.Body.Close()
			u.last = time.Now()
			return nil, fmt.Errorf("上游响应超过 %d 字节限制", limit)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		response.Body.Close()
		u.last = time.Now()
		if readErr != nil {
			return nil, readErr
		}
		if int64(len(body)) > limit {
			return nil, fmt.Errorf("上游响应超过 %d 字节限制", limit)
		}
		return body, nil
	}
	return nil, lastErr
}

func errorPreview(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 256 {
		text = text[:256]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		text += "…"
	}
	return ": " + text
}

func isRetryable(status int) bool {
	return status == 429 || status == 500 || status == 502 || status == 503 || status == 504
}

func retryDelay(response *http.Response, attempt int) time.Duration {
	value := strings.TrimSpace(response.Header.Get("Retry-After"))
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return min(time.Duration(seconds)*time.Second, 60*time.Second)
	}
	if date, err := http.ParseTime(value); err == nil && time.Until(date) > 0 {
		return min(time.Until(date), 60*time.Second)
	}
	return backoff(attempt)
}

func backoff(attempt int) time.Duration {
	return min(time.Second*time.Duration(1<<attempt), 15*time.Second)
}

func pause(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func fetchText(ctx context.Context, u *Upstream, target string) (string, error) {
	body, err := u.Get(ctx, target, map[string]string{"Accept": "text/html,application/json;q=0.9,*/*;q=0.8"}, defaultBodyLimit)
	return string(body), err
}

func fetchJSON(ctx context.Context, u *Upstream, target string, destination any) error {
	body, err := u.Get(ctx, target, map[string]string{"Accept": "application/json"}, defaultBodyLimit)
	if err != nil {
		return err
	}
	if err := jsonUnmarshal(body, destination); err != nil {
		return fmt.Errorf("解析上游 JSON 失败：%w", err)
	}
	return nil
}

func urlQuery(base string, values url.Values) string {
	if strings.Contains(base, "?") {
		return base + "&" + values.Encode()
	}
	return base + "?" + values.Encode()
}
