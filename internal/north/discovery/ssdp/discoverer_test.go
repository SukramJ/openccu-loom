// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ssdp

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// New / defaults
// ---------------------------------------------------------------------------

func TestNewDefaults(t *testing.T) {
	t.Parallel()

	d := New(0, nil)
	if d.interval != 60*time.Second {
		t.Errorf("interval = %v, want 60s default for interval<=0", d.interval)
	}
	if d.logger == nil {
		t.Error("logger must default to slog.Default() when nil is passed")
	}
	if d.http == nil {
		t.Error("http client must be initialised")
	}
	if d.found == nil {
		t.Error("found map must be initialised")
	}

	d2 := New(-5*time.Second, nil)
	if d2.interval != 60*time.Second {
		t.Errorf("interval = %v, want 60s default for negative interval", d2.interval)
	}

	d3 := New(30*time.Second, nil)
	if d3.interval != 30*time.Second {
		t.Errorf("interval = %v, want 30s (explicit)", d3.interval)
	}
}

// ---------------------------------------------------------------------------
// fetch — httptest.Server-backed
// ---------------------------------------------------------------------------

func TestDiscovererFetch(t *testing.T) {
	t.Parallel()

	t.Run("valid CCU description", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(realOpenCCUXML))
		}))
		defer srv.Close()

		d := New(time.Minute, nil)
		ccu, ok := d.fetch(context.Background(), srv.URL+"/upnp/basic_dev.cgi")
		if !ok {
			t.Fatal("fetch: expected ok=true for a valid CCU description")
		}
		if ccu.Name != "Hausanlage" {
			t.Errorf("Name = %q, want Hausanlage", ccu.Name)
		}
	})

	t.Run("non-200 status is rejected", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		d := New(time.Minute, nil)
		_, ok := d.fetch(context.Background(), srv.URL+"/upnp/basic_dev.cgi")
		if ok {
			t.Fatal("fetch: expected ok=false for a non-200 response")
		}
	})

	t.Run("unparseable body is rejected", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not xml at all"))
		}))
		defer srv.Close()

		d := New(time.Minute, nil)
		_, ok := d.fetch(context.Background(), srv.URL+"/upnp/basic_dev.cgi")
		if ok {
			t.Fatal("fetch: expected ok=false for an unparseable body")
		}
	})

	t.Run("non-CCU manufacturer is rejected", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(sonosXML))
		}))
		defer srv.Close()

		d := New(time.Minute, nil)
		_, ok := d.fetch(context.Background(), srv.URL+"/upnp/basic_dev.cgi")
		if ok {
			t.Fatal("fetch: expected ok=false for a non-CCU manufacturer")
		}
	})

	t.Run("body larger than maxDescriptionBytes is truncated not failed", func(t *testing.T) {
		t.Parallel()
		// A body larger than maxDescriptionBytes is read via io.LimitReader,
		// so truncation itself must not fail the fetch — the resulting XML
		// happens to still be unparseable here, so ok is false, but via the
		// XML-parse path rather than an io error.
		big := make([]byte, maxDescriptionBytes+1024)
		for i := range big {
			big[i] = 'a'
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(big)
		}))
		defer srv.Close()

		d := New(time.Minute, nil)
		_, ok := d.fetch(context.Background(), srv.URL+"/upnp/basic_dev.cgi")
		if ok {
			t.Fatal("fetch: expected ok=false for an oversized, non-XML body")
		}
	})

	t.Run("invalid request URL is rejected", func(t *testing.T) {
		t.Parallel()
		d := New(time.Minute, nil)
		_, ok := d.fetch(context.Background(), "://not-a-url")
		if ok {
			t.Fatal("fetch: expected ok=false for a malformed location URL")
		}
	})

	t.Run("unreachable host is rejected", func(t *testing.T) {
		t.Parallel()
		d := New(time.Minute, nil)
		_, ok := d.fetch(context.Background(), "http://127.0.0.1:1/upnp/basic_dev.cgi")
		if ok {
			t.Fatal("fetch: expected ok=false when the connection fails")
		}
	})
}

// ---------------------------------------------------------------------------
// List — sort order + nil-receiver guard
// ---------------------------------------------------------------------------

func TestDiscovererListNilReceiver(t *testing.T) {
	t.Parallel()
	var d *Discoverer
	if got := d.List(); got != nil {
		t.Errorf("List() on a nil *Discoverer = %+v, want nil", got)
	}
}

func TestDiscovererListEmptyIsNeverNil(t *testing.T) {
	t.Parallel()
	d := New(time.Minute, nil)
	got := d.List()
	if got == nil {
		t.Fatal("List() on an empty Discoverer must return a non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("List() = %+v, want empty", got)
	}
}

func TestDiscovererListSortsByNameThenSerial(t *testing.T) {
	t.Parallel()
	d := New(time.Minute, nil)
	d.found = map[string]DiscoveredCCU{
		"s3": {Serial: "s3", Name: "Zeta"},
		"s1": {Serial: "s1", Name: "Alpha"},
		"s2": {Serial: "s2", Name: "Alpha"}, // same name as s1 → tie-break on Serial
	}
	got := d.List()
	if len(got) != 3 {
		t.Fatalf("List() len = %d, want 3", len(got))
	}
	wantOrder := []string{"s1", "s2", "s3"}
	for i, want := range wantOrder {
		if got[i].Serial != want {
			t.Errorf("List()[%d].Serial = %q, want %q (order=%+v)", i, got[i].Serial, want, got)
		}
	}
}

// ---------------------------------------------------------------------------
// scan — stale-entry eviction via the injectable now func
// ---------------------------------------------------------------------------

func TestDiscovererScanEvictsStaleEntries(t *testing.T) {
	t.Parallel()

	d := New(time.Minute, nil)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return base }

	// Seed one fresh and one already-stale entry directly (scan() only
	// evicts; multicastSourceIPs()/searchFrom() are left untested here as
	// they need real UDP sockets).
	d.found = map[string]DiscoveredCCU{
		"fresh": {Serial: "fresh", Name: "Fresh", LastSeen: base},
		"stale": {Serial: "stale", Name: "Stale", LastSeen: base.Add(-staleAfter - time.Second)},
	}

	// scan() calls multicastSourceIPs()/searchFrom() first, which is a real
	// network operation; eviction runs unconditionally afterward regardless
	// of what (if anything) those calls found, so we can assert on it
	// directly. An already-cancelled context makes searchFrom return without
	// waiting out its M-SEARCH read deadline — that wait says nothing about
	// eviction and costs several seconds per call.
	d.scan(cancelledContext(t))

	got := d.List()
	if len(got) != 1 {
		t.Fatalf("List() after scan = %+v, want exactly the fresh entry", got)
	}
	if got[0].Serial != "fresh" {
		t.Errorf("List()[0].Serial = %q, want fresh", got[0].Serial)
	}
}

// TestDiscovererScanStopsProbingOnCancel pins that a scan interrupted by
// shutdown does not walk the remaining interfaces. Each probe blocks for the
// M-SEARCH read deadline (~3s) and a cancelled context does not shorten it, so
// on a multi-homed host the daemon's Stop would otherwise wait out one deadline
// per remaining source address before the loop could exit.
func TestDiscovererScanStopsProbingOnCancel(t *testing.T) {
	t.Parallel()

	d := New(time.Minute, nil)
	d.sourceIPs = func() []net.IP {
		return []net.IP{net.IPv4(10, 0, 0, 1), net.IPv4(10, 0, 0, 2), net.IPv4(10, 0, 0, 3)}
	}
	ctx, cancel := context.WithCancel(context.Background())
	probes := 0
	d.search = func(context.Context, net.IP) ([]string, error) {
		probes++
		cancel() // shutdown lands while the first probe is in flight
		return nil, nil
	}

	d.scan(ctx)

	if probes != 1 {
		t.Errorf("probed %d source addresses after cancellation, want 1", probes)
	}
}

// cancelledContext returns a context that is already done, so the M-SEARCH
// read loop in searchFrom exits on its first check instead of blocking until
// the SSDP read deadline expires.
func cancelledContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestDiscovererScanKeepsFreshEntries(t *testing.T) {
	t.Parallel()

	d := New(time.Minute, nil)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return base }
	d.found = map[string]DiscoveredCCU{
		"fresh": {Serial: "fresh", Name: "Fresh", LastSeen: base.Add(-staleAfter + time.Second)},
	}

	d.scan(cancelledContext(t))

	got := d.List()
	if len(got) != 1 {
		t.Fatalf("List() after scan = %+v, want the not-yet-stale entry kept", got)
	}
}

// ---------------------------------------------------------------------------
// Start / Stop lifecycle
// ---------------------------------------------------------------------------

func TestDiscovererStartStop(t *testing.T) {
	t.Parallel()

	d := New(10*time.Millisecond, nil)
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Stop must return promptly and not hang even though the loop runs a
	// real scan() (which touches the network) on entry.
	done := make(chan struct{})
	go func() {
		_ = d.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop() did not return within 10s")
	}
}
