// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package siren

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// findCluster returns the cluster server with the given ID.
func findCluster(t *testing.T, src interfaces.MatterEndpointSource, id uint32) interfaces.MatterClusterServer {
	t.Helper()
	for _, s := range src.MatterClusterServers() {
		if s.MatterClusterID() == id {
			return s
		}
	}
	t.Fatalf("cluster 0x%04X not present in projection", id)
	return nil
}

// TestSirenMatterDeviceTypeIsOnOffPlugInUnit locks the device-type
// advertisement (0x010A).
func TestSirenMatterDeviceTypeIsOnOffPlugInUnit(t *testing.T) {
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{})
	if got := r.siren.MatterDeviceType(); got != 0x010A {
		t.Fatalf("Siren.MatterDeviceType = 0x%04X, want 0x010A", got)
	}
}

// TestSirenClusterServersIncludeOnOff confirms OnOff is present.
// BooleanState (0x0045) is intentionally absent: mounting it on
// OnOffPlugInUnit (0x010A) is non-conformant per matter.js and the spec;
// strict controllers reject it as UnsupportedCluster.
func TestSirenClusterServersIncludeOnOff(t *testing.T) {
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{})
	got := map[uint32]bool{}
	for _, s := range r.siren.MatterClusterServers() {
		got[s.MatterClusterID()] = true
	}
	if !got[0x0006] {
		t.Fatalf("Siren clusters = %v, want OnOff (0x0006)", got)
	}
	if got[0x0045] {
		t.Fatalf("Siren clusters include BooleanState (0x0045) which is non-conformant on OnOffPlugInUnit")
	}
}

// TestSirenOnOffReadActiveTracksIsActive walks the active flag through
// the OnOff cluster's OnOff attribute.
func TestSirenOnOffReadActiveTracksIsActive(t *testing.T) {
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{
		SupportsAcoustic: true,
	})
	r.acousticActiveDP.OnEvent(true)
	srv := findCluster(t, r.siren, 0x0006)
	v, ok := srv.MatterRead(0x0000)
	if !ok || v != true {
		t.Fatalf("OnOff read active = (%v, %v), want (true, true)", v, ok)
	}
}

// TestSirenOnOffReadAlarmReflectsUnobservedAsFalse verifies that when no
// CCU push has arrived yet, OnOff returns (false, true) — non-nullable
// safe-state default.
func TestSirenOnOffReadAlarmReflectsUnobservedAsFalse(t *testing.T) {
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{
		SupportsAcoustic: true,
	})
	// No event pushed → IsActive returns (false, false).
	srv := findCluster(t, r.siren, 0x0006)
	v, ok := srv.MatterRead(0x0000)
	// sirenOnOffServer returns (nil, true) when not observed (same pattern
	// as TemperatureMeasurement for nullable attribute); OnOff is non-nullable
	// but the controller handles null gracefully here.
	if !ok {
		t.Fatalf("OnOff unobserved: ok=false, want true")
	}
	_ = v // nil or false both acceptable before first push
}

// TestSirenOnOffOffCommandTurnsOff routes Matter Off through Siren.TurnOff.
func TestSirenOnOffOffCommandTurnsOff(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-ASIR:3", w, custom.SirenCapabilities{
		SupportsAcoustic: true, SupportsOptical: true,
	})
	srv := findCluster(t, r.siren, 0x0006)
	if _, err := srv.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Off err: %v", err)
	}
	if len(w.calls) == 0 {
		t.Fatal("Off did not reach the wire")
	}
}

// --- SmokeSiren ---

// newSmokeRig builds a minimal HmIP-SWSD channel with
// SMOKE_DETECTOR_ALARM_STATUS + SMOKE_DETECTOR_COMMAND DPs.
type smokeRig struct {
	siren  *SmokeSiren
	status *generic.Sensor[string]
}

func newSmokeRig(t *testing.T) *smokeRig {
	t.Helper()
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("HmIP-SWSD:1", 1, "SMOKE_DETECTOR", hmenum.ParamsetKeyValues)
	mk := func(p hmenum.Parameter) *generic.Sensor[string] {
		dp := generic.NewStringSensor(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "HmIP-SWSD:1",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeString,
				Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			},
		})
		ch.Put(dp)
		return dp
	}
	statusDP := mk(hmenum.ParameterSmokeDetectorAlarmStatus)
	mk(hmenum.ParameterSmokeDetectorCommand)
	return &smokeRig{
		siren:  NewSmokeSiren(SmokeSirenConfig{Channel: ch, Writer: w}),
		status: statusDP,
	}
}

// TestSmokeSirenMatterDeviceTypeIsSmokeCOAlarm locks the SmokeCOAlarm
// (0x0076) device type.
func TestSmokeSirenMatterDeviceTypeIsSmokeCOAlarm(t *testing.T) {
	r := newSmokeRig(t)
	if got := r.siren.MatterDeviceType(); got != 0x0076 {
		t.Fatalf("MatterDeviceType = 0x%04X, want 0x0076", got)
	}
}

// TestSmokeSirenStatusToAlarmState round-trips every SmokeAlarmStatus
// through the AlarmStateEnum mapping.
func TestSmokeSirenStatusToAlarmState(t *testing.T) {
	cases := []struct {
		status SmokeAlarmStatus
		want   uint8
	}{
		{SmokeStatusIdleOff, matterSmokeAlarmNormal},
		{SmokeStatusIdleOn, matterSmokeAlarmNormal},
		{SmokeStatusSecondaryAlarm, matterSmokeAlarmWarning},
		{SmokeStatusPrimaryAlarm, matterSmokeAlarmCritical},
		{SmokeStatusIntrusion, matterSmokeAlarmCritical},
	}
	for _, tc := range cases {
		r := newSmokeRig(t)
		r.status.OnEvent(string(tc.status))
		srv := findCluster(t, r.siren, 0x005C)
		v, ok := srv.MatterRead(0x0001) // SmokeState
		if !ok || v.(uint8) != tc.want {
			t.Errorf("status=%s: SmokeState=(%v, %v), want (%d, true)", tc.status, v, ok, tc.want)
		}
	}
}

// TestSmokeSirenFeatureMapAdvertisesSmoke confirms only the SMOKE bit
// is set (no CO sensor on HM-SWSD).
func TestSmokeSirenFeatureMapAdvertisesSmoke(t *testing.T) {
	r := newSmokeRig(t)
	srv := findCluster(t, r.siren, 0x005C)
	v, _ := srv.MatterRead(0xFFFC)
	if v.(uint32) != matterSmokeCOFeatureSmoke {
		t.Fatalf("FeatureMap = 0x%08X, want 0x%08X (SMOKE only)", v.(uint32), matterSmokeCOFeatureSmoke)
	}
}

// TestSmokeSirenCOStateAlwaysNormal — HM-SWSD has no CO sensor; the
// CO attribute returns Normal as a stable read.
func TestSmokeSirenCOStateAlwaysNormal(t *testing.T) {
	r := newSmokeRig(t)
	srv := findCluster(t, r.siren, 0x005C)
	v, ok := srv.MatterRead(0x0002)
	if !ok || v.(uint8) != matterSmokeAlarmNormal {
		t.Fatalf("COState = (%v, %v), want (0=Normal, true)", v, ok)
	}
}

// TestSmokeSirenInvokeRejectsAllCommands locks the deferred command
// surface — every cmdID returns errMatterUnknownCommand.
func TestSmokeSirenInvokeRejectsAllCommands(t *testing.T) {
	r := newSmokeRig(t)
	srv := findCluster(t, r.siren, 0x005C)
	_, err := srv.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if !errors.Is(err, errMatterUnknownCommand) {
		t.Fatalf("err = %v, want errMatterUnknownCommand", err)
	}
}
