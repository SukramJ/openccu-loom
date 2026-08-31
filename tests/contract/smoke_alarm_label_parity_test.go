// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/calculated"
	"github.com/SukramJ/openccu-loom/internal/model/safety"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// smokeAlarmLabelValueList is the declared ENUM vocabulary of
// SMOKE_DETECTOR_ALARM_STATUS on the HmIP-SWSD SMOKE_DETECTOR channel,
// in the order the CCU paramset description declares it.
//
// It is pinned here rather than read from an artefact because no
// artefact committed to this repository carries a VALUES-paramset value
// list for this parameter: the model snapshot that would is generated on
// demand and gitignored, so a contract test cannot depend on its
// presence. The list is the input domain of the comparison below, never
// the asserted value — both verdicts are computed by production code.
var smokeAlarmLabelValueList = []string{"IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM", "SECONDARY_ALARM"}

// smokeAlarmLabelCalculatedVerdict feeds one label through the derived
// binary sensor the device pipeline builds for CALCULATED SMOKE_ALARM
// and returns its verdict. decided is false when the sensor recognises
// neither an on- nor an off-label and therefore holds its previous
// value.
func smokeAlarmLabelCalculatedVerdict(t *testing.T, label string) (active, decided bool) {
	t.Helper()

	m, ok := calculated.LookupDerivedBinaryMappingByParam(hmenum.CalculatedParameterSmokeAlarm)
	if !ok {
		t.Fatal("no derived-binary mapping registered for CALCULATED SMOKE_ALARM")
	}
	s := calculated.MakeDerivedBinarySensor(m)
	s.OnLabel(label)
	return s.Value()
}

// smokeAlarmLabelSafetyVerdict runs one label through the safety
// classifier's activation rule, the way the security index and the alarm
// engine read an unenrolled SMOKE_DETECTOR_ALARM_STATUS source.
func smokeAlarmLabelSafetyVerdict(t *testing.T, label string) (active, known bool) {
	t.Helper()

	cls, ok := safety.Classify("HmIP-SWSD", "SMOKE_DETECTOR", hmenum.ParameterSmokeDetectorAlarmStatus)
	if !ok {
		t.Fatal("safety.Classify(HmIP-SWSD, SMOKE_DETECTOR, SMOKE_DETECTOR_ALARM_STATUS) ok=false, want true")
	}
	active, known, _ = safety.ActiveFromRaw(cls.ActiveValues, label, smokeAlarmLabelValueList)
	return active, known
}

// TestSmokeAlarmVerdictAgreesAcrossModelPackages pins the two
// independent "this label means smoke" definitions against each other.
//
// internal/model/calculated decides it by set membership in the derived
// SMOKE_ALARM mapping's OnValues/OffValues, and internal/model/safety by
// the classifier's ActiveValues. Neither package imports the other, so
// nothing but this guard keeps them from drifting apart — and drift
// means the north-bound SMOKE_ALARM binary sensor and the security
// index's active verdict disagree about the same wire label on the same
// device.
func TestSmokeAlarmVerdictAgreesAcrossModelPackages(t *testing.T) {
	t.Parallel()

	for _, label := range smokeAlarmLabelValueList {
		calcActive, calcDecided := smokeAlarmLabelCalculatedVerdict(t, label)
		safeActive, safeKnown := smokeAlarmLabelSafetyVerdict(t, label)

		if !calcDecided {
			t.Errorf("label %q: the derived SMOKE_ALARM sensor reaches no verdict (holds its previous value) "+
				"while safety reports active=%v — every label of the declared value list must be decided by both",
				label, safeActive)
			continue
		}
		if !safeKnown {
			t.Errorf("label %q: safety.ActiveFromRaw reports the value as having no activation semantics "+
				"while the derived SMOKE_ALARM sensor reports active=%v", label, calcActive)
			continue
		}
		if calcActive != safeActive {
			t.Errorf("label %q: calculated SMOKE_ALARM active=%v, safety active=%v — "+
				"the two definitions of the smoke label set have drifted apart",
				label, calcActive, safeActive)
		}
	}
}

// TestSmokeAlarmIntrusionLabelIsNotSmokeOnEitherSide pins the carve-out
// that makes the shared verdict correct rather than merely consistent.
//
// INTRUSION_ALARM on SMOKE_DETECTOR_ALARM_STATUS means the installation
// drove this smoke detector as a siren for an intrusion alarm. It is the
// domain reading back its own output, not a detection, so neither the
// derived binary sensor nor the safety classifier may treat it as smoke.
func TestSmokeAlarmIntrusionLabelIsNotSmokeOnEitherSide(t *testing.T) {
	t.Parallel()

	const label = "INTRUSION_ALARM"

	calcActive, calcDecided := smokeAlarmLabelCalculatedVerdict(t, label)
	if !calcDecided || calcActive {
		t.Errorf("calculated SMOKE_ALARM on %q: active=%v decided=%v, want active=false decided=true — "+
			"the siren command must clear the smoke sensor, not raise it", label, calcActive, calcDecided)
	}

	safeActive, safeKnown := smokeAlarmLabelSafetyVerdict(t, label)
	if !safeKnown || safeActive {
		t.Errorf("safety verdict on %q: active=%v known=%v, want active=false known=true — "+
			"the siren command must not count as a smoke activation", label, safeActive, safeKnown)
	}
}

// TestSmokeAlarmLabelSetIsPinnedForPersistedConfig pins the content and
// order of the shared smoke label set.
//
// This is a value pin, not a duplication guard: the set is not private
// to the domain. It is published over REST as an enrolment
// recommendation, pre-filled into the operator's sensor configuration
// and read back from persisted storage at runtime, so reordering or
// extending it silently changes what an already-stored enrolment means.
func TestSmokeAlarmLabelSetIsPinnedForPersistedConfig(t *testing.T) {
	t.Parallel()

	want := []string{"PRIMARY_ALARM", "SECONDARY_ALARM"}
	got := hmenum.SmokeDetectorAlarmStatusSmokeLabels()
	if !slices.Equal(got, want) {
		t.Errorf("SmokeDetectorAlarmStatusSmokeLabels() = %v, want %v — the set is persisted "+
			"in operator enrolments and published over REST, so its content and order are wire identity",
			got, want)
	}

	// The accessor must not hand out a shared backing array: consumers
	// store it by reference into long-lived index state.
	first := hmenum.SmokeDetectorAlarmStatusSmokeLabels()
	second := hmenum.SmokeDetectorAlarmStatusSmokeLabels()
	first[0] = "MUTATED"
	if second[0] == "MUTATED" {
		t.Error("SmokeDetectorAlarmStatusSmokeLabels() returns a shared backing array; " +
			"a consumer mutating its copy would rewrite the domain-wide set")
	}
}
