// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
)

// newUnitForArchiveNameTest constructs a *central.Unit the way the other
// adapter tests do (see [buildCCUMaintenanceFixture] in
// ccu_maintenance_test.go) and seeds its system information via
// [central.Unit.SetSystemInformation].
func newUnitForArchiveNameTest(t *testing.T, centralName string, info central.SystemInfo) *central.Unit {
	t.Helper()
	u, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	u.SetSystemInformation(info)
	return u
}

// TestCcuArchiveNameKeepsDotsInFirmwareVersion pins the whole point of
// [backupNameSegment]: the existing backupSafeName maps '.' to '_', which
// would turn a real firmware version like "3.87.6.20260404" into
// "3_87_6_20260404" — a string nobody can match against a release. The
// archive name must keep the dots.
func TestCcuArchiveNameKeepsDotsInFirmwareVersion(t *testing.T) {
	t.Parallel()
	u := newUnitForArchiveNameTest(t, "ccu-01", central.SystemInfo{
		Hostname: "ccu-01.local",
		Version:  "3.87.6.20260404",
	})
	at := time.Date(2026, 8, 17, 14, 5, 0, 0, time.Local)

	got := ccuArchiveName(u, at)
	want := "ccu-01.local-3.87.6.20260404-2026-08-17-1405.sbk"
	if got != want {
		t.Fatalf("ccuArchiveName = %q, want %q", got, want)
	}
}

// TestCcuArchiveNameFallsBackToCentralNameWhenHostnameEmpty verifies that a
// CCU which has not (yet) reported its own hostname still yields a name,
// built from the central's configured name instead.
func TestCcuArchiveNameFallsBackToCentralNameWhenHostnameEmpty(t *testing.T) {
	t.Parallel()
	u := newUnitForArchiveNameTest(t, "ccu-fallback", central.SystemInfo{
		Version: "3.87.6.20260404",
	})
	at := time.Date(2026, 8, 17, 9, 30, 0, 0, time.Local)

	got := ccuArchiveName(u, at)
	want := "ccu-fallback-3.87.6.20260404-2026-08-17-0930.sbk"
	if got != want {
		t.Fatalf("ccuArchiveName = %q, want %q", got, want)
	}
}

// TestCcuArchiveNameEmptyWhenVersionMissing verifies that a hostname alone
// is not enough: a half-filled name (CCU identity but no firmware) is worse
// than none, because it reads as a fact the archive does not actually carry.
func TestCcuArchiveNameEmptyWhenVersionMissing(t *testing.T) {
	t.Parallel()
	u := newUnitForArchiveNameTest(t, "ccu-01", central.SystemInfo{
		Hostname: "ccu-01.local",
	})

	if got := ccuArchiveName(u, time.Now()); got != "" {
		t.Fatalf("ccuArchiveName = %q, want empty when the firmware version is missing", got)
	}
}

// TestCcuArchiveNameEmptyWhenNoSystemInformationReportedYet covers the
// common boot-time case: the CCU has reported neither hostname nor version,
// and the central name alone (used as the hostname fallback) is not a
// substitute for the still-missing firmware version.
func TestCcuArchiveNameEmptyWhenNoSystemInformationReportedYet(t *testing.T) {
	t.Parallel()
	u := newUnitForArchiveNameTest(t, "ccu-01", central.SystemInfo{})

	if got := ccuArchiveName(u, time.Now()); got != "" {
		t.Fatalf("ccuArchiveName = %q, want empty before the CCU has reported anything", got)
	}
}

// TestCcuArchiveNameEmptyForNilUnit verifies the nil-receiver guard: a caller
// passing a nil *central.Unit must not panic.
func TestCcuArchiveNameEmptyForNilUnit(t *testing.T) {
	t.Parallel()
	if got := ccuArchiveName(nil, time.Now()); got != "" {
		t.Fatalf("ccuArchiveName(nil, ...) = %q, want empty", got)
	}
}

// TestBackupNameSegmentKeepsDots is the direct unit-level twin of
// TestCcuArchiveNameKeepsDotsInFirmwareVersion: backupNameSegment itself
// must leave a dotted firmware version untouched.
func TestBackupNameSegmentKeepsDots(t *testing.T) {
	t.Parallel()
	const version = "3.87.6.20260404"
	if got := backupNameSegment(version); got != version {
		t.Fatalf("backupNameSegment(%q) = %q, want unchanged", version, got)
	}
}

// TestBackupNameSegmentSanitizesUnsafeCharacters verifies that anything
// outside A-Za-z0-9._- is mapped to '_' — the characters that would be
// unsafe in a filesystem path or an HTTP Content-Disposition header.
func TestBackupNameSegmentSanitizesUnsafeCharacters(t *testing.T) {
	t.Parallel()
	got := backupNameSegment(`ccu 01/name\evil"; x=1`)
	want := "ccu_01_name_evil___x_1"
	if got != want {
		t.Fatalf("backupNameSegment = %q, want %q", got, want)
	}
}

// TestBackupNameSegmentEmptyWhenOnlyPunctuationSurvives verifies that a
// segment left with nothing but '.', '_' and '-' after sanitising collapses
// to "", which suppresses the whole archive name rather than producing one
// that looks meaningful but carries no information.
func TestBackupNameSegmentEmptyWhenOnlyPunctuationSurvives(t *testing.T) {
	t.Parallel()
	if got := backupNameSegment("   "); got != "" {
		t.Fatalf("backupNameSegment(spaces only) = %q, want empty", got)
	}
	if got := backupNameSegment("._-"); got != "" {
		t.Fatalf("backupNameSegment(punctuation only) = %q, want empty", got)
	}
}
