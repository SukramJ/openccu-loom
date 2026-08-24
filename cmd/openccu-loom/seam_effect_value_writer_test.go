// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestSeamEffect_ValueWriterHooks_RecordTheSentValue asserts what the
// client.value_writer_hooks seam's Why claims after round 7 corrected it:
// that a value write is recorded by the interface client's command
// tracker.
//
// The tracker entry is what the callback path clears when the CCU echoes
// the value back, and what the in-flight gauge counts. Without the hook the
// write still reaches the CCU — which is why the original Why, claiming
// north-bound planes would show a stale value, was wrong — but nothing
// knows a write is outstanding.
func TestSeamEffect_ValueWriterHooks_RecordTheSentValue(t *testing.T) {
	t.Parallel()

	reg, writer, ic := seamEffectWriterStack(t)
	wireValueWriterHookFns(reg, writer)

	const (
		channel = "VCU0000123:1"
		param   = hmenum.Parameter("LEVEL")
	)
	if err := writer.SetValue(context.Background(), "wr-central",
		"wr-central-HmIP-RF", channel, param, 0.42, hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("set value: %v", err)
	}

	dpk, err := hmtypes.NewDataPointKey("wr-central-HmIP-RF", channel,
		hmenum.ParamsetKeyValues, string(param))
	if err != nil {
		t.Fatalf("data point key: %v", err)
	}
	if _, ok := ic.CommandTracker().GetLastSentValue(dpk); !ok {
		t.Error("the sent value reached no command tracker: the in-flight gauge under-reports " +
			"and the callback path has no entry to clear when the CCU echoes the value back")
	}
}

// TestSeamEffect_ValueWriterHooks_IsAttributableToTheSeam is the negative
// control. Without it, a tracker entry written by the interface client's
// own SetValue path would read as the hook working.
func TestSeamEffect_ValueWriterHooks_IsAttributableToTheSeam(t *testing.T) {
	t.Parallel()

	_, writer, ic := seamEffectWriterStack(t)

	const (
		channel = "VCU0000123:1"
		param   = hmenum.Parameter("LEVEL")
	)
	if err := writer.SetValue(context.Background(), "wr-central",
		"wr-central-HmIP-RF", channel, param, 0.42, hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("set value: %v", err)
	}

	dpk, err := hmtypes.NewDataPointKey("wr-central-HmIP-RF", channel,
		hmenum.ParamsetKeyValues, string(param))
	if err != nil {
		t.Fatalf("data point key: %v", err)
	}
	if _, ok := ic.CommandTracker().GetLastSentValue(dpk); ok {
		t.Error("the value was tracked without the seam being wired — something else records " +
			"it, so the test above proves nothing about this seam")
	}
}

// seamEffectWriterStack builds a registry holding one central with one
// interface client, plus a value writer pointed at the same backend.
func seamEffectWriterStack(t *testing.T) (*central.Registry, *clientpkg.ValueWriter, *clientpkg.InterfaceClient) {
	t.Helper()

	const (
		centralName = "wr-central"
		ifaceID     = "wr-central-HmIP-RF"
	)

	caller := clientpkg.CallerFunc(func(context.Context, string, []any) (any, error) {
		return nil, nil
	})
	ic, err := clientpkg.New(clientpkg.Config{
		CentralName: centralName,
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      caller,
		Logger:      discardTestLogger(),
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	reg := central.NewRegistry()
	unit := registerSeamEffectCentral(t, reg, centralName)
	if err := unit.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: ifaceID,
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("register client entry: %v", err)
	}

	writer := clientpkg.NewValueWriter()
	writer.RegisterIC(centralName, hmtypes.WireInterfaceID(ifaceID), ic)
	writer.Register(centralName, hmtypes.WireInterfaceID(ifaceID), seamEffectBackend{})
	return reg, writer, ic
}

// seamEffectBackend accepts every write without touching a network.
//
// The embedded interface supplies the rest of backends.Operations, which
// this path never calls: a nil method would panic loudly rather than
// quietly returning a zero, so the test cannot drift into exercising a
// surface it does not mean to.
type seamEffectBackend struct {
	backends.Operations
}

func (seamEffectBackend) SetValue(context.Context, string, hmenum.Parameter, any,
	hmenum.CommandPriority, hmenum.CommandRxMode,
) error {
	return nil
}
