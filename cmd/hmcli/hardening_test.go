// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recordingServer captures the escaped request path of the last request and
// replies with the supplied JSON body for every route. Unlike a ServeMux it
// never cleans or re-routes the path, so it observes exactly what the client
// put on the wire — the point of the path-escaping checks below.
func recordingServer(t *testing.T, body string) (server *httptest.Server, escapedPath *string) {
	t.Helper()
	var gotEscaped string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscaped = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts, &gotEscaped
}

// ─── path escaping ──────────────────────────────────────────────────────────

// A user-supplied identifier containing a slash must be percent-encoded into a
// single path segment so it cannot inject extra segments (e.g. break out of
// /devices/{addr} into /admin/...). Contrast the pre-fix code, which spliced
// the raw argument into the URL.
func TestUserArgsArePathEscaped(t *testing.T) {
	t.Parallel()
	const malicious = "evil/../../admin"
	cases := []struct {
		name string
		args []string
		body string
	}{
		{"devices get", []string{"devices", "get", malicious}, `{"address":"x"}`},
		{"sysvar get", []string{"sysvar", "get", malicious}, `{"name":"x","value_type":"STRING","value":"y"}`},
		{"program get", []string{"program", "get", malicious}, `{"id":"x","name":"y"}`},
		{"paramset get", []string{"paramset", "get", malicious, "MASTER"}, `{}`},
		{"devices get-value", []string{"devices", "get-value", malicious, "1", "STATE"}, `{"parameter":"STATE","value":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ts, gotEscaped := recordingServer(t, tc.body)
			args := append([]string{tc.args[0], tc.args[1], "--host", ts.URL}, tc.args[2:]...)
			var stdout, stderr bytes.Buffer
			if err := run(args, &stdout, &stderr); err != nil {
				t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
			}
			if strings.Contains(*gotEscaped, "evil/") {
				t.Errorf("escaped path %q still contains an un-encoded slash from the argument", *gotEscaped)
			}
			if !strings.Contains(*gotEscaped, "evil%2F") {
				t.Errorf("escaped path %q should contain the percent-encoded segment evil%%2F", *gotEscaped)
			}
		})
	}
}

// A query-value argument must be URL-encoded too (sysvar fetch --central).
func TestQueryArgsAreEscaped(t *testing.T) {
	t.Parallel()
	var gotRawQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)

	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "fetch", "--host", ts.URL, "--central", "a&b c"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	// The raw query must not contain the literal '&' or ' ' from the value; they
	// must be percent-encoded so they can't be read as extra query parameters.
	if strings.Contains(gotRawQuery, "a&b") || strings.Contains(gotRawQuery, "b c") {
		t.Errorf("raw query %q contains an un-encoded special character", gotRawQuery)
	}
	if !strings.Contains(gotRawQuery, "central=") {
		t.Errorf("raw query %q missing central parameter", gotRawQuery)
	}
}

// ─── output sanitization ────────────────────────────────────────────────────

const ansiName = "\x1b[31mALERT\x1b[0m\x07"

// assertSanitized fails when out still carries any escape/control byte from a
// server-supplied string while confirming the visible text survived.
func assertSanitized(t *testing.T, out, wantVisible string) {
	t.Helper()
	if !strings.Contains(out, wantVisible) {
		t.Errorf("output missing visible text %q; got:\n%q", wantVisible, out)
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("output still contains an ESC byte:\n%q", out)
	}
	if strings.ContainsRune(out, 0x07) {
		t.Errorf("output still contains a BEL byte:\n%q", out)
	}
	if strings.Contains(out, "[31m") {
		t.Errorf("output still contains raw CSI parameter text:\n%q", out)
	}
}

func TestDevicesListSanitizesServerStrings(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, deviceListResponse{
				Items: []deviceSummary{{Address: "AABB", Model: "HmIP-PS", Name: ansiName, Interface: "HmIP-RF"}},
				Total: 1,
			})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"devices", "list", "--host", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertSanitized(t, stdout.String(), "ALERT")
}

func TestSysvarGetSanitizesServerStrings(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/sysvars/SV": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, sysvarSummary{Name: "SV", ValueType: "STRING", Value: ansiName})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sysvar", "get", "--host", ts.URL, "SV"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertSanitized(t, stdout.String(), "ALERT")
}

func TestProgramGetSanitizesServerStrings(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/programs/P": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, programSummary{ID: "P", Name: ansiName})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"program", "get", "--host", ts.URL, "P"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertSanitized(t, stdout.String(), "ALERT")
}

func TestParamsetGetSanitizesServerStrings(t *testing.T) {
	t.Parallel()
	ts := newDevicesServer(t, map[string]http.HandlerFunc{
		"/api/v1/devices/D/paramsets/MASTER": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON200(w, map[string]any{"PARAM": ansiName})
		},
	})
	var stdout, stderr bytes.Buffer
	if err := run([]string{"paramset", "get", "--host", ts.URL, "D", "MASTER"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertSanitized(t, stdout.String(), "ALERT")
}

func TestEventsHumanOutputSanitizesServerStrings(t *testing.T) {
	t.Parallel()
	frames := [][]byte{
		mustMarshal(t, map[string]any{
			"topic": "device.X",
			"type":  ansiName,
			"ts":    "2026-07-01T10:00:00.000Z",
		}),
	}
	srv, _ := fakeEventServer(t, frames)
	var stdout, stderr bytes.Buffer
	if err := cmdEvents([]string{"tail", "--host", srv.URL, "--topics", "*"}, &stdout, &stderr); err != nil {
		t.Fatalf("cmdEvents: %v", err)
	}
	assertSanitized(t, stdout.String(), "ALERT")
}
