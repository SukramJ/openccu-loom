// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// recordingCaller records recent calls — unlike fakeCaller (atomic-based)
// it uses a simple slice for sequence assertions.
type recordingCaller struct {
	lastPriority hmenum.CommandPriority
	calls        []recordedCall
	reply        any
	replies      map[string]any // method → reply (takes precedence over reply)
	err          error
}

type recordedCall struct {
	Method string
	Args   []any
}

func (r *recordingCaller) Call(_ context.Context, method string, args ...any) (any, error) {
	r.calls = append(r.calls, recordedCall{Method: method, Args: args})
	if r.err != nil {
		return nil, r.err
	}
	if reply, ok := r.replies[method]; ok {
		return reply, nil
	}
	return r.reply, nil
}

func (r *recordingCaller) lastCall() recordedCall {
	if len(r.calls) == 0 {
		return recordedCall{}
	}
	return r.calls[len(r.calls)-1]
}

func TestHomegearBackendKindAndCapabilities(t *testing.T) {
	b := NewHomegearBackend(&recordingCaller{}, nil)
	if b.Kind() != KindHomegear {
		t.Fatalf("kind=%s", b.Kind())
	}
	caps := b.Capabilities()
	if !caps.RPCCallback || !caps.ListDevices {
		t.Fatalf("expected push-capable caps, got %+v", caps)
	}
	if caps.PingPong {
		t.Fatal("Homegear does not implement ping/pong — must not claim PingPong")
	}
	if caps.FirmwareUpdate {
		t.Fatal("Homegear is XML-RPC-only — must not claim firmware update")
	}
}

func TestHomegearPingUsesClientServerInitialized(t *testing.T) {
	x := &recordingCaller{}
	b := NewHomegearBackend(x, nil)
	if err := b.Ping(context.Background(), "homegear-rf"); err != nil {
		t.Fatalf("ping: %v", err)
	}
	last := x.lastCall()
	if last.Method != "clientServerInitialized" {
		t.Fatalf("method=%s, want clientServerInitialized", last.Method)
	}
	if len(last.Args) != 1 || last.Args[0] != "homegear-rf" {
		t.Fatalf("args=%v", last.Args)
	}
}

func TestHomegearListDevicesNormalizes(t *testing.T) {
	x := &recordingCaller{reply: []any{
		map[string]any{"ADDRESS": "ABCD1234", "TYPE": "HM-LC-Bl1-FM"},
		map[string]any{"ADDRESS": "ABCD1234:1", "TYPE": "BLIND", "PARENT": "ABCD1234"},
	}}
	b := NewHomegearBackend(x, nil)
	devs, err := b.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devs) != 2 {
		t.Fatalf("len=%d", len(devs))
	}
	if devs[0].Address != "ABCD1234" || devs[1].Parent != "ABCD1234" {
		t.Fatalf("unexpected mapping: %+v", devs)
	}
}

func TestHomegearGetSetParamset(t *testing.T) {
	x := &recordingCaller{reply: map[string]any{"STATE": true}}
	b := NewHomegearBackend(x, nil)

	got, err := b.GetParamset(context.Background(), "ABCD1234:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got["STATE"] != true {
		t.Fatalf("got=%v", got)
	}

	x.reply = nil
	if err := b.PutParamset(context.Background(), "ABCD1234:1", hmenum.ParamsetKeyValues, map[string]any{"STATE": false}, hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset); err != nil {
		t.Fatalf("put: %v", err)
	}
	last := x.lastCall()
	if last.Method != "putParamset" || last.Args[0] != "ABCD1234:1" || last.Args[1] != "VALUES" {
		t.Fatalf("args=%v", last.Args)
	}
	values, _ := last.Args[2].(map[string]any)
	if values["STATE"] != false {
		t.Fatalf("values=%v", values)
	}
}

func TestHomegearSetGetValue(t *testing.T) {
	x := &recordingCaller{reply: 23.5}
	b := NewHomegearBackend(x, nil)

	v, err := b.GetValue(context.Background(), "ABCD1234:1", hmenum.ParameterActualTemperature)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v.(float64) != 23.5 {
		t.Fatalf("v=%v", v)
	}

	if err := b.SetValue(context.Background(), "ABCD1234:1", hmenum.ParameterState, true, hmenum.CommandPriorityHigh, hmenum.CommandRxModeUnset); err != nil {
		t.Fatalf("set: %v", err)
	}
	last := x.lastCall()
	if last.Method != "setValue" {
		t.Fatalf("method=%s", last.Method)
	}
	// Priority must NOT be forwarded as an XML-RPC argument.
	if len(last.Args) != 3 {
		t.Fatalf("args=%v (priority must not leak to wire)", last.Args)
	}
	if last.Args[0] != "ABCD1234:1" || last.Args[1] != "STATE" || last.Args[2] != true {
		t.Fatalf("args=%v", last.Args)
	}
}

func TestHomegearFirmwareUpdateUnsupported(t *testing.T) {
	b := NewHomegearBackend(&recordingCaller{}, nil)
	if err := b.UpdateFirmware(context.Background(), "ABCD1234"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err=%v, want ErrUnsupported", err)
	}
}

func TestHomegearSystemVariablesViaXMLRPC(t *testing.T) {
	x := &recordingCaller{
		replies: map[string]any{
			"getSystemVariable":     "hello",
			"getAllSystemVariables": map[string]any{"a": 1, "b": "two"},
		},
	}
	b := NewHomegearBackend(x, nil)

	v, err := b.GetSystemVariable(context.Background(), "var")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v != "hello" {
		t.Fatalf("v=%v", v)
	}

	// GetAllSystemVariables now satisfies Operations (returns []map[string]any).
	// Use GetAllSystemVariablesRaw for direct map access in tests.
	allRaw, err := b.GetAllSystemVariablesRaw(context.Background())
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(allRaw) != 2 || allRaw["a"].(int) != 1 || allRaw["b"].(string) != "two" {
		t.Fatalf("all=%v", allRaw)
	}

	if err := b.SetSystemVariable(context.Background(), "var", 42); err != nil {
		t.Fatalf("set: %v", err)
	}
	last := x.lastCall()
	if last.Method != "setSystemVariable" || last.Args[0] != "var" || last.Args[1].(int) != 42 {
		t.Fatalf("set call=%v", last)
	}

	if ok, err := b.DeleteSystemVariable(context.Background(), "var"); !ok || err != nil {
		t.Fatalf("del: ok=%v err=%v", ok, err)
	}
	last = x.lastCall()
	if last.Method != "deleteSystemVariable" || last.Args[0] != "var" {
		t.Fatalf("del call=%v", last)
	}
}

func TestHomegearGetAllSystemVariablesNilReturnsEmpty(t *testing.T) {
	x := &recordingCaller{reply: nil}
	b := NewHomegearBackend(x, nil)
	all, err := b.GetAllSystemVariables(context.Background())
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(all) != 0 {
		t.Fatalf("expected empty map, got %v", all)
	}
}

func TestHomegearMetadataAndDeviceName(t *testing.T) {
	x := &recordingCaller{
		replies: map[string]any{
			"getMetadata": "Wohnzimmerlampe",
		},
	}
	b := NewHomegearBackend(x, nil)

	v, err := b.GetMetadata(context.Background(), "ABCD1234", "NAME")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v != "Wohnzimmerlampe" {
		t.Fatalf("v=%v", v)
	}

	name, err := b.GetDeviceName(context.Background(), "ABCD1234")
	if err != nil {
		t.Fatalf("name: %v", err)
	}
	if name != "Wohnzimmerlampe" {
		t.Fatalf("name=%q", name)
	}

	if err := b.SetMetadata(context.Background(), "ABCD1234", "NAME", "Küche"); err != nil {
		t.Fatalf("set: %v", err)
	}
	last := x.lastCall()
	if last.Method != "setMetadata" || last.Args[0] != "ABCD1234" || last.Args[1] != "NAME" || last.Args[2] != "Küche" {
		t.Fatalf("set call=%v", last)
	}

	if err := b.DeleteMetadata(context.Background(), "ABCD1234", "NAME"); err != nil {
		t.Fatalf("del: %v", err)
	}
	last = x.lastCall()
	if last.Method != "deleteMetadata" {
		t.Fatalf("del call=%v", last)
	}
}

func TestHomegearGetDeviceNameMissingMetadataIsEmpty(t *testing.T) {
	x := &recordingCaller{reply: nil}
	b := NewHomegearBackend(x, nil)
	name, err := b.GetDeviceName(context.Background(), "ABCD1234")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if name != "" {
		t.Fatalf("name=%q, want empty", name)
	}
}

func TestHomegearGetDeviceNameNonStringMetadataCoerces(t *testing.T) {
	x := &recordingCaller{reply: 42}
	b := NewHomegearBackend(x, nil)
	name, err := b.GetDeviceName(context.Background(), "ABCD1234")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if name != "42" {
		t.Fatalf("name=%q, want \"42\"", name)
	}
}

func TestHomegearLinkOps(t *testing.T) {
	x := &recordingCaller{
		replies: map[string]any{
			"getLinks": []any{
				map[string]any{
					"SENDER":      "ABCD1234:1",
					"RECEIVER":    "EF567890:1",
					"NAME":        "Treppe",
					"DESCRIPTION": "Auto",
					"FLAGS":       0,
				},
			},
			"getLinkPeers": []any{"EF567890:1", "EF567890:2"},
		},
	}
	b := NewHomegearBackend(x, nil)

	links, err := b.GetLinks(context.Background(), "ABCD1234:1")
	if err != nil {
		t.Fatalf("links: %v", err)
	}
	if len(links) != 1 || links[0].Sender != "ABCD1234:1" || links[0].Receiver != "EF567890:1" {
		t.Fatalf("links=%+v", links)
	}

	peers, err := b.GetLinkPeers(context.Background(), "ABCD1234:1")
	if err != nil {
		t.Fatalf("peers: %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("peers=%v", peers)
	}

	if err := b.AddLink(context.Background(), "ABCD1234:1", "EF567890:1", "Bind", "auto"); err != nil {
		t.Fatalf("add: %v", err)
	}
	last := x.lastCall()
	if last.Method != "addLink" || len(last.Args) != 4 {
		t.Fatalf("add call=%v", last)
	}

	if err := b.RemoveLink(context.Background(), "ABCD1234:1", "EF567890:1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	last = x.lastCall()
	if last.Method != "removeLink" || last.Args[0] != "ABCD1234:1" || last.Args[1] != "EF567890:1" {
		t.Fatalf("remove call=%v", last)
	}
}

func TestHomegearReportValueUsage(t *testing.T) {
	x := &recordingCaller{}
	b := NewHomegearBackend(x, nil)
	if err := b.ReportValueUsage(context.Background(), "ABCD1234:1", "STATE", 3); err != nil {
		t.Fatalf("err=%v", err)
	}
	last := x.lastCall()
	if last.Method != "reportValueUsage" {
		t.Fatalf("method=%s", last.Method)
	}
	if last.Args[0] != "ABCD1234:1" || last.Args[1] != "STATE" || last.Args[2] != 3 {
		t.Fatalf("args=%v", last.Args)
	}
}

func TestHomegearAllOpsErrNotWiredWithoutCaller(t *testing.T) {
	b := NewHomegearBackend(nil, nil)
	ctx := context.Background()

	if err := b.Ping(ctx, "x"); !errors.Is(err, ErrNotWired) {
		t.Errorf("Ping err=%v", err)
	}
	if _, err := b.ListDevices(ctx); !errors.Is(err, ErrNotWired) {
		t.Errorf("ListDevices err=%v", err)
	}
	if _, err := b.GetParamsetDescription(ctx, "x", hmenum.ParamsetKeyValues); !errors.Is(err, ErrNotWired) {
		t.Errorf("GetParamsetDescription err=%v", err)
	}
	if _, err := b.GetParamset(ctx, "x", hmenum.ParamsetKeyValues); !errors.Is(err, ErrNotWired) {
		t.Errorf("GetParamset err=%v", err)
	}
	if err := b.PutParamset(ctx, "x", hmenum.ParamsetKeyValues, nil, hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset); !errors.Is(err, ErrNotWired) {
		t.Errorf("PutParamset err=%v", err)
	}
	if _, err := b.GetSystemVariable(ctx, "x"); !errors.Is(err, ErrNotWired) {
		t.Errorf("GetSystemVariable err=%v", err)
	}
	if _, err := b.GetMetadata(ctx, "x", "NAME"); !errors.Is(err, ErrNotWired) {
		t.Errorf("GetMetadata err=%v", err)
	}
}

func TestHomegearInitDeinitWithoutAnnouncerNoop(t *testing.T) {
	b := NewHomegearBackend(&recordingCaller{}, nil)
	if err := b.Init(context.Background(), "homegear-rf", "http://cb"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := b.Deinit(context.Background(), "homegear-rf"); err != nil {
		t.Fatalf("deinit: %v", err)
	}
}

type recordingAnnouncer struct {
	inits   int
	deinits int
}

func (r *recordingAnnouncer) Init(_ context.Context, _, _ string) error { r.inits++; return nil }
func (r *recordingAnnouncer) Deinit(_ context.Context, _ string) error  { r.deinits++; return nil }

func TestHomegearInitDeinitDelegatesToAnnouncer(t *testing.T) {
	ann := &recordingAnnouncer{}
	b := NewHomegearBackend(&recordingCaller{}, ann)
	if err := b.Init(context.Background(), "homegear-rf", "http://cb"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := b.Deinit(context.Background(), "homegear-rf"); err != nil {
		t.Fatalf("deinit: %v", err)
	}
	if ann.inits != 1 || ann.deinits != 1 {
		t.Fatalf("inits=%d deinits=%d", ann.inits, ann.deinits)
	}
}

func TestHomegearDeleteDevice(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{}
	b := NewHomegearBackend(x, nil)
	if err := b.DeleteDevice(context.Background(), "ABCD1234", 0); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	last := x.lastCall()
	if last.Method != "deleteDevice" {
		t.Fatalf("method=%s, want deleteDevice", last.Method)
	}
	if len(last.Args) != 2 || last.Args[0] != "ABCD1234" || last.Args[1] != 0 {
		t.Fatalf("args=%v, want [ABCD1234 0]", last.Args)
	}
}

// TestHomegearDeleteDeviceForwardsFlags verifies the delete bitmask reaches
// Homegear on the wire rather than a hard-coded 0.
func TestHomegearDeleteDeviceForwardsFlags(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{}
	b := NewHomegearBackend(x, nil)
	flags := DeleteFlagReset | DeleteFlagForce
	if err := b.DeleteDevice(context.Background(), "ABCD1234", flags); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	last := x.lastCall()
	if len(last.Args) != 2 || last.Args[0] != "ABCD1234" || last.Args[1] != flags {
		t.Fatalf("args=%v, want [ABCD1234 %d]", last.Args, flags)
	}
}

func TestHomegearDeleteDeviceWithoutXMLRPC(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	if err := b.DeleteDevice(context.Background(), "ABCD1234", 0); !errors.Is(err, ErrNotWired) {
		t.Fatalf("expected ErrNotWired, got %v", err)
	}
}

func TestHomegearOperationsInterfaceCompliance(t *testing.T) {
	// Compile-time + runtime check: HomegearBackend must satisfy the
	// full Operations interface.
	var _ Operations = (*HomegearBackend)(nil)
	b := NewHomegearBackend(&recordingCaller{}, nil)
	var op Operations = b
	if op.Kind() != KindHomegear {
		t.Fatalf("kind=%s", op.Kind())
	}
}

// TestHomegearCapabilitiesAdvertisedMatchImplementation verifies that the
// three capability flags that were previously missing (LinkOperations,
// DeleteDevice, Metadata) are now correctly advertised in the Homegear
// capability matrix and that the implementations work as expected.
func TestHomegearCapabilitiesAdvertisedMatchImplementation(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindHomegear)

	if !caps.LinkOperations {
		t.Error("KindHomegear: LinkOperations must be true — getLinks/addLink/removeLink/getLinkPeers are implemented via XML-RPC")
	}
	if !caps.DeleteDevice {
		t.Error("KindHomegear: DeleteDevice must be true — deleteDevice(address,0) is implemented via XML-RPC")
	}
	if caps.Metadata {
		t.Error("KindHomegear: Metadata must be false — Homegear does not implement getMetadata/setMetadata/deleteMetadata")
	}
}

// TestHomegearGetLinkParamsetDescriptionAndPutLinkParamset verifies
// the GetLinkParamsetDescription and PutLinkParamset wire calls.
func TestHomegearGetLinkParamsetDescriptionAndPutLinkParamset(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{
		replies: map[string]any{
			"getParamsetDescription": map[string]any{
				"TOGGLE": map[string]any{
					"TYPE":       "BOOL",
					"MAX":        true,
					"MIN":        false,
					"DEFAULT":    false,
					"ID":         "TOGGLE",
					"FLAGS":      1,
					"OPERATIONS": 7,
				},
			},
		},
	}
	b := NewHomegearBackend(x, nil)

	desc, err := b.GetLinkParamsetDescription(context.Background(), "ABCD1234:1", "EF567890:1")
	if err != nil {
		t.Fatalf("GetLinkParamsetDescription: %v", err)
	}
	if len(desc) == 0 {
		t.Fatal("GetLinkParamsetDescription: expected non-empty descriptor")
	}
	// Wire call must use "LINK" as the key, not the peerAddress.
	last := x.lastCall()
	if last.Method != "getParamsetDescription" {
		t.Fatalf("method=%s, want getParamsetDescription", last.Method)
	}
	if len(last.Args) < 2 || last.Args[1] != "LINK" {
		t.Fatalf("expected key=LINK, args=%v", last.Args)
	}

	// PutLinkParamset uses peerAddress directly as the paramset key.
	if err := b.PutLinkParamset(context.Background(), "ABCD1234:1", "EF567890:1", map[string]any{"TOGGLE": true}); err != nil {
		t.Fatalf("PutLinkParamset: %v", err)
	}
	last = x.lastCall()
	if last.Method != "putParamset" {
		t.Fatalf("method=%s, want putParamset", last.Method)
	}
	if len(last.Args) < 2 || last.Args[0] != "ABCD1234:1" || last.Args[1] != "EF567890:1" {
		t.Fatalf("PutLinkParamset args=%v", last.Args)
	}
}

// TestHomegearDetermineParameter verifies that DetermineParameter forwards
// to the XML-RPC determineParameter method.
func TestHomegearDetermineParameter(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{reply: 42.0}
	b := NewHomegearBackend(x, nil)

	v, err := b.DetermineParameter(context.Background(), "ABCD1234:1", "LEVEL")
	if err != nil {
		t.Fatalf("DetermineParameter: %v", err)
	}
	if v.(float64) != 42.0 {
		t.Fatalf("value=%v, want 42.0", v)
	}
	last := x.lastCall()
	if last.Method != "determineParameter" {
		t.Fatalf("method=%s, want determineParameter", last.Method)
	}
	if len(last.Args) != 2 || last.Args[0] != "ABCD1234:1" || last.Args[1] != "LEVEL" {
		t.Fatalf("args=%v", last.Args)
	}
}

// TestHomegearDetermineParameterNotWired verifies ErrNotWired when no
// XML caller is configured.
func TestHomegearDetermineParameterNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	if _, err := b.DetermineParameter(context.Background(), "ABCD1234:1", "LEVEL"); !errors.Is(err, ErrNotWired) {
		t.Fatalf("expected ErrNotWired, got %v", err)
	}
}

// TestHomegearGetDeviceDetailsReturnsNameAndEmptyID verifies that
// GetDeviceDetails builds the expected minimal shape (address, name, id=0,
// channels=[]) for each input address.
func TestHomegearGetDeviceDetailsReturnsNameAndEmptyID(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{reply: "Wohnzimmer"}
	b := NewHomegearBackend(x, nil)

	details, err := b.GetDeviceDetails(context.Background(), []string{"ABCD1234", "EF567890"})
	if err != nil {
		t.Fatalf("GetDeviceDetails: %v", err)
	}
	if len(details) != 2 {
		t.Fatalf("len=%d, want 2", len(details))
	}
	for _, d := range details {
		if d["id"].(int) != 0 {
			t.Errorf("id=%v, want 0", d["id"])
		}
		if _, ok := d["address"].(string); !ok {
			t.Errorf("address missing in %v", d)
		}
	}
}

// ---------------------------------------------------------------------------
// HomegearBackend — error / type-mismatch branch coverage
// ---------------------------------------------------------------------------

// TestHomegearGetLinkParamsetHappyPath covers GetLinkParamset success path.
func TestHomegearGetLinkParamsetHappyPath(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{reply: map[string]any{"TOGGLE": true}}
	b := NewHomegearBackend(x, nil)

	got, err := b.GetLinkParamset(context.Background(), "ABCD1234:1", "EF567890:1")
	if err != nil {
		t.Fatalf("GetLinkParamset: %v", err)
	}
	if got["TOGGLE"] != true {
		t.Fatalf("got=%v", got)
	}
	last := x.lastCall()
	if last.Method != "getParamset" {
		t.Fatalf("method=%s, want getParamset", last.Method)
	}
	if len(last.Args) < 2 || last.Args[0] != "ABCD1234:1" || last.Args[1] != "EF567890:1" {
		t.Fatalf("args=%v", last.Args)
	}
}

func TestHomegearGetLinkParamsetNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	_, err := b.GetLinkParamset(context.Background(), "ABCD1234:1", "EF567890:1")
	if !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestHomegearGetLinkParamsetTypeError(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{reply: "not a map"}
	b := NewHomegearBackend(x, nil)
	_, err := b.GetLinkParamset(context.Background(), "ABCD1234:1", "EF567890:1")
	if err == nil {
		t.Fatal("expected error on wrong type, got nil")
	}
}

// TestHomegearGetDeviceDescriptionHappyPath covers the success branch.
func TestHomegearGetDeviceDescriptionHappyPath(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{reply: map[string]any{"ADDRESS": "ABCD1234", "TYPE": "HM-LC-Bl1-FM"}}
	b := NewHomegearBackend(x, nil)

	got, err := b.GetDeviceDescription(context.Background(), "ABCD1234")
	if err != nil {
		t.Fatalf("GetDeviceDescription: %v", err)
	}
	if got["ADDRESS"] != "ABCD1234" {
		t.Fatalf("ADDRESS=%v", got["ADDRESS"])
	}
	last := x.lastCall()
	if last.Method != "getDeviceDescription" {
		t.Fatalf("method=%s, want getDeviceDescription", last.Method)
	}
}

func TestHomegearGetDeviceDescriptionTypeError(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{reply: "not a map"}
	b := NewHomegearBackend(x, nil)
	_, err := b.GetDeviceDescription(context.Background(), "ABCD1234")
	if err == nil {
		t.Fatal("expected error on wrong type, got nil")
	}
}

// TestHomegearGetParamsetDescriptionHappyPath covers the success and
// type-mismatch branches.
func TestHomegearGetParamsetDescriptionHappyPath(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{reply: map[string]any{
		"STATE": map[string]any{"TYPE": "BOOL", "OPERATIONS": 7},
	}}
	b := NewHomegearBackend(x, nil)

	out, err := b.GetParamsetDescription(context.Background(), "ABCD1234:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("GetParamsetDescription: %v", err)
	}
	pd, ok := out["STATE"]
	if !ok {
		t.Fatalf("STATE missing in %v", out)
	}
	if pd.Operations != 7 {
		t.Fatalf("operations=%d, want 7", pd.Operations)
	}
	last := x.lastCall()
	if last.Method != "getParamsetDescription" {
		t.Fatalf("method=%s", last.Method)
	}
}

func TestHomegearGetParamsetDescriptionTypeError(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{reply: "not a map"}
	b := NewHomegearBackend(x, nil)
	_, err := b.GetParamsetDescription(context.Background(), "ABCD1234:1", hmenum.ParamsetKeyValues)
	if err == nil {
		t.Fatal("expected error on wrong type, got nil")
	}
}

func TestHomegearGetParamsetDescriptionCallError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("rpc call failed")
	x := &recordingCaller{err: sentinel}
	b := NewHomegearBackend(x, nil)
	_, err := b.GetParamsetDescription(context.Background(), "ABCD1234:1", hmenum.ParamsetKeyValues)
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// HomegearBackend — ErrNotWired nil-caller branches
// ---------------------------------------------------------------------------

func TestHomegearSetSystemVariableNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	if err := b.SetSystemVariable(context.Background(), "x", 1); !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestHomegearDeleteSystemVariableNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	if _, err := b.DeleteSystemVariable(context.Background(), "x"); !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestHomegearSetMetadataNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	if err := b.SetMetadata(context.Background(), "ADDR", "NAME", "x"); !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestHomegearDeleteMetadataNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	if err := b.DeleteMetadata(context.Background(), "ADDR", "NAME"); !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestHomegearAddLinkNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	if err := b.AddLink(context.Background(), "S:1", "R:1", "n", "d"); !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestHomegearRemoveLinkNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	if err := b.RemoveLink(context.Background(), "S:1", "R:1"); !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestHomegearGetLinksTypeError(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{reply: "not a slice"}
	b := NewHomegearBackend(x, nil)
	_, err := b.GetLinks(context.Background(), "ABCD1234:1")
	if err == nil {
		t.Fatal("expected error on wrong type, got nil")
	}
}

func TestHomegearGetLinkPeersTypeError(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{reply: "not a slice"}
	b := NewHomegearBackend(x, nil)
	_, err := b.GetLinkPeers(context.Background(), "ABCD1234:1")
	if err == nil {
		t.Fatal("expected error on wrong type, got nil")
	}
}

func TestHomegearGetAllSystemVariablesRawTypeError(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{reply: "not a map"}
	b := NewHomegearBackend(x, nil)
	_, err := b.GetAllSystemVariablesRaw(context.Background())
	if err == nil {
		t.Fatal("expected error on wrong type, got nil")
	}
}

func TestHomegearGetAllSystemVariablesRawNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	_, err := b.GetAllSystemVariablesRaw(context.Background())
	if !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestHomegearGetAllSystemVariablesErrorPropagates(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("xml rpc failure")
	x := &recordingCaller{err: sentinel}
	b := NewHomegearBackend(x, nil)
	_, err := b.GetAllSystemVariables(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
}

func TestHomegearReportValueUsageNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	if err := b.ReportValueUsage(context.Background(), "ADDR:1", "STATE", 1); !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestHomegearSetValueNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	if err := b.SetValue(context.Background(), "ADDR:1", hmenum.ParameterState, true, hmenum.CommandPriorityHigh, hmenum.CommandRxModeUnset); !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestHomegearGetValueNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	if _, err := b.GetValue(context.Background(), "ADDR:1", hmenum.ParameterState); !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestHomegearGetLinkParamsetDescriptionNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	if _, err := b.GetLinkParamsetDescription(context.Background(), "ADDR:1", "PEER:1"); !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestHomegearGetLinkParamsetDescriptionTypeError(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{reply: "not a map"}
	b := NewHomegearBackend(x, nil)
	if _, err := b.GetLinkParamsetDescription(context.Background(), "ADDR:1", "PEER:1"); err == nil {
		t.Fatal("expected error on wrong type, got nil")
	}
}

func TestHomegearPutLinkParamsetNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	if err := b.PutLinkParamset(context.Background(), "ADDR:1", "PEER:1", nil); !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestHomegearListDevicesTypeError(t *testing.T) {
	t.Parallel()
	x := &recordingCaller{reply: "not a slice"}
	b := NewHomegearBackend(x, nil)
	if _, err := b.ListDevices(context.Background()); err == nil {
		t.Fatal("expected error on wrong type, got nil")
	}
}

func TestHomegearListDevicesEntryTypeError(t *testing.T) {
	t.Parallel()
	// A slice containing a non-map entry must surface an error.
	x := &recordingCaller{reply: []any{"not a map"}}
	b := NewHomegearBackend(x, nil)
	if _, err := b.ListDevices(context.Background()); err == nil {
		t.Fatal("expected error for non-map entry, got nil")
	}
}

func TestHomegearGetDeviceDetailsNotWired(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(nil, nil)
	if _, err := b.GetDeviceDetails(context.Background(), []string{"ADDR"}); !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

// CallAt implements Caller: records the priority the command carried
// alongside the call itself.
func (r *recordingCaller) CallAt(
	ctx context.Context, priority hmenum.CommandPriority, method string, args ...any,
) (any, error) {
	r.lastPriority = priority
	return r.Call(ctx, method, args...)
}
