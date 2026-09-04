// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
)

// hmSchEntryWithDuration is a [schedule.SimpleEntry] that is valid apart from
// the two duration fields, so a validation failure can only come from them.
func hmSchEntryWithDuration(duration, rampTime string) schedule.SimpleEntry {
	return schedule.SimpleEntry{
		Weekdays: []schedule.Weekday{schedule.WeekdayMonday},
		Time:     "06:30",
		Duration: duration,
		RampTime: rampTime,
	}
}

// TestHmSchDomainValidatorAcceptsEveryDurationTheWireDecoderEmits crosses the
// two grammars that must be one.
//
// [FormatTimeBaseFactor] renders what the CCU holds — the digits are the
// factor multiplied out in the base's own unit, so a coarse base emits
// "50min" or "500ms" — while [schedule.SimpleEntry.Validate] used to read the
// same digits as the wire factor and reject anything above 30. Both reserved
// words fared worse: "permanent" did not match the numeric pattern at all,
// and "0ms" failed the lower bound, although the daemon's own lock encoder
// mints both.
//
// The strings are generated from the encoder rather than typed out, so this
// cannot pass by agreeing with a fixture that shares the mistake.
func TestHmSchDomainValidatorAcceptsEveryDurationTheWireDecoderEmits(t *testing.T) {
	t.Parallel()

	emitted := []string{PermanentDuration, ZeroDuration}
	for _, row := range timeBaseTable {
		for factor := 1; factor <= MaxTimeBaseFactor; factor++ {
			d := FormatTimeBaseFactor(row.id, factor)
			if d == "" {
				t.Fatalf("FormatTimeBaseFactor(%d, %d) rendered nothing", row.id, factor)
			}
			emitted = append(emitted, d)
		}
	}
	emitted = append(emitted, decodeWireDuration(permanentBase, permanentFactor))

	for _, d := range emitted {
		entry := hmSchEntryWithDuration(d, d)
		if err := (&entry).Validate(); err != nil {
			t.Errorf("the read path emits duration %q, and the domain validator rejects it: %v", d, err)
		}
	}
}

// TestHmSchDomainValidatorStillRejectsWhatIsNotADuration is the negative
// control for the test above: widening the grammar to the encoder's output
// must not widen it to everything.
func TestHmSchDomainValidatorStillRejectsWhatIsNotADuration(t *testing.T) {
	t.Parallel()

	for _, d := range []string{"forever", "10", "10 s", "-5s", "1h30min", "dauerhaft", "permanently"} {
		entry := hmSchEntryWithDuration(d, "")
		if err := (&entry).Validate(); err == nil {
			t.Errorf("duration %q was accepted; it is not a duration the CCU pair can carry", d)
		}
	}
}

// TestHmSchPermanentPairIsOneFactAcrossPackages pins the CCU's "Dauerhaft"
// encoding to a single spelling.
//
// The pair (7, 31) is one firmware fact — the weekly-program editor's first
// duration option writes exactly it — and it was spelled independently in the
// wire converter and in the lock domain's action table. A device declaring a
// wider DURATION_FACTOR would move it, and a fix applied to one spelling only
// would leave the other writing a timed duration where the operator asked for
// a standing one.
func TestHmSchPermanentPairIsOneFactAcrossPackages(t *testing.T) {
	t.Parallel()

	if permanentBase != schedule.PermanentDurationBase || permanentFactor != schedule.PermanentDurationFactor {
		t.Fatalf("weekprofile spells the permanent pair (%d, %d), the schedule domain (%d, %d)",
			permanentBase, permanentFactor,
			schedule.PermanentDurationBase, schedule.PermanentDurationFactor)
	}
	if got := decodeWireDuration(schedule.PermanentDurationBase, schedule.PermanentDurationFactor); got != PermanentDuration {
		t.Errorf("the domain's permanent pair decodes to %q, want %q", got, PermanentDuration)
	}
	// Every lock action that means "until further notice" carries the pair;
	// the auto-relock start, which is a zero duration, must not.
	for _, action := range []schedule.LockAction{
		schedule.LockActionAutoRelockEnd,
		schedule.LockActionUnlock,
		schedule.LockActionOpen,
	} {
		raw, ok := schedule.LockActionTable[action]
		if !ok {
			t.Fatalf("LockActionTable no longer carries %q", action)
		}
		if raw.DurBase() != schedule.PermanentDurationBase || raw.DurFactor() != schedule.PermanentDurationFactor {
			t.Errorf("lock action %q encodes duration (%d, %d), want the permanent pair (%d, %d)",
				action, raw.DurBase(), raw.DurFactor(),
				schedule.PermanentDurationBase, schedule.PermanentDurationFactor)
		}
	}
}

// TestHmSchAstroOffsetFallbackIsOneBound pins the domain validator's astro
// sanity bound to the bound the encoder falls back on when the channel
// declares no ASTRO_OFFSET range. Two literals stating one rule drift apart
// silently, and the encoder's copy is the only one on the write path.
func TestHmSchAstroOffsetFallbackIsOneBound(t *testing.T) {
	t.Parallel()

	var undeclared AstroOffsetLimits
	lo, hi := undeclared.bounds()
	if lo != -schedule.AstroOffsetFallbackLimit || hi != schedule.AstroOffsetFallbackLimit {
		t.Fatalf("the encoder's fallback range is %d..%d, the domain's bound is ±%d",
			lo, hi, schedule.AstroOffsetFallbackLimit)
	}
	for _, tc := range []struct {
		offset int
		valid  bool
	}{
		{lo, true},
		{hi, true},
		{lo - 1, false},
		{hi + 1, false},
	} {
		entry := schedule.SimpleEntry{
			Weekdays:           []schedule.Weekday{schedule.WeekdayMonday},
			Time:               "06:30",
			AstroOffsetMinutes: tc.offset,
		}
		err := (&entry).Validate()
		if tc.valid && err != nil {
			t.Errorf("astro offset %d is inside the encoder's fallback range but the "+
				"validator rejects it: %v", tc.offset, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("astro offset %d is outside the encoder's fallback range and the "+
				"validator accepts it", tc.offset)
		}
	}
}
