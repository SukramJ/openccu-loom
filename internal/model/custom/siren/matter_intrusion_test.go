// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestIntrusionIsNotReportedAsSmoke pins that an intrusion alarm driving the
// detector as a siren is not published to a Matter controller as a fire.
//
// SMOKE_DETECTOR_ALARM_STATUS carries INTRUSION_ALARM when the installation
// drove this smoke detector as a *siren* for a burglar alarm. It is a command
// the domain sent, not a detection the device made — which is why
// hmenum.SmokeDetectorAlarmStatusSmokeLabels deliberately excludes it, and why
// the safety classifier and the derived SMOKE_ALARM sensor both exclude it.
//
// Matter's SmokeState is "whether the device's smoke sensor is currently
// triggering a smoke alarm" (smoke-co-alarm.d.ts:150), and ExpressedState
// SmokeAlarm means the device is expressing visual and audible indication of
// a *smoke* alarm (:566-574). Reporting an intrusion there tells a controller
// there is a fire, and it is the one plane that reverses the rule the rest of
// the domain follows.
func TestIntrusionIsNotReportedAsSmoke(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		status        SmokeAlarmStatus
		wantAlarm     uint8
		wantExpressed uint8
		why           string
	}{
		{SmokeStatusIdleOff, matterSmokeAlarmNormal, matterExpressedStateNormal, "idle"},
		{SmokeStatusPrimaryAlarm, matterSmokeAlarmCritical, matterExpressedStateSmokeAlarm, "this detector sensed smoke"},
		{SmokeStatusSecondaryAlarm, matterSmokeAlarmWarning, matterExpressedStateSmokeAlarm, "a peer detector sensed smoke"},
		{SmokeStatusIntrusion, matterSmokeAlarmNormal, matterExpressedStateNormal, "the installation is using the sounder; no smoke was sensed"},
	} {
		if got := smokeStatusToAlarmState(tc.status); got != tc.wantAlarm {
			t.Errorf("SmokeState(%s) = %d, want %d — %s", tc.status, got, tc.wantAlarm, tc.why)
		}
		if got := smokeStatusToExpressedState(tc.status); got != tc.wantExpressed {
			t.Errorf("ExpressedState(%s) = %d, want %d — %s", tc.status, got, tc.wantExpressed, tc.why)
		}
	}
}

// TestSmokeMatterPlaneAgreesWithTheDomainsSmokeLabels pins the two planes
// against each other: whatever the domain counts as smoke is exactly what the
// Matter projection reports as a smoke alarm. The audit found them reversed on
// one label, with each side internally consistent, which is why neither test
// suite caught it.
func TestSmokeMatterPlaneAgreesWithTheDomainsSmokeLabels(t *testing.T) {
	t.Parallel()

	for _, st := range []SmokeAlarmStatus{
		SmokeStatusIdleOff, SmokeStatusPrimaryAlarm, SmokeStatusSecondaryAlarm, SmokeStatusIntrusion,
	} {
		domainSaysSmoke := slices.Contains(hmenum.SmokeDetectorAlarmStatusSmokeLabels(), string(st))
		matterSaysSmoke := smokeStatusToAlarmState(st) != matterSmokeAlarmNormal
		if domainSaysSmoke != matterSaysSmoke {
			t.Errorf("%s: the domain says smoke=%v, the Matter projection says smoke=%v",
				st, domainSaysSmoke, matterSaysSmoke)
		}
	}
}
