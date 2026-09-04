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

// channelAddressHandRolledBuilders records every file outside pkg/hmtypes
// that still writes `<device> + ":" + strconv.Itoa(<channel>)` instead of
// calling [hmtypes.ChannelAddress].
//
// The rows are recorded rather than folded because the canonical builder is
// not a drop-in for all of them: for a negative ordinal it returns the bare
// device address (pkg/hmtypes/address.go), which at a rename or a paramset
// write aims the call at the device instead of failing the channel lookup.
// Deciding per site whether that fallback is the wanted behaviour is a change
// of its own; what this guard buys is that a fourteenth site cannot appear
// without someone making that decision.
var channelAddressHandRolledBuilders = map[string]string{
	"internal/central/adapter/device_admin.go":      "RenameChannel :157 and the paramset path :528; the ordinal arrives from a REST or WS boundary",
	"internal/central/adapter/device_team.go":       "team candidate :24 and team assignment :45",
	"internal/central/central.go":                   ":1085 builds a channel NAME, \"<device name>:<n>\" — a different grammar, not an address",
	"internal/north/mqtt/discovery.go":              ":496 discovery channel address for one data point",
	"internal/north/mqtt/discovery_aggregate.go":    ":499 and the unique-id segment :512",
	"internal/north/mqtt/discovery_press_button.go": ":66 unique-id segment for a press-button channel",
	"internal/north/mqtt/discovery_week_profile.go": ":82 week-profile channel address",
	"internal/north/rest/handlers/device_admin.go":  ":183 and :189 stamp the audit record; the ordinal is range-checked at :161",
	"internal/north/rest/handlers/device_team.go":   ":111 team candidate response field",
	"internal/north/rest/handlers/schedules.go":     ":315 schedule copy source address",
	"internal/north/rest/handlers/values_batch.go":  ":91 batch query channel address",
	"internal/north/rest/ws/device_trigger.go":      ":98 trigger fan-out channel address",
	"internal/client/reliability/coalesce.go":       ":129 joins call arguments into a coalescing key — not an address at all",
}

// TestChannelAddressBuildersAreRecorded fails when a file grows a new
// hand-rolled "<device>:<ordinal>" concatenation.
//
// The sibling guard TestChannelOrdinalParsersDelegateToHmtypes looks only at
// the parsing direction — a ':' comparison plus a digit accumulation — so it
// is blind to construction. Construction is where the reachable defect was:
// a REST path segment of "-1" reached the CCU as "DEV001:-1" because the
// handler parsed the ordinal without a range check and the concatenation
// happily rendered it.
func TestChannelAddressBuildersAreRecorded(t *testing.T) {
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
			if !channelAddressBuildsOrdinalSuffix(path) {
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
		if _, recorded := channelAddressHandRolledBuilders[f]; !recorded {
			t.Errorf("%s builds a channel address by concatenation; call pkg/hmtypes.ChannelAddress or record why not", f)
		}
	}
	for f := range channelAddressHandRolledBuilders {
		if !slices.Contains(found, f) {
			t.Errorf("%s no longer concatenates a channel address — drop it from channelAddressHandRolledBuilders", f)
		}
	}
}

// channelAddressBuildsOrdinalSuffix reports whether the file contains an
// addition whose right operand converts a number to decimal and whose left
// operand ends in the string ":". That is the whole shape of a hand-rolled
// channel address; either half alone (a lone ":" separator, a lone Itoa) is
// ordinary code, so the conjunction carries the signal.
//
// The right-operand test accepts any call ending in Itoa/FormatInt/Sprint,
// not just strconv.Itoa, so a site written in an unexpected but equivalent
// shape is reported rather than silently skipped.
func channelAddressBuildsOrdinalSuffix(path string) bool {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return false
	}
	var hit bool
	ast.Inspect(file, func(n ast.Node) bool {
		be, ok := n.(*ast.BinaryExpr)
		if !ok || be.Op != token.ADD {
			return true
		}
		if channelAddressIsNumberToString(be.Y) && channelAddressEndsWithColon(be.X) {
			hit = true
		}
		return true
	})
	return hit
}

// channelAddressEndsWithColon reports whether the expression is the string
// ":" or an addition whose last operand is.
func channelAddressEndsWithColon(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.BasicLit:
		return x.Kind == token.STRING && x.Value == `":"`
	case *ast.BinaryExpr:
		return x.Op == token.ADD && channelAddressEndsWithColon(x.Y)
	case *ast.ParenExpr:
		return channelAddressEndsWithColon(x.X)
	default:
		return false
	}
}

// channelAddressIsNumberToString reports whether the expression is a call to
// a decimal number-to-string conversion.
func channelAddressIsNumberToString(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch sel.Sel.Name {
	case "Itoa", "FormatInt", "FormatUint", "Sprint":
		return true
	default:
		return false
	}
}
