// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --------------------------------------------------------------------------
// ResolveCentral
// --------------------------------------------------------------------------

func TestIntrospectAdapter_ResolveCentral_KnownName(t *testing.T) {
	t.Parallel()
	reg, _ := buildRecorderRegistry(t, "ccu1")
	a := NewIntrospectAdapter(reg)

	name, ok := a.ResolveCentral("ccu1")
	if !ok {
		t.Fatal("ResolveCentral(known name) returned ok=false")
	}
	if name != "ccu1" {
		t.Errorf("name = %q, want ccu1", name)
	}
}

func TestIntrospectAdapter_ResolveCentral_EmptyWithOneCentral(t *testing.T) {
	t.Parallel()
	reg, _ := buildRecorderRegistry(t, "solo")
	a := NewIntrospectAdapter(reg)

	name, ok := a.ResolveCentral("")
	if !ok {
		t.Fatal("ResolveCentral('') with exactly one central returned ok=false")
	}
	if name != "solo" {
		t.Errorf("name = %q, want solo", name)
	}
}

func TestIntrospectAdapter_ResolveCentral_UnknownName(t *testing.T) {
	t.Parallel()
	reg, _ := buildRecorderRegistry(t, "ccu1")
	a := NewIntrospectAdapter(reg)

	name, ok := a.ResolveCentral("ghost")
	if ok {
		t.Errorf("ResolveCentral(unknown name) returned ok=true with name=%q", name)
	}
	if name != "" {
		t.Errorf("name = %q, want empty", name)
	}
}

// --------------------------------------------------------------------------
// TapEventBus — DataPointValueChanged passes through
// --------------------------------------------------------------------------

func TestIntrospectAdapter_TapEventBus_DeliversDataPointValueChanged(t *testing.T) {
	t.Parallel()
	reg, unit := buildRecorderRegistry(t, "ccuA")
	a := NewIntrospectAdapter(reg)

	ch := make(chan hmapi.DiagnosticsEvent, 8)
	emit := func(e hmapi.DiagnosticsEvent) { ch <- e }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		a.TapEventBus(ctx, "ccuA", nil, emit)
	}()

	// Give the goroutine time to subscribe before publishing.
	time.Sleep(20 * time.Millisecond)

	events.Publish(unit.EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "ABC:1",
			Parameter:      "STATE",
		},
		NewValue: hmtypes.ParamValue{Kind: hmtypes.ValueKindBool, Bool: true},
	})

	select {
	case e := <-ch:
		if e.Type != "DataPointValueChanged" {
			t.Errorf("event type = %q, want DataPointValueChanged", e.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for DataPointValueChanged event")
	}

	cancel()
}

// --------------------------------------------------------------------------
// TapEventBus — recovery progress detail passes through
// --------------------------------------------------------------------------

// TestIntrospectAdapter_TapEventBus_DeliversRecoveryProgress pins that the
// tap carries the per-stage and per-attempt recovery events, not only the
// Started/Completed endpoints. These two are the only bus surface for
// recovery progress; dropping them here makes an in-flight recovery look
// like a silent gap to the diagnostics stream.
func TestIntrospectAdapter_TapEventBus_DeliversRecoveryProgress(t *testing.T) {
	t.Parallel()
	reg, unit := buildRecorderRegistry(t, "ccuR")
	a := NewIntrospectAdapter(reg)

	ch := make(chan hmapi.DiagnosticsEvent, 8)
	emit := func(e hmapi.DiagnosticsEvent) { ch <- e }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		a.TapEventBus(ctx, "ccuR", nil, emit)
	}()

	// Give the goroutine time to subscribe before publishing.
	time.Sleep(20 * time.Millisecond)

	events.Publish(unit.EventBus, hmevent.RecoveryStageChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccuR",
		InterfaceID: "HmIP-RF",
	})
	events.Publish(unit.EventBus, hmevent.RecoveryAttemptedEvent{
		Base:          hmevent.NewBase(),
		CentralName:   "ccuR",
		InterfaceID:   "HmIP-RF",
		AttemptNumber: 1,
		MaxAttempts:   3,
	})

	want := map[string]bool{"RecoveryStageChanged": false, "RecoveryAttempted": false}
	for range want {
		select {
		case e := <-ch:
			if _, ok := want[e.Type]; !ok {
				t.Fatalf("unexpected event type %q", e.Type)
			}
			want[e.Type] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout; delivered so far: %v", want)
		}
	}
	for typ, seen := range want {
		if !seen {
			t.Errorf("event %q never reached the tap", typ)
		}
	}

	cancel()
}

// --------------------------------------------------------------------------
// TapEventBus — type filter excludes non-matching events
// --------------------------------------------------------------------------

func TestIntrospectAdapter_TapEventBus_TypeFilterExcludes(t *testing.T) {
	t.Parallel()
	reg, unit := buildRecorderRegistry(t, "ccuB")
	a := NewIntrospectAdapter(reg)

	ch := make(chan hmapi.DiagnosticsEvent, 8)
	emit := func(e hmapi.DiagnosticsEvent) { ch <- e }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Subscribe only for "DeviceTrigger" events.
	go func() {
		a.TapEventBus(ctx, "ccuB", []string{"DeviceTrigger"}, emit)
	}()

	// Give the goroutine time to subscribe.
	time.Sleep(20 * time.Millisecond)

	// Publish a DataPointValueChanged — should be filtered out.
	events.Publish(unit.EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "ABC:1",
			Parameter:      "STATE",
		},
		NewValue: hmtypes.ParamValue{Kind: hmtypes.ValueKindBool, Bool: false},
	})

	// A short window — no event should arrive.
	select {
	case e := <-ch:
		t.Errorf("unexpected event received: type=%q", e.Type)
	case <-time.After(300 * time.Millisecond):
		// correct: filtered event did not arrive
	}

	cancel()
}

// --------------------------------------------------------------------------
// ReliabilitySnapshot
// --------------------------------------------------------------------------

func TestIntrospectAdapter_ReliabilitySnapshot_NonNilSlice(t *testing.T) {
	t.Parallel()
	reg, unit := buildRecorderRegistry(t, "ccuC")
	a := NewIntrospectAdapter(reg)

	rows := a.ReliabilitySnapshot("")

	// The result must never be nil — an empty slice is acceptable when the
	// unit has no clients.
	if rows == nil {
		t.Fatal("ReliabilitySnapshot returned nil, want non-nil slice")
	}

	// If the unit carries clients, every row must name the correct central.
	if unit.Clients != nil {
		for i, r := range rows {
			if r.Central != "ccuC" {
				t.Errorf("rows[%d].Central = %q, want ccuC", i, r.Central)
			}
		}
	}
}
