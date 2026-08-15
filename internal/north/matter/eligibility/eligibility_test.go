// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package eligibility_test

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/eligibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ---------------------------------------------------------------------------
// Minimal fakes — no real model packages imported.
// ---------------------------------------------------------------------------

// fakeClusterServer implements interfaces.MatterClusterServer.
type fakeClusterServer struct {
	clusterID uint32
}

func (f *fakeClusterServer) MatterClusterID() uint32 { return f.clusterID }
func (f *fakeClusterServer) MatterRead(_ uint32) (any, bool) {
	return nil, false
}

func (f *fakeClusterServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return nil
}

func (f *fakeClusterServer) MatterInvoke(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, nil
}
func (f *fakeClusterServer) MatterReportable() []uint32 { return nil }

// fakeEndpointSource implements interfaces.MatterEndpointSource.
type fakeEndpointSource struct {
	deviceType uint16
	clusters   []interfaces.MatterClusterServer
}

func (f *fakeEndpointSource) MatterDeviceType() uint16 { return f.deviceType }
func (f *fakeEndpointSource) MatterClusterServers() []interfaces.MatterClusterServer {
	return f.clusters
}

// fakeMeasurementSource implements interfaces.MatterMeasurementSource.
type fakeMeasurementSource struct {
	class interfaces.MatterMeasurementClass
}

func (f *fakeMeasurementSource) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return f.class
}

// fakeEligibilitySource implements both MatterEndpointSource AND
// MatterEligibilitySource. The latter overrides Classify's decision.
type fakeEligibilitySource struct {
	fakeEndpointSource
	verdict interfaces.MatterEligibilityVerdict
}

func (f *fakeEligibilitySource) MatterEligibility() interfaces.MatterEligibilityVerdict {
	return f.verdict
}

// opaqueSource implements neither endpoint nor measurement interface.
type opaqueSource struct{}

// ---------------------------------------------------------------------------
// Classify tests
// ---------------------------------------------------------------------------

func TestClassify_EndpointSource_Returns_Mappable(t *testing.T) {
	t.Parallel()
	src := &fakeEndpointSource{
		deviceType: 0x010A,
		clusters:   []interfaces.MatterClusterServer{&fakeClusterServer{clusterID: 0x0006}},
	}
	got := eligibility.Classify(src)
	if got.State != eligibility.StateMappable {
		t.Errorf("expected Mappable, got %v (reason: %q)", got.State, got.Reason)
	}
	if got.DeviceType != 0x010A {
		t.Errorf("device type: got %d, want 0x010A", got.DeviceType)
	}
	if len(got.Clusters) != 1 || got.Clusters[0] != 0x0006 {
		t.Errorf("clusters: got %v, want [0x0006]", got.Clusters)
	}
}

func TestClassify_MeasurementSource_NonNone_Returns_Mappable(t *testing.T) {
	t.Parallel()
	src := &fakeMeasurementSource{class: interfaces.MatterMeasurementTemperature}
	got := eligibility.Classify(src)
	if got.State != eligibility.StateMappable {
		t.Errorf("expected Mappable, got %v (reason: %q)", got.State, got.Reason)
	}
	if len(got.Clusters) == 0 {
		t.Error("expected at least one cluster ID")
	}
}

func TestClassify_Nil_Returns_Unmappable_NilSource(t *testing.T) {
	t.Parallel()
	got := eligibility.Classify(nil)
	if got.State != eligibility.StateUnmappable {
		t.Errorf("expected Unmappable, got %v", got.State)
	}
	if got.Reason != "nil source" {
		t.Errorf("reason: got %q, want %q", got.Reason, "nil source")
	}
}

func TestClassify_EndpointSource_ZeroDeviceType_Returns_Unmappable(t *testing.T) {
	t.Parallel()
	src := &fakeEndpointSource{
		deviceType: 0,
		clusters:   []interfaces.MatterClusterServer{&fakeClusterServer{clusterID: 0x0006}},
	}
	got := eligibility.Classify(src)
	if got.State != eligibility.StateUnmappable {
		t.Errorf("expected Unmappable for zero device type, got %v", got.State)
	}
}

func TestClassify_EndpointSource_NoClusters_Returns_Unmappable(t *testing.T) {
	t.Parallel()
	src := &fakeEndpointSource{
		deviceType: 0x010A,
		clusters:   nil,
	}
	got := eligibility.Classify(src)
	if got.State != eligibility.StateUnmappable {
		t.Errorf("expected Unmappable for empty clusters, got %v", got.State)
	}
}

func TestClassify_MeasurementSource_None_Returns_Unmappable(t *testing.T) {
	t.Parallel()
	src := &fakeMeasurementSource{class: interfaces.MatterMeasurementNone}
	got := eligibility.Classify(src)
	if got.State != eligibility.StateUnmappable {
		t.Errorf("expected Unmappable for None class, got %v", got.State)
	}
	if !strings.Contains(got.Reason, "None") && !strings.Contains(got.Reason, "none") && !strings.Contains(got.Reason, "measurement class") {
		t.Errorf("reason should mention measurement class, got %q", got.Reason)
	}
}

func TestClassify_OpaqueSource_Returns_Unmappable_NoProjection(t *testing.T) {
	t.Parallel()
	src := &opaqueSource{}
	got := eligibility.Classify(src)
	if got.State != eligibility.StateUnmappable {
		t.Errorf("expected Unmappable for opaque source, got %v", got.State)
	}
	if !strings.Contains(got.Reason, "no Matter projection") {
		t.Errorf("reason should mention 'no Matter projection', got %q", got.Reason)
	}
}

func TestClassify_EligibilitySourceOverride_Partial(t *testing.T) {
	t.Parallel()
	// The source implements BOTH MatterEndpointSource (which would give
	// Mappable) AND MatterEligibilitySource (which returns Partial).
	// MatterEligibilitySource must win.
	src := &fakeEligibilitySource{
		fakeEndpointSource: fakeEndpointSource{
			deviceType: 0x0302,
			clusters:   []interfaces.MatterClusterServer{&fakeClusterServer{clusterID: 0x0402}},
		},
		verdict: interfaces.MatterEligibilityVerdict{
			State:  eligibility.StatePartial,
			Reason: "siren tones not mappable",
		},
	}
	got := eligibility.Classify(src)
	if got.State != eligibility.StatePartial {
		t.Errorf("expected Partial, got %v", got.State)
	}
	if got.Reason != "siren tones not mappable" {
		t.Errorf("reason: got %q, want %q", got.Reason, "siren tones not mappable")
	}
}

// ---------------------------------------------------------------------------
// DeriveMatterEligibility tests
// ---------------------------------------------------------------------------

func TestDeriveMatterEligibility_CustomDP_Returns_ClusterIDs(t *testing.T) {
	t.Parallel()
	srv1 := &fakeClusterServer{clusterID: 0x0006}
	srv2 := &fakeClusterServer{clusterID: 0x0008}
	src := &fakeEndpointSource{
		deviceType: 0x0100,
		clusters:   []interfaces.MatterClusterServer{srv1, srv2},
	}
	got := eligibility.DeriveMatterEligibility(src)
	if got.State != eligibility.StateMappable {
		t.Errorf("expected Mappable, got %v", got.State)
	}
	if len(got.Clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d: %v", len(got.Clusters), got.Clusters)
	}
	if got.Clusters[0] != 0x0006 || got.Clusters[1] != 0x0008 {
		t.Errorf("cluster IDs: got %v", got.Clusters)
	}
}

func TestDeriveMatterEligibility_MeasurementSource_UsesClassDeviceTypeAndClusterID(t *testing.T) {
	t.Parallel()
	src := &fakeMeasurementSource{class: interfaces.MatterMeasurementHumidity}
	got := eligibility.DeriveMatterEligibility(src)
	if got.State != eligibility.StateMappable {
		t.Errorf("expected Mappable, got %v", got.State)
	}
	wantDT := interfaces.MatterMeasurementClassDeviceType(interfaces.MatterMeasurementHumidity)
	wantCl := interfaces.MatterMeasurementClassClusterID(interfaces.MatterMeasurementHumidity)
	if got.DeviceType != wantDT {
		t.Errorf("device type: got %d, want %d", got.DeviceType, wantDT)
	}
	if len(got.Clusters) != 1 || got.Clusters[0] != wantCl {
		t.Errorf("clusters: got %v, want [%d]", got.Clusters, wantCl)
	}
}
