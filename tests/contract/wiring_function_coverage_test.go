// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// wiringFuncsWithoutSeam names the wiring functions that hand nothing
// over, with what they do instead.
var wiringFuncsWithoutSeam = map[string]string{
	"registerFirmwareJobsFor":           "runs once per central, from the boot walk and the live-adopt path alike; the manifest records daemon-level seams, and a name declared per central would collide",
	"registerScheduledBackupJobFor":     "runs once per central, from the boot walk and the live-adopt path alike; the manifest records daemon-level seams, and a name declared per central would collide",
	"registerStandardJobs":              "installs the attach for the seam its caller declares; a second declaration would collide on the name",
	"registerStandardJobsFor":           "runs once per central, from the boot walk and the live-adopt path alike; the manifest records daemon-level seams, and a name declared per central would collide",
	"wireAddonUpdate":                   "constructs a value and returns it; the caller decides where it goes, so there is no handover here to declare",
	"wireAlarmService":                  "constructs a value and returns it; the caller decides where it goes, so there is no handover here to declare",
	"wireAuditPersistenceWithDB":        "constructs a value and returns it; the caller decides where it goes, so there is no handover here to declare",
	"wireBINRPCCallback":                "constructs a value and returns it; the caller decides where it goes, so there is no handover here to declare",
	"wireCentralNorthbound":             "runs once per central, from the boot walk and the live-adopt path alike; the manifest records daemon-level seams, and a name declared per central would collide",
	"wireConfigAssemblerFn":             "installs the attach for the seam its caller declares; a second declaration would collide on the name",
	"wireConfigStoreCrypto":             "serves the daemon binary's own `config export/import` subcommand (main.go -> runConfigCLI), which builds no central registry and therefore has no manifest; nothing in cmd/hmcli references it",
	"wireDescriptorStores":              "constructs a value and returns it; the caller decides where it goes, so there is no handover here to declare",
	"wireHistoryRecorder":               "starts collaborators that each declare their own seam through OnRegisterDeclared, so the entries exist one level down",
	"wireHistoryRetention":              "constructs a value and returns it; the caller decides where it goes, so there is no handover here to declare",
	"wireHistoryStore":                  "constructs a value and returns it; the caller decides where it goes, so there is no handover here to declare",
	"wireHubDiscoveryOnReady":           "installs the attach for a seam its caller declares one level up (wireHubReadyRestart, seam mqtt.hub_ready_restart); it subscribes and debounces, and declares nothing of its own",
	"wireMasterValuesStore":             "constructs a value and returns it; the caller decides where it goes, so there is no handover here to declare",
	"wireMatterCentralReadinessForUnit": "runs once per central, from the boot walk and the live-adopt path alike; the manifest records daemon-level seams, and a name declared per central would collide",
	"wireMatterDeviceReachableForward":  "runs once per central, from the boot walk and the live-adopt path alike; the manifest records daemon-level seams, and a name declared per central would collide",
	"wireMatterReassembleOnReady":       "composes several attaches rather than being one; the ones with a constraint declare it themselves, at the call that makes them",
	"wireMatterRuntime":                 "composes several attaches rather than being one; the ones with a constraint declare it themselves, at the call that makes them",
	"wireREST":                          "composes several attaches rather than being one; the ones with a constraint declare it themselves, at the call that makes them",
	"wireRecordingOverrides":            "constructs a value and returns it; the caller decides where it goes, so there is no handover here to declare",
	"wireRetainedOrphanSweepHook":       "installs the attach for the seam its caller declares; a second declaration would collide on the name",
	"wireSecurityIndexRefreshHook":      "installs the attach for the seam its caller declares; a second declaration would collide on the name",
	"wireSecurityService":               "constructs a value and returns it; the caller decides where it goes, so there is no handover here to declare",
	"wireSharedInfrastructure":          "composes several attaches rather than being one; the ones with a constraint declare it themselves, at the call that makes them",
	"wireSouthbound":                    "composes several attaches rather than being one; the ones with a constraint declare it themselves, at the call that makes them",
	"wireSystemStatusSubscribers":       "starts collaborators that each declare their own seam through OnRegisterDeclared, so the entries exist one level down",
	"wireValueWriterHookFns":            "installs the attach for the seam its caller declares; a second declaration would collide on the name",
	"wireValuesCacheStore":              "constructs a value and returns it; the caller decides where it goes, so there is no handover here to declare",
	"wireVisibilityUnIgnoreStore":       "constructs a value and returns it; the caller decides where it goes, so there is no handover here to declare",
	"wireWSCommands":                    "hands over logging context (logger, central name), not a collaborator whose absence stops a feature",
	"wireXMLRPCCallback":                "constructs a value and returns it; the caller decides where it goes, so there is no handover here to declare",
}

// TestEveryWiringFunctionDeclaresOrExplainsItself is the guard that makes
// ADR 0065's end-state measurable instead of aspirational.
//
// The ADR says each `wire*` function registers what it wires. That is the
// only way its first promise holds — **a seam with no entry is unwired**,
// exactly, with no name matching — because the promise is about absence,
// and absence only means something once presence is the rule.
//
// The rule is a decision ledger, not a claim that one function is one
// seam. Plenty of `wire*` functions construct a value and hand it back,
// and the caller decides where it goes; some compose several attaches
// whose seams are declared further down, by the collaborator itself. None
// of those should be forced to invent a seam — inventing one would put an
// entry in the manifest that names nothing anybody can lose.
//
// So: declare a seam, or say in one line why there is nothing here to
// declare. The exemption is the interesting half. It is where a reader
// learns that a `wire*` name meant "build" rather than "attach", or that
// the seams are one level down — and it is why a *new* wiring function
// cannot join the composition root without somebody answering the
// question.
func TestEveryWiringFunctionDeclaresOrExplainsItself(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	dir := filepath.Join(root, "cmd", "openccu-loom")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	var undeclared []string
	seen := map[string]bool{}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		full := filepath.Join(dir, name)
		file, perr := parser.ParseFile(fset, full, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", full, perr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil || fn.Recv != nil || fn.Body == nil {
				continue
			}
			fname := fn.Name.Name
			if !strings.HasPrefix(fname, "wire") && !strings.HasPrefix(fname, "register") {
				continue
			}
			seen[fname] = true
			if declaresASeam(fn) {
				continue
			}
			if _, exempt := wiringFuncsWithoutSeam[fname]; exempt {
				continue
			}
			undeclared = append(undeclared,
				fname+"  ("+name+":"+itoa(fset.Position(fn.Pos()).Line)+")")
		}
	}

	if len(seen) == 0 {
		t.Fatal("found no wiring functions — the guard is measuring nothing")
	}

	sort.Strings(undeclared)
	if len(undeclared) > 0 {
		t.Errorf("%d wiring function(s) neither declare a seam nor say why they have none:\n  %s\n\n"+
			"Declare what the function hands over — reg.Manifest().Attach(wiring.Seam{...}, ...) — or "+
			"add the function to wiringFuncsWithoutSeam with the reason. ADR 0065's promise is that a "+
			"seam with no manifest entry is unwired, and that only means something while every seam "+
			"has an entry.",
			len(undeclared), strings.Join(undeclared, "\n  "))
	}

	// Stale entries hide the next gap behind a name nobody checks.
	var stale []string
	for fname := range wiringFuncsWithoutSeam {
		if !seen[fname] {
			stale = append(stale, fname+" (no such wiring function)")
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("wiringFuncsWithoutSeam carries %d stale entr(ies):\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// declaresASeam reports whether fn's body constructs a wiring.Seam.
func declaresASeam(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		if cl, ok := n.(*ast.CompositeLit); ok && exprTypeName(cl.Type) == "wiring.Seam" {
			found = true
			return false
		}
		return true
	})
	return found
}
