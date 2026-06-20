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
