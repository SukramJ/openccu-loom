// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// The onboarding phase a device sits in is written in three places that
// no compiler couples: the coordinator constants the onboarding loop
// actually writes and reads, the store constant that fills the default
// when a row is inserted without one, and the column default declared by
// the goose migration. All three are untyped strings, so a rename in one
// place leaves the other two on the old spelling and rows split into two
// vocabularies that each half of the system considers unknown.
//
// The store deliberately does not import the coordinator package — the
// dependency runs the other way, through the adapter — so this guard is
// what keeps the copies equal.
const pendingPhaseMigration = "internal/store/sqlite/migrations/042_pending_devices_phase.sql"

// pendingPhaseDefaultRE captures the phase column's declared default from
// the migration's ALTER line. It is deliberately loose about whitespace
// and about the value itself: a guard that only matches the correct
// spelling cannot report the wrong one.
var pendingPhaseDefaultRE = regexp.MustCompile(
	`ALTER\s+TABLE\s+pending_devices\s+ADD\s+COLUMN\s+phase\s+TEXT\s+NOT\s+NULL\s+DEFAULT\s+'([^']*)'`,
)

// TestPendingDevicePhaseVocabularyIsOneVocabulary pins the three copies
// of the onboarding phase vocabulary against each other.
func TestPendingDevicePhaseVocabularyIsOneVocabulary(t *testing.T) {
	if sqlite.PhasePending != coordinators.PhasePending {
		t.Errorf("sqlite.PhasePending = %q, coordinators.PhasePending = %q — the store default "+
			"no longer matches the vocabulary the onboarding loop writes",
			sqlite.PhasePending, coordinators.PhasePending)
	}
	if coordinators.PhaseUnreleased != "unreleased" {
		t.Errorf("coordinators.PhaseUnreleased = %q, want \"unreleased\" — rows already persisted "+
			"under the old spelling would load into no branch at all",
			coordinators.PhaseUnreleased)
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(pendingPhaseMigration)))
	if err != nil {
		t.Fatalf("read %s: %v", pendingPhaseMigration, err)
	}
	m := pendingPhaseDefaultRE.FindStringSubmatch(string(src))
	if m == nil {
		t.Fatalf("%s no longer declares a DEFAULT for pending_devices.phase — "+
			"the guard cannot compare what it cannot find", pendingPhaseMigration)
	}
	if m[1] != coordinators.PhasePending {
		t.Errorf("%s declares DEFAULT '%s', coordinators.PhasePending = %q — a row inserted "+
			"without an explicit phase lands outside the vocabulary",
			pendingPhaseMigration, m[1], coordinators.PhasePending)
	}
	if !strings.Contains(string(src), coordinators.PhaseUnreleased) {
		t.Errorf("%s no longer mentions %q, the second phase of the same column",
			pendingPhaseMigration, coordinators.PhaseUnreleased)
	}
}
