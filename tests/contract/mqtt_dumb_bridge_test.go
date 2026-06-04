// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

// TestMQTTBridgeIsADumbRouter pins ADR 0011 §"Bridge cleanup": the MQTT bridge
// package must not contain references to Custom-DP Go package paths or
// Custom-DP type identifier strings outside of test fixtures. The bridge
// routes by DataPointCategory and HAComponent constants only.
//
// Specifically this test prevents:
//   - Import paths like `internal/model/custom/climate`, `…/lock`, etc.
//   - String literals naming a custom-DP Go package (e.g. `"climate"` inside
//     a routing switch/map that selects behavior by DP type name).
//
// What is explicitly ALLOWED and therefore excluded from the scan:
//   - HAComponent constants in discovery.go: `HAComponentClimate HAComponent = "climate"`.
//     These are HA-platform names, not Custom-DP type references.
//   - Category values in entity_descriptions_generated.go such as
//     `Category: "cover"` which are DataPointCategory enum values, not
//     routing decisions.
//   - Any line whose content is the const/var declaration of an HAComponent
//     constant (identified by "HAComponent" on the same line).
//   - Any occurrence in a `_test.go` file (test fixtures are allowed).
//   - Comments — architecture references in comments are acceptable.
//
// This test walks every non-test `.go` file under `internal/north/mqtt/`,
// reads each line, and rejects any line that (a) contains a forbidden
// import path or (b) contains a lowercase string literal of a Custom-DP
// type name in a routing context without an HAComponent declaration on the
// same line.

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// customDPGoPackages are the Go sub-package names under
// `internal/model/custom/` that the bridge must not import.
var customDPGoPackages = []string{
	"internal/model/custom/climate",
	"internal/model/custom/lock",
	"internal/model/custom/cover",
	"internal/model/custom/light",
	"internal/model/custom/siren",
	"internal/model/custom/valve",
	"internal/model/custom/textdisplay",
}

// customDPTypeLiteralRouting are string literals that would indicate
// routing-by-type (e.g. in a switch on DP type name). We do NOT flag
// "lock" and "light" here because they appear in HA-platform names
// ("lock" → payload_lock; "light" → schema) that are legitimately
// referenced from discovery payloads. The import-path check above
// catches any forbidden Go-level coupling for those.
//
// The list focuses on names that ONLY exist as custom-DP type
// identifiers and have no legitimate HA-platform use in bridge code.
var customDPTypeLiteralRouting = []string{
	"textdisplay",
}

func TestMQTTBridgeIsADumbRouter(t *testing.T) {
	t.Parallel()

	mqttDir := filepath.Join(repoRoot(t), "internal", "north", "mqtt")
	entries, err := os.ReadDir(mqttDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", mqttDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		// Skip test files — they may reference custom DP types in fixtures.
		if strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		fpath := filepath.Join(mqttDir, entry.Name())
		checkBridgeFile(t, fpath)
	}
}

// checkBridgeFile scans a single source file for forbidden patterns.
func checkBridgeFile(t *testing.T, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Errorf("open %s: %v", path, err)
		return
	}
	defer f.Close()

	base := filepath.Base(path)
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()

		// Trim leading whitespace for analysis; keep the raw line for messages.
		line := strings.TrimSpace(raw)

		// Skip blank lines and pure comments.
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		// Remove inline comments so we don't flag comment text.
		code := removeInlineComment(line)

		// ── Import-path check ──────────────────────────────────────────────
		// Reject any line that imports a custom-DP Go sub-package.
		for _, pkg := range customDPGoPackages {
			if strings.Contains(code, pkg) {
				t.Errorf("%s:%d imports or references %q — bridge must be a dumb router (ADR 0011)\n  line: %s",
					base, lineNo, pkg, raw)
			}
		}

		// ── Routing-context type-literal check ─────────────────────────────
		// Reject lines that contain a bare textdisplay literal (the only
		// type name that has no legitimate HA-platform alias in bridge code).
		for _, typName := range customDPTypeLiteralRouting {
			if containsTypeNameLiteral(code, typName) {
				t.Errorf(`%s:%d mentions %q as a bare type name — bridge must be a dumb router (ADR 0011)
  If this is an HAComponent constant declaration, the line must contain "HAComponent" to be exempted.
  line: %s`, base, lineNo, typName, raw)
			}
		}

		// ── model/custom import block check ───────────────────────────────
		// A blanket check: any occurrence of `model/custom` in a non-comment,
		// non-test file is a violation.
		if strings.Contains(code, `model/custom`) {
			t.Errorf("%s:%d references model/custom — bridge must be a dumb router (ADR 0011)\n  line: %s",
				base, lineNo, raw)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Errorf("scan %s: %v", path, err)
	}
}

// containsTypeNameLiteral returns true when code contains the given typName
// as a quoted string literal (`"typName"`) and the line does NOT also contain
// "HAComponent" (which would indicate this is an HAComponent constant
// declaration — a legitimate use).
func containsTypeNameLiteral(code, typName string) bool {
	quoted := `"` + typName + `"`
	if !strings.Contains(code, quoted) {
		return false
	}
	// Exempt HAComponent constant declarations and DataPointCategory
	// assignments in entity_descriptions files.
	if strings.Contains(code, "HAComponent") {
		return false
	}
	if strings.Contains(code, "DataPointCategory") {
		return false
	}
	if strings.Contains(code, "Category:") {
		return false
	}
	return true
}

// removeInlineComment strips the trailing `// ...` comment from a line.
func removeInlineComment(line string) string {
	// Simple heuristic: find the first `//` that is not inside a string.
	inString := false
	escaped := false
	for i := range len(line) - 1 {
		ch := line[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
		}
		if !inString && ch == '/' && line[i+1] == '/' {
			return line[:i]
		}
	}
	return line
}

// Note: repoRoot(t) is provided by multi_ccu_scope_test.go in this package.
