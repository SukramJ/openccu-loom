// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// The UI E2E tests cover both north-bound surfaces a browser sees:
//
//   - Svelte SPA (assets/ui/, embedded as `internal/north/ui/spa_dist/`)
//     served on the REST listener under /app/ — the primary user
//     interface. Tests assert structure (index.html shape, hashed
//     assets, immutable caching, client-routing fallback).
//
//   - HTMX bootstrap (login / setup / health / about / OIDC start +
//     callback) served on the UI listener (`:8081`). Pre-auth flows
//     plus a SPA-down diagnosis page. Tests assert each page returns
//     well-formed HTML with the expected anchors.
//
// All UI tests are HTTP-only (no browser); deep DOM behaviour is
// tracked separately in the nightly Playwright job (Layer B).

// ─────────────────────────────────────────────────────────────────
// Svelte SPA (mounted on the REST listener under /app/)
// ─────────────────────────────────────────────────────────────────

// TestUISPAIndexServed asserts that GET /app/ returns the embedded
// index.html with sensible meta-data: HTML content-type, no-store
// caching (so a daemon update is picked up immediately), and a body
// that actually mentions the SPA mount root.
func TestUISPAIndexServed(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})

	resp, err := h.REST().HTTPClient().Get(h.RESTBase() + "/app/")
	if err != nil {
		t.Fatalf("GET /app/: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /app/: status=%d body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type=%q, want text/html prefix", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control=%q, want no-store", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	bodyStr := string(body)
	for _, want := range []string{
		"<!doctype html>",
		`<div id="app"></div>`,
		"/app/assets/", // hashed asset references rooted at /app/
	} {
		if !strings.Contains(strings.ToLower(bodyStr), strings.ToLower(want)) {
			t.Errorf("index.html missing %q\nbody (first 400):\n%s", want, truncate(bodyStr, 400))
		}
	}
}

// TestUISPARootRedirect asserts that GET /app (no trailing slash)
// 301-redirects to /app/. The REST router pins this redirect so
// users typing /app in the address bar end up on the SPA mount
// instead of the Chi mux's catch-all 404.
func TestUISPARootRedirect(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})

	hc := h.REST().HTTPClient()
	prev := hc.CheckRedirect
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer func() { hc.CheckRedirect = prev }()

	resp, err := hc.Get(h.RESTBase() + "/app")
	if err != nil {
		t.Fatalf("GET /app: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("GET /app: status=%d, want 301", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/app/" {
		t.Errorf("Location=%q, want /app/", loc)
	}
}

// TestUISPADeepLinkFallback asserts that GET /app/<unknown-route>
// falls back to index.html instead of 404. Hash-less SPA routing
// (`/app/devices/0001ABCD`) only works because the file server
// rewrites unknown paths to the SPA shell.
func TestUISPADeepLinkFallback(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})

	resp, err := h.REST().HTTPClient().Get(h.RESTBase() + "/app/some/deep/route-no-such-file")
	if err != nil {
		t.Fatalf("GET deep link: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deep link: status=%d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(strings.ToLower(string(body)), `<div id="app"></div>`) {
		t.Errorf("deep link: did not get index.html shell\nbody (first 200):\n%s",
			truncate(string(body), 200))
	}
}

// TestUISPAHashedAssetCacheable asserts that hashed assets under
// /app/assets/ get the long-lived immutable cache header. Combined
// with the no-store on index.html, this is the canonical SPA cache
// strategy: the shell rotates instantly, the hashed bundles cache
// forever (the hash changes on every build).
func TestUISPAHashedAssetCacheable(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})

	// First fetch index.html, scrape one hashed asset URL, then probe
	// it directly. This avoids hardcoding a hash that rotates per build.
	resp, err := h.REST().HTTPClient().Get(h.RESTBase() + "/app/")
	if err != nil {
		t.Fatalf("GET /app/: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	asset := scrapeAsset(string(body))
	if asset == "" {
		t.Skipf("index.html carries no /app/assets/* reference — likely a dev build:\n%s", truncate(string(body), 400))
	}

	resp2, err := h.REST().HTTPClient().Get(h.RESTBase() + asset)
	if err != nil {
		t.Fatalf("GET %s: %v", asset, err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status=%d, want 200", asset, resp2.StatusCode)
	}
	const wantCache = "public, max-age=31536000, immutable"
	if got := resp2.Header.Get("Cache-Control"); got != wantCache {
		t.Errorf("Cache-Control on %s = %q, want %q", asset, got, wantCache)
	}
}

// ─────────────────────────────────────────────────────────────────
// HTMX bootstrap surface (UI listener)
// ─────────────────────────────────────────────────────────────────

// TestUIHTMXRootRedirectsToHealth asserts that GET / on the UI
// listener 303-redirects to /health. The SPA-down diagnosis page is
// the canonical landing-page for the bootstrap surface.
func TestUIHTMXRootRedirectsToHealth(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})

	hc := h.REST().HTTPClient()
	prev := hc.CheckRedirect
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer func() { hc.CheckRedirect = prev }()

	resp, err := hc.Get(h.UIBase() + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /: status=%d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/health" {
		t.Errorf("Location=%q, want /health", loc)
	}
}

// TestUIHTMXPagesRender asserts that each pre-auth bootstrap page
// returns well-formed HTML. Templates evolve, so we pin only
// invariant anchors (the layout's <html> tag and a per-page anchor).
func TestUIHTMXPagesRender(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})

	pages := []struct {
		path   string
		anchor string
	}{
		{"/health", `<html`},
		{"/about", `<html`},
		{"/login", `name="username"`},
		{"/setup", `<html`},
	}
	for _, p := range pages {
		resp, err := h.REST().HTTPClient().Get(h.UIBase() + p.path)
		if err != nil {
			t.Errorf("GET %s: %v", p.path, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		// /setup is reachable only on a fresh install; once an admin
		// user exists the bootstrap redirects to /login. Either is
		// fine — we only require a well-formed HTML envelope.
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusSeeOther {
			t.Errorf("GET %s: status=%d", p.path, resp.StatusCode)
			continue
		}
		if got := resp.Header.Get("Content-Type"); resp.StatusCode == http.StatusOK &&
			!strings.HasPrefix(got, "text/html") {
			t.Errorf("GET %s: Content-Type=%q, want text/html prefix", p.path, got)
		}
		if resp.StatusCode == http.StatusOK && !strings.Contains(strings.ToLower(string(body)), strings.ToLower(p.anchor)) {
			t.Errorf("GET %s: missing anchor %q\nbody (first 200):\n%s", p.path, p.anchor, truncate(string(body), 200))
		}
	}
}

// TestUIHTMXStaticAssetsServed asserts that /ui/assets/app.css on
// the UI listener serves the embedded stylesheet the bootstrap
// templates link to. The HTMX runtime (htmx.min.js) lives in the
// source tree under assets/ but is NOT embedded today (the embed
// glob is `assets/*.css`); the bootstrap templates currently do not
// reference it. See docs/e2e-testplan.md §11.6 for the dead-file
// follow-up.
func TestUIHTMXStaticAssetsServed(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})

	resp, err := h.REST().HTTPClient().Get(h.UIBase() + "/ui/assets/app.css")
	if err != nil {
		t.Fatalf("GET /ui/assets/app.css: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ui/assets/app.css: status=%d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Errorf("Content-Type=%q, want text/css prefix", got)
	}
}

// ─────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────

// scrapeAsset returns the first /app/assets/* URL referenced from
// the SPA shell, or the empty string when none is present.
func scrapeAsset(html string) string {
	const marker = "/app/assets/"
	i := strings.Index(html, marker)
	if i < 0 {
		return ""
	}
	rest := html[i:]
	end := strings.IndexAny(rest, `"' >`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
