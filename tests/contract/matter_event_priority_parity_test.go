// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// matterEventPriority pins the Matter event priority of every event the
// bridge emits against matter.js HEAD, which is this project's gold standard
// for the Matter side (CLAUDE.md → References).
//
// The key is the event-id expression as the emit call writes it; the value is
// the priority matter.js declares, with the element file and line it is read
// from so the claim stays checkable. matter.js keeps these in
// ../matter.js/packages/model/src/standard/elements/<cluster>.element.ts as
// `Event({ name: …, id: …, priority: "critical" | "info" })`.
//
// Priority is not cosmetic. It decides which records the event buffer
// harvests first (internal/north/matter/im/eventlog.go), and Critical is the
// class the buffer protects. Miscategorising a high-frequency event as
// Critical is how the boot-once StartUp and BootReason events — the ones a
// controller reads out-of-band at Subscribe-Initial — got pushed out of the
// buffer by a single CCU interface flap.
var matterEventPriority = map[string]struct {
	priority string
	source   string
}{
	// BasicInformation — basic-information.element.ts
	"basicInfoEventStartUp":          {"Critical", "basic-information.element.ts:108"},
	"basicInfoEventShutDown":         {"Critical", "basic-information.element.ts:111"},
	"basicInfoEventLeave":            {"Info", "basic-information.element.ts:113"},
	"basicInfoEventReachableChanged": {"Info", "basic-information.element.ts:117"},

	// GeneralDiagnostics — general-diagnostics.element.ts
	"gendiagEventBootReason": {"Critical", "general-diagnostics.element.ts:90"},

	// BridgedDeviceBasicInformation — bridged-device-basic-information.element.ts.
	// Emitted from two paths: the cluster server's own setter and the
	// bridge's push forward. They must agree.
	"bridgedBasicInfoEventReachableChanged": {"Info", "bridged-device-basic-information.element.ts:55"},
	"core.EventReachableChanged":            {"Info", "bridged-device-basic-information.element.ts:55"},

	// Switch — switch.element.ts. Every event of the cluster is "info"
	// there; two of ours were Critical, which is also why they disagreed
	// with their own sibling emitters.
	"MatterEventInitialPress": {"Info", "switch.element.ts:48"},
	"MatterEventLongPress":    {"Info", "switch.element.ts:52"},
	"MatterEventShortRelease": {"Info", "switch.element.ts:57"},
	"MatterEventLongRelease":  {"Info", "switch.element.ts:65"},

	// DoorLock — door-lock-cluster.element.ts
	"wire.DoorLockEventLockOperation": {"Critical", "door-lock-cluster.element.ts:181"},

	// AccessControl — access-control.element.ts
	"accessControlEventEntryChanged":     {"Info", "access-control.element.ts:73"},
	"accessControlEventExtensionChanged": {"Info", "access-control.element.ts:88"},
}

// TestMatterEventPrioritiesMatchMatterJS walks every MatterEmitEvent call in
// the Matter tree and checks the priority it passes against
// [matterEventPriority].
//
// The call sites are found in the source rather than listed here, so a new
// emitter cannot slip in with an unreviewed priority: an event id that is not
// in the table fails with the instruction to read matter.js for it. That is
// the half a hand-written list cannot give — the four wrong priorities this
// table now pins had unit tests asserting them, two of which even cited the
// matter.js line that says the opposite.
func TestMatterEventPrioritiesMatchMatterJS(t *testing.T) {
	root := repoRootForHelpers(t)
	matterDir := filepath.Join(root, "internal", "north", "matter")

	seen := map[string]bool{}
	calls := 0

	err := filepath.WalkDir(matterDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		rel, _ := filepath.Rel(root, path)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "MatterEmitEvent" || len(call.Args) != 5 {
				return true
			}
			event := exprText(call.Args[2])
			// The priority constants live in pkg/mattercontract; pkg/interfaces
			// keeps prefixed aliases for callers outside the Matter subtree,
			// so accept either spelling. An unrecognised one keeps its
			// qualifier and fails the comparison loudly rather than silently
			// reading as a match.
			priority := exprText(call.Args[4])
			for _, prefix := range []string{"mattercontract.EventPriority", "interfaces.MatterEventPriority"} {
				if after, found := strings.CutPrefix(priority, prefix); found {
					priority = after
					break
				}
			}
			pos := fset.Position(call.Pos())
			calls++

			want, known := matterEventPriority[event]
			if !known {
				t.Errorf("%s:%d: MatterEmitEvent for %q has no entry in matterEventPriority — "+
					"read the event's priority from matter.js "+
					"(packages/model/src/standard/elements/<cluster>.element.ts) and add it",
					rel, pos.Line, event)
				return true
			}
			seen[event] = true
			if priority != want.priority {
				t.Errorf("%s:%d: %s emitted with priority %s, want %s (matter.js %s)",
					rel, pos.Line, event, priority, want.priority, want.source)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", matterDir, err)
	}

	// Negative control on the walk itself: a search that finds nothing
	// reports every priority as correct.
	if calls == 0 {
		t.Fatal("no MatterEmitEvent call found — the walk is broken, not the priorities")
	}
	for event := range matterEventPriority {
		if !seen[event] {
			t.Errorf("matterEventPriority lists %q but nothing emits it — "+
				"drop the entry, or the table drifts from the code it claims to pin", event)
		}
	}
}

// exprText renders an expression the way the source writes it, so a
// qualified identifier (core.EventReachableChanged) and a package-local one
// (basicInfoEventStartUp) are both usable as table keys. A literal renders
// as written, quotes included, for callers that resolve constants and
// literals through the same path.
func exprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprText(v.X) + "." + v.Sel.Name
	case *ast.BasicLit:
		return v.Value
	default:
		return ""
	}
}
