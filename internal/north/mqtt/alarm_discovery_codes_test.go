// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestBuildAlarmPanelDiscovery_CodePolicyFlipsREMOTECODE covers the
// notes/concepts/alarm-concept.md §11 discovery flip: whenever an zone's code
// policy requires a code for arm and/or disarm, the panel must
// advertise code:"REMOTE_CODE" plus the command_template that folds
// the entered code into the raw command JSON — otherwise HA never
// prompts for a code and it never reaches loom's validator. The
// baseline (both flags false) case is covered by
// TestBuildAlarmPanelDiscovery_AreaPanelShape in alarm_discovery_test.go;
// this file exercises every other combination of the two booleans.
func TestBuildAlarmPanelDiscovery_CodePolicyFlipsREMOTECODE(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                string
		codeArmRequired     bool
		codeDisarmRequired  bool
		wantCodeFieldsSet   bool
		wantArmRequiredFlag bool
	}{
		{"neither_required", false, false, false, false},
		{"arm_required_only", true, false, true, true},
		{"disarm_required_only", false, true, true, false},
		{"both_required", true, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			item := BuildAlarmPanelDiscovery("gh", "eg", "Erdgeschoss",
				[]hmenum.AlarmMode{hmenum.AlarmModeFull}, false, tc.codeArmRequired, tc.codeDisarmRequired)
			body := alarmDiscoveryBody(t, item)

			if got := body["code_arm_required"]; got != tc.codeArmRequired {
				t.Errorf("code_arm_required = %v, want %v", got, tc.codeArmRequired)
			}
			if got := body["code_disarm_required"]; got != tc.codeDisarmRequired {
				t.Errorf("code_disarm_required = %v, want %v", got, tc.codeDisarmRequired)
			}

			code, hasCode := body["code"]
			template, hasTemplate := body["command_template"]
			if hasCode != tc.wantCodeFieldsSet || hasTemplate != tc.wantCodeFieldsSet {
				t.Fatalf("code=%v(present=%v) command_template=%v(present=%v), want present=%v",
					code, hasCode, template, hasTemplate, tc.wantCodeFieldsSet)
			}
			if !tc.wantCodeFieldsSet {
				return
			}
			if code != alarmRemoteCode {
				t.Errorf("code = %v, want %q", code, alarmRemoteCode)
			}
			if template != alarmCommandTemplate {
				t.Errorf("command_template = %v, want %q", template, alarmCommandTemplate)
			}
		})
	}
}

// TestBuildAlarmPanelDiscovery_MasterPanelHonorsCodePolicyToo asserts
// the flip is not special-cased away for the aggregate master panel —
// it reads the same two booleans as any zone panel.
func TestBuildAlarmPanelDiscovery_MasterPanelHonorsCodePolicyToo(t *testing.T) {
	t.Parallel()
	item := BuildAlarmPanelDiscovery("gh", "ignored", "Alarm system",
		[]hmenum.AlarmMode{hmenum.AlarmModeFull}, true, false, true)
	body := alarmDiscoveryBody(t, item)

	if got := body["code_disarm_required"]; got != true {
		t.Errorf("code_disarm_required = %v, want true", got)
	}
	if got := body["code"]; got != alarmRemoteCode {
		t.Errorf("code = %v, want %q", got, alarmRemoteCode)
	}
	if got := body["command_template"]; got != alarmCommandTemplate {
		t.Errorf("command_template = %v, want %q", got, alarmCommandTemplate)
	}
}

// boolPtr returns a pointer to b, for building an explicit
// engine.CodePolicy.RequireDisarm value (as opposed to the nil
// default).
func boolPtr(b bool) *bool { return &b }

// TestAlarmMQTTPublisher_AreaCodePolicyEffectiveRequirement covers the
// review-fix regression in [AlarmMQTTPublisher.zoneCodePolicy]: the
// discovery flags advertised for an zone must reflect BOTH halves of
// the effective requirement — the zone's engine.CodePolicy AND
// whether an applicable enabled pin code actually exists
// (internal/alarm/codes.Facade.HasPINCodes) — never the policy alone.
// Advertising the policy half without an existing code would leave HA
// prompting for a code the engine can never demand; the reverse
// leaves an existing code unadvertised and HA sends a bare,
// code-less command the engine refuses (notes/concepts/alarm-concept.md
// §11/§13.3).
func TestAlarmMQTTPublisher_AreaCodePolicyEffectiveRequirement(t *testing.T) {
	t.Parallel()

	type seedCode struct {
		id      string
		enabled bool
		zones   []string
	}
	cases := []struct {
		name          string
		requireArm    bool
		requireDisarm *bool
		codes         []seedCode
		wantArmReq    bool
		wantDisarmReq bool
	}{
		{
			name:          "nil_require_disarm_with_enabled_pin_requires_disarm_code",
			requireDisarm: nil,
			codes:         []seedCode{{id: "c1", enabled: true}},
			wantDisarmReq: true,
		},
		{
			name:          "nil_require_disarm_without_any_pin_requires_nothing",
			requireDisarm: nil,
			codes:         nil,
			wantDisarmReq: false,
		},
		{
			name:          "explicit_require_disarm_false_with_pin_stays_false",
			requireDisarm: boolPtr(false),
			codes:         []seedCode{{id: "c1", enabled: true}},
			wantDisarmReq: false,
		},
		{
			name:          "require_arm_with_pin_requires_arm_code",
			requireArm:    true,
			requireDisarm: boolPtr(false),
			codes:         []seedCode{{id: "c1", enabled: true}},
			wantArmReq:    true,
		},
		{
			name:          "require_arm_without_pin_stays_false",
			requireArm:    true,
			requireDisarm: boolPtr(false),
			codes:         nil,
			wantArmReq:    false,
		},
		{
			name:          "disabled_pin_does_not_count",
			requireDisarm: nil,
			codes:         []seedCode{{id: "c1", enabled: false}},
			wantDisarmReq: false,
		},
		{
			name:          "pin_scoped_to_a_different_area_does_not_count",
			requireDisarm: nil,
			codes:         []seedCode{{id: "c1", enabled: true, zones: []string{"og"}}},
			wantDisarmReq: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newAlarmPublisherFixture(t)
			cfg := zeroDelayFullMode()
			cfg.CodePolicy = engine.CodePolicy{RequireArm: tc.requireArm, RequireDisarm: tc.requireDisarm}
			f.seedZone("eg", "Erdgeschoss", cfg)
			for _, c := range tc.codes {
				f.seedPINCode(c.id, "PIN "+c.id, "1234", c.enabled, c.zones)
			}

			armReq, disarmReq := f.pub.zoneCodePolicy(context.Background(), "eg")
			if armReq != tc.wantArmReq {
				t.Errorf("armReq = %v, want %v", armReq, tc.wantArmReq)
			}
			if disarmReq != tc.wantDisarmReq {
				t.Errorf("disarmReq = %v, want %v", disarmReq, tc.wantDisarmReq)
			}

			item := BuildAlarmPanelDiscovery("gh", "eg", "Erdgeschoss",
				[]hmenum.AlarmMode{hmenum.AlarmModeFull}, false, armReq, disarmReq)
			body := alarmDiscoveryBody(t, item)
			wantCodeFields := armReq || disarmReq
			_, hasCode := body["code"]
			_, hasTemplate := body["command_template"]
			if hasCode != wantCodeFields || hasTemplate != wantCodeFields {
				t.Fatalf("code field present=%v command_template present=%v, want present=%v",
					hasCode, hasTemplate, wantCodeFields)
			}
			if wantCodeFields && body["code"] != alarmRemoteCode {
				t.Errorf("code = %v, want %q", body["code"], alarmRemoteCode)
			}
		})
	}
}
