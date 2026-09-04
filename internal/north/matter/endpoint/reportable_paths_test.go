// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package endpoint_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/matterport"
)

// stubFloatMeasNotifier is a minimal [matterport.FloatMeasurementSource]
// + [matterport.ChangeNotifier] that reports Temperature class.
// Used to wire a bridged measurement endpoint that ReportablePaths can
// walk over.
type stubFloatMeasNotifier struct {
	class matterport.MeasurementClass
	val   float64
	obs   bool
}

func (s *stubFloatMeasNotifier) DataPointKey() hmtypes.DataPointKey {
	return hmtypes.DataPointKey{ChannelAddress: "RPTEST:1", Parameter: "TEMPERATURE"}
}

func (s *stubFloatMeasNotifier) MatterMeasurementClass() matterport.MeasurementClass {
	return s.class
}
func (s *stubFloatMeasNotifier) MatterFloatValue() (float64, bool) { return s.val, s.obs }
func (s *stubFloatMeasNotifier) OnMatterValueChanged(_ func()) func() {
	return func() {}
}

// newTempMeasBridgedEndpoint builds a bridged *Endpoint (ID ≥ 2)
// backed by a Temperature measurement source via the real Assembler so
// the resulting endpoint reflects the exact production shape.
func newTempMeasBridgedEndpoint(t *testing.T) *endpoint.Endpoint {
	t.Helper()
	ctx := context.Background()

	dev := newDevice("RPDEV0001", "TempSensor")
	ch := addChannel(dev, "RPDEV0001:1", 1)

	src := &stubFloatMeasNotifier{
		class: matterport.MeasurementTemperature,
		val:   21.0,
		obs:   true,
	}
	ch.AttachCalculatedDataPoint(src)

	cfg := validConfig()
	cfg.IncludeMeasurements = true
	a, err := endpoint.New(newFakeStore(), cfg, nil)
	if err != nil {
		t.Fatalf("endpoint.New: %v", err)
	}
	snap := endpoint.Snapshot{CentralName: "ccu1", Devices: []*device.Device{dev}}
	top, err := a.Assemble(ctx, []endpoint.Snapshot{snap})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	bridged := top.Bridged()
	if len(bridged) != 1 {
		t.Fatalf("expected 1 bridged endpoint, got %d", len(bridged))
	}
	return bridged[0]
}

// TestReportablePaths_BridgedEndpoint_CollectsAttributesFromAllServers
// exercises the happy path: a Temperature-class measurement endpoint
// produces one or more ConcreteAttributePaths. All paths must have
// HasEndpoint/HasCluster/HasAttribute set and must carry the endpoint's
// own ID. The result must include at least one path whose cluster is
// 0x0402 (TemperatureMeasurement) — the primary measurement cluster for
// the source used here. Other clusters (Identify 0x0003, Descriptor
// 0x001D, BridgedDeviceBasicInformation 0x0039) may also appear in the
// slice because ReportablePaths walks every cluster server.
func TestReportablePaths_BridgedEndpoint_CollectsAttributesFromAllServers(t *testing.T) {
	t.Parallel()
	ep := newTempMeasBridgedEndpoint(t)

	paths := endpoint.ReportablePaths(ep)
	if len(paths) == 0 {
		t.Fatal("ReportablePaths returned no paths for a Temperature measurement endpoint")
	}
	var foundTemp bool
	for _, p := range paths {
		if !p.HasEndpoint {
			t.Errorf("path %+v: HasEndpoint must be true", p)
		}
		if !p.HasCluster {
			t.Errorf("path %+v: HasCluster must be true", p)
		}
		if !p.HasAttribute {
			t.Errorf("path %+v: HasAttribute must be true", p)
		}
		if p.Endpoint != ep.ID {
			t.Errorf("path %+v: Endpoint = %d, want %d", p, p.Endpoint, ep.ID)
		}
		const tempCluster uint32 = 0x0402 // TemperatureMeasurement
		if p.Cluster == tempCluster {
			foundTemp = true
		}
	}
	if !foundTemp {
		t.Errorf("expected at least one path with cluster 0x0402 (TemperatureMeasurement), got %v", paths)
	}
}

// TestReportablePaths_AirQualityEndpoint_CoversBothDerivedClusters
// verifies that an air-quality endpoint reports on the AirQuality
// cluster as well as the concentration cluster it derives from. Both
// values move on the same push, and the AirQuality cluster server has no
// change notifier of its own — it relies on this path set being wired to
// the concentration source, so a missing entry would leave the level
// stale on every controller until the next full read.
func TestReportablePaths_AirQualityEndpoint_CoversBothDerivedClusters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev := newDevice("AQDEV0001", "CO2 Sensor")
	ch := addChannel(dev, "AQDEV0001:1", 1)
	ch.AttachCalculatedDataPoint(&stubFloatMeasNotifier{
		class: matterport.MeasurementCO2,
		val:   650,
		obs:   true,
	})

	cfg := validConfig()
	cfg.IncludeMeasurements = true
	a, err := endpoint.New(newFakeStore(), cfg, nil)
	if err != nil {
		t.Fatalf("endpoint.New: %v", err)
	}
	top, err := a.Assemble(ctx, []endpoint.Snapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	bridged := top.Bridged()
	if len(bridged) != 1 {
		t.Fatalf("expected 1 bridged endpoint, got %d", len(bridged))
	}

	const (
		airQualityCluster uint32 = 0x005B
		co2Cluster        uint32 = 0x040D
	)
	clusters := make(map[uint32]bool)
	for _, p := range endpoint.ReportablePaths(bridged[0]) {
		clusters[p.Cluster] = true
	}
	if !clusters[airQualityCluster] {
		t.Errorf("no reportable path on AirQuality (0x005B); got clusters %v", clusters)
	}
	if !clusters[co2Cluster] {
		t.Errorf("no reportable path on CarbonDioxideConcentrationMeasurement (0x040D); got clusters %v", clusters)
	}
}

// TestReportablePaths_RootEndpoint_ReturnsNil verifies that the root
// endpoint (ID 0) always returns nil — it uses its own change-emission
// path and must not be wired via the measurement push path.
func TestReportablePaths_RootEndpoint_ReturnsNil(t *testing.T) {
	t.Parallel()
	ep := &endpoint.Endpoint{ID: 0}
	if got := endpoint.ReportablePaths(ep); got != nil {
		t.Errorf("ReportablePaths(root) = %v, want nil", got)
	}
}

// TestReportablePaths_Aggregator_ReturnsNil verifies that the
// aggregator endpoint (ID 1) also returns nil.
func TestReportablePaths_Aggregator_ReturnsNil(t *testing.T) {
	t.Parallel()
	ep := &endpoint.Endpoint{ID: 1}
	if got := endpoint.ReportablePaths(ep); got != nil {
		t.Errorf("ReportablePaths(aggregator) = %v, want nil", got)
	}
}

// TestReportablePaths_NilEndpoint_ReturnsNil is the nil-safety check.
func TestReportablePaths_NilEndpoint_ReturnsNil(t *testing.T) {
	t.Parallel()
	if got := endpoint.ReportablePaths(nil); got != nil {
		t.Errorf("ReportablePaths(nil) = %v, want nil", got)
	}
}

// TestReportablePaths_NoClusterServers_ReturnsNil verifies that a
// bridged endpoint with neither Source nor Measurement (= no cluster
// servers) yields nil rather than an empty slice.
func TestReportablePaths_NoClusterServers_ReturnsNil(t *testing.T) {
	t.Parallel()
	// ID ≥ 2 to avoid the root/aggregator early-return branches inside
	// ClusterServers.
	ep := &endpoint.Endpoint{ID: 5}
	if got := endpoint.ReportablePaths(ep); got != nil {
		t.Errorf("ReportablePaths(no source/measurement) = %v, want nil", got)
	}
}
