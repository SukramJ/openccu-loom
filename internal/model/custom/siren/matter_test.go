// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"errors"
	"sync"
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
	if !got[onoff.ClusterID] {
		t.Fatalf("Siren clusters = %v, want OnOff (0x0006)", got)
	}
	if got[0x0045] {
		t.Fatalf("Siren clusters include BooleanState (0x0045) which is non-conformant on OnOffPlugInUnit")
	}
}

// matterWriter records every CCU write the Matter Sink causes, with the
// priority it was dispatched at, and can refuse writes.
type matterWriter struct {
	mu    sync.Mutex
	calls []matterCall
	err   error
}

type matterCall struct {
	param    hmenum.Parameter
	value    any
	priority hmenum.CommandPriority
}

func (w *matterWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, v any, prio hmenum.CommandPriority) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	w.calls = append(w.calls, matterCall{p, v, prio})
	return nil
}

func (w *matterWriter) snapshot() []matterCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]matterCall(nil), w.calls...)
}

// matterSiren builds an acoustic-only siren whose observed tone is
// FREQUENCY_RISING, so a switch-on writes that tone and a switch-off writes
// the disable label — the two Sink paths are told apart on the wire. It
// returns the siren's OnOff server and the writer behind the Sink.
func matterSiren(t *testing.T) (*rig, *onoff.Server, *matterWriter) {
	t.Helper()
	w := &matterWriter{}
	r := newRig(t, "HmIP-ASIR:3", w, custom.SirenCapabilities{SupportsAcoustic: true})
	r.acousticIdxDP.RecordLabel("FREQUENCY_RISING")
	servers := r.siren.MatterClusterServers()
	if len(servers) != 1 {
		t.Fatalf("MatterClusterServers = %d servers, want exactly the OnOff server", len(servers))
	}
	srv, ok := servers[0].(*onoff.Server)
	if !ok {
		t.Fatalf("OnOff server is %T, want go-fabric's *onoff.Server", servers[0])
	}
	return r, srv, w
}

// wantSirenWrite asserts the one acoustic selection write a Sink call makes.
func wantSirenWrite(t *testing.T, c matterCall, label string) {
	t.Helper()
	if c.param != hmenum.ParameterAcousticAlarmSelection || c.value != label {
		t.Errorf("write = %s=%v, want %s=%s", c.param, c.value, hmenum.ParameterAcousticAlarmSelection, label)
	}
	if c.priority != matterDispatchPriority {
		t.Errorf("write dispatched at priority %v, want %v", c.priority, matterDispatchPriority)
	}
}

// TestSirenOnOffServerIsBuiltOnce pins that the projection hands out the
// same server on every call: the bridge keeps the server it serves across
// reassemblies, and a rebuilt one would lose its data version and timers.
func TestSirenOnOffServerIsBuiltOnce(t *testing.T) {
	t.Parallel()
	r, srv, _ := matterSiren(t)
	if again := r.siren.MatterClusterServers()[0]; again != srv {
		t.Fatalf("a second MatterClusterServers call returned a different server (%p vs %p)", again, srv)
	}
}

// TestSirenOnOffStartsFalseWhileUnobserved pins the non-nullable default:
// OnOff is a plain bool (Matter §1.5.6.2), so an unobserved siren reads
// false, never TLV null.
func TestSirenOnOffStartsFalseWhileUnobserved(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSiren(t)
	if v, ok := srv.MatterRead(onoff.AttrOnOff); !ok || v != false {
		t.Fatalf("unobserved OnOff = (%v, %v), want (false, true)", v, ok)
	}
}

// TestSirenOnOffStartsFromTheObservedState pins the initial value: an alarm
// the CCU reported before the server was built is served as on.
func TestSirenOnOffStartsFromTheObservedState(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-ASIR:3", &matterWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	r.acousticActiveDP.OnEvent(true)
	srv := findCluster(t, r.siren, onoff.ClusterID)
	if v, ok := srv.MatterRead(onoff.AttrOnOff); !ok || v != true {
		t.Fatalf("OnOff observed active before build = (%v, %v), want (true, true)", v, ok)
	}
}

// TestSirenOnOffConfirmedActivityReachesTheServerWithoutAWrite pins the
// CCU→Matter direction: an ACOUSTIC_ALARM_ACTIVE or OPTICAL_ALARM_ACTIVE
// confirmation (a CCU program raising the alarm) is stored on the server
// and advances its data version, without a single CCU write.
func TestSirenOnOffConfirmedActivityReachesTheServerWithoutAWrite(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		dp   func(r *rig) *generic.BinarySensor
	}{
		{"acoustic", func(r *rig) *generic.BinarySensor { return r.acousticActiveDP }},
		{"optical", func(r *rig) *generic.BinarySensor { return r.opticalActiveDP }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, srv, w := matterSiren(t)
			dp := tc.dp(r)
			before := srv.MatterDataVersion()
			dp.OnEvent(true)
			if v, _ := srv.MatterRead(onoff.AttrOnOff); v != true {
				t.Fatalf("OnOff after confirmed %s alarm active: got %v, want true", tc.name, v)
			}
			if after := srv.MatterDataVersion(); after == before {
				t.Errorf("data version did not advance on a confirmed change (%d)", after)
			}
			dp.OnEvent(false)
			if v, _ := srv.MatterRead(onoff.AttrOnOff); v != false {
				t.Fatalf("OnOff after confirmed %s alarm inactive: got %v, want false", tc.name, v)
			}
			if n := len(w.snapshot()); n != 0 {
				t.Fatalf("a confirmed CCU update must not write back: %d write(s)", n)
			}
		})
	}
}

// TestSirenOnOffCommandsReachTheSink pins the Matter→CCU direction: every
// switch-on command raises the alarm through Siren.TurnOn and every
// switch-off ends it through Siren.TurnOff, at the bridge's dispatch
// priority, and the attribute follows the accepted command.
func TestSirenOnOffCommandsReachTheSink(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		cmd    uint32
		wantOn bool
	}{
		{"On", onoff.CmdOn, true},
		{"OnWithRecallGlobalScene", onoff.CmdOnWithRecallGlobalScene, true},
		{"Off", onoff.CmdOff, false},
		{"OffWithEffect", onoff.CmdOffWithEffect, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, srv, w := matterSiren(t)
			if _, err := srv.MatterInvoke(context.Background(), tc.cmd, nil); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			calls := w.snapshot()
			if len(calls) != 1 {
				t.Fatalf("%s: %d CCU write(s) %+v, want exactly one", tc.name, len(calls), calls)
			}
			want := acousticDisableLabel
			if tc.wantOn {
				want = "FREQUENCY_RISING"
			}
			wantSirenWrite(t, calls[0], want)
			if v, _ := srv.MatterRead(onoff.AttrOnOff); v != tc.wantOn {
				t.Errorf("%s: OnOff = %v after the accepted command, want %v", tc.name, v, tc.wantOn)
			}
		})
	}
}

// TestSirenOnOffRefusedWriteKeepsTheAttribute pins the error path: a CCU
// write the Sink fails answers the command with an error, and the attribute
// and data version stay where the CCU left them.
func TestSirenOnOffRefusedWriteKeepsTheAttribute(t *testing.T) {
	t.Parallel()
	_, srv, w := matterSiren(t)
	w.err = errors.New("radio busy")
	before := srv.MatterDataVersion()
	if _, err := srv.MatterInvoke(context.Background(), onoff.CmdOn, nil); err == nil {
		t.Fatal("a failed CCU write must answer the command with an error")
	}
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != false {
		t.Errorf("OnOff after a refused On: got %v, want the CCU's value false", v)
	}
	if after := srv.MatterDataVersion(); after != before {
		t.Errorf("a refused command advanced the data version: %d -> %d", before, after)
	}
}

// TestSirenOnOffTimedOffEndsTheAlarm pins OnWithTimedOff end to end: the
// alarm is raised through the Sink, and after OnTime the stack's countdown
// ends it through the same Sink. OnTime is in tenths of a second.
func TestSirenOnOffTimedOffEndsTheAlarm(t *testing.T) {
	t.Parallel()
	_, srv, w := matterSiren(t)
	if _, err := srv.MatterInvoke(context.Background(), onoff.CmdOnWithTimedOff, onoffspec.OnWithTimedOffRequest{OnTime: 2}); err != nil {
		t.Fatalf("OnWithTimedOff: %v", err)
	}
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != true {
		t.Fatalf("OnOff right after OnWithTimedOff: got %v, want true", v)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(w.snapshot()) < 2 {
		time.Sleep(20 * time.Millisecond)
	}
	calls := w.snapshot()
	if len(calls) != 2 {
		t.Fatalf("timed on must raise and then end the alarm, got %d write(s) %+v", len(calls), calls)
	}
	wantSirenWrite(t, calls[0], "FREQUENCY_RISING")
	wantSirenWrite(t, calls[1], acousticDisableLabel)
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != false {
		t.Errorf("OnOff after the countdown: got %v, want false", v)
	}
}

// TestSirenOnOffAttributeIsNotWritable pins access "R V" on OnOff (matter.js
// on-off.element.ts): the alarm moves through commands only, and a refused
// write never reaches the CCU.
func TestSirenOnOffAttributeIsNotWritable(t *testing.T) {
	t.Parallel()
	_, srv, w := matterSiren(t)
	if err := srv.MatterWrite(context.Background(), onoff.AttrOnOff, true); err == nil {
		t.Fatal("a write of OnOff must be refused")
	}
	if n := len(w.snapshot()); n != 0 {
		t.Fatalf("a refused attribute write must not reach the CCU: %d write(s)", n)
	}
}

// --- SmokeSiren ---

// newSmokeRig builds a minimal HmIP-SWSD channel with
// SMOKE_DETECTOR_ALARM_STATUS + SMOKE_DETECTOR_COMMAND DPs.
type smokeRig struct {
	siren  *SmokeSiren
	status *generic.Sensor[int32]
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
	statusDP := attachSmokeStatusSensor(ch)
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
		{SmokeStatusSecondaryAlarm, matterSmokeAlarmWarning},
		{SmokeStatusPrimaryAlarm, matterSmokeAlarmCritical},
		// Normal, not Critical: the detector is sounding for a burglar alarm
		// the installation started, and its smoke sensor detected nothing.
		// See TestIntrusionIsNotReportedAsSmoke.
		{SmokeStatusIntrusion, matterSmokeAlarmNormal},
	}
	for _, tc := range cases {
		r := newSmokeRig(t)
		fireSmokeStatus(t, r.status, string(tc.status))
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

// TestSmokeSirenCOStateNotServedWithoutCOFeature — HM-SWSD has no CO
// sensor, so the projection does not advertise the CO feature, and
// CoState (conformance "CO") must not answer beside it.
func TestSmokeSirenCOStateNotServedWithoutCOFeature(t *testing.T) {
	r := newSmokeRig(t)
	srv := findCluster(t, r.siren, 0x005C)
	if v, ok := srv.MatterRead(0x0002); ok {
		t.Fatalf("COState = (%v, %v), want (nil, false) on a smoke-only FeatureMap", v, ok)
	}
}

// TestSmokeSirenInvokeRejectsAllCommands locks the deferred command
// surface — every cmdID returns errMatterUnknownCommand.
func TestSmokeSirenInvokeRejectsAllCommands(t *testing.T) {
	r := newSmokeRig(t)
	srv := findCluster(t, r.siren, 0x005C)
	_, err := srv.MatterInvoke(context.Background(), 0x00, nil)
	if !errors.Is(err, errMatterUnknownCommand) {
		t.Fatalf("err = %v, want errMatterUnknownCommand", err)
	}
}

// --- OnMatterValueChanged (MatterChangeNotifier) ---

// TestSirenOnMatterValueChangedFiresOnConfirmedAcousticChange verifies
// that a CCU-confirmed ACOUSTIC_ALARM_ACTIVE change (e.g. the siren
// started/stopped via a CCU program, not through Apple) reaches a
// registered OnMatterValueChanged callback.
func TestSirenOnMatterValueChangedFiresOnConfirmedAcousticChange(t *testing.T) {
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	var count int
	_ = r.siren.OnMatterValueChanged(func() { count++ })
	r.acousticActiveDP.OnEvent(true)
	r.acousticActiveDP.OnEvent(false)
	if count != 2 {
		t.Fatalf("expected 2 callback invocations, got %d", count)
	}
}

// TestSirenOnMatterValueChangedUnsubscribeStopsCallback verifies that the
// returned closure detaches every wired DP so a further confirmed change
// does not fire the callback again.
func TestSirenOnMatterValueChangedUnsubscribeStopsCallback(t *testing.T) {
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	var count int
	unsub := r.siren.OnMatterValueChanged(func() { count++ })
	r.acousticActiveDP.OnEvent(true)
	unsub()
	r.acousticActiveDP.OnEvent(false)
	if count != 1 {
		t.Fatalf("expected 1 callback invocation after unsub, got %d", count)
	}
}

// TestSirenOnMatterValueChangedNilSafe verifies nil-receiver and
// nil-callback safety.
func TestSirenOnMatterValueChangedNilSafe(t *testing.T) {
	var s *Siren
	unsub := s.OnMatterValueChanged(func() {})
	if unsub == nil {
		t.Fatal("nil Siren: OnMatterValueChanged must return non-nil unsub")
	}
	unsub() // must not panic

	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	unsub2 := r.siren.OnMatterValueChanged(nil)
	if unsub2 == nil {
		t.Fatal("nil callback: OnMatterValueChanged must return non-nil unsub")
	}
	r.acousticActiveDP.OnEvent(true) // must not panic with no subscriber
}

// TestSmokeSirenOnMatterValueChangedFiresOnConfirmedStatusChange verifies
// that a CCU-confirmed SMOKE_DETECTOR_ALARM_STATUS change reaches a
// registered OnMatterValueChanged callback.
func TestSmokeSirenOnMatterValueChangedFiresOnConfirmedStatusChange(t *testing.T) {
	r := newSmokeRig(t)
	var count int
	_ = r.siren.OnMatterValueChanged(func() { count++ })
	fireSmokeStatus(t, r.status, string(SmokeStatusPrimaryAlarm))
	fireSmokeStatus(t, r.status, string(SmokeStatusIdleOff))
	if count != 2 {
		t.Fatalf("expected 2 callback invocations, got %d", count)
	}
}

// TestSmokeSirenOnMatterValueChangedUnsubscribeStopsCallback verifies
// that the returned closure detaches the status subscription.
func TestSmokeSirenOnMatterValueChangedUnsubscribeStopsCallback(t *testing.T) {
	r := newSmokeRig(t)
	var count int
	unsub := r.siren.OnMatterValueChanged(func() { count++ })
	fireSmokeStatus(t, r.status, string(SmokeStatusPrimaryAlarm))
	unsub()
	fireSmokeStatus(t, r.status, string(SmokeStatusIdleOff))
	if count != 1 {
		t.Fatalf("expected 1 callback invocation after unsub, got %d", count)
	}
}

// TestSmokeSirenOnMatterValueChangedNilSafe verifies nil-receiver and
// nil-callback safety.
func TestSmokeSirenOnMatterValueChangedNilSafe(t *testing.T) {
	var s *SmokeSiren
	unsub := s.OnMatterValueChanged(func() {})
	if unsub == nil {
		t.Fatal("nil SmokeSiren: OnMatterValueChanged must return non-nil unsub")
	}
	unsub() // must not panic

	r := newSmokeRig(t)
	unsub2 := r.siren.OnMatterValueChanged(nil)
	if unsub2 == nil {
		t.Fatal("nil callback: OnMatterValueChanged must return non-nil unsub")
	}
	fireSmokeStatus(t, r.status, string(SmokeStatusPrimaryAlarm)) // must not panic with no subscriber
}
