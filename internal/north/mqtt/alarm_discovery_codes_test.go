// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestBuildAlarmPanelDiscovery_CodePolicyFlipsREMOTECODE covers the
// docs/alarm-concept.md §11 discovery flip: whenever an area's code
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
// it reads the same two booleans as any area panel.
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
