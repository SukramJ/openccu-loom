// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func testCUxDBackoff() []time.Duration {
	return []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// The CUxD addon starts independently of ReGaHss, so the first ingest
// regularly fails against a CCU that already reports ready. The activation
// must retry instead of leaving the interface permanently empty.
func TestRunCUxDActivationRetriesUntilIngestSucceeds(t *testing.T) {
	t.Parallel()

	calls := 0
	ingested := runCUxDActivation(context.Background(), testCUxDBackoff(),
		func(context.Context) error {
			calls++
			if calls < 3 {
				return errors.New("list devices: connection refused")
			}
			return nil
		},
		nil, "ccu", "ccu-CUxD", discardLogger())

	if calls != 3 {
		t.Fatalf("activate calls = %d, want 3 (two failures then success)", calls)
	}
	if !ingested {
		t.Fatal("runCUxDActivation reported ingested=false after the retry succeeded")
	}
}

// A permanently failing ingest exhausts the schedule and leaves the client
// in DISCONNECTED, the only state from which the recovery pipeline can
// reconnect the interface later.
func TestRunCUxDActivationExhaustsRetriesAndDisconnectsClient(t *testing.T) {
	t.Parallel()

	ic, err := client.New(client.Config{
		CentralName: "ccu",
		Interface:   hmenum.InterfaceCUxD,
		Caller:      client.CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
		Enabled:     true,
		Logger:      discardLogger(),
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	backoff := testCUxDBackoff()
	calls := 0
	ingested := runCUxDActivation(context.Background(), backoff,
		func(context.Context) error {
			calls++
			return errors.New("list devices: connection refused")
		},
		ic, "ccu", "ccu-CUxD", discardLogger())

	if want := len(backoff) + 1; calls != want {
		t.Fatalf("activate calls = %d, want %d", calls, want)
	}
	if got := ic.ClientState(); got != hmenum.ClientStateDisconnected {
		t.Fatalf("client state = %s, want %s", got, hmenum.ClientStateDisconnected)
	}
	// This is the readiness-tally defect's regression guard: a CUxD interface
	// that exhausts every retry must report ingested=false, the same as its
	// XML-RPC sibling, so wireInterface does not count it toward "interfaces
	// loaded" over a central that is actually still partly dark.
	if ingested {
		t.Fatal("runCUxDActivation reported ingested=true for a permanently failing ingest")
	}
}

// Teardown during the retry window must abort the wait immediately rather
// than blocking the bring-up goroutine for the rest of the schedule.
func TestRunCUxDActivationStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	var ingested bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		ingested = runCUxDActivation(ctx, []time.Duration{time.Hour, time.Hour},
			func(context.Context) error {
				calls++
				cancel()
				return errors.New("list devices: connection refused")
			},
			nil, "ccu", "ccu-CUxD", discardLogger())
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runCUxDActivation did not return after context cancel")
	}
	if calls != 1 {
		t.Fatalf("activate calls = %d, want 1", calls)
	}
	if ingested {
		t.Fatal("ingested=true after context cancel")
	}
}
