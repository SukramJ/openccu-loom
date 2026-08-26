// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// lockedSinkFixture registers one central carrying one device with a single
// channel whose write path is the recording writer it returns.
func lockedSinkFixture(t *testing.T, name string) (*MQTTCommandSink, *device.Channel, *sinkChannelWriter, *fakeWriter) {
	t.Helper()
	c, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	d := device.New(device.Config{
		Address:     "LOCKDEV01",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-PS",
	})
	ch := d.AddChannel("LOCKDEV01:4", 4, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	cw := &sinkChannelWriter{}
	ch.SetWriter(cw)
	c.ModelRegistry.Put(d)

	raw := &fakeWriter{}
	return NewMQTTCommandSink(reg, raw), ch, cw, raw
}

// TestMQTTCommandSinkSetValueRejectsLockedChannel pins the operator channel
// lock on the MQTT VALUES command path: a `.../set` command for a channel the
// operator locked must be refused before any wire call, exactly as the REST,
// WS and SPA paths refuse it.
func TestMQTTCommandSinkSetValueRejectsLockedChannel(t *testing.T) {
	t.Parallel()
	s, ch, cw, raw := lockedSinkFixture(t, "ccu-lock")
	ch.SetOperatorFlags(false, true)

	err := s.SetValue(context.Background(), "ccu-lock", "HmIP-RF", "LOCKDEV01:4",
		hmenum.ParameterState, true, hmenum.CommandPriorityLow)
	if !errors.Is(err, device.ErrChannelOperationLocked) {
		t.Fatalf("SetValue on a locked channel: got %v, want ErrChannelOperationLocked", err)
	}
	if _, _, calls := cw.snapshot(); calls != 0 {
		t.Errorf("locked channel still dispatched %d wire writes", calls)
	}
	if n := raw.calls.Load(); n != 0 {
		t.Errorf("locked channel still reached the raw writer %d times", n)
	}
}

// TestMQTTCommandSinkSetValueUnlockedChannelWrites is the counterpart: an
// unlocked channel still dispatches, through the channel's own writer.
func TestMQTTCommandSinkSetValueUnlockedChannelWrites(t *testing.T) {
	t.Parallel()
	s, _, cw, raw := lockedSinkFixture(t, "ccu-unlock")

	if err := s.SetValue(context.Background(), "ccu-unlock", "HmIP-RF", "LOCKDEV01:4",
		hmenum.ParameterState, true, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	param, value, calls := cw.snapshot()
	if calls != 1 {
		t.Fatalf("expected 1 write through the channel writer, got %d", calls)
	}
	if param != hmenum.ParameterState || value != true {
		t.Errorf("write carried %s=%v, want STATE=true", param, value)
	}
	if n := raw.calls.Load(); n != 0 {
		t.Errorf("known channel bypassed its writer and used the raw writer %d times", n)
	}
}

// TestMQTTCommandSinkSetValueUnknownChannelFallsBack verifies an address the
// model does not carry still reaches the raw writer, so a command for a device
// that has not been hydrated is not silently dropped.
func TestMQTTCommandSinkSetValueUnknownChannelFallsBack(t *testing.T) {
	t.Parallel()
	s, _, cw, raw := lockedSinkFixture(t, "ccu-unknown")

	if err := s.SetValue(context.Background(), "ccu-unknown", "HmIP-RF", "NOSUCHDEV:1",
		hmenum.ParameterState, true, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	if n := raw.calls.Load(); n != 1 {
		t.Errorf("raw writer calls = %d, want 1", n)
	}
	if _, _, calls := cw.snapshot(); calls != 0 {
		t.Errorf("unrelated channel writer was used %d times", calls)
	}
}
