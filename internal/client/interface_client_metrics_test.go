// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type metricsFakeCaller struct {
	calls atomic.Int32
	reply any
	err   error
}

func (f *metricsFakeCaller) Call(_ context.Context, _ string, _ []any) (any, error) {
	f.calls.Add(1)
	return f.reply, f.err
}

func newMetricsTestClient(t *testing.T, caller Caller) *InterfaceClient {
	t.Helper()
	ic, err := New(Config{
		CentralName: "ccu-A",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      caller,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(ic.Close)
	return ic
}

func TestInterfaceClientMetricsSurfaceLiveCounters(t *testing.T) {
	t.Parallel()

	ic := newMetricsTestClient(t, &metricsFakeCaller{reply: "ok"})

	if got := ic.MetricsTotalRequests(); got != 0 {
		t.Fatalf("baseline total=%d, want 0", got)
	}
	if got := ic.MetricsExecutedRequests(); got != 0 {
		t.Fatalf("baseline executed=%d, want 0", got)
	}
	if got := ic.MetricsCircuitState(); got != 0 {
		t.Fatalf("baseline circuit=%d, want 0 (closed)", got)
	}
	if _, ok := ic.MetricsLastFailureTime(); ok {
		t.Fatal("baseline last-failure should be zero")
	}

	for range 3 {
		_, _ = ic.Call(context.Background(), "x", nil, hmenum.CommandPriorityLow, "")
	}
	if got := ic.MetricsTotalRequests(); got != 3 {
		t.Errorf("total=%d, want 3", got)
	}
	if got := ic.MetricsExecutedRequests(); got != 3 {
		t.Errorf("executed=%d, want 3", got)
	}
	if got := ic.MetricsPendingRequests(); got != 0 {
		t.Errorf("pending=%d, want 0 after Call returns", got)
	}
}

func TestInterfaceClientMetricsLastFailureTimestamp(t *testing.T) {
	t.Parallel()

	caller := &metricsFakeCaller{err: errors.New("boom")}
	ic, err := New(Config{
		CentralName: "ccu-A",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      caller,
		// Avoid retry padding the duration.
		Retrier: reliability.NewRetrier(reliability.RetryConfig{MaxAttempts: 1}),
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer ic.Close()

	before := time.Now()
	_, _ = ic.Call(context.Background(), "x", nil, hmenum.CommandPriorityLow, "")

	failedAt, ok := ic.MetricsLastFailureTime()
	if !ok {
		t.Fatal("expected last-failure recorded")
	}
	if failedAt.Before(before) {
		t.Errorf("failure ts %v is before call (%v)", failedAt, before)
	}
}

func TestInterfaceClientMetricsRaceSafe(t *testing.T) {
	t.Parallel()

	ic := newMetricsTestClient(t, &metricsFakeCaller{reply: "ok"})

	const goroutines = 8
	const calls = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range calls {
				_, _ = ic.Call(context.Background(), "m", nil, hmenum.CommandPriorityLow, "")
			}
		}()
	}
	// Concurrent readers.
	wg.Go(func() {
		for range goroutines * calls {
			_ = ic.MetricsTotalRequests()
			_ = ic.MetricsExecutedRequests()
			_ = ic.MetricsPendingRequests()
			_ = ic.MetricsCircuitState()
			_, _ = ic.MetricsLastFailureTime()
		}
	})
	wg.Wait()

	if got := ic.MetricsTotalRequests(); got != goroutines*calls {
		t.Errorf("total=%d, want %d", got, goroutines*calls)
	}
}

func TestMetricsClientProviderRegistersAndDeregisters(t *testing.T) {
	t.Parallel()

	p := NewMetricsClientProvider("ccu-A")
	if got := p.CentralName(); got != "ccu-A" {
		t.Errorf("scope=%q", got)
	}
	if got := p.Snapshot(); len(got) != 0 {
		t.Errorf("baseline snapshot len=%d", len(got))
	}

	ic := newMetricsTestClient(t, &metricsFakeCaller{})
	p.Register(ic)
	p.Register(nil) // ignored.
	if got := p.Snapshot(); len(got) != 1 {
		t.Errorf("after register len=%d, want 1", len(got))
	}
	p.Deregister(hmenum.InterfaceHmIPRF)
	if got := p.Snapshot(); len(got) != 0 {
		t.Errorf("after deregister len=%d, want 0", len(got))
	}
}

func TestMetricsClientProviderMultiCCUScopes(t *testing.T) {
	t.Parallel()

	pa := NewMetricsClientProvider("ccu-A")
	pb := NewMetricsClientProvider("ccu-B")

	icA, _ := New(Config{
		CentralName: "ccu-A", Interface: hmenum.InterfaceHmIPRF,
		Caller: &metricsFakeCaller{},
	})
	defer icA.Close()
	icB, _ := New(Config{
		CentralName: "ccu-B", Interface: hmenum.InterfaceHmIPRF,
		Caller: &metricsFakeCaller{},
	})
	defer icB.Close()

	pa.Register(icA)
	pb.Register(icB)

	if pa.CentralName() == pb.CentralName() {
		t.Fatal("scopes leaked across providers")
	}
	if got := pa.Snapshot(); len(got) != 1 || got[0].CentralName() != "ccu-A" {
		t.Errorf("ccu-A snapshot wrong: %+v", got)
	}
	if got := pb.Snapshot(); len(got) != 1 || got[0].CentralName() != "ccu-B" {
		t.Errorf("ccu-B snapshot wrong: %+v", got)
	}
}
