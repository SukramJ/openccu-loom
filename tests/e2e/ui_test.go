// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
//     callback) folded onto the REST listener (`:8119`, ADR 0044).
//     Pre-auth flows plus a SPA-down diagnosis page. Tests assert each
//     page returns well-formed HTML with the expected anchors.
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
		"./assets/", // hashed asset refs are relative so they resolve against
		// the document base — works under /app/ directly and behind a HA
		// Ingress path prefix alike.
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
		t.Skipf("index.html carries no ./assets/* reference — likely a dev build:\n%s", truncate(string(body), 400))
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
// HTMX bootstrap surface (folded onto the REST listener — ADR 0044)
// ─────────────────────────────────────────────────────────────────

// TestUIRootRedirectsToSPA asserts that GET / redirects into the SPA (/app/).
// Since 0.14.0 the bootstrap shares the REST listener and the root is owned by
// the SPA; the SPA itself probes /api/v1/setup/status and renders the
// onboarding wizard on first run, so there is no server-side /setup redirect.
func TestUIRootRedirectsToSPA(t *testing.T) {
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
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		t.Fatalf("GET /: status=%d, want a 3xx redirect", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/app/" {
		t.Errorf("Location=%q, want /app/", loc)
	}
}

// TestUIDiagnosticPagesRender asserts that the server-rendered, no-JS
// diagnostic pages (/health, /about) return well-formed HTML. They are the
// SPA-down fallback; login and onboarding now live in the SPA. Templates
// evolve, so we pin only invariant anchors (the layout's <html> tag).
func TestUIDiagnosticPagesRender(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})

	pages := []struct {
		path   string
		anchor string
	}{
		{"/health", `<html`},
		{"/about", `<html`},
	}
	for _, p := range pages {
		resp, err := h.REST().HTTPClient().Get(h.UIBase() + p.path)
		if err != nil {
			t.Errorf("GET %s: %v", p.path, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status=%d", p.path, resp.StatusCode)
			continue
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("GET %s: Content-Type=%q, want text/html prefix", p.path, got)
		}
		if !strings.Contains(strings.ToLower(string(body)), strings.ToLower(p.anchor)) {
			t.Errorf("GET %s: missing anchor %q\nbody (first 200):\n%s", p.path, p.anchor, truncate(string(body), 200))
		}
	}
}

// TestUIDiagnosticStaticAssetsServed asserts that /ui/assets/app.css on the
// UI listener serves the embedded stylesheet the server-rendered diagnostic
// templates (/health, /about) link to.
func TestUIDiagnosticStaticAssetsServed(t *testing.T) {
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

// scrapeAsset returns the served URL path of the first hashed asset
// referenced from the SPA shell (e.g. "/app/assets/index-abc.js"), or the
// empty string when none is present. The built index.html references assets
// relatively ("./assets/…") so they resolve against the document base — this
// is what makes the SPA work behind a HA Ingress path prefix. The files are
// still served under /app/assets/… by the SPA mount, so map the reference
// onto that path for a direct probe.
func scrapeAsset(html string) string {
	const marker = "./assets/"
	i := strings.Index(html, marker)
	if i < 0 {
		return ""
	}
	rest := html[i+len("./"):] // drop the leading "./" → "assets/…"
	end := strings.IndexAny(rest, `"' >`)
	if end < 0 {
		return ""
	}
	return "/app/" + rest[:end]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
