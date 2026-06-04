// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func keyFor(parameter string) hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "0001ABCD:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      parameter,
	}
}

func TestRetrierDoForKeySupersededByNewerCall(t *testing.T) {
	r := NewRetrier(RetryConfig{
		MaxAttempts:              3,
		Initial:                  50 * time.Millisecond,
		Max:                      50 * time.Millisecond,
		Multiplier:               1,
		DutyCycleDelay:           1 * time.Millisecond,
		TransmissionPendingDelay: 1 * time.Millisecond,
	})

	key := keyFor("LEVEL")
	first := make(chan error, 1)
	started := make(chan struct{})

	go func() {
		first <- r.DoForKey(context.Background(), key, func(_ context.Context, _ int) error {
			select {
			case <-started:
			default:
				close(started)
			}
			return errors.New("transient")
		})
	}()

	// Wait until the first call entered the loop and is sleeping in
	// its backoff, then issue a second call for the same key.
	<-started
	time.Sleep(10 * time.Millisecond)

	// Second call succeeds immediately to keep the test fast.
	if err := r.DoForKey(context.Background(), key, func(_ context.Context, _ int) error {
		return nil
	}); err != nil {
		t.Fatalf("second call: %v", err)
	}

	select {
	case err := <-first:
		if !errors.Is(err, ErrRetrySuperseded) {
			t.Fatalf("first call expected ErrRetrySuperseded, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first call did not return after supersede")
	}

	if got := r.ActiveRetryCount(); got != 0 {
		t.Fatalf("ActiveRetryCount=%d, want 0 after both finished", got)
	}
}

func TestRetrierCancelKey(t *testing.T) {
	r := NewRetrier(RetryConfig{
		MaxAttempts: 5,
		Initial:     100 * time.Millisecond,
		Max:         100 * time.Millisecond,
		Multiplier:  1,
	})

	key := keyFor("LEVEL")
	done := make(chan error, 1)
	go func() {
		done <- r.DoForKey(context.Background(), key, func(_ context.Context, _ int) error {
			return errors.New("transient")
		})
	}()

	// Let the goroutine register itself, then cancel.
	for range 50 {
		if r.ActiveRetryCount() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.CancelKey(key)

	select {
	case err := <-done:
		if !errors.Is(err, ErrRetrySuperseded) {
			t.Fatalf("CancelKey expected ErrRetrySuperseded, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CancelKey did not return")
	}
}

func TestRetrierDoForKeyDifferentKeysIndependent(t *testing.T) {
	r := NewRetrier(RetryConfig{
		MaxAttempts: 2,
		Initial:     1 * time.Millisecond,
		Max:         1 * time.Millisecond,
		Multiplier:  1,
	})

	var done atomic.Int32
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		k := keyFor("LEVEL_" + string(rune('A'+i)))
		go func() {
			defer wg.Done()
			err := r.DoForKey(context.Background(), k, func(_ context.Context, _ int) error {
				return nil
			})
			if err == nil {
				done.Add(1)
			}
		}()
	}
	wg.Wait()
	if done.Load() != 3 {
		t.Fatalf("expected 3 successes for different keys, got %d", done.Load())
	}
}
