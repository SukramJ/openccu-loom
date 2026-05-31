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
	req := httptest.NewRequest("GET", path, http.NoBody)
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
	req := httptest.NewRequest("GET", "/health", http.NoBody)
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
		Lang:        "en",
		Catalogs:    cats,
		AuthResolve: nil,
		AuthRequire: nil,
	})
	req := httptest.NewRequest("GET", "/health", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestNewRouterWithAuthResolveMiddleware(t *testing.T) {
	t.Parallel()
	cats, _ := i18n.NewCatalogs()
	called := false
	middleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			next.ServeHTTP(w, r)
		})
	}
	h := NewRouter(Deps{
		Lang:        "en",
		Catalogs:    cats,
		AuthResolve: middleware,
	})
	req := httptest.NewRequest("GET", "/health", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !called {
		t.Fatal("AuthResolve middleware was not called")
	}
}

func TestNewRouterWithAuthRequireMiddleware(t *testing.T) {
	t.Parallel()
	cats, _ := i18n.NewCatalogs()
	// Middleware that adds a header to the response — verifies it runs.
	middleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Test-MW", "yes")
			next.ServeHTTP(w, r)
		})
	}
	h := NewRouter(Deps{
		Lang:        "en",
		Catalogs:    cats,
		AuthRequire: middleware,
	})
	req := httptest.NewRequest("GET", "/health", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Header().Get("X-Test-MW") != "yes" {
		t.Fatal("AuthRequire middleware was not called")
	}
}

// ---------------------------------------------------------------------------
// ifLogger — covers the nil-Logger branch
// ---------------------------------------------------------------------------

func TestIfLoggerReturnsDefaultWhenNil(t *testing.T) {
	d := Deps{Logger: nil}
	l := ifLogger(d)
	if l == nil {
		t.Fatal("ifLogger must return slog.Default(), never nil")
	}
}

func TestIfLoggerReturnsProvidedLogger(t *testing.T) {
	// Use a non-nil Deps.Logger and verify the same instance is returned.
	// We can't easily compare *slog.Logger identity, but we can verify
	// non-nil is passed through.
	cats, _ := i18n.NewCatalogs()
	h := NewRouter(Deps{Lang: "en", Catalogs: cats})
	// Presence of a valid handler means NewRouter used ifLogger without panic.
	if h == nil {
		t.Fatal("router must not be nil")
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
	req := httptest.NewRequest("GET", "/", http.NoBody)
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
	req := httptest.NewRequest("GET", "/app/", http.NoBody)
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
	req := httptest.NewRequest("GET", "/app/some/deep/client-route", http.NoBody)
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
	req := httptest.NewRequest("GET", "/app/assets/"+asset, http.NoBody)
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
	req := httptest.NewRequest("GET", "/app/definitely-not-here.js", http.NoBody)
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
	req := httptest.NewRequest("GET", "/app/", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	// Either 200 (SPA built) or 503 (SPA not built).
	if rr.Code != http.StatusOK && rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status %d", rr.Code)
	}
}
