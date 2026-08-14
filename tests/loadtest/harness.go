// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build loadtest

package loadtest

// harness.go builds the in-process daemon stack the load test drives:
// a godevccu virtual CCU, a central with its device fleet ingested via
// the real DevicePipeline, and an httptest.Server that routes the two
// hot REST paths (GET data-points, PUT value) through the production
// chi-routed handlers. No separate daemon process is spawned; the load
// generators hit the same Central → Channel → backend stack the live
// daemon uses, so the measured headroom reflects real handler cost.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/godevccu/pkg/godevccu"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// harness bundles the in-process daemon stack plus the device fleet
// the workload generators read and write against.
type harness struct {
	mock     *mockCCU
	central  *central.Unit
	registry *central.Registry
	server   *httptest.Server
	// targets is the pre-resolved set of writable VALUES data points the
	// workload picks from. Pre-resolving once keeps the per-request hot
	// loop allocation-free and avoids re-walking the fleet under load.
	targets []dpTarget
}

// dpTarget is one addressable (channel, parameter) pair the workload
// reads and writes. The REST path components are pre-split so each
// request only does string concatenation, never a fleet walk.
type dpTarget struct {
	deviceAddr string
	channelNo  int
	parameter  hmenum.Parameter
	// writable is true when the descriptor advertises the WRITE
	// operation; the write generator only targets writable rows.
	writable bool
	// writeValue is a descriptor-valid value to write. For BOOL the
	// workload toggles; this is the seed value.
	writeValue any
}

// newHarness spins up the full stack against a fleet of `models`
// godevccu device instances. The caller owns nothing — every resource
// is registered for t.Cleanup.
func newHarness(t *testing.T, models []string) *harness {
	t.Helper()

	mock := startMockCCU(t, models)

	xmlClient := newXMLRPCClient(t, mock.URL())
	caller := &xmlrpcBackendCaller{client: xmlClient}
	backend := backends.NewCcuBackend(caller, nil, nil)

	c, err := central.New(central.Config{Name: "ccu-loadtest"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	// The value writer installs per-channel writers during ingest, so the
	// PUT /value handler's ch.Set path dispatches back through this
	// CcuBackend → godevccu rather than returning ErrNoChannelWriter.
	writer := backendValueWriter{backend: backend}

	pipeline := adapter.NewDevicePipeline(c)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger := slog.New(slog.DiscardHandler)
	if err := pipeline.IngestFromBackend(ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, writer, nil, logger); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}
	if c.ModelRegistry.Len() == 0 {
		t.Fatal("godevccu returned no devices — fleet loading failed")
	}

	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}

	idx := adapter.NewDevicesAdapter(reg)
	server := newRESTServer(t, idx)

	h := &harness{
		mock:     mock,
		central:  c,
		registry: reg,
		server:   server,
		targets:  resolveTargets(c),
	}
	if len(h.targets) == 0 {
		t.Fatal("no addressable VALUES data points resolved from the fleet")
	}
	return h
}

// newRESTServer wires the two hot REST routes through the production
// chi router + handlers so the workload exercises the real handler
// chain (path-param extraction, descriptor coercion, channel dispatch).
func newRESTServer(t *testing.T, idx handlers.DeviceIndex) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	// DataPointVis = nil → expose every parameter (no visibility filter),
	// which keeps the read workload deterministic regardless of the
	// embedded visibility catalogue. Labels = nil → fall back to the
	// title-cased parameter label, exercising the same toDataPointSummary
	// serialisation the live surface runs.
	r.Get("/api/v1/devices/{addr}/channels/{no}/data-points",
		handlers.ListDataPoints(idx, nil, nil))
	r.Put("/api/v1/devices/{addr}/channels/{no}/data-points/{param}/value",
		handlers.PutDataPointValue(idx, nil))
	s := httptest.NewServer(r)
	t.Cleanup(s.Close)
	return s
}

// resolveTargets walks the fleet once and records every channel's
// parameters, splitting the REST path components and flagging writable
// rows. The workload then picks from this slice without ever touching
// the model under concurrent load.
func resolveTargets(c *central.Unit) []dpTarget {
	var out []dpTarget
	for _, d := range c.ModelRegistry.List() {
		for _, ch := range d.Channels() {
			if ch.Number < 0 {
				continue
			}
			for _, dp := range ch.DataPoints() {
				pd := dp.ParameterData()
				if !pd.IsReadable() {
					continue
				}
				t := dpTarget{
					deviceAddr: d.Address,
					channelNo:  ch.Number,
					parameter:  dp.Parameter(),
					writable:   pd.IsWritable(),
				}
				if t.writable {
					t.writeValue = seedWriteValue(pd)
				}
				out = append(out, t)
			}
		}
	}
	return out
}

// seedWriteValue returns a descriptor-VALID value the write generator
// can submit so the workload exercises the write SUCCESS path rather
// than the 400-validation path. It prefers the descriptor DEFAULT, then
// the MIN bound (e.g. SET_POINT_TEMPERATURE MIN 4.5, ACTIVE_PROFILE
// MIN 1), then a type-appropriate fallback. godevccu is a simulator —
// writes have no real-world side effect, unlike the live-CCU rule in
// CLAUDE.md.
func seedWriteValue(pd hmproto.ParameterData) any {
	switch pd.Type {
	case hmenum.ParameterTypeBool, hmenum.ParameterTypeAction:
		return false
	case hmenum.ParameterTypeFloat:
		if v, ok := rawFloat(pd.Default); ok {
			return v
		}
		if v, ok := rawFloat(pd.Min); ok {
			return v
		}
		return 0.0
	case hmenum.ParameterTypeInteger, hmenum.ParameterTypeEnum:
		if v, ok := rawInt(pd.Default); ok {
			return v
		}
		if v, ok := rawInt(pd.Min); ok {
			return v
		}
		return 0
	default:
		return 0
	}
}

// rawFloat parses a json.RawMessage numeric descriptor bound to float64.
func rawFloat(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return f, true
}

// rawInt parses a json.RawMessage numeric descriptor bound to int. CCU
// integer bounds are emitted as bare numbers; a float-encoded bound is
// truncated toward zero.
func rawInt(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return int(f), true
}

// readURL builds the GET data-points URL for a target's channel.
func (h *harness) readURL(t dpTarget) string {
	return fmt.Sprintf("%s/api/v1/devices/%s/channels/%d/data-points",
		h.server.URL, t.deviceAddr, t.channelNo)
}

// writeURL builds the PUT value URL for a single target parameter.
func (h *harness) writeURL(t dpTarget) string {
	return fmt.Sprintf("%s/api/v1/devices/%s/channels/%d/data-points/%s/value",
		h.server.URL, t.deviceAddr, t.channelNo, t.parameter)
}

// ── godevccu bring-up (self-contained; the integration package's copy
// lives behind a different build tag and cannot be imported here) ──

// mockCCU is a running godevccu virtual CCU on an OS-assigned XML-RPC
// port.
type mockCCU struct {
	v *godevccu.VirtualCCU
}

// URL returns the XML-RPC endpoint URL.
func (m *mockCCU) URL() string {
	return fmt.Sprintf("http://%s/", m.v.XMLRPCAddr().String())
}

// startMockCCU spins up a godevccu instance in Homegear mode (XML-RPC
// only, no auth) loaded with the named models. Pass nil to load the
// full embedded fleet (~399 device instances). A Cleanup stops it.
func startMockCCU(t *testing.T, devices []string) *mockCCU {
	t.Helper()
	v, err := godevccu.New(godevccu.Config{
		Mode:        godevccu.BackendModeHomegear,
		Host:        "127.0.0.1",
		XMLRPCPort:  godevccu.EphemeralPort,
		AuthEnabled: false,
		Devices:     devices,
	})
	if err != nil {
		t.Fatalf("godevccu.New: %v", err)
	}
	if err := v.Start(); err != nil {
		t.Fatalf("godevccu.Start: %v", err)
	}
	t.Cleanup(func() { _ = v.Stop() })

	addr, ok := v.XMLRPCAddr().(*net.TCPAddr)
	if !ok || addr == nil || addr.Port == 0 {
		_ = v.Stop()
		t.Fatalf("godevccu: ephemeral XML-RPC port not resolved: %v", v.XMLRPCAddr())
	}
	return &mockCCU{v: v}
}

// newXMLRPCClient builds an XML-RPC client pointed at the simulator.
func newXMLRPCClient(t *testing.T, url string) *xmlrpc.Client {
	t.Helper()
	c, err := xmlrpc.NewClient(xmlrpc.Config{
		URL:       url,
		Interface: "HmIP-RF",
	})
	if err != nil {
		t.Fatalf("xmlrpc.NewClient: %v", err)
	}
	return c
}

// backendValueWriter satisfies adapter.ValueWriter and routes single-DP
// writes back through the CcuBackend that godevccu owns. Production
// wires this through the InterfaceClient stack; for the harness the
// backend hop is enough to exercise the write path.
type backendValueWriter struct {
	backend *backends.CcuBackend
}

func (w backendValueWriter) SetValue(
	ctx context.Context,
	_ string, // central name (production routing dimension, irrelevant here)
	_ string, // interface id (CcuBackend.SetValue ignores it)
	channelAddress string,
	parameter hmenum.Parameter,
	value any,
	priority hmenum.CommandPriority,
) error {
	return w.backend.SetValue(ctx, channelAddress, parameter, value, priority, hmenum.CommandRxModeUnset)
}

// xmlrpcBackendCaller bridges the XML-RPC client to backends.Caller,
// normalising the XML-RPC Value into plain Go types so the backend can
// reflect over maps/slices/scalars.
type xmlrpcBackendCaller struct{ client *xmlrpc.Client }

// CallAt satisfies the priority-carrying half of [backends.Caller]. The
// load harness drives XML-RPC, which has no wire representation for a
// command priority: the priority steers the reliability stack in front
// of the transport, not the request itself, so the harness records it
// nowhere and forwards the call unchanged. The method is required
// rather than optional on purpose — an optional one falls back
// silently, which is how a dropped priority stayed invisible before.
func (c *xmlrpcBackendCaller) CallAt(
	ctx context.Context, _ hmenum.CommandPriority, method string, args ...any,
) (any, error) {
	return c.Call(ctx, method, args...)
}

func (c *xmlrpcBackendCaller) Call(ctx context.Context, method string, args ...any) (any, error) {
	params := make([]xmlrpc.Value, 0, len(args))
	for _, arg := range args {
		v, err := goToXMLRPCValue(arg)
		if err != nil {
			return nil, fmt.Errorf("arg to xmlrpc: %w", err)
		}
		params = append(params, v)
	}
	reply, err := c.client.Call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	return xmlRPCValueToGo(reply), nil
}

func goToXMLRPCValue(v any) (xmlrpc.Value, error) {
	switch x := v.(type) {
	case nil:
		return xmlrpc.NilValue{}, nil
	case string:
		return xmlrpc.StringValue(x), nil
	case int:
		return xmlrpc.IntValue(x), nil //nolint:gosec // test-scope range
	case int32:
		return xmlrpc.IntValue(x), nil
	case bool:
		return xmlrpc.BoolValue(x), nil
	case float64:
		return xmlrpc.DoubleValue(x), nil
	case []string:
		out := make(xmlrpc.ArrayValue, 0, len(x))
		for _, s := range x {
			out = append(out, xmlrpc.StringValue(s))
		}
		return out, nil
	case map[string]any:
		members := make([]xmlrpc.Member, 0, len(x))
		for k, val := range x {
			sub, err := goToXMLRPCValue(val)
			if err != nil {
				return nil, fmt.Errorf("struct member %q: %w", k, err)
			}
			members = append(members, xmlrpc.Member{Name: k, Value: sub})
		}
		return xmlrpc.StructValue{Members: members}, nil
	case []any:
		out := make(xmlrpc.ArrayValue, 0, len(x))
		for _, e := range x {
			sub, err := goToXMLRPCValue(e)
			if err != nil {
				return nil, err
			}
			out = append(out, sub)
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported arg %T", v)
}

func xmlRPCValueToGo(v xmlrpc.Value) any {
	switch x := v.(type) {
	case xmlrpc.NilValue:
		return nil
	case xmlrpc.StringValue:
		return string(x)
	case xmlrpc.IntValue:
		return int(x)
	case xmlrpc.BoolValue:
		return bool(x)
	case xmlrpc.DoubleValue:
		return float64(x)
	case xmlrpc.ArrayValue:
		out := make([]any, 0, len(x))
		for _, e := range x {
			out = append(out, xmlRPCValueToGo(e))
		}
		return out
	case xmlrpc.StructValue:
		out := make(map[string]any, len(x.Members))
		for _, m := range x.Members {
			out[m.Name] = xmlRPCValueToGo(m.Value)
		}
		return out
	}
	return nil
}
