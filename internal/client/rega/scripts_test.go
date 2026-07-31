// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rega

import (
	"io/fs"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestEveryRegaScriptIsLoadable iterates over every constant in
// hmenum.AllRegaScripts and verifies that loadScript returns a
// non-empty, non-error body. This documents the 1:1 mapping between
// enum value and embedded .fn file.
func TestEveryRegaScriptIsLoadable(t *testing.T) {
	t.Parallel()
	for _, s := range hmenum.AllRegaScripts {
		t.Run(string(s), func(t *testing.T) {
			t.Parallel()
			body, err := loadScript(s)
			if err != nil {
				t.Fatalf("loadScript(%q) returned error: %v", s, err)
			}
			if strings.TrimSpace(body) == "" {
				t.Fatalf("loadScript(%q) returned empty body", s)
			}
		})
	}
}

// TestLoadScriptUnknownReturnsError verifies that requesting a script
// whose name has no matching embedded .fn file returns an error rather
// than silently succeeding with an empty body.
func TestLoadScriptUnknownReturnsError(t *testing.T) {
	t.Parallel()
	_, err := loadScript(hmenum.RegaScript("nonexistent_script"))
	if err == nil {
		t.Fatal("expected an error for unknown script name, got nil")
	}
}

// TestEveryScriptIsValidUTF8AndHasNoBOM pins the actual CCU behaviour
// observed against a live OpenCCU host: scripts MUST be sent WITHOUT
// a UTF-8 BOM. The correct behaviour is
// inverted from an early assumption — a BOM-prefixed body causes the
// CCU to return an empty `result` string. Sending the raw body works.
//
// The empirical test:
//
//	WriteLine("hello-bom-test"); → result "hello-bom-test\r\n"
//	"\xef\xbb\xbf" + same script → result ""
//
// Therefore Runner.Run is correct in passing the body through verbatim,
// and this tripwire ensures nobody accidentally adds a BOM (e.g. via an
// editor with auto-BOM, or a future generator script).
func TestEveryScriptIsValidUTF8AndHasNoBOM(t *testing.T) {
	t.Parallel()
	for _, s := range hmenum.AllRegaScripts {
		t.Run(string(s), func(t *testing.T) {
			t.Parallel()
			body, err := loadScript(s)
			if err != nil {
				t.Fatalf("loadScript(%q) error: %v", s, err)
			}
			if !utf8.ValidString(body) {
				t.Errorf("script %q body is not valid UTF-8", s)
			}
			if strings.HasPrefix(body, "\xef\xbb\xbf") {
				t.Errorf("script %q starts with UTF-8 BOM — CCU returns empty result for BOM-prefixed scripts (verified 2026-04-28)", s)
			}
		})
	}
}

// TestScriptFileCountMatchesEnumCount verifies that the number of .fn
// files embedded in scriptFS equals the number of constants in
// hmenum.AllRegaScripts. A mismatch means a file was added without a
// corresponding constant, or vice-versa.
func TestScriptFileCountMatchesEnumCount(t *testing.T) {
	t.Parallel()
	entries, err := fs.ReadDir(scriptFS, "scripts")
	if err != nil {
		t.Fatalf("ReadDir(scriptFS, \"scripts\"): %v", err)
	}
	var fnCount int
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".fn") {
			fnCount++
		}
	}
	enumCount := len(hmenum.AllRegaScripts)
	if fnCount != enumCount {
		t.Errorf("embedded .fn file count = %d, hmenum.AllRegaScripts count = %d; they must match",
			fnCount, enumCount)
	}
}

// TestScriptBodyContainsExpectedPlaceholders spot-checks three scripts
// that carry template parameters and asserts their body contains the
// expected ##NAME## tokens. This protects against accidental template
// erasure during edits.
func TestScriptBodyContainsExpectedPlaceholders(t *testing.T) {
	t.Parallel()

	cases := []struct {
		script      hmenum.RegaScript
		mustContain []string
	}{
		{
			script:      hmenum.RegaScriptSetSystemVariable,
			mustContain: []string{"##name##", "##value##"},
		},
		{
			script:      hmenum.RegaScriptAcceptDeviceInInbox,
			mustContain: []string{"##device_address##"},
		},
		{
			script:      hmenum.RegaScriptSetProgramState,
			mustContain: []string{"##id##", "##state##"},
		},
		{
			script:      hmenum.RegaScriptDeleteProgram,
			mustContain: []string{"##id##"},
		},
		{
			script:      hmenum.RegaScriptUsageBySysvar,
			mustContain: []string{"##name##", "DPEnumUsagePrograms"},
		},
		{
			script: hmenum.RegaScriptSetCCUPosition,
			// The read-back is what lets the caller confirm the CCU
			// accepted the write instead of trusting a bare ack.
			mustContain: []string{"##longitude##", "##latitude##", "system.Longitude(", "system.Latitude("},
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.script), func(t *testing.T) {
			t.Parallel()
			body, err := loadScript(tc.script)
			if err != nil {
				t.Fatalf("loadScript(%q): %v", tc.script, err)
			}
			for _, token := range tc.mustContain {
				if !strings.Contains(body, token) {
					t.Errorf("script %q does not contain expected placeholder %q", tc.script, token)
				}
			}
		})
	}
}

// TestRebootCCUScriptBody verifies the reboot script is registered and its
// body persists runtime state before triggering the reboot. A reboot without
// the preceding system.Save() would lose unsaved CCU state.
func TestRebootCCUScriptBody(t *testing.T) {
	t.Parallel()
	body, err := loadScript(hmenum.RegaScriptRebootCCU)
	if err != nil {
		t.Fatalf("loadScript(reboot_ccu): %v", err)
	}
	for _, token := range []string{"system.Save()", "/sbin/reboot"} {
		if !strings.Contains(body, token) {
			t.Errorf("reboot_ccu script does not contain %q", token)
		}
	}
	// Save must precede the reboot so state is persisted first.
	if strings.Index(body, "system.Save()") > strings.Index(body, "/sbin/reboot") {
		t.Error("reboot_ccu must call system.Save() before /sbin/reboot")
	}
}

// TestCreateSystemVariableScriptHasAlarmBranch pins the ALARM branch of
// the create_system_variable script. An alarm line must be backed by an
// OT_ALARMDP object (not the OT_VARDP every other type uses), wire up the
// binary alarm condition, and be marked a system alarm — otherwise the
// created variable is not a real, acknowledgeable alarm on the CCU.
func TestCreateSystemVariableScriptHasAlarmBranch(t *testing.T) {
	t.Parallel()
	body, err := loadScript(hmenum.RegaScriptCreateSystemVariable)
	if err != nil {
		t.Fatalf("loadScript(create_system_variable): %v", err)
	}
	for _, token := range []string{
		"##type##",
		"OT_ALARMDP",
		"AlSetBinaryCondition()",
		"ValueSubType(istAlarm)",
		"AlType(atSystem)",
		"AlArm(true)",
	} {
		if !strings.Contains(body, token) {
			t.Errorf("create_system_variable script missing ALARM-branch token %q", token)
		}
	}
}

// TestScriptsWithoutPlaceholdersAreParamFree verifies that scripts
// known to have no template parameters can be loaded and contain no
// ##NAME## tokens — protecting against accidental placeholder insertion
// that would cause Run to fail at substitute time.
func TestScriptsWithoutPlaceholdersAreParamFree(t *testing.T) {
	t.Parallel()

	noParamScripts := []hmenum.RegaScript{
		hmenum.RegaScriptGetSerial,
		hmenum.RegaScriptGetBackendInfo,
		hmenum.RegaScriptGetAlarmMessages,
		hmenum.RegaScriptGetServiceMessages,
		hmenum.RegaScriptGetSystemUpdateInfo,
		hmenum.RegaScriptGetSystemVariableDescriptions,
		hmenum.RegaScriptGetProgramDescriptions,
		hmenum.RegaScriptGetInboxDevices,
		hmenum.RegaScriptCreateBackupStart,
		hmenum.RegaScriptCreateBackupStatus,
	}

	for _, s := range noParamScripts {
		t.Run(string(s), func(t *testing.T) {
			t.Parallel()
			body, err := loadScript(s)
			if err != nil {
				t.Fatalf("loadScript(%q): %v", s, err)
			}
			if placeholderPattern.MatchString(body) {
				t.Errorf("script %q unexpectedly contains ##NAME## placeholders", s)
			}
		})
	}
}
