// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/cluster/onoff"
	onoffspec "github.com/SukramJ/go-fabric/cluster/spec/onoff"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestOnWithTimedOffExpiryTurnsTheLightOffAtTheCCU pins that the end of
// an OnWithTimedOff countdown reaches the CCU: the stack counts OnTime
// down on a 100 ms tick (matter.js OnOffServer.ts:102) and its turn-off
// goes through the light's Sink, so OnTime=2 writes LEVEL=0 about
// 200 ms after the turn-on. The countdown itself is go-fabric's and is
// tested there; this pins only that its end is a CCU write.
func TestOnWithTimedOffExpiryTurnsTheLightOffAtTheCCU(t *testing.T) {
	w := &recWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	t.Cleanup(l.Close)
	srv := onOffServer(t, l)

	if _, err := srv.MatterInvoke(context.Background(), onoff.CmdOnWithTimedOff, onoffspec.OnWithTimedOffRequest{OnTime: 2}); err != nil {
		t.Fatalf("OnWithTimedOff: %v", err)
	}
	if values, _ := w.writes(); len(values) != 1 || values[0] <= 0 {
		t.Fatalf("CCU writes right after OnWithTimedOff = %v, want exactly one turn-on level", values)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		values, prios := w.writes()
		if len(values) >= 2 {
			if len(values) != 2 || values[1] != 0 {
				t.Fatalf("CCU writes after the countdown = %v, want [turn-on level, 0]", values)
			}
			if prios[1] != hmenum.CommandPriorityHigh {
				t.Fatalf("countdown turn-off priority = %v, want CommandPriorityHigh", prios[1])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no turn-off reached the CCU within 2 s of OnWithTimedOff(OnTime=2); writes = %v", values)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != false {
		t.Fatalf("OnOff after the countdown = %v, want false", v)
	}
}

// TestChannelTeardownStopsTheTimedOnOffCountdown drives the real
// teardown path: the Light is bound to its channel (which subscribes
// it), a controller arms OnWithTimedOff, and the channel is then removed
// — the device-removal / cache-clear / custom-DP-replacement path.
// Light.Close quiesces the OnOff server, so the armed countdown never
// issues a CCU turn-off for a device the daemon no longer models.
func TestChannelTeardownStopsTheTimedOnOffCountdown(t *testing.T) {
	w := &recWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("HmIP-BDT:4", 4, "DIMMER", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "HmIP-BDT:4",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
	l := New(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true}})
	ch.SetCustomDataPoint(l)
	srv := onOffServer(t, l)

	// OnTime = 2 tenths: without the teardown the turn-off would land
	// about 200 ms from now.
	if _, err := srv.MatterInvoke(context.Background(), onoff.CmdOnWithTimedOff, onoffspec.OnWithTimedOffRequest{OnTime: 2}); err != nil {
		t.Fatalf("OnWithTimedOff: %v", err)
	}

	ch.Remove()

	time.Sleep(600 * time.Millisecond)
	if values, _ := w.writes(); len(values) != 1 {
		t.Fatalf("CCU writes after teardown = %v, want only the turn-on — the countdown must not outlive the channel", values)
	}
}
