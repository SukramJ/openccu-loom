// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

import "testing"

// allSecurityClasses is the independently-enumerated set of every defined
// SecurityClass constant. Tests derive their expectations from this list
// rather than from SecurityClasses() so that a new class added to the
// const block but forgotten in SecurityClasses() is caught.
var allSecurityClasses = []SecurityClass{
	SecurityClassSmoke,
	SecurityClassWater,
	SecurityClassGas,
	SecurityClassCO,
	SecurityClassTamper,
	SecurityClassBattery,
	SecurityClassTechnical,
	SecurityClassIntrusion,
	SecurityClassPanic,
}

// TestSecurityClassValid verifies every defined class is Valid and an
// invented one is not.
func TestSecurityClassValid(t *testing.T) {
	t.Parallel()

	for _, c := range allSecurityClasses {
		if !c.Valid() {
			t.Errorf("SecurityClass(%q).Valid() = false, want true", c)
		}
	}

	if SecurityClass("bogus").Valid() {
		t.Errorf("SecurityClass(%q).Valid() = true, want false", "bogus")
	}
	if SecurityClass("").Valid() {
		t.Errorf("SecurityClass(%q).Valid() = true, want false", "")
	}
}

// TestSecurityClassHazardDiagnosticPartition verifies that Hazard and
// Diagnostic partition the valid class set exactly: every valid class is
// exactly one of the two, and an invalid class is neither.
func TestSecurityClassHazardDiagnosticPartition(t *testing.T) {
	t.Parallel()

	for _, c := range allSecurityClasses {
		hazard, diagnostic := c.Hazard(), c.Diagnostic()
		if hazard == diagnostic {
			t.Errorf("SecurityClass(%q): Hazard()=%v Diagnostic()=%v, want exactly one true", c, hazard, diagnostic)
		}
	}

	bogus := SecurityClass("bogus")
	if bogus.Hazard() {
		t.Errorf("SecurityClass(%q).Hazard() = true, want false for an invalid class", bogus)
	}
	if bogus.Diagnostic() {
		t.Errorf("SecurityClass(%q).Diagnostic() = true, want false for an invalid class", bogus)
	}
}

// TestSecurityClasses verifies SecurityClasses returns every defined class
// exactly once. The expected set is derived from allSecurityClasses (built
// independently of the production slice), so a class added to the const
// block without updating SecurityClasses fails this test.
func TestSecurityClasses(t *testing.T) {
	t.Parallel()

	got := SecurityClasses()

	if len(got) != len(allSecurityClasses) {
		t.Fatalf("SecurityClasses() has %d entries, want %d", len(got), len(allSecurityClasses))
	}

	seen := make(map[SecurityClass]struct{}, len(got))
	for _, c := range got {
		if _, dup := seen[c]; dup {
			t.Errorf("SecurityClasses() contains duplicate %q", c)
		}
		seen[c] = struct{}{}
	}

	for _, c := range allSecurityClasses {
		if _, ok := seen[c]; !ok {
			t.Errorf("SecurityClasses() is missing %q", c)
		}
	}
}

// TestSecuritySeverityRank verifies Rank is strictly ascending across the
// defined severities and that an undefined severity ranks -1 and is
// therefore not Valid.
func TestSecuritySeverityRank(t *testing.T) {
	t.Parallel()

	ascending := []SecuritySeverity{
		SecuritySeverityOK,
		SecuritySeverityInfo,
		SecuritySeverityWarning,
		SecuritySeverityAlarm,
		SecuritySeverityCritical,
	}

	for i := 1; i < len(ascending); i++ {
		prev, cur := ascending[i-1], ascending[i]
		if prev.Rank() >= cur.Rank() {
			t.Errorf("Rank(%q)=%d must be < Rank(%q)=%d", prev, prev.Rank(), cur, cur.Rank())
		}
	}

	undefined := SecuritySeverity("bogus")
	if got := undefined.Rank(); got != -1 {
		t.Errorf("SecuritySeverity(%q).Rank() = %d, want -1", undefined, got)
	}
	if undefined.Valid() {
		t.Errorf("SecuritySeverity(%q).Valid() = true, want false", undefined)
	}
}

// TestSeverityForClass verifies every class in SecurityClasses() maps to a
// Valid severity, and pins the documented precedence: smoke/gas/co are
// critical, intrusion/panic/water are alarm, tamper is warning, and
// technical/battery are info.
func TestSeverityForClass(t *testing.T) {
	t.Parallel()

	for _, c := range SecurityClasses() {
		if s := SeverityForClass(c); !s.Valid() {
			t.Errorf("SeverityForClass(%q) = %q, want a Valid severity", c, s)
		}
	}

	cases := []struct {
		class SecurityClass
		want  SecuritySeverity
	}{
		{SecurityClassSmoke, SecuritySeverityCritical},
		{SecurityClassGas, SecuritySeverityCritical},
		{SecurityClassCO, SecuritySeverityCritical},
		{SecurityClassIntrusion, SecuritySeverityAlarm},
		{SecurityClassPanic, SecuritySeverityAlarm},
		{SecurityClassWater, SecuritySeverityAlarm},
		{SecurityClassTamper, SecuritySeverityWarning},
		{SecurityClassTechnical, SecuritySeverityInfo},
		{SecurityClassBattery, SecuritySeverityInfo},
	}

	for _, tc := range cases {
		if got := SeverityForClass(tc.class); got != tc.want {
			t.Errorf("SeverityForClass(%q) = %q, want %q", tc.class, got, tc.want)
		}
	}
}

// TestSecurityFaultReasonValid verifies every defined fault reason is
// Valid and an invented one is not.
func TestSecurityFaultReasonValid(t *testing.T) {
	t.Parallel()

	reasons := []SecurityFaultReason{
		SecurityFaultReasonUnreachable,
		SecurityFaultReasonBlocked,
		SecurityFaultReasonDeviceError,
		SecurityFaultReasonCentralLost,
		SecurityFaultReasonDutyCycle,
		SecurityFaultReasonLowBattery,
		SecurityFaultReasonTamper,
	}

	for _, r := range reasons {
		if !r.Valid() {
			t.Errorf("SecurityFaultReason(%q).Valid() = false, want true", r)
		}
	}

	if SecurityFaultReason("bogus").Valid() {
		t.Errorf("SecurityFaultReason(%q).Valid() = true, want false", "bogus")
	}
	if SecurityFaultReason("").Valid() {
		t.Errorf("SecurityFaultReason(%q).Valid() = true, want false", "")
	}
}

// TestSecurityEnumStringRoundTrip verifies String returns the raw wire
// value for every SecurityClass, SecuritySeverity and SecurityFaultReason
// constant.
func TestSecurityEnumStringRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("SecurityClass", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			class SecurityClass
			want  string
		}{
			{SecurityClassSmoke, "smoke"},
			{SecurityClassWater, "water"},
			{SecurityClassGas, "gas"},
			{SecurityClassCO, "co"},
			{SecurityClassTamper, "tamper"},
			{SecurityClassBattery, "battery"},
			{SecurityClassTechnical, "technical"},
			{SecurityClassIntrusion, "intrusion"},
			{SecurityClassPanic, "panic"},
		}
		for _, tc := range cases {
			if got := tc.class.String(); got != tc.want {
				t.Errorf("SecurityClass.String() = %q, want %q", got, tc.want)
			}
		}
	})

	t.Run("SecuritySeverity", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			severity SecuritySeverity
			want     string
		}{
			{SecuritySeverityOK, "ok"},
			{SecuritySeverityInfo, "info"},
			{SecuritySeverityWarning, "warning"},
			{SecuritySeverityAlarm, "alarm"},
			{SecuritySeverityCritical, "critical"},
		}
		for _, tc := range cases {
			if got := tc.severity.String(); got != tc.want {
				t.Errorf("SecuritySeverity.String() = %q, want %q", got, tc.want)
			}
		}
	})

	t.Run("SecurityFaultReason", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			reason SecurityFaultReason
			want   string
		}{
			{SecurityFaultReasonUnreachable, "unreachable"},
			{SecurityFaultReasonBlocked, "blocked"},
			{SecurityFaultReasonDeviceError, "device_error"},
			{SecurityFaultReasonCentralLost, "central_lost"},
			{SecurityFaultReasonDutyCycle, "duty_cycle"},
			{SecurityFaultReasonLowBattery, "low_battery"},
			{SecurityFaultReasonTamper, "tamper"},
		}
		for _, tc := range cases {
			if got := tc.reason.String(); got != tc.want {
				t.Errorf("SecurityFaultReason.String() = %q, want %q", got, tc.want)
			}
		}
	})
}
