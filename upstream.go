package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const defaultBodyLimit int64 = 16 << 20

var nonPublicIPPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("3fff::/20"),
}

type Upstream struct {
	client *http.Client
	mu     sync.Mutex
	last   time.Time
	delay  time.Duration
	paused error
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

func (err *upstreamHTTPError) PauseBatch() bool {
	return err.status == http.StatusForbidden || err.status == http.StatusTooManyRequests
}

type upstreamRequestError struct {
	target string
	err    error
}

func (err *upstreamRequestError) Error() string {
	return fmt.Sprintf("上游请求失败（%s）", err.target)
}

func (err *upstreamRequestError) Unwrap() error { return err.err }

type upstreamTargetHostKey struct{}

func isBatchPauseError(err error) bool {
	var pause interface{ PauseBatch() bool }
	return errors.As(err, &pause) && pause.PauseBatch()
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
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: 25 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		targetHost, _ := ctx.Value(upstreamTargetHostKey{}).(string)
		dialHost, port, err := net.SplitHostPort(address)
		if err != nil || targetHost == "" || !sameHostname(targetHost, dialHost) {
			return dialer.DialContext(ctx, network, address)
		}
		return dialPublicHost(ctx, dialer, network, port, targetHost)
	}
	client := &http.Client{Transport: transport, Timeout: 25 * time.Second}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("上游重定向次数过多")
		}
		if err := validatePublicURL(request.Context(), request.URL); err != nil {
			return fmt.Errorf("上游重定向目标不安全：%w", err)
		}
		*request = *request.WithContext(context.WithValue(request.Context(), upstreamTargetHostKey{}, request.URL.Hostname()))
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
	if u.paused != nil {
		return nil, u.paused
	}
	targetURL, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("解析上游 URL 失败")
	}
	if err := validatePublicURL(ctx, targetURL); err != nil {
		return nil, fmt.Errorf("上游地址校验失败（%s）：%w", safeURLForLog(targetURL.String()), err)
	}
	target = targetURL.String()
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
		requestContext := context.WithValue(ctx, upstreamTargetHostKey{}, targetURL.Hostname())
		request, err := http.NewRequestWithContext(requestContext, http.MethodGet, target, nil)
		if err != nil {
			return nil, fmt.Errorf("构造上游请求失败（%s）", safeURLForLog(target))
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
			return nil, &upstreamRequestError{target: safeURLForLog(target), err: err}
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
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 512))
			response.Body.Close()
			u.last = time.Now()
			httpErr := &upstreamHTTPError{status: response.StatusCode, url: safeURLForLog(target)}
			if httpErr.PauseBatch() {
				u.paused = httpErr
			}
			return nil, httpErr
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

func (u *Upstream) PauseError() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.paused
}

func (u *Upstream) ResetPause() {
	u.mu.Lock()
	u.paused = nil
	u.mu.Unlock()
}

func validatePublicURL(ctx context.Context, target *url.URL) error {
	if target == nil || !strings.EqualFold(target.Scheme, "https") || target.Hostname() == "" || target.User != nil || !isPublicHost(target.Hostname()) {
		return fmt.Errorf("上游地址必须是无用户信息的公网 HTTPS URL")
	}
	_, err := lookupPublicIPs(ctx, target.Hostname())
	return err
}

func lookupPublicIPs(ctx context.Context, host string) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		if !isPublicIP(address) {
			return nil, fmt.Errorf("目标地址不是公网 IP")
		}
		return []netip.Addr{address.Unmap()}, nil
	}
	addresses, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("解析公网主机名失败")
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("公网主机名没有 IP 地址")
	}
	result := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		parsed, ok := netip.AddrFromSlice(address)
		if !ok || !isPublicIP(parsed) {
			return nil, fmt.Errorf("主机名解析到了非公网 IP")
		}
		result = append(result, parsed.Unmap())
	}
	return result, nil
}

func isPublicIP(address netip.Addr) bool {
	if !address.IsValid() || address.Zone() != "" {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicIPPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func dialPublicHost(ctx context.Context, dialer *net.Dialer, network, port, host string) (net.Conn, error) {
	addresses, err := lookupPublicIPs(ctx, host)
	if err != nil {
		return nil, err
	}
	var failures []error
	for _, address := range addresses {
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
		if err == nil {
			return connection, nil
		}
		failures = append(failures, err)
	}
	return nil, errors.Join(failures...)
}

func sameHostname(left, right string) bool {
	return strings.EqualFold(strings.TrimSuffix(left, "."), strings.TrimSuffix(right, "."))
}

func safeURLForLog(value string) string {
	target, err := url.Parse(value)
	if err != nil {
		return "<无效 URL>"
	}
	target.User = nil
	target.RawQuery = ""
	target.ForceQuery = false
	target.Fragment = ""
	target.RawFragment = ""
	return target.String()
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
		if seconds >= int((60*time.Second)/time.Second) {
			return 60 * time.Second
		}
		return time.Duration(seconds) * time.Second
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
