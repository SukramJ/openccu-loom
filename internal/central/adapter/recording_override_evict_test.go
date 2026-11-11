// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// purgeCall is one DeleteDevice call the fake purger saw.
type purgeCall struct {
	central, iface, address string
}

// fakeOverridePurger records every DeleteDevice call.
type fakeOverridePurger struct {
	mu    sync.Mutex
	calls []purgeCall
}

func (p *fakeOverridePurger) DeleteDevice(_ context.Context, centralName, interfaceID, deviceAddress string) error {
	p.mu.Lock()
	p.calls = append(p.calls, purgeCall{centralName, interfaceID, deviceAddress})
	p.mu.Unlock()
	return nil
}

func (p *fakeOverridePurger) got() []purgeCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]purgeCall(nil), p.calls...)
}

// TestWireRecordingOverrideEviction_PurgesTheRemovedDevice pins that a
// device removal on a registered central's bus reaches the purger once,
// with the event's central, interface and address.
func TestWireRecordingOverrideEviction_PurgesTheRemovedDevice(t *testing.T) {
	t.Parallel()

	purger := &fakeOverridePurger{}
	reg, unit := registryWithEventBus(t)

	evictor := WireRecordingOverrideEviction(reg, purger, nil)
	t.Cleanup(evictor.Stop)

	// The bus dispatches synchronously on the publishing goroutine.
	unit.EventBus.Publish(hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-test",
		InterfaceID: "HmIP-RF",
		Address:     "DEVICE-A",
	})

	got := purger.got()
	want := purgeCall{"ccu-test", "HmIP-RF", "DEVICE-A"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("purger calls = %+v, want exactly [%+v]", got, want)
	}
}

// TestWireRecordingOverrideEviction_ModelTeardownKeepsOverrides pins that a
// cache-clear re-init, which drops the whole model without the operator
// removing any device, purges nothing.
func TestWireRecordingOverrideEviction_ModelTeardownKeepsOverrides(t *testing.T) {
	t.Parallel()

	purger := &fakeOverridePurger{}
	reg, unit := registryWithEventBus(t)

	evictor := WireRecordingOverrideEviction(reg, purger, nil)
	t.Cleanup(evictor.Stop)

	unit.EventBus.Publish(hmevent.DeviceRemovedEvent{
		Base:          hmevent.NewBase(),
		CentralName:   "ccu-test",
		InterfaceID:   "HmIP-RF",
		Address:       "DEVICE-A",
		ModelTeardown: true,
	})

	if got := purger.got(); len(got) != 0 {
		t.Errorf("purger calls = %+v, want none for a model teardown", got)
	}
}

// TestWireRecordingOverrideEviction_NilInputsDisable pins the disabled
// shape: a nil purger or registry yields a nil handle whose Stop is safe.
func TestWireRecordingOverrideEviction_NilInputsDisable(t *testing.T) {
	t.Parallel()

	reg, _ := registryWithEventBus(t)
	if e := WireRecordingOverrideEviction(reg, nil, nil); e != nil {
		t.Errorf("nil purger: handle = %v, want nil", e)
	}
	if e := WireRecordingOverrideEviction(nil, &fakeOverridePurger{}, nil); e != nil {
		t.Errorf("nil registry: handle = %v, want nil", e)
	}
	var e *RecordingOverrideEvictor
	e.Stop()
}
