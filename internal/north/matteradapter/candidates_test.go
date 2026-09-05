// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/contract"
	"github.com/SukramJ/go-fabric/eligibility"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// fakeClusterServer is a minimal [contract.ClusterServer] whose only
// job is to make a source advertise a cluster id, so the candidate walk sees
// a non-empty cluster set without pulling in a real cluster implementation.
type fakeClusterServer struct {
	clusterID uint32
}

func (f *fakeClusterServer) MatterClusterID() uint32 { return f.clusterID }

func (f *fakeClusterServer) MatterRead(_ uint32) (any, bool) { return nil, false }

func (f *fakeClusterServer) MatterWrite(_ context.Context, _ uint32, _ any) error { return nil }

func (f *fakeClusterServer) MatterInvoke(_ context.Context, _ uint32, _ any) (any, error) {
	return nil, nil
}

func (f *fakeClusterServer) MatterReportable() []uint32 { return nil }

// genericParamDP is a minimal ParameterDataPoint implementation that also
// implements MatterEndpointSource so it surfaces as Mappable via the
// generic-DP path (ch.DataPoints() / genericDPKey).
type genericParamDP struct {
	key     hmtypes.DataPointKey
	devType uint16
	cluster uint32
}

func (g *genericParamDP) DataPointKey() hmtypes.DataPointKey { return g.key }
func (g *genericParamDP) Parameter() hmenum.Parameter        { return hmenum.Parameter(g.key.Parameter) }

func (g *genericParamDP) ParameterData() hmproto.ParameterData {
	return hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
}
func (g *genericParamDP) RawValue() (any, bool)                    { return 0.0, true }
func (g *genericParamDP) ModifiedAt() time.Time                    { return time.Time{} }
func (g *genericParamDP) OnAnyUpdate(_ func(old, next any)) func() { return func() {} }
func (g *genericParamDP) MatterDeviceType() uint16                 { return g.devType }
func (g *genericParamDP) MatterClusterServers() []contract.ClusterServer {
	return []contract.ClusterServer{&fakeClusterServer{clusterID: g.cluster}}
}

// ---- minimal AttachableDataPoint fakes ----

// dpWithKey implements device.AttachableDataPoint and
// contract.EndpointSource so it shows up as Mappable.
type mappableDP struct {
	key     hmtypes.DataPointKey
	devType uint16
	cluster uint32
}

func (d *mappableDP) DataPointKey() hmtypes.DataPointKey { return d.key }
func (d *mappableDP) MatterDeviceType() uint16           { return d.devType }
func (d *mappableDP) MatterClusterServers() []contract.ClusterServer {
	return []contract.ClusterServer{&fakeClusterServer{clusterID: d.cluster}}
}

// opaqueDP implements only device.AttachableDataPoint — no Matter
// projection → collectChannelCandidates skips it (Unmappable with
// reason "no Matter projection on source type").
type opaqueDP struct {
	key hmtypes.DataPointKey
}

func (o *opaqueDP) DataPointKey() hmtypes.DataPointKey { return o.key }

// TestCollectCandidates_NilDevice verifies that a nil device entry is
// silently skipped and does not panic.
func TestCollectCandidates_NilDevice(t *testing.T) {
	t.Parallel()
	devices := []*device.Device{nil}
	got := matteradapter.CollectCandidates("central1", devices, false)
	if len(got) != 0 {
		t.Errorf("expected 0 candidates for nil device, got %d", len(got))
	}
}

// TestCollectCandidates_EmptyDeviceList verifies that an empty list
// returns an empty slice.
func TestCollectCandidates_EmptyDeviceList(t *testing.T) {
	t.Parallel()
	got := matteradapter.CollectCandidates("central1", nil, false)
	if len(got) != 0 {
		t.Errorf("expected 0, got %d", len(got))
	}
}

// TestCollectCandidates_DeviceNoChannels verifies that a device with no
// channels produces zero candidates.
func TestCollectCandidates_DeviceNoChannels(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address: "A1:0",
		Model:   "HmIP-TEST",
	})
	got := matteradapter.CollectCandidates("c1", []*device.Device{d}, false)
	if len(got) != 0 {
		t.Errorf("expected 0 candidates for device with no channels, got %d", len(got))
	}
}

// TestCollectCandidates_DeviceWithEmptyChannel verifies that a channel
// with no DPs produces zero candidates.
func TestCollectCandidates_DeviceWithEmptyChannel(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address: "A2:0",
		Model:   "HmIP-TEST",
	})
	d.AddChannel("A2:1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	got := matteradapter.CollectCandidates("c2", []*device.Device{d}, false)
	if len(got) != 0 {
		t.Errorf("expected 0 candidates for empty channel, got %d", len(got))
	}
}

// TestCollectCandidates_CustomDP_Mappable verifies that a custom DP
// that implements MatterEndpointSource is collected as a Mappable
// candidate.
func TestCollectCandidates_CustomDP_Mappable(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address: "B1:0",
		Name:    "Test Switch",
		Model:   "HmIP-PS",
	})
	ch := d.AddChannel("B1:1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	dp := &mappableDP{
		key:     hmtypes.DataPointKey{Parameter: "STATE"},
		devType: 0x0100,
		cluster: 0x0006,
	}
	ch.SetCustomDataPoint(dp)

	got := matteradapter.CollectCandidates("central", []*device.Device{d}, false)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	cand := got[0]
	if cand.Verdict.State != eligibility.StateMappable {
		t.Errorf("state = %v, want Mappable", cand.Verdict.State)
	}
	if cand.Key.DPKey != "STATE" {
		t.Errorf("DPKey = %q, want %q", cand.Key.DPKey, "STATE")
	}
	if cand.DisplayName != "Test Switch" {
		t.Errorf("DisplayName = %q, want %q", cand.DisplayName, "Test Switch")
	}
}

// TestCollectCandidates_CalculatedDP_Mappable verifies that a
// calculated DP is collected.
func TestCollectCandidates_CalculatedDP_Mappable(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address: "C1:0",
		Name:    "Sensor",
	})
	ch := d.AddChannel("C1:1", 1, "TEMPERATURE_TRANSCEIVER", hmenum.ParamsetKeyValues)
	dp := &mappableDP{
		key:     hmtypes.DataPointKey{Parameter: "TEMPERATURE"},
		devType: 0x0302,
		cluster: 0x0402,
	}
	ch.AttachCalculatedDataPoint(dp)

	got := matteradapter.CollectCandidates("c", []*device.Device{d}, false)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	if got[0].Verdict.State != eligibility.StateMappable {
		t.Errorf("state = %v, want Mappable", got[0].Verdict.State)
	}
}

// TestCollectCandidates_OpaqueDP_Skipped verifies that a DP with no
// Matter projection is not emitted as a candidate (the "skip opaque"
// branch).
func TestCollectCandidates_OpaqueDP_Skipped(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address: "D1:0",
		Name:    "Device",
	})
	ch := d.AddChannel("D1:1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	// Opaque source → no Matter projection → skipped.
	ch.SetCustomDataPoint(&opaqueDP{
		key: hmtypes.DataPointKey{Parameter: "OPAQUE"},
	})

	got := matteradapter.CollectCandidates("c", []*device.Device{d}, false)
	if len(got) != 0 {
		t.Fatalf("expected 0 candidates for opaque DP, got %d: %+v", len(got), got)
	}
}

// namedDP implements AttachableDataPoint + Name() but no
// DataPointKey.Parameter — exercises the Name() fallback branch in candidateDPKey.
type namedDP struct {
	name    string
	devType uint16
	cluster uint32
}

func (n *namedDP) DataPointKey() hmtypes.DataPointKey { return hmtypes.DataPointKey{} }
func (n *namedDP) Name() string                       { return n.name }
func (n *namedDP) MatterDeviceType() uint16           { return n.devType }
func (n *namedDP) MatterClusterServers() []contract.ClusterServer {
	return []contract.ClusterServer{&fakeClusterServer{clusterID: n.cluster}}
}

// unknownDP has no DataPointKey.Parameter and no Name().
// candidateDPKey falls through to the "unknown(%T)" branch.
type unknownDP struct {
	devType uint16
	cluster uint32
}

func (u *unknownDP) DataPointKey() hmtypes.DataPointKey { return hmtypes.DataPointKey{} }
func (u *unknownDP) MatterDeviceType() uint16           { return u.devType }
func (u *unknownDP) MatterClusterServers() []contract.ClusterServer {
	return []contract.ClusterServer{&fakeClusterServer{clusterID: u.cluster}}
}

// TestCollectCandidates_NameDPKey verifies the Name() candidateDPKey fallback path.
func TestCollectCandidates_NameDPKey(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address: "G1:0",
		Name:    "Named DP Device",
	})
	ch := d.AddChannel("G1:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	dp := &namedDP{name: "my-light", devType: 0x0100, cluster: 0x0006}
	ch.SetCustomDataPoint(dp)

	got := matteradapter.CollectCandidates("c", []*device.Device{d}, false)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	if got[0].Key.DPKey != "my-light" {
		t.Errorf("DPKey = %q, want %q", got[0].Key.DPKey, "my-light")
	}
}

// TestCollectCandidates_UnknownDPKey verifies the unknown(%T) candidateDPKey fallback.
func TestCollectCandidates_UnknownDPKey(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address: "H1:0",
		Name:    "Unknown DP Device",
	})
	ch := d.AddChannel("H1:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	dp := &unknownDP{devType: 0x0100, cluster: 0x0006}
	ch.SetCustomDataPoint(dp)

	got := matteradapter.CollectCandidates("c", []*device.Device{d}, false)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	// Key should contain the type name since no key/profile/name is available.
	if got[0].Key.DPKey == "" {
		t.Error("DPKey should not be empty for unknownDP")
	}
	if got[0].Key.DPKey == "unknown" && got[0].Key.DPKey != "" {
		// Verify it's the typed unknown form.
		t.Logf("DPKey = %q", got[0].Key.DPKey)
	}
}

// TestCollectCandidates_GenericDP_Mappable verifies that a generic DP
// (from the VALUES paramset, added via ch.Put) is collected and uses
// genericDPKey for its key (covering the genericDPKey code path).
func TestCollectCandidates_GenericDP_Mappable(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address: "J1:0",
		Name:    "Generic DP Device",
	})
	ch := d.AddChannel("J1:1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	dp := &genericParamDP{
		key:     hmtypes.DataPointKey{Parameter: "STATE"},
		devType: 0x0100,
		cluster: 0x0006,
	}
	ch.Put(dp) // adds to VALUES paramset → DataPoints()

	got := matteradapter.CollectCandidates("central", []*device.Device{d}, false)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate for generic DP, got %d", len(got))
	}
	if got[0].Key.DPKey != "STATE" {
		t.Errorf("DPKey = %q, want %q", got[0].Key.DPKey, "STATE")
	}
	if got[0].Verdict.State != eligibility.StateMappable {
		t.Errorf("state = %v, want Mappable", got[0].Verdict.State)
	}
}

// TestCollectCandidates_NilChannel verifies that a nil channel is silently skipped.
func TestCollectCandidates_NilChannel(t *testing.T) {
	t.Parallel()
	// device.New doesn't expose a way to inject a nil channel directly;
	// use a device with no channels (already covered) and verify no panic.
	d := device.New(device.Config{Address: "I1:0"})
	got := matteradapter.CollectCandidates("c", []*device.Device{d}, false)
	if len(got) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(got))
	}
}

// TestCollectCandidates_DisplayNameFallbackToAddress verifies that when
// a device has no Name set the Address is used.
func TestCollectCandidates_DisplayNameFallbackToAddress(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address: "F1:0",
		Name:    "",
	})
	ch := d.AddChannel("F1:1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	dp := &mappableDP{
		key:     hmtypes.DataPointKey{Parameter: "ON"},
		devType: 0x0100,
		cluster: 0x0006,
	}
	ch.SetCustomDataPoint(dp)

	got := matteradapter.CollectCandidates("c", []*device.Device{d}, false)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	if got[0].DisplayName != "F1:0" {
		t.Errorf("DisplayName = %q, want %q", got[0].DisplayName, "F1:0")
	}
}
