// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package remoteproxy

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// pollTGoroutineLeakThreshold is the maximum tolerated goroutine-count
// delta once a poll loop is expected to have exited. A small non-zero
// budget accounts for Go runtime workers (GC, finalizer) that may still
// be settling between measurements.
const pollTGoroutineLeakThreshold = 3

// pollTLogger returns a discard logger so probes exercised on purpose
// (unreachable upstream, malformed info body) do not spam test output.
func pollTLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// pollTResponse is one fixed status/body pair a pollTUpstream fixture
// answers with for a single probe path.
type pollTResponse struct {
	status int
	body   string
}

// pollTRecorder captures the Authorization header seen per request path,
// so a test can assert token injection on the health and info probes
// independently.
type pollTRecorder struct {
	mu   sync.Mutex
	auth map[string]string
}

func (r *pollTRecorder) record(path string, header http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.auth == nil {
		r.auth = make(map[string]string)
	}
	r.auth[path] = header.Get("Authorization")
}

func (r *pollTRecorder) authFor(path string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.auth[path]
}

// pollTUpstream starts a fake upstream that answers GET /api/v1/health and
// GET /api/v1/info from fixed status/body pairs, recording the
// Authorization header seen on each path.
func pollTUpstream(t *testing.T, health, info pollTResponse) (*httptest.Server, *pollTRecorder) {
	t.Helper()
	rec := &pollTRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r.URL.Path, r.Header)
		var resp pollTResponse
		switch r.URL.Path {
		case "/api/v1/health":
			resp = health
		case "/api/v1/info":
			resp = info
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// pollTNewInstanceProxy builds a bare instanceProxy against the given
// upstream URL, mirroring what Server's constructor wires for one
// instance, without going through Options validation.
func pollTNewInstanceProxy(t *testing.T, name, url, token string) *instanceProxy {
	t.Helper()
	ip, err := newInstanceProxy(Instance{Name: name, URL: url, Token: token}, "", pollTLogger())
	if err != nil {
		t.Fatalf("newInstanceProxy: %v", err)
	}
	return ip
}

// pollTNewServer builds a Server for the given instances via New(),
// giving a test access to the unexported poller and instanceProxy fields.
func pollTNewServer(t *testing.T, instances []Instance) *Server {
	t.Helper()
	srv, err := New(Options{Instances: instances}, pollTLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

// pollTClosedUpstreamURL returns a URL whose port is guaranteed to refuse
// connections: a listener is opened and immediately closed, so the
// address is syntactically valid but nothing answers on it.
func pollTClosedUpstreamURL(t *testing.T) string {
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

// pollTEventuallyGoroutineDelta polls runtime.NumGoroutine for up to total
// duration, returning once the delta against baseline drops at or below
// threshold, or the deadline passes — whichever comes first.
func pollTEventuallyGoroutineDelta(baseline, threshold int, total time.Duration) int {
	deadline := time.Now().Add(total)
	delta := runtime.NumGoroutine() - baseline
	for time.Now().Before(deadline) && delta > threshold {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
		delta = runtime.NumGoroutine() - baseline
	}
	return delta
}

func TestPollerProbeHealthyFillsVersionAndInjectsToken(t *testing.T) {
	upstream, rec := pollTUpstream(
		t,
		pollTResponse{status: http.StatusOK, body: `{"status":"healthy"}`},
		pollTResponse{status: http.StatusOK, body: `{"version":"1.2.3","api_version":"2.27.0","uptime":"3h"}`},
	)
	// A second, non-dialed instance is enough to put Server in
	// multi-instance mode so the poller field is populated; the poller is
	// then reached through the Server itself rather than built directly.
	srv := pollTNewServer(t, []Instance{
		{Name: "alpha", URL: upstream.URL, Token: "tok"},
		{Name: "beta", URL: "http://127.0.0.1:1"},
	})

	st := srv.poller.probe(context.Background(), srv.instances[0])

	if st.Name != "alpha" {
		t.Errorf("Name = %q, want %q", st.Name, "alpha")
	}
	if st.Status != "healthy" {
		t.Errorf("Status = %q, want %q", st.Status, "healthy")
	}
	if st.Version != "1.2.3" {
		t.Errorf("Version = %q, want %q", st.Version, "1.2.3")
	}
	if st.APIVersion != "2.27.0" {
		t.Errorf("APIVersion = %q, want %q", st.APIVersion, "2.27.0")
	}
	if st.Uptime != "3h" {
		t.Errorf("Uptime = %q, want %q", st.Uptime, "3h")
	}
	if st.CheckedAt.IsZero() {
		t.Error("CheckedAt is zero, want a timestamp")
	}
	if got := rec.authFor("/api/v1/health"); got != "Bearer tok" {
		t.Errorf("health Authorization = %q, want %q", got, "Bearer tok")
	}
	if got := rec.authFor("/api/v1/info"); got != "Bearer tok" {
		t.Errorf("info Authorization = %q, want %q", got, "Bearer tok")
	}
}

func TestPollerProbeNoTokenSendsNoAuthorization(t *testing.T) {
	upstream, rec := pollTUpstream(
		t,
		pollTResponse{status: http.StatusOK, body: `{"status":"healthy"}`},
		pollTResponse{status: http.StatusOK, body: `{}`},
	)
	ip := pollTNewInstanceProxy(t, "alpha", upstream.URL, "")
	p := newPoller([]*instanceProxy{ip}, clock.New(), pollTLogger())

	_ = p.probe(context.Background(), ip)

	if got := rec.authFor("/api/v1/health"); got != "" {
		t.Errorf("Authorization = %q, want empty", got)
	}
}

func TestPollerProbeUnhealthyStatusCodeIsIgnored(t *testing.T) {
	// /api/v1/health answers 503 when the remote is unhealthy but still
	// carries the JSON body; the probe must read the status field, not
	// the HTTP status code.
	upstream, _ := pollTUpstream(
		t,
		pollTResponse{status: http.StatusServiceUnavailable, body: `{"status":"unhealthy"}`},
		pollTResponse{status: http.StatusOK, body: `{}`},
	)
	ip := pollTNewInstanceProxy(t, "alpha", upstream.URL, "")
	p := newPoller([]*instanceProxy{ip}, clock.New(), pollTLogger())

	st := p.probe(context.Background(), ip)

	if st.Status != "unhealthy" {
		t.Errorf("Status = %q, want %q", st.Status, "unhealthy")
	}
}

func TestPollerProbeUnreachableUpstream(t *testing.T) {
	ip := pollTNewInstanceProxy(t, "alpha", pollTClosedUpstreamURL(t), "")
	p := newPoller([]*instanceProxy{ip}, clock.New(), pollTLogger())

	st := p.probe(context.Background(), ip)

	if st.Status != statusUnreachable {
		t.Errorf("Status = %q, want %q", st.Status, statusUnreachable)
	}
}

func TestPollerProbeInfoEndpointIsBestEffort(t *testing.T) {
	t.Run("info endpoint returns a server error", func(t *testing.T) {
		upstream, _ := pollTUpstream(
			t,
			pollTResponse{status: http.StatusOK, body: `{"status":"healthy"}`},
			pollTResponse{status: http.StatusInternalServerError, body: ``},
		)
		ip := pollTNewInstanceProxy(t, "alpha", upstream.URL, "")
		p := newPoller([]*instanceProxy{ip}, clock.New(), pollTLogger())

		st := p.probe(context.Background(), ip)

		if st.Status != "healthy" {
			t.Errorf("Status = %q, want %q", st.Status, "healthy")
		}
		if st.Version != "" {
			t.Errorf("Version = %q, want empty", st.Version)
		}
	})

	t.Run("info endpoint returns malformed JSON", func(t *testing.T) {
		upstream, _ := pollTUpstream(
			t,
			pollTResponse{status: http.StatusOK, body: `{"status":"healthy"}`},
			pollTResponse{status: http.StatusOK, body: `not json`},
		)
		ip := pollTNewInstanceProxy(t, "alpha", upstream.URL, "")
		p := newPoller([]*instanceProxy{ip}, clock.New(), pollTLogger())

		st := p.probe(context.Background(), ip)

		if st.Status != "healthy" {
			t.Errorf("Status = %q, want %q", st.Status, "healthy")
		}
		if st.Version != "" {
			t.Errorf("Version = %q, want empty", st.Version)
		}
		if st.Uptime != "" {
			t.Errorf("Uptime = %q, want empty", st.Uptime)
		}
	})
}

func TestPollerSnapshotOrderAndUnknownDefault(t *testing.T) {
	names := []string{"c", "a", "b"}
	instances := make([]*instanceProxy, len(names))
	for i, name := range names {
		// No request is ever issued against these instances in this
		// test, so the URL only needs to parse — it does not need a
		// listener behind it.
		instances[i] = pollTNewInstanceProxy(t, name, "http://127.0.0.1:1", "")
	}
	p := newPoller(instances, clock.New(), pollTLogger())

	got := p.snapshot()
	if len(got) != len(names) {
		t.Fatalf("snapshot length = %d, want %d", len(got), len(names))
	}
	for i, name := range names {
		if got[i].Name != name {
			t.Errorf("snapshot[%d].Name = %q, want %q", i, got[i].Name, name)
		}
		if got[i].Status != statusUnknown {
			t.Errorf("snapshot[%d].Status = %q, want %q", i, got[i].Status, statusUnknown)
		}
	}
}

func TestPollerStartStopsOnContextCancel(t *testing.T) {
	upstream, _ := pollTUpstream(
		t,
		pollTResponse{status: http.StatusOK, body: `{"status":"healthy"}`},
		pollTResponse{status: http.StatusOK, body: `{}`},
	)
	ip := pollTNewInstanceProxy(t, "alpha", upstream.URL, "")
	p := newPoller([]*instanceProxy{ip}, clock.New(), pollTLogger())
	p.interval = 5 * time.Millisecond // bounds the test to a short custom tick

	runtime.GC()
	baseline := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	p.start(ctx)

	// Wait for the loop to actually run at least once (not merely be
	// scheduled) before exercising the cancel path.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.snapshot()[0].Status != statusUnknown {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := p.snapshot()[0].Status; got != "healthy" {
		t.Fatalf("status before cancel = %q, want %q (loop never ran)", got, "healthy")
	}

	cancel()

	if delta := pollTEventuallyGoroutineDelta(baseline, pollTGoroutineLeakThreshold, 2*time.Second); delta > pollTGoroutineLeakThreshold {
		t.Errorf("goroutine delta after cancel = %d, want <= %d (poll loop leaked)", delta, pollTGoroutineLeakThreshold)
	}
}
