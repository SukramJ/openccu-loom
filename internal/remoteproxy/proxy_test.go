// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package remoteproxy

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// quietProxyLogger discards log output so a failing upstream (deliberately
// exercised by the unreachable-upstream test) does not spam test output.
func quietProxyLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// proxyFixtureCapture records the last request an upstream fixture
// received. The *http.Request handed to an http.Handler is only valid for
// the duration of that call, so the fields worth asserting on are copied
// out under a mutex instead of retaining the request itself.
type proxyFixtureCapture struct {
	mu     sync.Mutex
	header http.Header
}

func (c *proxyFixtureCapture) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.header = r.Header.Clone()
}

// Header returns the headers of the last recorded request.
func (c *proxyFixtureCapture) Header() http.Header {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.header
}

// newProxyUpstreamFixture starts a fake upstream that records every
// request it receives and otherwise answers via respond (a nil respond
// answers 200 with an empty body).
func newProxyUpstreamFixture(t *testing.T, respond http.HandlerFunc) (*httptest.Server, *proxyFixtureCapture) {
	t.Helper()
	capture := &proxyFixtureCapture{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		if respond != nil {
			respond(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	return upstream, capture
}

// newProxyServerFixture builds a remoteproxy.Server from the given
// instances via New() and exposes it through an httptest server.
func newProxyServerFixture(t *testing.T, instances []Instance) *httptest.Server {
	t.Helper()
	srv, err := New(Options{Instances: instances}, quietProxyLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	proxy := httptest.NewServer(srv.Handler())
	t.Cleanup(proxy.Close)
	return proxy
}

// proxyGet issues a GET against target with the given extra headers and
// returns the response without following redirects, so a test can inspect
// exactly what the proxy handed back.
func proxyGet(t *testing.T, target string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// proxyClosedUpstreamURL returns a URL whose port is guaranteed to refuse
// connections: a listener is opened and immediately closed, so the address
// is valid but nothing answers on it.
func proxyClosedUpstreamURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("Close listener: %v", err)
	}
	return "http://" + addr
}

func TestInstanceProxyTokenInjection(t *testing.T) {
	t.Run("no client credential gets the instance bearer token", func(t *testing.T) {
		upstream, capture := newProxyUpstreamFixture(t, nil)
		proxy := newProxyServerFixture(t, []Instance{{Name: "alpha", URL: upstream.URL, Token: "tok"}})

		resp := proxyGet(t, proxy.URL+"/", nil)
		resp.Body.Close()

		if got := capture.Header().Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer tok")
		}
	})

	t.Run("client credential is not overridden", func(t *testing.T) {
		upstream, capture := newProxyUpstreamFixture(t, nil)
		proxy := newProxyServerFixture(t, []Instance{{Name: "alpha", URL: upstream.URL, Token: "tok"}})

		resp := proxyGet(t, proxy.URL+"/", map[string]string{"Authorization": "Basic xyz"})
		resp.Body.Close()

		if got := capture.Header().Get("Authorization"); got != "Basic xyz" {
			t.Errorf("Authorization = %q, want %q", got, "Basic xyz")
		}
	})

	t.Run("empty token injects no header at all", func(t *testing.T) {
		upstream, capture := newProxyUpstreamFixture(t, nil)
		proxy := newProxyServerFixture(t, []Instance{{Name: "alpha", URL: upstream.URL}})

		resp := proxyGet(t, proxy.URL+"/", nil)
		resp.Body.Close()

		if vals := capture.Header().Values("Authorization"); len(vals) != 0 {
			t.Errorf("Authorization present: %v", vals)
		}
	})
}

func TestInstanceProxySessionCookieStrip(t *testing.T) {
	t.Run("token mode drops the competing session cookie, keeps others", func(t *testing.T) {
		upstream, capture := newProxyUpstreamFixture(t, nil)
		proxy := newProxyServerFixture(t, []Instance{{Name: "alpha", URL: upstream.URL, Token: "tok"}})

		resp := proxyGet(t, proxy.URL+"/", map[string]string{
			"Cookie": sessionCookieName + "=stale; keep=me",
		})
		resp.Body.Close()

		gotCookie := capture.Header().Get("Cookie")
		if strings.Contains(gotCookie, sessionCookieName) {
			t.Errorf("session cookie forwarded upstream: %q", gotCookie)
		}
		if !strings.Contains(gotCookie, "keep=me") {
			t.Errorf("unrelated cookie dropped: %q", gotCookie)
		}
		if got := capture.Header().Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer tok")
		}
	})

	t.Run("no-token mode preserves the session cookie (proxied login)", func(t *testing.T) {
		upstream, capture := newProxyUpstreamFixture(t, nil)
		proxy := newProxyServerFixture(t, []Instance{{Name: "alpha", URL: upstream.URL}})

		resp := proxyGet(t, proxy.URL+"/", map[string]string{
			"Cookie": sessionCookieName + "=live",
		})
		resp.Body.Close()

		if got := capture.Header().Get("Cookie"); !strings.Contains(got, sessionCookieName+"=live") {
			t.Errorf("session cookie stripped in no-token mode: %q", got)
		}
	})
}

func TestInstanceProxyIngressPathHeader(t *testing.T) {
	t.Run("multi-instance mount appends the instance prefix", func(t *testing.T) {
		upstream, capture := newProxyUpstreamFixture(t, nil)
		proxy := newProxyServerFixture(t, []Instance{
			{Name: "alpha", URL: upstream.URL},
			{Name: "beta", URL: upstream.URL},
		})

		resp := proxyGet(t, proxy.URL+"/i/alpha/x", map[string]string{ingressPathHeader: "/api/hassio_ingress/abc"})
		resp.Body.Close()

		want := "/api/hassio_ingress/abc/i/alpha"
		if got := capture.Header().Get(ingressPathHeader); got != want {
			t.Errorf("%s = %q, want %q", ingressPathHeader, got, want)
		}
	})

	t.Run("protocol-relative spoof is dropped but the prefix still forwards", func(t *testing.T) {
		upstream, capture := newProxyUpstreamFixture(t, nil)
		proxy := newProxyServerFixture(t, []Instance{
			{Name: "alpha", URL: upstream.URL},
			{Name: "beta", URL: upstream.URL},
		})

		resp := proxyGet(t, proxy.URL+"/i/alpha/x", map[string]string{ingressPathHeader: "//evil.com"})
		resp.Body.Close()

		want := "/i/alpha"
		if got := capture.Header().Get(ingressPathHeader); got != want {
			t.Errorf("%s = %q, want %q", ingressPathHeader, got, want)
		}
	})

	t.Run("query-bearing spoof is dropped but the prefix still forwards", func(t *testing.T) {
		upstream, capture := newProxyUpstreamFixture(t, nil)
		proxy := newProxyServerFixture(t, []Instance{
			{Name: "alpha", URL: upstream.URL},
			{Name: "beta", URL: upstream.URL},
		})

		resp := proxyGet(t, proxy.URL+"/i/alpha/x", map[string]string{ingressPathHeader: "/x?y"})
		resp.Body.Close()

		want := "/i/alpha"
		if got := capture.Header().Get(ingressPathHeader); got != want {
			t.Errorf("%s = %q, want %q", ingressPathHeader, got, want)
		}
	})

	t.Run("single-instance mode passes a clean header through unchanged", func(t *testing.T) {
		upstream, capture := newProxyUpstreamFixture(t, nil)
		proxy := newProxyServerFixture(t, []Instance{{Name: "alpha", URL: upstream.URL}})

		resp := proxyGet(t, proxy.URL+"/", map[string]string{ingressPathHeader: "/api/hassio_ingress/xyz"})
		resp.Body.Close()

		want := "/api/hassio_ingress/xyz"
		if got := capture.Header().Get(ingressPathHeader); got != want {
			t.Errorf("%s = %q, want %q", ingressPathHeader, got, want)
		}
	})

	t.Run("single-instance mode with no header forwards none", func(t *testing.T) {
		upstream, capture := newProxyUpstreamFixture(t, nil)
		proxy := newProxyServerFixture(t, []Instance{{Name: "alpha", URL: upstream.URL}})

		resp := proxyGet(t, proxy.URL+"/", nil)
		resp.Body.Close()

		if vals := capture.Header().Values(ingressPathHeader); len(vals) != 0 {
			t.Errorf("%s present: %v", ingressPathHeader, vals)
		}
	})
}

// proxyLocationResponder lets a test swap the upstream's redirect response
// between subtests that share one upstream/proxy fixture pair.
type proxyLocationResponder struct {
	mu       sync.Mutex
	status   int
	location string
}

func (l *proxyLocationResponder) set(status int, location string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.status = status
	l.location = location
}

func (l *proxyLocationResponder) handle(w http.ResponseWriter, _ *http.Request) {
	l.mu.Lock()
	status, location := l.status, l.location
	l.mu.Unlock()
	if location != "" {
		w.Header().Set("Location", location)
	}
	w.WriteHeader(status)
}

func TestInstanceProxyLocationRewrite(t *testing.T) {
	const ingressHeader = "/api/hassio_ingress/abc"
	const base = ingressHeader + "/i/alpha"

	responder := &proxyLocationResponder{}
	upstream, _ := newProxyUpstreamFixture(t, responder.handle)
	proxy := newProxyServerFixture(t, []Instance{
		{Name: "alpha", URL: upstream.URL},
		{Name: "beta", URL: upstream.URL},
	})

	get := func(t *testing.T) *http.Response {
		t.Helper()
		return proxyGet(t, proxy.URL+"/i/alpha/x", map[string]string{ingressPathHeader: ingressHeader})
	}

	t.Run("absolute path redirect gains the browser-facing base", func(t *testing.T) {
		responder.set(http.StatusFound, "/login")
		resp := get(t)
		resp.Body.Close()

		want := base + "/login"
		if got := resp.Header.Get("Location"); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	})

	t.Run("location already carrying the full base passes unchanged", func(t *testing.T) {
		loc := base + "/dashboard"
		responder.set(http.StatusFound, loc)
		resp := get(t)
		resp.Body.Close()

		if got := resp.Header.Get("Location"); got != loc {
			t.Errorf("Location = %q, want %q", got, loc)
		}
	})

	t.Run("absolute URL to the upstream host is folded onto the base", func(t *testing.T) {
		responder.set(http.StatusFound, upstream.URL+"/login")
		resp := get(t)
		resp.Body.Close()

		want := base + "/login"
		if got := resp.Header.Get("Location"); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	})

	t.Run("absolute URL to a foreign host passes unchanged", func(t *testing.T) {
		responder.set(http.StatusFound, "https://issuer.example/authorize")
		resp := get(t)
		resp.Body.Close()

		want := "https://issuer.example/authorize"
		if got := resp.Header.Get("Location"); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	})

	t.Run("relative location passes unchanged", func(t *testing.T) {
		responder.set(http.StatusFound, "sub/page")
		resp := get(t)
		resp.Body.Close()

		want := "sub/page"
		if got := resp.Header.Get("Location"); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	})
}

// proxyCookieResponder lets a test swap the Set-Cookie lines an upstream
// answers with between subtests that share one upstream/proxy fixture pair.
type proxyCookieResponder struct {
	mu    sync.Mutex
	lines []string
}

func (c *proxyCookieResponder) set(lines ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = lines
}

func (c *proxyCookieResponder) handle(w http.ResponseWriter, _ *http.Request) {
	c.mu.Lock()
	lines := c.lines
	c.mu.Unlock()
	for _, l := range lines {
		w.Header().Add("Set-Cookie", l)
	}
	w.WriteHeader(http.StatusOK)
}

func parseProxyCookie(t *testing.T, raw string) *http.Cookie {
	t.Helper()
	c, err := http.ParseSetCookie(raw)
	if err != nil {
		t.Fatalf("ParseSetCookie(%q): %v", raw, err)
	}
	return c
}

func TestInstanceProxySetCookieRewrite(t *testing.T) {
	const base = "/api/hassio_ingress/abc"

	responder := &proxyCookieResponder{}
	upstream, _ := newProxyUpstreamFixture(t, responder.handle)
	proxy := newProxyServerFixture(t, []Instance{{Name: "alpha", URL: upstream.URL}})

	get := func(t *testing.T) *http.Response {
		t.Helper()
		return proxyGet(t, proxy.URL+"/", map[string]string{ingressPathHeader: base})
	}

	t.Run("Path=/ becomes base + slash", func(t *testing.T) {
		responder.set("sid=1; Path=/")
		resp := get(t)
		resp.Body.Close()

		lines := resp.Header.Values("Set-Cookie")
		if len(lines) != 1 {
			t.Fatalf("Set-Cookie lines = %d, want 1", len(lines))
		}
		c := parseProxyCookie(t, lines[0])
		if want := base + "/"; c.Path != want {
			t.Errorf("Path = %q, want %q", c.Path, want)
		}
	})

	t.Run("empty Path becomes base + slash", func(t *testing.T) {
		responder.set("sid=1")
		resp := get(t)
		resp.Body.Close()

		lines := resp.Header.Values("Set-Cookie")
		if len(lines) != 1 {
			t.Fatalf("Set-Cookie lines = %d, want 1", len(lines))
		}
		c := parseProxyCookie(t, lines[0])
		if want := base + "/"; c.Path != want {
			t.Errorf("Path = %q, want %q", c.Path, want)
		}
	})

	t.Run("Path=/sub becomes base + /sub", func(t *testing.T) {
		responder.set("sid=1; Path=/sub")
		resp := get(t)
		resp.Body.Close()

		lines := resp.Header.Values("Set-Cookie")
		if len(lines) != 1 {
			t.Fatalf("Set-Cookie lines = %d, want 1", len(lines))
		}
		c := parseProxyCookie(t, lines[0])
		if want := base + "/sub"; c.Path != want {
			t.Errorf("Path = %q, want %q", c.Path, want)
		}
	})

	t.Run("two cookies in one response are both rewritten", func(t *testing.T) {
		responder.set("a=1; Path=/", "b=2; Path=/sub")
		resp := get(t)
		resp.Body.Close()

		lines := resp.Header.Values("Set-Cookie")
		if len(lines) != 2 {
			t.Fatalf("Set-Cookie lines = %d, want 2", len(lines))
		}
		byName := make(map[string]*http.Cookie, len(lines))
		for _, l := range lines {
			c := parseProxyCookie(t, l)
			byName[c.Name] = c
		}
		if got, want := byName["a"].Path, base+"/"; got != want {
			t.Errorf("cookie a Path = %q, want %q", got, want)
		}
		if got, want := byName["b"].Path, base+"/sub"; got != want {
			t.Errorf("cookie b Path = %q, want %q", got, want)
		}
	})

	t.Run("HttpOnly, Secure, SameSite and Max-Age survive the rewrite", func(t *testing.T) {
		responder.set("sid=1; Path=/; HttpOnly; Secure; SameSite=Strict; Max-Age=3600")
		resp := get(t)
		resp.Body.Close()

		lines := resp.Header.Values("Set-Cookie")
		if len(lines) != 1 {
			t.Fatalf("Set-Cookie lines = %d, want 1", len(lines))
		}
		c := parseProxyCookie(t, lines[0])
		if !c.HttpOnly {
			t.Error("HttpOnly attribute lost")
		}
		if !c.Secure {
			t.Error("Secure attribute lost")
		}
		if c.SameSite != http.SameSiteStrictMode {
			t.Errorf("SameSite = %v, want %v", c.SameSite, http.SameSiteStrictMode)
		}
		if c.MaxAge != 3600 {
			t.Errorf("MaxAge = %d, want 3600", c.MaxAge)
		}
	})
}

func TestInstanceProxyForwardingHeaders(t *testing.T) {
	upstream, capture := newProxyUpstreamFixture(t, nil)
	proxy := newProxyServerFixture(t, []Instance{{Name: "alpha", URL: upstream.URL}})

	resp := proxyGet(t, proxy.URL+"/", nil)
	resp.Body.Close()

	h := capture.Header()
	if got := h.Get("X-Forwarded-Proto"); got != "http" {
		t.Errorf("X-Forwarded-Proto = %q, want %q", got, "http")
	}
	if got := h.Get("X-Forwarded-For"); got == "" {
		t.Error("X-Forwarded-For is empty")
	}
	wantHost := strings.TrimPrefix(proxy.URL, "http://")
	if got := h.Get("X-Forwarded-Host"); got != wantHost {
		t.Errorf("X-Forwarded-Host = %q, want %q", got, wantHost)
	}
}

// TestInstanceProxyPreservesUpstreamForwardedHost pins that an X-Forwarded-Host
// a trusted upstream proxy (e.g. Traefik) already set reaches the daemon
// unchanged, rather than being overwritten with this hop's own host by
// SetXForwarded. The daemon's WebSocket same-origin check compares the browser
// Origin against X-Forwarded-Host; across a double proxy it must stay the
// browser-facing host or the SPA's live WebSocket 403s.
func TestInstanceProxyPreservesUpstreamForwardedHost(t *testing.T) {
	upstream, capture := newProxyUpstreamFixture(t, nil)
	proxy := newProxyServerFixture(t, []Instance{{Name: "alpha", URL: upstream.URL}})

	resp := proxyGet(t, proxy.URL+"/", map[string]string{
		"X-Forwarded-Host":  "loom.example",
		"X-Forwarded-Proto": "https",
	})
	resp.Body.Close()

	h := capture.Header()
	if got := h.Get("X-Forwarded-Host"); got != "loom.example" {
		t.Errorf("X-Forwarded-Host = %q, want loom.example (upstream value preserved)", got)
	}
	if got := h.Get("X-Forwarded-Proto"); got != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want https (upstream value preserved)", got)
	}
}

func TestInstanceProxyUnreachableUpstream(t *testing.T) {
	proxy := newProxyServerFixture(t, []Instance{{Name: "alpha", URL: proxyClosedUpstreamURL(t)}})

	resp := proxyGet(t, proxy.URL+"/", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want a text/html prefix", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) == 0 {
		t.Error("body is empty")
	}
}

func TestInstanceProxyCookiePathStripsUpstreamBase(t *testing.T) {
	const ingressHeader = "/ing"
	const base = ingressHeader + "/i/alpha"

	responder := &proxyCookieResponder{}
	upstream, _ := newProxyUpstreamFixture(t, responder.handle)
	proxy := newProxyServerFixture(t, []Instance{
		{Name: "alpha", URL: upstream.URL + "/loom"},
		{Name: "beta", URL: upstream.URL},
	})

	get := func(t *testing.T) *http.Response {
		t.Helper()
		return proxyGet(t, proxy.URL+"/i/alpha/x", map[string]string{ingressPathHeader: ingressHeader})
	}

	t.Run("Path=/loom becomes base + slash", func(t *testing.T) {
		responder.set("sid=1; Path=/loom")
		resp := get(t)
		resp.Body.Close()

		lines := resp.Header.Values("Set-Cookie")
		if len(lines) != 1 {
			t.Fatalf("Set-Cookie lines = %d, want 1", len(lines))
		}
		c := parseProxyCookie(t, lines[0])
		if want := base + "/"; c.Path != want {
			t.Errorf("Path = %q, want %q", c.Path, want)
		}
	})

	t.Run("Path=/loom/sub becomes base + /sub", func(t *testing.T) {
		responder.set("sid=1; Path=/loom/sub")
		resp := get(t)
		resp.Body.Close()

		lines := resp.Header.Values("Set-Cookie")
		if len(lines) != 1 {
			t.Fatalf("Set-Cookie lines = %d, want 1", len(lines))
		}
		c := parseProxyCookie(t, lines[0])
		if want := base + "/sub"; c.Path != want {
			t.Errorf("Path = %q, want %q", c.Path, want)
		}
	})
}

func TestInstanceProxyLocationSegmentBoundaryNotStripped(t *testing.T) {
	const ingressHeader = "/api/hassio_ingress/seg"
	const base = ingressHeader + "/i/alpha"

	responder := &proxyLocationResponder{}
	upstream, _ := newProxyUpstreamFixture(t, responder.handle)
	proxy := newProxyServerFixture(t, []Instance{
		{Name: "alpha", URL: upstream.URL + "/app"},
		{Name: "beta", URL: upstream.URL},
	})

	// "/apple" only shares the "/app" prefix as a literal string, not as a
	// path segment: stripping it would wrongly turn a sibling path into a
	// broken one-character redirect.
	responder.set(http.StatusFound, "/apple")
	resp := proxyGet(t, proxy.URL+"/i/alpha/x", map[string]string{ingressPathHeader: ingressHeader})
	resp.Body.Close()

	want := base + "/apple"
	if got := resp.Header.Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestInstanceProxyLocationEscapedPathAndFragmentSurvive(t *testing.T) {
	responder := &proxyLocationResponder{}
	upstream, _ := newProxyUpstreamFixture(t, responder.handle)
	proxy := newProxyServerFixture(t, []Instance{
		{Name: "alpha", URL: upstream.URL},
		{Name: "beta", URL: upstream.URL},
	})

	// A decoded "%3F" would turn the path into a real query separator, and
	// a dropped fragment would silently discard the client-side anchor.
	responder.set(http.StatusFound, upstream.URL+"/file%3Fname#frag")
	resp := proxyGet(t, proxy.URL+"/i/alpha/x", nil)
	resp.Body.Close()

	want := "/i/alpha/file%3Fname#frag"
	if got := resp.Header.Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestInstanceProxySingleInstanceBasePathUpstreamLocation(t *testing.T) {
	responder := &proxyLocationResponder{}
	upstream, _ := newProxyUpstreamFixture(t, responder.handle)
	proxy := newProxyServerFixture(t, []Instance{{Name: "alpha", URL: upstream.URL + "/loom"}})

	responder.set(http.StatusFound, "/loom/login")
	resp := proxyGet(t, proxy.URL+"/", nil)
	resp.Body.Close()

	want := "/login"
	if got := resp.Header.Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestInstanceProxySetCookieUnrecognizedAttributeSurvives(t *testing.T) {
	const base = "/api/hassio_ingress/pri"

	responder := &proxyCookieResponder{}
	upstream, _ := newProxyUpstreamFixture(t, responder.handle)
	proxy := newProxyServerFixture(t, []Instance{{Name: "alpha", URL: upstream.URL}})

	responder.set("sid=1; Path=/; Priority=High")
	resp := proxyGet(t, proxy.URL+"/", map[string]string{ingressPathHeader: base})
	resp.Body.Close()

	lines := resp.Header.Values("Set-Cookie")
	if len(lines) != 1 {
		t.Fatalf("Set-Cookie lines = %d, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "Priority=High") {
		t.Errorf("Set-Cookie = %q, want it to contain %q", lines[0], "Priority=High")
	}
	c := parseProxyCookie(t, lines[0])
	if want := base + "/"; c.Path != want {
		t.Errorf("Path = %q, want %q", c.Path, want)
	}
}

func TestInstanceProxyIngressPathTabSpoofRejected(t *testing.T) {
	upstream, capture := newProxyUpstreamFixture(t, nil)
	proxy := newProxyServerFixture(t, []Instance{
		{Name: "alpha", URL: upstream.URL},
		{Name: "beta", URL: upstream.URL},
	})

	t.Run("upstream never sees the spoofed value", func(t *testing.T) {
		resp := proxyGet(t, proxy.URL+"/i/alpha/x", map[string]string{ingressPathHeader: "/\tevil"})
		resp.Body.Close()

		want := "/i/alpha"
		if got := capture.Header().Get(ingressPathHeader); got != want {
			t.Errorf("%s = %q, want %q", ingressPathHeader, got, want)
		}
	})

	t.Run("bare instance mount redirect stays unprefixed", func(t *testing.T) {
		resp := proxyGet(t, proxy.URL+"/i/alpha", map[string]string{ingressPathHeader: "/\tevil"})
		resp.Body.Close()

		want := "/i/alpha/"
		if got := resp.Header.Get("Location"); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	})
}

// TestIsLocalPath pins the open-redirect guard (go/bad-redirect-check): a
// rewritten Location may only be emitted when it is an absolute-path reference
// that a browser cannot reinterpret as an external URL — a single leading "/"
// not immediately followed by another "/" or a "\".
func TestIsLocalPath(t *testing.T) {
	cases := map[string]bool{
		"/":                true,
		"/login":           true,
		"/a/b?x=1":         true,
		`/prefix/\evil`:    true, // backslash only in the middle is a normal path
		"//evil.com":       false,
		`/\evil.com`:       false,
		"/%2F%2Fevil":      true, // encoded slashes are not a leading "//"
		"":                 false,
		"foo":              false,
		"http://evil.com":  false,
		"https://evil.com": false,
	}
	for s, want := range cases {
		if got := isLocalPath(s); got != want {
			t.Errorf("isLocalPath(%q) = %v, want %v", s, got, want)
		}
	}
}

// TestInstanceProxyProtocolRelativeLocationNotRebased verifies a protocol-
// relative redirect to a foreign host (//host) is treated like any other
// foreign redirect — passed through, never fabricated into a base-prefixed
// local path that still resolves off-site.
func TestInstanceProxyProtocolRelativeLocationNotRebased(t *testing.T) {
	const ingressHeader = "/api/hassio_ingress/abc"
	const base = ingressHeader + "/i/alpha"
	responder := &proxyLocationResponder{}
	upstream, _ := newProxyUpstreamFixture(t, responder.handle)
	proxy := newProxyServerFixture(t, []Instance{{Name: "alpha", URL: upstream.URL}})

	responder.set(http.StatusFound, "//evil.example/x")
	resp := proxyGet(t, proxy.URL+"/i/alpha/x", map[string]string{ingressPathHeader: ingressHeader})
	resp.Body.Close()

	if got := resp.Header.Get("Location"); strings.HasPrefix(got, base) {
		t.Errorf("Location = %q was rebased under the base; a protocol-relative foreign host must not be", got)
	}
}
