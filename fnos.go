package main

import (
	"bytes"
	"context"
	"crypto/md5"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type FnOSClient struct {
	base, username, password, token string
	client                          *http.Client
	authMu                          sync.Mutex
	tokenMu                         sync.RWMutex
}

func NewFnOSClient(base, username, password, token string) *FnOSClient {
	return &FnOSClient{base: strings.TrimRight(base, "/") + "/v/api/v1", username: username, password: password, token: token,
		client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

type apiEnvelope struct {
	Code    *int            `json:"code"`
	Msg     string          `json:"msg"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type fnosAPIError struct {
	path       string
	message    string
	statusCode int
}

func (err *fnosAPIError) Error() string {
	return fmt.Sprintf("FnOS API %s：%s (%d)", err.path, err.message, err.statusCode)
}

func (client *FnOSClient) request(ctx context.Context, path string, method string, body io.Reader, contentType string, destination any) error {
	var bodyBytes []byte
	if body != nil {
		data, err := io.ReadAll(io.LimitReader(body, defaultBodyLimit+1))
		if err != nil {
			return err
		}
		if int64(len(data)) > defaultBodyLimit {
			return fmt.Errorf("FnOS API 请求体超过 16 MiB")
		}
		bodyBytes = data
	}
	hasBody := body != nil
	token := client.tokenValue()
	err := client.requestOnce(ctx, path, method, bodyBytes, hasBody, contentType, destination)
	if !isFnOSUnauthorized(err) {
		return err
	}
	if client.username == "" || client.password == "" {
		return fmt.Errorf("FnOS 会话已过期（HTTP 401）；配置 FNOS_USERNAME 和 FNOS_PASSWORD 后才能自动重新登录，当前仅配置了 token")
	}
	client.authMu.Lock()
	defer client.authMu.Unlock()
	if client.token == token {
		if err := client.loginLocked(ctx); err != nil {
			return fmt.Errorf("FnOS 会话过期后重新登录失败：%w", err)
		}
	}
	return client.requestOnce(ctx, path, method, bodyBytes, hasBody, contentType, destination)
}

func (client *FnOSClient) tokenValue() string {
	client.tokenMu.RLock()
	defer client.tokenMu.RUnlock()
	return client.token
}

func isFnOSUnauthorized(err error) bool {
	apiErr, ok := err.(*fnosAPIError)
	return ok && apiErr.statusCode == http.StatusUnauthorized
}

func (client *FnOSClient) requestOnce(ctx context.Context, path string, method string, bodyBytes []byte, hasBody bool, contentType string, destination any) error {
	if int64(len(bodyBytes)) > defaultBodyLimit {
		return fmt.Errorf("FnOS API 请求体超过 16 MiB")
	}
	var body io.Reader
	if hasBody {
		body = bytes.NewReader(bodyBytes)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.base+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Trim-Client", "web")
	request.Header.Set("X-Trim-Client-Version", "631")
	request.Header.Set("authx", fnosSignature(request, bodyBytes, contentType))
	if token := client.tokenValue(); token != "" {
		request.Header.Set("authorization", token)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.client.Do(request)
	if err != nil {
		return fmt.Errorf("FnOS API %s 请求失败：%w", path, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, defaultBodyLimit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > defaultBodyLimit {
		return fmt.Errorf("FnOS API 响应超过 16 MiB")
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		if response.StatusCode == http.StatusUnauthorized {
			return &fnosAPIError{path: path, message: "会话已过期", statusCode: response.StatusCode}
		}
		return fmt.Errorf("FnOS API %s 返回非 JSON 响应（HTTP %d，Content-Type %s）：%w；请检查 FNOS_URL、容器网络模式和 NO_PROXY", path, response.StatusCode, response.Header.Get("Content-Type"), err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || envelope.Code != nil && *envelope.Code != 0 {
		message := envelope.Msg
		if message == "" {
			message = envelope.Message
		}
		if message == "" {
			message = response.Status
		}
		return &fnosAPIError{path: path, message: message, statusCode: response.StatusCode}
	}
	payload := data
	if len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		payload = envelope.Data
	}
	if destination != nil {
		if err := json.Unmarshal(payload, destination); err != nil {
			return fmt.Errorf("解析 FnOS API %s 响应失败：%w", path, err)
		}
	}
	return nil
}

func fnosSignature(request *http.Request, body []byte, contentType string) string {
	var bodyHash string
	if request.Method == http.MethodGet {
		query := request.URL.Query()
		keys := make([]string, 0, len(query))
		for key := range query {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			values := query[key]
			if len(values) == 0 {
				continue
			}
			value := values[len(values)-1]
			if value == "undefined" || value == "null" {
				continue
			}
			parts = append(parts, key+"="+value)
		}
		bodyHash = md5Hex([]byte(strings.Join(parts, "&")))
	} else if strings.HasPrefix(contentType, "multipart/form-data") {
		// The FnOS web client hashes JSON.stringify(FormData), which is "{}".
		bodyHash = md5Hex([]byte("{}"))
	} else {
		bodyHash = md5Hex(body)
	}

	nonceNumber, err := cryptorand.Int(cryptorand.Reader, big.NewInt(900000))
	if err != nil {
		nonceNumber = big.NewInt(time.Now().UnixNano() % 900000)
	}
	nonce := strconv.FormatInt(nonceNumber.Int64()+100000, 10)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	apiKeyBytes := []byte{152, 159, 234, 234, 236, 235, 154, 237, 132, 232, 235, 157, 155, 132, 153, 158, 158, 237, 132, 154, 159, 232, 152, 132, 239, 154, 156, 156, 154, 155, 157, 236, 157, 155, 154, 158}
	for index := range apiKeyBytes {
		apiKeyBytes[index] ^= 169
	}
	signatureInput := strings.Join([]string{"NDzZTVxnRKP8Z0jXg1VAMonaG8akvh", request.URL.EscapedPath(), nonce, timestamp, bodyHash, string(apiKeyBytes)}, "_")
	return fmt.Sprintf("nonce=%s&timestamp=%s&sign=%s", nonce, timestamp, md5Hex([]byte(signatureInput)))
}

func md5Hex(data []byte) string {
	hash := md5.Sum(data)
	return hex.EncodeToString(hash[:])
}

func (client *FnOSClient) Login(ctx context.Context) error {
	client.authMu.Lock()
	defer client.authMu.Unlock()
	if client.token != "" {
		return nil
	}
	return client.loginLocked(ctx)
}

func (client *FnOSClient) loginLocked(ctx context.Context) error {
	if client.username == "" || client.password == "" {
		return fmt.Errorf("请设置 FNOS_USERNAME 和 FNOS_PASSWORD，或提供 FNOS_TOKEN")
	}
	client.tokenMu.Lock()
	client.token = ""
	client.tokenMu.Unlock()
	hash := sha256.Sum256([]byte(client.password))
	body, _ := json.Marshal(map[string]string{"username": client.username, "password": hex.EncodeToString(hash[:]), "app_name": "trimemedia-web"})
	var result struct {
		Token string `json:"token"`
	}
	err := client.requestOnce(ctx, "/user/loginByPassword?channel=v2", http.MethodPost, body, true, "application/json", &result)
	if apiErr, ok := err.(*fnosAPIError); ok && strings.EqualFold(apiErr.message, "Invalid Params") {
		// Some FnOS versions still expose only the legacy login endpoint. The web UI
		// uses this same endpoint with the original password when v2 is unavailable.
		legacyBody, _ := json.Marshal(map[string]string{"username": client.username, "password": client.password, "app_name": "trimemedia-web"})
		err = client.requestOnce(ctx, "/login", http.MethodPost, legacyBody, true, "application/json", &result)
	}
	if err != nil {
		return err
	}
	if result.Token == "" {
		return fmt.Errorf("飞牛登录响应中没有 token；请检查 FnOS 版本或登录接口兼容性")
	}
	client.tokenMu.Lock()
	client.token = result.Token
	client.tokenMu.Unlock()
	return nil
}

func (client *FnOSClient) SearchPeople(ctx context.Context, keyword string) ([]FnPerson, error) {
	body, _ := json.Marshal(map[string]any{"keyword": keyword, "page": 1, "page_size": 50})
	var result json.RawMessage
	if err := client.request(ctx, "/person/search", http.MethodPost, bytes.NewReader(body), "application/json", &result); err != nil {
		return nil, err
	}
	var people []FnPerson
	if json.Unmarshal(result, &people) == nil {
		return people, nil
	}
	var wrapper map[string]json.RawMessage
	if json.Unmarshal(result, &wrapper) != nil {
		return nil, nil
	}
	for _, key := range []string{"list", "items", "records", "persons", "data"} {
		if raw := wrapper[key]; len(raw) > 0 && json.Unmarshal(raw, &people) == nil {
			return people, nil
		}
	}
	return nil, nil
}

func (client *FnOSClient) GetEditDetail(ctx context.Context, guid string) (FnPerson, error) {
	body, _ := json.Marshal(map[string]string{"guid": guid})
	var person FnPerson
	err := client.request(ctx, "/person/getEditDetail", http.MethodPost, bytes.NewReader(body), "application/json", &person)
	return person, err
}

func (client *FnOSClient) UploadProfile(ctx context.Context, image []byte) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "profile.jpg")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(image); err != nil {
		return "", err
	}
	if err := writer.WriteField("image_type", "poster"); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	var result struct {
		HashPath string `json:"hash_path"`
	}
	if err := client.request(ctx, "/image/temp/upload", http.MethodPost, &body, writer.FormDataContentType(), &result); err != nil {
		return "", err
	}
	if result.HashPath == "" {
		return "", fmt.Errorf("飞牛图片上传响应中没有 hash_path")
	}
	return result.HashPath, nil
}

func (client *FnOSClient) SaveProfile(ctx context.Context, person FnPerson, fallbackName string, biography, profilePath *string) error {
	if biography == nil {
		biography = &person.Biography
	}
	if profilePath == nil {
		profilePath = &person.ProfilePath
	}
	body, _ := json.Marshal(map[string]any{"guid": person.GUID, "is_official": person.IsOfficial, "name": firstNonempty(person.Name, person.OriginalName, fallbackName),
		"name_locked": person.NameLocked, "biography": *biography, "biography_locked": person.BiographyLocked,
		"profile_path": *profilePath, "profile_path_locked": person.ProfilePathLocked})
	return client.request(ctx, "/person/saveEditDetail", http.MethodPost, bytes.NewReader(body), "application/json", nil)
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
