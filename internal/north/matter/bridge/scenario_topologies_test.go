// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// scenarioTopology bundles a snapshotter + the notifier sources it
// embeds, keyed by their DataPointKey-string so scenario steps can
// fire a specific source. Used by F2 / DataVersion scenarios that
// need a real cluster server registered through the reassembler.
type scenarioTopology struct {
	snapshotter Snapshotter
	sources     map[string]*scenarioFakeNotifier
}

// scenarioFakeNotifier is a test-only Matter Temperature measurement
// source with a manually-fireable OnMatterValueChanged callback.
// Built on the same pattern as measurement_listener_test.go's
// notifiableTempSource but lives in package bridge so the harness
// can address it directly.
type scenarioFakeNotifier struct {
	mu    sync.Mutex
	cbs   []func()
	key   hmtypes.DataPointKey
	value float64
}

var (
	_ interfaces.MatterMeasurementSource      = (*scenarioFakeNotifier)(nil)
	_ interfaces.MatterFloatMeasurementSource = (*scenarioFakeNotifier)(nil)
	_ interfaces.MatterChangeNotifier         = (*scenarioFakeNotifier)(nil)
)

func (n *scenarioFakeNotifier) DataPointKey() hmtypes.DataPointKey { return n.key }
func (n *scenarioFakeNotifier) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return interfaces.MatterMeasurementTemperature
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

// fire dispatches every live subscriber callback. Used by
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

// topologyRecipes is the global registry of named topologies. New
// recipes drop in here without touching the harness wiring.
var topologyRecipes = map[string]func() *scenarioTopology{
	"single_temp_sensor":           buildSingleTempSensorTopology,
	"single_onoff_endpoint_source": buildSingleOnOffEndpointSourceTopology,
	"many_temp_sensors":            buildManyTempSensorsTopology,
	"fabric_scoped_reader":         buildFabricScopedReaderTopology,
	"two_centrals":                 buildTwoCentralsTopology,
}

// buildTwoCentralsTopology lays out two distinct centrals (CCU A
// and CCU B), each carrying one OnOff endpoint source. The bridge
// reassembles to four bridged endpoints (one OnOff per central
// plus aggregator scaffolding). Used by Phase-U scenarios to
// verify cross-CCU isolation: a notifier fire on central A must
// emit a report on A's endpoint only; B's endpoint stays silent.
func buildTwoCentralsTopology() *scenarioTopology {
	sources := make(map[string]*scenarioFakeNotifier, 2)
	mkCentral := func(centralName, devAddr string, dversInit uint32) (*device.Device, *scenarioFakeOnOffEndpointSource) {
		chAddr := devAddr + ":1"
		src := &scenarioFakeOnOffEndpointSource{
			key: hmtypes.DataPointKey{ChannelAddress: chAddr, Parameter: "STATE"},
		}
		src.dvers.Store(dversInit)
		dev := device.New(device.Config{Address: devAddr, Name: devAddr})
		ch := dev.AddChannel(chAddr, 1, "SWITCH", hmenum.ParamsetKeyValues)
		ch.AttachCalculatedDataPoint(src)
		sources[centralName+"/"+chAddr+"/STATE"] = newOnOffFireAdapter(src)
		return dev, src
	}
	devA, _ := mkCentral("ccuA", "ONOFFA01", 1)
	devB, _ := mkCentral("ccuB", "ONOFFB01", 1)
	snap := Snapshotter(func(_ context.Context) []endpoint.Snapshot {
		return []endpoint.Snapshot{
			{CentralName: "ccuA", Devices: []*device.Device{devA}},
			{CentralName: "ccuB", Devices: []*device.Device{devB}},
		}
	})
	return &scenarioTopology{
		snapshotter: snap,
		sources:     sources,
	}
}

// scenarioFakeFabricReader is a test-only cluster server that
// implements MatterClusterServer + FabricScopedReader: its
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
	_ interfaces.MatterEndpointSource = (*scenarioFakeFabricReader)(nil)
	_ interfaces.MatterClusterServer  = (*scenarioFakeFabricReader)(nil)
	_ interfaces.FabricScopedReader   = (*scenarioFakeFabricReader)(nil)
)

func (n *scenarioFakeFabricReader) DataPointKey() hmtypes.DataPointKey { return n.key }
func (*scenarioFakeFabricReader) MatterDeviceType() uint16             { return 0x010A }
func (n *scenarioFakeFabricReader) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{n}
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

func (*scenarioFakeFabricReader) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return nil
}

func (*scenarioFakeFabricReader) MatterInvoke(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, nil
}
func (*scenarioFakeFabricReader) MatterReportable() []uint32 { return []uint32{0x0000} }

// buildFabricScopedReaderTopology lays out one bridged endpoint
// whose OnOff cluster server is FabricScopedReader-capable. Used
// by Phase-S scenarios to verify that FabricFiltered=true with a
// non-zero FabricIndex stamps the right fabric into the dispatch
// context and the cluster server projects accordingly.
func buildFabricScopedReaderTopology() *scenarioTopology {
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
	snap := Snapshotter(func(_ context.Context) []endpoint.Snapshot {
		return []endpoint.Snapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}}
	})
	return &scenarioTopology{
		snapshotter: snap,
		// No notifier surface — fabric scenarios drive reads, not echoes.
		sources: map[string]*scenarioFakeNotifier{},
	}
}

// buildManyTempSensorsTopology lays out N=30 bridged Temperature
// sensors on one device. Wildcard subscribes against this topology
// produce ~360 AttributeReports (30 endpoints × ~12 reportable
// attributes — TemperatureMeasurement plus the standard bridged-
// endpoint scaffolding), overflowing the 1100-byte chunk budget
// many times over. Locks the F5/F6 per-chunk handshake at scenarios
// that must drive >1 chunk deterministically.
func buildManyTempSensorsTopology() *scenarioTopology {
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
	snap := Snapshotter(func(_ context.Context) []endpoint.Snapshot {
		return []endpoint.Snapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}}
	})
	return &scenarioTopology{
		snapshotter: snap,
		sources:     sources,
	}
}

// scenarioFakeOnOffEndpointSource is a test-only Custom-DP-style
// endpoint source: it advertises an OnOff cluster and is ALSO a
// MatterChangeNotifier + MatterClusterServer + MatterClusterDataVersion.
// Mirrors the shape that production *custom.Switch presents, so the
// F2 filterPathsByNotifierCluster path (notifier is the cluster
// server → narrow to its own MatterClusterID) gets exercised at
// scenario level. Bumps its DataVersion on every fire so consecutive
// fires produce strictly increasing wire DataVersion values.
type scenarioFakeOnOffEndpointSource struct {
	key   hmtypes.DataPointKey
	mu    sync.Mutex
	cbs   []func()
	on    bool
	dvers atomic.Uint32
}

var (
	_ interfaces.MatterEndpointSource     = (*scenarioFakeOnOffEndpointSource)(nil)
	_ interfaces.MatterClusterServer      = (*scenarioFakeOnOffEndpointSource)(nil)
	_ interfaces.MatterChangeNotifier     = (*scenarioFakeOnOffEndpointSource)(nil)
	_ interfaces.MatterClusterDataVersion = (*scenarioFakeOnOffEndpointSource)(nil)
)

func (n *scenarioFakeOnOffEndpointSource) DataPointKey() hmtypes.DataPointKey { return n.key }

// MatterDeviceType — OnOffPlugInUnit per matter.js
// packages/node/src/devices/on-off-plug-in-unit.ts.
func (*scenarioFakeOnOffEndpointSource) MatterDeviceType() uint16 { return 0x010A }

func (n *scenarioFakeOnOffEndpointSource) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{n, &scenarioFakeLevelControlServer{owner: n}}
}

// scenarioFakeLevelControlServer is a sibling cluster server on the
// OnOff endpoint source: implements MatterClusterServer for
// LevelControl (0x0008). Records the most-recent fields it received
// via MatterInvoke so the harness can verify that command-fields
// reached the cluster server intact. Used by Phase-X dim scenarios
// to lock the commandFieldsReader → MatterInvoke contract for
// LevelControl.MoveToLevel.
type scenarioFakeLevelControlServer struct {
	owner *scenarioFakeOnOffEndpointSource
}

var _ interfaces.MatterClusterServer = (*scenarioFakeLevelControlServer)(nil)

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

func (*scenarioFakeLevelControlServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return nil
}

func (*scenarioFakeLevelControlServer) MatterInvoke(_ context.Context, cmdID uint32, fields any, _ hmenum.CommandPriority) (any, error) {
	if cmdID != 0x00 && cmdID != 0x04 {
		return nil, fmt.Errorf("unknown LevelControl cmd 0x%02X", cmdID)
	}
	if fields == nil {
		// Distinct sentinel so the harness can attribute the bug:
		// the dispatch path lost the command-fields struct before
		// reaching us.
		return nil, errors.New("LevelControl MoveToLevel: command fields are nil (commandFieldsReader dropped the TLV payload)")
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

// MatterWrite applies an Apple-driven attribute write. Production
// *custom.Switch translates the write into a CCU SetValue; the fake
// just mutates the boolean and bumps DataVersion so subsequent
// reads / reports observe the new state.
func (n *scenarioFakeOnOffEndpointSource) MatterWrite(_ context.Context, attrID uint32, value any, _ hmenum.CommandPriority) error {
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

func (*scenarioFakeOnOffEndpointSource) MatterInvoke(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) (any, error) {
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
// Used by fire_notifier_source step kind for F2 + DataVersion
// scenarios.
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
// IS both the notifier and the cluster server. Enables F2
// narrowing + DataVersion-monotonicity scenarios.
func buildSingleOnOffEndpointSourceTopology() *scenarioTopology {
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

	snap := Snapshotter(func(_ context.Context) []endpoint.Snapshot {
		return []endpoint.Snapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}}
	})
	return &scenarioTopology{
		snapshotter: snap,
		sources: map[string]*scenarioFakeNotifier{
			// Bridge into the scenarioFakeNotifier-keyed dispatch by
			// wrapping the OnOff source in an adapter. The harness's
			// fire_notifier_source looks up *scenarioFakeNotifier by
			// key; we register a synthetic adapter whose fire() calls
			// the OnOff source's fire.
			chAddr + "/STATE": newOnOffFireAdapter(src),
		},
	}
}

// newOnOffFireAdapter exposes a scenarioFakeOnOffEndpointSource via
// the same fire() entry-point the existing scenarioFakeNotifier uses.
// Avoids broadening the topology's sources map to a richer interface
// just for one recipe.
func newOnOffFireAdapter(src *scenarioFakeOnOffEndpointSource) *scenarioFakeNotifier {
	adapter := &scenarioFakeNotifier{
		key:   src.key,
		value: 0,
	}
	adapter.cbs = append(adapter.cbs, src.fire)
	return adapter
}

// resolveTopology returns the scenarioTopology for name, or nil for
// the default (wbEmptySnapshotter).
func resolveTopology(name string) *scenarioTopology {
	if name == "" {
		return nil
	}
	build, ok := topologyRecipes[name]
	if !ok {
		return nil
	}
	return build()
}

// buildSingleTempSensorTopology constructs a single-device topology:
// one channel (WEATHER paramset) carrying one Temperature
// measurement source. Reassembly produces one bridged endpoint with
// the standard Identify / Descriptor / BridgedDeviceBasicInformation
// scaffolding plus the TemperatureMeasurement cluster — which is
// exactly the shape F2 scenarios assert against.
func buildSingleTempSensorTopology() *scenarioTopology {
	const (
		devAddr = "F2DEV01"
		chAddr  = "F2DEV01:1"
	)
	src := &scenarioFakeNotifier{
		key:   hmtypes.DataPointKey{ChannelAddress: chAddr, Parameter: "ACTUAL_TEMPERATURE"},
		value: 21.0,
	}
	dev := device.New(device.Config{Address: devAddr, Name: "F2-temp"})
	ch := dev.AddChannel(chAddr, 1, "WEATHER", hmenum.ParamsetKeyValues)
	ch.AttachCalculatedDataPoint(src)

	snap := Snapshotter(func(_ context.Context) []endpoint.Snapshot {
		return []endpoint.Snapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}}
	})
	return &scenarioTopology{
		snapshotter: snap,
		sources:     map[string]*scenarioFakeNotifier{chAddr + "/ACTUAL_TEMPERATURE": src},
	}
}
