// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

import "testing"

func TestAllRegaScriptsUnique(t *testing.T) {
	seen := make(map[RegaScript]struct{}, len(AllRegaScripts))
	for _, s := range AllRegaScripts {
		if _, dup := seen[s]; dup {
			t.Errorf("duplicate in AllRegaScripts: %s", s)
		}
		seen[s] = struct{}{}
	}
	// 32 = MVP subset + fetch_all_device_data + lifecycle scripts +
	// room/function entity CRUD (create/rename/delete × 2) + get_user_level
	// + reboot_ccu + the two bulk message-acknowledge scripts (service +
	// alarm) + execute_program_conditional + delete_program.
	// delete_system_variable was dropped in favor of the JSON-RPC
	// SysVar.deleteSysVarByName call.
	if len(AllRegaScripts) != 37 {
		t.Errorf("AllRegaScripts has %d entries, want 37", len(AllRegaScripts))
	}
}

func TestRegaScriptStringIsStable(t *testing.T) {
	// Wire-level identifier; changing it breaks recorded sessions.
	cases := map[RegaScript]string{
		RegaScriptGetSerial:                 "get_serial",
		RegaScriptSetSystemVariable:         "set_system_variable",
		RegaScriptAcknowledgeMessage:        "acknowledge_message",
		RegaScriptExecuteProgramConditional: "execute_program_conditional",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("%v.String() = %q, want %q", s, got, want)
		}
	}
}
