// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
)

// TestBootRecordStopMarkerLivesOneBoot: a marker written at shutdown is what
// the next boot reads, and the boot after that reads none — whether or not
// anything read it in between.
func TestBootRecordStopMarkerLivesOneBoot(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "boot-record-stop.db")
	ctx := context.Background()

	if rec, err := LoadBootRecord(ctx, db); err != nil || rec != (BootRecord{}) {
		t.Fatalf("fresh record = %+v, %v; want the zero record", rec, err)
	}
	if err := RecordStopRequested(ctx, db); err != nil {
		t.Fatalf("record stop: %v", err)
	}
	if rec, _ := LoadBootRecord(ctx, db); rec.LastStopRequested {
		t.Fatal("the marker of the running process reads as the last stop's before the next boot took it")
	}
	if err := TakeStopRecordAtBoot(ctx, db); err != nil {
		t.Fatalf("take at boot: %v", err)
	}
	if rec, _ := LoadBootRecord(ctx, db); !rec.LastStopRequested {
		t.Fatal("the boot after a requested stop does not read the marker")
	}
	if err := TakeStopRecordAtBoot(ctx, db); err != nil {
		t.Fatalf("second take: %v", err)
	}
	if rec, _ := LoadBootRecord(ctx, db); rec.LastStopRequested {
		t.Fatal("the marker outlived the boot after the requested stop")
	}
}

// TestBootRecordMatterStartedVersion: the version is stored and read back,
// and leaves the stop marker alone.
func TestBootRecordMatterStartedVersion(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "boot-record-version.db")
	ctx := context.Background()

	if err := SetMatterStartedVersion(ctx, db, "0.108.0"); err != nil {
		t.Fatalf("set version: %v", err)
	}
	rec, err := LoadBootRecord(ctx, db)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if rec != (BootRecord{MatterStartedVersion: "0.108.0"}) {
		t.Fatalf("record = %+v, want only the version set", rec)
	}
}
