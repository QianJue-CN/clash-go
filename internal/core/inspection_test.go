package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInspectionAPI(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer inspection-secret" {
			t.Error("missing auth")
		}
		requests = append(requests, r.Method+" "+r.URL.EscapedPath())
		switch r.URL.Path {
		case "/configs":
			fmt.Fprint(w, `{"mode":"rule"}`)
		case "/proxies":
			fmt.Fprint(w, `{"proxies":{"组/A":{"type":"Selector","now":"节点 B","all":["节点 B"]}}}`)
		case "/connections":
			if r.Method == "DELETE" {
				w.WriteHeader(204)
				return
			}
			fmt.Fprint(w, `{"uploadTotal":100,"downloadTotal":500,"connections":[{"id":"c/1","start":"2025-01-01T00:00:00Z","upload":50,"download":400,"chains":["组/A","节点 B"],"rule":"Domain","rulePayload":"example.com","metadata":{"network":"tcp","sourcePort":"1000","destinationPort":"443","host":"example.com","processPath":"/usr/bin/app"}}]}`)
		case "/rules":
			fmt.Fprint(w, `{"rules":[{"type":"Domain","payload":"example.com","proxy":"组/A"}]}`)
		case "/providers/proxies":
			fmt.Fprint(w, `{"providers":{"订阅/A":{"vehicleType":"HTTP","proxies":[{"name":"节点 B"}]}}}`)
		case "/providers/rules":
			fmt.Fprint(w, `{"providers":{"规则/B":{"vehicleType":"HTTP","ruleCount":123}}}`)
		case "/proxies/节点 B/1/delay":
			if r.URL.Query().Get("url") != "https://example.com/test?a=1&b=2" || r.URL.Query().Get("timeout") != "5000" {
				t.Error("incorrect delay query")
			}
			fmt.Fprint(w, `{"delay":42}`)
		case "/dns/query":
			if r.URL.Query().Get("name") != "example.com" || r.URL.Query().Get("type") != "AAAA" {
				t.Error("incorrect DNS query")
			}
			fmt.Fprint(w, `{"Status":0,"Answer":[{"data":"::1","TTL":60}]}`)
		default:
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	m := &Manager{api: &apiClient{base: server.URL, secret: "inspection-secret", http: directHTTP(time.Second)}}
	defer m.api.http.CloseIdleConnections()
	ctx := context.Background()
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if m.state.Connections != 1 || m.state.ConnectionDetails[0].Metadata.Host != "example.com" || m.state.UploadRate != 0 {
		t.Fatalf("bad connections: %+v", m.state)
	}
	m.state.SampledAt = time.Now().Add(-time.Second)
	m.state.Upload, m.state.Download = 0, 0
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if m.state.UploadRate <= 0 || m.state.DownloadRate <= 0 {
		t.Fatal("rates not sampled")
	}
	for _, page := range []string{"rules", "providers"} {
		if err := m.RefreshPage(ctx, page); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.state.Rules) != 1 || len(m.state.Providers) != 2 || m.state.Providers[1].RuleCount != 123 {
		t.Fatal("missing rule/provider data")
	}
	if err := m.CloseConnection(ctx, "c/1"); err != nil {
		t.Fatal(err)
	}
	if err := m.CloseConnections(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateProvider(ctx, "proxies", "订阅/A", false); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateProvider(ctx, "rules", "规则/B", false); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateProvider(ctx, "proxies", "订阅/A", true); err != nil {
		t.Fatal(err)
	}
	delay, err := m.TestDelay(ctx, "节点 B/1", "https://example.com/test?a=1&b=2")
	if err != nil || delay != 42 {
		t.Fatalf("delay=%d err=%v", delay, err)
	}
	result, err := m.QueryDNS(ctx, "example.com", "AAAA")
	if err != nil || !strings.Contains(result, "::1") {
		t.Fatalf("dns=%s err=%v", result, err)
	}
	if err := m.FlushDNS(ctx); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(requests, "\n")
	for _, want := range []string{"DELETE /connections/c%2F1", "DELETE /connections\n", "/healthcheck", "POST /cache/dns/flush"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %s", want, joined)
		}
	}
}

func TestInspectionValidationAndStoppedCore(t *testing.T) {
	m := &Manager{}
	ctx := context.Background()
	for _, action := range []func() error{
		func() error { return m.CloseConnection(ctx, "") },
		func() error { return m.CloseConnection(ctx, "id") },
		func() error { return m.CloseConnections(ctx) },
		func() error { return m.UpdateProvider(ctx, "../configs", "x", false) },
		func() error { return m.UpdateProvider(ctx, "rules", "x", true) },
		func() error { return m.FlushDNS(ctx) },
	} {
		if action() == nil {
			t.Error("operation accepted")
		}
	}
	for _, address := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com/#secret"} {
		if _, err := m.TestDelay(ctx, "name", address); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe delay URL accepted/leaked")
		}
	}
	for _, name := range []string{"", "https://example.com", "name\nother", strings.Repeat("a", 254)} {
		if _, err := m.QueryDNS(ctx, name, "A"); err == nil {
			t.Error("invalid DNS name accepted")
		}
	}
	if _, err := m.QueryDNS(ctx, "example.com", "BAD"); err == nil {
		t.Error("invalid DNS type accepted")
	}
	m.logs.Write([]byte("old log"))
	m.ClearLogs()
	if m.logs.String() != "" {
		t.Fatal("logs not cleared")
	}
	for _, tc := range []struct {
		current, previous int64
		elapsed           time.Duration
		want              int64
	}{{1024, 0, 2 * time.Second, 512}, {1, 2, time.Second, 0}, {1, 0, 0, 0}} {
		if got := transferRate(tc.current, tc.previous, tc.elapsed); got != tc.want {
			t.Errorf("rate=%d want=%d", got, tc.want)
		}
	}
}

func TestInspectionFailureDoesNotPublishPartialState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/providers/proxies" {
			json.NewEncoder(w).Encode(map[string]any{"providers": map[string]any{"new": Provider{}}})
			return
		}
		w.WriteHeader(503)
	}))
	defer server.Close()
	m := &Manager{api: &apiClient{base: server.URL, http: directHTTP(time.Second)}, state: Snapshot{Providers: []Provider{{Name: "old"}}}}
	defer m.api.http.CloseIdleConnections()
	if err := m.RefreshPage(context.Background(), "providers"); err == nil {
		t.Fatal("failure swallowed")
	}
	if len(m.state.Providers) != 1 || m.state.Providers[0].Name != "old" {
		t.Fatal("partial result published")
	}
	if _, err := m.TestDelay(context.Background(), "proxy", "https://example.com/?token=private"); err == nil || strings.Contains(err.Error(), "token") {
		t.Fatal("delay failure missing or URL leaked")
	}
}
