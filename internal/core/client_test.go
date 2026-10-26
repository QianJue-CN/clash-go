package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAPIAuthEscapingAndActions(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing authorization")
		}
		requests = append(requests, r.Method+" "+r.URL.EscapedPath())
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.Method == "PATCH" && body["mode"] != "global" {
			t.Error("wrong mode")
		}
		if r.Method == "PUT" && body["name"] != "节点 A" {
			t.Error("wrong node")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	m := &Manager{api: &apiClient{base: server.URL, secret: "secret", http: directHTTP(time.Second)}}
	if err := m.SetMode(context.Background(), "global"); err != nil {
		t.Fatal(err)
	}
	if err := m.Select(context.Background(), "香港/自动", "节点 A"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || !strings.Contains(requests[1], "%2F") {
		t.Fatalf("requests: %v", requests)
	}
	if err := m.SetMode(context.Background(), "unknown"); err == nil {
		t.Fatal("accepted invalid mode")
	}
}

func TestAPIDoesNotFollowRedirects(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed API redirect") }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer server.Close()
	client := apiClient{base: server.URL, secret: "secret", http: directHTTP(time.Second)}
	if err := client.request(context.Background(), "GET", "/version", nil, nil); err == nil {
		t.Fatal("accepted redirect")
	}
}

func TestDownloadURLValidation(t *testing.T) {
	for _, address := range []string{"http://example.com/secret", "https://user:pass@example.com", "file:///tmp/x", "https://", "https://example.com/#fragment"} {
		_, err := Download(context.Background(), address)
		if err == nil {
			t.Errorf("accepted %s", address)
		}
		if strings.Contains(err.Error(), address) {
			t.Error("error leaked URL")
		}
	}
}

func TestAPIBoundedAndInvalidResponses(t *testing.T) {
	for _, response := range []string{"not-json", strings.Repeat(" ", MaxConfigSize+1)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(response)) }))
		client := apiClient{base: server.URL, http: directHTTP(time.Second)}
		var value any
		if err := client.request(context.Background(), "GET", "/version", nil, &value); err == nil {
			t.Error("accepted bad response")
		}
		client.http.CloseIdleConnections()
		server.Close()
	}
}
