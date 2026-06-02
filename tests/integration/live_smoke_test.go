// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration_live

// Package integration — live smoke tests against a real HmIP CCU.
//
// # Running the tests
//
// These tests require a reachable Homematic CCU. They are NOT part of
// the normal `make test` or `make integration` gates (which run against
// the in-process godevccu simulator). Run them explicitly with:
//
//	go test -tags=integration_live -timeout=120s \
//	 ./tests/integration/... -run TestLive -v
//
// # Required environment variables
//
//	OPENCCU_LOOM_LIVE_CCU_HOST hostname or IP of the CCU (e.g. 192.168.1.10)
//	OPENCCU_LOOM_LIVE_CCU_USER CCU admin username (may be empty for open CCUs)
//	OPENCCU_LOOM_LIVE_CCU_PASS CCU admin password (may be empty for open CCUs)
//
// All three variables are read; if OPENCCU_LOOM_LIVE_CCU_HOST is empty every
// TestLive* test is skipped so `go test` exits 0 in CI without a CCU.
//
// # Scope
//
// The suite boots the openccu-loom pipeline against the live CCU over
// the HmIP-RF interface (XML-RPC, port 2010) and verifies the fix-wave
// invariants:
//
// - : ProductGroup populated on every device (classification).
// - : SchemaVersion >= 1 on the majority of devices.
// - : Lock devices expose ERROR / ERROR_JAMMED data points.
// - : MQTT Discovery payloads for switch / lock use state_on="true"
// and value_template with `| lower`.
//
// No writes are performed; the tests are read-only.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─────────────────────────────────────────────────────────────────────────────
// Shared live-CCU fixture
// ─────────────────────────────────────────────────────────────────────────────

// liveCCUEnv holds the env-var gate values.
type liveCCUEnv struct {
	host string
	user string //nolint:unused // reserved for JSON-RPC auth in future tests
	pass string //nolint:unused // reserved for JSON-RPC auth in future tests
}

// checkLiveCCU reads the required env vars and skips the test if any are
// absent.
func checkLiveCCU(t *testing.T) liveCCUEnv {
	t.Helper()
	host := os.Getenv("OPENCCU_LOOM_LIVE_CCU_HOST")
	if host == "" {
		t.Skip("set OPENCCU_LOOM_LIVE_CCU_HOST to enable live-smoke")
	}
	user := os.Getenv("OPENCCU_LOOM_LIVE_CCU_USER")
	pass := os.Getenv("OPENCCU_LOOM_LIVE_CCU_PASS")
	return liveCCUEnv{host: host, user: user, pass: pass}
}

// liveCCUXMLRPCURL returns the XML-RPC URL for the HmIP-RF interface
// on a real CCU (default port 2010).
func liveCCUXMLRPCURL(host string) string {
	return "http://" + host + ":2010/"
}

// buildLivePipeline connects to the live CCU, ingests devices, and
// returns the populated Unit. It is the shared setup step for
// all TestLive_* sub-tests that need the full device graph.
func buildLivePipeline(t *testing.T, env liveCCUEnv) *central.Unit {
	t.Helper()

	xmlClient, err := xmlrpc.NewClient(xmlrpc.Config{
		URL:       liveCCUXMLRPCURL(env.host),
		Interface: "HmIP-RF",
	})
	if err != nil {
		t.Fatalf("xmlrpc.NewClient: %v", err)
	}

	// liveBackendCaller bridges the xmlrpc.Client to the backends.Caller
	// interface inline so the live test does not depend on the
	// xmlrpcBackendCaller type defined under the `integration` tag.
	caller := &liveBECaller{client: xmlClient}
	backend := backends.NewCcuBackend(caller, nil, nil)

	c, err := central.New(central.Config{Name: "live-ccu"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	pipeline := adapter.NewDevicePipeline(c)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := pipeline.IngestFromBackend(ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, logger); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}
	return c
}

// liveBECaller is a self-contained xmlrpc.Client → backends.Caller bridge
// for the live tests. It mirrors the xmlrpcBackendCaller from
// device_graph_test.go (integration tag) without sharing its definition.
type liveBECaller struct{ client *xmlrpc.Client }

func (c *liveBECaller) Call(ctx context.Context, method string, args ...any) (any, error) {
	params := make([]xmlrpc.Value, 0, len(args))
	for _, arg := range args {
		v, err := liveGoToXMLRPC(arg)
		if err != nil {
			return nil, fmt.Errorf("arg to xmlrpc: %w", err)
		}
		params = append(params, v)
	}
	reply, err := c.client.Call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	return liveXMLRPCToGo(reply), nil
}

func liveGoToXMLRPC(v any) (xmlrpc.Value, error) {
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
	}
	return nil, fmt.Errorf("unsupported arg type %T", v)
}

func liveXMLRPCToGo(v xmlrpc.Value) any {
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
			out = append(out, liveXMLRPCToGo(e))
		}
		return out
	case xmlrpc.StructValue:
		out := make(map[string]any, len(x.Members))
		for _, m := range x.Members {
			out[m.Name] = liveXMLRPCToGo(m.Value)
		}
		return out
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// TestLive_DeviceList — basic ingest sanity (W1 baseline)
// ─────────────────────────────────────────────────────────────────────────────

// TestLive_DeviceList verifies that IngestFromBackend against the live CCU
// returns at least one device and that every device has Address, Model, and
// InterfaceID populated.
func TestLive_DeviceList(t *testing.T) {
	env := checkLiveCCU(t)
	c := buildLivePipeline(t, env)

	devices := c.ModelRegistry.List()
	if len(devices) == 0 {
		t.Fatal("IngestFromBackend returned 0 devices against live CCU")
	}
	t.Logf("device count: %d", len(devices))

	for _, d := range devices {
		if d.Address == "" {
			t.Errorf("device %q: Address is empty", d.Model)
		}
		if d.Model == "" {
			t.Errorf("device at address %q: Model is empty", d.Address)
		}
		if d.InterfaceID == "" {
			t.Errorf("device %s: InterfaceID is empty", d.Address)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestLive_DeviceClassification — : ProductGroup populated
// ─────────────────────────────────────────────────────────────────────────────

// TestLive_DeviceClassification checks that every device received from the
// live CCU has a non-empty ProductGroup. An empty or "unknown" ProductGroup
// means the classification fix regressed.
func TestLive_DeviceClassification(t *testing.T) {
	env := checkLiveCCU(t)
	c := buildLivePipeline(t, env)

	devices := c.ModelRegistry.List()
	if len(devices) == 0 {
		t.Fatal("no devices to classify")
	}

	var unknown []string
	for _, d := range devices {
		if d.ProductGroup == "" || d.ProductGroup == hmenum.ProductGroupUnknown {
			unknown = append(unknown, d.Address+"("+d.Model+")")
		}
	}
	if len(unknown) > 0 {
		t.Errorf("W5-B regression: %d device(s) have ProductGroup=unknown: %v", len(unknown), unknown)
	}
	t.Logf("classification OK: %d devices, 0 unknown", len(devices))
}

// ─────────────────────────────────────────────────────────────────────────────
// TestLive_DeviceVersion — : SchemaVersion populated on most devices
// ─────────────────────────────────────────────────────────────────────────────

// TestLive_DeviceVersion asserts that at least 50 % of devices have a
// non-zero SchemaVersion. A CCU that returns valid listDevices data should
// always carry VERSION fields; if all are 0 the ingest pipeline regressed.
func TestLive_DeviceVersion(t *testing.T) {
	env := checkLiveCCU(t)
	c := buildLivePipeline(t, env)

	devices := c.ModelRegistry.List()
	if len(devices) == 0 {
		t.Fatal("no devices")
	}

	var nonzero int
	for _, d := range devices {
		if d.SchemaVersion > 0 {
			nonzero++
		}
	}
	pct := nonzero * 100 / len(devices)
	t.Logf("SchemaVersion > 0: %d/%d (%d%%)", nonzero, len(devices), pct)
	if pct < 50 {
		t.Errorf("W5-B regression: only %d%% of devices have SchemaVersion > 0 (threshold: 50%%)", pct)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestLive_LockERRORDPCreated — : lock error DPs exist
// ─────────────────────────────────────────────────────────────────────────────

// lockModelPrefixes is the set of device model prefixes that must expose an
// ERROR or ERROR_JAMMED data point after the fix.
var lockModelPrefixes = []string{
	"HM-Sec-Key",
	"HM-Sec-Win",
	"HmIP-DLD",
	"HmIP-DLP",
}

// isLockModel reports whether model starts with any known lock prefix.
func isLockModel(model string) bool {
	for _, prefix := range lockModelPrefixes {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

// TestLive_LockERRORDPCreated verifies that whenever a lock device
// (HM-Sec-Key, HM-Sec-Win, HmIP-DLD, HmIP-DLP) is present in the live
// inventory, at least one of its channels exposes an ERROR or ERROR_JAMMED
// data point. This pins the fix that materialised those DPs.
//
// The test is skipped (not failed) when none of the lock models appear in
// the live inventory so the suite stays green on CCUs that have no locks.
func TestLive_LockERRORDPCreated(t *testing.T) {
	env := checkLiveCCU(t)
	c := buildLivePipeline(t, env)

	devices := c.ModelRegistry.List()
	var lockDevices []*device.Device
	for _, d := range devices {
		if isLockModel(d.Model) {
			lockDevices = append(lockDevices, d)
		}
	}

	if len(lockDevices) == 0 {
		t.Skip("no lock devices (HM-Sec-Key/Win, HmIP-DLD/DLP) in live inventory — skipping W6-A pin")
	}

	t.Logf("found %d lock device(s)", len(lockDevices))
	var missingError []string

	for _, d := range lockDevices {
		hasErrorDP := false
		for _, ch := range d.Channels() {
			if ch == nil {
				continue
			}
			if ch.Parameter(hmenum.ParameterError) != nil || ch.Parameter(hmenum.ParameterErrorJammed) != nil {
				hasErrorDP = true
				break
			}
		}
		if !hasErrorDP {
			missingError = append(missingError, d.Address+"("+d.Model+")")
		}
	}

	if len(missingError) > 0 {
		t.Errorf("W6-A regression: %d lock device(s) missing ERROR/ERROR_JAMMED DP: %v",
			len(missingError), missingError)
	} else {
		t.Logf("W6-A OK: all %d lock device(s) have ERROR/ERROR_JAMMED DP", len(lockDevices))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestLive_DiscoverySnapshot — : switch/lock discovery payload shape
// ─────────────────────────────────────────────────────────────────────────────

// liveDiscoveryRecorder captures homeassistant/.../config publishes.
type liveDiscoveryRecorder struct {
	byTopic map[string][]byte
}

func newLiveDiscoveryRecorder() *liveDiscoveryRecorder {
	return &liveDiscoveryRecorder{byTopic: make(map[string][]byte, 512)}
}

func (r *liveDiscoveryRecorder) Publish(_ context.Context, topic string, payload []byte, _ mqtt.QoS, _ bool) error {
	if !strings.HasPrefix(topic, "homeassistant/") || !strings.HasSuffix(topic, "/config") {
		return nil
	}
	if _, exists := r.byTopic[topic]; exists {
		return nil
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	r.byTopic[topic] = cp
	return nil
}

// payloads returns all captured payloads keyed by their topic component
// (the second path segment, e.g. "switch", "lock").
func (r *liveDiscoveryRecorder) payloads() map[string][]map[string]any {
	out := make(map[string][]map[string]any)
	for topic, raw := range r.byTopic {
		parts := strings.Split(topic, "/")
		if len(parts) < 2 {
			continue
		}
		component := parts[1]
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			continue
		}
		out[component] = append(out[component], body)
	}
	return out
}

// liveDriveChannelDPs drives every DP on ch through the bridge so Discovery
// payloads are captured. It is a simplified version of driveChannelDPs from
// discovery_snapshot_test.go (integration tag), without the snapshot-schema
// encoding. It produces the same MQTT publish side-effects.
func liveDriveChannelDPs(ctx context.Context, bridge *mqtt.Bridge, d *device.Device, ch *device.Channel) {
	common := mqtt.Event{
		Central:        "live-ccu",
		Interface:      string(d.Interface),
		DeviceAddress:  d.Address,
		DeviceName:     d.Name,
		Model:          d.Model,
		ChannelNo:      ch.Number,
		ChannelAddress: ch.Address,
		ChannelType:    ch.Type,
		Channel:        ch,
		Device:         d,
	}
	for _, dp := range ch.DataPoints() {
		desc := dp.ParameterData()
		ev := common
		ev.Parameter = string(dp.Parameter())
		ev.Paramset = hmenum.ParamsetKeyValues
		ev.Writable = desc.IsWritable()
		ev.ValueList = append([]string(nil), desc.ValueList...)
		ev.WireType = desc.Type
		_ = bridge.PublishState(ctx, ev)
	}
}

// TestLive_DiscoverySnapshot drives every device through the MQTT bridge with
// HA-Discovery enabled and verifies that:
// - Switch entities have state_on = "true" (lowercase, not "True").
// - Switch and lock entities use a value_template containing `| lower`.
//
// This pins the round-trip fix that prevented HA from matching the
// state because the raw CCU boolean value "True"/"False" was not lower-cased.
func TestLive_DiscoverySnapshot(t *testing.T) {
	env := checkLiveCCU(t)
	c := buildLivePipeline(t, env)

	devices := c.ModelRegistry.List()
	if len(devices) == 0 {
		t.Fatal("no devices")
	}

	rec := newLiveDiscoveryRecorder()

	bridgeCfg := mqtt.BridgeConfig{
		Base:               "gh",
		CentralName:        "live-ccu",
		RawEnabled:         false,
		HADiscoveryEnabled: true,
		PayloadFormat:      mqtt.PayloadFormatJSON,
	}
	bridge := mqtt.NewBridge(bridgeCfg, rec)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	for _, d := range devices {
		for _, ch := range d.Channels() {
			if ch == nil {
				continue
			}
			liveDriveChannelDPs(ctx, bridge, d, ch)
		}
	}

	byComponent := rec.payloads()
	switchPayloads := byComponent["switch"]
	lockPayloads := byComponent["lock"]

	t.Logf("discovery: switch=%d lock=%d (total captured=%d)",
		len(switchPayloads), len(lockPayloads), len(rec.byTopic))

	if len(switchPayloads) == 0 && len(lockPayloads) == 0 {
		t.Skip("no switch or lock entities discovered — CCU may have no HA-mappable switch/lock devices")
	}

	// pin: switch payload must have state_on="true" and value_template
	// containing | lower.
	for i, body := range switchPayloads {
		if stateOn, ok := body["state_on"].(string); ok {
			if stateOn != "true" {
				t.Errorf("W6-B regression: switch[%d] state_on=%q want \"true\"", i, stateOn)
			}
		}
		if vt, ok := body["value_template"].(string); ok {
			if !strings.Contains(vt, "| lower") {
				t.Errorf("W6-B regression: switch[%d] value_template=%q missing '| lower'", i, vt)
			}
		}
	}

	// pin: lock entities must use value_template with | lower.
	for i, body := range lockPayloads {
		if vt, ok := body["value_template"].(string); ok {
			if !strings.Contains(vt, "| lower") {
				t.Errorf("W6-B regression: lock[%d] value_template=%q missing '| lower'", i, vt)
			}
		}
	}
}
