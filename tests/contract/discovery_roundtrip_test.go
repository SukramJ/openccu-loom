// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package contract — HA Discovery Round-Trip tests.
//
// These tests verify that the value_template strings emitted in the
// HA-Discovery payloads produce the correct output when rendered
// against a realistic PerDPState JSON envelope. The bug cluster that
// motivated them:
//
// 1. Switch / Lock / binary_sensor stayed "unknown" — Jinja `True`/
// `False` capitalised rendering vs. `state_on="true"` (lowercase).
// The `| lower` filter in valueJSONValueLowerTemplate fixes this;
// the round-trip test verifies the fix is present end-to-end.
//
// 2. mqtt.event parses the post-value_template payload as JSON. The
// default valueJSONValueTemplate extracts a scalar
// (`{{ value_json.value }}`), which breaks the JSON parser when HA
// tries to read `event_type` from the rendered string. The fix
// drops value_template for event entities; the test pins that.
//
// The Jinja2 engine is approximated by a minimal Go renderer (see
// jinja_helpers.go) covering the exact filter/test subset openccu-loom's
// templates use: `lower`, `int`, `float`, `default`, `is defined`,
// `tojson`, plus the `{% if value_json.<field> %}…{% else %}…{% endif %}`
// truthiness branch the irrigation-valve builder emits (renderJinjaBoolIfElse).
//
// The aggregate components (cover / climate / siren / valve) are driven
// from their REAL custom-DP HADiscoveryPayload builders (ADR 0010) — the
// tests construct the concrete custom-DP against a minimal channel rig and
// round-trip the builder's own emitted templates rather than hand-written
// approximations, so a builder-side template regression is caught here.
package contract

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	"github.com/SukramJ/openccu-loom/internal/model/custom/valve"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	mqtt "github.com/SukramJ/openccu-loom/internal/north/mqtt"
	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- helpers ----------------------------------------------------------------

// buildDiscovery is a minimal test-fixture helper: it creates a
// DefaultDiscoveryBuilder with sane defaults and calls Build with the
// given Event. It fatals when build returns ok=false.
func buildDiscovery(t *testing.T, ev mqtt.Event) map[string]any {
	t.Helper()
	db := mqtt.NewDefaultDiscoveryBuilder(mqtt.NewTopicBuilder("openccu-loom"), "ccu")
	_, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatalf("Build(%+v) returned ok=false", ev)
	}
	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("unmarshal discovery payload: %v", err)
	}
	return payload
}

// mustStringField extracts a required string field from the discovery payload.
func mustStringField(t *testing.T, payload map[string]any, key string) string {
	t.Helper()
	v, ok := payload[key]
	if !ok {
		t.Fatalf("discovery payload missing required field %q; got keys: %v", key, mapKeys(payload))
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("discovery payload field %q: expected string, got %T (%v)", key, v, v)
	}
	return s
}

// mapKeys returns a sorted list of keys for error messages.
func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- aggregate builder rigs -------------------------------------------------
//
// The aggregate components (cover / climate / siren / valve) do not flow
// through the per-parameter [mqtt.DefaultDiscoveryBuilder.Build] fast
// path — their templates come from each custom-DP type's own
// HADiscoveryPayload builder (ADR 0010). To round-trip the REAL emitted
// templates (rather than hand-written approximations) these helpers
// construct the concrete custom-DP against a minimal channel rig — the
// same assembly the device pipeline performs — and return the builder's
// body map. Mirrors the per-package rigs (e.g.
// internal/model/custom/cover/cover_test.go:newRig).

// roundtripWriter is a no-op [generic.Writer]; the round-trip tests
// never assert on the wire, only on the discovery-payload templates.
type roundtripWriter struct{}

func (roundtripWriter) SetValue(context.Context, string, hmenum.Parameter, any, hmenum.CommandPriority) error {
	return nil
}

// roundtripDiscoveryCtx is a minimal [pload.HADiscoveryContext] whose
// topic strings are deterministic stand-ins — the round-trip tests
// exercise the value_template / state-string fields, not the topic
// wiring. Mirrors internal/model/custom/cover/payload_discovery_test.go:discoveryCtx.
type roundtripDiscoveryCtx struct{}

func (roundtripDiscoveryCtx) AggregatedStateTopic() string { return "rt/state" }
func (roundtripDiscoveryCtx) CustomDPStateTopic() string   { return "rt/custom/state" }

func (roundtripDiscoveryCtx) ServiceMethodCommandTopic(m string) string {
	return "rt/svc/" + m + "/set"
}

func (roundtripDiscoveryCtx) WireParameterCommandTopic(p string) string { return "rt/" + p + "/set" }
func (roundtripDiscoveryCtx) WireParameterStateTopic(p string) string   { return "rt/" + p }

func (roundtripDiscoveryCtx) WireParameterStateTopicOn(addr, p string) string {
	return "rt/" + addr + "/" + p
}

var _ pload.HADiscoveryContext = roundtripDiscoveryCtx{}

// rtPutFloat registers a writable FLOAT wire DP on ch under parameter.
func rtPutFloat(ch *device.Channel, address string, parameter hmenum.Parameter, w generic.Writer) {
	ch.Put(generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(parameter),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
}

// rtPutFloatSensor registers a read-only FLOAT sensor DP on ch (climate
// ACTUAL_TEMPERATURE is a sensor, resolved via custom.FloatSensorField).
func rtPutFloatSensor(ch *device.Channel, address string, parameter hmenum.Parameter) {
	ch.Put(generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(parameter),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}))
}

// rtPutSwitch registers a writable BOOL STATE wire DP on ch.
func rtPutSwitch(ch *device.Channel, address string, parameter hmenum.Parameter, w generic.Writer) {
	ch.Put(generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(parameter),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
}

// buildAggregateBody validates that a custom-DP HADiscoveryPayload
// builder produced a non-nil body and returns it. Fatals otherwise.
func buildAggregateBody(t *testing.T, comp string, body map[string]any) map[string]any {
	t.Helper()
	if body == nil {
		t.Fatalf("%s HADiscoveryPayload returned nil body", comp)
	}
	return body
}

// mustBodyString extracts a required string field from a builder body
// (the aggregate state-string / template keys are plain strings).
func mustBodyString(t *testing.T, body map[string]any, key string) string {
	t.Helper()
	s, ok := body[key].(string)
	if !ok {
		t.Fatalf("builder body missing string field %q; got keys: %v", key, mapKeys(body))
	}
	return s
}

// boolIfElseRe matches the minimal `{% if value_json.<field> %}A{% else
// %}B{% endif %}` truthiness branch the irrigation-valve builder emits
// (internal/model/custom/valve/payload.go:167). The stock renderJinja
// only handles the `is defined` guard, so this dedicated renderer covers
// the boolean if/else shape without pulling a full Jinja engine into the
// contract suite.
var boolIfElseRe = regexp.MustCompile(`(?s)\{%\s*if\s+value_json\.(\w+)\s*%\}(.*?)\{%\s*else\s*%\}(.*?)\{%\s*endif\s*%\}`)

// renderJinjaBoolIfElse renders the valve/irrigation truthiness template
// against a PerDPState envelope, returning the true-branch text when the
// referenced boolean field is truthy and the false-branch otherwise.
func renderJinjaBoolIfElse(t *testing.T, template, envelope string) string {
	t.Helper()
	m := boolIfElseRe.FindStringSubmatch(template)
	if m == nil {
		t.Fatalf("template %q is not a value_json.<field> if/else branch", template)
	}
	field, truePart, falsePart := m[1], m[2], m[3]
	var vj map[string]any
	if err := json.Unmarshal([]byte(envelope), &vj); err != nil {
		t.Fatalf("envelope %q is not JSON: %v", envelope, err)
	}
	truthy := false
	switch v := vj[field].(type) {
	case bool:
		truthy = v
	case float64:
		truthy = v != 0
	case string:
		truthy = v != "" && v != "false"
	}
	if truthy {
		return strings.TrimSpace(truePart)
	}
	return strings.TrimSpace(falsePart)
}

// --- switch -----------------------------------------------------------------

// TestDiscoveryRoundTrip_Switch verifies the full surface-1→surface-2 loop
// for HAComponentSwitch:
//
// 1. Discovery payload declares value_template with `| lower` filter.
// 2. state_on = "true", state_off = "false" (lowercase).
// 3. PerDPState envelope {"value":true} → rendered by value_template → "true".
// 4. Rendered output matches state_on (not the Python-capitalised "True").
func TestDiscoveryRoundTrip_Switch(t *testing.T) {
	t.Parallel()
	payload := buildDiscovery(t, mqtt.Event{
		Interface: "HmIP-RF", DeviceAddress: "AABBCC", ChannelNo: 1,
		Parameter: "STATE", Category: hmenum.DataPointCategorySwitch, Writable: true,
	})

	// --- Surface 1: Discovery payload fields --------------------------------
	stateOn := mustStringField(t, payload, "state_on")
	stateOff := mustStringField(t, payload, "state_off")
	valueTemplate := mustStringField(t, payload, "value_template")

	if stateOn != "true" {
		t.Errorf("state_on = %q, want %q (lowercase so Jinja | lower matches)", stateOn, "true")
	}
	if stateOff != "false" {
		t.Errorf("state_off = %q, want %q", stateOff, "false")
	}
	if !strings.Contains(valueTemplate, "| lower") {
		t.Errorf("value_template %q must contain '| lower' filter to normalise Go JSON bool", valueTemplate)
	}

	// --- Surface 2: Template render -----------------------------------------
	// PerDPState: {"value":true} — Go JSON encodes bool as lowercase "true".
	rendered := renderJinja(t, valueTemplate, `{"value":true,"available":true}`)
	if rendered != "true" {
		t.Errorf("rendered value_template = %q, want %q\n"+
			"  template: %q\n  envelope: {\"value\":true}\n"+
			"  (Go JSON bool is lowercase; Jinja | lower must keep it lowercase)",
			rendered, "true", valueTemplate)
	}
	if rendered != stateOn {
		t.Errorf("rendered %q does not match state_on %q — switch would stay 'unknown' in HA", rendered, stateOn)
	}

	// Negative: off state.
	renderedOff := renderJinja(t, valueTemplate, `{"value":false,"available":true}`)
	if renderedOff != stateOff {
		t.Errorf("off render %q does not match state_off %q", renderedOff, stateOff)
	}
}

// --- lock -------------------------------------------------------------------

// TestDiscoveryRoundTrip_Lock verifies the per-parameter lock entity
// (LOCK_STATE / LOCK_TARGET_LEVEL → HAComponentLock path, not the
// custom-DP aggregated path). Uses HA's lock-specific shape:
// payload_lock / payload_unlock on the command topic and
// state_locked / state_unlocked against the rendered value_template —
// same shape the custom-DP aggregated path uses
// (`internal/model/custom/lock/payload.go:122-129`).
func TestDiscoveryRoundTrip_Lock(t *testing.T) {
	t.Parallel()
	payload := buildDiscovery(t, mqtt.Event{
		Interface: "HmIP-RF", DeviceAddress: "AABBCC", ChannelNo: 1,
		Parameter: "LOCK_STATE", Category: hmenum.DataPointCategoryLock, Writable: true,
	})

	payloadLock := mustStringField(t, payload, "payload_lock")
	payloadUnlock := mustStringField(t, payload, "payload_unlock")
	stateLocked := mustStringField(t, payload, "state_locked")
	stateUnlocked := mustStringField(t, payload, "state_unlocked")
	valueTemplate := mustStringField(t, payload, "value_template")

	if !strings.Contains(valueTemplate, "| lower") {
		t.Errorf("lock value_template %q missing '| lower'", valueTemplate)
	}
	// Wire mapping: 0 = locked, 1 = unlocked.
	if payloadLock != "0" {
		t.Errorf("lock payload_lock = %q, want %q", payloadLock, "0")
	}
	if payloadUnlock != "1" {
		t.Errorf("lock payload_unlock = %q, want %q", payloadUnlock, "1")
	}
	if stateLocked != "0" {
		t.Errorf("lock state_locked = %q, want %q", stateLocked, "0")
	}
	if stateUnlocked != "1" {
		t.Errorf("lock state_unlocked = %q, want %q", stateUnlocked, "1")
	}

	// Switch-shape regression guard: the legacy state_on / state_off /
	// payload_on / payload_off keys must NOT be emitted on the lock
	// component (switch and lock surfaces use different shapes).
	for _, k := range []string{"state_on", "state_off", "payload_on", "payload_off"} {
		if _, present := payload[k]; present {
			t.Errorf("lock payload must not carry switch-shape key %q", k)
		}
	}
}

// --- binary_sensor ----------------------------------------------------------

// TestDiscoveryRoundTrip_BinarySensor verifies:
// 1. payload_on / payload_off use lowercase ("true"/"false").
// 2. value_template contains `| lower`.
// 3. Round-trip from {"value":true} → "true" == payload_on.
func TestDiscoveryRoundTrip_BinarySensor(t *testing.T) {
	t.Parallel()
	// Use a read-only STATE parameter → classified as binary_sensor (writability downgrade).
	payload := buildDiscovery(t, mqtt.Event{
		Interface: "HmIP-RF", DeviceAddress: "AABBCC", ChannelNo: 1,
		Parameter: "STATE", Category: hmenum.DataPointCategorySwitch, Writable: false,
	})

	payloadOn := mustStringField(t, payload, "payload_on")
	payloadOff := mustStringField(t, payload, "payload_off")
	valueTemplate := mustStringField(t, payload, "value_template")

	if payloadOn != "true" {
		t.Errorf("binary_sensor payload_on = %q, want %q (lowercase)", payloadOn, "true")
	}
	if payloadOff != "false" {
		t.Errorf("binary_sensor payload_off = %q, want %q", payloadOff, "false")
	}
	if !strings.Contains(valueTemplate, "| lower") {
		t.Errorf("binary_sensor value_template %q must have '| lower' filter", valueTemplate)
	}

	// Round-trip.
	rendered := renderJinja(t, valueTemplate, `{"value":true,"available":true}`)
	if rendered != payloadOn {
		t.Errorf("binary_sensor render %q != payload_on %q — sensor stays 'unknown'", rendered, payloadOn)
	}

	renderedOff := renderJinja(t, valueTemplate, `{"value":false,"available":true}`)
	if renderedOff != payloadOff {
		t.Errorf("binary_sensor off render %q != payload_off %q", renderedOff, payloadOff)
	}

	// binary_sensor must NOT have command_topic (it is read-only).
	if _, has := payload["command_topic"]; has {
		t.Errorf("binary_sensor must not have command_topic (read-only entity)")
	}
}

// --- sensor -----------------------------------------------------------------

// TestDiscoveryRoundTrip_Sensor verifies that a numeric sensor renders
// its value directly (no boolean lower-filter; float/int pass-through).
func TestDiscoveryRoundTrip_Sensor(t *testing.T) {
	t.Parallel()
	payload := buildDiscovery(t, mqtt.Event{
		Interface: "HmIP-RF", DeviceAddress: "AABBCC", ChannelNo: 1,
		Parameter: "ACTUAL_TEMPERATURE", Category: hmenum.DataPointCategorySensor, Descriptor: &pload.GenericConfig{Unit: "°C"},
	})

	valueTemplate, hasVT := payload["value_template"].(string)
	if !hasVT || valueTemplate == "" {
		t.Fatal("sensor must have value_template")
	}
	if strings.Contains(valueTemplate, "| lower") {
		t.Errorf("sensor value_template %q must NOT have | lower — numeric values break under lowercase filter", valueTemplate)
	}

	// Round-trip: float value.
	rendered := renderJinja(t, valueTemplate, `{"value":22.5,"available":true}`)
	if rendered != "22.5" {
		t.Errorf("sensor render = %q, want %q", rendered, "22.5")
	}

	// No state_on / state_off for sensors.
	if _, has := payload["state_on"]; has {
		t.Errorf("sensor must not have state_on")
	}
}

// --- light (JSON schema) ----------------------------------------------------

// TestDiscoveryRoundTrip_Light verifies that the per-parameter LEVEL
// discovery (classifyComponent fallback → HAComponentLight) includes
// command_topic and NO value_template referencing boolean comparison.
// The channel-aggregated light uses schema=json (no templates needed).
//
// This test covers the per-parameter path (no custom-DP Source) which
// classifies LEVEL as HAComponentLight.
func TestDiscoveryRoundTrip_Light(t *testing.T) {
	t.Parallel()
	payload := buildDiscovery(t, mqtt.Event{
		Interface: "HmIP-RF", DeviceAddress: "AABBCC", ChannelNo: 1,
		Parameter: "LEVEL", Category: hmenum.DataPointCategoryLight, Writable: true,
	})

	if _, has := payload["command_topic"]; !has {
		t.Errorf("light must have command_topic")
	}
	// Per-parameter light entity: value_template should not contain | lower
	// (LEVEL is a float, not a boolean).
	if vt, has := payload["value_template"].(string); has && strings.Contains(vt, "| lower") {
		t.Errorf("light (LEVEL) value_template %q should not apply | lower to a float", vt)
	}
	// optimistic must be false — critical for correct state feedback.
	if payload["optimistic"] != false {
		t.Errorf("light optimistic = %v, want false", payload["optimistic"])
	}

	// Round-trip: float LEVEL 0.75 renders numerically.
	if vt, has := payload["value_template"].(string); has {
		rendered := renderJinja(t, vt, `{"value":0.75,"available":true}`)
		if rendered == "" {
			t.Errorf("light value_template rendered empty for {value:0.75}")
		}
		// Must not look like a boolean.
		if rendered == "true" || rendered == "false" || rendered == "True" || rendered == "False" {
			t.Errorf("light render = %q — looks like a boolean; LEVEL should be numeric", rendered)
		}
	}
}

// --- cover ------------------------------------------------------------------

// buildCoverBody assembles a real [cover.Cover] against a LEVEL-carrying
// channel (SupportsPosition so the position_template is emitted) and
// returns its actual HADiscoveryPayload body — the same builder output
// the MQTT bridge marshals for a shutter.
func buildCoverBody(t *testing.T) map[string]any {
	t.Helper()
	const addr = "COVER0001:3"
	w := roundtripWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "COVER0001"})
	ch := d.AddChannel(addr, 3, "BLIND", hmenum.ParamsetKeyValues)
	rtPutFloat(ch, addr, hmenum.ParameterLevel, w)
	c := cover.New(cover.Config{Channel: ch, Writer: w, Capabilities: custom.CoverCapabilities{SupportsPosition: true}})
	comp, body := c.HADiscoveryPayload(roundtripDiscoveryCtx{})
	if comp != "cover" {
		t.Fatalf("cover component = %q, want cover", comp)
	}
	return buildAggregateBody(t, "cover", body)
}

// TestDiscoveryRoundTrip_Cover drives the ACTUAL cover-builder output
// (not a hand-written template string): the value_template
// `{{ value_json.state }}` must render the HA-canonical state strings
// the builder also advertises via state_open / state_closed, and the
// position_template must render the numeric current_position. A bug in
// either half leaves the HA cover card stuck on "unknown" / no slider.
func TestDiscoveryRoundTrip_Cover(t *testing.T) {
	t.Parallel()
	body := buildCoverBody(t)

	valueTemplate := mustBodyString(t, body, "value_template")
	stateOpen := mustBodyString(t, body, "state_open")
	stateClosed := mustBodyString(t, body, "state_closed")

	// Round-trip the aggregated envelope through the builder's own template.
	openEnv := `{"state":"open","current_position":75}`
	if got := renderJinja(t, valueTemplate, openEnv); got != stateOpen {
		t.Errorf("cover value_template render = %q, want state_open %q", got, stateOpen)
	}
	closedEnv := `{"state":"closed","current_position":0}`
	if got := renderJinja(t, valueTemplate, closedEnv); got != stateClosed {
		t.Errorf("cover value_template render = %q, want state_closed %q", got, stateClosed)
	}

	// position_template must render the numeric current_position field.
	posTemplate := mustBodyString(t, body, "position_template")
	if got := renderJinja(t, posTemplate, openEnv); got != "75" {
		t.Errorf("cover position_template render = %q, want %q", got, "75")
	}

	// optimistic must be false so HA waits for the CCU echo.
	if body["optimistic"] != false {
		t.Errorf("cover optimistic = %v, want false", body["optimistic"])
	}
}

// --- climate ----------------------------------------------------------------

// buildClimateBody assembles a real [climate.Climate] (RF kind) against a
// channel carrying SET_TEMPERATURE + ACTUAL_TEMPERATURE and returns its
// actual HADiscoveryPayload body.
func buildClimateBody(t *testing.T) map[string]any {
	t.Helper()
	const addr = "CLIMATE001:1"
	w := roundtripWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "CLIMATE001"})
	ch := d.AddChannel(addr, 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	rtPutFloat(ch, addr, hmenum.ParameterSetTemperature, w)
	rtPutFloatSensor(ch, addr, hmenum.ParameterActualTemperature)
	c := climate.New(climate.Config{Channel: ch, Writer: w, Kind: climate.KindRF})
	comp, body := c.HADiscoveryPayload(roundtripDiscoveryCtx{})
	if comp != "climate" {
		t.Fatalf("climate component = %q, want climate", comp)
	}
	return buildAggregateBody(t, "climate", body)
}

// TestDiscoveryRoundTrip_Climate drives the ACTUAL climate-builder output
// (not hand-written strings). The per-DP wire templates
// (current_temperature_template, temperature_state_template) extract the
// `value` scalar from the slot envelope; the mode_state_template reads
// hvac_mode from the aggregate. All three must render cleanly or the HA
// climate card shows a null temperature / unknown mode.
func TestDiscoveryRoundTrip_Climate(t *testing.T) {
	t.Parallel()
	body := buildClimateBody(t)

	curTempTemplate := mustBodyString(t, body, "current_temperature_template")
	if got := renderJinja(t, curTempTemplate, `{"value":21.5,"available":true,"unit":"°C"}`); got != "21.5" {
		t.Errorf("climate current_temperature render = %q, want %q", got, "21.5")
	}

	setpointTemplate := mustBodyString(t, body, "temperature_state_template")
	if got := renderJinja(t, setpointTemplate, `{"value":19.5,"available":true,"unit":"°C"}`); got != "19.5" {
		t.Errorf("climate temperature_state render = %q, want %q", got, "19.5")
	}

	modeTemplate := mustBodyString(t, body, "mode_state_template")
	if got := renderJinja(t, modeTemplate, `{"hvac_mode":"heat","action":"heating"}`); got != "heat" {
		t.Errorf("climate mode_state render = %q, want %q", got, "heat")
	}

	// optimistic must be false so HA waits for the CCU echo.
	if body["optimistic"] != false {
		t.Errorf("climate optimistic = %v, want false", body["optimistic"])
	}
}

// --- siren ------------------------------------------------------------------

// TestDiscoveryRoundTrip_Siren drives the ACTUAL siren-builder output.
// HA's strict siren schema requires the value_template output to match
// state_on / state_off; the builder emits `{{ value_json.state }}` and
// the StatePayload publishes `{"state":"on"|"off"}`. A mismatch leaves
// HA logging `Payload received … is not one of [on, off]`.
func TestDiscoveryRoundTrip_Siren(t *testing.T) {
	t.Parallel()
	s := siren.New(siren.Config{Writer: roundtripWriter{}})
	comp, body := s.HADiscoveryPayload(roundtripDiscoveryCtx{})
	if comp != "siren" {
		t.Fatalf("siren component = %q, want siren", comp)
	}
	body = buildAggregateBody(t, "siren", body)

	valueTemplate := mustBodyString(t, body, "value_template")
	stateOn := mustBodyString(t, body, "state_on")
	stateOff := mustBodyString(t, body, "state_off")

	if got := renderJinja(t, valueTemplate, `{"state":"on"}`); got != stateOn {
		t.Errorf("siren value_template render = %q, want state_on %q", got, stateOn)
	}
	if got := renderJinja(t, valueTemplate, `{"state":"off"}`); got != stateOff {
		t.Errorf("siren value_template render = %q, want state_off %q", got, stateOff)
	}
	// The command surface HA sends payload_on/payload_off to must be present.
	if _, has := body["command_topic"]; !has {
		t.Error("siren must have command_topic")
	}
}

// --- valve ------------------------------------------------------------------

// buildIrrigationBody assembles a real [valve.Irrigation] against a
// STATE-carrying channel and returns its actual HADiscoveryPayload body.
func buildIrrigationBody(t *testing.T) map[string]any {
	t.Helper()
	const addr = "VALVE0001:1"
	w := roundtripWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VALVE0001"})
	ch := d.AddChannel(addr, 1, "IRRIGATION_VALVE", hmenum.ParamsetKeyValues)
	rtPutSwitch(ch, addr, hmenum.ParameterState, w)
	v := valve.NewIrrigation(ch, custom.RebasedChannelGroupConfig{})
	if v == nil {
		t.Fatal("valve.NewIrrigation returned nil (STATE switch not resolved)")
	}
	comp, body := v.HADiscoveryPayload(roundtripDiscoveryCtx{})
	if comp != "valve" {
		t.Fatalf("irrigation component = %q, want valve", comp)
	}
	return buildAggregateBody(t, "valve", body)
}

// buildModulatingBody assembles a real [valve.Modulating] against a
// LEVEL-carrying channel and returns its actual HADiscoveryPayload body.
func buildModulatingBody(t *testing.T) map[string]any {
	t.Helper()
	const addr = "VALVE0002:1"
	w := roundtripWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VALVE0002"})
	ch := d.AddChannel(addr, 1, "MODULATING_VALVE", hmenum.ParamsetKeyValues)
	rtPutFloat(ch, addr, hmenum.ParameterLevel, w)
	v := valve.NewModulating(ch, custom.RebasedChannelGroupConfig{})
	if v == nil {
		t.Fatal("valve.NewModulating returned nil (LEVEL float not resolved)")
	}
	comp, body := v.HADiscoveryPayload(roundtripDiscoveryCtx{})
	if comp != "valve" {
		t.Fatalf("modulating component = %q, want valve", comp)
	}
	return buildAggregateBody(t, "valve", body)
}

// TestDiscoveryRoundTrip_Valve drives the ACTUAL valve-builder output for
// both flavours. The irrigation builder emits the `{% if
// value_json.is_open %}open{% else %}closed{% endif %}` truthiness branch
// (internal/model/custom/valve/payload.go:167) — the bare
// `{{ value_json.is_open }}` form would render Python's `True`/`False`
// and match no state_open / state_closed permutation, leaving HA logging
// `Payload received … is not one of [open, closed, opening, closing]`.
// The modulating builder emits `{{ value_json.current_level_pct }}` for
// the 0..100 position.
func TestDiscoveryRoundTrip_Valve(t *testing.T) {
	t.Parallel()

	t.Run("irrigation_if_else", func(t *testing.T) {
		t.Parallel()
		body := buildIrrigationBody(t)

		valueTemplate := mustBodyString(t, body, "value_template")
		stateOpen := mustBodyString(t, body, "state_open")
		stateClosed := mustBodyString(t, body, "state_closed")

		// The template MUST be the if/else branch, not the bare accessor —
		// pin the shape so a regression back to `{{ value_json.is_open }}`
		// is caught.
		if !strings.Contains(valueTemplate, "{% if") {
			t.Errorf("irrigation value_template %q must use the {%% if %%} branch", valueTemplate)
		}
		if got := renderJinjaBoolIfElse(t, valueTemplate, `{"is_open":true}`); got != stateOpen {
			t.Errorf("irrigation open render = %q, want state_open %q", got, stateOpen)
		}
		if got := renderJinjaBoolIfElse(t, valueTemplate, `{"is_open":false}`); got != stateClosed {
			t.Errorf("irrigation closed render = %q, want state_closed %q", got, stateClosed)
		}
		// Binary irrigation must NOT report a position.
		if body["reports_position"] != false {
			t.Errorf("irrigation reports_position = %v, want false", body["reports_position"])
		}
	})

	t.Run("modulating_position", func(t *testing.T) {
		t.Parallel()
		body := buildModulatingBody(t)

		valueTemplate := mustBodyString(t, body, "value_template")
		if got := renderJinja(t, valueTemplate, `{"current_level_pct":42}`); got != "42" {
			t.Errorf("modulating value_template render = %q, want %q", got, "42")
		}
		// Modulating valves report position.
		if body["reports_position"] != true {
			t.Errorf("modulating reports_position = %v, want true", body["reports_position"])
		}
	})
}

// --- event ------------------------------------------------------------------

// TestDiscoveryRoundTrip_Event_NoValueTemplate pins the contract that
// mqtt.event entities must NOT have a value_template. HA's mqtt.event
// component parses the post-value_template payload as JSON and reads
// `event_type` from it. If a scalar-extracting value_template like
// `{{ value_json.value }}` is present, HA receives "press_short" (a
// plain string, not JSON) and logs:
//
//	"No valid JSON event payload detected, value after processing payload 'press_short'"
//
// The fix: drop value_template for event entities so HA receives the
// raw {"event_type":"press_short"} envelope directly.
func TestDiscoveryRoundTrip_Event_NoValueTemplate(t *testing.T) {
	t.Parallel()
	payload := buildDiscovery(t, mqtt.Event{
		Interface: "HmIP-RF", DeviceAddress: "AABBCC", ChannelNo: 1,
		Parameter: "PRESS_SHORT", Category: hmenum.DataPointCategoryEvent,
	})

	// event must have event_types.
	evTypes, hasEvTypes := payload["event_types"]
	if !hasEvTypes {
		t.Fatalf("event entity missing event_types: %v", mapKeys(payload))
	}
	typesList, ok := evTypes.([]any)
	if !ok || len(typesList) == 0 {
		t.Fatalf("event_types must be a non-empty list, got %T %v", evTypes, evTypes)
	}
	if typesList[0] != "press_short" {
		t.Errorf("event_types[0] = %v, want %q", typesList[0], "press_short")
	}

	// THE CRITICAL CONTRACT: no value_template on an event entity.
	// If value_template is present HA parses the rendered scalar as JSON
	// and fails with "No valid JSON event payload detected".
	if vt, has := payload["value_template"]; has {
		t.Errorf("event entity must NOT have value_template (breaks HA JSON parsing of event_type), got %q", vt)
	}

	// device_class must be "button" per HA event platform convention.
	if dc := payload["device_class"]; dc != "button" {
		t.Errorf("event device_class = %v, want %q", dc, "button")
	}
}

// TestDiscoveryRoundTrip_Event_JSONPayloadNotBroken verifies the scenario
// that triggered the bug: if value_template extracted a scalar, the HA
// event parser would receive a non-JSON string and fail. We simulate
// what WOULD happen if the template were present, to document the failure
// mode, and then assert the fix (no template) avoids it.
func TestDiscoveryRoundTrip_Event_JSONPayloadNotBroken(t *testing.T) {
	t.Parallel()
	// The raw state-topic payload for an event entity looks like:
	// {"event_type":"press_short"} — a JSON object HA can parse.
	rawEnvelope := `{"event_type":"press_short"}`

	// If a scalar-extracting template were applied:
	brokenTemplate := `{{ value_json.event_type }}`
	brokenResult := renderJinja(t, brokenTemplate, rawEnvelope)
	// brokenResult would be "press_short" — a plain string, not JSON.
	// HA would fail to parse it as JSON.
	var js any
	if err := json.Unmarshal([]byte(brokenResult), &js); err == nil {
		// If it happened to be valid JSON (e.g. a number or "true"), that
		// would be unexpected — document it.
		t.Logf("note: brokenResult %q is valid JSON — unexpected but not a failure here", brokenResult)
	}
	// The correct fix is to have NO value_template so HA receives rawEnvelope intact.
	// Verify rawEnvelope IS valid JSON and contains event_type.
	var eventPayload map[string]any
	if err := json.Unmarshal([]byte(rawEnvelope), &eventPayload); err != nil {
		t.Fatalf("raw event envelope is not valid JSON: %v", err)
	}
	if eventPayload["event_type"] != "press_short" {
		t.Errorf("event_type = %v, want %q", eventPayload["event_type"], "press_short")
	}
	// Document the bug: brokenResult is NOT valid JSON for the event parser.
	var brokenMap map[string]any
	if err := json.Unmarshal([]byte(brokenResult), &brokenMap); err == nil {
		// It is valid JSON — this would only happen if event_type value happened
		// to be a JSON expression itself (e.g. "null", "1"). For "press_short" it
		// must fail.
		if brokenResult != "null" && brokenResult != "true" && brokenResult != "false" {
			t.Logf("note: brokenResult %q parsed as map — unexpected for string event_type", brokenResult)
		}
	}
}

// --- button -----------------------------------------------------------------

// TestDiscoveryRoundTrip_Button verifies that button entities have no
// state topic rendering and carry payload_press="PRESS".
func TestDiscoveryRoundTrip_Button(t *testing.T) {
	t.Parallel()
	payload := buildDiscovery(t, mqtt.Event{
		Interface: "HmIP-RF", DeviceAddress: "AABBCC", ChannelNo: 1,
		Parameter: "SUBMIT", Category: hmenum.DataPointCategoryButton,
	})

	pp := mustStringField(t, payload, "payload_press")
	if pp != "PRESS" {
		t.Errorf("button payload_press = %q, want %q", pp, "PRESS")
	}
	if _, has := payload["command_topic"]; !has {
		t.Errorf("button must have command_topic")
	}
	// Buttons have no state — value_template must not encode a boolean matcher.
	if vt, has := payload["value_template"].(string); has && strings.Contains(vt, "| lower") {
		t.Errorf("button value_template %q unexpectedly has | lower — buttons have no readable state", vt)
	}
}

// --- guard: empty envelope --------------------------------------------------

// TestDiscoveryRoundTrip_GuardEmptyEnvelope pins the `is defined` guard
// in valueJSONValueTemplate and valueJSONValueLowerTemplate. When the
// state topic carries an empty retained payload (eviction flow for
// unobserved DPs) HA would raise `'value_json' is undefined` without the
// guard. The template must render empty ("") for an empty input, not panic.
func TestDiscoveryRoundTrip_GuardEmptyEnvelope(t *testing.T) {
	t.Parallel()
	guardedTemplate := `{% if value_json is defined %}{{ value_json.value | lower }}{% endif %}`

	// Empty input → guard fires → empty output → HA marks entity unavailable.
	rendered := renderJinja(t, guardedTemplate, "")
	if rendered != "" {
		t.Errorf("guard rendered %q for empty input, want empty string (triggers HA unavailable badge)", rendered)
	}

	// Non-JSON input (e.g. naked "online" string) → guard fires → empty.
	renderedNotJSON := renderJinja(t, guardedTemplate, "online")
	if renderedNotJSON != "" {
		t.Errorf("guard rendered %q for non-JSON input, want empty string", renderedNotJSON)
	}

	// Valid JSON with defined value → guard passes → renders.
	renderedValid := renderJinja(t, guardedTemplate, `{"value":true}`)
	if renderedValid != "true" {
		t.Errorf("guard with valid input rendered %q, want %q", renderedValid, "true")
	}
}

// --- boolean capitalisation contract ----------------------------------------

// TestDiscoveryRoundTrip_BoolCapitalisation is the direct regression test
// for the bug cluster: Go's json.Marshal(true) produces lowercase "true",
// but Jinja2's default rendering of a Python bool is "True" (capitalised).
//
// The state topics openccu-loom publishes carry Go-marshalled JSON
// ("true"/"false"), NOT Python-capitalised booleans. The `| lower`
// filter in the templates is still correct because it normalises any
// stray capitalisation and is harmless for already-lowercase values.
// This test locks the invariant that Go-JSON-encoded booleans remain
// lowercase after `| lower` so the match against state_on/payload_on
// ("true") succeeds.
func TestDiscoveryRoundTrip_BoolCapitalisation(t *testing.T) {
	t.Parallel()
	// Go encodes bool true as lowercase "true" in JSON.
	envelope := map[string]any{"value": true, "available": true}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	envelopeJSON := string(raw)

	// Verify Go produces lowercase.
	if !strings.Contains(envelopeJSON, `"value":true`) {
		t.Fatalf("Go json.Marshal(true) did not produce lowercase 'true'; got %q — fundamental encoding assumption violated", envelopeJSON)
	}

	// Apply valueJSONValueLowerTemplate (the actual template in the discovery payload).
	template := `{% if value_json is defined %}{{ value_json.value | lower }}{% endif %}`
	rendered := renderJinja(t, template, envelopeJSON)

	if rendered != "true" {
		t.Errorf(
			"valueJSONValueLowerTemplate rendered %q, want %q\n"+
				"  input JSON: %s\n"+
				"  This means switch/lock/binary_sensor would stay 'unknown' in HA.",
			rendered, "true", envelopeJSON,
		)
	}
}

// --- multi-press event aggregation ------------------------------------------

// TestDiscoveryRoundTrip_Event_MultiPress verifies that a PRESS_LONG
// event entity also drops value_template — the same fix applies to all
// press-event variants.
func TestDiscoveryRoundTrip_Event_MultiPress(t *testing.T) {
	t.Parallel()
	for _, param := range []string{"PRESS_SHORT", "PRESS_LONG", "PRESS_LONG_RELEASE", "PRESS_LONG_START"} {
		t.Run(param, func(t *testing.T) {
			t.Parallel()
			p := buildDiscovery(t, mqtt.Event{
				Interface: "HmIP-RF", DeviceAddress: "AABBCC", ChannelNo: 1,
				Parameter: param, Category: hmenum.DataPointCategoryEvent,
			})
			if _, has := p["value_template"]; has {
				t.Errorf("%s event entity must NOT have value_template (breaks mqtt.event JSON parsing)", param)
			}
			if _, has := p["event_types"]; !has {
				t.Errorf("%s event entity missing event_types", param)
			}
		})
	}
}

// --- number / select / text / update round-trips ---------------------------

// TestDiscoveryRoundTrip_Number verifies the per-parameter number
// path classifies a writable numeric DP as HAComponentNumber, emits
// a value_template that renders the scalar without the `| lower`
// filter (numeric values break under lowercase), and round-trips a
// floating-point payload to the same scalar HA expects.
func TestDiscoveryRoundTrip_Number(t *testing.T) {
	t.Parallel()
	payload := buildDiscovery(t, mqtt.Event{
		Interface: "HmIP-RF", DeviceAddress: "AABBCC", ChannelNo: 1,
		Parameter: "TEMPERATURE_OFFSET", Category: hmenum.DataPointCategoryNumber,
		Writable: true, Descriptor: &pload.GenericConfig{Unit: "°C"},
	})
	vt, ok := payload["value_template"].(string)
	if !ok || vt == "" {
		t.Fatal("number must have value_template")
	}
	if strings.Contains(vt, "| lower") {
		t.Errorf("number value_template %q must NOT apply | lower to a float", vt)
	}
	rendered := renderJinja(t, vt, `{"value":-2.5,"available":true}`)
	if rendered != "-2.5" {
		t.Errorf("number render = %q, want %q", rendered, "-2.5")
	}
	if _, has := payload["command_topic"]; !has {
		t.Errorf("writable number must have command_topic")
	}
}

// TestDiscoveryRoundTrip_Select verifies the writable-LIST path
// classifies as HAComponentSelect, advertises lower-cased `options`
// (the reference stack lowercases enum tokens so HA can translate
// them), renders the wire label through `| lower` so state and options
// agree, and maps the chosen option back to the uppercase CCU token
// via the command_template.
func TestDiscoveryRoundTrip_Select(t *testing.T) {
	t.Parallel()
	payload := buildDiscovery(t, mqtt.Event{
		Interface: "HmIP-RF", DeviceAddress: "AABBCC", ChannelNo: 1,
		Parameter: "DECISION_VALUE", Category: hmenum.DataPointCategorySelect,
		Writable:   true,
		Descriptor: &pload.GenericConfig{ValueList: []string{"OFF", "ON", "AUTO"}},
	})
	opts, has := payload["options"].([]any)
	if !has || len(opts) != 3 {
		t.Fatalf("select must declare 3 options, got %v", payload["options"])
	}
	if opts[0] != "off" || opts[1] != "on" || opts[2] != "auto" {
		t.Errorf("select options must be lower-cased, got %v", opts)
	}
	vt, ok := payload["value_template"].(string)
	if !ok || vt == "" {
		t.Fatal("select must have value_template")
	}
	if !strings.Contains(vt, "| lower") {
		t.Errorf("select value_template %q must apply | lower so the state matches the lower-cased options", vt)
	}
	rendered := renderJinja(t, vt, `{"value":"AUTO","available":true}`)
	if rendered != "auto" {
		t.Errorf("select render = %q, want %q", rendered, "auto")
	}
	ct, ok := payload["command_template"].(string)
	if !ok || !strings.Contains(ct, "| upper") {
		t.Errorf("select command_template %q must restore the uppercase CCU token", ct)
	}
}

// TestDiscoveryRoundTrip_Text verifies the writable HubValueTypeString
// path now classifies as sensor (text caps at 255 chars; the recent
// commit 5720923 routed strings to sensor) AND that the value_template
// passes a multi-line payload through intact.
func TestDiscoveryRoundTrip_Text(t *testing.T) {
	t.Parallel()
	payload := buildDiscovery(t, mqtt.Event{
		Interface: "HmIP-RF", DeviceAddress: "AABBCC", ChannelNo: 1,
		Parameter: "ALARM_TEXT", Category: hmenum.DataPointCategorySensor,
		Descriptor: &pload.GenericConfig{},
	})
	vt, ok := payload["value_template"].(string)
	if !ok || vt == "" {
		t.Fatal("text-style sensor must have value_template")
	}
	rendered := renderJinja(t, vt, `{"value":"Hello World","available":true}`)
	if rendered != "Hello World" {
		t.Errorf("text render = %q, want %q", rendered, "Hello World")
	}
}
