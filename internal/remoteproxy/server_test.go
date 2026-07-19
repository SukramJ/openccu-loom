// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package remoteproxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// serverTRecorder captures the last request an upstream test server saw.
// The proxy dials the upstream over a real loopback connection, so the
// recorder is written from the server's own goroutine; every field is
// read back through the mutex to stay race-clean.
type serverTRecorder struct {
	mu         sync.Mutex
	hit        bool
	requestURI string
	body       string
}

func (r *serverTRecorder) record(req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hit = true
	r.requestURI = req.RequestURI
	r.body = string(body)
}

func (r *serverTRecorder) snapshot() (hit bool, requestURI, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hit, r.requestURI, r.body
}

// serverTNewUpstream starts a recording upstream test server that answers
// every request with a fixed 200 body.
func serverTNewUpstream(t *testing.T, respBody string) (*httptest.Server, *serverTRecorder) {
	t.Helper()
	rec := &serverTRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// serverTLogger is a discarding logger for tests that do not assert on
// log output.
func serverTLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// serverTNewServer builds a Server for the given instance names, each
// backed by its own recording upstream, and returns the built http.Handler
// alongside the per-instance recorders keyed by instance name.
func serverTNewServer(t *testing.T, names ...string) (handler http.Handler, recorders map[string]*serverTRecorder) {
	t.Helper()
	recs := make(map[string]*serverTRecorder, len(names))
	var opts Options
	for _, name := range names {
		upstream, rec := serverTNewUpstream(t, name+"-ok")
		recs[name] = rec
		opts.Instances = append(opts.Instances, Instance{Name: name, URL: upstream.URL})
	}
	srv, err := New(opts, serverTLogger())
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	return srv.Handler(), recs
}

func TestServerSingleInstanceIsTransparent(t *testing.T) {
	handler, recs := serverTNewServer(t, "solo")
	rec := recs["solo"]

	req := httptest.NewRequest(http.MethodGet, "/any/path?q=1", http.NoBody)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", rw.Code, http.StatusOK)
	}
	if _, uri, _ := rec.snapshot(); uri != "/any/path?q=1" {
		t.Errorf("upstream requestURI = %q, want %q", uri, "/any/path?q=1")
	}

	body := "hello-body"
	req2 := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(body))
	rw2 := httptest.NewRecorder()
	handler.ServeHTTP(rw2, req2)

	if rw2.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want %d", rw2.Code, http.StatusOK)
	}
	if _, _, gotBody := rec.snapshot(); gotBody != body {
		t.Errorf("upstream body = %q, want %q", gotBody, body)
	}
}

func TestServerMultiInstanceRouting(t *testing.T) {
	handler, recs := serverTNewServer(t, "a", "b")
	recA, recB := recs["a"], recs["b"]

	req := httptest.NewRequest(http.MethodGet, "/i/a/api/v1/devices?x=2", http.NoBody)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rw.Code, http.StatusOK)
	}
	hitA, uriA, _ := recA.snapshot()
	if !hitA {
		t.Fatal("instance a: expected upstream to be hit, was not")
	}
	if uriA != "/api/v1/devices?x=2" {
		t.Errorf("instance a requestURI = %q, want %q", uriA, "/api/v1/devices?x=2")
	}
	if hitB, _, _ := recB.snapshot(); hitB {
		t.Error("instance b: expected upstream not to be hit, but it was")
	}

	t.Run("escaped path segment keeps its escaping", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/i/a/foo%2Fbar", http.NoBody)
		rw := httptest.NewRecorder()
		handler.ServeHTTP(rw, req)

		if rw.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rw.Code, http.StatusOK)
		}
		if _, uri, _ := recA.snapshot(); uri != "/foo%2Fbar" {
			t.Errorf("instance a requestURI = %q, want %q", uri, "/foo%2Fbar")
		}
	})
}

func TestServerBareInstanceMountRedirects(t *testing.T) {
	handler, _ := serverTNewServer(t, "a", "b")

	t.Run("with ingress path header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/i/a", http.NoBody)
		req.Header.Set(ingressPathHeader, "/api/hassio_ingress/abc123")
		rw := httptest.NewRecorder()
		handler.ServeHTTP(rw, req)

		if rw.Code != http.StatusPermanentRedirect {
			t.Fatalf("status = %d, want %d", rw.Code, http.StatusPermanentRedirect)
		}
		want := "/api/hassio_ingress/abc123/i/a/"
		if got := rw.Header().Get("Location"); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	})

	t.Run("without ingress path header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/i/a", http.NoBody)
		rw := httptest.NewRecorder()
		handler.ServeHTTP(rw, req)

		if rw.Code != http.StatusPermanentRedirect {
			t.Fatalf("status = %d, want %d", rw.Code, http.StatusPermanentRedirect)
		}
		if got := rw.Header().Get("Location"); got != "/i/a/" {
			t.Errorf("Location = %q, want %q", got, "/i/a/")
		}
	})
}

func TestServerUnknownInstanceRedirectsToOverview(t *testing.T) {
	handler, _ := serverTNewServer(t, "a", "b")

	req := httptest.NewRequest(http.MethodGet, "/i/zzz/", http.NoBody)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rw.Code, http.StatusFound)
	}
	if got := rw.Header().Get("Location"); got != "/" {
		t.Errorf("Location = %q, want %q", got, "/")
	}
}

func TestServerOverviewPage(t *testing.T) {
	handler, _ := serverTNewServer(t, "a", "b")

	t.Run("default locale is English", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		rw := httptest.NewRecorder()
		handler.ServeHTTP(rw, req)

		if rw.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rw.Code, http.StatusOK)
		}
		if ct := rw.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html prefix", ct)
		}
		body := rw.Body.String()
		if !strings.Contains(body, `href="i/a/"`) {
			t.Errorf("body missing link to instance a: %s", body)
		}
		if !strings.Contains(body, `href="i/b/"`) {
			t.Errorf("body missing link to instance b: %s", body)
		}
		if !strings.Contains(body, `lang="en"`) {
			t.Errorf("body missing lang=%q: %s", "en", body)
		}
	})

	t.Run("Accept-Language de wins over en", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("Accept-Language", "de-DE,de")
		rw := httptest.NewRecorder()
		handler.ServeHTTP(rw, req)

		body := rw.Body.String()
		if !strings.Contains(body, `lang="de"`) {
			t.Errorf("body missing lang=%q: %s", "de", body)
		}
	})
}

func TestServerStrayPathRedirectsToOverview(t *testing.T) {
	handler, _ := serverTNewServer(t, "a", "b")

	req := httptest.NewRequest(http.MethodGet, "/nonsense", http.NoBody)
	req.Header.Set(ingressPathHeader, "/api/hassio_ingress/abc123")
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rw.Code, http.StatusFound)
	}
	want := "/api/hassio_ingress/abc123/"
	if got := rw.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestServerBareInstanceMountRedirectPreservesQuery(t *testing.T) {
	handler, _ := serverTNewServer(t, "a", "b")

	t.Run("without ingress path header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/i/a?tab=devices", http.NoBody)
		rw := httptest.NewRecorder()
		handler.ServeHTTP(rw, req)

		if rw.Code != http.StatusPermanentRedirect {
			t.Fatalf("status = %d, want %d", rw.Code, http.StatusPermanentRedirect)
		}
		want := "/i/a/?tab=devices"
		if got := rw.Header().Get("Location"); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	})

	t.Run("with ingress path header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/i/a?tab=devices", http.NoBody)
		req.Header.Set(ingressPathHeader, "/api/hassio_ingress/abc123")
		rw := httptest.NewRecorder()
		handler.ServeHTTP(rw, req)

		if rw.Code != http.StatusPermanentRedirect {
			t.Fatalf("status = %d, want %d", rw.Code, http.StatusPermanentRedirect)
		}
		want := "/api/hassio_ingress/abc123/i/a/?tab=devices"
		if got := rw.Header().Get("Location"); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	})
}

func TestServerRootPostMethodNotAllowed(t *testing.T) {
	handler, _ := serverTNewServer(t, "a", "b")

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body"))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rw.Code, http.StatusMethodNotAllowed)
	}
	if got := rw.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
	}
	if got := rw.Header().Get("Location"); got != "" {
		t.Errorf("Location = %q, want empty (no redirect)", got)
	}
}
