// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestHTTPStatusErrorIncludesBody(t *testing.T) {
	t.Parallel()
	resp := &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found"}
	err := newHTTPStatusError(http.MethodGet, "http://x/y", resp, []byte("  device not found  \n"))
	got := err.Error()
	if !strings.Contains(got, "GET http://x/y") {
		t.Errorf("error %q missing method+URL", got)
	}
	if !strings.Contains(got, "404") {
		t.Errorf("error %q missing status", got)
	}
	if !strings.Contains(got, "device not found") {
		t.Errorf("error %q missing trimmed body", got)
	}
	if strings.Contains(got, "  device not found  \n") {
		t.Errorf("error %q should have the body whitespace trimmed", got)
	}
}

func TestHTTPStatusErrorOmitsEmptyBody(t *testing.T) {
	t.Parallel()
	resp := &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error"}
	err := newHTTPStatusError(http.MethodPost, "http://x/y", resp, nil)
	got := err.Error()
	if strings.HasSuffix(got, ": ") {
		t.Errorf("error %q should not have a trailing empty-body separator", got)
	}
	if !strings.Contains(got, "500") {
		t.Errorf("error %q missing status", got)
	}
}

func TestHTTPStatusErrorExposesStatusCode(t *testing.T) {
	t.Parallel()
	resp := &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden"}
	err := newHTTPStatusError(http.MethodGet, "http://x", resp, nil)
	if err.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, http.StatusForbidden)
	}
}
