package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

type FnOSClient struct {
	base, username, password, token string
	client                          *http.Client
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

func (client *FnOSClient) request(ctx context.Context, path string, method string, body io.Reader, contentType string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, method, client.base+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if client.token != "" {
		request.Header.Set("authorization", client.token)
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
		return fmt.Errorf("FnOS API %s 返回无效 JSON：%w", path, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || envelope.Code != nil && *envelope.Code != 0 {
		message := envelope.Msg
		if message == "" {
			message = envelope.Message
		}
		if message == "" {
			message = response.Status
		}
		return fmt.Errorf("FnOS API %s：%s (%d)", path, message, response.StatusCode)
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

func (client *FnOSClient) Login(ctx context.Context) error {
	if client.token != "" {
		return nil
	}
	if client.username == "" || client.password == "" {
		return fmt.Errorf("请设置 FNOS_USERNAME 和 FNOS_PASSWORD，或提供 FNOS_TOKEN")
	}
	hash := sha256.Sum256([]byte(client.password))
	body, _ := json.Marshal(map[string]string{"username": client.username, "password": hex.EncodeToString(hash[:])})
	var result struct {
		Token string `json:"token"`
	}
	if err := client.request(ctx, "/user/loginByPassword?channel=v2", http.MethodPost, bytes.NewReader(body), "application/json", &result); err != nil {
		return err
	}
	if result.Token == "" {
		return fmt.Errorf("飞牛登录响应中没有 token；请检查 FnOS 版本或登录接口兼容性")
	}
	client.token = result.Token
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

func (client *FnOSClient) SaveProfile(ctx context.Context, person FnPerson, biography, profilePath *string) error {
	if biography == nil {
		biography = &person.Biography
	}
	if profilePath == nil {
		profilePath = &person.ProfilePath
	}
	body, _ := json.Marshal(map[string]any{"guid": person.GUID, "is_official": person.IsOfficial, "name": firstNonempty(person.Name, person.OriginalName),
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
