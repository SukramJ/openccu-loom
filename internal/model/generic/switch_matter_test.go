// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/cluster/onoff"
	onoffspec "github.com/SukramJ/go-fabric/cluster/spec/onoff"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestSwitchMatter_EligibleOnlyForState(t *testing.T) {
	t.Parallel()
	if !matterGenericSwitchEligible(hmenum.ParameterState) {
		t.Error("STATE should be eligible")
	}
	if matterGenericSwitchEligible(hmenum.ParameterOnTime) {
		t.Error("ON_TIME should not be eligible")
	}
}

func TestSwitchMatter_MatterDeviceType(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	if got := s.MatterDeviceType(); got != onoff.DeviceTypeOnOffPlugInUnit {
		t.Errorf("MatterDeviceType: got 0x%04X, want 0x%04X", got, onoff.DeviceTypeOnOffPlugInUnit)
	}
}

// matterSwitch builds a writable STATE switch and returns it with its
// OnOff server and the stub writer behind the Sink.
func matterSwitch(t *testing.T) (*Switch, *onoff.Server, *stubWriter) {
	t.Helper()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	w := &stubWriter{}
	s.Writer = w
	servers := s.MatterClusterServers()
	if len(servers) != 1 {
		t.Fatalf("MatterClusterServers = %d servers, want exactly the OnOff server", len(servers))
	}
	srv, ok := servers[0].(*onoff.Server)
	if !ok {
		t.Fatalf("OnOff server is %T, want go-fabric's *onoff.Server", servers[0])
	}
	return s, srv, w
}

func TestSwitchMatter_MatterClusterServers_State(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSwitch(t)
	if srv.MatterClusterID() != onoff.ClusterID {
		t.Fatalf("cluster id = 0x%04X, want OnOff 0x%04X", srv.MatterClusterID(), onoff.ClusterID)
	}
}

func TestSwitchMatter_MatterClusterServers_BuiltOncePerSwitch(t *testing.T) {
	t.Parallel()
	s, srv, _ := matterSwitch(t)
	if again := s.MatterClusterServers()[0]; again != srv {
		t.Fatalf("a second MatterClusterServers call returned a different server (%p vs %p); the bridge keeps the server it serves across reassemblies, so it must be the same one", again, srv)
	}
}

func TestSwitchMatter_MatterClusterServers_NonState_ReturnsNil(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterOnTime, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	if servers := s.MatterClusterServers(); servers != nil {
		t.Errorf("MatterClusterServers with ON_TIME: want nil, got %v", servers)
	}
}

func TestSwitchMatter_MatterClusterServers_Nil(t *testing.T) {
	t.Parallel()
	var s *Switch
	if servers := s.MatterClusterServers(); servers != nil {
		t.Error("nil switch: MatterClusterServers must return nil")
	}
}

func TestSwitchMatter_OnOffStartsFalseWhileUnobserved(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSwitch(t)
	if v, ok := srv.MatterRead(onoff.AttrOnOff); !ok || v != false {
		t.Errorf("unobserved: want (false, true), got (%v, %v)", v, ok)
	}
}

func TestSwitchMatter_OnOffStartsFromTheObservedState(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
	s.OnEvent(true)
	srv := s.MatterClusterServers()[0]
	if v, ok := srv.MatterRead(onoff.AttrOnOff); !ok || v != true {
		t.Errorf("observed true before the server was built: want (true, true), got (%v, %v)", v, ok)
	}
}

func TestSwitchMatter_ConfirmedStateReachesTheServerWithoutAWrite(t *testing.T) {
	t.Parallel()
	s, srv, w := matterSwitch(t)
	before := srv.MatterDataVersion()
	// A CCU echo (wall switch, program): the confirmed-update hook stores
	// it through Server.Set — no Sink call, so no CCU write.
	s.OnEvent(true)
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != true {
		t.Fatalf("OnOff after confirmed STATE=true: got %v, want true", v)
	}
	if n := len(w.calls); n != 0 {
		t.Fatalf("a confirmed CCU update must not write back: %d write(s)", n)
	}
	if after := srv.MatterDataVersion(); after == before {
		t.Errorf("data version did not advance on a confirmed change (%d)", after)
	}
}

func TestSwitchMatter_FeatureMapAdvertisesLighting(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSwitch(t)
	v, ok := srv.MatterRead(0xFFFC)
	if !ok || v != onoff.FeatureLighting {
		t.Errorf("FeatureMap: want (0x%02X, true), got (%v, %v)", onoff.FeatureLighting, v, ok)
	}
	if v, ok := srv.MatterRead(0xFFFD); !ok || v != onoff.Revision() {
		t.Errorf("ClusterRevision: want (%v, true), got (%v, %v)", onoff.Revision(), v, ok)
	}
}

func TestSwitchMatter_OnOffAttributeIsNotWritable(t *testing.T) {
	t.Parallel()
	_, srv, w := matterSwitch(t)
	if err := srv.MatterWrite(context.Background(), onoff.AttrOnOff, true); err == nil {
		t.Fatal("OnOff carries access R V (matter.js on-off.element.ts); a write must be refused")
	}
	if len(w.calls) != 0 {
		t.Fatalf("a refused attribute write must not reach the CCU: %d write(s)", len(w.calls))
	}
}

func TestSwitchMatter_LTWritesAcceptDecodedUint64(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
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
			_, srv, _ := matterSwitch(t)
			if err := srv.MatterWrite(ctx, tc.attr, tc.write); err != nil {
				t.Fatalf("write %s: %v", tc.name, err)
			}
			if v, _ := srv.MatterRead(tc.attr); v != tc.want {
				t.Errorf("%s after write: got %v (%T), want %v", tc.name, v, v, tc.want)
			}
		})
	}
}

func TestSwitchMatter_CommandsReachTheCCUThroughTheSink(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name string
		cmd  uint32
		want bool
	}{
		{"On", onoff.CmdOn, true},
		{"Off", onoff.CmdOff, false},
		{"OffWithEffect", onoff.CmdOffWithEffect, false},
		{"OnWithRecallGlobalScene", onoff.CmdOnWithRecallGlobalScene, true},
		{"Toggle from unobserved", onoff.CmdToggle, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, srv, w := matterSwitch(t)
			if _, err := srv.MatterInvoke(ctx, tc.cmd, nil); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			w.mu.Lock()
			defer w.mu.Unlock()
			if len(w.calls) != 1 {
				t.Fatalf("%s: %d CCU write(s), want exactly one", tc.name, len(w.calls))
			}
			c := w.calls[0]
			if c.param != hmenum.ParameterState || c.value != tc.want || c.priority != matterDispatchPriority {
				t.Errorf("%s wrote %+v, want STATE=%v at priority %v", tc.name, c, tc.want, matterDispatchPriority)
			}
			if v, _ := srv.MatterRead(onoff.AttrOnOff); v != tc.want {
				t.Errorf("%s: OnOff = %v after the accepted command, want %v", tc.name, v, tc.want)
			}
		})
	}
}

func TestSwitchMatter_ToggleFromOnWritesOff(t *testing.T) {
	t.Parallel()
	s, srv, w := matterSwitch(t)
	s.OnEvent(true)
	if _, err := srv.MatterInvoke(context.Background(), onoff.CmdToggle, nil); err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	if len(w.calls) != 1 || w.calls[0].value != false {
		t.Fatalf("Toggle from on must write STATE=false once, got %+v", w.calls)
	}
}

func TestSwitchMatter_RefusedWriteKeepsTheAttribute(t *testing.T) {
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

func TestSwitchMatter_OnWithTimedOffSwitchesOffAfterOnTime(t *testing.T) {
	t.Parallel()
	_, srv, w := matterSwitch(t)
	// OnTime is in tenths of a second; the stack ticks every 100 ms
	// (OnOffServer.ts:102) and its off reaches the Sink.
	if _, err := srv.MatterInvoke(context.Background(), onoff.CmdOnWithTimedOff, onoffspec.OnWithTimedOffRequest{OnTime: 2}); err != nil {
		t.Fatalf("OnWithTimedOff: %v", err)
	}
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != true {
		t.Fatalf("OnOff right after OnWithTimedOff: got %v, want true", v)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		n := len(w.calls)
		w.mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.calls) != 2 || w.calls[0].value != true || w.calls[1].value != false {
		t.Fatalf("timed on must write STATE=true then STATE=false, got %+v", w.calls)
	}
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != false {
		t.Errorf("OnOff after the countdown: got %v, want false", v)
	}
}

func TestSwitchMatter_UnknownCommandIsRejected(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSwitch(t)
	if _, err := srv.MatterInvoke(context.Background(), 0xFF, nil); err == nil {
		t.Fatal("unknown command: expected error")
	}
}

func TestSwitchMatter_AdvertisedListsAreTheLightingSet(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSwitch(t)
	if got, want := srv.MatterAttributes(), onoff.LightingAttributes(); !w2GenEqual(got, want) {
		t.Errorf("MatterAttributes() = %#x, want the LT set %#x", got, want)
	}
	if got, want := srv.MatterAcceptedCommands(), onoff.LightingCommands(); !w2GenEqual(got, want) {
		t.Errorf("MatterAcceptedCommands() = %#x, want the LT set %#x", got, want)
	}
	if len(srv.MatterGeneratedCommands()) != 0 {
		t.Error("OnOff commands carry no response payload")
	}
}
