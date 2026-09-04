// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// hmCliVirtualRemoteAddressRoots are the CCU's virtual-remote device address
// roots. They are listed here ONLY so this package can assert it does not
// carry a second copy of them: the datum's single home is
// internal/routingkey (virtualRemoteRoots), which feeds published unique ids
// and retained MQTT topics, and pkg/hmenum/device_model.go names that home
// explicitly. The MODEL axis (HM-RCV-50 / HMW-RCV-50 / HmIP-RCV-50) is a
// different datum and lives in pkg/hmenum; the two sets must not be merged.
//
// Firmware source for the addresses, so a reader does not have to trust the
// list: OpenCCU-Base src/rfd/RFCentral.cpp:36-37 (`serial="BidCoS-RF"`),
// src/hs485d/HS485Central.cpp:36-37 (`serial="BidCoS-Wir"`), and on the HmIP
// side HMIPServer de.eq3.cbcs.legacy.bidcos.rpc.internal.VirtualRemoteControl,
// whose address is DEVICE_ID_BASE "HmIP-RCV-" plus an instance counter that
// starts at 1 — hence "HmIP-RCV-1" for the single handler a CCU builds.
var hmCliVirtualRemoteAddressRoots = []string{"BidCoS-RF", "BidCoS-Wir", "HmIP-RCV-"}

// TestHmCliClientHoldsNoVirtualRemoteAddressCopy pins the single-home rule for
// the virtual-remote ADDRESS datum: internal/client must not restate it.
//
// The removed InterfaceClient.VirtualRemote answered the same question with a
// per-interface literal set that disagreed with internal/routingkey on two of
// three entries — it returned the INTERFACE tag "HmIP-RF" as the HmIP virtual
// remote's device ADDRESS (the firmware address is "HmIP-RCV-1"; an exact-match
// lookup against "HmIP-RF" resolves no device), and it documented wired
// interfaces as having no virtual remote although hs485d constructs one
// unconditionally. It had no production caller, so the wrong address sat behind
// a green test; this guard is what keeps a second home from reappearing.
func TestHmCliClientHoldsNoVirtualRemoteAddressCopy(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Parse rather than grep: the rule bans a second *copy* of the
		// datum, not a comment that cites the firmware's value.
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, root := range hmCliVirtualRemoteAddressRoots {
				if strings.HasPrefix(s, root) {
					t.Errorf("%s:%d carries the virtual-remote address literal %q; that datum's single home is internal/routingkey (virtualRemoteRoots)",
						name, fset.Position(lit.Pos()).Line, s)
				}
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("scanned no production files — the guard would pass vacuously")
	}
}
