// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"context"
	"fmt"
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

func (discoveryCtx) WireParameterStateTopicOn(_, parameter string) string {
	return discoveryCtx{}.WireParameterStateTopic(parameter)
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

// TestCoverHADiscoveryPayload_CommandTopicDrivesOpenCloseStop pins that
// HA's three cover buttons reach the cover's Open / Close / Stop
// operations. HA publishes payload_open / payload_close / payload_stop to
// the one `command_topic` an MQTT cover entity has, so that topic must
// accept all three tokens: aimed at a wire parameter instead, the "open"
// payload writes a truthy value to the boolean STOP action and halts the
// cover, while the "close" payload writes STOP=false, which the actuator
// ignores entirely.
func TestCoverHADiscoveryPayload_CommandTopicDrivesOpenCloseStop(t *testing.T) {
	t.Parallel()
	w := &putWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{SupportsPosition: true, SupportsStop: true})
	ctx := discoveryCtx{}
	_, body := c.HADiscoveryPayload(ctx)

	// The method name is part of the published contract — HA writes to the
	// topic derived from it — so it is pinned literally here.
	const method = "cover_command"
	if got, _ := body["command_topic"].(string); got != ctx.ServiceMethodCommandTopic(method) {
		t.Fatalf("command_topic = %q, want the %q service-method topic %q",
			got, method, ctx.ServiceMethodCommandTopic(method))
	}

	cases := []struct {
		field string
		param hmenum.Parameter
		value any
	}{
		{"payload_open", hmenum.ParameterLevel, 1.0},
		{"payload_close", hmenum.ParameterLevel, 0.0},
		{"payload_stop", hmenum.ParameterStop, true},
	}
	for _, tc := range cases {
		token, _ := body[tc.field].(string)
		if token == "" {
			t.Fatalf("%s missing from the discovery payload", tc.field)
		}
		w.mu.Lock()
		w.calls = nil
		w.mu.Unlock()
		// The MQTT bridge wraps a bare payload under the method's globally
		// registered scalar-argument key before invoking the service.
		params := map[string]any{payload.GlobalScalarArgKey(method): token}
		if err := c.Invoke(context.Background(), method, params, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("%s (%q): %v", tc.field, token, err)
		}
		w.mu.Lock()
		calls := append([]setCall(nil), w.calls...)
		w.mu.Unlock()
		if len(calls) != 1 {
			t.Fatalf("%s (%q): %d wire writes, want exactly 1: %+v", tc.field, token, len(calls), calls)
		}
		if calls[0].param != tc.param || calls[0].value != tc.value {
			t.Errorf("%s (%q) wrote %v=%v, want %v=%v",
				tc.field, token, calls[0].param, calls[0].value, tc.param, tc.value)
		}
	}
}

// TestCoverInvoke_UnknownCommandTokenRejected pins that an unrecognised
// token on the multiplexed command topic fails loudly instead of being
// coerced into one of the three motions.
func TestCoverInvoke_UnknownCommandTokenRejected(t *testing.T) {
	t.Parallel()
	w := &putWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{SupportsPosition: true, SupportsStop: true})
	err := c.Invoke(context.Background(), "cover_command",
		map[string]any{"command": "TILT"}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("unknown command token must return an error")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.calls) != 0 {
		t.Errorf("unknown command token wrote to the wire: %+v", w.calls)
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

// TestBlindHADiscoveryPayload_CommandTopicUsesBlindOverrides pins that the
// multiplexed cover command reaches the Blind's own Open / Close — not the
// inherited Cover ones. A Blind drives both axes through the combined
// parameter (LEVEL_COMBINED for HM, COMBINED_PARAMETER for HmIP); a plain
// LEVEL write leaves the slats where they were.
func TestBlindHADiscoveryPayload_CommandTopicUsesBlindOverrides(t *testing.T) {
	t.Parallel()
	w := &putWriter{}
	b := newBlindRig(t, "HmIP-BBL:3", w, custom.CoverCapabilities{SupportsPosition: true, SupportsTilt: true}, BlindKindHM)
	ctx := discoveryCtx{}
	_, body := b.HADiscoveryPayload(ctx)

	const method = "cover_command"
	if got, _ := body["command_topic"].(string); got != ctx.ServiceMethodCommandTopic(method) {
		t.Fatalf("command_topic = %q, want %q", got, ctx.ServiceMethodCommandTopic(method))
	}
	token, _ := body["payload_open"].(string)
	if token == "" {
		t.Fatal("payload_open missing from the discovery payload")
	}
	params := map[string]any{payload.GlobalScalarArgKey(method): token}
	if err := b.Invoke(context.Background(), method, params, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Invoke(%q, %q): %v", method, token, err)
	}
	cc := w.combinedCalls()
	if len(cc) != 1 {
		t.Fatalf("payload_open produced %d combined writes, want 1 (Blind.Open was bypassed)", len(cc))
	}
	// level=1.0 → int(1.0*100*2)=200=0xc8; tilt held at 0 → "0xc8,0x00".
	if got, _ := cc[0].value.(string); got != "0xc8,0x00" {
		t.Errorf("LEVEL_COMBINED = %v, want %q", cc[0].value, "0xc8,0x00")
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

// TestGarageHADiscoveryPayloadCarriesNoVentKey pins that the cover body
// declares nothing for the ventilation position, whatever the drive
// supports.
//
// It used to carry a vent_command_topic. HA's MQTT cover platform
// validates the discovery body against a closed key schema and has no
// field for a vent command, so the key was dropped before any entity saw
// it — a control that looked declared and did nothing. A vent-capable
// drive now gets a separate select entity, which HA renders as a real
// control and can read back.
func TestGarageHADiscoveryPayloadCarriesNoVentKey(t *testing.T) {
	t.Parallel()

	for _, supportsVent := range []bool{true, false} {
		t.Run(fmt.Sprintf("SupportsVent=%v", supportsVent), func(t *testing.T) {
			t.Parallel()
			g := NewGarage(GarageConfig{
				Writer:       &stubWriter{},
				Capabilities: custom.CoverCapabilities{SupportsVent: supportsVent},
			})
			_, body := g.HADiscoveryPayload(discoveryCtx{})
			if _, ok := body["vent_command_topic"]; ok {
				t.Error("the cover body must carry no vent key: HA's cover schema has no field " +
					"for one, so it is dropped before any entity sees it. The ventilation " +
					"position is a separate select entity (Garage.attachDoorMode)")
			}
		})
	}
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
