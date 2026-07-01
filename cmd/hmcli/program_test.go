// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// ─── program list ─────────────────────────────────────────────────────────────

func TestProgramListCallsCorrectEndpoint(t *testing.T) {
	t.Parallel()
	var gotPath string
	boolTrue := true
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/programs": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			writeJSON200(w, []programSummary{
				{ID: "P001", Name: "Nightlight", Active: &boolTrue},
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"program", "list", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotPath != "/api/v1/programs" {
		t.Errorf("path=%q, want /api/v1/programs", gotPath)
	}
}

func TestProgramListPrintsTableHeader(t *testing.T) {
	t.Parallel()
	boolTrue := true
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/programs": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, []programSummary{
				{ID: "P001", Name: "Nightlight", Active: &boolTrue},
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"program", "list", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"ID", "NAME", "ACTIVE"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing column header %q:\n%s", want, out)
		}
	}
}

func TestProgramListShowsCentralColumnWhenMultipleCentrals(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/programs": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, []programSummary{
				{ID: "P001", Name: "Prog1", Central: "ccu1"},
				{ID: "P002", Name: "Prog2", Central: "ccu2"},
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"program", "list", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "CENTRAL") {
		t.Errorf("expected CENTRAL column for multi-central result:\n%s", stdout.String())
	}
}

func TestProgramListOmitsCentralColumnWhenSingleCentral(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/programs": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, []programSummary{
				{ID: "P001", Name: "Prog1", Central: "ccu1"},
				{ID: "P002", Name: "Prog2", Central: "ccu1"},
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"program", "list", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(stdout.String(), "CENTRAL") {
		t.Errorf("did not expect CENTRAL column for single-central result:\n%s", stdout.String())
	}
}

func TestProgramListJSONFlagEmitsRawJSON(t *testing.T) {
	t.Parallel()
	boolTrue := true
	item := programSummary{ID: "P001", Name: "Nightlight", Active: &boolTrue}
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/programs": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, []programSummary{item})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"program", "list", "--host", ts.URL, "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	var got []programSummary
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v\noutput: %s", err, stdout.String())
	}
	if len(got) != 1 || got[0].ID != "P001" {
		t.Errorf("unexpected JSON output: %+v", got)
	}
}

func TestProgramListNon2xxReturnsError(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/programs": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		},
	})
	var stdout, stderr bytes.Buffer
	err := run([]string{"program", "list", "--host", ts.URL}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
}

// ─── program get ──────────────────────────────────────────────────────────────

func TestProgramGetCallsCorrectEndpoint(t *testing.T) {
	t.Parallel()
	var gotPath string
	boolTrue := true
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/programs/P001": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			writeJSON200(w, programSummary{ID: "P001", Name: "Nightlight", Active: &boolTrue})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"program", "get", "--host", ts.URL, "P001"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotPath != "/api/v1/programs/P001" {
		t.Errorf("path=%q, want /api/v1/programs/P001", gotPath)
	}
}

func TestProgramGetPrintsFields(t *testing.T) {
	t.Parallel()
	boolTrue := true
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/programs/P001": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, programSummary{ID: "P001", Name: "Nightlight", Active: &boolTrue, Central: "ccu1"})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"program", "get", "--host", ts.URL, "P001"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"P001", "Nightlight"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestProgramGetMissingIDReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"program", "get", "--host", "http://localhost:1"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when ID is missing")
	}
}

// ─── program run ──────────────────────────────────────────────────────────────

func TestProgramRunCallsCorrectEndpointAndMethod(t *testing.T) {
	t.Parallel()
	var gotPath, gotMethod string
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/programs/P001/execute": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotMethod = r.Method
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"program", "run", "--host", ts.URL, "P001"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotPath != "/api/v1/programs/P001/execute" {
		t.Errorf("path=%q, want /api/v1/programs/P001/execute", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method=%q, want POST", gotMethod)
	}
}

func TestProgramRunPrintsOkOnSuccess(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/programs/P001/execute": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"program", "run", "--host", ts.URL, "P001"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "ok") {
		t.Errorf("expected 'ok' in stdout, got: %q", stdout.String())
	}
}

func TestProgramRunMissingIDReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"program", "run", "--host", "http://localhost:1"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when ID is missing")
	}
}

// ─── program enable / disable ─────────────────────────────────────────────────

func TestProgramEnableCallsCorrectEndpointAndMethod(t *testing.T) {
	t.Parallel()
	var gotPath, gotMethod string
	var gotBody map[string]any
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/programs/P001": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotMethod = r.Method
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"program", "enable", "--host", ts.URL, "P001"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotPath != "/api/v1/programs/P001" {
		t.Errorf("path=%q, want /api/v1/programs/P001", gotPath)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method=%q, want PATCH", gotMethod)
	}
	if gotBody["active"] != true {
		t.Errorf("body active=%v, want true", gotBody["active"])
	}
}

func TestProgramDisableCallsCorrectEndpointAndMethod(t *testing.T) {
	t.Parallel()
	var gotPath, gotMethod string
	var gotBody map[string]any
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/programs/P001": func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotMethod = r.Method
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"program", "disable", "--host", ts.URL, "P001"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if gotPath != "/api/v1/programs/P001" {
		t.Errorf("path=%q, want /api/v1/programs/P001", gotPath)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method=%q, want PATCH", gotMethod)
	}
	if gotBody["active"] != false {
		t.Errorf("body active=%v, want false", gotBody["active"])
	}
}

// ─── routing ──────────────────────────────────────────────────────────────────

func TestProgramMissingOperationReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"program"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when program has no operation")
	}
	if !strings.Contains(err.Error(), "missing operation") {
		t.Errorf("error=%v, want 'missing operation'", err)
	}
}

func TestProgramUnknownOperationReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"program", "frobnicate"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown program operation")
	}
}
