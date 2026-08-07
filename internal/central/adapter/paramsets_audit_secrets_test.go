// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import "testing"

// TestAuditChangesWithholdsCredentialValues pins that a paramset write
// records the name of a credential parameter but never its value.
//
// CODE_ID carries the access code of a keypad or lock channel, and a
// paramset write that sets it puts the code into the write payload. The
// audit log is append-only with a 90-day retention, so persisting that
// value handed the code to every operator dump — while the sibling
// data-point write path had recorded names only for exactly this reason
// since it was written.
func TestAuditChangesWithholdsCredentialValues(t *testing.T) {
	t.Parallel()

	before := map[string]any{"CODE_ID": 1234, "SET_POINT_TEMPERATURE": 21.0}
	after := map[string]any{"CODE_ID": 4711, "SET_POINT_TEMPERATURE": 24.0}

	byParam := map[string][2]any{}
	for _, c := range auditChanges(before, after) {
		byParam[c.Parameter] = [2]any{c.Before, c.After}
	}

	code, ok := byParam["CODE_ID"]
	if !ok {
		t.Fatal("CODE_ID missing from the audit rows; the name must still be recorded")
	}
	if code[1] != auditRedactedMask || code[0] != auditRedactedMask {
		t.Errorf("CODE_ID audit row = before %v, after %v; both must be withheld", code[0], code[1])
	}

	temp, ok := byParam["SET_POINT_TEMPERATURE"]
	if !ok {
		t.Fatal("SET_POINT_TEMPERATURE missing from the audit rows")
	}
	if temp[0] != 21.0 || temp[1] != 24.0 {
		t.Errorf("ordinary setting = before %v, after %v; its values carry the audit's whole point", temp[0], temp[1])
	}
}

// TestAuditChangesKeepsNilBeforeDistinguishable pins that a first-time
// write of a credential still reads as "had no value before" rather than
// as a withheld one.
func TestAuditChangesKeepsNilBeforeDistinguishable(t *testing.T) {
	t.Parallel()

	for _, c := range auditChanges(map[string]any{}, map[string]any{"CODE_ID": 4711}) {
		if c.Parameter != "CODE_ID" {
			continue
		}
		if c.Before != nil {
			t.Errorf("first write of CODE_ID reported before=%v, want nil", c.Before)
		}
		if c.After != auditRedactedMask {
			t.Errorf("first write of CODE_ID reported after=%v, want it withheld", c.After)
		}
	}
}
