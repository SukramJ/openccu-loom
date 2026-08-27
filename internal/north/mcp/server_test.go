// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── fakes ───────────────────────────────────────────────────────────────────

// fakeParamsets is a minimal in-memory implementation of mcp.ParamsetService.
type fakeParamsets struct {
	store    map[string]map[string]any // address:key → values
	putCalls []putParamsetCall
	err      error
}

type putParamsetCall struct {
	address string
	key     hmenum.ParamsetKey
	values  map[string]any
}

func newFakeParamsets() *fakeParamsets {
	return &fakeParamsets{store: make(map[string]map[string]any)}
}

func (f *fakeParamsets) seed(address string, key hmenum.ParamsetKey, values map[string]any) {
	f.store[address+":"+string(key)] = values
}

func (f *fakeParamsets) GetParamset(_ context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	v := f.store[address+":"+string(key)]
	return v, nil
}

func (f *fakeParamsets) PutParamset(_ context.Context, address string, key hmenum.ParamsetKey, values map[string]any) error {
	if f.err != nil {
		return f.err
	}
	f.putCalls = append(f.putCalls, putParamsetCall{address: address, key: key, values: values})
	return nil
}

// fakeHealth is a minimal in-memory implementation of mcp.HealthReader.
type fakeHealth struct {
	overall    health.Status
	components []health.Component
	gauges     map[string]float64
}

func (f *fakeHealth) Overall() health.Status       { return f.overall }
func (f *fakeHealth) Snapshot() []health.Component { return f.components }
func (f *fakeHealth) Gauges() map[string]float64   { return f.gauges }

// fakeHubs resolves a central name to a pre-built *hub.Hub.
type fakeHubs struct {
	hubs map[string]*hub.Hub
}

func newFakeHubs() *fakeHubs { return &fakeHubs{hubs: make(map[string]*hub.Hub)} }

func (f *fakeHubs) add(centralName string, h *hub.Hub) { f.hubs[centralName] = h }

func (f *fakeHubs) HubFor(centralName string) *hub.Hub { return f.hubs[centralName] }

// fakeProgramWriter records ExecuteProgram calls and returns a preset error.
type fakeProgramWriter struct {
	calls []string
	err   error
}

func (f *fakeProgramWriter) ExecuteProgram(_ context.Context, id string) error {
	f.calls = append(f.calls, id)
	return f.err
}

func (f *fakeProgramWriter) SetProgramEnabled(_ context.Context, _ string, _ bool) error {
	return errors.New("not implemented in fake")
}

type fakeCentrals struct{ names []string }

func (f *fakeCentrals) Names() []string { return f.names }

type fakeDevices struct {
	devices  map[string]*device.Device
	centrals map[string]string // address → central name
}

func newFakeDevices() *fakeDevices {
	return &fakeDevices{
		devices:  make(map[string]*device.Device),
		centrals: make(map[string]string),
	}
}

func (f *fakeDevices) add(d *device.Device, central string) {
	f.devices[d.Address] = d
	f.centrals[d.Address] = central
}

func (f *fakeDevices) Devices() []*device.Device {
	out := make([]*device.Device, 0, len(f.devices))
	for _, d := range f.devices {
		out = append(out, d)
	}
	return out
}

func (f *fakeDevices) Device(address string) (*device.Device, bool) {
	d, ok := f.devices[address]
	return d, ok
}

func (f *fakeDevices) CentralOf(address string) string {
	return f.centrals[address]
}

func (f *fakeDevices) Released(string) bool { return true }

type writeCall struct {
	address   string
	parameter hmenum.Parameter
	value     any
	priority  hmenum.CommandPriority
}

type fakeWriter struct {
	last writeCall
	err  error
}

func (fw *fakeWriter) SetValue(
	_ context.Context,
	address string,
	parameter hmenum.Parameter,
	value any,
	priority hmenum.CommandPriority,
) error {
	fw.last = writeCall{address: address, parameter: parameter, value: value, priority: priority}
	return fw.err
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// connect pairs a new MCP server (built from deps) with an in-memory client
// and returns the ready client session. The test calls defer cs.Close().
func connect(t *testing.T, deps mcp.Deps) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()

	srv := mcp.NewServer(deps)
	t1, t2 := mcpsdk.NewInMemoryTransports()

	_, err := srv.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server Connect: %v", err)
	}

	c := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	return cs
}

// callTool invokes a tool by name and returns the raw result.
func callTool(t *testing.T, cs *mcpsdk.ClientSession, name string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool %q: %v", name, err)
	}
	return res
}

// unmarshalStructured round-trips StructuredContent through JSON into dst.
func unmarshalStructured(t *testing.T, res *mcpsdk.CallToolResult, dst any) {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal StructuredContent: %v", err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("unmarshal StructuredContent: %v", err)
	}
}

// toolNames collects all tool names advertised by the server.
func toolNames(t *testing.T, cs *mcpsdk.ClientSession) map[string]bool {
	t.Helper()
	names := make(map[string]bool)
	next, stop := iter.Pull2(cs.Tools(context.Background(), nil))
	defer stop()
	for {
		tool, err, ok := next()
		if !ok {
			break
		}
		if err != nil {
			t.Fatalf("Tools iterator: %v", err)
		}
		names[tool.Name] = true
	}
	return names
}

// ─── shared fixtures ─────────────────────────────────────────────────────────

func makeDeviceFixture() (devs *fakeDevices, lampDev, sensorDev *device.Device) {
	devs = newFakeDevices()

	lampDev = device.New(device.Config{
		Address:   "ADDR001",
		Model:     "HmIP-PS",
		Name:      "Lamp",
		Interface: hmenum.InterfaceHmIPRF,
	})
	sensorDev = device.New(device.Config{
		Address:   "ADDR002",
		Model:     "HmIP-WTH-2",
		Name:      "Thermostat",
		Interface: hmenum.InterfaceHmIPRF,
	})

	devs.add(lampDev, "ccu1")
	devs.add(sensorDev, "ccu2")

	return devs, lampDev, sensorDev
}

// ─── tests ───────────────────────────────────────────────────────────────────

func TestListCentrals(t *testing.T) {
	deps := mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:  newFakeDevices(),
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_centrals", map[string]any{})
	if res.IsError {
		t.Fatalf("list_centrals returned error: %v", res.Content)
	}

	var out struct {
		Centrals []string `json:"centrals"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Centrals) != 2 {
		t.Fatalf("expected 2 centrals, got %d: %v", len(out.Centrals), out.Centrals)
	}
	want := map[string]bool{"ccu1": true, "ccu2": true}
	for _, name := range out.Centrals {
		if !want[name] {
			t.Errorf("unexpected central name %q", name)
		}
	}
}

func TestListDevices_AllCentrals(t *testing.T) {
	devs, _, _ := makeDeviceFixture()
	deps := mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:  devs,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_devices", map[string]any{})
	if res.IsError {
		t.Fatalf("list_devices returned error")
	}

	var out struct {
		Devices []struct {
			Address string `json:"address"`
			Central string `json:"central"`
		} `json:"devices"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Devices) != 2 {
		t.Fatalf("expected 2 devices (no filter), got %d", len(out.Devices))
	}
}

func TestListDevices_FilteredByCentral(t *testing.T) {
	devs, _, _ := makeDeviceFixture()
	deps := mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:  devs,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_devices", map[string]any{"central_name": "ccu1"})
	if res.IsError {
		t.Fatalf("list_devices (filtered) returned error")
	}

	var out struct {
		Devices []struct {
			Address string `json:"address"`
			Central string `json:"central"`
		} `json:"devices"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Devices) != 1 {
		t.Fatalf("expected 1 device for ccu1, got %d", len(out.Devices))
	}
	if out.Devices[0].Address != "ADDR001" {
		t.Errorf("expected ADDR001, got %q", out.Devices[0].Address)
	}
	if out.Devices[0].Central != "ccu1" {
		t.Errorf("expected central ccu1, got %q", out.Devices[0].Central)
	}
}

// TestListDevices_UnknownCentralReturnsError mirrors
// TestListPrograms_UnknownCentralReturnsError (tools_hub_test.go) for
// list_devices, which resolves central_name against d.Devices client-side
// rather than through centralsToScan and therefore needed its own guard.
func TestListDevices_UnknownCentralReturnsError(t *testing.T) {
	devs, _, _ := makeDeviceFixture()
	deps := mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:  devs,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "list_devices", map[string]any{"central_name": "ghost"})
	if !res.IsError {
		t.Fatal("expected IsError=true for an unknown central_name")
	}
}

func TestGetDevice_Found(t *testing.T) {
	devs, _, _ := makeDeviceFixture()
	deps := mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:  devs,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "get_device", map[string]any{"address": "ADDR001"})
	if res.IsError {
		t.Fatalf("get_device returned error")
	}

	var out struct {
		Found  bool `json:"found"`
		Device struct {
			Address   string `json:"address"`
			Model     string `json:"model"`
			Name      string `json:"name"`
			Interface string `json:"interface"`
			Central   string `json:"central"`
		} `json:"device"`
	}
	unmarshalStructured(t, res, &out)

	if !out.Found {
		t.Fatal("expected Found=true for ADDR001")
	}
	if out.Device.Address != "ADDR001" {
		t.Errorf("address: want ADDR001, got %q", out.Device.Address)
	}
	if out.Device.Model != "HmIP-PS" {
		t.Errorf("model: want HmIP-PS, got %q", out.Device.Model)
	}
	if out.Device.Name != "Lamp" {
		t.Errorf("name: want Lamp, got %q", out.Device.Name)
	}
	if out.Device.Central != "ccu1" {
		t.Errorf("central: want ccu1, got %q", out.Device.Central)
	}
}

func TestGetDevice_NotFound(t *testing.T) {
	devs, _, _ := makeDeviceFixture()
	deps := mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"ccu1"}},
		Devices:  devs,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "get_device", map[string]any{"address": "DOESNOTEXIST"})
	if res.IsError {
		t.Fatalf("get_device returned error for unknown address (expected Found=false, not an error)")
	}

	var out struct {
		Found bool `json:"found"`
	}
	unmarshalStructured(t, res, &out)

	if out.Found {
		t.Fatal("expected Found=false for unknown address")
	}
}

// TestListAudit_WithEntries connects as an admin because the tool is
// admin-gated (see TestListAudit_DeniedForViewer); it asserts the entry
// projection, not the gate.
func TestListAudit_WithEntries(t *testing.T) {
	buf := audit.NewBuffer(100)
	buf.Record(audit.Entry{
		Action:        audit.ActionDataPointWrite,
		DeviceAddress: "ADDR001",
		Parameter:     "STATE",
		Note:          "test note",
		User:          "admin",
	})

	devs, _, _ := makeDeviceFixture()
	deps := mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"ccu1"}},
		Devices:  devs,
		Audit:    buf,
	}
	cs := serveMCPAs(t, auth.Identity{Subject: "root", Role: auth.RoleAdmin, Scheme: auth.SchemeBasic}, deps)

	res := callTool(t, cs, "list_audit", map[string]any{})
	if res.IsError {
		t.Fatalf("list_audit returned error")
	}

	var out struct {
		Entries []struct {
			Action        string `json:"action"`
			DeviceAddress string `json:"device_address"`
			Parameter     string `json:"parameter"`
			Note          string `json:"note"`
			User          string `json:"user"`
		} `json:"entries"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(out.Entries))
	}
	e := out.Entries[0]
	if e.Action != string(audit.ActionDataPointWrite) {
		t.Errorf("action: want %q, got %q", audit.ActionDataPointWrite, e.Action)
	}
	if e.DeviceAddress != "ADDR001" {
		t.Errorf("device_address: want ADDR001, got %q", e.DeviceAddress)
	}
	if e.Parameter != "STATE" {
		t.Errorf("parameter: want STATE, got %q", e.Parameter)
	}
	if e.User != "admin" {
		t.Errorf("user: want admin, got %q", e.User)
	}
}

func TestSetDatapoint_NotInCatalogueWhenWritesDisabled(t *testing.T) {
	devs, _, _ := makeDeviceFixture()

	// AllowWrites=false — set_datapoint must not be registered.
	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		Devices:     devs,
		AllowWrites: false,
		Writer:      &fakeWriter{},
	}
	cs := connect(t, deps)
	defer cs.Close()

	names := toolNames(t, cs)
	if names["set_datapoint"] {
		t.Fatal("set_datapoint should not be in catalogue when AllowWrites=false")
	}
}

func TestSetDatapoint_NotInCatalogueWhenWriterNil(t *testing.T) {
	devs, _, _ := makeDeviceFixture()

	// AllowWrites=true but Writer=nil — still not registered.
	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		Devices:     devs,
		AllowWrites: true,
		Writer:      nil,
	}
	cs := connect(t, deps)
	defer cs.Close()

	names := toolNames(t, cs)
	if names["set_datapoint"] {
		t.Fatal("set_datapoint should not be in catalogue when Writer=nil")
	}
}

func TestSetDatapoint_CallErrorWhenDisabled(t *testing.T) {
	devs, _, _ := makeDeviceFixture()

	// Writes disabled — calling the tool (which won't be registered) must
	// produce either a protocol-level error or IsError.
	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		Devices:     devs,
		AllowWrites: false,
	}
	cs := connect(t, deps)
	defer cs.Close()

	_, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "set_datapoint",
		Arguments: map[string]any{"central_name": "ccu1", "address": "ADDR001", "parameter": "STATE", "value": true},
	})
	if err == nil {
		t.Fatal("expected error when calling unregistered set_datapoint")
	}
}

func TestSetDatapoint_SuccessfulWrite(t *testing.T) {
	devs, _, _ := makeDeviceFixture()
	writer := &fakeWriter{}
	buf := audit.NewBuffer(100)

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Writer:      writer,
		Audit:       buf,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "set_datapoint", map[string]any{
		"central_name": "ccu1",
		"address":      "ADDR001",
		"parameter":    "STATE",
		"value":        true,
	})
	if res.IsError {
		t.Fatalf("set_datapoint returned error: %v", res.Content)
	}

	var out struct {
		OK bool `json:"ok"`
	}
	unmarshalStructured(t, res, &out)
	if !out.OK {
		t.Fatal("expected OK=true on successful write")
	}

	// Verify the writer received the correct call.
	if writer.last.address != "ADDR001" {
		t.Errorf("writer address: want ADDR001, got %q", writer.last.address)
	}
	if writer.last.parameter != hmenum.Parameter("STATE") {
		t.Errorf("writer parameter: want STATE, got %q", writer.last.parameter)
	}
	if writer.last.priority != hmenum.CommandPriorityHigh {
		t.Errorf("writer priority: want CommandPriorityHigh, got %v", writer.last.priority)
	}

	// Verify audit entry was recorded.
	entries := buf.List(10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry after write, got %d", len(entries))
	}
	if entries[0].Action != audit.ActionDataPointWrite {
		t.Errorf("audit action: want %q, got %q", audit.ActionDataPointWrite, entries[0].Action)
	}
	if entries[0].DeviceAddress != "ADDR001" {
		t.Errorf("audit device_address: want ADDR001, got %q", entries[0].DeviceAddress)
	}
}

func TestSetDatapoint_WrongCentral(t *testing.T) {
	devs, _, _ := makeDeviceFixture()
	writer := &fakeWriter{}
	buf := audit.NewBuffer(100)

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Writer:      writer,
		Audit:       buf,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	// ADDR001 belongs to ccu1 — passing ccu2 must be refused.
	res := callTool(t, cs, "set_datapoint", map[string]any{
		"central_name": "ccu2",
		"address":      "ADDR001",
		"parameter":    "STATE",
		"value":        true,
	})
	if !res.IsError {
		t.Fatal("expected IsError=true when central_name does not own the device")
	}

	// Writer must NOT have been called.
	if writer.last.address != "" {
		t.Errorf("writer should not have been called, but got address %q", writer.last.address)
	}

	// No audit entry must have been recorded.
	if buf.Len() != 0 {
		t.Errorf("expected 0 audit entries after failed write, got %d", buf.Len())
	}
}

// ─── read_paramset ───────────────────────────────────────────────────────────

func TestReadParamset_ReturnsValues(t *testing.T) {
	ps := newFakeParamsets()
	ps.seed("ADDR001:1", hmenum.ParamsetKeyMaster, map[string]any{"MIN_SETPOINT": 5.0, "MAX_SETPOINT": 30.5})

	devs, _, _ := makeDeviceFixture()
	deps := mcp.Deps{
		Centrals:  &fakeCentrals{names: []string{"ccu1"}},
		Devices:   devs,
		Paramsets: ps,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "read_paramset", map[string]any{
		"address": "ADDR001:1",
		"key":     "MASTER",
	})
	if res.IsError {
		t.Fatalf("read_paramset returned error: %v", res.Content)
	}

	var out struct {
		Values map[string]any `json:"values"`
	}
	unmarshalStructured(t, res, &out)

	if out.Values["MIN_SETPOINT"] == nil {
		t.Error("expected MIN_SETPOINT in values")
	}
	if out.Values["MAX_SETPOINT"] == nil {
		t.Error("expected MAX_SETPOINT in values")
	}
}

func TestReadParamset_InvalidKeyErrors(t *testing.T) {
	ps := newFakeParamsets()
	devs, _, _ := makeDeviceFixture()
	deps := mcp.Deps{
		Centrals:  &fakeCentrals{names: []string{"ccu1"}},
		Devices:   devs,
		Paramsets: ps,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "read_paramset", map[string]any{
		"address": "ADDR001:1",
		"key":     "BOGUS",
	})
	if !res.IsError {
		t.Fatal("expected IsError=true for invalid paramset key")
	}
}

// ─── get_health ──────────────────────────────────────────────────────────────

func TestGetHealth_ReturnsOverallAndComponents(t *testing.T) {
	fh := &fakeHealth{
		overall: health.StatusHealthy,
		components: []health.Component{
			{Name: "ccu1-HmIP-RF", Status: health.StatusHealthy},
			{Name: "sqlite", Status: health.StatusDegraded},
		},
	}
	devs, _, _ := makeDeviceFixture()
	deps := mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"ccu1"}},
		Devices:  devs,
		Health:   fh,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "get_health", map[string]any{})
	if res.IsError {
		t.Fatalf("get_health returned error: %v", res.Content)
	}

	var out struct {
		Overall    string `json:"overall"`
		Components []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"components"`
	}
	unmarshalStructured(t, res, &out)

	if out.Overall != string(health.StatusHealthy) {
		t.Errorf("overall: want %q, got %q", health.StatusHealthy, out.Overall)
	}
	if len(out.Components) != 2 {
		t.Fatalf("expected 2 components, got %d", len(out.Components))
	}
	byName := make(map[string]string, len(out.Components))
	for _, c := range out.Components {
		byName[c.Name] = c.Status
	}
	if byName["ccu1-HmIP-RF"] != string(health.StatusHealthy) {
		t.Errorf("ccu1-HmIP-RF: want %q, got %q", health.StatusHealthy, byName["ccu1-HmIP-RF"])
	}
	if byName["sqlite"] != string(health.StatusDegraded) {
		t.Errorf("sqlite: want %q, got %q", health.StatusDegraded, byName["sqlite"])
	}
}

// ─── write_paramset ──────────────────────────────────────────────────────────

func TestWriteParamset_SuccessfulWrite(t *testing.T) {
	ps := newFakeParamsets()
	buf := audit.NewBuffer(100)
	devs, _, _ := makeDeviceFixture()

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Paramsets:   ps,
		Audit:       buf,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	writeVals := map[string]any{"MIN_SETPOINT": 10.0}
	res := callTool(t, cs, "write_paramset", map[string]any{
		"central_name": "ccu1",
		"address":      "ADDR001",
		"key":          "MASTER",
		"values":       writeVals,
	})
	if res.IsError {
		t.Fatalf("write_paramset returned error: %v", res.Content)
	}

	var out struct {
		OK bool `json:"ok"`
	}
	unmarshalStructured(t, res, &out)
	if !out.OK {
		t.Fatal("expected OK=true on successful paramset write")
	}

	// Verify PutParamset was called with the right arguments.
	if len(ps.putCalls) != 1 {
		t.Fatalf("expected 1 PutParamset call, got %d", len(ps.putCalls))
	}
	call := ps.putCalls[0]
	if call.address != "ADDR001" {
		t.Errorf("PutParamset address: want ADDR001, got %q", call.address)
	}
	if call.key != hmenum.ParamsetKeyMaster {
		t.Errorf("PutParamset key: want MASTER, got %q", call.key)
	}

	// The paramset domain behind this seam records the change itself, with
	// the per-parameter before/after pairs. The tool must not add one too:
	// a second row for the same write shows up in the change history as a
	// write that never happened, and the extra row carries no values.
	if entries := buf.List(10); len(entries) != 0 {
		t.Errorf("write_paramset recorded %d audit entries of its own; the paramset domain already records the write: %+v", len(entries), entries)
	}
}

func TestWriteParamset_WrongCentralErrors(t *testing.T) {
	ps := newFakeParamsets()
	buf := audit.NewBuffer(100)
	devs, _, _ := makeDeviceFixture()

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Paramsets:   ps,
		Audit:       buf,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	// ADDR001 belongs to ccu1 — submitting ccu2 as owner must be refused.
	res := callTool(t, cs, "write_paramset", map[string]any{
		"central_name": "ccu2",
		"address":      "ADDR001",
		"key":          "MASTER",
		"values":       map[string]any{"MIN_SETPOINT": 10.0},
	})
	if !res.IsError {
		t.Fatal("expected IsError=true when central_name does not own the device")
	}

	// PutParamset must NOT have been called.
	if len(ps.putCalls) != 0 {
		t.Errorf("PutParamset should not have been called, got %d calls", len(ps.putCalls))
	}

	// No audit entry must have been recorded.
	if buf.Len() != 0 {
		t.Errorf("expected 0 audit entries after failed write, got %d", buf.Len())
	}
}

// ─── trigger_program ─────────────────────────────────────────────────────────

// makeHubWithProgram builds a *hub.Hub containing one registered program
// whose writer records execution calls. Returns the hub and the writer.
func makeHubWithProgram(centralName, programID string) (*hub.Hub, *fakeProgramWriter) {
	writer := &fakeProgramWriter{}
	prog := hub.NewProgram(centralName, programID, "Test Program", "a test program", false, writer)
	h := hub.NewHub(centralName)
	h.PutProgram(prog)
	return h, writer
}

func TestTriggerProgram_ExecutesProgram(t *testing.T) {
	devs, _, _ := makeDeviceFixture()
	h, writer := makeHubWithProgram("ccu1", "prog-42")

	hubs := newFakeHubs()
	hubs.add("ccu1", h)

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		Devices:     devs,
		Hubs:        hubs,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "trigger_program", map[string]any{
		"central_name": "ccu1",
		"program_id":   "prog-42",
	})
	if res.IsError {
		t.Fatalf("trigger_program returned error: %v", res.Content)
	}

	var out struct {
		OK bool `json:"ok"`
	}
	unmarshalStructured(t, res, &out)
	if !out.OK {
		t.Fatal("expected OK=true on successful program trigger")
	}

	// The fake writer must have been called exactly once with the program ID.
	if len(writer.calls) != 1 {
		t.Fatalf("expected 1 ExecuteProgram call, got %d", len(writer.calls))
	}
	if writer.calls[0] != "prog-42" {
		t.Errorf("ExecuteProgram id: want %q, got %q", "prog-42", writer.calls[0])
	}
}

func TestTriggerProgram_UnknownProgramErrors(t *testing.T) {
	devs, _, _ := makeDeviceFixture()
	h := hub.NewHub("ccu1") // no programs registered

	hubs := newFakeHubs()
	hubs.add("ccu1", h)

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		Devices:     devs,
		Hubs:        hubs,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "trigger_program", map[string]any{
		"central_name": "ccu1",
		"program_id":   "does-not-exist",
	})
	if !res.IsError {
		t.Fatal("expected IsError=true for unknown program_id")
	}
}

func TestTriggerProgram_UnknownCentralErrors(t *testing.T) {
	devs, _, _ := makeDeviceFixture()

	hubs := newFakeHubs() // no centrals registered

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		Devices:     devs,
		Hubs:        hubs,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "trigger_program", map[string]any{
		"central_name": "ccu-unknown",
		"program_id":   "prog-1",
	})
	if !res.IsError {
		t.Fatal("expected IsError=true for unknown central")
	}
}

// ─── catalogue gate (AllowWrites=false) ──────────────────────────────────────

func TestCatalogue_ReadToolsPresentWriteToolsAbsentWhenWritesDisabled(t *testing.T) {
	ps := newFakeParamsets()
	fh := &fakeHealth{overall: health.StatusHealthy}
	hubs := newFakeHubs()
	devs, _, _ := makeDeviceFixture()

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		Devices:     devs,
		Paramsets:   ps,
		Health:      fh,
		Hubs:        hubs,
		AllowWrites: false,
	}
	cs := connect(t, deps)
	defer cs.Close()

	names := toolNames(t, cs)

	// Read tools must be present.
	for _, want := range []string{"read_paramset", "get_health"} {
		if !names[want] {
			t.Errorf("expected read tool %q to be in catalogue when AllowWrites=false", want)
		}
	}

	// Write tools must be absent.
	for _, absent := range []string{"write_paramset", "trigger_program"} {
		if names[absent] {
			t.Errorf("write tool %q must not be in catalogue when AllowWrites=false", absent)
		}
	}
}

// ─── channel-address ownership (writes always target channels) ───────────────

// TestWriteTools_ChannelAddressPassesOwnershipCheck pins the
// device-vs-channel ownership resolution: real writes always target a
// channel address (`ADDR:n`) while CentralOf tracks device addresses.
// The guard must strip the channel suffix before the lookup — with the
// raw channel address every write was rejected with
// `belongs to central ""`, making the write surface unusable.
func TestWriteTools_ChannelAddressPassesOwnershipCheck(t *testing.T) {
	devs, _, _ := makeDeviceFixture()
	writer := &fakeWriter{}
	ps := newFakeParamsets()

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Writer:      writer,
		Paramsets:   ps,
		Audit:       audit.NewBuffer(100),
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "set_datapoint", map[string]any{
		"central_name": "ccu1",
		"address":      "ADDR001:3",
		"parameter":    "STATE",
		"value":        true,
	})
	if res.IsError {
		t.Fatalf("set_datapoint with channel address returned error: %v", res.Content)
	}
	if writer.last.address != "ADDR001:3" {
		t.Errorf("writer must receive the channel address untouched: got %q", writer.last.address)
	}

	res = callTool(t, cs, "write_paramset", map[string]any{
		"central_name": "ccu1",
		"address":      "ADDR001:3",
		"key":          "VALUES",
		"values":       map[string]any{"STATE": true},
	})
	if res.IsError {
		t.Fatalf("write_paramset with channel address returned error: %v", res.Content)
	}

	// The wrong central must still be rejected — suffix stripping must
	// not weaken the multi-CCU guard.
	res = callTool(t, cs, "set_datapoint", map[string]any{
		"central_name": "ccu2",
		"address":      "ADDR001:3",
		"parameter":    "STATE",
		"value":        true,
	})
	if !res.IsError {
		t.Fatal("set_datapoint must reject a central that does not own the device")
	}
}

// TestHandlerRetainsNoSession pins that the mount does not accumulate state
// per connecting client. A retained session is only released by an explicit
// DELETE, which a client that simply restarts never sends: every reconnect
// would leave a session and its reader goroutine behind for the daemon's
// lifetime. Stateless serving also keeps each tool call on its own request
// context, which is what lets the admin-gated tools judge the actual caller.
func TestHandlerRetainsNoSession(t *testing.T) {
	devs, _, _ := makeDeviceFixture()
	handler := mcp.Handler(mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"ccu1"}},
		Devices:  devs,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-06-18","capabilities":{},` +
		`"clientInfo":{"name":"test","version":"1"}}}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize: status %d", resp.StatusCode)
	}
	if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
		t.Errorf("handler handed out session id %q — the session outlives the request that made it", id)
	}
}

// TestGetHealthCarriesTheGauges pins the numeric half of the health tool.
//
// An assistant driving the daemon over MCP is the caller most likely to be
// asked "why is this slow", and the three latency legs are the only thing that
// can answer it. Component status cannot: every subsystem reads "available"
// while a client sits behind a saturated tunnel. The gauges were registered on
// the health tracker and reachable over REST, but `get_health` returned only
// the component list — measurable everywhere except the surface built for the
// caller who would ask.
//
// The empty case is the negative control: a daemon with no gauges must omit
// the field rather than send an empty object, so a consumer can tell "none
// registered" from "all reading zero".
func TestGetHealthCarriesTheGauges(t *testing.T) {
	t.Parallel()

	t.Run("registered gauges reach the caller", func(t *testing.T) {
		t.Parallel()
		fh := &fakeHealth{
			overall:    health.StatusHealthy,
			components: []health.Component{{Name: "central", Status: health.StatusHealthy}},
			gauges: map[string]float64{
				"ws.heartbeat_rtt_ms": 0.79,
				"mqtt.publish_ack_ms": 3.9,
			},
		}
		out := callGetHealth(t, fh)

		if len(out.Gauges) != 2 {
			t.Fatalf("gauges = %v, want the two registered readings", out.Gauges)
		}
		if got := out.Gauges["ws.heartbeat_rtt_ms"]; got != 0.79 {
			t.Errorf("ws.heartbeat_rtt_ms = %v, want 0.79", got)
		}
		if got := out.Gauges["mqtt.publish_ack_ms"]; got != 3.9 {
			t.Errorf("mqtt.publish_ack_ms = %v, want 3.9", got)
		}
	})

	t.Run("no gauges omits the field", func(t *testing.T) {
		t.Parallel()
		fh := &fakeHealth{overall: health.StatusHealthy}
		out := callGetHealth(t, fh)

		if out.Gauges != nil {
			t.Errorf("gauges = %v for a daemon with none registered, want omitted: an empty object reads as "+
				"\"all zero\" rather than \"nothing to report\"", out.Gauges)
		}
	})
}

// callGetHealth invokes the get_health tool against fh and decodes the gauge
// half of its answer.
func callGetHealth(t *testing.T, fh *fakeHealth) struct {
	Gauges map[string]float64 `json:"gauges,omitempty"`
} {
	t.Helper()
	devs, _, _ := makeDeviceFixture()
	cs := connect(t, mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"ccu1"}},
		Devices:  devs,
		Health:   fh,
	})
	defer cs.Close()

	res := callTool(t, cs, "get_health", map[string]any{})
	if res.IsError {
		t.Fatalf("get_health returned error: %v", res.Content)
	}
	var out struct {
		Gauges map[string]float64 `json:"gauges,omitempty"`
	}
	unmarshalStructured(t, res, &out)
	return out
}
