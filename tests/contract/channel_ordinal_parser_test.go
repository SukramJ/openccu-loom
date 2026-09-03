// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// materializeChannelNoHandRolledParsers records every file outside
// pkg/hmtypes that still turns a ':'-separated suffix into a number with its
// own byte loop instead of calling [hmtypes.ChannelNo].
//
// The one remaining CCU-address entry scans backwards where hmtypes takes
// the first separator, and answers 0 for a non-numeric suffix. Which
// sentinel and which direction is correct is a question about what that
// call site actually receives, and answering it is a change of its own —
// so it is recorded, not blessed.
//
// Two files have left this map by delegating rather than by being
// reworded: schedule_query_adapter.go's splitChannelAddress and
// pkg/hmproto's DeviceDescription.ChannelNo both call hmtypes now and keep
// only their own no-separator fallback.
var materializeChannelNoHandRolledParsers = map[string]string{
	"internal/central/adapter/paramsets.go":  "channelNumberOf: backwards scan, sentinel 0 for a non-numeric suffix",
	"internal/north/matter/bridge/bridge.go": "udpPort parses a network listen string, not a CCU address — a different grammar",
}

// TestChannelOrdinalParsersDelegateToHmtypes fails when a file grows its own
// "<something>:<number>" scanner.
//
// The name-based guard next door (TestAddressSplittingHasOneSource) only
// inspects functions whose name carries "deviceaddress", so a pair of helpers
// called indexOfColon and atoiSmall sat on the custom-DP visibility path
// unseen: a profile lookup that failed there force-marked every generic data
// point of the device NoCreate, i.e. removed it from MQTT, REST and the SPA.
// This guard looks at the shape instead — a comparison against ':' plus a
// digit accumulation in the same file — which is what a hand-rolled ordinal
// parser looks like however its functions are named.
func TestChannelOrdinalParsersDelegateToHmtypes(t *testing.T) {
	t.Parallel()
	const root = "../.."
	var found []string

	for _, sub := range []string{"internal", "pkg", "cmd"} {
		err := filepath.Walk(filepath.Join(root, sub), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err //nolint:wrapcheck // walk error is returned as-is
			}
			if strings.Contains(filepath.ToSlash(path), "pkg/hmtypes/") {
				return nil
			}
			if !materializeChannelNoScansOrdinal(path) {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			found = append(found, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sub, err)
		}
	}

	sort.Strings(found)
	for _, f := range found {
		if _, recorded := materializeChannelNoHandRolledParsers[f]; !recorded {
			t.Errorf("%s parses a ':'-separated channel ordinal itself; call pkg/hmtypes.ChannelNo or record why not", f)
		}
	}
	for f := range materializeChannelNoHandRolledParsers {
		if !slices.Contains(found, f) {
			t.Errorf("%s no longer parses an ordinal — drop it from materializeChannelNoHandRolledParsers", f)
		}
	}
}

// materializeChannelNoScansOrdinal reports whether the file both compares a
// byte against ':' and accumulates decimal digits, the two halves of a
// hand-rolled "<prefix>:<n>" parser. Either half alone is ordinary code —
// building a key with a ':' separator, or converting a single digit — so the
// conjunction is what carries the signal.
func materializeChannelNoScansOrdinal(path string) bool {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return false
	}
	var separator, digits bool
	ast.Inspect(file, func(n ast.Node) bool {
		be, ok := n.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		switch be.Op {
		case token.EQL, token.NEQ:
			if materializeChannelNoIsCharLit(be.X, ':') || materializeChannelNoIsCharLit(be.Y, ':') {
				separator = true
			}
		case token.SUB:
			if materializeChannelNoIsCharLit(be.Y, '0') {
				digits = true
			}
		default:
		}
		return true
	})
	return separator && digits
}

func materializeChannelNoIsCharLit(e ast.Expr, c byte) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.CHAR && lit.Value == "'"+string(c)+"'"
}
