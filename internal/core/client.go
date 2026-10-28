package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type apiClient struct {
	base, secret string
	http         *http.Client
}

func directHTTP(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (c *apiClient) request(ctx context.Context, method, path string, body, result any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("连接内核 API 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("内核 API %s: HTTP %d", path, resp.StatusCode)
	}
	if result == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return err
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, MaxConfigSize+1))
	if err != nil {
		return err
	}
	if len(data) > MaxConfigSize {
		return fmt.Errorf("内核 API 响应过大")
	}
	return json.Unmarshal(data, result)
}

type DownloadedProfile struct {
	Name string
	Data []byte
}

// Download accepts HTTPS only, including redirects, and never logs subscription tokens.
func Download(ctx context.Context, rawURL string) (DownloadedProfile, error) {
	client := subscriptionHTTP()
	defer client.CloseIdleConnections()
	return downloadProfile(ctx, client, rawURL)
}

func subscriptionHTTP() *http.Client {
	client := directHTTP(30 * time.Second)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != "https" || req.URL.User != nil {
			return fmt.Errorf("拒绝不安全或过多的重定向")
		}
		return nil
	}
	return client
}

func downloadProfile(ctx context.Context, client *http.Client, rawURL string) (DownloadedProfile, error) {
	var result DownloadedProfile
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return result, fmt.Errorf("请输入不含用户信息或片段的 HTTPS 订阅地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return result, fmt.Errorf("无效订阅地址")
	}
	req.Header.Set("User-Agent", "ClashGo/0.1 mihomo")
	resp, err := client.Do(req)
	if err != nil {
		return result, fmt.Errorf("订阅下载失败，请检查网络、TLS 证书或地址")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("订阅服务器返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxConfigSize+1))
	if err != nil {
		return result, fmt.Errorf("读取订阅失败")
	}
	if len(data) > MaxConfigSize {
		return result, fmt.Errorf("订阅不能超过 8 MiB")
	}
	return DownloadedProfile{Name: subscriptionName(resp.Header), Data: data}, nil
}

func subscriptionName(header http.Header) string {
	title := strings.TrimSpace(header.Get("Profile-Title"))
	if encoded, ok := strings.CutPrefix(title, "base64:"); ok {
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(encoded)
		}
		if err == nil {
			title = string(data)
		} else {
			title = ""
		}
	} else if decoded, err := url.PathUnescape(title); err == nil {
		title = decoded
	}
	if title = cleanProfileName(title); title != "" {
		return title
	}
	_, params, err := mime.ParseMediaType(header.Get("Content-Disposition"))
	if err != nil {
		return ""
	}
	filename := path.Base(strings.ReplaceAll(params["filename"], `\`, "/"))
	if filename == "." || filename == "/" {
		return ""
	}
	switch strings.ToLower(path.Ext(filename)) {
	case ".yaml", ".yml":
		filename = strings.TrimSuffix(filename, path.Ext(filename))
	}
	return cleanProfileName(filename)
}

func cleanProfileName(name string) string {
	if !utf8.ValidString(name) {
		return ""
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	runes := []rune(name)
	if len(runes) > 80 {
		name = string(runes[:80])
	}
	return name
}
