// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// The tests in this file validate the SHAPE of the committed
// notes/parity/dead-code-inventory.json snapshot: that it exists, that
// its whitelist/unreachable/summary/by_package sections are internally
// consistent, and that no test file leaked into the unreachable list.
// None of them re-run `go run ./script/reachability` and compare its
// output against this snapshot, and none enforces a ceiling on the
// unreachable count — a green run here says the last committed snapshot
// is well-formed, not that the current tree matches it or that the
// unreachable count has not grown. Regenerate and diff the snapshot by
// hand (`make reachability`) to check the tree itself.
package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// inventoryWhitelistEntry mirrors one whitelist row of dead-code-inventory.json.
type inventoryWhitelistEntry struct {
	Package    string `json:"package"`
	Identifier string `json:"identifier"`
	Reason     string `json:"reason"`
	File       string `json:"file"`
	Line       int    `json:"line"`
}

// inventoryUnreachableEntry mirrors one unreachable row of dead-code-inventory.json.
type inventoryUnreachableEntry struct {
	Package    string `json:"package"`
	Identifier string `json:"identifier"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Kind       string `json:"kind"`
}

// inventorySummary mirrors the summary object of dead-code-inventory.json.
type inventorySummary struct {
	TotalExported int `json:"total_exported"`
	Reachable     int `json:"reachable"`
	Whitelisted   int `json:"whitelisted"`
	Unreachable   int `json:"unreachable"`
}

// inventoryPackageSummary mirrors one by_package row of dead-code-inventory.json.
type inventoryPackageSummary struct {
	Package          string `json:"package"`
	UnreachableFuncs int    `json:"unreachable_funcs"`
	UnreachableTypes int    `json:"unreachable_types"`
	UnreachableOther int    `json:"unreachable_other"`
}

// deadCodeInventory ist das vollständige Inventory-Dokument.
type deadCodeInventory struct {
	Generated   string                      `json:"generated"`
	Head        string                      `json:"head"`
	EntryPoints []string                    `json:"entry_points"`
	Summary     inventorySummary            `json:"summary"`
	ByPackage   []inventoryPackageSummary   `json:"by_package"`
	Unreachable []inventoryUnreachableEntry `json:"unreachable"`
	Whitelisted []inventoryWhitelistEntry   `json:"whitelisted"`
}

// repoRootFromTestFile resolves the repo root relative to this test file.
func repoRootFromTestFile(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// tests/contract/reachability_test.go → ../../ = repo root
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("repo root auflösen: %v", err)
	}
	return abs
}

// loadDeadCodeInventory liest und deserialisiert notes/parity/dead-code-inventory.json.
func loadDeadCodeInventory(t *testing.T) deadCodeInventory {
	t.Helper()
	root := repoRootFromTestFile(t)
	path := filepath.Join(root, "notes", "parity", "dead-code-inventory.json")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("dead-code-inventory.json nicht lesbar (%s): %v\n"+
			"Tipp: 'go run ./script/reachability' ausführen um das Inventory zu erzeugen.", path, err)
	}

	var inv deadCodeInventory
	if err := json.Unmarshal(data, &inv); err != nil {
		t.Fatalf("dead-code-inventory.json is not valid JSON: %v", err)
	}
	return inv
}

// TestReachabilitySnapshotExists checks that the inventory file exists and
// parses. It is the floor the rest of this file stands on: every other test
// here loads the same document, so a missing or truncated inventory fails
// here first rather than passing everything vacuously.
func TestReachabilitySnapshotExists(t *testing.T) {
	root := repoRootFromTestFile(t)
	path := filepath.Join(root, "notes", "parity", "dead-code-inventory.json")

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("notes/parity/dead-code-inventory.json fehlt.\n" +
			"Ausführen: go run ./script/reachability\n" +
			"Danach das Inventory prüfen und committen.")
	}

	inv := loadDeadCodeInventory(t)

	if inv.Generated == "" {
		t.Error("generated-Feld ist leer")
	}
	if inv.Head == "" {
		t.Error("head-Feld ist leer")
	}
	if len(inv.EntryPoints) == 0 {
		t.Error("entry_points ist leer")
	}

	t.Logf(
		"Inventory geladen: generated=%s head=%s total_exported=%d unreachable=%d whitelisted=%d",
		inv.Generated, inv.Head,
		inv.Summary.TotalExported,
		inv.Summary.Unreachable,
		inv.Summary.Whitelisted,
	)
}

// TestReachabilitySnapshotWhitelistFormat verifies that all whitelist entries have the
// required JSON fields populated (package, identifier, reason, file, line).
func TestReachabilitySnapshotWhitelistFormat(t *testing.T) {
	inv := loadDeadCodeInventory(t)

	if len(inv.Whitelisted) == 0 {
		// Kein Whitelist-Eintrag ist kein Fehler — solange das Inventory existiert.
		t.Log("Keine Whitelist-Einträge vorhanden (korrekt wenn noch keine loom:reachable-Kommentare gesetzt sind)")
		return
	}

	for i, entry := range inv.Whitelisted {
		if entry.Package == "" {
			t.Errorf("Whitelist[%d]: package ist leer (identifier=%q)", i, entry.Identifier)
		}
		if entry.Identifier == "" {
			t.Errorf("Whitelist[%d]: identifier ist leer (package=%q)", i, entry.Package)
		}
		if entry.Reason == "" {
			t.Errorf("Whitelist[%d]: reason ist leer (%s.%s) — loom:reachable:reason=... muss einen Text haben",
				i, entry.Package, entry.Identifier)
		}
		if entry.File == "" {
			t.Errorf("Whitelist[%d]: file ist leer (%s.%s)", i, entry.Package, entry.Identifier)
		}
		if entry.Line <= 0 {
			t.Errorf("Whitelist[%d]: line muss > 0 sein (%s.%s, file=%s)",
				i, entry.Package, entry.Identifier, entry.File)
		}
	}

	t.Logf("whitelist format ok: %d entries checked", len(inv.Whitelisted))
}

// TestReachabilitySnapshotUnreachableFormat checks that every unreachable
// entry carries the fields its consumers read.//
// This is a shape check on a generated artefact, not a statement about the
// tree. The inventory is regenerated wholesale by script/reachability, which
// writes every counter as the len() of the slice beside it, so the equalities
// here hold by construction of the generator and no production edit can break
// them. What it does catch is corruption of the committed file — a bad merge
// resolution of thirty thousand generated lines, which this repo is worked on
// concurrently enough to make plausible. The guard that is tethered to the
// tree is TestReachabilitySnapshotUnreachableCountHasACeiling.
func TestReachabilitySnapshotUnreachableFormat(t *testing.T) {
	inv := loadDeadCodeInventory(t)

	validKinds := map[string]bool{"func": true, "type": true, "var": true, "const": true, "unknown": true}

	for i, entry := range inv.Unreachable {
		if entry.Package == "" {
			t.Errorf("Unreachable[%d]: package ist leer (identifier=%q)", i, entry.Identifier)
		}
		if entry.Identifier == "" {
			t.Errorf("Unreachable[%d]: identifier ist leer (package=%q)", i, entry.Package)
		}
		if entry.File == "" {
			t.Errorf("Unreachable[%d]: file ist leer (%s.%s)", i, entry.Package, entry.Identifier)
		}
		if entry.Line <= 0 {
			t.Errorf("Unreachable[%d]: line muss > 0 sein (%s.%s)", i, entry.Package, entry.Identifier)
		}
		if !validKinds[entry.Kind] {
			t.Errorf("Unreachable[%d]: ungültiger kind=%q (%s.%s)",
				i, entry.Kind, entry.Package, entry.Identifier)
		}
	}

	if t.Failed() {
		t.Logf("unreachable format errors: see notes/parity/dead-code-inventory.json")
	} else {
		t.Logf("unreachable format ok: %d entries checked", len(inv.Unreachable))
	}
}

// TestReachabilitySnapshotSummaryConsistency checks the summary counters
// against the arrays they count.//
// This is a shape check on a generated artefact, not a statement about the
// tree. The inventory is regenerated wholesale by script/reachability, which
// writes every counter as the len() of the slice beside it, so the equalities
// here hold by construction of the generator and no production edit can break
// them. What it does catch is corruption of the committed file — a bad merge
// resolution of thirty thousand generated lines, which this repo is worked on
// concurrently enough to make plausible. The guard that is tethered to the
// tree is TestReachabilitySnapshotUnreachableCountHasACeiling.
func TestReachabilitySnapshotSummaryConsistency(t *testing.T) {
	inv := loadDeadCodeInventory(t)

	if inv.Summary.Unreachable != len(inv.Unreachable) {
		t.Errorf("summary.unreachable=%d does not match len(unreachable)=%d",
			inv.Summary.Unreachable, len(inv.Unreachable))
	}
	if inv.Summary.Whitelisted != len(inv.Whitelisted) {
		t.Errorf("summary.whitelisted=%d does not match len(whitelisted)=%d",
			inv.Summary.Whitelisted, len(inv.Whitelisted))
	}
	if inv.Summary.TotalExported < inv.Summary.Reachable+inv.Summary.Whitelisted+inv.Summary.Unreachable {
		t.Errorf("summary.total_exported=%d < reachable+whitelisted+unreachable=%d",
			inv.Summary.TotalExported,
			inv.Summary.Reachable+inv.Summary.Whitelisted+inv.Summary.Unreachable)
	}
}

// TestReachabilitySnapshotByPackageConsistency checks the by_package counters
// against the unreachable entries. Unlike its siblings above it re-implements
// the aggregation rather than re-reading it, so a change to the generator's
// grouping does reach this test.
func TestReachabilitySnapshotByPackageConsistency(t *testing.T) {
	inv := loadDeadCodeInventory(t)

	if len(inv.ByPackage) == 0 && len(inv.Unreachable) > 0 {
		t.Error("by_package ist leer aber unreachable hat Einträge")
		return
	}

	// Aggregiere Unreachable-Einträge nach Package und Kind
	type counts struct{ funcs, types, other int }
	expected := make(map[string]counts)
	for _, item := range inv.Unreachable {
		c := expected[item.Package]
		switch item.Kind {
		case "func":
			c.funcs++
		case "type":
			c.types++
		default:
			c.other++
		}
		expected[item.Package] = c
	}

	for _, ps := range inv.ByPackage {
		exp, ok := expected[ps.Package]
		if !ok {
			t.Errorf("by_package enthält Package %q das nicht in unreachable vorkommt", ps.Package)
			continue
		}
		if ps.UnreachableFuncs != exp.funcs {
			t.Errorf("by_package[%s].unreachable_funcs=%d stimmt nicht mit gezählten %d überein",
				ps.Package, ps.UnreachableFuncs, exp.funcs)
		}
		if ps.UnreachableTypes != exp.types {
			t.Errorf("by_package[%s].unreachable_types=%d stimmt nicht mit gezählten %d überein",
				ps.Package, ps.UnreachableTypes, exp.types)
		}
		if ps.UnreachableOther != exp.other {
			t.Errorf("by_package[%s].unreachable_other=%d stimmt nicht mit gezählten %d überein",
				ps.Package, ps.UnreachableOther, exp.other)
		}
	}

	t.Logf("by_package Konsistenz OK: %d Packages", len(inv.ByPackage))
}

// TestReachabilitySnapshotProductionOnlyExists asserts that the production-only
// inventory file exists. It is generated by `go run ./script/reachability
// -production-only` and lists only items reachable from non-test entry
// points. The production-only inventory typically has more unreachable
// entries than the combined inventory — that is the intended outcome:
// test-only callers hide real dead-code in the combined run.
func TestReachabilitySnapshotProductionOnlyExists(t *testing.T) {
	root := repoRootFromTestFile(t)
	path := filepath.Join(root, "notes", "parity", "dead-code-production-only.json")

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("notes/parity/dead-code-production-only.json fehlt.\n" +
			"Ausführen: go run ./script/reachability -production-only\n" +
			"Danach das Inventory prüfen und committen.")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("dead-code-production-only.json nicht lesbar: %v", err)
	}
	var inv deadCodeInventory
	if err := json.Unmarshal(data, &inv); err != nil {
		t.Fatalf("dead-code-production-only.json JSON-Fehler: %v", err)
	}
	if inv.Generated == "" {
		t.Error("generated-Feld ist leer")
	}
	t.Logf("production-only inventory: unreachable=%d whitelisted=%d total_exported=%d",
		inv.Summary.Unreachable, inv.Summary.Whitelisted, inv.Summary.TotalExported)
}

// TestReachabilitySnapshotHasNoTestFiles checks that no _test.go or tests/
// item lands in the unreachable array — the analyzer auto-whitelists them.//
// This is a shape check on a generated artefact, not a statement about the
// tree. The inventory is regenerated wholesale by script/reachability, which
// writes every counter as the len() of the slice beside it, so the equalities
// here hold by construction of the generator and no production edit can break
// them. What it does catch is corruption of the committed file — a bad merge
// resolution of thirty thousand generated lines, which this repo is worked on
// concurrently enough to make plausible. The guard that is tethered to the
// tree is TestReachabilitySnapshotUnreachableCountHasACeiling.
func TestReachabilitySnapshotHasNoTestFiles(t *testing.T) {
	inv := loadDeadCodeInventory(t)

	for i, entry := range inv.Unreachable {
		if strings.HasSuffix(entry.File, "_test.go") {
			t.Errorf("unreachable[%d]: a _test.go item is in the unreachable array (%s.%s file=%s) — it should have been auto-whitelisted",
				i, entry.Package, entry.Identifier, entry.File)
		}
		if strings.HasPrefix(entry.File, "tests/") {
			t.Errorf("Unreachable[%d]: tests/-Item im unreachable-Array (%s.%s file=%s) — soll auto-whitelisted sein",
				i, entry.Package, entry.Identifier, entry.File)
		}
	}

	if !t.Failed() {
		t.Logf("Keine Test-File-Items im unreachable-Array: OK")
	}
}

// reachabilityUnreachableCeiling is the number of unreachable exported
// identifiers the committed snapshot is allowed to carry.
//
// It is a ratchet, and it only moves down. Raising it is a decision someone
// has to make and explain in the commit that raises it — which is the entire
// point: without a ceiling, `make reachability` regenerating a snapshot with
// two hundred more dead identifiers than the last one produced a green test
// run and a diff nobody had a reason to read closely.
// Raised 3007 -> 3011 for the two climate-correction wire DTOs
// (hmapi.ClimateScheduleWriteResult, hmapi.ClimateTimeCorrection). They are
// constructed in internal/north/rest/handlers/schedules.go and serialised to
// the wire, but the analysis cannot see through JSON marshalling, so every
// payload type in pkg/hmapi/rest_contract.go lands here -- 94 of them,
// hmapi.ClimateSchedule and hmapi.ClimatePeriod among them. These two join
// that established class; no new production dead code came with them.
//
// Lowered 3033 -> 1385 by fixing what the number counts.
//
// The array held one entry per exported identifier PER LOADED SSA PACKAGE,
// and go/packages loads a package again for each test binary that links
// it. Two things followed. The count multiplied, so the ceiling could rise
// while the set of dead identifiers fell — which is how it last moved. And
// RTA answers per object, so one variant could call a symbol reachable
// while another did not: 443 identifiers were listed as dead AND counted
// as reachable in the same run, among them addonupdate.Updater, a field of
// the composition root, and addonupdate.PeriodicChecker, constructed in
// addon_update_wiring.go.
//
// The analyzer folds the variants now — whitelisted, then reachable in any
// variant, then dead — and prints how many disagreed, so the next one is
// visible rather than absorbed. 1385 is the unique count, which is what
// this ceiling was always meant to be: from here a newly dead export moves
// it by exactly one.
const reachabilityUnreachableCeiling = 1385

// TestReachabilitySnapshotUnreachableCountHasACeiling is the one test in this
// file that says something about the tree rather than about the snapshot's
// JSON shape.
//
// It is still not a dead-code guard: it reads the committed snapshot, so it
// can only notice growth once somebody regenerates. What it removes is the
// step where growth is invisible even then. The honest way to check the tree
// itself remains `make reachability` followed by reading the diff.
func TestReachabilitySnapshotUnreachableCountHasACeiling(t *testing.T) {
	inv := loadDeadCodeInventory(t)
	got := inv.Summary.Unreachable
	switch {
	case got > reachabilityUnreachableCeiling:
		t.Errorf("the committed reachability snapshot carries %d unreachable exported "+
			"identifiers, %d more than the ceiling of %d.\n\n"+
			"Either reach them (wire the seam, or delete the identifier), or whitelist each "+
			"one with a reason, or — if the growth is genuinely justified — raise "+
			"reachabilityUnreachableCeiling in the same commit and say why there.",
			got, got-reachabilityUnreachableCeiling, reachabilityUnreachableCeiling)
	case got < reachabilityUnreachableCeiling:
		t.Errorf("the committed snapshot is down to %d unreachable identifiers, below the "+
			"ceiling of %d. Lower reachabilityUnreachableCeiling to %d so the ground gained "+
			"cannot be given back unnoticed.", got, reachabilityUnreachableCeiling, got)
	}
}
