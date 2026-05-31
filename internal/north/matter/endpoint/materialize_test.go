// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package endpoint

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// --- fakes ---

type fakeSource struct {
	servers []interfaces.MatterClusterServer
}

func (f fakeSource) MatterDeviceType() uint16 { return 0x010A }
func (f fakeSource) MatterClusterServers() []interfaces.MatterClusterServer {
	return f.servers
}

type fakeBoolMeasurement struct{}

func (fakeBoolMeasurement) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return interfaces.MatterMeasurementOccupancy
}
func (fakeBoolMeasurement) MatterBoolValue() (value, observed bool) { return true, true }

type fakeServer struct{ id uint32 }

func (s fakeServer) MatterClusterID() uint32 { return s.id }
func (s fakeServer) MatterRead(_ uint32) (any, bool) {
	return nil, false
}

func (s fakeServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return errors.New("read-only")
}

func (s fakeServer) MatterInvoke(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, errors.New("no commands")
}
func (s fakeServer) MatterReportable() []uint32 { return nil }

// --- tests ---

// fakeMomentarySwitchSource implements both
// [interfaces.MatterMeasurementSource] (with MomentarySwitch) and
// the wire.GenericSwitchSource shape, mirroring how a HM Button
// surfaces in the live model. The struct lives in the test file so
// it can pretend to be a Button without pulling the model package.
type fakeMomentarySwitchSource struct {
	supportsLong bool
}

func (f fakeMomentarySwitchSource) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return interfaces.MatterMeasurementMomentarySwitch
}
func (f fakeMomentarySwitchSource) MatterSwitchPositions() uint8        { return 2 }
func (f fakeMomentarySwitchSource) MatterSwitchSupportsLongPress() bool { return f.supportsLong }

// TestClusterServersNilEndpoint verifies that a nil endpoint returns nil.
func TestClusterServersNilEndpoint(t *testing.T) {
	t.Parallel()
	if got := ClusterServers(nil); got != nil {
		t.Errorf("ClusterServers(nil) = %v, want nil", got)
	}
}

// TestClusterServersMomentarySwitch verifies that an endpoint whose
// measurement source advertises MomentarySwitch + the GenericSwitchSource
// shape gets a single GenericSwitch (cluster ID 0x003B) cluster
// server materialised against the endpoint ID.
func TestClusterServersMomentarySwitch(t *testing.T) {
	t.Parallel()
	ep := &Endpoint{
		ID:          17,
		Measurement: fakeMomentarySwitchSource{supportsLong: true},
	}
	servers := ClusterServers(ep)
	if len(servers) != 1 {
		t.Fatalf("ClusterServers() returned %d servers, want 1", len(servers))
	}
	if id := servers[0].MatterClusterID(); id != 0x003B {
		t.Errorf("MatterClusterID() = 0x%04X, want 0x003B (GenericSwitch)", id)
	}
}

// TestClusterServersRootReturnsNil verifies that the root endpoint (ID=0) always returns nil.
func TestClusterServersRootReturnsNil(t *testing.T) {
	t.Parallel()
	ep := &Endpoint{
		ID:     0,
		Source: fakeSource{servers: []interfaces.MatterClusterServer{fakeServer{id: 0x1234}}},
	}
	if got := ClusterServers(ep); got != nil {
		t.Errorf("ClusterServers(root) = %v, want nil", got)
	}
}

// TestClusterServersFromSource verifies that a Source-backed endpoint returns a fresh independent copy.
func TestClusterServersFromSource(t *testing.T) {
	t.Parallel()
	sentinel := fakeServer{id: 0x1234}
	ep := &Endpoint{
		ID:     2,
		Source: fakeSource{servers: []interfaces.MatterClusterServer{sentinel}},
	}

	first := ClusterServers(ep)
	if len(first) != 1 {
		t.Fatalf("want 1 server, got %d", len(first))
	}
	if got := first[0].MatterClusterID(); got != 0x1234 {
		t.Errorf("ClusterID = 0x%04X, want 0x1234", got)
	}

	// Mutate the returned slice to verify independence.
	_ = append(first, fakeServer{id: 0xDEAD}) //nolint:staticcheck // SA4006: deliberate mutate-and-discard to assert ClusterServers returns a fresh slice

	second := ClusterServers(ep)
	if len(second) != 1 {
		t.Errorf("second call: want 1 server, got %d — ClusterServers not returning a fresh slice", len(second))
	}
}

// TestClusterServersFromMeasurement verifies that a Measurement-backed endpoint returns OccupancySensing.
func TestClusterServersFromMeasurement(t *testing.T) {
	t.Parallel()
	ep := &Endpoint{
		ID:          2,
		Measurement: fakeBoolMeasurement{},
	}
	servers := ClusterServers(ep)
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(servers))
	}
	// OccupancySensing cluster ID per Matter Application Cluster Spec 1.5.1.
	const wantID uint32 = 0x0406
	if got := servers[0].MatterClusterID(); got != wantID {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X (OccupancySensing)", got, wantID)
	}
}

// TestClusterServersBothNilReturnsNil verifies that an endpoint with neither Source nor Measurement returns nil.
func TestClusterServersBothNilReturnsNil(t *testing.T) {
	t.Parallel()
	// Use ID >= 2 (bridged range) to avoid taking the root/aggregator branch.
	ep := &Endpoint{ID: 3}
	if got := ClusterServers(ep); got != nil {
		t.Errorf("ClusterServers(no source/measurement) = %v, want nil", got)
	}
}

// TestBridgedNodeRevisionFromSchema asserts that bridged endpoints
// emit `BridgedNode` (0x0013) with the revision sourced from the
// codegen'd schema table, NOT a hardcoded constant. Mirrors the V3.1
// audit P1-6 fix: previously `materialize.go` had `Revision: 3`
// hardcoded, which would silently desync the next time matter.js
// bumps the bridged-node revision via `make generate-matter-schema`.
// The lookup goes via `deviceTypeRevision(0x0013)` →
// `schema.DeviceTypeRevision(0x13)`.
func TestBridgedNodeRevisionFromSchema(t *testing.T) {
	t.Parallel()
	// matter.js HEAD `bridged-node.element.ts` defaults revision = 3
	// at Matter 1.5.1. The codegen'd schema lookup MUST match.
	got := deviceTypeRevision(uint16(matterDeviceTypeBridgedNode))
	if got == 1 {
		// `deviceTypeRevision` falls back to 1 when the schema entry
		// is missing — that would be a regression. Schema codegen must
		// produce an entry for 0x0013.
		t.Errorf("deviceTypeRevision(0x0013) = 1 (schema fallback); want explicit schema entry")
	}
	if got < 3 {
		t.Errorf("deviceTypeRevision(0x0013) = %d; matter.js HEAD baseline is 3 (Matter 1.5)", got)
	}
}
