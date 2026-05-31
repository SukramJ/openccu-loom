// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire_test

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestDecodeMoveToHueRoundTrip encodes all five fields and verifies each
// decoded field matches exactly.
func TestDecodeMoveToHueRoundTrip(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 200)
		e.PutUint(tlv.ContextTag(1), uint64(wire.ColorHueDirUp))
		e.PutUint(tlv.ContextTag(2), 300)
		e.PutUint(tlv.ContextTag(3), 1)
		e.PutUint(tlv.ContextTag(4), 2)
	})
	got, err := wire.DecodeMoveToHue(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Hue != 200 {
		t.Errorf("Hue = %d, want 200", got.Hue)
	}
	if got.Direction != wire.ColorHueDirUp {
		t.Errorf("Direction = %d, want %d (Up)", got.Direction, wire.ColorHueDirUp)
	}
	if got.TransitionTime != 300 {
		t.Errorf("TransitionTime = %d, want 300", got.TransitionTime)
	}
	if got.OptionsMask != 1 {
		t.Errorf("OptionsMask = %d, want 1", got.OptionsMask)
	}
	if got.OptionsOverride != 2 {
		t.Errorf("OptionsOverride = %d, want 2", got.OptionsOverride)
	}
}

// TestDecodeMoveToHueMissingTrailingFields verifies that only the first
// field (Hue) is populated when the remaining four are absent.
func TestDecodeMoveToHueMissingTrailingFields(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 128)
	})
	got, err := wire.DecodeMoveToHue(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Hue != 128 {
		t.Errorf("Hue = %d, want 128", got.Hue)
	}
	if got.Direction != 0 {
		t.Errorf("Direction = %d, want 0 (zero-value)", got.Direction)
	}
	if got.TransitionTime != 0 {
		t.Errorf("TransitionTime = %d, want 0 (zero-value)", got.TransitionTime)
	}
	if got.OptionsMask != 0 {
		t.Errorf("OptionsMask = %d, want 0 (zero-value)", got.OptionsMask)
	}
	if got.OptionsOverride != 0 {
		t.Errorf("OptionsOverride = %d, want 0 (zero-value)", got.OptionsOverride)
	}
}

// TestDecodeMoveToSaturationRoundTrip encodes all four fields and
// verifies the decoded struct matches exactly.
func TestDecodeMoveToSaturationRoundTrip(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 180)
		e.PutUint(tlv.ContextTag(1), 50)
		e.PutUint(tlv.ContextTag(2), 3)
		e.PutUint(tlv.ContextTag(3), 3)
	})
	got, err := wire.DecodeMoveToSaturation(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Saturation != 180 {
		t.Errorf("Saturation = %d, want 180", got.Saturation)
	}
	if got.TransitionTime != 50 {
		t.Errorf("TransitionTime = %d, want 50", got.TransitionTime)
	}
	if got.OptionsMask != 3 {
		t.Errorf("OptionsMask = %d, want 3", got.OptionsMask)
	}
	if got.OptionsOverride != 3 {
		t.Errorf("OptionsOverride = %d, want 3", got.OptionsOverride)
	}
}

// TestDecodeMoveToHueAndSaturationRoundTrip encodes all five fields and
// verifies each decoded field matches exactly.
func TestDecodeMoveToHueAndSaturationRoundTrip(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 120)
		e.PutUint(tlv.ContextTag(1), 240)
		e.PutUint(tlv.ContextTag(2), 100)
		e.PutUint(tlv.ContextTag(3), 1)
		e.PutUint(tlv.ContextTag(4), 1)
	})
	got, err := wire.DecodeMoveToHueAndSaturation(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Hue != 120 {
		t.Errorf("Hue = %d, want 120", got.Hue)
	}
	if got.Saturation != 240 {
		t.Errorf("Saturation = %d, want 240", got.Saturation)
	}
	if got.TransitionTime != 100 {
		t.Errorf("TransitionTime = %d, want 100", got.TransitionTime)
	}
	if got.OptionsMask != 1 {
		t.Errorf("OptionsMask = %d, want 1", got.OptionsMask)
	}
	if got.OptionsOverride != 1 {
		t.Errorf("OptionsOverride = %d, want 1", got.OptionsOverride)
	}
}

// TestDecodeMoveToHueAndSaturationOnlyMandatory verifies that absent
// trailing fields (TransitionTime, OptionsMask, OptionsOverride) default
// to zero when only Hue and Saturation are encoded.
func TestDecodeMoveToHueAndSaturationOnlyMandatory(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 60)
		e.PutUint(tlv.ContextTag(1), 200)
	})
	got, err := wire.DecodeMoveToHueAndSaturation(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Hue != 60 {
		t.Errorf("Hue = %d, want 60", got.Hue)
	}
	if got.Saturation != 200 {
		t.Errorf("Saturation = %d, want 200", got.Saturation)
	}
	if got.TransitionTime != 0 {
		t.Errorf("TransitionTime = %d, want 0 (zero-value)", got.TransitionTime)
	}
	if got.OptionsMask != 0 {
		t.Errorf("OptionsMask = %d, want 0 (zero-value)", got.OptionsMask)
	}
	if got.OptionsOverride != 0 {
		t.Errorf("OptionsOverride = %d, want 0 (zero-value)", got.OptionsOverride)
	}
}

// TestDecodeMoveToColorTemperatureRoundTrip encodes all four fields and
// verifies the decoded struct matches exactly.
func TestDecodeMoveToColorTemperatureRoundTrip(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 4000)
		e.PutUint(tlv.ContextTag(1), 20)
		e.PutUint(tlv.ContextTag(2), 2)
		e.PutUint(tlv.ContextTag(3), 2)
	})
	got, err := wire.DecodeMoveToColorTemperature(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ColorTemperatureMireds != 4000 {
		t.Errorf("ColorTemperatureMireds = %d, want 4000", got.ColorTemperatureMireds)
	}
	if got.TransitionTime != 20 {
		t.Errorf("TransitionTime = %d, want 20", got.TransitionTime)
	}
	if got.OptionsMask != 2 {
		t.Errorf("OptionsMask = %d, want 2", got.OptionsMask)
	}
	if got.OptionsOverride != 2 {
		t.Errorf("OptionsOverride = %d, want 2", got.OptionsOverride)
	}
}

// TestDecodeColorControlTruncatedPayload verifies that a truncated wire
// payload (struct-open with no body) wraps ErrColorControlMalformed.
func TestDecodeColorControlTruncatedPayload(t *testing.T) {
	t.Parallel()
	_, err := wire.DecodeMoveToHue([]byte{0x15}) // structure open only
	if !errors.Is(err, wire.ErrColorControlMalformed) {
		t.Fatalf("err = %v, want ErrColorControlMalformed", err)
	}
}

// TestDecodeColorControlNonStructTopLevel verifies that a payload whose
// top-level element is not a Structure wraps ErrColorControlMalformed.
func TestDecodeColorControlNonStructTopLevel(t *testing.T) {
	t.Parallel()
	e := tlv.NewEncoder()
	e.PutBool(tlv.AnonymousTag(), true)
	payload, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, gotErr := wire.DecodeMoveToHue(payload)
	if !errors.Is(gotErr, wire.ErrColorControlMalformed) {
		t.Fatalf("err = %v, want ErrColorControlMalformed", gotErr)
	}
}

// TestDecodeMoveHueRoundTrip encodes all four MoveHue fields and verifies
// each decoded field matches exactly.
func TestDecodeMoveHueRoundTrip(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), uint64(wire.ColorMoveModeUp))
		e.PutUint(tlv.ContextTag(1), 30)
		e.PutUint(tlv.ContextTag(2), 1)
		e.PutUint(tlv.ContextTag(3), 2)
	})
	got, err := wire.DecodeMoveHue(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MoveMode != wire.ColorMoveModeUp {
		t.Errorf("MoveMode = %d, want %d (Up)", got.MoveMode, wire.ColorMoveModeUp)
	}
	if got.Rate != 30 {
		t.Errorf("Rate = %d, want 30", got.Rate)
	}
	if got.OptionsMask != 1 {
		t.Errorf("OptionsMask = %d, want 1", got.OptionsMask)
	}
	if got.OptionsOverride != 2 {
		t.Errorf("OptionsOverride = %d, want 2", got.OptionsOverride)
	}
}

// TestDecodeStepHueRoundTrip encodes all five StepHue fields and verifies
// each decoded field matches exactly.
func TestDecodeStepHueRoundTrip(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), uint64(wire.ColorStepModeDown))
		e.PutUint(tlv.ContextTag(1), 15)
		e.PutUint(tlv.ContextTag(2), 500)
		e.PutUint(tlv.ContextTag(3), 3)
		e.PutUint(tlv.ContextTag(4), 3)
	})
	got, err := wire.DecodeStepHue(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.StepMode != wire.ColorStepModeDown {
		t.Errorf("StepMode = %d, want %d (Down)", got.StepMode, wire.ColorStepModeDown)
	}
	if got.StepSize != 15 {
		t.Errorf("StepSize = %d, want 15", got.StepSize)
	}
	if got.TransitionTime != 500 {
		t.Errorf("TransitionTime = %d, want 500", got.TransitionTime)
	}
	if got.OptionsMask != 3 {
		t.Errorf("OptionsMask = %d, want 3", got.OptionsMask)
	}
	if got.OptionsOverride != 3 {
		t.Errorf("OptionsOverride = %d, want 3", got.OptionsOverride)
	}
}

// TestDecodeMoveSaturationRoundTrip encodes all four MoveSaturation fields
// and verifies each decoded field matches exactly.
func TestDecodeMoveSaturationRoundTrip(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), uint64(wire.ColorMoveModeDown))
		e.PutUint(tlv.ContextTag(1), 20)
		e.PutUint(tlv.ContextTag(2), 1)
		e.PutUint(tlv.ContextTag(3), 1)
	})
	got, err := wire.DecodeMoveSaturation(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MoveMode != wire.ColorMoveModeDown {
		t.Errorf("MoveMode = %d, want %d (Down)", got.MoveMode, wire.ColorMoveModeDown)
	}
	if got.Rate != 20 {
		t.Errorf("Rate = %d, want 20", got.Rate)
	}
	if got.OptionsMask != 1 {
		t.Errorf("OptionsMask = %d, want 1", got.OptionsMask)
	}
	if got.OptionsOverride != 1 {
		t.Errorf("OptionsOverride = %d, want 1", got.OptionsOverride)
	}
}

// TestDecodeStepSaturationRoundTrip encodes all five StepSaturation fields
// and verifies each decoded field matches exactly.
func TestDecodeStepSaturationRoundTrip(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), uint64(wire.ColorStepModeUp))
		e.PutUint(tlv.ContextTag(1), 10)
		e.PutUint(tlv.ContextTag(2), 250)
		e.PutUint(tlv.ContextTag(3), 2)
		e.PutUint(tlv.ContextTag(4), 2)
	})
	got, err := wire.DecodeStepSaturation(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.StepMode != wire.ColorStepModeUp {
		t.Errorf("StepMode = %d, want %d (Up)", got.StepMode, wire.ColorStepModeUp)
	}
	if got.StepSize != 10 {
		t.Errorf("StepSize = %d, want 10", got.StepSize)
	}
	if got.TransitionTime != 250 {
		t.Errorf("TransitionTime = %d, want 250", got.TransitionTime)
	}
	if got.OptionsMask != 2 {
		t.Errorf("OptionsMask = %d, want 2", got.OptionsMask)
	}
	if got.OptionsOverride != 2 {
		t.Errorf("OptionsOverride = %d, want 2", got.OptionsOverride)
	}
}
