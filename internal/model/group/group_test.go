// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package group

import (
	"strings"
	"testing"
)

// ─── sentinel / empty payloads ────────────────────────────────────────────

func TestParseGroupList_MissingFileSentinel(t *testing.T) {
	t.Parallel()
	got, err := ParseGroupList("-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("want non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("want 0 groups, got %d", len(got))
	}
}

func TestParseGroupList_EmptyString(t *testing.T) {
	t.Parallel()
	got, err := ParseGroupList("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("want non-nil empty slice, got %#v", got)
	}
}

func TestParseGroupList_Whitespace(t *testing.T) {
	t.Parallel()
	got, err := ParseGroupList("   \n\t  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("want non-nil empty slice, got %#v", got)
	}
}

// ─── realistic groups.gson payload ────────────────────────────────────────

const twoGroupsPayload = `{
  "groups": [
    {
      "id": 1,
      "groupType": {"id": "HEATING", "label": "group.type.heating"},
      "groupProperties": {
        "NAME": "Living Room Heating",
        "GROUP_DEVICE_NAME": "Living Room Heating Device",
        "FORBID_SINGLE_OPERATION": "true"
      },
      "groupMembers": [
        {"id": "000ABC0123456:1", "memberType": {"id": "THERMOSTAT"}}
      ]
    },
    {
      "id": 2,
      "groupType": {"id": "HEATING", "label": "group.type.heating"},
      "groupProperties": {
        "NAME": "Bedroom Heating",
        "GROUP_DEVICE_NAME": "Bedroom Heating Device",
        "FORBID_SINGLE_OPERATION": "FALSE"
      },
      "groupMembers": [
        {"id": "000DEF0987654:1", "memberType": {"id": "THERMOSTAT"}}
      ]
    }
  ]
}`

func TestParseGroupList_RealisticPayload(t *testing.T) {
	t.Parallel()
	got, err := ParseGroupList(twoGroupsPayload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 groups, got %d", len(got))
	}

	g1 := got[0]
	if g1.ID != 1 {
		t.Errorf("g1.ID = %d, want 1", g1.ID)
	}
	if g1.Name != "Living Room Heating" {
		t.Errorf("g1.Name = %q, want %q", g1.Name, "Living Room Heating")
	}
	if g1.GroupDeviceName != "Living Room Heating Device" {
		t.Errorf("g1.GroupDeviceName = %q, want %q", g1.GroupDeviceName, "Living Room Heating Device")
	}
	if !g1.ForbidSingleOperation {
		t.Error("g1.ForbidSingleOperation = false, want true (\"true\" case)")
	}
	if g1.TypeID != "HEATING" {
		t.Errorf("g1.TypeID = %q, want HEATING", g1.TypeID)
	}
	if g1.TypeLabel != "group.type.heating" {
		t.Errorf("g1.TypeLabel = %q, want group.type.heating", g1.TypeLabel)
	}
	if len(g1.Members) != 1 {
		t.Fatalf("g1.Members len = %d, want 1", len(g1.Members))
	}
	if g1.Members[0].Address != "000ABC0123456:1" {
		t.Errorf("g1.Members[0].Address = %q, want 000ABC0123456:1", g1.Members[0].Address)
	}
	if g1.Members[0].TypeID != "THERMOSTAT" {
		t.Errorf("g1.Members[0].TypeID = %q, want THERMOSTAT", g1.Members[0].TypeID)
	}

	g2 := got[1]
	if g2.ID != 2 {
		t.Errorf("g2.ID = %d, want 2", g2.ID)
	}
	if g2.Name != "Bedroom Heating" {
		t.Errorf("g2.Name = %q, want %q", g2.Name, "Bedroom Heating")
	}
	// FALSE (upper-case) must still parse case-insensitively to false.
	if g2.ForbidSingleOperation {
		t.Error("g2.ForbidSingleOperation = true, want false (\"FALSE\" case)")
	}
	if len(g2.Members) != 1 || g2.Members[0].Address != "000DEF0987654:1" {
		t.Errorf("g2.Members = %#v", g2.Members)
	}
}

// ─── malformed JSON ────────────────────────────────────────────────────────

func TestParseGroupList_MalformedJSON(t *testing.T) {
	t.Parallel()
	_, err := ParseGroupList("{bad")
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "parse groups.gson") {
		t.Errorf("error %q should mention the parse context", err.Error())
	}
}

// ─── missing / empty sub-fields ───────────────────────────────────────────

func TestParseGroupList_NoMembersNoProperties(t *testing.T) {
	t.Parallel()
	const payload = `{"groups":[{"id":7,"groupType":{"id":"HEATING","label":""},"groupProperties":{},"groupMembers":[]}]}`
	got, err := ParseGroupList(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 group, got %d", len(got))
	}
	g := got[0]
	if g.ID != 7 {
		t.Errorf("g.ID = %d, want 7", g.ID)
	}
	if g.Name != "" || g.GroupDeviceName != "" {
		t.Errorf("expected empty Name/GroupDeviceName, got Name=%q GroupDeviceName=%q", g.Name, g.GroupDeviceName)
	}
	if g.ForbidSingleOperation {
		t.Error("ForbidSingleOperation should default to false when the property is absent")
	}
	if g.Members == nil {
		t.Error("Members must be a non-nil empty slice, not nil")
	}
	if len(g.Members) != 0 {
		t.Errorf("Members len = %d, want 0", len(g.Members))
	}
}

// TestParseGroupList_MissingGroupPropertiesKey asserts the whole
// groupProperties object can be absent (nil map) without a panic.
func TestParseGroupList_MissingGroupPropertiesKey(t *testing.T) {
	t.Parallel()
	const payload = `{"groups":[{"id":9,"groupType":{"id":"HEATING","label":"x"}}]}`
	got, err := ParseGroupList(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 group, got %d", len(got))
	}
	if got[0].Name != "" {
		t.Errorf("Name = %q, want empty", got[0].Name)
	}
	if got[0].Members == nil || len(got[0].Members) != 0 {
		t.Errorf("Members = %#v, want non-nil empty", got[0].Members)
	}
}

func TestParseGroupList_EmptyGroupsArray(t *testing.T) {
	t.Parallel()
	got, err := ParseGroupList(`{"groups":[]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("want non-nil empty slice, got %#v", got)
	}
}
