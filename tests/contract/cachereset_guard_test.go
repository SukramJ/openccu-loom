// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestCacheresetGuardNoOperatorStateTouched enforces ADR 0042: the cache-reset
// service must never reference operator or system state tables. Only
// CCU-derivable stores (devices, paramsets, values, master) may appear in the
// package source.
func TestCacheresetGuardNoOperatorStateTouched(t *testing.T) {
	t.Parallel()

	// Sacrosanct tokens that must never appear in the cache-reset package's
	// code: operator/system-state table names (lowercase snake_case) and the
	// store TYPE identifiers that front them (CamelCase). Both are matched
	// case-sensitively with word boundaries, so the lone word "audit" (the
	// service has an Audit callback) and "central" stay legal — only the exact
	// table token "audit_log" / the exact "AuditStore" type are banned.
	all := []string{
		// table names
		"centrals",
		"config_sections",
		"users",
		"tokens",
		"auth_sessions",
		"audit_log",
		"incidents",
		"session_recorder",
		"matter_fabrics",
		"matter_node_identities",
		"matter_group_keys",
		"matter_group_key_map",
		"matter_acl_entries",
		"matter_endpoints",
		"matter_exposures",
		"matter_persistent_subscriptions",
		"matter_diagnostics",
		"matter_metadata",
		"visibility_unignore",
		// store type names
		"VisibilityUnIgnoreStore",
		"UserStore",
		"TokenStore",
		"AuthSessionStore",
		"AuditStore",
		"IncidentStore",
		"SessionRecorderStore",
		"CentralsStore",
		"ConfigSectionStore",
	}

	regexps := make([]*regexp.Regexp, len(all))
	for i, tok := range all {
		regexps[i] = regexp.MustCompile(`\b` + regexp.QuoteMeta(tok) + `\b`)
	}

	// Resolve the cachereset package directory relative to the test's working
	// directory (tests/contract/ during `go test`).
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	target := filepath.Join(wd, "..", "..", "internal", "central", "cachereset")

	found := false
	walkErr := filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Errorf("read %s: %v", path, readErr)
			found = true
			return nil
		}
		for _, raw := range strings.Split(string(data), "\n") {
			// Scan code only — strip any trailing line comment so prose may
			// use English words that happen to match a table-name token
			// ("centrals", etc.) without tripping the guard, while a token
			// in real code on the same line is still caught.
			code := raw
			if idx := strings.Index(code, "//"); idx >= 0 {
				code = code[:idx]
			}
			if strings.TrimSpace(code) == "" {
				continue
			}
			for i, re := range regexps {
				if re.MatchString(code) {
					t.Errorf("forbidden token %q found in %s: %s", all[i], filepath.Base(path), strings.TrimSpace(raw))
					found = true
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", target, walkErr)
	}
	if found {
		t.FailNow()
	}
}
