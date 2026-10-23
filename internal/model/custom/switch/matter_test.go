// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package switchdev

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/cluster/onoff"
	onoffspec "github.com/SukramJ/go-fabric/cluster/spec/onoff"
	"github.com/SukramJ/go-fabric/contract"
	fabricdevice "github.com/SukramJ/go-fabric/device"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// matterWriteCall is one southbound write the OnOff Sink caused.
type matterWriteCall struct {
	param    hmenum.Parameter
	value    any
	priority hmenum.CommandPriority
}

// matterWriter records every southbound write with its priority. It is
// safe for the OnOff server's countdown goroutine, and err refuses the
// write the way a busy radio would.
type matterWriter struct {
	mu    sync.Mutex
	calls []matterWriteCall
	err   error
}

func (w *matterWriter) SetValue(_ context.Context, _ string, parameter hmenum.Parameter, value any, priority hmenum.CommandPriority) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	w.calls = append(w.calls, matterWriteCall{param: parameter, value: value, priority: priority})
	return nil
}

func (w *matterWriter) snapshot() []matterWriteCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]matterWriteCall(nil), w.calls...)
}

// matterOnOffServer is what these tests read from the switch's OnOff
// server: the cluster surface, its data version and its lists.
type matterOnOffServer interface {
	contract.ClusterServer
	contract.ClusterDataVersion
	contract.ClusterAttributeLister
	contract.ClusterCommandLister
}

// matterSwitch builds a writable STATE switch and returns it with its
// OnOff server and the writer behind the Sink.
func matterSwitch(t *testing.T) (*Switch, matterOnOffServer, *matterWriter) {
	t.Helper()
	w := &matterWriter{}
	s := newTestSwitch(t, "HmIP-PS:3", "", w)
	return s, matterOnOffServerOf(t, s), w
}

// matterDeviceOf returns the device s projects.
func matterDeviceOf(t *testing.T, s *Switch) *fabricdevice.Device {
	t.Helper()
	d, err := s.MatterDevice(fabricdevice.Options{StableKey: fabricdevice.StringKey(s.Address()), Name: "switch"})
	if err != nil {
		t.Fatalf("MatterDevice: %v", err)
	}
	if d == nil {
		t.Fatal("MatterDevice returned no device")
	}
	return d
}

// matterOnOffServerOf returns the OnOff server of the device s projects.
func matterOnOffServerOf(t *testing.T, s *Switch) matterOnOffServer {
	t.Helper()
	d := matterDeviceOf(t, s)
	srv, ok := d.Server(onoff.ClusterID).(matterOnOffServer)
	if !ok {
		t.Fatalf("OnOff server is %T, want a server with data version and lists", d.Server(onoff.ClusterID))
	}
	return srv
}

// TestMatterDeviceType locks the OnOffPlugInUnit device-type projection
// the endpoint assembler starts from.
func TestMatterDeviceType(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	if got := s.MatterDeviceType(); got != onoff.DeviceTypeOnOffPlugInUnit {
		t.Fatalf("MatterDeviceType = 0x%04X, want OnOffPlugInUnit 0x%04X", got, onoff.DeviceTypeOnOffPlugInUnit)
	}
}

// TestMatterDeviceServesOnOff confirms the Switch's device serves the
// OnOff cluster. Groups and ScenesManagement are stack state the endpoint
// assembler mounts from the node's group state.
func TestMatterDeviceServesOnOff(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSwitch(t)
	if id := srv.MatterClusterID(); id != onoff.ClusterID {
		t.Fatalf("cluster id = 0x%04X, want OnOff 0x%04X", id, onoff.ClusterID)
	}
}

// TestMatterDeviceBuiltOncePerSwitch pins that reassembly sees the same
// device: a rebuilt one would lose its servers' timers, attribute state
// and the subscription behind them.
func TestMatterDeviceBuiltOncePerSwitch(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	d := matterDeviceOf(t, s)
	if again := matterDeviceOf(t, s); again != d {
		t.Fatalf("a second MatterDevice call returned a different device (%p vs %p)", again, d)
	}
}

// TestMatterDeviceShadowsTheEmbeddedGenericSwitch pins that the profile,
// not the generic STATE data point it embeds, builds the device: the
// promoted generic method would serve the bare data point's commands,
// which bypass the profile's group-state handling.
func TestMatterDeviceShadowsTheEmbeddedGenericSwitch(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	var src interfaces.MatterDeviceSource = s
	d, err := src.MatterDevice(fabricdevice.Options{StableKey: fabricdevice.StringKey("k"), Name: "switch"})
	if err != nil || d == nil {
		t.Fatalf("MatterDevice through the interface = (%v, %v), want a device", d, err)
	}
	if held := s.matterDevice.Current(); held != d {
		t.Fatalf("the device came from %p, the profile holds %p: the embedded generic switch built it", d, held)
	}
}

// TestMatterOnOffStartsFalseWhileUnobserved pins the cold-start value:
// Apple Home's HAP mapper drops the OnOffPlugInUnit accessory when OnOff
// carries no boolean on the first Subscribe report.
func TestMatterOnOffStartsFalseWhileUnobserved(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSwitch(t)
	if v, ok := srv.MatterRead(onoff.AttrOnOff); !ok || v != false {
		t.Fatalf("OnOff on unobserved = (%v, %v), want (false, true)", v, ok)
	}
}

// TestMatterOnOffStartsFromTheObservedState pins that a server built
// after the CCU reported STATE starts from that state.
func TestMatterOnOffStartsFromTheObservedState(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	s.OnState(true)
	srv := matterOnOffServerOf(t, s)
	if v, ok := srv.MatterRead(onoff.AttrOnOff); !ok || v != true {
		t.Fatalf("OnOff after observed STATE=true = (%v, %v), want (true, true)", v, ok)
	}
}

// TestMatterConfirmedStateReachesTheServerWithoutAWrite pins the
// confirmed-update hook: a wall switch or CCU program flip is stored on
// the server and reported, and never echoes back into a CCU write.
func TestMatterConfirmedStateReachesTheServerWithoutAWrite(t *testing.T) {
	t.Parallel()
	s, srv, w := matterSwitch(t)
	for _, on := range []bool{true, false, true} {
		s.OnState(on)
		if v, _ := srv.MatterRead(onoff.AttrOnOff); v != on {
			t.Fatalf("OnOff after confirmed STATE=%v: got %v", on, v)
		}
	}
	if calls := w.snapshot(); len(calls) != 0 {
		t.Fatalf("a confirmed CCU update must not write back: %+v", calls)
	}
}

// TestMatterFeatureMapAdvertisesLighting pins the LT feature the
// OnOffPlugInUnit device type mandates, and the stack's revision.
func TestMatterFeatureMapAdvertisesLighting(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSwitch(t)
	if v, ok := srv.MatterRead(0xFFFC); !ok || v != onoff.FeatureLighting {
		t.Errorf("FeatureMap = (%v, %v), want (0x%02X, true)", v, ok, onoff.FeatureLighting)
	}
	if v, ok := srv.MatterRead(0xFFFD); !ok || v != onoff.Revision() {
		t.Errorf("ClusterRevision = (%v, %v), want (%v, true)", v, ok, onoff.Revision())
	}
}

// TestMatterAdvertisedListsAreTheLightingSet pins the attribute and
// accepted-command lists to the LT set of the stack's definition.
func TestMatterAdvertisedListsAreTheLightingSet(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSwitch(t)
	if got, want := srv.MatterAttributes(), onoff.LightingAttributes(); !sameIDs(got, want) {
		t.Errorf("MatterAttributes() = %#x, want the LT set %#x", got, want)
	}
	if got, want := srv.MatterAcceptedCommands(), onoff.LightingCommands(); !sameIDs(got, want) {
		t.Errorf("MatterAcceptedCommands() = %#x, want the LT set %#x", got, want)
	}
	if len(srv.MatterGeneratedCommands()) != 0 {
		t.Error("OnOff commands carry no response payload")
	}
}

func sameIDs(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMatterOnOffAttributeIsNotWritable pins OnOff's access R V: a
// controller changes it only through a command, and a refused write
// never reaches the CCU.
func TestMatterOnOffAttributeIsNotWritable(t *testing.T) {
	t.Parallel()
	_, srv, w := matterSwitch(t)
	if err := srv.MatterWrite(context.Background(), onoff.AttrOnOff, true); err == nil {
		t.Fatal("a write to OnOff must be refused")
	}
	if calls := w.snapshot(); len(calls) != 0 {
		t.Fatalf("a refused attribute write must not reach the CCU: %+v", calls)
	}
}

// TestMatterWriteLTAttributesAcceptDecodedUint64 pins the type the IM
// layer delivers: every unsigned TLV integer decodes to uint64, and the
// three writable LT attributes must accept it.
func TestMatterWriteLTAttributesAcceptDecodedUint64(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		attr  uint32
		write any
		want  any
	}{
		{"OnTime", onoff.AttrOnTime, uint64(42), uint16(42)},
		{"OffWaitTime", onoff.AttrOffWaitTime, uint64(7), uint16(7)},
		{"StartUpOnOff", onoff.AttrStartUpOnOff, uint64(1), uint8(1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, srv, w := matterSwitch(t)
			if err := srv.MatterWrite(context.Background(), tc.attr, tc.write); err != nil {
				t.Fatalf("write %s: %v", tc.name, err)
			}
			if v, _ := srv.MatterRead(tc.attr); v != tc.want {
				t.Errorf("%s after write: got %v (%T), want %v", tc.name, v, v, tc.want)
			}
			if calls := w.snapshot(); len(calls) != 0 {
				t.Errorf("an LT attribute write must not reach the CCU: %+v", calls)
			}
		})
	}
}

// TestMatterCommandsReachTheCCUThroughTheSink pins the Sink: every
// command that changes OnOff becomes exactly one STATE write at the
// bridge's foreground priority, and the server stores the new value.
func TestMatterCommandsReachTheCCUThroughTheSink(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		startOn bool
		cmd     uint32
		want    bool
	}{
		{"On", false, onoff.CmdOn, true},
		{"Off", true, onoff.CmdOff, false},
		{"Toggle from off", false, onoff.CmdToggle, true},
		{"Toggle from on", true, onoff.CmdToggle, false},
		{"OffWithEffect", true, onoff.CmdOffWithEffect, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, srv, w := matterSwitch(t)
			s.OnState(tc.startOn)
			if _, err := srv.MatterInvoke(context.Background(), tc.cmd, nil); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			calls := w.snapshot()
			if len(calls) != 1 {
				t.Fatalf("%s: %d CCU write(s), want exactly one: %+v", tc.name, len(calls), calls)
			}
			c := calls[0]
			if c.param != hmenum.ParameterState || c.value != tc.want || c.priority != hmenum.CommandPriorityHigh {
				t.Errorf("%s wrote %+v, want STATE=%v at priority High", tc.name, c, tc.want)
			}
			if v, _ := srv.MatterRead(onoff.AttrOnOff); v != tc.want {
				t.Errorf("%s: OnOff = %v after the accepted command, want %v", tc.name, v, tc.want)
			}
		})
	}
}

// TestMatterOnWithRecallGlobalSceneReachesTheSink pins the recall path
// after an OffWithEffect cleared GlobalSceneControl: the On it implies
// reaches the CCU.
func TestMatterOnWithRecallGlobalSceneReachesTheSink(t *testing.T) {
	t.Parallel()
	_, srv, w := matterSwitch(t)
	ctx := context.Background()
	if _, err := srv.MatterInvoke(ctx, onoff.CmdOffWithEffect, nil); err != nil {
		t.Fatalf("OffWithEffect: %v", err)
	}
	if _, err := srv.MatterInvoke(ctx, onoff.CmdOnWithRecallGlobalScene, nil); err != nil {
		t.Fatalf("OnWithRecallGlobalScene: %v", err)
	}
	calls := w.snapshot()
	if len(calls) == 0 || calls[len(calls)-1].value != true {
		t.Fatalf("OnWithRecallGlobalScene must end with STATE=true, got %+v", calls)
	}
}

// TestMatterCommandMatchingTheObservedStateDoesNotWrite pins that the
// Sink is the profile's gated TurnOn / TurnOff: a command whose target
// equals the CCU-confirmed state costs no radio telegram.
func TestMatterCommandMatchingTheObservedStateDoesNotWrite(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		on   bool
		cmd  uint32
	}{
		{"On while on", true, onoff.CmdOn},
		{"Off while off", false, onoff.CmdOff},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, srv, w := matterSwitch(t)
			s.OnState(tc.on)
			if _, err := srv.MatterInvoke(context.Background(), tc.cmd, nil); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if calls := w.snapshot(); len(calls) != 0 {
				t.Fatalf("%s wrote %+v, want no write", tc.name, calls)
			}
			if v, _ := srv.MatterRead(onoff.AttrOnOff); v != tc.on {
				t.Errorf("%s: OnOff = %v, want %v", tc.name, v, tc.on)
			}
		})
	}
}

// TestMatterRefusedWriteKeepsTheAttribute pins that a failed CCU write
// answers the command with an error and leaves OnOff at the CCU's value.
func TestMatterRefusedWriteKeepsTheAttribute(t *testing.T) {
	t.Parallel()
	_, srv, w := matterSwitch(t)
	w.err = errors.New("radio busy")
	if _, err := srv.MatterInvoke(context.Background(), onoff.CmdOn, nil); err == nil {
		t.Fatal("a failed CCU write must answer the command with an error")
	}
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != false {
		t.Errorf("OnOff after a refused On: got %v, want the CCU's value false", v)
	}
}

// TestMatterOnWithTimedOffSwitchesOffAfterOnTime pins that the stack's
// countdown reaches the CCU: the timed On writes STATE=true, and the
// Off at the end of OnTime writes STATE=false through the same Sink.
func TestMatterOnWithTimedOffSwitchesOffAfterOnTime(t *testing.T) {
	t.Parallel()
	_, srv, w := matterSwitch(t)
	// OnTime is in tenths of a second.
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
	if len(calls) != 2 || calls[0].value != true || calls[1].value != false {
		t.Fatalf("timed on must write STATE=true then STATE=false, got %+v", calls)
	}
	if calls[1].priority != hmenum.CommandPriorityHigh {
		t.Errorf("countdown Off priority = %v, want High", calls[1].priority)
	}
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != false {
		t.Errorf("OnOff after the countdown: got %v, want false", v)
	}
}

// TestMatterUnknownCommandIsRejected pins that an id outside the OnOff
// command set is refused without a write.
func TestMatterUnknownCommandIsRejected(t *testing.T) {
	t.Parallel()
	_, srv, w := matterSwitch(t)
	if _, err := srv.MatterInvoke(context.Background(), 0x43, nil); err == nil {
		t.Fatal("unknown command: expected error")
	}
	if calls := w.snapshot(); len(calls) != 0 {
		t.Fatalf("an unknown command must not write: %+v", calls)
	}
}
