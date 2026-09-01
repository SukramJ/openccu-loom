// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ccuDeclaredArguments is the CCU's own ARGUMENTS list for each JSON-RPC
// method this daemon calls with named parameters, transcribed from
// www/api/methods.conf in the firmware tree. `_session_id_` is omitted: the
// transport adds it.
//
// The CCU's dispatcher builds `args` with `array set args $JSONRPC(PARAMS)`
// and then runs checkArguments, which tests `[info exists args($argName)]`
// for every declared argument. A name we get wrong therefore leaves a
// declared argument unset and the call is rejected before it reaches the
// interface process — silently, as an upstream error.
//
// The list is deliberately NOT uniform, and that is the whole point:
//
//   - Interface.setLinkInfo takes sender / receiver, while
//     Interface.getLinkInfo takes senderAddress / receiverAddress.
//   - SysVar.createEnum takes valList, not valueList.
//   - The SysVar creators take chnID, not chn_id.
//
// Each of those three shapes shipped wrong at least once. The reference
// implementation carries the same error for setLinkInfo, so cross-stack
// parity does not catch this class; only the firmware does.
var ccuDeclaredArguments = map[string][]string{
	"Interface.setLinkInfo": {"interface", "sender", "receiver", "name", "description"},
	"Interface.getLinkInfo": {"interface", "senderAddress", "receiverAddress"},
	"SysVar.createBool":     {"name", "init_val", "internal", "chnID"},
	"SysVar.createEnum":     {"name", "valList", "internal", "chnID"},
	"SysVar.createFloat":    {"name", "minValue", "maxValue", "internal", "chnID"},
}

// forbiddenLookalikes are the wrong spellings that actually shipped. Pinning
// the right name alone is not enough: the defect was always a plausible
// neighbour, and a future edit reaching for the same neighbour has to fail
// rather than merely fail to match.
var forbiddenLookalikes = map[string][]string{
	"Interface.setLinkInfo": {"senderAddress", "receiverAddress"},
	"SysVar.createBool":     {"chn_id"},
	"SysVar.createEnum":     {"valueList", "value_list", "chn_id"},
	"SysVar.createFloat":    {"min_value", "max_value", "chn_id"},
}

// TestJSONRPCCallsUseTheDeclaredArgumentNames reads the production sources
// and checks the parameter keys near every named-parameter JSON-RPC call
// against the CCU's own registry.
//
// It is a source scan rather than a round-trip because these calls have no
// single seam: they are spread over the transport, the CCU backend and the
// hub writer, and the defect is a string literal in each.
func TestJSONRPCCallsUseTheDeclaredArgumentNames(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	keyRe := regexp.MustCompile(`"([a-zA-Z_][a-zA-Z0-9_]*)"\s*[:\]]`)

	for method, declared := range ccuDeclaredArguments {
		methodRe := regexp.MustCompile(`"` + regexp.QuoteMeta(method) + `"`)
		found := false
		for _, file := range goSourcesUnder(t, root, "internal") {
			src, err := os.ReadFile(file) //nolint:gosec // paths come from the walk below
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			lines := strings.Split(string(src), "\n")
			for i, line := range lines {
				if !methodRe.MatchString(line) || strings.Contains(line, "//") {
					continue
				}
				found = true
				// The params map may be built above or below the call, so
				// take a window on both sides.
				lo, hi := max(0, i-14), min(len(lines), i+18)
				window := strings.Join(lines[lo:hi], "\n")
				keys := map[string]bool{}
				for _, m := range keyRe.FindAllStringSubmatch(window, -1) {
					keys[m[1]] = true
				}
				rel, _ := filepath.Rel(root, file)
				for _, want := range declared {
					if !keys[want] {
						t.Errorf("%s at %s:%d does not send the declared argument %q\n"+
							"  www/api/methods.conf declares: %v\n"+
							"  a missing declared argument is rejected by checkArguments",
							method, rel, i+1, want, declared)
					}
				}
				for _, bad := range forbiddenLookalikes[method] {
					if keys[bad] {
						t.Errorf("%s at %s:%d sends %q, which the CCU does not declare for it\n"+
							"  declared: %v", method, rel, i+1, bad, declared)
					}
				}
			}
		}
		if !found {
			t.Errorf("no call site found for %s — remove it from ccuDeclaredArguments "+
				"or restore the call, but do not leave the pin describing nothing", method)
		}
	}
}

func goSourcesUnder(t *testing.T, root, sub string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(root, sub), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", sub, err)
	}
	return out
}
