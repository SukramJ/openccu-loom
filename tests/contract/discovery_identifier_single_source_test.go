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
	"strconv"
	"strings"
	"testing"
)

// discoveryIdentifierPrefix is the literal every Home Assistant identifier this
// daemon emits begins with. Two functions are allowed to write it; everything
// else must go through them.
const discoveryIdentifierPrefix = "openccu-loom_"

// discoveryIdentifierBuilders are the only functions permitted to compose the
// prefix into an identifier. They are the ones that know when an address needs
// its central: [routingkey.NeedsCentralScope] is true for the address classes
// that repeat across CCUs — the internal `INT000*` range, CUxD serials, the
// virtual-remote buses — and false for a real device serial, which is globally
// unique on its own.
var discoveryIdentifierBuilders = map[string]struct{}{
	"physicalDeviceIdentifier": {},
	"centralDeviceIdentifier":  {},
	"hubDeviceBlock":           {},
	"hubNodeID":                {},
	"discoveryNodeID":          {},
}

// discoveryIdentifierExemptions lists call sites that spell the prefix for a
// reason other than composing an identifier, with that reason. An entry is a
// claim someone checked.
var discoveryIdentifierExemptions = map[string]string{
	"discovery_week_profile.go": "reads the prefix to test whether an id already carries it, and prepends only when it does not; the id itself arrives from a builder",
	"retain_cleanup.go":         "matches retained payloads left by earlier builds, whose ids this build no longer produces — a parser, not a producer",
	// The alarm and security planes are daemon-level, not per-CCU: their
	// identifiers key on a zone or a hazard class, both of which are concepts
	// of this daemon rather than of any one CCU, and alarmDeviceBlock takes no
	// arguments at all. There is no address in them to scope and no second
	// central that could collide. Should either plane ever gain a per-device
	// identifier, this exemption becomes wrong and the entry has to go.
	"alarm_discovery.go":    "daemon-level plane keyed on zone; no device address to scope",
	"security_discovery.go": "daemon-level plane keyed on hazard class; a deliberate cross-CCU aggregate",
}

// TestHADiscoveryIdentifiersComeFromOneBuilder pins that a Home Assistant
// identifier is composed in one place.
//
// The failure this prevents is invisible on a single-CCU installation and
// silent on a multi-CCU one. `physicalDeviceIdentifier` prefixes the central
// for address classes that repeat between CCUs; a hand-written
// `"openccu-loom_" + strings.ToLower(addr)` does not. Two CCUs then publish
// byte-identical discovery configs for two different devices, and Home
// Assistant keeps whichever arrived first and drops the other — permanently,
// because the payload is retained on the broker. Nothing on the daemon side
// notices: both centrals publish happily to their own distinct state topics,
// and only the entity registry on the far side is one row short.
//
// The same slip in a `via_device` needs no second CCU at all: the sub-device
// points at a parent identifier no device declares, so it floats unparented in
// the Home Assistant device list.
//
// Four instances existed when this guard was written — the schedule switch, the
// schedule entity, the schedule sub-device's via_device and the combined data
// point — and a hand search of the same package had previously reported the
// class as not mechanically detectable. It is: the rule is not "find keys
// without a central", it is "only these functions write this prefix".
func TestHADiscoveryIdentifiersComeFromOneBuilder(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(repoRootForHelpers(t), "internal", "north", "mqtt")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	var offenders []string
	scanned := 0

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if _, exempt := discoveryIdentifierExemptions[name]; exempt {
			continue
		}
		full := filepath.Join(dir, name)
		file, perr := parser.ParseFile(fset, full, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", full, perr)
		}
		scanned++

		// Walk top-level functions so an offending literal can be reported
		// with the function that writes it, which is what a reader needs.
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil {
				continue
			}
			if _, allowed := discoveryIdentifierBuilders[fn.Name.Name]; allowed {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if !strings.Contains(lit.Value, discoveryIdentifierPrefix) {
					return true
				}
				offenders = append(offenders, filepath.Base(full)+":"+
					strconv.Itoa(fset.Position(lit.Pos()).Line)+" in "+fn.Name.Name+"  "+lit.Value)
				return true
			})
		}
	}

	if scanned == 0 {
		t.Fatal("scanned no files — the guard is measuring nothing")
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("%d site(s) compose a Home Assistant identifier outside the builders that "+
			"know about central scoping:\n  %s\n\n"+
			"Use physicalDeviceIdentifier(centralName, address) — it adds the central for the "+
			"address classes that repeat across CCUs, which a hand-written prefix does not, and "+
			"the collision it prevents is invisible until two CCUs are configured.\n"+
			"If the literal is not composing an identifier, add the FILE to "+
			"discoveryIdentifierExemptions with the reason.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}
