// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// TestCacheControlFor pins the caching policy per bundle file. It is a pure
// function so the coverage does not depend on whether spa_dist is built —
// the integration CI job embeds an empty bundle, and the asset-serving path
// that calls this in production is never reached there.
func TestCacheControlFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		path       string
		wantSubstr string
		wantNot    string
	}{
		{"content-hashed asset is immutable", "assets/index-a1b2c3.js", "immutable", ""},
		{"hashed css chunk is immutable", "assets/style-9f8e7d.css", "immutable", ""},
		{"verbatim favicon revalidates", "favicon-32.png", "must-revalidate", "immutable"},
		{"web manifest revalidates", "manifest.webmanifest", "must-revalidate", "immutable"},
		{"wordmark revalidates", "mark-loom-512.png", "must-revalidate", "immutable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cacheControlFor(tc.path)
			if !strings.Contains(got, tc.wantSubstr) {
				t.Fatalf("cacheControlFor(%q) = %q, want it to contain %q", tc.path, got, tc.wantSubstr)
			}
			if tc.wantNot != "" && strings.Contains(got, tc.wantNot) {
				t.Fatalf("cacheControlFor(%q) = %q, must not contain %q", tc.path, got, tc.wantNot)
			}
		})
	}
}

// TestServeIndex covers both branches of serveIndex against a synthetic FS,
// so neither depends on the embedded bundle being present.
func TestServeIndex(t *testing.T) {
	t.Parallel()

	t.Run("serves the built index with no-store", func(t *testing.T) {
		t.Parallel()
		body := "<!doctype html><title>loom</title>"
		fsys := fstest.MapFS{"index.html": {Data: []byte(body)}}
		rec := httptest.NewRecorder()
		serveIndex(fsys, rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("Cache-Control = %q, want no-store", cc)
		}
		if rec.Body.String() != body {
			t.Fatalf("body = %q, want the embedded index", rec.Body.String())
		}
	})

	t.Run("falls back to the not-built page when the bundle is empty", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		serveIndex(fstest.MapFS{}, rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("Cache-Control = %q, want no-store", cc)
		}
		if !strings.Contains(rec.Body.String(), "SPA bundle not built") {
			t.Fatalf("body did not carry the not-built page: %q", rec.Body.String())
		}
	})
}

// TestSPAHandlerRoutesIndexRequestsToServeIndex covers the two client-side
// entry-point branches (root and an explicit index.html) plus the
// missing-asset fallback. Every one answers through serveIndex, which always
// sets Cache-Control: no-store, so the assertion holds whether or not the
// bundle is built — the branch is exercised in the integration CI job too,
// where spa_dist embeds nothing.
func TestSPAHandlerRoutesIndexRequestsToServeIndex(t *testing.T) {
	t.Parallel()
	h := SPAHandler()
	for _, path := range []string{"/", "/index.html", "/route/does/not/resolve.js"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
			if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
				t.Fatalf("%s: Cache-Control = %q, want no-store (should fall through to serveIndex)", path, cc)
			}
		})
	}
}
