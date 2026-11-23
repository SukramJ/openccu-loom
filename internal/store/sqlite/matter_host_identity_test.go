// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
)

// TestMatterHostIdentityFirstSeedWins: the first load fixes serial and label;
// a later load with a different seed returns the stored values, so a changed
// node label never moves the pinned serial.
func TestMatterHostIdentityFirstSeedWins(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "matter-host-identity.db")
	ctx := context.Background()

	first, err := LoadMatterHostIdentity(ctx, db, MatterHostIdentity{RootSerial: "0123456789abcdef", AppliedNodeLabel: "openccu-loom"})
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if first.RootSerial != "0123456789abcdef" || first.AppliedNodeLabel != "openccu-loom" {
		t.Fatalf("first load = %+v, want the seed", first)
	}
	again, err := LoadMatterHostIdentity(ctx, db, MatterHostIdentity{RootSerial: "fedcba9876543210", AppliedNodeLabel: "Wohnzimmer"})
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if again != first {
		t.Fatalf("second load = %+v, want the stored %+v", again, first)
	}
}

// TestMatterHostIdentityAppliedLabelUpdates: recording an applied label
// changes only the label; the serial stays pinned.
func TestMatterHostIdentityAppliedLabelUpdates(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "matter-host-identity-label.db")
	ctx := context.Background()

	if _, err := LoadMatterHostIdentity(ctx, db, MatterHostIdentity{RootSerial: "0123456789abcdef", AppliedNodeLabel: "openccu-loom"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SetMatterAppliedNodeLabel(ctx, db, "Bridge Keller"); err != nil {
		t.Fatalf("SetMatterAppliedNodeLabel: %v", err)
	}
	got, err := LoadMatterHostIdentity(ctx, db, MatterHostIdentity{RootSerial: "x", AppliedNodeLabel: "y"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := MatterHostIdentity{RootSerial: "0123456789abcdef", AppliedNodeLabel: "Bridge Keller"}
	if got != want {
		t.Fatalf("load = %+v, want %+v", got, want)
	}
}

// TestMatterHostIdentitySetWithoutRowFails: recording a label before the row
// was seeded is an error, not a silent no-op.
func TestMatterHostIdentitySetWithoutRowFails(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "matter-host-identity-empty.db")
	if err := SetMatterAppliedNodeLabel(context.Background(), db, "x"); err == nil {
		t.Fatal("SetMatterAppliedNodeLabel on an empty table: nil error, want one")
	}
}
