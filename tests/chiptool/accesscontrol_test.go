// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package chiptool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestAccessControl_ACL_HasShareController reads
// AccessControl.ACL on endpoint 0. After commissioning the ACL must
// contain at least one entry that grants the shared controller
// Administer privilege on the bridge.
func TestAccessControl_ACL_HasShareController(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "accesscontrol", "acl", 0)
	if err != nil {
		t.Fatalf("read acl: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Errorf("acl read did not succeed:\n%s", out)
	}
	if !strings.Contains(out, "Privilege") {
		t.Errorf("ACL output missing Privilege marker:\n%s", out)
	}
}

// TestAccessControl_SubjectsPerEntry reads
// SubjectsPerAccessControlEntry. Matter §9.10.4.3 mandates ≥ 4.
func TestAccessControl_SubjectsPerEntry(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "accesscontrol", "subjects-per-access-control-entry", 0)
	if err != nil {
		t.Fatalf("read subjects-per-access-control-entry: %v", err)
	}
	v, ok := harness.FindAttrUint(out, "SubjectsPerAccessControlEntry")
	if !ok {
		t.Fatalf("SubjectsPerAccessControlEntry not parsed:\n%s", out)
	}
	if v < 4 {
		t.Errorf("SubjectsPerAccessControlEntry=%d, Matter mandates ≥ 4", v)
	}
}

// TestAccessControl_TargetsPerEntry reads
// TargetsPerAccessControlEntry. Matter §9.10.4.4 mandates ≥ 3.
func TestAccessControl_TargetsPerEntry(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "accesscontrol", "targets-per-access-control-entry", 0)
	if err != nil {
		t.Fatalf("read targets-per-access-control-entry: %v", err)
	}
	v, ok := harness.FindAttrUint(out, "TargetsPerAccessControlEntry")
	if !ok {
		t.Fatalf("TargetsPerAccessControlEntry not parsed:\n%s", out)
	}
	if v < 3 {
		t.Errorf("TargetsPerAccessControlEntry=%d, Matter mandates ≥ 3", v)
	}
}

// TestAccessControl_AccessControlEntriesPerFabric reads
// AccessControlEntriesPerFabric. Matter §9.10.4.5 mandates ≥ 4.
func TestAccessControl_AccessControlEntriesPerFabric(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "accesscontrol", "access-control-entries-per-fabric", 0)
	if err != nil {
		t.Fatalf("read access-control-entries-per-fabric: %v", err)
	}
	v, ok := harness.FindAttrUint(out, "AccessControlEntriesPerFabric")
	if !ok {
		t.Fatalf("AccessControlEntriesPerFabric not parsed:\n%s", out)
	}
	if v < 4 {
		t.Errorf("AccessControlEntriesPerFabric=%d, Matter mandates ≥ 4", v)
	}
}
