// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/i18n"
)

func newUIRouter(t *testing.T) http.Handler {
	t.Helper()
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("catalogs: %v", err)
	}
	tracker := health.NewTracker()
	tracker.Record("central", health.Sample{Healthy: true})
	return NewRouter(Deps{
		Lang:     "de",
		Catalogs: cats,
		Health:   tracker,
	})
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestUIRootRedirectsToHealth(t *testing.T) {
	rr := get(t, newUIRouter(t), "/")
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/health" {
		t.Fatalf("location=%q", loc)
	}
}

func TestUIHealthPage(t *testing.T) {
	rr := get(t, newUIRouter(t), "/health")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "central") {
		t.Fatalf("health: %s", rr.Body.String())
	}
}

func TestUIAssetsServed(t *testing.T) {
	rr := get(t, newUIRouter(t), "/ui/assets/app.css")
	if rr.Code != 200 || rr.Header().Get("Content-Type") == "" {
		t.Fatalf("status=%d ct=%s", rr.Code, rr.Header().Get("Content-Type"))
	}
	if !strings.Contains(rr.Body.String(), "--color-bg") {
		t.Fatal("expected css content")
	}
}

func TestUIDeletedRoutesReturn404(t *testing.T) {
	h := newUIRouter(t)
	for _, p := range []string{
		"/devices",
		"/devices/0001ABCD",
		"/devices/0001ABCD/channels/1",
		"/programs",
		"/sysvars",
		"/config",
		"/incidents",
		"/settings",
		"/users",
		"/tokens",
		"/devices/0001ABCD/paramsets/MASTER",
		"/devices/0001ABCD/channels/1/schedule",
		"/devices/0001ABCD/links",
	} {
		rr := get(t, h, p)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s: expected 404, got %d", p, rr.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// NewRouter — configuration edge cases
// ---------------------------------------------------------------------------

func TestNewRouterEmptyLangDefaultsToEn(t *testing.T) {
	t.Parallel()
	cats, _ := i18n.NewCatalogs()
	h := NewRouter(Deps{
		// Lang deliberately empty — should default to "en"
		Catalogs: cats,
	})
	req := httptest.NewRequest(http.MethodGet, "/health", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with empty Lang, got %d", rr.Code)
	}
}

func TestNewRouterNoMiddlewaresIsNilSafe(t *testing.T) {
	t.Parallel()
	cats, _ := i18n.NewCatalogs()
	h := NewRouter(Deps{
		Lang:     "en",
		Catalogs: cats,
	})
	req := httptest.NewRequest(http.MethodGet, "/health", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// SPAHandler — static asset caching and fallback behaviour
// ---------------------------------------------------------------------------

// firstEmbeddedAsset returns the basename of any file under
// spa_dist/assets/ in the embedded FS, or "" when the SPA has not
// been built yet.
func firstEmbeddedAsset(t *testing.T) string {
	t.Helper()
	entries, err := fs.ReadDir(spaFS, "spa_dist/assets")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			return e.Name()
		}
	}
	return ""
}

// emptyTestFS satisfies fs.FS but never contains any file.
type emptyTestFS struct{}

func (emptyTestFS) Open(_ string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

func TestServeIndexFallbackWhenNoIndexHTML(t *testing.T) {
	// Use an empty in-memory FS that has no index.html.
	emptyFS := emptyTestFS{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	serveIndex(emptyFS, rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "SPA bundle not built") {
		t.Fatalf("expected SPA-not-built message, body: %s", body)
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected no-store cache header, got %q", rr.Header().Get("Cache-Control"))
	}
}

func TestSPAHandlerDoesNotPanic(t *testing.T) {
	h := http.StripPrefix("/app", SPAHandler())
	req := httptest.NewRequest(http.MethodGet, "/app/", http.NoBody)
	rr := httptest.NewRecorder()
	// Must not panic regardless of whether the SPA was built.
	h.ServeHTTP(rr, req)
	// Accept 200 (spa built) or 503 (spa not built).
	if rr.Code != http.StatusOK && rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status %d", rr.Code)
	}
}

func TestSPAHandlerUnknownPathFallsBackToIndex(t *testing.T) {
	h := http.StripPrefix("/app", SPAHandler())
	req := httptest.NewRequest(http.MethodGet, "/app/some/deep/client-route", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	// Either 200 (found index.html) or 503 (SPA not built).
	if rr.Code != http.StatusOK && rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status %d for unknown path", rr.Code)
	}
}

func TestSPAHandlerStaticAssetSetsCacheControl(t *testing.T) {
	t.Parallel()
	h := http.StripPrefix("/app", SPAHandler())

	// Discover an actual hashed asset from the embedded build at
	// runtime — vite emits a fresh content-hash per build, so a
	// hardcoded filename would rot at every dependency bump.
	asset := firstEmbeddedAsset(t)
	if asset == "" {
		t.Skip("no built SPA assets present — handler exercised by other tests")
	}
	req := httptest.NewRequest(http.MethodGet, "/app/assets/"+asset, http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status %d for static asset %q", rr.Code, asset)
	}
	cc := rr.Header().Get("Cache-Control")
	if !strings.Contains(cc, "immutable") {
		t.Fatalf("expected immutable cache-control for static asset, got %q", cc)
	}
}

func TestSPAHandlerFileNotFoundFallsBackToIndex(t *testing.T) {
	t.Parallel()
	h := http.StripPrefix("/app", SPAHandler())

	// Request a path that definitely won't exist in spa_dist.
	req := httptest.NewRequest(http.MethodGet, "/app/definitely-not-here.js", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	switch rr.Code {
	case http.StatusOK, http.StatusServiceUnavailable:
		// Both valid depending on SPA build status.
	default:
		t.Fatalf("unexpected status %d for not-found asset", rr.Code)
	}
}

func TestSPAHandlerEmptyPath(t *testing.T) {
	t.Parallel()
	h := http.StripPrefix("/app", SPAHandler())
	req := httptest.NewRequest(http.MethodGet, "/app/", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	// Either 200 (SPA built) or 503 (SPA not built).
	if rr.Code != http.StatusOK && rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status %d", rr.Code)
	}
}

// TestUIHealthPageMirrorsTheAPIVerdict pins the two health surfaces to one
// verdict. The page used to render the tracker's raw worst-of while
// GET /api/v1/health collapses through health.ServiceAvailability, so a
// single non-critical interface down made this page — the one an operator
// opens precisely when the SPA is unreachable — read "unhealthy" while every
// other surface, and any load balancer, read "degraded".
func TestUIHealthPageMirrorsTheAPIVerdict(t *testing.T) {
	t.Parallel()

	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("catalogs: %v", err)
	}
	tracker := health.NewTracker()
	tracker.Record("central", health.Sample{Healthy: true})
	tracker.Record("sqlite", health.Sample{Healthy: true})
	tracker.Record("ccu1-HmIP-RF", health.Sample{Healthy: true})
	tracker.Record("ccu2-HmIP-RF", health.Sample{Healthy: false})

	snap := tracker.Snapshot()
	want := string(health.ServiceAvailability(snap))
	if want == string(tracker.Overall()) {
		t.Fatalf("fixture does not separate the two verdicts: both say %q", want)
	}

	h := NewRouter(Deps{Lang: "en", Catalogs: cats, Health: tracker})
	rr := get(t, h, "/health")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	// The overall verdict is the <strong class="status-…"> right after the
	// heading; the per-component cells carry their own raw statuses, so the
	// assertion has to name the overall element rather than search the page.
	body := rr.Body.String()
	overall := `<strong class="status-` + want + `">`
	if !strings.Contains(body, overall) {
		t.Errorf("page does not report the service-availability verdict %q; body: %s", want, body)
	}
	if raw := `<strong class="status-` + string(tracker.Overall()) + `">`; strings.Contains(body, raw) {
		t.Errorf("page still reports the raw worst-of verdict %q", tracker.Overall())
	}
}

// TestSPAHandlerUnhashedAssetsRevalidate pins the caching split. Only the
// content-hashed files under assets/ may carry `immutable`: the bundle also
// ships verbatim public files whose names never change across releases
// (manifest.webmanifest, the favicons, the wordmark). Marked immutable, a
// rebranded logo or an updated manifest would stay stale in returning
// browsers for up to a year without a single revalidation request.
func TestSPAHandlerUnhashedAssetsRevalidate(t *testing.T) {
	t.Parallel()
	h := http.StripPrefix("/app", SPAHandler())

	for _, name := range []string{"manifest.webmanifest", "favicon.svg", "wordmark.svg"} {
		req := httptest.NewRequest(http.MethodGet, "/app/"+name, http.NoBody)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code == http.StatusServiceUnavailable {
			t.Skip("no built SPA bundle present")
		}
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d", name, rr.Code)
		}
		cc := rr.Header().Get("Cache-Control")
		if strings.Contains(cc, "immutable") {
			t.Errorf("%s has a stable name across releases but is served %q", name, cc)
		}
		if !strings.Contains(cc, "must-revalidate") {
			t.Errorf("%s: expected a revalidating cache header, got %q", name, cc)
		}
	}
}

// TestSPAHandlerIndexIsNeverCached pins that the entry point is uncacheable
// however it is addressed — the deep-link fallback and the explicit
// /app/index.html have to agree, or a deploy is picked up on one path and not
// on the other.
func TestSPAHandlerIndexIsNeverCached(t *testing.T) {
	t.Parallel()
	h := http.StripPrefix("/app", SPAHandler())

	for _, p := range []string{"/app/", "/app/index.html"} {
		req := httptest.NewRequest(http.MethodGet, p, http.NoBody)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s: Cache-Control=%q, want no-store", p, cc)
		}
	}
}
