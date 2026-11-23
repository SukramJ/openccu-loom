// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package config

import (
	"strings"
	"testing"
)

// TestMatterNodeLabelWithinBasicInformationConstraint pins the bound the
// node facade enforces at build: BasicInformation.NodeLabel is at most 32
// bytes, counted in bytes, not characters, so a label the save accepted
// cannot fail the bridge's next start.
func TestMatterNodeLabelWithinBasicInformationConstraint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		label string
		ok    bool
	}{
		{"empty selects the default", "", true},
		{"32 bytes", strings.Repeat("a", 32), true},
		{"33 bytes", strings.Repeat("a", 33), false},
		{"16 two-byte characters are 32 bytes", strings.Repeat("ä", 16), true},
		{"17 two-byte characters are 34 bytes", strings.Repeat("ä", 17), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := NorthMatter{NodeLabel: tc.label}
			err := validateMatter(&m)
			if tc.ok && err != nil {
				t.Fatalf("validateMatter(%q): %v", tc.label, err)
			}
			if !tc.ok && (err == nil || !strings.Contains(err.Error(), "north.matter.node_label")) {
				t.Fatalf("validateMatter(%d bytes) = %v, want a north.matter.node_label error", len(tc.label), err)
			}
		})
	}
}

// TestRetiredMatterSaltLoadsAndIsNoted pins how the retired
// north.matter.commissioning.salt is handled: a config file and a stored
// section that still set it load as before, and the value records that it
// did, so the bridge start can name the key.
func TestRetiredMatterSaltLoadsAndIsNoted(t *testing.T) {
	t.Parallel()

	t.Run("yaml", func(t *testing.T) {
		t.Parallel()
		cfg, err := Parse([]byte("north:\n  matter:\n    commissioning:\n      passcode: 20202021\n      salt: openccu-loom-dev0\n" + minimalCentralYAML))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if !cfg.North.Matter.Commissioning.SetsRetiredSalt() {
			t.Error("SetsRetiredSalt = false for a file that sets salt")
		}
		if cfg.North.Matter.Commissioning.Passcode != 20202021 {
			t.Errorf("Passcode = %d, want the block's other keys decoded", cfg.North.Matter.Commissioning.Passcode)
		}
	})

	t.Run("yaml without salt", func(t *testing.T) {
		t.Parallel()
		cfg, err := Parse([]byte("north:\n  matter:\n    commissioning:\n      passcode: 20202021\n" + minimalCentralYAML))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if cfg.North.Matter.Commissioning.SetsRetiredSalt() {
			t.Error("SetsRetiredSalt = true for a file without salt")
		}
	})

	t.Run("stored section", func(t *testing.T) {
		t.Parallel()
		var c NorthMatterCommissioning
		if err := strictDecode(t, `{"passcode":20202021,"salt":"openccu-loom-dev0"}`, &c); err != nil {
			t.Fatalf("a stored section with the retired salt must still decode: %v", err)
		}
		if !c.SetsRetiredSalt() {
			t.Error("SetsRetiredSalt = false for a section that sets salt")
		}
	})

	t.Run("restart comparison ignores the mark", func(t *testing.T) {
		t.Parallel()
		b, e := Default(), Default()
		b.North.Matter.Commissioning.retiredSalt = true
		for _, r := range RestartRequiredDiff(b, e) {
			if strings.HasPrefix(r, "north.matter.commissioning") {
				t.Fatalf("RestartRequired names %q for a retired key alone", r)
			}
		}
	})
}
