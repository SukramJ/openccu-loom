// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// forbiddenReliabilityDeps are the package prefixes internal/client/reliability
// must not reach, transitively or directly.
//
// The reliability primitives are meant to be composable in isolation: a
// circuit breaker, a retry, a throttle, a coalescer, a ping/pong tracker
// and a command tracker that [client.InterfaceClient] wires together. Its
// package doc states the property outright — "Each primitive is an
// independent Go type with no transport dependencies" — and the property
// had already been lost: CommandTracker.AddCombinedParameter called into
// internal/client/backends to parse a combined-parameter wire string,
// which pulled the whole backend surface (and internal/httpx with it) into
// a package that is supposed to know nothing about the wire.
//
// A dependency like that is invisible in a passing build. This makes the
// doc comment measurable.
var forbiddenReliabilityDeps = []string{
	"github.com/SukramJ/openccu-loom/internal/client/backends",
	"github.com/SukramJ/openccu-loom/internal/client/transport",
	"github.com/SukramJ/openccu-loom/internal/httpx",
	"github.com/SukramJ/openccu-loom/pkg/hmproto",
	"github.com/SukramJ/openccu-loom/pkg/hmapi",
}

// TestReliabilityPrimitivesCarryNoTransportDependency walks the transitive
// import graph of internal/client/reliability and fails when it reaches any
// transport, backend or wire-DTO package.
func TestReliabilityPrimitivesCarryNoTransportDependency(t *testing.T) {
	t.Parallel()

	const target = "github.com/SukramJ/openccu-loom/internal/client/reliability"

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps,
		Dir:  repoRoot(t),
	}
	loaded, err := packages.Load(cfg, target)
	if err != nil {
		t.Fatalf("packages.Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d packages, want exactly 1 (%s)", len(loaded), target)
	}
	if len(loaded[0].Imports) == 0 {
		t.Fatal("no imports resolved; the walk is broken and this test would pass vacuously")
	}

	// Collect the transitive import set.
	seen := map[string]bool{}
	var walk func(p *packages.Package)
	walk = func(p *packages.Package) {
		for path, imp := range p.Imports {
			if seen[path] {
				continue
			}
			seen[path] = true
			walk(imp)
		}
	}
	walk(loaded[0])

	var violations []string
	for path := range seen {
		for _, bad := range forbiddenReliabilityDeps {
			if path == bad || strings.HasPrefix(path, bad+"/") {
				violations = append(violations, path)
				break
			}
		}
	}
	sort.Strings(violations)

	if len(violations) > 0 {
		t.Errorf(
			"internal/client/reliability transitively imports %s.\n"+
				"Its package doc (internal/client/reliability/doc.go) states: "+
				"\"Each primitive is an independent Go type with no transport dependencies; "+
				"[InterfaceClient] composes them.\" Parse wire shapes in the composing layer "+
				"and hand the primitive plain values.",
			strings.Join(violations, ", "),
		)
	}
}
