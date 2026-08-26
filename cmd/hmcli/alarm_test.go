// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ─── alarm disarm ─────────────────────────────────────────────────────────────

func TestAlarmDisarmPrintsOkOnSuccess(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/alarm/zones/1/disarm": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"alarm", "disarm", "--host", ts.URL, "--zone", "1"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ok") {
		t.Errorf("expected 'ok' in stdout, got: %q", stdout.String())
	}
}

// TestAlarmDisarmWithJSONFlagEmitsParseableObject is the regression guard for
// the --json flag being silently dropped on write/action commands: without
// the fix, --json still printed the bare literal "ok", which a script piping
// into jq cannot parse as an object.
func TestAlarmDisarmWithJSONFlagEmitsParseableObject(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/alarm/zones/1/disarm": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"alarm", "disarm", "--host", ts.URL, "--json", "--zone", "1"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("--json output did not parse as JSON: %v (got %q)", err, stdout.String())
	}
	if got["status"] != "ok" {
		t.Errorf("status=%v, want ok", got["status"])
	}
	if got["zone"] != "1" {
		t.Errorf("zone=%v, want 1", got["zone"])
	}
}
