// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package combined_test

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// projectionContext is a discovery context with fixed topics. Translate
// echoes its key so an assertion can tell a catalogue lookup from a
// hard-coded string, and ParameterLabel is configurable per case because
// the timer's label resolution depends on it.
type projectionContext struct {
	parameterLabel string
	hasLabel       bool
}

func (projectionContext) CombinedStateTopic() string   { return "base/combined/kind" }
func (projectionContext) CombinedCommandTopic() string { return "base/combined/kind/set" }
func (projectionContext) Translate(key string) string  { return key }
func (c projectionContext) ParameterLabel(hmenum.Parameter) (string, bool) {
	return c.parameterLabel, c.hasLabel
}

// TestCombinedProjectionBodiesAreUnchanged pins the discovery body each
// migrated projection emits.
//
// These three data points had their bodies assembled by per-kind builders
// in the MQTT adapter before the projection seam existed. Moving that
// assembly into the model must not change a single key: every one of them
// is already published in a retained discovery message, and a changed key
// set is a silently different entity on every installation that upgrades.
//
// The expected maps are written out in full rather than derived, because
// a expectation derived from the same code it checks agrees with any
// change that code makes.
func TestCombinedProjectionBodiesAreUnchanged(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		projection    payload.CombinedProjection
		ctx           projectionContext
		wantComponent string
		wantBody      map[string]any
	}{
		{
			name:          "timer without a catalogue label",
			projection:    combined.NewTimer("VCU0000001:1", nil, "DURATION_VALUE", "DURATION_UNIT"),
			wantComponent: "number",
			wantBody: map[string]any{
				"name":                "discovery.duration",
				"command_topic":       "base/combined/kind/set",
				"min":                 float64(0),
				"max":                 float64(86400),
				"step":                float64(1),
				"unit_of_measurement": "s",
				"entity_category":     "config",
				"mode":                "box",
				"optimistic":          false,
			},
		},
		{
			name:       "timer strips the German value prefix",
			projection: combined.NewTimer("VCU0000001:1", nil, "DURATION_VALUE", "DURATION_UNIT"),
			// The OCCU catalogue translates the DURATION_VALUE wire
			// parameter; the "Wert " prefix is dropped so HA's entity_id
			// derivation does not stutter.
			ctx:           projectionContext{parameterLabel: "Wert Zeitdauer", hasLabel: true},
			wantComponent: "number",
			wantBody: map[string]any{
				"name":                "Zeitdauer",
				"command_topic":       "base/combined/kind/set",
				"min":                 float64(0),
				"max":                 float64(86400),
				"step":                float64(1),
				"unit_of_measurement": "s",
				"entity_category":     "config",
				"mode":                "box",
				"optimistic":          false,
			},
		},
		{
			name:          "timer strips the English value prefix",
			projection:    combined.NewTimer("VCU0000001:1", nil, "DURATION_VALUE", "DURATION_UNIT"),
			ctx:           projectionContext{parameterLabel: "Value Duration", hasLabel: true},
			wantComponent: "number",
			wantBody: map[string]any{
				"name":                "Duration",
				"command_topic":       "base/combined/kind/set",
				"min":                 float64(0),
				"max":                 float64(86400),
				"step":                float64(1),
				"unit_of_measurement": "s",
				"entity_category":     "config",
				"mode":                "box",
				"optimistic":          false,
			},
		},
		{
			name:          "level combined",
			projection:    combined.NewLevelCombined("VCU0000001:1", nil, "LEVEL", "LEVEL_2", "LEVEL_COMBINED"),
			wantComponent: "sensor",
			wantBody: map[string]any{
				"name":            "discovery.level_combined",
				"value_template":  "{{ value_json.level }}",
				"entity_category": "diagnostic",
			},
		},
		{
			name:          "hs colour",
			projection:    combined.NewHSColor("VCU0000001:1", nil, "HUE", "SATURATION"),
			wantComponent: "sensor",
			wantBody: map[string]any{
				"name":            "discovery.hs_color",
				"value_template":  "{{ value_json.hue }}",
				"entity_category": "diagnostic",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			component, body := tc.projection.HACombinedDiscovery(tc.ctx)
			if component != tc.wantComponent {
				t.Errorf("component = %q, want %q", component, tc.wantComponent)
			}
			if !maps.Equal(bodyToComparable(body), bodyToComparable(tc.wantBody)) {
				t.Errorf("body = %v,\nwant %v", body, tc.wantBody)
			}
		})
	}
}

// bodyToComparable flattens a discovery body so maps.Equal can compare
// it. Every value these projections emit is already comparable; the
// helper exists to fail loudly if one stops being.
func bodyToComparable(body map[string]any) map[string]string {
	out := make(map[string]string, len(body))
	for k, v := range body {
		out[k] = valueKey(v)
	}
	return out
}

func valueKey(v any) string {
	switch x := v.(type) {
	case string:
		return "s:" + x
	case bool:
		if x {
			return "b:true"
		}
		return "b:false"
	case float64:
		return "f:" + formatFloatKey(x)
	default:
		return "?:unexpected"
	}
}

func formatFloatKey(f float64) string {
	// Integral values render without a fraction so the expectation stays
	// readable; the projections only emit integral bounds today.
	if f == float64(int64(f)) {
		return time.Duration(int64(f)).String()
	}
	return time.Duration(f).String()
}

// TestCombinedProjectionKindsAreStable pins the retained-topic segment of
// each projection. The kind is part of the wire contract: renaming one
// orphans every retained message published under the old segment.
func TestCombinedProjectionKindsAreStable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		projection payload.CombinedProjection
		want       string
	}{
		{combined.NewTimer("VCU0000001:1", nil, "DURATION_VALUE", "DURATION_UNIT"), "duration"},
		{combined.NewLevelCombined("VCU0000001:1", nil, "LEVEL", "LEVEL_2", "LEVEL_COMBINED"), "level_combined"},
		{combined.NewHSColor("VCU0000001:1", nil, "HUE", "SATURATION"), "hs_color"},
	}
	for _, tc := range cases {
		if got := tc.projection.CombinedKind(); got != tc.want {
			t.Errorf("CombinedKind() = %q, want %q", got, tc.want)
		}
	}
}

// TestCombinedProjectionStatePayloads pins the rendered state of each
// migrated projection, including the "not observed yet" case.
//
// An unobserved projection must report observed=false rather than a zero
// value: the bridge publishes nothing in that case, and a zero on a
// retained topic is a reading every subscriber then believes.
func TestCombinedProjectionStatePayloads(t *testing.T) {
	t.Parallel()

	t.Run("timer", func(t *testing.T) {
		t.Parallel()
		timer := combined.NewTimer("VCU0000001:1", nil, "DURATION_VALUE", "DURATION_UNIT")
		if _, observed := timer.CombinedStatePayload(); observed {
			t.Fatal("an unobserved timer must not report a state")
		}
		timer.OnComponents(90, combined.TimerUnitSeconds)
		state, observed := timer.CombinedStatePayload()
		if !observed || state != "90" {
			t.Fatalf("state = (%q, %v), want (\"90\", true)", state, observed)
		}
	})

	t.Run("level combined", func(t *testing.T) {
		t.Parallel()
		lc := combined.NewLevelCombined("VCU0000001:1", nil, "LEVEL", "LEVEL_2", "LEVEL_COMBINED")
		if _, observed := lc.CombinedStatePayload(); observed {
			t.Fatal("an unobserved level pair must not report a state")
		}
		lc.OnLevel(0.5)
		lc.OnSlatsLevel(0.25)
		state, observed := lc.CombinedStatePayload()
		if !observed || state != `{"level":0.5,"slats":0.25}` {
			t.Fatalf("state = (%q, %v)", state, observed)
		}
	})

	t.Run("hs colour", func(t *testing.T) {
		t.Parallel()
		hs := combined.NewHSColor("VCU0000001:1", nil, "HUE", "SATURATION")
		if _, observed := hs.CombinedStatePayload(); observed {
			t.Fatal("an unobserved colour pair must not report a state")
		}
		hs.OnHue(120)
		hs.OnSaturation(1.0)
		state, observed := hs.CombinedStatePayload()
		if !observed || state != `{"hue":120,"saturation":100}` {
			t.Fatalf("state = (%q, %v)", state, observed)
		}
	})
}

// TestCombinedProjectionOnCombinedChangeFires pins the live-update seam.
// The event bridge re-reads the state on this callback, so a projection
// whose callback never fires leaves its retained topic stale forever.
func TestCombinedProjectionOnCombinedChangeFires(t *testing.T) {
	t.Parallel()

	t.Run("timer", func(t *testing.T) {
		t.Parallel()
		timer := combined.NewTimer("VCU0000001:1", nil, "DURATION_VALUE", "DURATION_UNIT")
		fired := 0
		unsub := timer.OnCombinedChange(func() { fired++ })
		defer unsub()
		timer.OnComponents(30, combined.TimerUnitSeconds)
		if fired == 0 {
			t.Error("timer change did not reach the projection subscriber")
		}
	})

	t.Run("level combined", func(t *testing.T) {
		t.Parallel()
		lc := combined.NewLevelCombined("VCU0000001:1", nil, "LEVEL", "LEVEL_2", "LEVEL_COMBINED")
		fired := 0
		unsub := lc.OnCombinedChange(func() { fired++ })
		defer unsub()
		lc.OnLevel(0.5)
		lc.OnSlatsLevel(0.25)
		if fired == 0 {
			t.Error("level change did not reach the projection subscriber")
		}
	})

	t.Run("hs colour", func(t *testing.T) {
		t.Parallel()
		hs := combined.NewHSColor("VCU0000001:1", nil, "HUE", "SATURATION")
		fired := 0
		unsub := hs.OnCombinedChange(func() { fired++ })
		defer unsub()
		hs.OnHue(120)
		hs.OnSaturation(1.0)
		if fired == 0 {
			t.Error("colour change did not reach the projection subscriber")
		}
	})
}

// TestTimerWriteCombinedParsesSeconds pins the MQTT write path: HA's
// number entity publishes a bare decimal, and the data point parses it
// rather than the transport guessing at the type.
func TestTimerWriteCombinedParsesSeconds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		raw     string
		wantErr bool
		want    time.Duration
	}{
		{name: "integer seconds", raw: "30", want: 30 * time.Second},
		{name: "fractional seconds", raw: "1.5", want: 1500 * time.Millisecond},
		{name: "surrounding whitespace", raw: " 45 ", want: 45 * time.Second},
		{name: "not a number", raw: "abc", wantErr: true},
		{name: "negative", raw: "-1", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &durationWriter{}
			timer := combined.NewTimer("VCU0000001:1", w, "DURATION_VALUE", "DURATION_UNIT")
			err := timer.WriteCombined(context.Background(), tc.raw, hmenum.CommandPriorityHigh)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("WriteCombined(%q) must fail", tc.raw)
				}
				if w.calls != 0 {
					t.Error("a rejected payload must not reach the device")
				}
				return
			}
			if err != nil {
				t.Fatalf("WriteCombined(%q): %v", tc.raw, err)
			}
			if w.calls == 0 {
				t.Fatal("WriteCombined wrote nothing")
			}
		})
	}
}

// durationWriter counts writes so a test can tell a dispatched command
// from a rejected one.
type durationWriter struct{ calls int }

func (w *durationWriter) SetValue(
	context.Context, string, hmenum.Parameter, any, hmenum.CommandPriority,
) error {
	w.calls++
	return nil
}

// --- EnumSelect wire binding ---------------------------------------

// newEnumChannel builds a channel carrying a read-only ENUM parameter
// projected onto an integer sensor — the shape the resolver produces for
// DOOR_STATE, where the wire value is a 0-based index into VALUE_LIST.
func newEnumChannel(t *testing.T, address string, valueList []string) (*device.Channel, *generic.Sensor[int32]) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MOD0001"})
	ch := d.AddChannel(address, 1, "GARAGE_DOOR", hmenum.ParamsetKeyValues)
	sensor := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "DOOR_STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  valueList,
		},
	})
	ch.Put(sensor)
	return ch, sensor
}

// TestEnumSelectSubscribeResolvesTheIndexAgainstTheValueList pins the
// read binding.
//
// A read-only ENUM arrives as a 0-based index on an integer sensor, not
// as its token — resolving it against VALUE_LIST is the whole job. A
// binding that took the raw number as a mode would match no mode at all
// and leave the control permanently valueless.
func TestEnumSelectSubscribeResolvesTheIndexAgainstTheValueList(t *testing.T) {
	t.Parallel()
	// The list order is the device's, and VENTILATION_POSITION sits at an
	// index that is not its position in the select — which is exactly why
	// the lookup has to go through the list.
	ch, sensor := newEnumChannel(t, "MOD0001:1",
		[]string{"CLOSED", "OPEN", "VENTILATION_POSITION", "POSITION_UNKNOWN"})
	e := newGarageSelect(&recordingWriter{})

	unsub := e.Subscribe(ch)
	if unsub == nil {
		t.Fatal("Subscribe returned nil for a channel that carries the state parameter")
	}
	defer unsub()

	sensor.OnEvent(2) // VENTILATION_POSITION
	if v, ok := e.Value(); !ok || v != "VENTILATION_POSITION" {
		t.Fatalf("Value() = (%q, %v), want VENTILATION_POSITION — the index was not resolved", v, ok)
	}

	// POSITION_UNKNOWN is a token, but not a selectable mode. With no
	// command outstanding — the door was started at the wall button — the
	// select reports no value: the daemon does not know which stop the
	// drive is heading for, and naming one would be a guess a controller
	// cannot tell from an observation. The hold covers the other case,
	// where a command said where it is going; see
	// TestEnumSelectHoldsTheCommandedModeWhileTravelling.
	sensor.OnEvent(3)
	if v, ok := e.Value(); ok {
		t.Fatalf("Value() = (%q, true) while travelling with no command outstanding, want no value", v)
	}

	sensor.OnEvent(0) // CLOSED
	if v, _ := e.Value(); v != "CLOSED" {
		t.Fatalf("Value() = %q, want CLOSED", v)
	}
}

// TestEnumSelectSubscribeHoldsACommandedModeThroughTravel is the other
// half of the case above: with a command outstanding the select keeps
// showing where the drive is heading, across the non-mode token the wire
// reports mid-travel.
func TestEnumSelectSubscribeHoldsACommandedModeThroughTravel(t *testing.T) {
	t.Parallel()
	ch, sensor := newEnumChannel(t, "MOD0004:1",
		[]string{"CLOSED", "OPEN", "VENTILATION_POSITION", "POSITION_UNKNOWN"})
	e := newGarageSelect(&recordingWriter{})
	unsub := e.Subscribe(ch)
	defer unsub()

	sensor.OnEvent(0) // CLOSED
	if err := e.SetMode(context.Background(), "OPEN", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetMode: %v", err)
	}

	sensor.OnEvent(3) // travelling
	if v, ok := e.Value(); !ok || v != "OPEN" {
		t.Fatalf("Value() = (%q, %v) while travelling toward OPEN, want the commanded OPEN", v, ok)
	}

	sensor.OnEvent(1) // arrived
	if v, _ := e.Value(); v != "OPEN" {
		t.Fatalf("Value() = %q after arrival, want OPEN", v)
	}
}

// TestEnumSelectSubscribeSeedsFromAnAlreadyObservedValue pins that a
// value present before Subscribe runs is picked up.
//
// After a cache hydration the wire value is already there and no further
// update is coming, so a binding that only listens starts blank and stays
// blank until the device happens to change.
func TestEnumSelectSubscribeSeedsFromAnAlreadyObservedValue(t *testing.T) {
	t.Parallel()
	ch, sensor := newEnumChannel(t, "MOD0002:1",
		[]string{"CLOSED", "OPEN", "VENTILATION_POSITION", "POSITION_UNKNOWN"})
	sensor.OnEvent(1) // OPEN, before anyone subscribes

	e := newGarageSelect(&recordingWriter{})
	unsub := e.Subscribe(ch)
	if unsub != nil {
		defer unsub()
	}
	if v, ok := e.Value(); !ok || v != "OPEN" {
		t.Fatalf("Value() = (%q, %v) after Subscribe, want the already-observed OPEN", v, ok)
	}
}

// TestEnumSelectSubscribeDeclinesAChannelWithoutTheStateParameter pins
// that a channel missing the read parameter leaves the data point
// valueless rather than reporting a mode it cannot observe.
func TestEnumSelectSubscribeDeclinesAChannelWithoutTheStateParameter(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MOD0003"})
	ch := d.AddChannel("MOD0003:1", 1, "GARAGE_DOOR", hmenum.ParamsetKeyValues)

	e := newGarageSelect(&recordingWriter{})
	if unsub := e.Subscribe(ch); unsub != nil {
		t.Error("Subscribe must decline a channel that does not carry the state parameter")
	}
	if _, ok := e.Value(); ok {
		t.Error("an unbound select must report no value")
	}
	if unsub := e.Subscribe(nil); unsub != nil {
		t.Error("Subscribe(nil) must decline")
	}
}

// TestEnumSelectIdentity pins the data point's identity surface: the
// combined marker channels filter on, and the key that scopes it.
func TestEnumSelectIdentity(t *testing.T) {
	t.Parallel()
	e := newGarageSelect(&recordingWriter{})
	if !e.IsCombined() {
		t.Error("IsCombined() must be true, or Channel.CombinedDataPoints never surfaces it")
	}
	key := e.DataPointKey()
	if key.Parameter != "DOOR_MODE" {
		t.Errorf("key.Parameter = %q, want DOOR_MODE — the synthetic name must not collide "+
			"with either wire parameter on the channel", key.Parameter)
	}
	if key.ChannelAddress != "VCU0000001:1" {
		t.Errorf("key.ChannelAddress = %q", key.ChannelAddress)
	}
	if got := e.StateParameter(); got != "DOOR_STATE" {
		t.Errorf("StateParameter() = %q, want DOOR_STATE", got)
	}
}

// TestEnumSelectDiscoveryDeclinesWithoutAContext pins the nil-context
// guard every projection carries.
func TestEnumSelectDiscoveryDeclinesWithoutAContext(t *testing.T) {
	t.Parallel()
	e := newGarageSelect(&recordingWriter{})
	if component, body := e.HACombinedDiscovery(nil); component != "" || body != nil {
		t.Errorf("HACombinedDiscovery(nil) = (%q, %v), want a declined projection", component, body)
	}
}

// TestEnumSelectSetModeWithoutAWriterFails pins that a select with no
// writer reports the failure rather than silently accepting a mode it
// cannot dispatch.
func TestEnumSelectSetModeWithoutAWriterFails(t *testing.T) {
	t.Parallel()
	e := combined.NewEnumSelect(combined.EnumSelectConfig{
		Address:           "VCU0000001:1",
		Kind:              "door_mode",
		CombinedParameter: "DOOR_MODE",
		StateParameter:    "DOOR_STATE",
		CommandParameter:  "DOOR_COMMAND",
		Modes:             garageModes(),
	})
	if err := e.SetMode(context.Background(), "OPEN", hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("SetMode without a writer must fail")
	}
}
