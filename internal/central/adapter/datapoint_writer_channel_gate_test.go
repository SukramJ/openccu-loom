// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// channelWriterSpy records what reaches a channel's installed writer.
type channelWriterSpy struct{ calls int }

func (s *channelWriterSpy) SetValue(context.Context, string, hmenum.Parameter, any, hmenum.CommandPriority) error {
	s.calls++
	return nil
}

func (s *channelWriterSpy) PutParamset(context.Context, string, hmenum.ParamsetKey, map[string]any, hmenum.CommandPriority) error {
	s.calls++
	return nil
}

// TestDataPointWriterAdapterHonoursTheOperatorChannelLock pins the direct
// writer — what MCP set_datapoint and the inbound webhook write through —
// to the same gate REST, WS and the MQTT sink pass: a locked channel is
// refused with ErrChannelOperationLocked before anything reaches the
// backend, and a read-only parameter is refused as not writable. Going
// straight to the ValueWriter let an assistant actuate a locked channel.
func TestDataPointWriterAdapterHonoursTheOperatorChannelLock(t *testing.T) {
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:3", 3, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	state := generic.NewSwitch(generic.Spec{
		Key:        hmtypes.DataPointKey{InterfaceID: "HmIP-RF", ChannelAddress: ch.Address, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "STATE"},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
	})
	ch.Put(state)
	sensor := generic.NewBinarySensor(generic.Spec{
		Key:        hmtypes.DataPointKey{InterfaceID: "HmIP-RF", ChannelAddress: ch.Address, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "PROCESS"},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(sensor)
	spy := &channelWriterSpy{}
	ch.SetWriter(spy)

	backend := &fakeWriter{}
	a := NewDataPointWriterAdapter(reg, backend)

	// Unlocked: the write goes through the channel's own writer.
	if err := a.SetValue(context.Background(), ch.Address, hmenum.ParameterState, true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("unlocked write: %v", err)
	}
	if spy.calls != 1 || backend.calls.Load() != 0 {
		t.Fatalf("unlocked write took the raw path: channel writer calls=%d, backend calls=%d", spy.calls, backend.calls.Load())
	}

	// Locked: refused before any writer.
	ch.SetOperatorFlags(false, true)
	err := a.SetValue(context.Background(), ch.Address, hmenum.ParameterState, false, hmenum.CommandPriorityHigh)
	if !errors.Is(err, device.ErrChannelOperationLocked) {
		t.Fatalf("locked write: err = %v, want ErrChannelOperationLocked", err)
	}
	if spy.calls != 1 || backend.calls.Load() != 0 {
		t.Fatalf("locked write reached a writer: channel writer calls=%d, backend calls=%d", spy.calls, backend.calls.Load())
	}

	// Read-only parameter: refused as not writable.
	ch.SetOperatorFlags(false, false)
	err = a.SetValue(context.Background(), ch.Address, hmenum.Parameter("PROCESS"), true, hmenum.CommandPriorityHigh)
	if !errors.Is(err, device.ErrParameterNotWritable) && !errors.Is(err, device.ErrValidation) {
		t.Fatalf("read-only write: err = %v, want ErrParameterNotWritable", err)
	}
	if spy.calls != 1 {
		t.Fatalf("read-only write reached the channel writer: calls=%d", spy.calls)
	}
}
