// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// discoveryCtx is a minimal stub for payload.HADiscoveryContext used in
// payload-builder smoke tests.
type discoveryCtx struct{}

func (discoveryCtx) AggregatedStateTopic() string { return "test/state" }
func (discoveryCtx) CustomDPStateTopic() string   { return "test/custom/state" }
func (discoveryCtx) ServiceMethodCommandTopic(method string) string {
	return "test/svc/" + method + "/set"
}

func (discoveryCtx) WireParameterCommandTopic(parameter string) string {
	return "test/" + parameter + "/set"
}

func (discoveryCtx) WireParameterStateTopic(parameter string) string {
	return "test/" + parameter
}

var _ payload.HADiscoveryContext = discoveryCtx{}

// --- Cover ---

func TestCoverHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var c *Cover
	comp, body := c.HADiscoveryPayload(discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestCoverHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	comp, body := c.HADiscoveryPayload(discoveryCtx{})
	if comp != "cover" {
		t.Fatalf("component = %q, want %q", comp, "cover")
	}
	if body == nil {
		t.Fatal("body must not be nil")
	}
}

func TestCoverHADiscoveryPayload_RequiredKeys(t *testing.T) {
	t.Parallel()
	// SupportsPosition required for position_topic emission (Task #38).
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{SupportsPosition: true})
	_, body := c.HADiscoveryPayload(discoveryCtx{})

	for _, key := range []string{
		"position_topic",
		"set_position_topic",
		"position_template",
		"command_topic",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing required key %q", key)
		}
	}
}

func TestCoverHADiscoveryPayload_TopicValues(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{SupportsPosition: true})
	ctx := discoveryCtx{}
	_, body := c.HADiscoveryPayload(ctx)

	if v, _ := body["position_topic"].(string); v != ctx.CustomDPStateTopic() {
		t.Errorf("position_topic = %q, want %q", v, ctx.CustomDPStateTopic())
	}
	wantSetPos := ctx.ServiceMethodCommandTopic("set_position")
	if v, _ := body["set_position_topic"].(string); v != wantSetPos {
		t.Errorf("set_position_topic = %q, want %q", v, wantSetPos)
	}
}

// TestCoverHADiscoveryPayload_NoPositionWithoutCapability pins Task
// #38: Cover ohne SupportsPosition-Capability emittiert keine
// position_topic / set_position_topic. HA würde sonst fälschlich
// einen Slider rendern für Geräte ohne LEVEL-Schreibsupport.
func TestCoverHADiscoveryPayload_NoPositionWithoutCapability(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	_, body := c.HADiscoveryPayload(discoveryCtx{})

	for _, key := range []string{"position_topic", "set_position_topic", "position_template", "set_position_template"} {
		if _, ok := body[key]; ok {
			t.Errorf("position-related key %q must NOT be emitted without SupportsPosition", key)
		}
	}
	// command_topic + state_topic still required (open/close/stop).
	for _, key := range []string{"command_topic", "state_topic"} {
		if _, ok := body[key]; !ok {
			t.Errorf("non-position key %q must still be emitted", key)
		}
	}
}

// --- Blind ---

func TestBlindHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var b *Blind
	comp, body := b.HADiscoveryPayload(discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestBlindHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	b := newBlindRig(t, "HmIP-BBL:3", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	comp, body := b.HADiscoveryPayload(discoveryCtx{})
	if comp != "cover" {
		t.Fatalf("component = %q, want %q", comp, "cover")
	}
	if body == nil {
		t.Fatal("body must not be nil")
	}
}

func TestBlindHADiscoveryPayload_TiltKeys(t *testing.T) {
	t.Parallel()
	// SupportsPosition required so the underlying Cover emits position_topic
	// (Task #38). SupportsTilt unlocks the tilt_* keys this test asserts.
	b := newBlindRig(t, "HmIP-BBL:3", &putWriter{}, custom.CoverCapabilities{SupportsPosition: true, SupportsTilt: true}, BlindKindHM)
	ctx := discoveryCtx{}
	_, body := b.HADiscoveryPayload(ctx)

	for _, key := range []string{
		"tilt_status_topic",
		"tilt_command_topic",
		"position_topic",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing required tilt key %q", key)
		}
	}

	if v, _ := body["tilt_status_topic"].(string); v != ctx.CustomDPStateTopic() {
		t.Errorf("tilt_status_topic = %q, want %q", v, ctx.CustomDPStateTopic())
	}
	wantTiltCmd := ctx.ServiceMethodCommandTopic("set_tilt")
	if v, _ := body["tilt_command_topic"].(string); v != wantTiltCmd {
		t.Errorf("tilt_command_topic = %q, want %q", v, wantTiltCmd)
	}
}

// --- Garage ---

func TestGarageHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var g *Garage
	comp, body := g.HADiscoveryPayload(discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestGarageHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	g := NewGarage(GarageConfig{Writer: &stubWriter{}})
	comp, body := g.HADiscoveryPayload(discoveryCtx{})
	if comp != "cover" {
		t.Fatalf("component = %q, want %q", comp, "cover")
	}
	if body == nil {
		t.Fatal("body must not be nil")
	}
}

func TestGarageHADiscoveryPayload_RequiredKeys(t *testing.T) {
	t.Parallel()
	g := NewGarage(GarageConfig{Writer: &stubWriter{}})
	ctx := discoveryCtx{}
	_, body := g.HADiscoveryPayload(ctx)

	for _, key := range []string{
		"command_topic",
		"state_topic",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing required garage key %q", key)
		}
	}

	wantCmd := ctx.WireParameterCommandTopic("DOOR_COMMAND")
	if v, _ := body["command_topic"].(string); v != wantCmd {
		t.Errorf("command_topic = %q, want %q", v, wantCmd)
	}
	if v, _ := body["state_topic"].(string); v != ctx.CustomDPStateTopic() {
		t.Errorf("state_topic = %q, want %q", v, ctx.CustomDPStateTopic())
	}
}

// --- Cover parity tests ---

func TestCoverHADiscoveryPayload_StateParity(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	ctx := discoveryCtx{}
	_, body := c.HADiscoveryPayload(ctx)

	// state_topic must equal aggregated state topic.
	if v, _ := body["state_topic"].(string); v != ctx.CustomDPStateTopic() {
		t.Errorf("state_topic = %q, want %q", v, ctx.CustomDPStateTopic())
	}
	// value_template must reference state field.
	if v, _ := body["value_template"].(string); v != "{{ value_json.state }}" {
		t.Errorf("value_template = %q, want %q", v, "{{ value_json.state }}")
	}
	// All five HA-canonical state strings must be present and lowercase.
	stateStrings := map[string]string{
		"state_open":    "open",
		"state_closed":  "closed",
		"state_opening": "opening",
		"state_closing": "closing",
		"state_stopped": "stopped",
	}
	for k, want := range stateStrings {
		if v, _ := body[k].(string); v != want {
			t.Errorf("body[%q] = %q, want %q", k, v, want)
		}
	}
	// device_class must be "shutter" for plain Cover.
	if v, _ := body["device_class"].(string); v != "shutter" {
		t.Errorf("device_class = %q, want %q", v, "shutter")
	}
	// optimistic must be false.
	if v, _ := body["optimistic"].(bool); v {
		t.Errorf("optimistic = %v, want false", v)
	}
}

func TestCoverHADiscoveryPayload_NoTiltFields(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	_, body := c.HADiscoveryPayload(discoveryCtx{})

	// Plain Cover must not carry tilt fields.
	for _, key := range []string{"tilt_opened_value", "tilt_closed_value"} {
		if _, ok := body[key]; ok {
			t.Errorf("Cover body must not contain %q", key)
		}
	}
}

// --- Blind parity tests ---

func TestBlindHADiscoveryPayload_StateParity(t *testing.T) {
	t.Parallel()
	b := newBlindRig(t, "HmIP-BBL:3", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	ctx := discoveryCtx{}
	_, body := b.HADiscoveryPayload(ctx)

	// state_topic must equal aggregated state topic.
	if v, _ := body["state_topic"].(string); v != ctx.CustomDPStateTopic() {
		t.Errorf("state_topic = %q, want %q", v, ctx.CustomDPStateTopic())
	}
	// value_template must reference state field.
	if v, _ := body["value_template"].(string); v != "{{ value_json.state }}" {
		t.Errorf("value_template = %q, want %q", v, "{{ value_json.state }}")
	}
	// All five HA-canonical state strings must be present and lowercase.
	stateStrings := map[string]string{
		"state_open":    "open",
		"state_closed":  "closed",
		"state_opening": "opening",
		"state_closing": "closing",
		"state_stopped": "stopped",
	}
	for k, want := range stateStrings {
		if v, _ := body[k].(string); v != want {
			t.Errorf("body[%q] = %q, want %q", k, v, want)
		}
	}
	// device_class must be "blind" (not "shutter").
	if v, _ := body["device_class"].(string); v != "blind" {
		t.Errorf("device_class = %q, want %q", v, "blind")
	}
	// optimistic must be false.
	if v, _ := body["optimistic"].(bool); v {
		t.Errorf("optimistic = %v, want false", v)
	}
}

func TestBlindHADiscoveryPayload_TiltOpenedClosedValues(t *testing.T) {
	t.Parallel()
	b := newBlindRig(t, "HmIP-BBL:3", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	_, body := b.HADiscoveryPayload(discoveryCtx{})

	if v, _ := body["tilt_opened_value"].(int); v != 100 {
		t.Errorf("tilt_opened_value = %v, want 100", v)
	}
	if v, _ := body["tilt_closed_value"].(int); v != 0 {
		t.Errorf("tilt_closed_value = %v, want 0", v)
	}
}

// --- Garage parity tests ---

func TestGarageHADiscoveryPayload_StateParity(t *testing.T) {
	t.Parallel()
	g := NewGarage(GarageConfig{Writer: &stubWriter{}})
	ctx := discoveryCtx{}
	_, body := g.HADiscoveryPayload(ctx)

	// state_topic must equal aggregated state topic.
	if v, _ := body["state_topic"].(string); v != ctx.CustomDPStateTopic() {
		t.Errorf("state_topic = %q, want %q", v, ctx.CustomDPStateTopic())
	}
	// value_template must reference state (lowercase) not door_state (uppercase CCU raw).
	if v, _ := body["value_template"].(string); v != "{{ value_json.state }}" {
		t.Errorf("value_template = %q, want %q", v, "{{ value_json.state }}")
	}
	// All five HA-canonical state strings must be present and lowercase.
	stateStrings := map[string]string{
		"state_open":    "open",
		"state_closed":  "closed",
		"state_opening": "opening",
		"state_closing": "closing",
		"state_stopped": "stopped",
	}
	for k, want := range stateStrings {
		if v, _ := body[k].(string); v != want {
			t.Errorf("body[%q] = %q, want %q", k, v, want)
		}
	}
	// device_class must be "garage".
	if v, _ := body["device_class"].(string); v != "garage" {
		t.Errorf("device_class = %q, want %q", v, "garage")
	}
	// optimistic must be false.
	if v, _ := body["optimistic"].(bool); v {
		t.Errorf("optimistic = %v, want false", v)
	}
	// Garage must NOT carry tilt fields.
	for _, key := range []string{"tilt_opened_value", "tilt_closed_value"} {
		if _, ok := body[key]; ok {
			t.Errorf("Garage body must not contain %q", key)
		}
	}
}

// TestGarageHADiscoveryPayload_VentCommandTopic pins
// parity rule (capabilities/cover.py:16): when SupportsVent is true,
// the discovery payload must include a vent_command_topic exposing the
// ventilate service method (PARTIAL_OPEN on the wire). When SupportsVent
// is false the field must be absent.
func TestGarageHADiscoveryPayload_VentCommandTopic(t *testing.T) {
	t.Parallel()

	t.Run("SupportsVent=true → vent_command_topic present", func(t *testing.T) {
		t.Parallel()
		g := NewGarage(GarageConfig{
			Writer:       &stubWriter{},
			Capabilities: custom.CoverCapabilities{SupportsVent: true},
		})
		ctx := discoveryCtx{}
		_, body := g.HADiscoveryPayload(ctx)
		v, ok := body["vent_command_topic"]
		if !ok {
			t.Fatal("vent_command_topic missing when SupportsVent=true")
		}
		want := ctx.ServiceMethodCommandTopic("ventilate")
		if s, _ := v.(string); s != want {
			t.Errorf("vent_command_topic = %q, want %q", s, want)
		}
	})

	t.Run("SupportsVent=false → vent_command_topic absent", func(t *testing.T) {
		t.Parallel()
		g := NewGarage(GarageConfig{
			Writer:       &stubWriter{},
			Capabilities: custom.CoverCapabilities{SupportsVent: false},
		})
		_, body := g.HADiscoveryPayload(discoveryCtx{})
		if _, ok := body["vent_command_topic"]; ok {
			t.Error("vent_command_topic must be absent when SupportsVent=false")
		}
	})
}

// TestGarageConfigPayload_SupportsVent pins that ConfigPayload exposes
// the SupportsVent capability so non-HA consumers (REST, WS) can
// discover the ventilate service method.
func TestGarageConfigPayload_SupportsVent(t *testing.T) {
	t.Parallel()
	g := NewGarage(GarageConfig{
		Writer:       &stubWriter{},
		Capabilities: custom.CoverCapabilities{SupportsVent: true},
	})
	cfg, _ := g.Config().(*payload.GarageConfig)
	if cfg == nil {
		t.Fatal("supports_vent missing from ConfigPayload")
	}
	if !cfg.SupportsVent {
		t.Error("supports_vent = false, want true")
	}
}

// --- CoverVariant / device_class tests ---

// TestVariantString_AllVariants pins every variant → HA device_class
// string mapping. If a new variant is added without updating
// VariantString, this test will catch the regression.
func TestVariantString_AllVariants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		v    CoverVariant
		want string
	}{
		{VariantShutter, "shutter"},
		{VariantBlind, "blind"},
		{VariantAwning, "awning"},
		{VariantCurtain, "curtain"},
		{VariantDamper, "damper"},
		{VariantShade, "shade"},
		{VariantWindow, "window"},
		{VariantGarage, "garage"},
	}
	for _, tc := range cases {
		if got := VariantString(tc.v); got != tc.want {
			t.Errorf("VariantString(%d) = %q, want %q", tc.v, got, tc.want)
		}
	}
}

// TestCoverHADiscoveryPayload_VariantDeviceClass verifies that each
// CoverVariant produces the correct HA device_class in the discovery
// payload. Covers VariantShutter (default), VariantAwning, VariantCurtain,
// VariantDamper, VariantShade, and VariantWindow.
func TestCoverHADiscoveryPayload_VariantDeviceClass(t *testing.T) {
	t.Parallel()
	cases := []struct {
		variant CoverVariant
		want    string
	}{
		{VariantShutter, "shutter"},
		{VariantAwning, "awning"},
		{VariantCurtain, "curtain"},
		{VariantDamper, "damper"},
		{VariantShade, "shade"},
		{VariantWindow, "window"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
			c.Variant = tc.variant
			_, body := c.HADiscoveryPayload(discoveryCtx{})
			if v, _ := body["device_class"].(string); v != tc.want {
				t.Errorf("device_class = %q, want %q", v, tc.want)
			}
		})
	}
}

// TestCoverHADiscoveryPayload_DefaultVariantIsShutter verifies that a
// Cover constructed without an explicit variant defaults to "shutter".
func TestCoverHADiscoveryPayload_DefaultVariantIsShutter(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	// Variant field is not set — must be zero value (VariantShutter).
	if c.Variant != VariantShutter {
		t.Fatalf("default Variant = %d, want VariantShutter (%d)", c.Variant, VariantShutter)
	}
	_, body := c.HADiscoveryPayload(discoveryCtx{})
	if v, _ := body["device_class"].(string); v != "shutter" {
		t.Errorf("default device_class = %q, want %q", v, "shutter")
	}
}

// TestBlindHADiscoveryPayload_VariantDeviceClass verifies that a Blind
// with VariantShade (HmIP-HDM) emits "shade" and a Blind with no
// explicit variant emits "blind".
func TestBlindHADiscoveryPayload_VariantDeviceClass(t *testing.T) {
	t.Parallel()

	t.Run("no variant → blind", func(t *testing.T) {
		t.Parallel()
		b := newBlindRig(t, "HmIP-BBL:3", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindIP)
		_, body := b.HADiscoveryPayload(discoveryCtx{})
		if v, _ := body["device_class"].(string); v != "blind" {
			t.Errorf("device_class = %q, want %q", v, "blind")
		}
	})

	t.Run("VariantShade → shade", func(t *testing.T) {
		t.Parallel()
		b := NewBlind(BlindConfig{
			Writer:       &putWriter{},
			Capabilities: custom.CoverCapabilities{SupportsTilt: true},
			Kind:         BlindKindIP,
			Variant:      VariantShade,
		})
		_, body := b.HADiscoveryPayload(discoveryCtx{})
		if v, _ := body["device_class"].(string); v != "shade" {
			t.Errorf("device_class = %q, want %q", v, "shade")
		}
	})

	t.Run("VariantCurtain → curtain", func(t *testing.T) {
		t.Parallel()
		b := NewBlind(BlindConfig{
			Writer:       &putWriter{},
			Capabilities: custom.CoverCapabilities{SupportsTilt: true},
			Kind:         BlindKindIP,
			Variant:      VariantCurtain,
		})
		_, body := b.HADiscoveryPayload(discoveryCtx{})
		if v, _ := body["device_class"].(string); v != "curtain" {
			t.Errorf("device_class = %q, want %q", v, "curtain")
		}
	})
}

// newDeviceWithModel constructs a device.Device with the given model string.
// Used for variant-detection tests that inspect ch.Device().Model.
func newDeviceWithModel(model string) *device.Device {
	return device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001", Model: model})
}

// TestCoverVariantFromModelHmSecWin pins that a channel whose device model
// is HM-Sec-Win maps to VariantWindow.
func TestCoverVariantFromModelHmSecWin(t *testing.T) {
	t.Parallel()
	d := newDeviceWithModel("HM-Sec-Win")
	ch := d.AddChannel("HM-Sec-Win:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	got := coverVariantFromModel(ch)
	if got != VariantWindow {
		t.Errorf("coverVariantFromModel(HM-Sec-Win) = %v, want VariantWindow", got)
	}
}

// TestCoverVariantFromModelDefault pins that unknown models map to
// VariantShutter (the zero value / safe default).
func TestCoverVariantFromModelDefault(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"HmIP-BROLL", "HmIP-FROLL", "HM-LC-Bl1-FM"} {
		d := newDeviceWithModel(model)
		ch := d.AddChannel(model+":1", 1, "BLIND", hmenum.ParamsetKeyValues)
		got := coverVariantFromModel(ch)
		if got != VariantShutter {
			t.Errorf("coverVariantFromModel(%q) = %v, want VariantShutter", model, got)
		}
	}
}

// TestCoverVariantFromModelNil pins that nil channel and nil device
// return VariantShutter without panic.
func TestCoverVariantFromModelNil(t *testing.T) {
	t.Parallel()
	if got := coverVariantFromModel(nil); got != VariantShutter {
		t.Errorf("coverVariantFromModel(nil) = %v, want VariantShutter", got)
	}
}

// --- StatePayload state-string tests ---

func TestCoverStatePayload_StateString(t *testing.T) {
	t.Parallel()
	c, _, level := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})

	// No level observed: not closed, not moving → "open".
	sp, _ := c.State().(*payload.CoverState)
	if sp.State != "open" {
		t.Errorf("initial state = %q, want %q", sp.State, "open")
	}

	// Level 0 → closed.
	level.OnEvent(0.0)
	sp, _ = c.State().(*payload.CoverState)
	if sp.State != "closed" {
		t.Errorf("level=0 state = %q, want %q", sp.State, "closed")
	}

	// Level 0.5 + DirectionUp → opening.
	level.OnEvent(0.5)
	c.OnDirection(DirectionUp)
	sp, _ = c.State().(*payload.CoverState)
	if sp.State != "opening" {
		t.Errorf("opening state = %q, want %q", sp.State, "opening")
	}

	// DirectionDown → closing.
	c.OnDirection(DirectionDown)
	sp, _ = c.State().(*payload.CoverState)
	if sp.State != "closing" {
		t.Errorf("closing state = %q, want %q", sp.State, "closing")
	}
}

func TestBlindStatePayload_StateString(t *testing.T) {
	t.Parallel()
	w := &putWriter{}
	b := newBlindRig(t, "HmIP-BBL:3", w, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)

	// No level observed → "open".
	sp, _ := b.State().(*payload.BlindState)
	if sp.State != "open" {
		t.Errorf("initial state = %q, want %q", sp.State, "open")
	}

	// Feed level 0 (closed) via the underlying Float.
	b.OnEvent(0.0)
	sp, _ = b.State().(*payload.BlindState)
	if sp.State != "closed" {
		t.Errorf("level=0 state = %q, want %q", sp.State, "closed")
	}
}

func TestGarageStatePayload_StateString(t *testing.T) {
	t.Parallel()
	g := NewGarage(GarageConfig{Writer: &stubWriter{}})

	// No state observed → "open" (not closed, not moving).
	sp, _ := g.State().(*payload.GarageState)
	if sp.State != "open" {
		t.Errorf("initial garage state = %q, want %q", sp.State, "open")
	}

	// State = CLOSED → "closed".
	g.OnState(DoorStateClosed)
	sp, _ = g.State().(*payload.GarageState)
	if sp.State != "closed" {
		t.Errorf("CLOSED state = %q, want %q", sp.State, "closed")
	}

	// State = OPEN + section = opening (2) → "opening".
	g.OnState(DoorStateOpen)
	g.OnSection(sectionOpening)
	sp, _ = g.State().(*payload.GarageState)
	if sp.State != "opening" {
		t.Errorf("OPEN+opening state = %q, want %q", sp.State, "opening")
	}

	// Section = closing (5) → "closing".
	g.OnSection(sectionClosing)
	sp, _ = g.State().(*payload.GarageState)
	if sp.State != "closing" {
		t.Errorf("closing state = %q, want %q", sp.State, "closing")
	}
}
