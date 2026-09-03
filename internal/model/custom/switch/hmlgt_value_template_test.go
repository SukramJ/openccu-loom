// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package switchdev

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// hmLgtCanonicalLowerTemplate extracts the daemon's canonical boolean
// value_template — valueJSONValueLowerTemplate in
// internal/north/mqtt/discovery.go — from its source.
//
// The constant is unexported and internal/model may not import
// internal/north, so the model package cannot reference it; reading it
// out of the source is what keeps the two spellings one rule instead of
// two literals that drift apart silently. A textual read is sound here
// because the value IS the text: the template is shipped verbatim in the
// discovery payload.
func hmLgtCanonicalLowerTemplate(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "..", "..", "north", "mqtt", "discovery.go")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	re := regexp.MustCompile("(?m)^const valueJSONValueLowerTemplate = `([^`]*)`")
	m := re.FindSubmatch(b)
	if m == nil {
		t.Fatalf("valueJSONValueLowerTemplate not found in %s — the canonical template moved; re-point this guard", src)
	}
	return string(m[1])
}

// The Switch entity's boolean value_template must be the same rule the
// per-parameter discovery plane publishes. Both render the same
// PerDPState envelope on the same STATE topic, so a clause present on
// one side and missing on the other makes one entity read "unknown" and
// the other the literal string "none" from an identical retained
// payload.
func TestHmLgtSwitchValueTemplateMatchesTheCanonicalOne(t *testing.T) {
	t.Parallel()

	s := &Switch{}
	_, body := s.HADiscoveryPayload(&stubDiscoveryCtx{})
	got, ok := body["value_template"].(string)
	if !ok {
		t.Fatalf("switch discovery body carries no string value_template: %#v", body["value_template"])
	}
	want := hmLgtCanonicalLowerTemplate(t)
	if got != want {
		t.Fatalf("switch value_template diverged from the canonical boolean template\n got: %s\nwant: %s", got, want)
	}
}
