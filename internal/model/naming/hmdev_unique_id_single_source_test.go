// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package naming

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHmDevNamingDeclaresNoHADiscoveryUniqueIDBuilder pins that this package
// carries no builder for HA's `unique_id` registry key.
//
// The model layer owns the MQTT topic shapes on purpose (see the note above
// [PathData.MQTTState]), and it owns the discovery node_id / object_id /
// config-topic formats for the same reason. The `unique_id` is the one
// discovery identifier it must NOT own: the value the daemon publishes comes
// from routingkey.CanonicalUniqueID, which namespaces it `loom_` and lays the
// fields out differently. A second builder here answered
// `openccu-loom_<central>_<address>_<channel>_<suffix>` for the same input,
// had no production caller at all, and carried a doc comment claiming the HA
// registry format was "pinned via this method" — a pin on a format HA never
// sees, kept alive by its own unit tests.
//
// The guards that police the live identifiers (tests/contract) scan
// internal/north/mqtt only, so nothing outside this test would notice the
// copy coming back.
func TestHmDevNamingDeclaresNoHADiscoveryUniqueIDBuilder(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if !strings.HasPrefix(line, "func ") {
				continue
			}
			if strings.Contains(line, "UniqueID") {
				t.Errorf("%s:%d declares a unique_id builder — %q\n"+
					"The HA unique_id the daemon publishes comes from "+
					"routingkey.CanonicalUniqueID; a second builder in the model "+
					"disagrees with it on the namespace and on the field order.",
					name, i+1, strings.TrimSpace(line))
			}
		}
	}
}
