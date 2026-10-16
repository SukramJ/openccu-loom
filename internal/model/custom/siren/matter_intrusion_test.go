// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package siren

import (
	"slices"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/alarm"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// alarmServerFor builds a SmokeSiren whose status reads st and returns the
// SmokeCOAlarm server the siren assembles, so a test asserts what a
// controller reads rather than an internal mapping.
func alarmServerFor(t *testing.T, st SmokeAlarmStatus) interface {
	MatterRead(attrID uint32) (any, bool)
} {
	t.Helper()
	s, _, statusDP := newSmokeSirenRig(t)
	srv := s.MatterClusterServers()[0]
	fireSmokeStatus(t, statusDP, string(st))
	return srv
}

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
		wantAlarm     alarm.AlarmState
		wantExpressed alarm.ExpressedState
		why           string
	}{
		{SmokeStatusIdleOff, alarm.AlarmNormal, alarm.ExpressedNormal, "idle"},
		{SmokeStatusPrimaryAlarm, alarm.AlarmCritical, alarm.ExpressedSmokeAlarm, "this detector sensed smoke"},
		{SmokeStatusSecondaryAlarm, alarm.AlarmWarning, alarm.ExpressedSmokeAlarm, "a peer detector sensed smoke"},
		{SmokeStatusIntrusion, alarm.AlarmNormal, alarm.ExpressedNormal, "the installation is using the sounder; no smoke was sensed"},
	} {
		srv := alarmServerFor(t, tc.status)
		if got, ok := srv.MatterRead(alarm.AttrSmokeState); !ok || got != uint8(tc.wantAlarm) {
			t.Errorf("SmokeState(%s) = (%v, %v), want (%d, true) — %s", tc.status, got, ok, tc.wantAlarm, tc.why)
		}
		if got, ok := srv.MatterRead(alarm.AttrExpressedState); !ok || got != uint8(tc.wantExpressed) {
			t.Errorf("ExpressedState(%s) = (%v, %v), want (%d, true) — %s", tc.status, got, ok, tc.wantExpressed, tc.why)
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
		matterSaysSmoke := smokeStatusToAlarmState(st) != alarm.AlarmNormal
		if domainSaysSmoke != matterSaysSmoke {
			t.Errorf("%s: the domain says smoke=%v, the Matter projection says smoke=%v",
				st, domainSaysSmoke, matterSaysSmoke)
		}
	}
}
