// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestWaitForCallback_Zero_Skips verifies that WaitForCallback=false (zero
// value) skips the wait logic entirely and returns immediately — even when
// No CCU echo arrives. Mirrors `wait_for_callback=None`.
// (interface_client.py:1147).
func TestWaitForCallback_Zero_Skips(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	w := NewValueWriter()
	w.SetEventBus(bus)
	w.Register("ccu", "HmIP-RF", &stubBackend{})

	// No event is published on the bus — the call must still return
	// immediately (without a timeout error) because WaitForCallback=false
	// (zero value).
	start := time.Now()
	err := w.SetValueWithOptions(
		context.Background(),
		"ccu", "HmIP-RF", "VCU9001:1",
		hmenum.Parameter("STATE"),
		true,
		WriteOptions{
			// WaitForCallback: false — zero value, no waiting
			WaitForCallback: false,
		},
	)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("WaitForCallback=false must return nil, got: %v", err)
	}
	// The call must not block — 100 ms is a generous upper bound.
	if elapsed > 100*time.Millisecond {
		t.Fatalf("WaitForCallback=false blocked: %v", elapsed)
	}
}

// TestWaitForCallback_Positive_Waits verifies that WaitForCallback=true with
// a positive timeout actually waits for the CCU echo and returns nil only
// after the event is received.
// Mirrors `wait_for_callback=<float>`.
func TestWaitForCallback_Positive_Waits(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	w := NewValueWriter()
	w.SetEventBus(bus)
	w.Register("ccu", "HmIP-RF", &stubBackend{})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Simulate an echo after 50 ms.
	go func() {
		time.Sleep(50 * time.Millisecond)
		dpk, _ := hmtypes.NewDataPointKey("HmIP-RF", "VCU9002:1", hmenum.ParamsetKeyValues, "LEVEL")
		pv, _ := hmtypes.NewParamValue(0.75)
		events.Publish(bus, hmevent.DataPointValueChangedEvent{
			Base:     hmevent.NewBase(),
			Key:      dpk,
			NewValue: pv,
		})
	}()

	err := w.SetValueWithOptions(
		ctx,
		"ccu", "HmIP-RF", "VCU9002:1",
		hmenum.Parameter("LEVEL"),
		0.75,
		WriteOptions{
			WaitForCallback:        true,
			WaitForCallbackTimeout: 2 * time.Second,
		},
	)
	if err != nil {
		t.Fatalf("WaitForCallback=true with echo must return nil, got: %v", err)
	}
}

// TestWaitForCallback_Positive_TimesOut verifies that WaitForCallback=true
// with a set timeout returns [ErrStateChangeTimeout] when no CCU echo
// arrives within the deadline.
func TestWaitForCallback_Positive_TimesOut(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	w := NewValueWriter()
	w.SetEventBus(bus)
	w.Register("ccu", "HmIP-RF", &stubBackend{})

	// No echo — the timeout must fire.
	err := w.SetValueWithOptions(
		context.Background(),
		"ccu", "HmIP-RF", "VCU9003:1",
		hmenum.Parameter("STATE"),
		false,
		WriteOptions{
			WaitForCallback:        true,
			WaitForCallbackTimeout: 30 * time.Millisecond,
		},
	)
	if err == nil {
		t.Fatal("without echo a timeout error must be returned")
	}
	if !errors.Is(err, ErrStateChangeTimeout) {
		t.Fatalf("expected ErrStateChangeTimeout in the error chain, got: %v", err)
	}
}
