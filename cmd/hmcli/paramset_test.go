// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// ─── paramset get ─────────────────────────────────────────────────────────────

func TestParamsetGetCallsCorrectEndpoint(t *testing.T) {
	t.Parallel()
	var gotPath string
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/ADDR:1/paramsets/MASTER": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			writeJSON200(w, map[string]any{"BURST_RX": false, "CYCLIC_INFO_MSG": true})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"paramset", "get", "--host", ts.URL, "ADDR:1", "MASTER"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotPath != "/api/v1/devices/ADDR:1/paramsets/MASTER" {
		t.Errorf("path=%q, want /api/v1/devices/ADDR:1/paramsets/MASTER", gotPath)
	}
}

func TestParamsetGetPrintsKeyValueTable(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/DEV/paramsets/VALUES": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, map[string]any{"LEVEL": 0.5, "STATE": false})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"paramset", "get", "--host", ts.URL, "DEV", "VALUES"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"PARAM", "VALUE"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing column header %q:\n%s", want, out)
		}
	}
}

func TestParamsetGetJSONFlagEmitsRawJSON(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/DEV/paramsets/MASTER": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, map[string]any{"BURST_RX": false})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"paramset", "get", "--host", ts.URL, "--json", "DEV", "MASTER"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON: %v\noutput: %s", err, stdout.String())
	}
	if _, ok := got["BURST_RX"]; !ok {
		t.Errorf("expected BURST_RX key in JSON output: %+v", got)
	}
}

func TestParamsetGetInvalidKeyReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"paramset", "get", "--host", "http://localhost:1", "ADDR", "INVALID"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for invalid paramset KEY")
	}
	if !strings.Contains(err.Error(), "INVALID") {
		t.Errorf("error should mention INVALID, got: %v", err)
	}
}

func TestParamsetGetMissingArgsReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"paramset", "get", "--host", "http://localhost:1", "ADDR"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when KEY argument is missing")
	}
}

// ─── paramset set ─────────────────────────────────────────────────────────────

func TestParamsetSetCallsCorrectEndpointAndMethod(t *testing.T) {
	t.Parallel()
	var gotPath, gotMethod string
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/ADDR:1/paramsets/VALUES": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotMethod = r.Method
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"paramset", "set", "--host", ts.URL, "ADDR:1", "VALUES", "LEVEL", "0.5"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotPath != "/api/v1/devices/ADDR:1/paramsets/VALUES" {
		t.Errorf("path=%q, want /api/v1/devices/ADDR:1/paramsets/VALUES", gotPath)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method=%q, want PUT", gotMethod)
	}
}

// editSessionStubHandler answers the edit-lock session endpoint that every
// MASTER/LINK paramset set now opens and closes around the actual write.
func editSessionStubHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		writeJSON200(w, map[string]string{"token": "tok-stub"})
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unexpected method "+r.Method, http.StatusMethodNotAllowed)
	}
}

func TestParamsetSetSendsParamAsMapKey(t *testing.T) {
	t.Parallel()
	var gotBody map[string]any
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sessions/edit": editSessionStubHandler,
		"/api/v1/devices/DEV/paramsets/MASTER": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"paramset", "set", "--host", ts.URL, "DEV", "MASTER", "MYPARAM", "42"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, ok := gotBody["MYPARAM"]; !ok {
		t.Errorf("expected MYPARAM key in body, got: %+v", gotBody)
	}
}

func TestParamsetSetPrintsOkOnSuccess(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sessions/edit": editSessionStubHandler,
		"/api/v1/devices/DEV/paramsets/MASTER": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"paramset", "set", "--host", ts.URL, "DEV", "MASTER", "PARAM", "val"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "ok") {
		t.Errorf("expected 'ok' in stdout, got: %q", stdout.String())
	}
}

func TestParamsetSetMissingArgsReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"paramset", "set", "--host", "http://localhost:1", "ADDR", "MASTER", "PARAM"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when value argument is missing")
	}
}

func TestParamsetSetNon2xxReturnsError(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/DEV/paramsets/VALUES": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		},
	})
	var stdout, stderr bytes.Buffer
	err := run([]string{"paramset", "set", "--host", ts.URL, "DEV", "VALUES", "LEVEL", "1"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should mention 403, got: %v", err)
	}
}

// ─── routing ──────────────────────────────────────────────────────────────────

func TestParamsetMissingOperationReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"paramset"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when paramset has no operation")
	}
	if !strings.Contains(err.Error(), "missing operation") {
		t.Errorf("error=%v, want 'missing operation'", err)
	}
}

func TestParamsetUnknownOperationReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"paramset", "frobnicate"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown paramset operation")
	}
}

// ─── paramset set: MASTER/LINK edit-lock plumbing ─────────────────────────────

// requestRecord captures the method + path of one request seen by the fake
// daemon server, in arrival order.
type requestRecord struct {
	method string
	path   string
}

func TestCmdParamsetSet_Master_OpensEditSession(t *testing.T) {
	t.Parallel()
	var (
		mu       sync.Mutex
		requests []requestRecord
		putToken string
		putBody  map[string]any
	)
	record := func(r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, requestRecord{method: r.Method, path: r.URL.Path})
	}

	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sessions/edit": func(w http.ResponseWriter, r *http.Request) {
			record(r)
			switch r.Method {
			case http.MethodPost:
				writeJSON200(w, map[string]string{"token": "tok-123"})
			case http.MethodDelete:
				w.WriteHeader(http.StatusNoContent)
			default:
				http.Error(w, "unexpected method "+r.Method, http.StatusMethodNotAllowed)
			}
		},
		"/api/v1/devices/DEV001:1/paramsets/MASTER": func(w http.ResponseWriter, r *http.Request) {
			record(r)
			mu.Lock()
			putToken = r.Header.Get(editTokenHeader)
			mu.Unlock()
			raw, _ := io.ReadAll(r.Body)
			var decoded map[string]any
			_ = json.Unmarshal(raw, &decoded)
			mu.Lock()
			putBody = decoded
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		},
	})

	var stdout, stderr bytes.Buffer
	if err := run([]string{"paramset", "set", "--host", ts.URL, "DEV001:1", "MASTER", "TEMPERATURE", "21"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}

	mu.Lock()
	got := append([]requestRecord(nil), requests...)
	token := putToken
	body := putBody
	mu.Unlock()

	want := []requestRecord{
		{method: http.MethodPost, path: "/api/v1/sessions/edit"},
		{method: http.MethodPut, path: "/api/v1/devices/DEV001:1/paramsets/MASTER"},
		{method: http.MethodDelete, path: "/api/v1/sessions/edit"},
	}
	if len(got) != len(want) {
		t.Fatalf("requests=%+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request[%d]=%+v, want %+v", i, got[i], want[i])
		}
	}

	if token != "tok-123" {
		t.Errorf("X-Edit-Token on PUT=%q, want tok-123", token)
	}
	if _, ok := body["TEMPERATURE"]; !ok {
		t.Errorf("expected TEMPERATURE key in PUT body, got: %+v", body)
	}
	if !strings.Contains(stdout.String(), "ok") {
		t.Errorf("expected 'ok' in stdout, got: %q", stdout.String())
	}
}

func TestCmdParamsetSet_Values_NoEditSession(t *testing.T) {
	t.Parallel()
	var (
		mu            sync.Mutex
		sessionCalled bool
		putToken      string
		putTokenSeen  bool
	)

	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sessions/edit": func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			sessionCalled = true
			mu.Unlock()
			http.Error(w, "unexpected edit-session request for VALUES", http.StatusInternalServerError)
		},
		"/api/v1/devices/DEV001:1/paramsets/VALUES": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			putToken = r.Header.Get(editTokenHeader)
			putTokenSeen = true
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		},
	})

	var stdout, stderr bytes.Buffer
	if err := run([]string{"paramset", "set", "--host", ts.URL, "DEV001:1", "VALUES", "STATE", "true"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if sessionCalled {
		t.Error("VALUES set must not open an edit session")
	}
	if !putTokenSeen {
		t.Fatal("expected the PUT .../paramsets/VALUES handler to be invoked")
	}
	if putToken != "" {
		t.Errorf("X-Edit-Token=%q, want empty header for a VALUES set", putToken)
	}
}
