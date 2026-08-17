// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// inventoryWhitelistEntry spiegelt den Whitelist-Eintrag aus dead-code-inventory.json.
type inventoryWhitelistEntry struct {
	Package    string `json:"package"`
	Identifier string `json:"identifier"`
	Reason     string `json:"reason"`
	File       string `json:"file"`
	Line       int    `json:"line"`
}

// inventoryUnreachableEntry spiegelt den Unreachable-Eintrag aus dead-code-inventory.json.
type inventoryUnreachableEntry struct {
	Package    string `json:"package"`
	Identifier string `json:"identifier"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Kind       string `json:"kind"`
}

// inventorySummary spiegelt das Summary-Objekt aus dead-code-inventory.json.
type inventorySummary struct {
	TotalExported int `json:"total_exported"`
	Reachable     int `json:"reachable"`
	Whitelisted   int `json:"whitelisted"`
	Unreachable   int `json:"unreachable"`
}

// inventoryPackageSummary spiegelt einen by_package-Eintrag aus dead-code-inventory.json.
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

// repoRootFromTestFile bestimmt den Repo-Root relativ zu dieser Testdatei.
func repoRootFromTestFile(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller schlug fehl")
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
		t.Fatalf("dead-code-inventory.json JSON-Fehler: %v", err)
	}
	return inv
}

// TestReachabilityInventoryExists prüft dass das Inventory-File existiert und lesbar ist.
// Inhaltliche Baseline-Prüfungen folgen in einer späteren Phase, nachdem das erste
// Inventory-Run manuell reviewed und committed wurde.
func TestReachabilityInventoryExists(t *testing.T) {
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

// TestReachabilityWhitelistFormat verifies that all whitelist entries have the
// required JSON fields populated (package, identifier, reason, file, line).
func TestReachabilityWhitelistFormat(t *testing.T) {
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

	t.Logf("Whitelist-Format OK: %d Einträge geprüft", len(inv.Whitelisted))
}

// TestReachabilityUnreachableFormat prüft dass alle Unreachable-Einträge
// vollständige und konsistente Felder haben.
func TestReachabilityUnreachableFormat(t *testing.T) {
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
		t.Logf("Unreachable-Format-Fehler: prüfe notes/parity/dead-code-inventory.json")
	} else {
		t.Logf("Unreachable-Format OK: %d Einträge geprüft", len(inv.Unreachable))
	}
}

// TestReachabilitySummaryConsistency prüft dass die Summary-Zähler konsistent sind.
func TestReachabilitySummaryConsistency(t *testing.T) {
	inv := loadDeadCodeInventory(t)

	if inv.Summary.Unreachable != len(inv.Unreachable) {
		t.Errorf("summary.unreachable=%d stimmt nicht mit len(unreachable)=%d überein",
			inv.Summary.Unreachable, len(inv.Unreachable))
	}
	if inv.Summary.Whitelisted != len(inv.Whitelisted) {
		t.Errorf("summary.whitelisted=%d stimmt nicht mit len(whitelisted)=%d überein",
			inv.Summary.Whitelisted, len(inv.Whitelisted))
	}
	if inv.Summary.TotalExported < inv.Summary.Reachable+inv.Summary.Whitelisted+inv.Summary.Unreachable {
		t.Errorf("summary.total_exported=%d < reachable+whitelisted+unreachable=%d (Inkonsistenz)",
			inv.Summary.TotalExported,
			inv.Summary.Reachable+inv.Summary.Whitelisted+inv.Summary.Unreachable)
	}
}

// TestReachabilityByPackageConsistency prüft dass by_package-Zähler mit den
// Unreachable-Einträgen übereinstimmen.
func TestReachabilityByPackageConsistency(t *testing.T) {
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

// TestReachabilityProductionOnlyExists asserts that the production-only
// inventory file exists. It is generated by `go run ./script/reachability
// -production-only` and lists only items reachable from non-test entry
// points. The production-only inventory typically has more unreachable
// entries than the combined inventory — that is the intended outcome:
// test-only callers hide real dead-code in the combined run.
func TestReachabilityProductionOnlyExists(t *testing.T) {
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

// TestReachabilityNoTestFilesInUnreachable prüft dass keine _test.go oder tests/-Items
// im unreachable-Array erscheinen (sie sollen in whitelisted landen).
func TestReachabilityNoTestFilesInUnreachable(t *testing.T) {
	inv := loadDeadCodeInventory(t)

	for i, entry := range inv.Unreachable {
		if strings.HasSuffix(entry.File, "_test.go") {
			t.Errorf("Unreachable[%d]: _test.go-Item im unreachable-Array (%s.%s file=%s) — soll auto-whitelisted sein",
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
