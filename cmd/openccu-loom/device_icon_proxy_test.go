// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// resolverFromCentrals builds a stub [adapter.CentralConfigResolver] over a
// fixed set of centrals, standing in for the store-backed resolver in tests.
func resolverFromCentrals(centrals ...config.CentralConfig) adapter.CentralConfigResolver {
	byName := make(map[string]config.CentralConfig, len(centrals))
	for i := range centrals {
		byName[centrals[i].Name] = centrals[i]
	}
	return func(_ context.Context, name string) (config.CentralConfig, bool) {
		cc, ok := byName[name]
		return cc, ok
	}
}

// ---------------------------------------------------------------------------
// ccuImageBaseURL
// ---------------------------------------------------------------------------

func TestCcuImageBaseURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cc   config.CentralConfig
		want string
	}{
		{
			name: "plain HTTP default port",
			cc:   config.CentralConfig{Host: "ccu", TLS: false},
			want: "http://ccu:80",
		},
		{
			name: "TLS default port",
			cc:   config.CentralConfig{Host: "ccu", TLS: true},
			want: "https://ccu:443",
		},
		{
			name: "plain HTTP with JSONRPCPort override",
			cc:   config.CentralConfig{Host: "ccu", TLS: false, JSONRPCPort: 8181},
			want: "http://ccu:8181",
		},
		{
			name: "TLS with JSONRPCPort override",
			cc:   config.CentralConfig{Host: "ccu", TLS: true, JSONRPCPort: 8443},
			want: "https://ccu:8443",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ccuImageBaseURL(tc.cc)
			if got != tc.want {
				t.Errorf("ccuImageBaseURL(%+v) = %q, want %q", tc.cc, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// splitHostPort parses host and port from a URL string like "http://host:port".
func splitHostPort(t *testing.T, rawURL string) (host string, port int) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", rawURL, err)
	}
	host = u.Hostname()
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("strconv.Atoi(port %q): %v", u.Port(), err)
	}
	return host, p
}

// ---------------------------------------------------------------------------
// deviceIconProxy.Icon — happy path
// ---------------------------------------------------------------------------

func TestDeviceIconProxy_Icon_HappyPath(t *testing.T) {
	t.Parallel()

	const iconFile = "swdo.png"
	const iconPath = "/config/img/devices/250/" + iconFile
	pngBody := []byte("PNGDATA")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == iconPath {
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(pngBody)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	host, port := splitHostPort(t, srv.URL)
	cc := config.CentralConfig{Name: "ccu", Host: host, JSONRPCPort: port, TLS: false}

	locate := func(_ string) (string, string, bool) {
		return iconFile, "ccu", true
	}
	proxy := newDeviceIconProxyWith(locate, resolverFromCentrals(cc))

	data, ct, ok := proxy.Icon(context.Background(), "AABB0001")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !bytes.Equal(data, pngBody) {
		t.Errorf("data = %q, want %q", data, pngBody)
	}
	if ct != "image/png" {
		t.Errorf("contentType = %q, want %q", ct, "image/png")
	}
}

// ---------------------------------------------------------------------------
// deviceIconProxy.Icon — CCU authentication
// ---------------------------------------------------------------------------

// TestDeviceIconProxy_Icon_SendsBasicAuth verifies the icon fetch carries
// the central's credentials as HTTP Basic Auth when a username is set (so a
// CCU with authentication enabled, reached off-box, still serves the image),
// and sends no Authorization header when no username is configured.
func TestDeviceIconProxy_Icon_SendsBasicAuth(t *testing.T) {
	t.Parallel()

	const iconFile = "swdo.png"
	const iconPath = "/config/img/devices/250/" + iconFile

	cases := []struct {
		name         string
		username     string
		password     string
		wantAuth     bool
		wantUser     string
		wantPassword string
	}{
		{name: "credentials set → Basic Auth", username: "Admin", password: "s3cret", wantAuth: true, wantUser: "Admin", wantPassword: "s3cret"},
		{name: "no username → no auth header", username: "", password: "", wantAuth: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotAuth atomic.Bool
			var gotUser, gotPass atomic.Value
			gotUser.Store("")
			gotPass.Store("")

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != iconPath {
					http.NotFound(w, r)
					return
				}
				if u, p, ok := r.BasicAuth(); ok {
					gotAuth.Store(true)
					gotUser.Store(u)
					gotPass.Store(p)
				}
				w.Header().Set("Content-Type", "image/png")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("PNGDATA"))
			}))
			defer srv.Close()

			host, port := splitHostPort(t, srv.URL)
			cc := config.CentralConfig{Name: "ccu", Host: host, JSONRPCPort: port, TLS: false, Username: tc.username, Password: tc.password}

			locate := func(_ string) (string, string, bool) { return iconFile, "ccu", true }
			proxy := newDeviceIconProxyWith(locate, resolverFromCentrals(cc))

			if _, _, ok := proxy.Icon(context.Background(), "AABB0001"); !ok {
				t.Fatal("expected ok=true")
			}
			if gotAuth.Load() != tc.wantAuth {
				t.Fatalf("Basic Auth present = %v, want %v", gotAuth.Load(), tc.wantAuth)
			}
			if tc.wantAuth {
				if u := gotUser.Load().(string); u != tc.wantUser {
					t.Errorf("Basic Auth user = %q, want %q", u, tc.wantUser)
				}
				if p := gotPass.Load().(string); p != tc.wantPassword {
					t.Errorf("Basic Auth password = %q, want %q", p, tc.wantPassword)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// deviceIconProxy.Icon — caching
// ---------------------------------------------------------------------------

func TestDeviceIconProxy_Icon_HitCached(t *testing.T) {
	t.Parallel()

	const iconFile = "swdo.png"
	const iconPath = "/config/img/devices/250/" + iconFile
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == iconPath {
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("PNGDATA"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	host, port := splitHostPort(t, srv.URL)
	cc := config.CentralConfig{Name: "ccu", Host: host, JSONRPCPort: port, TLS: false}

	locate := func(_ string) (string, string, bool) { return iconFile, "ccu", true }
	proxy := newDeviceIconProxyWith(locate, resolverFromCentrals(cc))

	// First call — fetches from server.
	_, _, ok := proxy.Icon(context.Background(), "AABB0001")
	if !ok {
		t.Fatal("first call: expected ok=true")
	}
	if hits.Load() != 1 {
		t.Fatalf("first call: expected 1 server hit, got %d", hits.Load())
	}

	// Second call — must use cache, no additional server hit.
	_, _, ok = proxy.Icon(context.Background(), "AABB0001")
	if !ok {
		t.Fatal("second call: expected ok=true")
	}
	if hits.Load() != 1 {
		t.Fatalf("second call: expected still 1 server hit (cache), got %d", hits.Load())
	}
}

func TestDeviceIconProxy_Icon_MissCached_NonOKUpstream(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.NotFound(w, nil) //nolint:staticcheck // test double
	}))
	defer srv.Close()

	host, port := splitHostPort(t, srv.URL)
	cc := config.CentralConfig{Name: "ccu", Host: host, JSONRPCPort: port, TLS: false}

	locate := func(_ string) (string, string, bool) { return "swdo.png", "ccu", true }
	proxy := newDeviceIconProxyWith(locate, resolverFromCentrals(cc))

	// First call — server returns 404 → miss.
	_, _, ok := proxy.Icon(context.Background(), "AABB0001")
	if ok {
		t.Fatal("first call: expected ok=false for non-200 upstream")
	}
	firstHits := hits.Load()
	if firstHits == 0 {
		t.Fatal("first call: expected at least 1 server hit")
	}

	// Second call — miss must be cached, server not hit again.
	_, _, ok = proxy.Icon(context.Background(), "AABB0001")
	if ok {
		t.Fatal("second call: expected ok=false (cached miss)")
	}
	if hits.Load() != firstHits {
		t.Fatalf("second call: expected no additional server hits (cached miss), got %d extra", hits.Load()-firstHits)
	}
}

// ---------------------------------------------------------------------------
// deviceIconProxy.Icon — unknown device
// ---------------------------------------------------------------------------

func TestDeviceIconProxy_Icon_UnknownDevice_Returns_False(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	locate := func(_ string) (string, string, bool) { return "", "", false }
	proxy := newDeviceIconProxyWith(locate, resolverFromCentrals())

	_, _, ok := proxy.Icon(context.Background(), "UNKNOWN")
	if ok {
		t.Fatal("expected ok=false for unknown device")
	}
	if hits.Load() != 0 {
		t.Fatalf("server must not be hit for unknown device, got %d hits", hits.Load())
	}
}

// ---------------------------------------------------------------------------
// deviceIconProxy.Icon — unknown addresses never grow the cache
// ---------------------------------------------------------------------------

// TestDeviceIconProxy_Icon_UnknownAddress_NeverCached guards the
// anti-enumeration property documented on [deviceIconProxy.Icon]: the icon
// route is unauthenticated, so an unknown address must never create a
// cache entry — otherwise a caller could grow the cache without bound (and
// probe which addresses exist) by requesting random addresses in a loop.
// Repeated calls with distinct unknown addresses must leave the cache
// empty.
func TestDeviceIconProxy_Icon_UnknownAddress_NeverCached(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	locate := func(_ string) (string, string, bool) { return "", "", false }
	proxy := newDeviceIconProxyWith(locate, resolverFromCentrals())

	addresses := []string{"UNKNOWN-1", "UNKNOWN-2", "RANDOM-ABC123", "does-not-exist"}
	for _, addr := range addresses {
		_, _, ok := proxy.Icon(context.Background(), addr)
		if ok {
			t.Fatalf("address %q: expected ok=false for unknown device", addr)
		}
	}

	proxy.mu.RLock()
	n := len(proxy.cache)
	proxy.mu.RUnlock()
	if n != 0 {
		t.Errorf("cache size = %d after %d unknown-address lookups, want 0 (anti-enumeration guard)", n, len(addresses))
	}
	if hits.Load() != 0 {
		t.Fatalf("server must not be hit for unknown addresses, got %d hits", hits.Load())
	}
}

// TestDeviceIconProxy_Icon_KnownAddress_FetchFailure_CachesSingleEntry
// verifies the counterpart of the anti-enumeration guard: once an address
// resolves to a real device, a failed upstream fetch still caches the
// (known-device) miss, and repeated lookups of the same known address do
// not grow the cache beyond a single entry.
func TestDeviceIconProxy_Icon_KnownAddress_FetchFailure_CachesSingleEntry(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.NotFound(w, nil) //nolint:staticcheck // test double
	}))
	defer srv.Close()

	host, port := splitHostPort(t, srv.URL)
	cc := config.CentralConfig{Name: "ccu", Host: host, JSONRPCPort: port, TLS: false}

	locate := func(_ string) (string, string, bool) { return "swdo.png", "ccu", true }
	proxy := newDeviceIconProxyWith(locate, resolverFromCentrals(cc))

	for range 3 {
		_, _, ok := proxy.Icon(context.Background(), "AABB0001")
		if ok {
			t.Fatal("expected ok=false for a known device whose upstream fetch fails")
		}
	}

	proxy.mu.RLock()
	n := len(proxy.cache)
	proxy.mu.RUnlock()
	if n != 1 {
		t.Errorf("cache size = %d after repeated lookups of one known address, want 1", n)
	}
	if hits.Load() != 1 {
		t.Errorf("server hits = %d, want 1 (subsequent lookups must hit the cached miss)", hits.Load())
	}
}

// ---------------------------------------------------------------------------
// deviceIconProxy.Icon — unsafe filenames
// ---------------------------------------------------------------------------

func TestDeviceIconProxy_Icon_UnsafeFilename_Returns_False(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host, port := splitHostPort(t, srv.URL)
	cc := config.CentralConfig{Name: "ccu", Host: host, JSONRPCPort: port, TLS: false}

	cases := []struct {
		name     string
		filename string
	}{
		{"path traversal double-dot", "../../etc/passwd.png"},
		{"relative traversal", "../evil.png"},
		{"non-PNG extension", "foo.txt"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// note: NOT t.Parallel() here — we share the hit counter
			filename := tc.filename
			locate := func(_ string) (string, string, bool) { return filename, "ccu", true }
			proxy := newDeviceIconProxyWith(locate, resolverFromCentrals(cc))

			_, _, ok := proxy.Icon(context.Background(), "DEV-"+filename)
			if ok {
				t.Errorf("filename %q: expected ok=false", filename)
			}
			if hits.Load() != 0 {
				t.Errorf("filename %q: server must not be hit, got %d hits", filename, hits.Load())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// deviceIconProxy.Icon — missing central config
// ---------------------------------------------------------------------------

func TestDeviceIconProxy_Icon_MissingCentralConfig_Returns_False(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// locate returns "unknown" as central name, but no such central in configs.
	locate := func(_ string) (string, string, bool) { return "swdo.png", "unknown", true }
	proxy := newDeviceIconProxyWith(locate, resolverFromCentrals())

	_, _, ok := proxy.Icon(context.Background(), "AABB0001")
	if ok {
		t.Fatal("expected ok=false for missing central config")
	}
	if hits.Load() != 0 {
		t.Fatalf("server must not be hit for missing central config, got %d hits", hits.Load())
	}
}

// ---------------------------------------------------------------------------
// deviceIconProxy.Icon — non-200 upstream
// ---------------------------------------------------------------------------

func TestDeviceIconProxy_Icon_Non200Upstream_Returns_False(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil) //nolint:staticcheck // test double
	}))
	defer srv.Close()

	host, port := splitHostPort(t, srv.URL)
	cc := config.CentralConfig{Name: "ccu", Host: host, JSONRPCPort: port, TLS: false}

	locate := func(_ string) (string, string, bool) { return "swdo.png", "ccu", true }
	proxy := newDeviceIconProxyWith(locate, resolverFromCentrals(cc))

	_, _, ok := proxy.Icon(context.Background(), "AABB0001")
	if ok {
		t.Fatal("expected ok=false for non-200 upstream response")
	}
}
