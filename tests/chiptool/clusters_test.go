// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// discoverEndpointsWith finds every bridged endpoint whose
// ServerList contains the given cluster id. Returns the first
// `limit` matches; pass limit=0 to collect all.
//
// One wildcard-endpoint server-list read replaces N per-EP reads —
// sequential per-EP reads on a fleet of ~30+ bridged endpoints
// accumulate CASE sessions in the daemon and start timing out around
// EP 25+. The wildcard call opens one CASE session and parses every
// endpoint's ServerList out of the single response.
func discoverEndpointsWith(t *testing.T, b interface {
	MatterPort() int
}, cluster uint32, limit int,
) []uint16 {
	t.Helper()
	br := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	slOut, err := br.SharedCtl.ReadAttr(ctx, t, "descriptor", "server-list", 0xFFFF)
	if err != nil {
		t.Fatalf("wildcard server-list: %v", err)
	}
	perEP := harness.ServerListIDsPerEndpoint(slOut)
	eps := make([]uint16, 0, len(perEP))
	for ep := range perEP {
		eps = append(eps, ep)
	}
	// Sort ascending so callers get a deterministic "first" endpoint.
	for i := 1; i < len(eps); i++ {
		for j := i; j > 0 && eps[j-1] > eps[j]; j-- {
			eps[j-1], eps[j] = eps[j], eps[j-1]
		}
	}
	var hits []uint16
	for _, ep := range eps {
		if harness.HasCluster(perEP[ep], cluster) {
			hits = append(hits, ep)
			if limit > 0 && len(hits) >= limit {
				break
			}
		}
	}
	return hits
}

// TestCluster_OnOff_Read finds the first endpoint advertising the
// OnOff cluster (0x0006), reads OnOff.OnOff, and asserts the read
// returned a Boolean value.
//
// Mirrors v9 capability report T5.
func TestCluster_OnOff_Read(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0006, 1)
	if len(eps) == 0 {
		t.Skip("no OnOff endpoint surfaced — godevccu fleet has no Switch/PSM device")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "onoff", "on-off", eps[0])
	if err != nil {
		t.Fatalf("read on-off: %v", err)
	}
	if _, ok := harness.FindAttrBool(out, "OnOff"); !ok {
		t.Errorf("OnOff boolean not parsed:\n%s", out)
	}
}

// TestCluster_OnOff_GlobalAttributes reads the four mandatory
// global attributes (FeatureMap, ClusterRevision, AttributeList,
// AcceptedCommandList) on the first OnOff endpoint.
func TestCluster_OnOff_GlobalAttributes(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0006, 1)
	if len(eps) == 0 {
		t.Skip("no OnOff endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, attr := range []string{"feature-map", "cluster-revision", "attribute-list", "accepted-command-list"} {
		out, err := b.SharedCtl.ReadAttr(ctx, t, "onoff", attr, eps[0])
		if err != nil {
			t.Errorf("read onoff.%s: %v", attr, err)
			continue
		}
		if !harness.AttrReadOK(out) {
			t.Errorf("onoff.%s did not report success:\n%s", attr, out)
		}
	}
}

// TestCluster_TemperatureMeasurement_Read finds the first endpoint
// advertising TemperatureMeasurement (0x0402), reads MeasuredValue,
// and asserts the read succeeded. The numeric value is godevccu-
// dependent so we do not pin it.
func TestCluster_TemperatureMeasurement_Read(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0402, 1)
	if len(eps) == 0 {
		t.Skip("no TemperatureMeasurement endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "temperaturemeasurement", "measured-value", eps[0])
	if err != nil {
		t.Fatalf("read measured-value: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Errorf("temperature read did not succeed:\n%s", out)
	}
	if !strings.Contains(out, "MeasuredValue") {
		t.Errorf("MeasuredValue marker missing:\n%s", out)
	}
}

// TestCluster_BooleanState_Read finds the first endpoint
// advertising BooleanState (0x0045) and reads StateValue. HmIP-SWSD
// (smoke detector) maps onto this cluster.
func TestCluster_BooleanState_Read(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0045, 1)
	if len(eps) == 0 {
		t.Skip("no BooleanState endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "booleanstate", "state-value", eps[0])
	if err != nil {
		t.Fatalf("read state-value: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Errorf("booleanstate read did not succeed:\n%s", out)
	}
}

// TestCluster_WindowCovering_Read finds the first endpoint
// advertising WindowCovering (0x0102) and reads
// CurrentPositionLiftPercent100ths. HmIP-BROLL maps here.
func TestCluster_WindowCovering_Read(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0102, 1)
	if len(eps) == 0 {
		t.Skip("no WindowCovering endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "windowcovering", "current-position-lift-percent100ths", eps[0])
	if err != nil {
		t.Fatalf("read current-position-lift-percent100ths: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Errorf("windowcovering read did not succeed:\n%s", out)
	}
}

// TestCluster_LevelControl_Read finds the first endpoint advertising
// LevelControl (0x0008) and reads CurrentLevel.
func TestCluster_LevelControl_Read(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0008, 1)
	if len(eps) == 0 {
		t.Skip("no LevelControl endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "levelcontrol", "current-level", eps[0])
	if err != nil {
		t.Fatalf("read current-level: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Errorf("levelcontrol read did not succeed:\n%s", out)
	}
}
