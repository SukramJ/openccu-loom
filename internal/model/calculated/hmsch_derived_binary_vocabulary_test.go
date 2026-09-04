// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package calculated

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// hmSchSmokeAlarmVocabulary is SMOKE_DETECTOR_ALARM_STATUS's VALUE_LIST,
// spelled through the constants of [hmenum.SmokeDetectorAlarmStatus] rather
// than as literals, so this test cannot confirm a label the enum does not
// carry. The enum's own doc names its authority: the four constants are the
// VALUE_LIST the HmIP-SWSD paramset description declares, in its order.
func hmSchSmokeAlarmVocabulary() map[string]struct{} {
	return map[string]struct{}{
		string(hmenum.SmokeDetectorAlarmStatusIdleOff):        {},
		string(hmenum.SmokeDetectorAlarmStatusPrimaryAlarm):   {},
		string(hmenum.SmokeDetectorAlarmStatusIntrusionAlarm): {},
		string(hmenum.SmokeDetectorAlarmStatusSecondaryAlarm): {},
	}
}

// TestHmSchDerivedBinaryLabelsComeFromTheParameterVocabulary pins every
// SMOKE_DETECTOR_ALARM_STATUS row of the derived-binary registry against the
// enum that carries the parameter's vocabulary.
//
// Both halves of such a row state the same vocabulary: OnValues is sourced
// from [hmenum.SmokeDetectorAlarmStatusSmokeLabels], OffValues is written out
// in the registry. A registry row is free to split the labels between on and
// off as the domain requires, but it is not free to invent one — a label
// outside the VALUE_LIST is dead data that hides the fact that the two halves
// disagree about what the device can send.
//
// The complementary property is what makes the split safe: classify() returns
// ok=false for a label in neither set and the sensor then holds its previous
// value, so a label the device really sends and neither set names would stick
// the derived sensor at a stale reading.
func TestHmSchDerivedBinaryLabelsComeFromTheParameterVocabulary(t *testing.T) {
	t.Parallel()

	vocabulary := hmSchSmokeAlarmVocabulary()
	rows := 0
	for _, m := range derivedBinaryRegistry {
		if m.SourceParameter != hmenum.ParameterSmokeDetectorAlarmStatus {
			continue
		}
		rows++
		covered := make(map[string]struct{}, len(vocabulary))
		for _, group := range [][]string{m.OnValues, m.OffValues} {
			for _, label := range group {
				if _, declared := vocabulary[label]; !declared {
					t.Errorf("%s row for %v carries label %q, which SMOKE_DETECTOR_ALARM_STATUS "+
						"does not declare (VALUE_LIST: IDLE_OFF, PRIMARY_ALARM, INTRUSION_ALARM, "+
						"SECONDARY_ALARM)", m.SourceParameter, m.CalculatedParameter, label)
					continue
				}
				covered[label] = struct{}{}
			}
		}
		for label := range vocabulary {
			if _, ok := covered[label]; !ok {
				t.Errorf("%v row classifies neither on nor off for %q; a label in neither set "+
					"holds the derived sensor at its previous value", m.CalculatedParameter, label)
			}
		}
	}
	if rows == 0 {
		t.Fatal("no SMOKE_DETECTOR_ALARM_STATUS row left in derivedBinaryRegistry — the guard lost its subject")
	}
}
