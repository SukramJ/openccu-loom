// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	fabricdevice "github.com/SukramJ/go-fabric/device"
	"github.com/SukramJ/go-fabric/eligibility"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// genericSwitchDevice returns a device whose one channel carries a generic
// switch on parameter p and nothing else.
func genericSwitchDevice(addr string, p hmenum.Parameter) *device.Device {
	dev := newDevice(addr, "Generic")
	chAddr := addr + ":1"
	ch := dev.AddChannel(chAddr, 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}))
	return dev
}

// TestCandidatesOfAGenericSwitch pins the exposable list's two answers for
// a generic switch: STATE is a mappable OnOffPlugInUnit row; any other
// writable bool parameter projects no device and is no row at all —
// nothing of it could be exposed, so no row names a reason.
func TestCandidatesOfAGenericSwitch(t *testing.T) {
	t.Parallel()
	state := matteradapter.CollectCandidates("c", []*device.Device{genericSwitchDevice("VCU0000201", hmenum.ParameterState)}, false, interfaces.MatterBuildOptions{})
	if len(state) != 1 || state[0].Verdict.State != eligibility.StateMappable || state[0].Verdict.DeviceType != 0x010A {
		t.Fatalf("STATE candidates = %+v, want one mappable OnOffPlugInUnit row", state)
	}
	if other := matteradapter.CollectCandidates("c", []*device.Device{genericSwitchDevice("VCU0000202", hmenum.ParameterOnTime)}, false, interfaces.MatterBuildOptions{}); len(other) != 0 {
		t.Fatalf("ON_TIME candidates = %+v, want none: a switch on a parameter other than STATE projects no device", other)
	}
}

// refusingSwitch is a custom data point whose device the kit refuses.
type refusingSwitch struct {
	key hmtypes.DataPointKey
}

func (r *refusingSwitch) DataPointKey() hmtypes.DataPointKey { return r.key }
func (r *refusingSwitch) Name() string                       { return "refusing" }
func (r *refusingSwitch) MatterDeviceType() uint16           { return 0x010A }

func (r *refusingSwitch) MatterDevice(fabricdevice.Options) (*fabricdevice.Device, error) {
	return nil, errors.New("refused on purpose")
}

// TestARefusedDeviceIsLoggedOncePerSource pins that a source whose device
// the kit refuses is logged once, not on every assembly: the source
// retries the build each time, and the finding does not change between
// two assemblies.
func TestARefusedDeviceIsLoggedOncePerSource(t *testing.T) {
	t.Parallel()
	const addr = "VCU0000204"
	dev := newDevice(addr, "Refusing")
	ch := dev.AddChannel(addr+":1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetCustomDataPoint(&refusingSwitch{key: hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "STATE"}})

	var log bytes.Buffer
	a := matteradapter.New(validConfig(), slog.New(slog.NewTextHandler(&log, nil)))
	snap := []matteradapter.DeviceSnapshot{{CentralName: "c", Devices: []*device.Device{dev}}}
	for range 3 {
		if _, err := assembleDevicesWith(context.Background(), a, newFakeStore(), testEndpointConfig(), snap); err != nil {
			t.Fatalf("Assemble: %v", err)
		}
	}
	if n := strings.Count(log.String(), "matter.assembler.device_refused"); n != 1 {
		t.Fatalf("the refusal was logged %d times over three assemblies, want once:\n%s", n, log.String())
	}
}
