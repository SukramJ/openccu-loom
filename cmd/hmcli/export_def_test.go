// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportDefRequiresAddress(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := cmdExportDef([]string{"-host", "http://localhost:1"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "-address is required") {
		t.Fatalf("expected -address error, got %v", err)
	}
}

func TestExportDefDownloadsToFile(t *testing.T) {
	const body = "PK\x03\x04 fake-zip-bytes"
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="HM-WDS30-T-O.zip"`)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "dev.zip")
	var stdout, stderr bytes.Buffer
	err := cmdExportDef([]string{
		"-host", srv.URL,
		"-address", "00021BE9957782",
		"-token", "secret-token",
		"-out", out,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("cmdExportDef: %v", err)
	}

	if gotPath != "/api/v1/devices/00021BE9957782/export-definition" {
		t.Errorf("server path = %q", gotPath)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("auth header = %q, want Bearer secret-token", gotAuth)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != body {
		t.Errorf("output = %q, want %q", data, body)
	}
}

func TestExportDefStdout(t *testing.T) {
	const body = "zip-payload"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	err := cmdExportDef([]string{"-host", srv.URL, "-address", "X", "-out", "-"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("cmdExportDef: %v", err)
	}
	if stdout.String() != body {
		t.Errorf("stdout = %q, want %q", stdout.String(), body)
	}
}

func TestExportDefNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "device not found", http.StatusNotFound)
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	err := cmdExportDef([]string{"-host", srv.URL, "-address", "missing"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got %v", err)
	}
}

// ─── Content-Disposition path-traversal hardening ──────────────────────────────

// sanitizeDispositionFilename must reduce any input to either "" (the
// caller then falls back to a default name) or a bare filename with no
// remaining path separator and never "." or "..". This is the unit-level
// guarantee behind TestExportDefContentDispositionCannotEscapeWorkingDirectory
// below.
func TestSanitizeDispositionFilenameNeverProducesAnUnsafeSegment(t *testing.T) {
	cases := []string{
		"",
		".",
		"..",
		"HM-WDS30-T-O.zip",
		"../../../.ssh/authorized_keys",
		"/etc/passwd",
		"/a/b/c/../../evil",
		`..\..\evil.txt`,
		"...",
		"a/b/../../../c",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := sanitizeDispositionFilename(in)
			if got == "" {
				return // outright rejection is always safe
			}
			if got == "." || got == ".." {
				t.Fatalf("sanitizeDispositionFilename(%q) = %q, must never be . or ..", in, got)
			}
			if strings.ContainsAny(got, `/\`) {
				t.Fatalf("sanitizeDispositionFilename(%q) = %q, still carries a path separator", in, got)
			}
		})
	}
}

// The canonical exploit from the audit: a "../" prefix must be stripped down
// to the trailing safe segment, not rejected into a different (also
// server-influenced) name.
func TestSanitizeDispositionFilenameStripsTraversalPrefix(t *testing.T) {
	got := sanitizeDispositionFilename("../../../.ssh/authorized_keys")
	if got != "authorized_keys" {
		t.Fatalf("sanitizeDispositionFilename(...) = %q, want %q", got, "authorized_keys")
	}
}

func TestSanitizeDispositionFilenameRejectsEmptyDotAndDotDot(t *testing.T) {
	for _, in := range []string{"", ".", ".."} {
		if got := sanitizeDispositionFilename(in); got != "" {
			t.Errorf("sanitizeDispositionFilename(%q) = %q, want empty (fall back to default)", in, got)
		}
	}
}

// End-to-end: a malicious daemon's Content-Disposition header must never
// cause cmdExportDef to write outside the operator's chosen/current
// directory, regardless of -out being left at its default.
func TestExportDefContentDispositionCannotEscapeWorkingDirectory(t *testing.T) {
	const body = "zip-bytes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="../../../../tmp/evil-hmcli-export-def-test.zip"`)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	var stdout, stderr bytes.Buffer
	if err := cmdExportDef([]string{"-host", srv.URL, "-address", "ADDR1"}, &stdout, &stderr); err != nil {
		t.Fatalf("cmdExportDef: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want exactly one file written into %s, got %d entries", dir, len(entries))
	}
	if entries[0].Name() != "evil-hmcli-export-def-test.zip" {
		t.Errorf("wrote %q, want the traversal prefix stripped to %q", entries[0].Name(), "evil-hmcli-export-def-test.zip")
	}
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != body {
		t.Errorf("output content = %q, want %q", data, body)
	}
	if _, statErr := os.Stat("/tmp/evil-hmcli-export-def-test.zip"); statErr == nil {
		_ = os.Remove("/tmp/evil-hmcli-export-def-test.zip")
		t.Fatal("file escaped into /tmp — path traversal was not prevented")
	}
}

// A Content-Disposition filename that reduces to "" (empty, ".", "..") must
// fall back to "<address>.zip", exercising the caller-side fallback path
// rather than sanitizeDispositionFilename in isolation.
func TestExportDefContentDispositionEmptyFallsBackToAddress(t *testing.T) {
	const body = "zip-bytes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename=".."`)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	var stdout, stderr bytes.Buffer
	if err := cmdExportDef([]string{"-host", srv.URL, "-address", "FALLBACK1"}, &stdout, &stderr); err != nil {
		t.Fatalf("cmdExportDef: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "FALLBACK1.zip")); err != nil {
		t.Errorf("expected fallback file FALLBACK1.zip, stat error: %v", err)
	}
}
