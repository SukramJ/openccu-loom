// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package contract — LOCK_TARGET_LEVEL payload/write parity.
//
// An HmIP lock is driven by exactly one wire parameter,
// LOCK_TARGET_LEVEL, and two north-bound paths write it: the daemon's
// own service methods ([lock.Lock.Lock] / Unlock / Open) and a Home
// Assistant command published onto the command topic the HA discovery
// payload advertises. Which token performs which operation is therefore
// domain knowledge with two readers, and it used to be spelled twice —
// as ENUM labels on the write path and as positional VALUE_LIST indices
// in the discovery payload. Neither the payload builder nor the
// discovery context can read a VALUE_LIST, so the index spelling was an
// unchecked assumption about an ordering no code on that path sees.
//
// This guard pins the two readers against each other: the advertised
// payload_lock / payload_unlock / payload_open must be exactly the
// values the write path puts on LOCK_TARGET_LEVEL. It asserts no token
// literal of its own, so it stays alive whichever spelling the model
// chooses; that the tokens are the real device labels is anchored
// separately by the wire-snapshot reference comparison under
// tests/contract/wire_snapshots.
package contract

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// lockTargetLevelWriter records every SetValue the lock performs.
type lockTargetLevelWriter struct {
	calls []lockTargetLevelCall
}

type lockTargetLevelCall struct {
	param hmenum.Parameter
	value any
}

func (w *lockTargetLevelWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority) error {
	w.calls = append(w.calls, lockTargetLevelCall{param: p, value: v})
	return nil
}

// lockTargetLevelDiscoveryCtx is a minimal [pload.HADiscoveryContext];
// this guard reads the payload values, not the topic wiring.
type lockTargetLevelDiscoveryCtx struct{}

func (lockTargetLevelDiscoveryCtx) AggregatedStateTopic() string { return "ltl/state" }
func (lockTargetLevelDiscoveryCtx) CustomDPStateTopic() string   { return "ltl/custom/state" }

func (lockTargetLevelDiscoveryCtx) ServiceMethodCommandTopic(m string) string {
	return "ltl/svc/" + m + "/set"
}

func (lockTargetLevelDiscoveryCtx) WireParameterCommandTopic(p string) string {
	return "ltl/" + p + "/set"
}
func (lockTargetLevelDiscoveryCtx) WireParameterStateTopic(p string) string { return "ltl/" + p }

func (lockTargetLevelDiscoveryCtx) WireParameterStateTopicOn(addr, p string) string {
	return "ltl/" + addr + "/" + p
}

var _ pload.HADiscoveryContext = lockTargetLevelDiscoveryCtx{}

// TestLockTargetLevelDiscoveryPayloadsMatchServiceWrites crosses the
// seam between the advertised HA payloads and the wire write: for each
// of the three lock operations the payload the discovery config offers
// Home Assistant must equal the value the daemon's own call puts on
// LOCK_TARGET_LEVEL.
func TestLockTargetLevelDiscoveryPayloadsMatchServiceWrites(t *testing.T) {
	t.Parallel()

	const addr = "DLD0001:1"
	w := &lockTargetLevelWriter{}
	l := lock.New(lock.Config{
		Channel:      makeCh(addr, "DOOR_LOCK_STATE_TRANSMITTER"),
		Writer:       w,
		Kind:         lock.KindIP,
		Capabilities: custom.LockCapabilities{SupportsOpen: true},
	})

	component, body := l.HADiscoveryPayload(lockTargetLevelDiscoveryCtx{})
	if component != "lock" {
		t.Fatalf("component = %q, want %q", component, "lock")
	}
	wantTopic := lockTargetLevelDiscoveryCtx{}.WireParameterCommandTopic(string(hmenum.ParameterLockTargetLevel))
	if got := body["command_topic"]; got != wantTopic {
		t.Fatalf("command_topic = %v, want %v — the advertised payloads only reach LOCK_TARGET_LEVEL through this topic", got, wantTopic)
	}

	cases := []struct {
		key    string
		invoke func(context.Context) error
	}{
		{"payload_lock", func(ctx context.Context) error { return l.Lock(ctx, hmenum.CommandPriorityHigh) }},
		{"payload_unlock", func(ctx context.Context) error { return l.Unlock(ctx, hmenum.CommandPriorityHigh) }},
		{"payload_open", func(ctx context.Context) error { return l.Open(ctx, hmenum.CommandPriorityHigh) }},
	}
	for _, tc := range cases {
		advertised, ok := body[tc.key].(string)
		if !ok {
			t.Errorf("%s missing from the discovery payload", tc.key)
			continue
		}
		w.calls = nil
		if err := tc.invoke(context.Background()); err != nil {
			t.Fatalf("%s: service path returned %v", tc.key, err)
		}
		if len(w.calls) != 1 {
			t.Fatalf("%s: %d SetValue calls, want 1", tc.key, len(w.calls))
		}
		call := w.calls[0]
		if call.param != hmenum.ParameterLockTargetLevel {
			t.Fatalf("%s: service path wrote %s, want LOCK_TARGET_LEVEL", tc.key, call.param)
		}
		written, ok := call.value.(string)
		if !ok {
			t.Fatalf("%s: service path wrote %T(%v), want a string token", tc.key, call.value, call.value)
		}
		if advertised != written {
			t.Errorf("%s advertises %q but the service path writes %q on LOCK_TARGET_LEVEL — a Home Assistant command and the daemon's own call would reach the CCU as different values", tc.key, advertised, written)
		}
	}
}
