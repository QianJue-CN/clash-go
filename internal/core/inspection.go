package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Connection struct {
	ID          string    `json:"id"`
	Upload      int64     `json:"upload"`
	Download    int64     `json:"download"`
	Start       time.Time `json:"start"`
	Chains      []string  `json:"chains"`
	Rule        string    `json:"rule"`
	RulePayload string    `json:"rulePayload"`
	Metadata    struct {
		Network         string `json:"network"`
		Type            string `json:"type"`
		Host            string `json:"host"`
		SourceIP        string `json:"sourceIP"`
		SourcePort      string `json:"sourcePort"`
		DestinationIP   string `json:"destinationIP"`
		DestinationPort string `json:"destinationPort"`
		Process         string `json:"process"`
		ProcessPath     string `json:"processPath"`
	} `json:"metadata"`
}

type Rule struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
	Proxy   string `json:"proxy"`
}

type Provider struct {
	Name        string    `json:"name"`
	Kind        string    `json:"-"`
	VehicleType string    `json:"vehicleType"`
	UpdatedAt   time.Time `json:"updatedAt"`
	RuleCount   int       `json:"ruleCount"`
	Proxies     []struct {
		Name string `json:"name"`
	} `json:"proxies"`
}

// RefreshPage avoids fetching potentially large rule/provider lists on every dashboard tick.
func (m *Manager) RefreshPage(ctx context.Context, page string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reap()
	if m.api == nil {
		return nil
	}
	switch page {
	case "rules":
		var result struct {
			Rules []Rule `json:"rules"`
		}
		if err := m.api.request(ctx, http.MethodGet, "/rules", nil, &result); err != nil {
			return err
		}
		m.state.Rules = result.Rules
	case "providers":
		var providers []Provider
		for _, kind := range []string{"proxies", "rules"} {
			var result struct {
				Providers map[string]Provider `json:"providers"`
			}
			if err := m.api.request(ctx, http.MethodGet, "/providers/"+kind, nil, &result); err != nil {
				return err
			}
			for name, provider := range result.Providers {
				provider.Name, provider.Kind = name, kind
				providers = append(providers, provider)
			}
		}
		sort.Slice(providers, func(i, j int) bool { return providers[i].Kind+providers[i].Name < providers[j].Kind+providers[j].Name })
		m.state.Providers = providers
	}
	return nil
}

func (m *Manager) coreRequest(ctx context.Context, method, path string, body, result any, timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reap()
	if m.api == nil {
		return fmt.Errorf("内核未运行")
	}
	// Longer operations share authentication/transport, not the polling client's 3s timeout.
	client := *m.api
	httpClient := *m.api.http
	httpClient.Timeout = timeout
	client.http = &httpClient
	return client.request(ctx, method, path, body, result)
}

func (m *Manager) CloseConnection(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("连接 ID 不能为空")
	}
	return m.coreRequest(ctx, http.MethodDelete, "/connections/"+url.PathEscape(id), nil, nil, 3*time.Second)
}

func (m *Manager) CloseConnections(ctx context.Context) error {
	return m.coreRequest(ctx, http.MethodDelete, "/connections", nil, nil, 3*time.Second)
}

func (m *Manager) TestDelay(ctx context.Context, name, address string) (int, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || name == "" {
		return 0, fmt.Errorf("请选择节点并填写不含用户信息或片段的 HTTPS 测试地址")
	}
	query := url.Values{"url": {address}, "timeout": {"5000"}}
	var result struct {
		Delay int `json:"delay"`
	}
	err = m.coreRequest(ctx, http.MethodGet, "/proxies/"+url.PathEscape(name)+"/delay?"+query.Encode(), nil, &result, 7*time.Second)
	if err != nil {
		return 0, fmt.Errorf("节点延迟测试失败（超时或不可达）；请检查内核日志")
	}
	if result.Delay <= 0 {
		return 0, fmt.Errorf("内核未返回有效延迟")
	}
	return result.Delay, nil
}

func (m *Manager) UpdateProvider(ctx context.Context, kind, name string, healthCheck bool) error {
	if (kind != "proxies" && kind != "rules") || name == "" || (healthCheck && kind != "proxies") {
		return fmt.Errorf("无效资源操作")
	}
	method, path := http.MethodPut, "/providers/"+kind+"/"+url.PathEscape(name)
	if healthCheck {
		method, path = http.MethodGet, path+"/healthcheck"
	}
	return m.coreRequest(ctx, method, path, nil, nil, 60*time.Second)
}

func (m *Manager) QueryDNS(ctx context.Context, name, kind string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 253 || strings.ContainsAny(name, " /\\\t\r\n?#") {
		return "", fmt.Errorf("请输入有效的 DNS 名称（国际化域名请使用 Punycode）")
	}
	switch kind {
	case "A", "AAAA", "CNAME", "MX", "TXT", "NS", "PTR":
	default:
		return "", fmt.Errorf("不支持的 DNS 查询类型")
	}
	var result json.RawMessage
	path := "/dns/query?" + url.Values{"name": {name}, "type": {kind}}.Encode()
	if err := m.coreRequest(ctx, http.MethodGet, path, nil, &result, 15*time.Second); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	return string(data), err
}

func (m *Manager) FlushDNS(ctx context.Context) error {
	return m.coreRequest(ctx, http.MethodPost, "/cache/dns/flush", nil, nil, 3*time.Second)
}

func (m *Manager) ClearLogs() {
	m.logs.mu.Lock()
	defer m.logs.mu.Unlock()
	m.logs.data = nil
}

func transferRate(current, previous int64, elapsed time.Duration) int64 {
	if elapsed <= 0 || current < previous {
		return 0
	}
	return int64(float64(current-previous) / elapsed.Seconds())
}
