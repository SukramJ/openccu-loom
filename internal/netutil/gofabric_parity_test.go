// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package netutil

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	gofabricmdns "github.com/SukramJ/go-fabric/mdns"
)

// TestVirtualInterfaceNameAgreesWithGoFabric pins [IsVirtualInterfaceName]
// against the equivalent predicate in the go-fabric module and fails when the
// two classify one interface name differently.
//
// # Why the check exists
//
// The rule lives twice: here, for the client-discovery mDNS advertiser, and in
// go-fabric's `mdns` package, for the Matter bridge's advertiser. Both publish
// A/AAAA records for the *same host*, so a divergence makes one advertiser
// announce an address the other deliberately suppresses — a commissioner or a
// client then resolves the daemon to a container-bridge address it cannot route
// to. The duplication is across a module boundary and cannot be removed from
// this side; what can be removed is its silence.
//
// # What the check measures, and against what
//
// The oracle is `mdns.NewZeroconf().InterfaceFilter`, which go-fabric documents
// as installing its own `isVirtualInterfaceName` and which is reachable through
// the exported field. That is the *compiled* predicate of the module version
// this repository builds against — the one whose decisions actually reach the
// wire in a release built from this commit. It is therefore a stronger oracle
// than the go-fabric source tree a developer happens to have checked out, which
// may sit ahead of or behind the pinned version and answer for code this
// binary never runs. It also needs no checkout at all, so the check runs in CI
// on every module bump, which is exactly when drift arrives.
//
// # Where the names come from
//
// Behavioural equivalence can only be asserted over names that are actually
// tried, so the corpus is built rather than listed:
//
//   - every prefix in this package's own [virtualIfacePrefixes], in several
//     shapes (bare, with a numeric suffix, upper-cased, and truncated by one
//     character) — this catches a prefix go-fabric drops or narrows;
//   - real LAN and VPN interface names that must stay unfiltered — this catches
//     a prefix go-fabric *widens* into a routable link;
//   - optionally, every prefix parsed out of a local go-fabric checkout — this
//     catches a prefix go-fabric *adds*, which no corpus derived from this
//     side alone could contain.
//
// The checkout contributes candidate names only; it is never the expected
// answer. So a checkout that is ahead of the pinned module cannot produce a
// false failure: the extra name is simply classified by both predicates, and
// the pinned module and this package agree on it (both false) until the bump
// lands. When the checkout is absent, that third source is skipped with a log
// line and the first two still run — a developer without the sibling clone must
// be able to run the suite, and the check keeps measuring what it can.
func TestVirtualInterfaceNameAgreesWithGoFabric(t *testing.T) {
	t.Parallel()

	theirs := gofabricmdns.NewZeroconf().InterfaceFilter
	if theirs == nil {
		t.Fatal("mdns.NewZeroconf().InterfaceFilter is nil: go-fabric no longer installs its default " +
			"interface filter, so this guard has no oracle and the two copies of the rule are unpinned again")
	}

	prefixes := append([]string(nil), virtualIfacePrefixes...)
	if extra, why := goFabricPrefixesFromCheckout(); why != "" {
		t.Logf("go-fabric checkout not read, corpus covers this package's prefixes only: %s", why)
	} else {
		prefixes = append(prefixes, extra...)
		t.Logf("corpus extended with %d prefix(es) read from the local go-fabric checkout", len(extra))
	}

	names := interfaceNameCorpus(prefixes)
	for _, name := range names {
		ours := IsVirtualInterfaceName(name)
		if got := theirs(name); got != ours {
			t.Errorf("interface %q: netutil.IsVirtualInterfaceName = %t, go-fabric mdns filter = %t — "+
				"the host and the Matter bridge would advertise different address sets for one machine",
				name, ours, got)
		}
	}
	t.Logf("compared %d interface name(s) against the go-fabric filter", len(names))
}

// interfaceNameCorpus expands prefixes into the concrete interface names the
// comparison runs over, plus the real LAN / VPN names that must stay unfiltered.
// The shapes exercise the parts of the rule that can drift independently: the
// prefix set itself, the case-insensitivity, and the prefix (rather than exact)
// matching — a name one character short of a prefix must be classified the same
// way by both sides too.
func interfaceNameCorpus(prefixes []string) []string {
	// Real links and VPN overlays: neither side may filter these. `br0` and
	// `bridge0` (macOS) are the near-misses of the `br-` prefix; `lo` is the
	// loopback the callers drop by flag rather than by name.
	names := []string{
		"eth0", "en0", "end0", "eno1", "enp3s0", "wlan0", "wlp2s0",
		"br0", "bridge0", "wg0", "tun0", "utun3", "tailscale0", "awdl0", "lo", "",
	}
	seen := make(map[string]struct{}, len(names)+4*len(prefixes))
	for _, n := range names {
		seen[n] = struct{}{}
	}
	for _, p := range prefixes {
		candidates := []string{p, p + "0", p + "123", strings.ToUpper(p) + "0"}
		if len(p) > 1 {
			candidates = append(candidates, p[:len(p)-1])
		}
		for _, c := range candidates {
			if _, dup := seen[c]; dup {
				continue
			}
			seen[c] = struct{}{}
			names = append(names, c)
		}
	}
	return names
}

// goFabricPrefixesFromCheckout reads the `virtualIfacePrefixes` literal out of a
// local go-fabric working tree, to widen the corpus with names only that side
// classifies as virtual. It returns a reason instead of prefixes whenever the
// tree is missing or does not carry the declaration in the expected shape;
// every such case is a "cannot measure this part", never a finding, because the
// checkout is not a build input of this repository.
//
// The location is GO_FABRIC_DIR or the sibling directory beside this
// repository, and it is confirmed by go.mod's module line rather than by the
// directory name, so a wrong sibling contributes nothing instead of nonsense.
func goFabricPrefixesFromCheckout() (prefixes []string, why string) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return nil, "cannot resolve the repository root: " + err.Error()
	}
	dir := os.Getenv("GO_FABRIC_DIR")
	source := "GO_FABRIC_DIR"
	if dir == "" {
		dir = filepath.Join(filepath.Dir(repoRoot), "go-fabric")
		source = "sibling checkout"
	}
	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, source + " " + strconv.Quote(dir) + " has no readable go.mod: " + err.Error()
	}
	if !strings.Contains(string(gomod), "module github.com/SukramJ/go-fabric") {
		return nil, source + " " + strconv.Quote(dir) + " is not the go-fabric module"
	}

	file := filepath.Join(dir, "mdns", "interface_filter.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		return nil, "cannot parse " + file + ": " + err.Error()
	}
	for _, decl := range parsed.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "virtualIfacePrefixes" || len(value.Values) != 1 {
				continue
			}
			composite, ok := value.Values[0].(*ast.CompositeLit)
			if !ok {
				return nil, file + ": virtualIfacePrefixes is not a composite literal"
			}
			for _, elt := range composite.Elts {
				lit, ok := elt.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return nil, file + ": virtualIfacePrefixes holds a non-literal element"
				}
				unquoted, unquoteErr := strconv.Unquote(lit.Value)
				if unquoteErr != nil {
					return nil, file + ": " + unquoteErr.Error()
				}
				prefixes = append(prefixes, unquoted)
			}
			return prefixes, ""
		}
	}
	return nil, file + ": no virtualIfacePrefixes declaration found"
}
