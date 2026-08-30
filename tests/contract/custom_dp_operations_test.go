// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// operationsNotYetAdvertised names every operation the dispatcher accepts
// that neither plane announces, each with the reason it is not closed by
// simply appending it to a table.
//
// Two of them are aliases and belong here permanently. The rest share one
// cause: the advertised tables are keyed by [hmenum.DataPointCategory],
// which is coarser than the concrete model type the dispatcher routes on.
// A Blind is a Cover, but a plain roller shutter is a Cover too — adding
// open_tilt to the Cover row would announce a tilt command on a shutter
// that has no slats, trading a silent omission for a false promise.
// Closing those needs the capability to be declared per data point by the
// model rather than per category by each plane; until then this list keeps
// the omission visible instead of letting it read as complete coverage.
var operationsNotYetAdvertised = map[string]string{
	"send_text":    `alias of the advertised "write"; the canonical name is announced`,
	"clear_text":   `alias of the advertised "clear"; the canonical name is announced`,
	"commit":       "text-display only, and only for displays that buffer lines",
	"ventilate":    "garage doors only; the Cover category also covers shutters and blinds",
	"open_tilt":    "slatted blinds only; announcing it per category would promise it on every Cover",
	"close_tilt":   "slatted blinds only; announcing it per category would promise it on every Cover",
	"stop_tilt":    "slatted blinds only; announcing it per category would promise it on every Cover",
	"set_combined": "slatted blinds only — sets position and tilt in one write",
	"set_on_time":  "lights and irrigation valves, not every member of either category",
}

// TestAdvertisedCustomDPOperationsMatchTheDispatcher pins the operation
// vocabulary the REST and WebSocket planes advertise against the set the
// dispatcher actually accepts.
//
// The planes each carry a hand-written category→operations table while the
// dispatcher routes per concrete model type. A table that falls behind is
// invisible in the worst way: the daemon executes an operation no client is
// told about. Climate shipped exactly that — enable_away_by_calendar and
// enable_away_by_duration were implemented, parameterised and reachable,
// and neither plane named them.
//
// The comparison is on the union, not per category, because category is
// coarser than type: set_color belongs to ColorLight, not to every Light.
// A per-category assertion would need a type→category mapping this test
// cannot read from either source, and inventing one would make the guard
// confirm its own assumption.
func TestAdvertisedCustomDPOperationsMatchTheDispatcher(t *testing.T) {
	t.Parallel()
	dispatcher := operationSwitchCases(t, "../../internal/central/adapter/custom_dp_dispatcher.go")
	if len(dispatcher) == 0 {
		t.Fatal("no operation switch found — the guard lost its subject")
	}
	rest := advertisedOperations(t, "../../internal/north/rest/handlers/custom_data_points.go")
	ws := advertisedOperations(t, "../../internal/north/rest/ws/custom_data_points.go")
	if len(rest) == 0 || len(ws) == 0 {
		t.Fatal("no advertised operations found — the guard lost its subject")
	}

	// The two planes keep separate copies of the same table. Comparing only
	// their union would let one plane silently lose an operation the other
	// still names, so they are held equal to each other first.
	for _, op := range sortedOperationNames(rest) {
		if !ws[op] {
			t.Errorf("REST advertises %q, the WebSocket plane does not", op)
		}
	}
	for _, op := range sortedOperationNames(ws) {
		if !rest[op] {
			t.Errorf("the WebSocket plane advertises %q, REST does not", op)
		}
	}

	advertised := map[string]bool{}
	for op := range rest {
		advertised[op] = true
	}
	for op := range ws {
		advertised[op] = true
	}

	for _, op := range sortedOperationNames(dispatcher) {
		if advertised[op] {
			if _, excused := operationsNotYetAdvertised[op]; excused {
				t.Errorf("%q is advertised now — drop it from operationsNotYetAdvertised", op)
			}
			continue
		}
		if _, excused := operationsNotYetAdvertised[op]; !excused {
			t.Errorf("dispatcher accepts %q but no plane advertises it — clients cannot discover it", op)
		}
	}
	for _, op := range sortedOperationNames(advertised) {
		if !dispatcher[op] {
			t.Errorf("planes advertise %q but the dispatcher rejects it — clients are invited to call it and get an error", op)
		}
	}
}

// operationSwitchCases returns the case values of every switch whose tag is
// the dispatched operation name. Restricting it to that tag keeps value
// switches — `switch strings.ToUpper(state)`, say — out of the result, which
// a blanket sweep of every case literal would wrongly collect.
func operationSwitchCases(t *testing.T, path string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		id, ok := sw.Tag.(*ast.Ident)
		if !ok || (id.Name != "op" && id.Name != "operation") {
			return true
		}
		for _, stmt := range sw.Body.List {
			cc, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, expr := range cc.List {
				if s, ok := stringLit(expr); ok {
					out[s] = true
				}
			}
		}
		return true
	})
	return out
}

// advertisedOperations returns every string literal returned by a
// supportedOperations* function in path.
func advertisedOperations(t *testing.T, path string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !strings.HasPrefix(fn.Name.Name, "supportedOperations") {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			if s, ok := stringLit(n); ok {
				out[s] = true
			}
			return true
		})
	}
	return out
}

func stringLit(n ast.Node) (string, bool) {
	lit, ok := n.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func sortedOperationNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
