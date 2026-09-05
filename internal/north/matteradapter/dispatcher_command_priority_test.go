// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// priorityRecordingWriter is a [custom.Writer] that keeps the
// CommandPriority of every southbound write. It is the measuring
// instrument for the tests below: the priority a Matter command ends up
// queueing at is otherwise invisible, because nothing between the
// cluster server and the command queue branches on it.
type priorityRecordingWriter struct {
	mu   sync.Mutex
	seen []hmenum.CommandPriority
}

func (w *priorityRecordingWriter) SetValue(
	_ context.Context, _ string, _ hmenum.Parameter, _ any, priority hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seen = append(w.seen, priority)
	return nil
}

func (w *priorityRecordingWriter) priorities() []hmenum.CommandPriority {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]hmenum.CommandPriority(nil), w.seen...)
}

// buildSwitchDevice returns a device whose single channel carries a real
// [switchdev.Switch] custom data point over a STATE wire DP, so the
// assembler materialises it as an On/Off Plug-in Unit endpoint and the
// dispatcher reaches the CCU writer through production code only.
func buildSwitchDevice(addr string, w custom.Writer) *device.Device {
	dev := newDevice(addr, "Switch")
	chAddr := addr + ":4"
	ch := dev.AddChannel(chAddr, 4, "SWITCH", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
	ch.SetCustomDataPoint(switchdev.New(ch, custom.RebasedChannelGroupConfig{}))
	return dev
}

// onOffCommandPath addresses OnOff (0x0006) command cmd on ep.
func onOffCommandPath(ep uint16, cmd uint32) im.ConcreteCommandPath {
	return im.ConcreteCommandPath{
		Endpoint: ep, HasEndpoint: true,
		Cluster: 0x0006, HasCluster: true,
		Command: cmd, HasCommand: true,
	}
}

// heatSetpointPath addresses Thermostat.OccupiedHeatingSetpoint
// (0x0201 / 0x0012) on ep. It is the write leg's target because it is one
// of the few attributes that is BOTH writable per the Matter schema —
// the dispatcher rejects a write to a read-only attribute with
// UnsupportedWrite before any cluster server runs, which is why
// OnOff.OnOff cannot serve here — and dispatched onward to the CCU.
func heatSetpointPath(ep uint16) im.ConcreteAttributePath {
	return im.ConcreteAttributePath{
		Endpoint: ep, HasEndpoint: true,
		Cluster: 0x0201, HasCluster: true,
		Attribute: 0x0012, HasAttribute: true,
	}
}

// buildThermostatDevice returns a device whose channel carries a real
// climate custom data point, built through the profile registry exactly
// as the model layer builds it, over a writable SET_POINT_TEMPERATURE
// wire DP that records the priority of every write.
func buildThermostatDevice(t *testing.T, addr string, w custom.Writer) *device.Device {
	t.Helper()
	dev := newDevice(addr, "Thermostat")
	chAddr := addr + ":1"
	ch := dev.AddChannel(chAddr, 1, "CLIMATE", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSetPointTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPThermostat)
	if !ok {
		t.Fatal("no constructor registered for DeviceProfileIPThermostat")
	}
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("thermostat constructor: %v", err)
	}
	ch.SetCustomDataPoint(dp)
	return dev
}

// assembleOne runs the real assembler over one device and returns its
// bridged endpoint ID together with the dispatcher.
func assembleOne(t *testing.T, addr string, dev *device.Device) (dispatcher *endpoint.TopologyDispatcher, endpointID uint16) {
	t.Helper()
	a, err := matteradapter.New(newFakeStore(), validConfig(), nil)
	if err != nil {
		t.Fatalf("matteradapter.New: %v", err)
	}
	top, err := a.AssembleDevices(context.Background(), []matteradapter.DeviceSnapshot{{
		CentralName: "ccu1",
		Devices:     []*device.Device{dev},
	}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	ep := findBridgedByAddress(top, addr)
	if ep == nil {
		t.Fatalf("device %s produced no bridged endpoint", addr)
	}
	return endpoint.NewTopologyDispatcher(top), ep.ID
}

// TestMatterCommandsReachTheWriterAtHighPriority pins the southbound
// urgency of a Matter-driven command all the way to the CCU writer.
//
// The value used to travel as an argument on ClusterServer.MatterWrite /
// MatterInvoke, hard-coded to High at the two dispatcher call sites and
// forwarded untouched by every implementation. It is now a constant
// named inside each cluster server instead, which keeps pkg/mattercontract
// free of host imports — and moves the value out of sight of any test
// that merely passes it in.
//
// Nothing between the cluster server and the command queue branches on
// the priority, so a regression here is silent: the bridge keeps
// working, its writes simply queue at the wrong urgency. Worse,
// [hmenum.CommandPriorityCritical] is the ZERO value, so the likely
// regression — a dropped assignment, an unset variable — escalates every
// bridged command rather than degrading it.
func TestMatterCommandsReachTheWriterAtHighPriority(t *testing.T) {
	ctx := context.Background()

	t.Run("invoke", func(t *testing.T) {
		const addr = "VCU0000001"
		w := &priorityRecordingWriter{}
		d, epID := assembleOne(t, addr, buildSwitchDevice(addr, w))

		// OnOff On (0x01) — the plain command path.
		if res := d.Invoke(ctx, onOffCommandPath(epID, 0x01), nil); res.Status != im.StatusSuccess {
			t.Fatalf("Invoke(OnOff.On) status = %v, want StatusSuccess", res.Status)
		}
		assertAllHigh(t, w.priorities(), "OnOff.On")
	})

	t.Run("write", func(t *testing.T) {
		const addr = "VCU0000002"
		w := &priorityRecordingWriter{}
		d, epID := assembleOne(t, addr, buildThermostatDevice(t, addr, w))

		// 21.0 °C in the cluster's centi-degree wire scale.
		results := d.Write(ctx, heatSetpointPath(epID), im.AttributeValue{Value: int64(2100)})
		if len(results) != 1 {
			t.Fatalf("Write(OccupiedHeatingSetpoint): want 1 result, got %d", len(results))
		}
		if results[0].Status != im.StatusSuccess {
			t.Fatalf("Write(OccupiedHeatingSetpoint) status = %v, want StatusSuccess", results[0].Status)
		}
		assertAllHigh(t, w.priorities(), "OccupiedHeatingSetpoint write")
	})
}

// assertAllHigh fails unless at least one write reached the writer and
// every one of them carried High. The "at least one" half matters: a
// dispatch that silently stopped short of the writer would otherwise
// satisfy an all-of-empty assertion.
func assertAllHigh(t *testing.T, got []hmenum.CommandPriority, what string) {
	t.Helper()
	if len(got) == 0 {
		t.Fatalf("%s reached the CCU writer 0 times, want at least 1", what)
	}
	for i, p := range got {
		if p != hmenum.CommandPriorityHigh {
			t.Errorf("%s: write %d queued at %v, want %v (Critical is the zero value, so an unset priority lands there)",
				what, i, p, hmenum.CommandPriorityHigh)
		}
	}
}
