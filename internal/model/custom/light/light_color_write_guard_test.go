// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for the color/kelvin/effect write-guard logic (suppressing redundant
// writes when the commanded state matches the current state), for
// kelvinBoundsFromChannel reading MIN/MAX from the COLOR_TEMPERATURE
// descriptor, and for the baseDP timestamp delegation methods on Light.

package light

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── SetColor write-guard ────────────────────────────────────────────────────

// TestSetColorRepeatStillWritesAfterObservation measures what the colour
// write-guard actually does with a repeated command: nothing. The check it
// runs (IsStateChangeFull) consults a commanded-colour accessor that holds
// no state, so the second write goes out even after the CCU has reported
// the commanded colour back. The name and body used to claim suppression
// and assert nothing; the property is pinned in
// TestHmLgtColourStateChangeFullAlwaysReportsAChange.
func TestSetColorRepeatStillWritesAfterObservation(t *testing.T) {
	t.Parallel()

	w := &colorStubWriter{}
	ch := newColorRig(t, "x", w, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true}})

	// First call always goes through (no prior state).
	if err := l.SetColor(context.Background(), 120, 0.8, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("first SetColor: %v", err)
	}
	writeCount := len(w.calls)
	if writeCount == 0 {
		t.Fatal("first SetColor must write to device")
	}

	// Drive the wire DP so the light sees the commanded color as its current state.
	if l.hue != nil {
		l.hue.OnEvent(int32(120))
	}
	if l.saturation != nil {
		l.saturation.OnEvent(0.8)
	}

	// Second call with identical values.
	if err := l.SetColor(context.Background(), 120, 0.8, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("second SetColor: %v", err)
	}
	if len(w.calls) == writeCount {
		t.Fatal("the repeat was suppressed — the commanded-colour accessors now carry state; update the doc comments in light.go / color.go and this test")
	}
}

// TestSetColorGuardAllowsChange verifies that a different hue always triggers
// a write.
func TestSetColorGuardAllowsChange(t *testing.T) {
	t.Parallel()

	w := &colorStubWriter{}
	ch := newColorRig(t, "x", w, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true}})

	if err := l.SetColor(context.Background(), 120, 0.8, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("first SetColor: %v", err)
	}
	before := len(w.calls)

	// Different hue — must write.
	if err := l.SetColor(context.Background(), 240, 0.8, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("second SetColor (different hue): %v", err)
	}
	if len(w.calls) == before {
		t.Error("SetColor with different hue must produce a write")
	}
}

// ─── SetKelvin write-guard ───────────────────────────────────────────────────

// TestSetKelvinRepeatStillWritesAfterObservation is the colour-temperature
// counterpart: the commanded-kelvin accessor holds no state, so a repeat of
// an already-observed value is written again. The old body asserted only
// that the FIRST call wrote, which no suppression rule can fail.
func TestSetKelvinRepeatStillWritesAfterObservation(t *testing.T) {
	t.Parallel()

	w := &colorStubWriter{}
	ch := newColorTempRig(t, "x", w, custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}, 2700, 6500)
	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}}, 2700, 6500)

	if err := l.SetKelvin(context.Background(), 4000, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("first SetKelvin: %v", err)
	}
	if len(w.calls) == 0 {
		t.Fatal("first SetKelvin must write to device")
	}
	if l.kelvin != nil {
		l.kelvin.OnEvent(int32(4000))
	}
	before := len(w.calls)
	if err := l.SetKelvin(context.Background(), 4000, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("second SetKelvin: %v", err)
	}
	if len(w.calls) == before {
		t.Fatal("the repeat was suppressed — the commanded-kelvin accessor now carries state; update the doc comments in light.go / color.go and this test")
	}
}

// TestSetKelvinGuardAllowsChange verifies that a different kelvin value
// triggers a write.
func TestSetKelvinGuardAllowsChange(t *testing.T) {
	t.Parallel()

	w := &colorStubWriter{}
	ch := newColorTempRig(t, "x", w, custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}, 2700, 6500)
	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}}, 2700, 6500)

	if err := l.SetKelvin(context.Background(), 3000, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("first SetKelvin: %v", err)
	}
	before := len(w.calls)

	if err := l.SetKelvin(context.Background(), 5000, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("second SetKelvin: %v", err)
	}
	if len(w.calls) == before {
		t.Error("SetKelvin with different kelvin value must produce a write")
	}
}

// ─── SetEffect write-guard ───────────────────────────────────────────────────

// TestSetEffectRepeatWithNoEffectList verifies the one effect path that
// does stop early: with no PROGRAM value list the label is "" for every
// index, and the write goes out at most once per call. It is NOT evidence
// of suppression — the commanded-effect accessor holds no state (see the
// hook block in light.go).
func TestSetEffectRepeatWithNoEffectList(t *testing.T) {
	t.Parallel()

	w := &colorStubWriter{}
	ch := newEffectRig(t, "x", w, custom.LightCapabilities{SupportsEffects: true, Dimmable: true})

	// Inject some effect labels.
	if dp := ch.Parameter(hmenum.ParameterProgram); dp != nil {
		// We cannot set ValueList after construction; effects come from the
		// constructor's ValueList reading. Build the EffectLight with a
		// separate channel that carries a PROGRAM DP with ValueList.
		_ = dp
	}
	l := NewEffectLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsEffects: true, Dimmable: true}})

	// Without effects registered the SetEffect path short-circuits with no
	// write (empty effects list → effectLabel = "" → guard fires). Verify at
	// least that no write occurs on repeat calls.
	_ = l.SetEffect(context.Background(), 0, hmenum.CommandPriorityHigh)
	before := len(w.calls)
	_ = l.SetEffect(context.Background(), 0, hmenum.CommandPriorityHigh)
	// Second call must not have produced MORE writes than the first.
	if len(w.calls) > before+1 {
		t.Error("SetEffect must not write more than once for the same effect")
	}
}

// TestSetEffectGuardAllowsChange verifies that switching to a different effect
// index triggers a write even when the first call was suppressed.
func TestSetEffectGuardAllowsChange(t *testing.T) {
	t.Parallel()

	w := &colorStubWriter{}
	// Build an EffectLight with two effect labels wired through ParameterProgram.
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("x", 1, "EFFECT", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "x", hmenum.ParameterLevel, w)
	putWritableInteger(ch, "x", hmenum.ParameterHue, w)
	putWritableFloat(ch, "x", hmenum.ParameterSaturation, w)
	// Program DP with ValueList so EffectLight sees two effects.
	ch.Put(generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "x",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterProgram),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  []string{"Off", "Slow"},
		},
		Writer: w,
	}))

	l := NewEffectLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsEffects: true, Dimmable: true}})

	// First call — index 0.
	_ = l.SetEffect(context.Background(), 0, hmenum.CommandPriorityHigh)
	before := len(w.calls)

	// Second call — index 1 (different): must write.
	_ = l.SetEffect(context.Background(), 1, hmenum.CommandPriorityHigh)
	if len(w.calls) <= before {
		t.Error("SetEffect with different index must produce a write")
	}
}

// ─── kelvinBoundsFromChannel descriptor read ─────────────────────────────────

// TestKelvinBoundsFromChannelReadsDescriptor verifies that the constructor
// picks up MIN/MAX from the COLOR_TEMPERATURE descriptor instead of using the
// fixed 2000/6500 fallback.
func TestKelvinBoundsFromChannelReadsDescriptor(t *testing.T) {
	t.Parallel()

	const addr = "HmLC-DW:3"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(addr, 3, "CT_DIMMER", hmenum.ParamsetKeyValues)

	minRaw, _ := json.Marshal(2700.0)
	maxRaw, _ := json.Marshal(5000.0)

	ch.Put(generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}))
	ch.Put(generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterColorTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage(minRaw),
			Max:        json.RawMessage(maxRaw),
		},
	}))

	minK, maxK := kelvinBoundsFromChannel(ch)
	if minK != 2700 {
		t.Errorf("kelvinBoundsFromChannel minK = %d, want 2700", minK)
	}
	if maxK != 5000 {
		t.Errorf("kelvinBoundsFromChannel maxK = %d, want 5000", maxK)
	}
}

// TestKelvinBoundsFromChannelFallbackWhenAbsent verifies that (0,0) is
// returned when the channel has no COLOR_TEMPERATURE descriptor, causing
// NewColorTempLight to apply its built-in 2000/6500 defaults.
func TestKelvinBoundsFromChannelFallbackWhenAbsent(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("x", 1, "CT", hmenum.ParamsetKeyValues)

	minK, maxK := kelvinBoundsFromChannel(ch)
	if minK != 0 || maxK != 0 {
		t.Errorf("kelvinBoundsFromChannel without descriptor = (%d, %d), want (0, 0)", minK, maxK)
	}
}

// TestKelvinBoundsFromChannelNil verifies nil-safety.
func TestKelvinBoundsFromChannelNil(t *testing.T) {
	t.Parallel()

	minK, maxK := kelvinBoundsFromChannel(nil)
	if minK != 0 || maxK != 0 {
		t.Errorf("kelvinBoundsFromChannel(nil) = (%d, %d), want (0, 0)", minK, maxK)
	}
}

// TestNewColorTempLightUsesDescriptorBounds verifies that a ColorTempLight
// built via newColorTempConstructor propagates the channel descriptor bounds
// into MinKelvin / MaxKelvin.
func TestNewColorTempLightUsesDescriptorBounds(t *testing.T) {
	t.Parallel()

	const addr = "HmLC-DW:4"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(addr, 4, "CT_DIMMER", hmenum.ParamsetKeyValues)

	minRaw, _ := json.Marshal(3000.0)
	maxRaw, _ := json.Marshal(4500.0)

	ch.Put(generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr, ParamsetKey: hmenum.ParamsetKeyValues,
			Parameter: string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}))
	ch.Put(generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr, ParamsetKey: hmenum.ParamsetKeyValues,
			Parameter: string(hmenum.ParameterColorTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage(minRaw),
			Max:        json.RawMessage(maxRaw),
		},
	}))

	dp, err := newColorTempConstructor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor error: %v", err)
	}
	ctl, ok := dp.(*ColorTempLight)
	if !ok {
		t.Fatalf("expected *ColorTempLight, got %T", dp)
	}
	if ctl.MinKelvin != 3000 {
		t.Errorf("MinKelvin = %d, want 3000", ctl.MinKelvin)
	}
	if ctl.MaxKelvin != 4500 {
		t.Errorf("MaxKelvin = %d, want 4500", ctl.MaxKelvin)
	}
}

// ─── baseDP timestamp delegation ─────────────────────────────────────────────

// TestLightBaseDPMethodsExist verifies that Light exposes the baseDP delegation
// methods without panicking.
func TestLightBaseDPMethodsExist(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:1", w, custom.LightCapabilities{Dimmable: true})

	// These methods must compile and return zero-values on an un-driven Light.
	_, _ = l.LightModifiedAt()
	_, _ = l.LightRefreshedAt()
	_ = l.LightUnconfirmedLastValuesSend()
	l.MarkLightModified()
	l.MarkLightRefreshed()

	// After marking, timestamps must be non-zero.
	if _, ok := l.LightModifiedAt(); !ok {
		t.Error("LightModifiedAt() must be non-zero after MarkLightModified()")
	}
	if _, ok := l.LightRefreshedAt(); !ok {
		t.Error("LightRefreshedAt() must be non-zero after MarkLightRefreshed()")
	}
}
