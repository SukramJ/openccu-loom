// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core_test

import (
	"bytes"
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---- helpers ----

func newNetcomm(cfg core.NetworkCommissioningConfig) *core.NetworkCommissioning {
	return core.NewNetworkCommissioning(cfg)
}

func defaultNetcomm() *core.NetworkCommissioning {
	return newNetcomm(core.NetworkCommissioningConfig{})
}

// ---- ClusterID / Revision ----

func TestNetcomm_ClusterID(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	if got := nc.MatterClusterID(); got != 0x0031 {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x0031", got)
	}
}

func TestNetcomm_ClusterRevision(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	v, ok := nc.MatterRead(cluster.AttrGlobalClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if v.(uint16) != 2 {
		t.Fatalf("ClusterRevision = %v, want 2", v)
	}
}

// ---- Constructor defaults ----

func TestNetcomm_DefaultInterfaceID(t *testing.T) {
	t.Parallel()
	nc := newNetcomm(core.NetworkCommissioningConfig{InterfaceID: nil})
	v, ok := nc.MatterRead(0x0001) // Networks
	if !ok {
		t.Fatal("Networks: ok=false")
	}
	nets := v.([]core.NetworkInfoStruct)
	if len(nets) != 1 {
		t.Fatalf("Networks len = %d, want 1", len(nets))
	}
	if !bytes.Equal(nets[0].NetworkID, []byte("eth0")) {
		t.Fatalf("NetworkID = %q, want eth0", nets[0].NetworkID)
	}
}

func TestNetcomm_CustomInterfaceID(t *testing.T) {
	t.Parallel()
	custom := []byte("enp3s0")
	nc := newNetcomm(core.NetworkCommissioningConfig{InterfaceID: custom})
	v, ok := nc.MatterRead(0x0001)
	if !ok {
		t.Fatal("Networks: ok=false")
	}
	nets := v.([]core.NetworkInfoStruct)
	if !bytes.Equal(nets[0].NetworkID, custom) {
		t.Fatalf("NetworkID = %q, want %q", nets[0].NetworkID, custom)
	}
}

// ---- Read attributes ----

func TestNetcomm_ReadMaxNetworks(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	v, ok := nc.MatterRead(0x0000)
	if !ok {
		t.Fatal("MaxNetworks: ok=false")
	}
	if v.(uint8) != 1 {
		t.Fatalf("MaxNetworks = %v, want 1", v)
	}
}

func TestNetcomm_ReadNetworks_OneEntry_Connected(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	v, ok := nc.MatterRead(0x0001)
	if !ok {
		t.Fatal("Networks: ok=false")
	}
	nets := v.([]core.NetworkInfoStruct)
	if len(nets) != 1 {
		t.Fatalf("Networks len = %d, want 1", len(nets))
	}
	if !nets[0].Connected {
		t.Fatal("Networks[0].Connected = false, want true")
	}
}

func TestNetcomm_ReadScanMaxTimeSeconds_NotAdvertisedOnEthernet(t *testing.T) {
	t.Parallel()
	// ScanMaxTimeSeconds (0x0002) has conformance WI|TH — not applicable
	// to Ethernet-only mode. MatterRead must return ok=false so the IM
	// dispatcher signals UnsupportedAttribute.
	nc := defaultNetcomm()
	_, ok := nc.MatterRead(0x0002)
	if ok {
		t.Fatal("ScanMaxTimeSeconds: ok=true on ETH-only, want false (WI|TH conformance)")
	}
}

func TestNetcomm_ReadConnectMaxTimeSeconds_NotAdvertisedOnEthernet(t *testing.T) {
	t.Parallel()
	// ConnectMaxTimeSeconds (0x0003) has conformance WI|TH — not applicable
	// to Ethernet-only mode. MatterRead must return ok=false so the IM
	// dispatcher signals UnsupportedAttribute.
	nc := defaultNetcomm()
	_, ok := nc.MatterRead(0x0003)
	if ok {
		t.Fatal("ConnectMaxTimeSeconds: ok=true on ETH-only, want false (WI|TH conformance)")
	}
}

func TestNetcomm_ReadInterfaceEnabled_InitialTrue(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	v, ok := nc.MatterRead(0x0004)
	if !ok {
		t.Fatal("InterfaceEnabled: ok=false")
	}
	if !v.(bool) {
		t.Fatal("InterfaceEnabled = false initially, want true")
	}
}

func TestNetcomm_ReadLastNetworkingStatus_NilInitially(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	v, ok := nc.MatterRead(0x0005)
	if !ok {
		t.Fatal("LastNetworkingStatus: ok=false")
	}
	if v != nil {
		t.Fatalf("LastNetworkingStatus = %v, want nil", v)
	}
}

func TestNetcomm_ReadLastNetworkID_NilInitially(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	v, ok := nc.MatterRead(0x0006)
	if !ok {
		t.Fatal("LastNetworkID: ok=false")
	}
	if v != nil {
		t.Fatalf("LastNetworkID = %v, want nil", v)
	}
}

func TestNetcomm_ReadLastConnectErrorValue_NilInitially(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	v, ok := nc.MatterRead(0x0007)
	if !ok {
		t.Fatal("LastConnectErrorValue: ok=false")
	}
	if v != nil {
		t.Fatalf("LastConnectErrorValue = %v, want nil", v)
	}
}

func TestNetcomm_ReadFeatureMap_Ethernet(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	v, ok := nc.MatterRead(cluster.AttrGlobalFeatureMap)
	if !ok {
		t.Fatal("FeatureMap: ok=false")
	}
	if v.(uint32) != core.NetworkCommFeatureEthernet {
		t.Fatalf("FeatureMap = 0x%04X, want NetworkCommFeatureEthernet (0x%04X)",
			v.(uint32), core.NetworkCommFeatureEthernet)
	}
}

func TestNetcomm_ReadUnknownAttr(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	v, ok := nc.MatterRead(0xDEAD)
	if ok || v != nil {
		t.Fatalf("unknown attr: got (%v, %v), want (nil, false)", v, ok)
	}
}

// ---- Write InterfaceEnabled ----

func TestNetcomm_WriteInterfaceEnabled_False(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	if err := nc.MatterWrite(context.Background(), 0x0004, false, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite InterfaceEnabled=false: %v", err)
	}
	v, ok := nc.MatterRead(0x0004)
	if !ok {
		t.Fatal("InterfaceEnabled: ok=false")
	}
	if v.(bool) {
		t.Fatal("InterfaceEnabled = true after write false, want false")
	}
}

func TestNetcomm_WriteInterfaceEnabled_WrongType(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	err := nc.MatterWrite(context.Background(), 0x0004, "yes", hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for wrong type, got nil")
	}
}

func TestNetcomm_WriteReadOnlyAttr(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	// MaxNetworks (0x0000) is read-only.
	err := nc.MatterWrite(context.Background(), 0x0000, uint8(2), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for write to read-only attr, got nil")
	}
}

// ---- Invoke — all rejected for Ethernet-only ----

func TestNetcomm_Invoke_AllRejected(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	ctx := context.Background()
	for _, cmdID := range []uint32{0x00, 0x02, 0x03, 0x04, 0x06, 0x08} {
		_, err := nc.MatterInvoke(ctx, cmdID, nil, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("MatterInvoke(0x%02X) expected error, got nil", cmdID)
		}
	}
}

// ---- MatterReportable ----

func TestNetcomm_Reportable_ContainsExpected(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	attrs := nc.MatterReportable()
	for _, want := range []uint32{0x0001, 0x0004, 0x0005} { // Networks, InterfaceEnabled, LastNetworkingStatus
		if !slices.Contains(attrs, want) {
			t.Errorf("MatterReportable missing attr 0x%04X", want)
		}
	}
}

// ---- Defensive copy for Networks ----

func TestNetcomm_Networks_DefensiveCopy(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	v, _ := nc.MatterRead(0x0001)
	nets := v.([]core.NetworkInfoStruct)
	// Mutate the returned slice.
	nets[0].Connected = false
	nets[0].NetworkID = []byte("tampered")

	// Read again — internal state must be unchanged.
	v2, _ := nc.MatterRead(0x0001)
	nets2 := v2.([]core.NetworkInfoStruct)
	if !nets2[0].Connected {
		t.Fatal("defensive copy failed: Connected was mutated in internal state")
	}
	if bytes.Equal(nets2[0].NetworkID, []byte("tampered")) {
		t.Fatal("defensive copy failed: NetworkID was mutated in internal state")
	}
}

// ---- Concurrent safety ----

func TestNetcomm_Concurrent_Race(t *testing.T) {
	t.Parallel()
	nc := defaultNetcomm()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 3 {
			case 0:
				_, _ = nc.MatterRead(0x0001)
				_, _ = nc.MatterRead(0x0004)
			case 1:
				_ = nc.MatterWrite(ctx, 0x0004, i%2 == 0, hmenum.CommandPriorityHigh)
			case 2:
				_, _ = nc.MatterRead(cluster.AttrGlobalFeatureMap)
			}
		}(i)
	}
	wg.Wait()
}

func TestNetcomm_MatterDataVersionNonZero(t *testing.T) {
	t.Parallel()
	n := core.NewNetworkCommissioning(core.NetworkCommissioningConfig{})
	// Seeded non-zero at construction so DataVersionFilter=0 doesn't
	// produce false-positive cache hits.
	if n.MatterDataVersion() == 0 {
		t.Fatal("MatterDataVersion() = 0 — expected non-zero sentinel")
	}
}

func TestNetcomm_MatterAttributesSurface(t *testing.T) {
	t.Parallel()
	n := core.NewNetworkCommissioning(core.NetworkCommissioningConfig{})
	list := n.MatterAttributes()
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	// MaxNetworks (0x0000) and Networks (0x0001) must be present.
	for _, want := range []uint32{0x0000, 0x0001} {
		if !have[want] {
			t.Errorf("MatterAttributes() missing attr 0x%04X", want)
		}
	}
}
