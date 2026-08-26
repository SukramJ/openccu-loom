// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── sysvar list ──────────────────────────────────────────────────────────────

func TestSysvarListCallsCorrectEndpoint(t *testing.T) {
	t.Parallel()
	var gotPath string
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			writeJSON200(w, []sysvarSummary{
				{Name: "PRESENCE", ValueType: "bool", Value: true},
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "list", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotPath != "/api/v1/sysvars" {
		t.Errorf("path=%q, want /api/v1/sysvars", gotPath)
	}
}

func TestSysvarListPrintsTableHeader(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, []sysvarSummary{
				{Name: "PRESENCE", ValueType: "bool", Value: true},
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "list", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"NAME", "TYPE", "VALUE"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing column header %q:\n%s", want, out)
		}
	}
}

func TestSysvarListShowsCentralColumnWhenMultipleCentrals(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, []sysvarSummary{
				{Name: "SV1", ValueType: "bool", Value: true, Central: "ccu1"},
				{Name: "SV2", ValueType: "bool", Value: false, Central: "ccu2"},
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "list", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "CENTRAL") {
		t.Errorf("expected CENTRAL column for multi-central result:\n%s", stdout.String())
	}
}

func TestSysvarListOmitsCentralColumnWhenSingleCentral(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, []sysvarSummary{
				{Name: "SV1", ValueType: "bool", Value: true, Central: "ccu1"},
				{Name: "SV2", ValueType: "bool", Value: false, Central: "ccu1"},
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "list", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(stdout.String(), "CENTRAL") {
		t.Errorf("did not expect CENTRAL column for single-central result:\n%s", stdout.String())
	}
}

func TestSysvarListJSONFlagEmitsRawJSON(t *testing.T) {
	t.Parallel()
	item := sysvarSummary{Name: "PRESENCE", ValueType: "bool", Value: true}
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, []sysvarSummary{item})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "list", "--host", ts.URL, "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	var got []sysvarSummary
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v\noutput: %s", err, stdout.String())
	}
	if len(got) != 1 || got[0].Name != "PRESENCE" {
		t.Errorf("unexpected JSON output: %+v", got)
	}
}

func TestSysvarListNon2xxReturnsError(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		},
	})
	var stdout, stderr bytes.Buffer
	err := run([]string{"sysvar", "list", "--host", ts.URL}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
}

// ─── sysvar get ───────────────────────────────────────────────────────────────

func TestSysvarGetCallsCorrectEndpoint(t *testing.T) {
	t.Parallel()
	var gotPath string
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars/MYVAR": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			writeJSON200(w, sysvarSummary{Name: "MYVAR", ValueType: "string", Value: "hello"})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "get", "--host", ts.URL, "MYVAR"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotPath != "/api/v1/sysvars/MYVAR" {
		t.Errorf("path=%q, want /api/v1/sysvars/MYVAR", gotPath)
	}
}

// TestSysvarGetWithCentralFlagAppendsQueryParam is the regression guard for
// `sysvar get` having no way to disambiguate a name that exists on more than
// one CCU: the daemon's GetSysvar handler answers 400 "central required
// (multiple CCUs)" without a `?central=` query parameter, and until this
// fix the CLI had no flag to supply one.
func TestSysvarGetWithCentralFlagAppendsQueryParam(t *testing.T) {
	t.Parallel()
	var gotQuery string
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars/MYVAR": func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			writeJSON200(w, sysvarSummary{Name: "MYVAR", ValueType: "string", Value: "hello"})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "get", "--host", ts.URL, "--central", "ccu-attic", "MYVAR"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotQuery != "central=ccu-attic" {
		t.Errorf("query=%q, want central=ccu-attic", gotQuery)
	}
}

func TestSysvarGetPrintsFields(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars/TEMPERATURE": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, sysvarSummary{Name: "TEMPERATURE", ValueType: "float", Value: 21.5, Unit: "°C"})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "get", "--host", ts.URL, "TEMPERATURE"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"TEMPERATURE", "float", "21.5"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSysvarGetMissingNameReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"sysvar", "get", "--host", "http://localhost:1"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when name is missing")
	}
}

func TestSysvarGetJSONFlagEmitsRawJSON(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars/SV": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, sysvarSummary{Name: "SV", ValueType: "bool", Value: true})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "get", "--host", ts.URL, "--json", "SV"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	var got sysvarSummary
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON: %v\noutput: %s", err, stdout.String())
	}
	if got.Name != "SV" {
		t.Errorf("name=%q, want SV", got.Name)
	}
}

// ─── sysvar set ───────────────────────────────────────────────────────────────

func TestSysvarSetCallsCorrectEndpointAndMethod(t *testing.T) {
	t.Parallel()
	var gotPath, gotMethod string
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars/MYVAR": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotMethod = r.Method
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "set", "--host", ts.URL, "MYVAR", "true"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotPath != "/api/v1/sysvars/MYVAR" {
		t.Errorf("path=%q, want /api/v1/sysvars/MYVAR", gotPath)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method=%q, want PUT", gotMethod)
	}
}

// TestSysvarSetWithCentralFlagAppendsQueryParam mirrors
// TestSysvarGetWithCentralFlagAppendsQueryParam for the write path: the
// daemon's PutSysvar handler is gated by requireMutationHub, which answers
// 400 "central required (multiple CCUs)" without `?central=`.
func TestSysvarSetWithCentralFlagAppendsQueryParam(t *testing.T) {
	t.Parallel()
	var gotQuery string
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars/MYVAR": func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "set", "--host", ts.URL, "--central", "ccu-basement", "MYVAR", "true"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotQuery != "central=ccu-basement" {
		t.Errorf("query=%q, want central=ccu-basement", gotQuery)
	}
}

func TestSysvarSetSendsCoercedValue(t *testing.T) {
	t.Parallel()
	var gotBody map[string]any
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars/FLAG": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "set", "--host", ts.URL, "FLAG", "true"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotBody["value"] != true {
		t.Errorf("value=%v (type %T), want true (bool)", gotBody["value"], gotBody["value"])
	}
}

func TestSysvarSetPrintsOkOnSuccess(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars/SV": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "set", "--host", ts.URL, "SV", "42"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "ok") {
		t.Errorf("expected 'ok' in stdout, got: %q", stdout.String())
	}
}

func TestSysvarSetWithJSONFlagEmitsParseableObject(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars/SV": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "set", "--host", ts.URL, "--json", "SV", "42"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("--json output did not parse as JSON: %v (got %q)", err, stdout.String())
	}
	if got["status"] != "ok" {
		t.Errorf("status=%v, want ok", got["status"])
	}
}

func TestSysvarSetMissingArgsReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"sysvar", "set", "--host", "http://localhost:1", "ONLYNAME"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when value argument is missing")
	}
}

// ─── sysvar fetch ─────────────────────────────────────────────────────────────

func TestSysvarFetchCallsCorrectEndpoint(t *testing.T) {
	t.Parallel()
	var gotPath, gotMethod string
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars/fetch": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotMethod = r.Method
			w.WriteHeader(http.StatusAccepted)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "fetch", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotPath != "/api/v1/sysvars/fetch" {
		t.Errorf("path=%q, want /api/v1/sysvars/fetch", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method=%q, want POST", gotMethod)
	}
}

func TestSysvarFetchWithCentralQueryParam(t *testing.T) {
	t.Parallel()
	var gotRawQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(ts.Close)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "fetch", "--host", ts.URL, "--central", "ccu1"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(gotRawQuery, "central=ccu1") {
		t.Errorf("query=%q, want central=ccu1", gotRawQuery)
	}
}

func TestSysvarFetchNon2xxReturnsError(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars/fetch": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	})
	var stdout, stderr bytes.Buffer
	err := run([]string{"sysvar", "fetch", "--host", ts.URL}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on 502 response")
	}
}

// ─── routing ──────────────────────────────────────────────────────────────────

func TestSysvarMissingOperationReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"sysvar"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when sysvar has no operation")
	}
	if !strings.Contains(err.Error(), "missing operation") {
		t.Errorf("error=%v, want 'missing operation'", err)
	}
}

func TestSysvarUnknownOperationReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"sysvar", "frobnicate"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown sysvar operation")
	}
}
