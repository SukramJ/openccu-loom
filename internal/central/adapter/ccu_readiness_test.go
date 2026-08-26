// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// ccFor builds a CentralConfig pointing at the test server's host:port so
// ccuBaseURLFor composes the same base the probe hits.
func ccFor(t *testing.T, serverURL string) config.CentralConfig {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("atoi port: %v", err)
	}
	return config.CentralConfig{Name: "t", Host: host, JSONRPCPort: port}
}

func TestWaitForCCUReady_ReadyImmediately(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != checkRegaPath {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("OK"))
	}))
	defer srv.Close()

	ok := WaitForCCUReady(context.Background(), ccFor(t, srv.URL),
		CCUReadinessConfig{Timeout: time.Second, Interval: 10 * time.Millisecond}, nil)
	if !ok {
		t.Fatal("WaitForCCUReady = false, want true for a CCU answering OK")
	}
}

func TestWaitForCCUReady_BootingThenReady(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// First two probes report "still booting"; the third flips to OK.
		if hits.Add(1) < 3 {
			_, _ = w.Write([]byte("ReGaHss not ready"))
			return
		}
		_, _ = w.Write([]byte("OK\n"))
	}))
	defer srv.Close()

	ok := WaitForCCUReady(context.Background(), ccFor(t, srv.URL),
		CCUReadinessConfig{Timeout: 2 * time.Second, Interval: 5 * time.Millisecond}, nil)
	if !ok {
		t.Fatal("WaitForCCUReady = false, want true once the CCU flips to OK")
	}
	if got := hits.Load(); got < 3 {
		t.Fatalf("probed %d times, want >= 3 (should keep polling until OK)", got)
	}
}

func TestWaitForCCUReady_TimesOutWhileBooting(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not ready")) // never OK
	}))
	defer srv.Close()

	ok := WaitForCCUReady(context.Background(), ccFor(t, srv.URL),
		CCUReadinessConfig{Timeout: 40 * time.Millisecond, Interval: 5 * time.Millisecond}, nil)
	if ok {
		t.Fatal("WaitForCCUReady = true, want false when the CCU never returns OK before timeout")
	}
}

func TestWaitForCCUReady_NonOKStatusKeepsWaiting(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal backend exception", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ok := WaitForCCUReady(context.Background(), ccFor(t, srv.URL),
		CCUReadinessConfig{Timeout: 30 * time.Millisecond, Interval: 5 * time.Millisecond}, nil)
	if ok {
		t.Fatal("WaitForCCUReady = true, want false while the CCU answers 503")
	}
}

func TestWaitForCCUReady_UnboundedKeepsWaitingUntilReady(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Stay "booting" well past where any bounded default would have given
		// up, then flip to OK — proves Timeout<0 never abandons the CCU.
		if hits.Add(1) < 6 {
			http.Error(w, "service not available", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("OK"))
	}))
	defer srv.Close()

	ok := WaitForCCUReady(context.Background(), ccFor(t, srv.URL),
		CCUReadinessConfig{Timeout: -1, Interval: 5 * time.Millisecond}, nil)
	if !ok {
		t.Fatal("unbounded WaitForCCUReady = false, want true once the CCU finally returns OK")
	}
	if got := hits.Load(); got < 6 {
		t.Fatalf("probed %d times, want >= 6 (unbounded must keep polling)", got)
	}
}

func TestWaitForCCUReady_UnboundedStopsOnCancel(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("booting")) // never OK
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()

	ok := WaitForCCUReady(ctx, ccFor(t, srv.URL),
		CCUReadinessConfig{Timeout: -1, Interval: 5 * time.Millisecond}, nil)
	if ok {
		t.Fatal("unbounded WaitForCCUReady = true, want false after ctx cancel")
	}
}

func TestWaitForCCUReady_ContextCancel(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("booting"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	ok := WaitForCCUReady(ctx, ccFor(t, srv.URL),
		CCUReadinessConfig{Timeout: time.Minute, Interval: 5 * time.Millisecond}, nil)
	if ok {
		t.Fatal("WaitForCCUReady = true, want false when ctx is cancelled")
	}
}
