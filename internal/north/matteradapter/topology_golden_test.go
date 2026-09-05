// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/builtins"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/matter/bootid"
	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/mattercontract"
)

// updateTopologyGolden rewrites the fixture from the current output:
//
//	go test -update-topology-golden ./internal/north/matteradapter/...
//
// Declared with its own flag name because the repository already owns a
// package-level `-update` in tests/golden and a `-update-naming`
// alongside it; a shared name would make one refresh silently rewrite
// the other's fixture.
var updateTopologyGolden = flag.Bool("update-topology-golden", false, "rewrite testdata/topology.golden.json")

// goldenCentralName scopes every fixture device. It is part of every
// [store.EndpointKey] and therefore feeds the UniqueID hash, so it is
// pinned here rather than spelled out per call site.
const goldenCentralName = "ccu-golden"

// ─── recorded shape ──────────────────────────────────────────────────

// goldenDeviceType is one entry of an endpoint's Descriptor
// DeviceTypeList (Matter §9.5.5.1). Both the id and the revision are
// read by a commissioner during pairing and cached afterwards.
type goldenDeviceType struct {
	ID       string `json:"id"`
	Revision uint16 `json:"revision"`
}

// goldenSourceKey is the persisted endpoint identity — the
// matter_endpoints primary key. It is not on the wire itself, but it is
// what decides which endpoint ID a source keeps across a restart, and it
// is the sole input to the UniqueID hash, so a change to it is a change
// a paired controller sees.
type goldenSourceKey struct {
	CentralName   string `json:"central_name"`
	DeviceAddress string `json:"device_address"`
	ChannelNo     int    `json:"channel_no"`
	DPKind        string `json:"dp_kind"`
	DPKey         string `json:"dp_key"`
}

// goldenEndpoint is the per-endpoint record. Every field here is
// something an already-commissioned controller has cached and would
// notice changing.
type goldenEndpoint struct {
	EndpointID          uint16             `json:"endpoint_id"`
	DeviceType          string             `json:"device_type"`
	DeviceTypeList      []goldenDeviceType `json:"device_type_list"`
	FriendlyName        string             `json:"friendly_name"`
	UniqueID            string             `json:"unique_id"`
	Reachable           bool               `json:"reachable"`
	ParentEndpointID    uint16             `json:"parent_endpoint_id"`
	HasParentEndpointID bool               `json:"has_parent_endpoint_id"`
	SourceKey           goldenSourceKey    `json:"source_key"`
	ServerClusters      []string           `json:"server_clusters"`
}

// goldenTopology is the whole recorded assembly.
type goldenTopology struct {
	NodeLabel string           `json:"node_label"`
	VendorID  string           `json:"vendor_id"`
	ProductID string           `json:"product_id"`
	Endpoints []goldenEndpoint `json:"endpoints"`
}

// ─── the fleet ───────────────────────────────────────────────────────

// goldenFleet returns the fixed device fleet the golden topology is
// assembled from. Every device is hand-built from the same primitives
// the CCU hydration uses (device.New → AddChannel → Channel.Put →
// custom.CreateCustomDataPoints), so the assembler walks production
// shapes rather than stubs.
//
// The fleet is chosen to cover each projection path the assembler owns:
//
//   - metering plug: a multi-channel device whose custom switch DP and
//     whose five electrical parameters produce two endpoints, one of
//     them the consolidated ElectricalSensor group;
//   - wall remote: two press channels, each consolidating several
//     PRESS_* parameters into one GenericSwitch endpoint, plus a LOWBAT
//     that must ride on another endpoint's PowerSource cluster instead
//     of spawning one of its own;
//   - climate sensor: two measurement sub-endpoints on one channel,
//     distinguished only by their parameter suffix, on an
//     operator-named channel;
//   - contact sensor: a device with no operator name at all, so both
//     the device-address fallback and the channel-number fallback in
//     [friendlyName] are exercised.
func goldenFleet(t *testing.T) []*device.Device {
	t.Helper()
	return []*device.Device{
		goldenMeteringPlug(t),
		goldenWallRemote(t),
		goldenClimateSensor(t),
		goldenContactSensor(t),
	}
}

// goldenFloatSensor attaches a read-only float VALUES data point, the
// shape CCU hydration produces for an analog parameter.
func goldenFloatSensor(ch *device.Channel, p hmenum.Parameter) {
	ch.Put(generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage("100000.0"),
		},
	}))
}

// goldenBinarySensor attaches a read-only bool VALUES data point.
func goldenBinarySensor(ch *device.Channel, p hmenum.Parameter) {
	ch.Put(generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}))
}

// goldenPressButton attaches an event-only press data point, the shape
// the resolver produces for a KEY channel's PRESS_* parameters.
func goldenPressButton(ch *device.Channel, p hmenum.Parameter) {
	ch.Put(generic.NewButton(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsEvent,
		},
	}))
}

// goldenMeteringPlug is a switching plug with metering: the actor on one
// channel, the five electrical parameters on another.
func goldenMeteringPlug(t *testing.T) *device.Device {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "0001PSM01",
		Name:         "Desk Lamp Plug",
		Model:        "HmIP-PSM",
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	for i := range 7 {
		dev.AddChannel("0001PSM01:"+strconv.Itoa(i), i, "X", hmenum.ParamsetKeyValues)
	}
	ch3 := dev.Channel("0001PSM01:3")
	ch3.Put(generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch3.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}))
	ch6 := dev.Channel("0001PSM01:6")
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterPower,
		hmenum.ParameterVoltage,
		hmenum.ParameterCurrent,
		hmenum.ParameterFrequency,
		hmenum.ParameterEnergyCounter,
	} {
		goldenFloatSensor(ch6, p)
	}
	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("CreateCustomDataPoints(metering plug): %v", err)
	}
	return dev
}

// goldenWallRemote is a battery-powered two-button wall remote: two KEY
// channels carrying several press parameters each, and the battery
// warning on the maintenance channel.
func goldenWallRemote(t *testing.T) *device.Device {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "0002WRC02",
		Name:         "Hallway Remote",
		Model:        "HmIP-WRC2",
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	for i := range 3 {
		dev.AddChannel("0002WRC02:"+strconv.Itoa(i), i, "X", hmenum.ParamsetKeyValues)
	}
	goldenBinarySensor(dev.Channel("0002WRC02:0"), hmenum.ParameterLowBat)
	for _, no := range []int{1, 2} {
		ch := dev.Channel("0002WRC02:" + strconv.Itoa(no))
		for _, p := range []hmenum.Parameter{
			hmenum.ParameterPressShort,
			hmenum.ParameterPressLong,
			hmenum.ParameterPressLongRelease,
		} {
			goldenPressButton(ch, p)
		}
	}
	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("CreateCustomDataPoints(wall remote): %v", err)
	}
	return dev
}

// goldenClimateSensor carries two analog readings on one
// operator-named channel, so the two endpoints they produce differ only
// by the parameter suffix [Assembler.parameterSuffix] appends.
func goldenClimateSensor(t *testing.T) *device.Device {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "0003STH03",
		Name:         "Nursery",
		Model:        "HmIP-STHO",
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	for i := range 2 {
		dev.AddChannel("0003STH03:"+strconv.Itoa(i), i, "X", hmenum.ParamsetKeyValues)
	}
	ch1 := dev.Channel("0003STH03:1")
	// An operator-assigned channel name suppresses the channel-number
	// disambiguator, which is the contrast case for the two devices
	// whose channels carry no name.
	ch1.SetName("Climate")
	goldenFloatSensor(ch1, hmenum.ParameterActualTemperature)
	goldenFloatSensor(ch1, hmenum.ParameterHumidity)
	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("CreateCustomDataPoints(climate sensor): %v", err)
	}
	return dev
}

// goldenContactSensor carries no operator name on the device or on any
// channel, so [friendlyName] falls back to the device address plus the
// channel-number disambiguator — the only path on which
// [Config.ChannelLabel] reaches an operator's controller.
func goldenContactSensor(t *testing.T) *device.Device {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "0004SWDO4",
		Model:        "HmIP-SWDO",
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	for i := range 2 {
		dev.AddChannel("0004SWDO4:"+strconv.Itoa(i), i, "X", hmenum.ParamsetKeyValues)
	}
	goldenBinarySensor(dev.Channel("0004SWDO4:1"), hmenum.ParameterState)
	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("CreateCustomDataPoints(contact sensor): %v", err)
	}
	return dev
}

// goldenConfig is the assembler configuration the fixture is recorded
// under. ChannelLabel is deliberately left empty so the recorded names
// travel through [Assembler.channelLabel]'s own fallback word rather
// than a value the test supplies.
func goldenConfig() matteradapter.Config {
	return matteradapter.Config{
		VendorID:            0x1234,
		ProductID:           0x5678,
		NodeLabel:           "GoldenBridge",
		IncludeMeasurements: true,
	}
}

// ─── recording ───────────────────────────────────────────────────────

// hexID renders a Matter identifier the way the specification and
// matter.js write it, so a fixture diff is readable against the
// cluster / device-type tables.
func hexID(v uint32) string { return fmt.Sprintf("0x%04X", v) }

// recordTopology projects an assembled topology onto the recorded
// shape. Everything it reads is either a plain field of the endpoint or
// an attribute read off a materialised cluster server — i.e. the same
// values a commissioner obtains over the wire.
func recordTopology(t *testing.T, top *endpoint.Topology) goldenTopology {
	t.Helper()
	out := goldenTopology{
		NodeLabel: top.NodeLabel,
		VendorID:  hexID(uint32(top.VendorID)),
		ProductID: hexID(uint32(top.ProductID)),
		Endpoints: make([]goldenEndpoint, 0, len(top.Endpoints)),
	}
	for _, ep := range top.Endpoints {
		servers := endpoint.ClusterServers(ep)
		rec := goldenEndpoint{
			EndpointID:          ep.ID,
			DeviceType:          hexID(uint32(ep.DeviceType)),
			FriendlyName:        ep.FriendlyName,
			Reachable:           ep.Reachable,
			ParentEndpointID:    ep.ParentEndpointID,
			HasParentEndpointID: ep.HasParentEndpointID,
			SourceKey: goldenSourceKey{
				CentralName:   ep.SourceKey.CentralName,
				DeviceAddress: ep.SourceKey.DeviceAddress,
				ChannelNo:     ep.SourceKey.ChannelNo,
				DPKind:        string(ep.SourceKey.DPKind),
				DPKey:         ep.SourceKey.DPKey,
			},
			ServerClusters: sortedClusterIDs(servers),
			DeviceTypeList: []goldenDeviceType{},
		}
		for _, srv := range servers {
			switch s := srv.(type) {
			case *mattercore.Descriptor:
				// DeviceTypeList, attribute 0x0000 (Matter §9.5.5.1).
				// The attribute id is unexported in the cluster package;
				// the literal is the specification's own.
				v, ok := s.MatterRead(0x0000)
				if !ok {
					t.Fatalf("EP %d: Descriptor did not answer the DeviceTypeList read", ep.ID)
				}
				list, ok := v.([]mattercore.DeviceTypeStruct)
				if !ok {
					t.Fatalf("EP %d: DeviceTypeList is %T, not []mattercore.DeviceTypeStruct", ep.ID, v)
				}
				for _, dt := range list {
					rec.DeviceTypeList = append(rec.DeviceTypeList, goldenDeviceType{
						ID: hexID(dt.DeviceType), Revision: dt.Revision,
					})
				}
			case *mattercore.BridgedDeviceBasicInformation:
				// UniqueID, attribute 0x0012 (Matter §9.13.5.20). Read
				// off the wire surface rather than recomputed, so the
				// fixture pins the value a controller actually caches.
				v, ok := s.MatterRead(0x0012)
				if !ok {
					t.Fatalf("EP %d: BridgedDeviceBasicInformation did not answer the UniqueID read", ep.ID)
				}
				uid, ok := v.(string)
				if !ok {
					t.Fatalf("EP %d: UniqueID is %T, not string", ep.ID, v)
				}
				rec.UniqueID = uid
			default:
			}
		}
		out.Endpoints = append(out.Endpoints, rec)
	}
	return out
}

// sortedClusterIDs returns the deduplicated, ascending server-cluster
// list of one endpoint. Sorted on purpose: the recorded set answers
// "which clusters does this endpoint serve", and mount order is a
// separate property that [TestMeteringPlugProjectsOneElectricalSensorEndpoint]
// and the dispatcher tests already own.
func sortedClusterIDs(servers []mattercontract.ClusterServer) []string {
	ids := make([]uint32, 0, len(servers))
	seen := make(map[uint32]struct{}, len(servers))
	for _, s := range servers {
		if s == nil {
			continue
		}
		id := s.MatterClusterID()
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, hexID(id))
	}
	return out
}

// ─── the guard ───────────────────────────────────────────────────────

// TestAssembledTopologyMatchesGolden is the behaviour-preservation
// anchor for the endpoint assembler: it runs the real
// [matteradapter.Assembler] over a fixed, hand-built device fleet and pins
// everything an already-commissioned controller has cached about the
// result.
//
// Why a golden rather than assertions: a controller keys its accessory
// list on the endpoint ID and its per-accessory identity on the
// UniqueID, so a refactor that renumbers an endpoint or re-derives a
// UniqueID desynchronises every paired controller until each device is
// removed and re-added by hand (see the Down comment of
// internal/store/sqlite/migrations/007_matter_endpoints.sql). Structural
// tests cannot see that: the assembler still returns a well-formed
// topology, only a different one.
//
// RECORDED, per endpoint: endpoint id, primary device type, the full
// Descriptor DeviceTypeList with revisions, the
// BridgedDeviceBasicInformation NodeLabel (FriendlyName) and UniqueID,
// Reachable, the parent-endpoint relation, the persisted source key,
// and the sorted, deduplicated server-cluster list.
//
// EXCLUDED, and why:
//
//   - Per-cluster DataVersion. Seeded from a random non-zero value on
//     first access ([mattercontract.DataVersionTracker]) and bound to
//     the assembler instance, so it differs on every run by design.
//   - The root (EP 0) and Aggregator (EP 1) cluster sets. Those servers
//     are built by the daemon and published onto the endpoints via
//     [endpoint.Endpoint.PublishClusterServers]; the assembler leaves
//     them unset, so both endpoints legitimately record an empty
//     server-cluster list here.
//   - Live measurement values. Nothing in the fleet is fed a value, and
//     the recorded shape reads no measurement attribute, so a reading
//     cannot leak into the fixture.
//   - Pointer-valued fields (Source, Measurement, PowerSource) and the
//     live availability probe. Identity, not wire content.
//
// DETERMINISM: the assembler sorts its output by endpoint id
// (Assemble's sort.SliceStable), and every model accessor it walks —
// Device.Channels, Channel.DataPoints, Channel.CalculatedDataPoints,
// Channel.CombinedDataPoints — returns a sorted snapshot rather than a
// map range, so the recording order is the assembler's own and is
// asserted strictly ascending below rather than sorted after the fact.
// The one remaining variable input is the UniqueID salt: uniqueIDFor
// mixes [bootid.Salt], which is all-zero unless some other test in this
// binary switches rotation on. The test refuses to run against a
// rotating salt rather than recording a value that cannot be
// reproduced.
//
// Refresh after an intentional change with:
//
//	go test -update-topology-golden ./internal/north/matteradapter/...
func TestAssembledTopologyMatchesGolden(t *testing.T) {
	if got := bootid.Salt(); got != [16]byte{} {
		t.Fatalf("bootid rotation is enabled in this test binary (salt %x); every recorded UniqueID "+
			"would be unreproducible. Some other test called bootid.EnableRotation or bootid.SetForTest "+
			"at package scope — move that into its own binary before touching this fixture.", got)
	}

	a, err := matteradapter.New(newFakeStore(), goldenConfig(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	top, err := a.AssembleDevices(context.Background(), []matteradapter.DeviceSnapshot{{
		CentralName:   goldenCentralName,
		Devices:       goldenFleet(t),
		ModelComplete: true,
	}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// Endpoint ids must come back strictly ascending. If a change ever
	// makes assembly order depend on a map range, this fires with a
	// pointed message instead of the fixture churning between runs.
	for i := 1; i < len(top.Endpoints); i++ {
		if top.Endpoints[i-1].ID >= top.Endpoints[i].ID {
			t.Fatalf("endpoint ids are not strictly ascending at index %d: %d then %d — "+
				"assembly order is no longer deterministic and the fixture cannot be trusted",
				i, top.Endpoints[i-1].ID, top.Endpoints[i].ID)
		}
	}

	got := recordTopology(t, top)
	gotBlob, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	gotBlob = append(gotBlob, '\n')

	goldenPath := filepath.Join("testdata", "topology.golden.json")
	if *updateTopologyGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o750); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, gotBlob, 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	wantRaw, err := os.ReadFile(goldenPath) //nolint:gosec // G304: constant path under the package's own testdata
	if err != nil {
		t.Fatalf("read golden: %v — run `go test -update-topology-golden ./internal/north/matteradapter/...`", err)
	}
	// Windows checkouts with core.autocrlf=true rewrite LF→CRLF on text
	// files; the encoder always emits LF.
	want := bytes.ReplaceAll(wantRaw, []byte{'\r'}, nil)
	if bytes.Equal(gotBlob, want) {
		return
	}
	t.Errorf("assembled topology drifted from testdata/topology.golden.json.\n"+
		"Every difference below is something a commissioned controller has cached.\n"+
		"Re-record with -update-topology-golden ONLY after deciding the change is intended\n"+
		"and that paired controllers may be re-linked.\n%s", lineDiff(want, gotBlob))
}

// lineDiff renders the differing lines of two fixtures. A byte-level
// "files differ" message would leave the reader to diff by hand, and
// the whole point of this fixture is that the reader sees which
// controller-visible field moved.
func lineDiff(want, got []byte) string {
	wantLines := bytes.Split(want, []byte{'\n'})
	gotLines := bytes.Split(got, []byte{'\n'})
	var b bytes.Buffer
	n := max(len(wantLines), len(gotLines))
	shown := 0
	for i := range n {
		var w, g []byte
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if bytes.Equal(w, g) {
			continue
		}
		if shown == 40 {
			fmt.Fprintf(&b, "  … further differences suppressed\n")
			break
		}
		shown++
		fmt.Fprintf(&b, "  line %d:\n    golden: %s\n    got:    %s\n", i+1, w, g)
	}
	return b.String()
}
