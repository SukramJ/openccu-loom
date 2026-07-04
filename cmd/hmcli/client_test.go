// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func newTestClient(t *testing.T, token, user, password string, handler http.HandlerFunc) (*daemonClient, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(handler)
	// Disable keep-alives so a connection is never reused across the parallel
	// tests' server teardown, which otherwise races into a "connection broken:
	// CloseIdleConnections" error on the next request over the reused socket.
	ts.Config.SetKeepAlivesEnabled(false)
	t.Cleanup(ts.Close)
	c, err := newDaemonClient(clientConfig{
		baseURL: ts.URL, token: token, user: user, password: password, timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("newDaemonClient: %v", err)
	}
	c.http.Transport = &http.Transport{DisableKeepAlives: true}
	return c, ts
}

// ─── auth ──────────────────────────────────────────────────────────────────────

func TestDaemonClientBearerTokenHeader(t *testing.T) {
	t.Parallel()
	var gotAuth string
	c, _ := newTestClient(t, "my-token", "", "", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	var out map[string]any
	if err := c.getJSON(context.Background(), "/test", &out); err != nil {
		t.Fatalf("getJSON: %v", err)
	}
	if gotAuth != "Bearer my-token" {
		t.Errorf("Authorization=%q, want 'Bearer my-token'", gotAuth)
	}
}

func TestDaemonClientBasicAuth(t *testing.T) {
	t.Parallel()
	var gotUser, gotPass string
	c, _ := newTestClient(t, "", "alice", "s3cr3t", func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	var out map[string]any
	if err := c.getJSON(context.Background(), "/test", &out); err != nil {
		t.Fatalf("getJSON: %v", err)
	}
	if gotUser != "alice" || gotPass != "s3cr3t" {
		t.Errorf("BasicAuth user=%q pass=%q, want alice / s3cr3t", gotUser, gotPass)
	}
}

func TestDaemonClientBearerWinsOverBasicAuth(t *testing.T) {
	t.Parallel()
	var gotAuth string
	c, _ := newTestClient(t, "tok", "alice", "pass", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	var out map[string]any
	if err := c.getJSON(context.Background(), "/test", &out); err != nil {
		t.Fatalf("getJSON: %v", err)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("Authorization=%q, want Bearer prefix (token should win)", gotAuth)
	}
}

func TestDaemonClientNoAuthWhenNoCredentials(t *testing.T) {
	t.Parallel()
	var gotAuth string
	c, _ := newTestClient(t, "", "", "", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	var out map[string]any
	if err := c.getJSON(context.Background(), "/test", &out); err != nil {
		t.Fatalf("getJSON: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("expected no Authorization header, got %q", gotAuth)
	}
}

// ─── getJSON ──────────────────────────────────────────────────────────────────

func TestGetJSONDecodesResponse(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, "", "", "", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"value","num":42}`))
	})
	var out map[string]any
	if err := c.getJSON(context.Background(), "/api/v1/test", &out); err != nil {
		t.Fatalf("getJSON: %v", err)
	}
	if out["key"] != "value" {
		t.Errorf("key=%v, want value", out["key"])
	}
}

func TestGetJSONSendsCorrectPath(t *testing.T) {
	t.Parallel()
	var gotPath string
	c, _ := newTestClient(t, "", "", "", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	var out map[string]any
	if err := c.getJSON(context.Background(), "/api/v1/devices", &out); err != nil {
		t.Fatalf("getJSON: %v", err)
	}
	if gotPath != "/api/v1/devices" {
		t.Errorf("path=%q, want /api/v1/devices", gotPath)
	}
}

func TestGetJSONNon2xxReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, "", "", "", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found here", http.StatusNotFound)
	})
	var out map[string]any
	err := c.getJSON(context.Background(), "/missing", &out)
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should contain '404', got: %v", err)
	}
	if !strings.Contains(err.Error(), "not found here") {
		t.Errorf("error should contain response body, got: %v", err)
	}
}

func TestGetJSON500IncludesStatusInError(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, "", "", "", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal boom", http.StatusInternalServerError)
	})
	var out map[string]any
	err := c.getJSON(context.Background(), "/crash", &out)
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should contain '500', got: %v", err)
	}
}

// ─── sendJSON ─────────────────────────────────────────────────────────────────

func TestSendJSONPutsCorrectBody(t *testing.T) {
	t.Parallel()
	type payload struct {
		Value int `json:"value"`
	}
	var gotBody payload
	c, _ := newTestClient(t, "", "", "", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method=%s, want PUT", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.sendJSON(context.Background(), http.MethodPut, "/api/v1/thing", payload{Value: 7}, nil); err != nil {
		t.Fatalf("sendJSON: %v", err)
	}
	if gotBody.Value != 7 {
		t.Errorf("value=%d, want 7", gotBody.Value)
	}
}

func TestSendJSONSetsContentType(t *testing.T) {
	t.Parallel()
	var gotCT string
	c, _ := newTestClient(t, "", "", "", func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusNoContent)
	})
	body := map[string]string{"x": "y"}
	if err := c.sendJSON(context.Background(), http.MethodPost, "/foo", body, nil); err != nil {
		t.Fatalf("sendJSON: %v", err)
	}
	if !strings.Contains(gotCT, "application/json") {
		t.Errorf("Content-Type=%q, want application/json", gotCT)
	}
}

func TestSendJSONDecodesResponseIntoOut(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, "", "", "", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"done"}`))
	})
	var out map[string]string
	if err := c.sendJSON(context.Background(), http.MethodPost, "/action", map[string]int{"n": 1}, &out); err != nil {
		t.Fatalf("sendJSON: %v", err)
	}
	if out["result"] != "done" {
		t.Errorf("result=%q, want done", out["result"])
	}
}

func TestSendJSONNon2xxReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, "", "", "", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "conflict", http.StatusConflict)
	})
	err := c.sendJSON(context.Background(), http.MethodPut, "/conflict", map[string]int{}, nil)
	if err == nil {
		t.Fatal("expected error on 409")
	}
	if !strings.Contains(err.Error(), "409") {
		t.Errorf("error should contain '409', got: %v", err)
	}
}

func TestSendJSONNilBodySendsNoContent(t *testing.T) {
	t.Parallel()
	var gotLen int64
	c, _ := newTestClient(t, "", "", "", func(w http.ResponseWriter, r *http.Request) {
		gotLen = r.ContentLength
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.sendJSON(context.Background(), http.MethodDelete, "/thing", nil, nil); err != nil {
		t.Fatalf("sendJSON: %v", err)
	}
	// No body means Content-Length should be 0 or -1 (unset).
	if gotLen > 0 {
		t.Errorf("ContentLength=%d, want ≤0 for nil body", gotLen)
	}
}

func TestSendJSONAppliesBearerToken(t *testing.T) {
	t.Parallel()
	var gotAuth string
	c, _ := newTestClient(t, "bearer-xyz", "", "", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.sendJSON(context.Background(), http.MethodPut, "/secure", map[string]int{}, nil); err != nil {
		t.Fatalf("sendJSON: %v", err)
	}
	if gotAuth != "Bearer bearer-xyz" {
		t.Errorf("Authorization=%q, want 'Bearer bearer-xyz'", gotAuth)
	}
}

// ─── baseURL trimming ─────────────────────────────────────────────────────────

func TestNewDaemonClientTrimsTrailingSlash(t *testing.T) {
	t.Parallel()
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	c, err := newDaemonClient(clientConfig{baseURL: ts.URL + "/", timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("newDaemonClient: %v", err)
	}
	var out map[string]any
	if err := c.getJSON(context.Background(), "/api/v1/foo", &out); err != nil {
		t.Fatalf("getJSON: %v", err)
	}
	if gotPath != "/api/v1/foo" {
		t.Errorf("path=%q, expected trailing slash stripped so path is /api/v1/foo", gotPath)
	}
}
