// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package scenario

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/SukramJ/go-fabric/bridge"
	"github.com/SukramJ/go-fabric/contract"
	matterendpoint "github.com/SukramJ/go-fabric/endpoint"
	"github.com/SukramJ/go-fabric/endpoint/endpointtest"
	"github.com/SukramJ/go-fabric/im"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// scenarioTopology bundles a snapshotter + the notifier sources it
// embeds, keyed by their DataPointKey-string so scenario steps can
// fire a specific source. Used by scenarios that need a real cluster
// server registered through the assembler.
type scenarioTopology struct {
	snapshotter bridge.Snapshotter
	// store is the endpoint-id store the assembler writes through. The
	// harness hands the same instance to the bridge so both halves
	// share one id space, which is how the daemon wires them. Each
	// topology builds its own, so parallel scenarios never share an
	// endpoint-id counter — two scenarios assigning the same device to
	// different endpoint ids would otherwise make their wire
	// expectations depend on execution order.
	store   *endpointtest.FakeStore
	sources map[string]*scenarioFakeNotifier
}

// newScenarioSnapshotter turns a fixed device fleet into the callback
// the bridge holds. The assembler is the daemon's own model walk, so
// the topology a scenario runs against is assembled by the same code
// the daemon uses rather than by a harness-local approximation — the
// endpoint ids the scenario JSON names (2, 3, …) are the ids that walk
// allocates.
func newScenarioSnapshotter(snapshots []matteradapter.DeviceSnapshot) (bridge.Snapshotter, *endpointtest.FakeStore, error) {
	epStore := endpointtest.NewFakeStore()
	asm, err := matteradapter.New(epStore, matteradapter.Config{
		VendorID:            0x1234,
		ProductID:           0x5678,
		NodeLabel:           "scenario-harness",
		IncludeMeasurements: true,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		return nil, nil, fmt.Errorf("scenario assembler: %w", err)
	}
	return func(ctx context.Context) (*matterendpoint.Topology, error) {
		return asm.AssembleDevices(ctx, snapshots)
	}, epStore, nil
}

// scenarioFakeNotifier is a test-only Matter Temperature measurement
// source with a manually-fireable OnMatterValueChanged callback.
type scenarioFakeNotifier struct {
	mu    sync.Mutex
	cbs   []func()
	key   hmtypes.DataPointKey
	value float64
}

var (
	_ contract.MeasurementSource      = (*scenarioFakeNotifier)(nil)
	_ contract.FloatMeasurementSource = (*scenarioFakeNotifier)(nil)
	_ contract.ChangeNotifier         = (*scenarioFakeNotifier)(nil)
)

func (n *scenarioFakeNotifier) DataPointKey() hmtypes.DataPointKey { return n.key }
func (n *scenarioFakeNotifier) MatterMeasurementClass() contract.MeasurementClass {
	return contract.MeasurementTemperature
}

func (n *scenarioFakeNotifier) MatterFloatValue() (float64, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.value, true
}

func (n *scenarioFakeNotifier) OnMatterValueChanged(cb func()) func() {
	if cb == nil {
		return func() {}
	}
	n.mu.Lock()
	n.cbs = append(n.cbs, cb)
	idx := len(n.cbs) - 1
	n.mu.Unlock()
	return func() {
		n.mu.Lock()
		if idx < len(n.cbs) {
			n.cbs[idx] = nil
		}
		n.mu.Unlock()
	}
}

// fire dispatches every live subscriber callback. Used by the
// fire_notifier_source step kind to drive the production
// notifier-callback wiring.
func (n *scenarioFakeNotifier) fire() {
	n.mu.Lock()
	cbs := append([]func(){}, n.cbs...)
	n.mu.Unlock()
	for _, cb := range cbs {
		if cb != nil {
			cb()
		}
	}
}

// topologyRecipes is the registry of named topologies. New recipes
// drop in here without touching the harness wiring.
var topologyRecipes = map[string]func() (*scenarioTopology, error){
	"single_temp_sensor":           buildSingleTempSensorTopology,
	"single_onoff_endpoint_source": buildSingleOnOffEndpointSourceTopology,
	"many_temp_sensors":            buildManyTempSensorsTopology,
	"fabric_scoped_reader":         buildFabricScopedReaderTopology,
	"two_centrals":                 buildTwoCentralsTopology,
}

// buildTwoCentralsTopology lays out two distinct centrals (CCU A and
// CCU B), each carrying one OnOff endpoint source. Used to verify
// cross-CCU isolation: a notifier fire on central A must emit a
// report on A's endpoint only; B's endpoint stays silent.
func buildTwoCentralsTopology() (*scenarioTopology, error) {
	sources := make(map[string]*scenarioFakeNotifier, 2)
	mkCentral := func(centralName, devAddr string, dversInit uint32) *device.Device {
		chAddr := devAddr + ":1"
		src := &scenarioFakeOnOffEndpointSource{
			key: hmtypes.DataPointKey{ChannelAddress: chAddr, Parameter: "STATE"},
		}
		src.dvers.Store(dversInit)
		dev := device.New(device.Config{Address: devAddr, Name: devAddr})
		ch := dev.AddChannel(chAddr, 1, "SWITCH", hmenum.ParamsetKeyValues)
		ch.AttachCalculatedDataPoint(src)
		sources[centralName+"/"+chAddr+"/STATE"] = newOnOffFireAdapter(src)
		return dev
	}
	devA := mkCentral("ccuA", "ONOFFA01", 1)
	devB := mkCentral("ccuB", "ONOFFB01", 1)
	snap, epStore, err := newScenarioSnapshotter([]matteradapter.DeviceSnapshot{
		{CentralName: "ccuA", Devices: []*device.Device{devA}, ModelComplete: true},
		{CentralName: "ccuB", Devices: []*device.Device{devB}, ModelComplete: true},
	})
	if err != nil {
		return nil, err
	}
	return &scenarioTopology{snapshotter: snap, store: epStore, sources: sources}, nil
}

// scenarioFakeFabricReader is a test-only cluster server that
// implements ClusterServer + FabricScopedReader: its
// MatterReadFiltered returns the FabricIndex stamped into the
// dispatch context as a uint8 attribute value. MatterRead (the
// FabricFiltered=false fallback) returns a constant sentinel
// (0xFF). Scenarios then verify the bridge's outbound wire carries
// the FabricIndex specific to the requesting session, locking
// per-fabric projection at scenario level.
type scenarioFakeFabricReader struct {
	key hmtypes.DataPointKey
}

var (
	_ contract.EndpointSource     = (*scenarioFakeFabricReader)(nil)
	_ contract.ClusterServer      = (*scenarioFakeFabricReader)(nil)
	_ contract.FabricScopedReader = (*scenarioFakeFabricReader)(nil)
)

func (n *scenarioFakeFabricReader) DataPointKey() hmtypes.DataPointKey { return n.key }
func (*scenarioFakeFabricReader) MatterDeviceType() uint16             { return 0x010A }
func (n *scenarioFakeFabricReader) MatterClusterServers() []contract.ClusterServer {
	return []contract.ClusterServer{n}
}
func (*scenarioFakeFabricReader) MatterClusterID() uint32 { return 0x0006 }
func (*scenarioFakeFabricReader) MatterRead(attrID uint32) (any, bool) {
	if attrID == 0x0000 {
		return uint8(0xFF), true
	}
	return nil, false
}

func (n *scenarioFakeFabricReader) MatterReadFiltered(ctx context.Context, attrID uint32) (any, bool) {
	if attrID != 0x0000 {
		return nil, false
	}
	_, fabric := im.FabricFilterFromContext(ctx)
	return fabric, true
}

func (*scenarioFakeFabricReader) MatterWrite(_ context.Context, _ uint32, _ any) error {
	return nil
}

func (*scenarioFakeFabricReader) MatterInvoke(_ context.Context, _ uint32, _ any) (any, error) {
	return nil, nil
}
func (*scenarioFakeFabricReader) MatterReportable() []uint32 { return []uint32{0x0000} }

// buildFabricScopedReaderTopology lays out one bridged endpoint
// whose OnOff cluster server is FabricScopedReader-capable, so a
// read with FabricFiltered=true and a non-zero FabricIndex can be
// observed to reach the cluster server with the right fabric.
func buildFabricScopedReaderTopology() (*scenarioTopology, error) {
	const (
		devAddr = "FABRIC01"
		chAddr  = "FABRIC01:1"
	)
	src := &scenarioFakeFabricReader{
		key: hmtypes.DataPointKey{ChannelAddress: chAddr, Parameter: "STATE"},
	}
	dev := device.New(device.Config{Address: devAddr, Name: "Fabric-Scoped"})
	ch := dev.AddChannel(chAddr, 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.AttachCalculatedDataPoint(src)
	snap, epStore, err := newScenarioSnapshotter([]matteradapter.DeviceSnapshot{
		{CentralName: "ccu1", Devices: []*device.Device{dev}, ModelComplete: true},
	})
	if err != nil {
		return nil, err
	}
	return &scenarioTopology{
		snapshotter: snap,
		store:       epStore,
		// No notifier surface — fabric scenarios drive reads, not echoes.
		sources: map[string]*scenarioFakeNotifier{},
	}, nil
}

// buildManyTempSensorsTopology lays out N=30 bridged Temperature
// sensors on one device. Wildcard subscribes against this topology
// produce hundreds of AttributeReports, overflowing the chunk budget
// many times over — which is what lets a scenario drive more than one
// chunk deterministically and observe the per-chunk handshake.
func buildManyTempSensorsTopology() (*scenarioTopology, error) {
	const n = 30
	dev := device.New(device.Config{Address: "MANYTMP", Name: "Many-Temp"})
	sources := make(map[string]*scenarioFakeNotifier, n)
	for i := range n {
		chAddr := fmt.Sprintf("MANYTMP:%d", i+1)
		src := &scenarioFakeNotifier{
			key:   hmtypes.DataPointKey{ChannelAddress: chAddr, Parameter: "ACTUAL_TEMPERATURE"},
			value: float64(20 + i),
		}
		ch := dev.AddChannel(chAddr, i+1, "WEATHER", hmenum.ParamsetKeyValues)
		ch.AttachCalculatedDataPoint(src)
		sources[chAddr+"/ACTUAL_TEMPERATURE"] = src
	}
	snap, epStore, err := newScenarioSnapshotter([]matteradapter.DeviceSnapshot{
		{CentralName: "ccu1", Devices: []*device.Device{dev}, ModelComplete: true},
	})
	if err != nil {
		return nil, err
	}
	return &scenarioTopology{snapshotter: snap, store: epStore, sources: sources}, nil
}

// scenarioFakeOnOffEndpointSource is a test-only Custom-DP-style
// endpoint source: it advertises an OnOff cluster and is ALSO a
// ChangeNotifier + ClusterServer + ClusterDataVersion. Mirrors the
// shape a production custom switch presents, so the notifier-cluster
// narrowing path (notifier is the cluster server → narrow to its own
// MatterClusterID) gets exercised at scenario level. Bumps its
// DataVersion on every fire so consecutive fires produce strictly
// increasing wire DataVersion values.
type scenarioFakeOnOffEndpointSource struct {
	key   hmtypes.DataPointKey
	mu    sync.Mutex
	cbs   []func()
	on    bool
	dvers atomic.Uint32
}

var (
	_ contract.EndpointSource     = (*scenarioFakeOnOffEndpointSource)(nil)
	_ contract.ClusterServer      = (*scenarioFakeOnOffEndpointSource)(nil)
	_ contract.ChangeNotifier     = (*scenarioFakeOnOffEndpointSource)(nil)
	_ contract.ClusterDataVersion = (*scenarioFakeOnOffEndpointSource)(nil)
)

func (n *scenarioFakeOnOffEndpointSource) DataPointKey() hmtypes.DataPointKey { return n.key }

// MatterDeviceType — OnOffPlugInUnit per matter.js
// packages/node/src/devices/on-off-plug-in-unit.ts.
func (*scenarioFakeOnOffEndpointSource) MatterDeviceType() uint16 { return 0x010A }

func (n *scenarioFakeOnOffEndpointSource) MatterClusterServers() []contract.ClusterServer {
	return []contract.ClusterServer{n, &scenarioFakeLevelControlServer{owner: n}}
}

// scenarioFakeLevelControlServer is a sibling cluster server on the
// OnOff endpoint source: implements ClusterServer for LevelControl
// (0x0008). Rejects a nil command-fields payload with a distinct
// error so a dispatch path that loses the TLV struct is attributable
// rather than merely failing.
type scenarioFakeLevelControlServer struct {
	owner *scenarioFakeOnOffEndpointSource
}

var _ contract.ClusterServer = (*scenarioFakeLevelControlServer)(nil)

func (*scenarioFakeLevelControlServer) MatterClusterID() uint32 { return 0x0008 }
func (l *scenarioFakeLevelControlServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case 0x0000:
		l.owner.mu.Lock()
		on := l.owner.on
		l.owner.mu.Unlock()
		if on {
			return uint8(254), true
		}
		return uint8(0), true
	case 0xFFFC:
		return uint32(1), true // OO feature
	case 0xFFFD:
		return uint16(7), true
	}
	return nil, false
}

func (*scenarioFakeLevelControlServer) MatterWrite(_ context.Context, _ uint32, _ any) error {
	return nil
}

func (*scenarioFakeLevelControlServer) MatterInvoke(_ context.Context, cmdID uint32, fields any) (any, error) {
	if cmdID != 0x00 && cmdID != 0x04 {
		return nil, fmt.Errorf("unknown LevelControl cmd 0x%02X", cmdID)
	}
	if fields == nil {
		// Distinct sentinel so the harness can attribute the bug:
		// the dispatch path lost the command-fields struct before
		// reaching us.
		return nil, errors.New("LevelControl MoveToLevel: command fields are nil (the invoke path dropped the TLV payload)")
	}
	return nil, nil
}
func (*scenarioFakeLevelControlServer) MatterReportable() []uint32 { return []uint32{0x0000} }

func (*scenarioFakeOnOffEndpointSource) MatterClusterID() uint32 { return 0x0006 }

func (n *scenarioFakeOnOffEndpointSource) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case 0x0000: // OnOff
		n.mu.Lock()
		defer n.mu.Unlock()
		return n.on, true
	case 0xFFFC: // FeatureMap
		return uint32(0), true
	case 0xFFFD: // ClusterRevision
		return uint16(6), true
	}
	return nil, false
}

// MatterWrite applies a controller-driven attribute write. A
// production custom switch translates the write into a CCU SetValue;
// the fake just mutates the boolean and bumps DataVersion so
// subsequent reads / reports observe the new state.
func (n *scenarioFakeOnOffEndpointSource) MatterWrite(_ context.Context, attrID uint32, value any) error {
	if attrID != 0x0000 {
		return nil
	}
	on, ok := value.(bool)
	if !ok {
		return nil
	}
	n.mu.Lock()
	n.on = on
	n.mu.Unlock()
	n.dvers.Add(1)
	return nil
}

func (*scenarioFakeOnOffEndpointSource) MatterInvoke(_ context.Context, _ uint32, _ any) (any, error) {
	return nil, nil
}

func (*scenarioFakeOnOffEndpointSource) MatterReportable() []uint32 { return []uint32{0x0000} }

func (n *scenarioFakeOnOffEndpointSource) MatterDataVersion() uint32 { return n.dvers.Load() }

func (n *scenarioFakeOnOffEndpointSource) OnMatterValueChanged(cb func()) func() {
	if cb == nil {
		return func() {}
	}
	n.mu.Lock()
	n.cbs = append(n.cbs, cb)
	idx := len(n.cbs) - 1
	n.mu.Unlock()
	return func() {
		n.mu.Lock()
		if idx < len(n.cbs) {
			n.cbs[idx] = nil
		}
		n.mu.Unlock()
	}
}

// fire flips the boolean state, bumps the DataVersion, and notifies.
func (n *scenarioFakeOnOffEndpointSource) fire() {
	n.mu.Lock()
	n.on = !n.on
	cbs := append([]func(){}, n.cbs...)
	n.mu.Unlock()
	n.dvers.Add(1)
	for _, cb := range cbs {
		if cb != nil {
			cb()
		}
	}
}

// buildSingleOnOffEndpointSourceTopology builds a topology that
// advertises one bridged endpoint with an OnOff-cluster source that
// IS both the notifier and the cluster server.
func buildSingleOnOffEndpointSourceTopology() (*scenarioTopology, error) {
	const (
		devAddr = "ONOFF01"
		chAddr  = "ONOFF01:1"
	)
	src := &scenarioFakeOnOffEndpointSource{
		key: hmtypes.DataPointKey{ChannelAddress: chAddr, Parameter: "STATE"},
	}
	src.dvers.Store(1)
	dev := device.New(device.Config{Address: devAddr, Name: "Fake-OnOff"})
	ch := dev.AddChannel(chAddr, 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.AttachCalculatedDataPoint(src)

	snap, epStore, err := newScenarioSnapshotter([]matteradapter.DeviceSnapshot{
		{CentralName: "ccu1", Devices: []*device.Device{dev}, ModelComplete: true},
	})
	if err != nil {
		return nil, err
	}
	return &scenarioTopology{
		snapshotter: snap,
		store:       epStore,
		sources: map[string]*scenarioFakeNotifier{
			// The harness's fire_notifier_source looks up a
			// *scenarioFakeNotifier by key; register a synthetic
			// adapter whose fire() calls the OnOff source's fire
			// rather than widening the sources map to an interface
			// for one recipe.
			chAddr + "/STATE": newOnOffFireAdapter(src),
		},
	}, nil
}

// newOnOffFireAdapter exposes a scenarioFakeOnOffEndpointSource via
// the same fire() entry-point the existing scenarioFakeNotifier uses.
func newOnOffFireAdapter(src *scenarioFakeOnOffEndpointSource) *scenarioFakeNotifier {
	adapter := &scenarioFakeNotifier{
		key:   src.key,
		value: 0,
	}
	adapter.cbs = append(adapter.cbs, src.fire)
	return adapter
}

// resolveTopology returns the scenarioTopology for name, or nil for
// the default empty topology (root + aggregator, no bridged endpoint).
func resolveTopology(name string) (*scenarioTopology, error) {
	if name == "" {
		return nil, nil //nolint:nilnil // the empty topology is deliberately represented by a nil recipe
	}
	build, ok := topologyRecipes[name]
	if !ok {
		return nil, fmt.Errorf("unknown topology %q (have %v)", name, sortedStringKeys(topologyRecipes))
	}
	return build()
}

// buildSingleTempSensorTopology constructs a single-device topology:
// one channel (WEATHER paramset) carrying one Temperature
// measurement source. Assembly produces one bridged endpoint with the
// standard Identify / Descriptor / BridgedDeviceBasicInformation
// scaffolding plus the TemperatureMeasurement cluster.
func buildSingleTempSensorTopology() (*scenarioTopology, error) {
	const (
		devAddr = "TEMPDEV01"
		chAddr  = "TEMPDEV01:1"
	)
	src := &scenarioFakeNotifier{
		key:   hmtypes.DataPointKey{ChannelAddress: chAddr, Parameter: "ACTUAL_TEMPERATURE"},
		value: 21.0,
	}
	dev := device.New(device.Config{Address: devAddr, Name: "Scenario-temp"})
	ch := dev.AddChannel(chAddr, 1, "WEATHER", hmenum.ParamsetKeyValues)
	ch.AttachCalculatedDataPoint(src)

	snap, epStore, err := newScenarioSnapshotter([]matteradapter.DeviceSnapshot{
		{CentralName: "ccu1", Devices: []*device.Device{dev}, ModelComplete: true},
	})
	if err != nil {
		return nil, err
	}
	return &scenarioTopology{
		snapshotter: snap,
		store:       epStore,
		sources:     map[string]*scenarioFakeNotifier{chAddr + "/ACTUAL_TEMPERATURE": src},
	}, nil
}
