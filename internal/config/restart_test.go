// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"slices"
	"strings"
	"testing"
)

// baseConfig returns a fully populated Config whose restart-required
// fields are all set to distinct non-zero values so any change is
// detectable.
func baseConfig() *Config {
	c := Default()
	c.DataDir = "/var/lib/loom"
	c.North.REST.Listen = ":8119"
	c.Callback.Host = "192.0.2.1"
	c.Callback.Port = 8120
	c.Callback.BinPort = 8129
	c.Callback.PortRange = "30000-30099"
	c.North.Matter.Enabled = false
	c.North.Matter.Listen = ":5540"
	c.North.MCP.Enabled = false
	c.North.MCP.AllowWrites = false
	c.North.MCP.Path = "/mcp"
	c.Centrals = []CentralConfig{{Name: "ccu1", Host: "192.0.2.10"}}
	return c
}

// clone returns a shallow copy of c. Centrals is deep-copied so
// mutations of the slice do not alias the original.
func clone(c *Config) *Config {
	cp := *c
	cp.Centrals = make([]CentralConfig, len(c.Centrals))
	copy(cp.Centrals, c.Centrals)
	return &cp
}

// TestRestartRequiredDiff_Identical verifies that two identical configs
// produce an empty result.
func TestRestartRequiredDiff_Identical(t *testing.T) {
	t.Parallel()
	a := baseConfig()
	b := clone(a)
	got := RestartRequiredDiff(a, b)
	if len(got) != 0 {
		t.Errorf("expected empty diff, got %v", got)
	}
}

// TestRestartRequiredDiff_NilInputs verifies that nil inputs do not panic
// and return nil.
func TestRestartRequiredDiff_NilInputs(t *testing.T) {
	t.Parallel()
	base := baseConfig()

	if got := RestartRequiredDiff(nil, base); got != nil {
		t.Errorf("(nil, x): expected nil, got %v", got)
	}
	if got := RestartRequiredDiff(base, nil); got != nil {
		t.Errorf("(x, nil): expected nil, got %v", got)
	}
	if got := RestartRequiredDiff(nil, nil); got != nil {
		t.Errorf("(nil, nil): expected nil, got %v", got)
	}
}

// TestRestartRequiredDiff_SingleFieldChange verifies that changing exactly
// one restart-required field produces exactly that path in the output.
func TestRestartRequiredDiff_SingleFieldChange(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path   string
		mutate func(c *Config)
	}{
		{
			path:   "data_dir",
			mutate: func(c *Config) { c.DataDir = "/tmp/other" },
		},
		{
			path:   "north.rest.listen",
			mutate: func(c *Config) { c.North.REST.Listen = ":9090" },
		},
		{
			path:   "north.rest.public_url",
			mutate: func(c *Config) { c.North.REST.PublicURL = "https://loom.example.de" },
		},
		{
			path:   "callback.host",
			mutate: func(c *Config) { c.Callback.Host = "10.0.0.1" },
		},
		{
			path:   "callback.port",
			mutate: func(c *Config) { c.Callback.Port = 9120 },
		},
		{
			path:   "callback.bin_port",
			mutate: func(c *Config) { c.Callback.BinPort = 9129 },
		},
		{
			path:   "callback.port_range",
			mutate: func(c *Config) { c.Callback.PortRange = "40000-40099" },
		},
		{
			path:   "north.matter.enabled",
			mutate: func(c *Config) { c.North.Matter.Enabled = true },
		},
		{
			path:   "north.matter.listen",
			mutate: func(c *Config) { c.North.Matter.Listen = ":5541" },
		},
		{
			path:   "north.mcp.enabled",
			mutate: func(c *Config) { c.North.MCP.Enabled = true },
		},
		{
			path:   "north.mcp.allow_writes",
			mutate: func(c *Config) { c.North.MCP.AllowWrites = true },
		},
		{
			path:   "north.mcp.path",
			mutate: func(c *Config) { c.North.MCP.Path = "/mcp2" },
		},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			boot := baseConfig()
			eff := clone(boot)
			tc.mutate(eff)

			got := RestartRequiredDiff(boot, eff)
			if len(got) != 1 {
				t.Fatalf("path %q: expected exactly 1 diff entry, got %v", tc.path, got)
			}
			if got[0] != tc.path {
				t.Errorf("path %q: expected diff[0]=%q, got %q", tc.path, tc.path, got[0])
			}
		})
	}
}

// TestRestartRequiredDiff_MatterMDNSAdvertise verifies that the comparison
// runs against the WithDefaults() view: an explicit boot-time value that
// differs from the effective value surfaces the path, but a boot-time unset
// value that resolves to the same default as the effective side does not —
// switching the advertiser is restart-required, applying the documented
// default is not a phantom restart.
func TestRestartRequiredDiff_MatterMDNSAdvertise(t *testing.T) {
	t.Parallel()

	t.Run("explicit_noop_vs_defaulted_zeroconf_differs", func(t *testing.T) {
		t.Parallel()
		boot := baseConfig()
		boot.North.Matter.MDNSAdvertise = "noop"
		eff := clone(boot)
		eff.North.Matter.MDNSAdvertise = "zeroconf"

		got := RestartRequiredDiff(boot, eff)
		if !slices.Contains(got, "north.matter.mdns_advertise") {
			t.Errorf("expected \"north.matter.mdns_advertise\" in diff %v", got)
		}
	})

	t.Run("unset_vs_explicit_default_is_not_a_diff", func(t *testing.T) {
		t.Parallel()
		boot := baseConfig()
		boot.North.Matter.MDNSAdvertise = ""
		eff := clone(boot)
		eff.North.Matter.MDNSAdvertise = "zeroconf"

		got := RestartRequiredDiff(boot, eff)
		if slices.Contains(got, "north.matter.mdns_advertise") {
			t.Errorf("unset defaults to the same value as explicit \"zeroconf\": unexpected \"north.matter.mdns_advertise\" in diff %v", got)
		}
	})

	t.Run("both_unset_is_not_a_diff", func(t *testing.T) {
		t.Parallel()
		boot := baseConfig()
		boot.North.Matter.MDNSAdvertise = ""
		eff := clone(boot)
		eff.North.Matter.MDNSAdvertise = ""

		got := RestartRequiredDiff(boot, eff)
		if slices.Contains(got, "north.matter.mdns_advertise") {
			t.Errorf("both unset: unexpected \"north.matter.mdns_advertise\" in diff %v", got)
		}
	})
}

// TestRestartRequiredDiff_CentralsAdded verifies that a pure addition —
// every boot-time central is unchanged, plus a new one appears in eff —
// is a live orchestrator operation and does NOT surface "centrals".
func TestRestartRequiredDiff_CentralsAdded(t *testing.T) {
	t.Parallel()

	boot := baseConfig()
	eff := clone(boot)
	eff.Centrals = append(eff.Centrals, CentralConfig{Name: "ccu2", Host: "192.0.2.20"})

	got := RestartRequiredDiff(boot, eff)
	if slices.Contains(got, "centrals") {
		t.Errorf("pure add: unexpected \"centrals\" in diff %v", got)
	}
}

// TestRestartRequiredDiff_CentralsRemoved verifies that a pure removal —
// eff drops a central that was present at boot, with no field changes to
// the survivors — does NOT surface "centrals".
func TestRestartRequiredDiff_CentralsRemoved(t *testing.T) {
	t.Parallel()

	boot := baseConfig()
	boot.Centrals = []CentralConfig{
		{Name: "a", Host: "1.2.3.4"},
		{Name: "b", Host: "1.2.3.5"},
	}
	eff := clone(boot)
	eff.Centrals = []CentralConfig{{Name: "a", Host: "1.2.3.4"}}

	got := RestartRequiredDiff(boot, eff)
	if slices.Contains(got, "centrals") {
		t.Errorf("pure remove: unexpected \"centrals\" in diff %v", got)
	}
}

// TestRestartRequiredDiff_CentralsModifiedInPlace verifies that a central
// present in both boot and eff with a changed field DOES surface
// "centrals" — that reconfiguration is not a live operation.
func TestRestartRequiredDiff_CentralsModifiedInPlace(t *testing.T) {
	t.Parallel()

	boot := baseConfig()
	eff := clone(boot)
	eff.Centrals[0].Host = "192.0.2.99"

	got := RestartRequiredDiff(boot, eff)
	if !slices.Contains(got, "centrals") {
		t.Errorf("expected \"centrals\" in diff %v", got)
	}
}

// TestRestartRequiredDiff_CentralsReordered verifies that reordering the
// centrals slice with identical per-name content does NOT surface
// "centrals" — the comparison is by name, not by slice position.
func TestRestartRequiredDiff_CentralsReordered(t *testing.T) {
	t.Parallel()

	boot := baseConfig()
	boot.Centrals = []CentralConfig{
		{Name: "a", Host: "1.2.3.4"},
		{Name: "b", Host: "1.2.3.5"},
	}
	eff := clone(boot)
	eff.Centrals = []CentralConfig{
		{Name: "b", Host: "1.2.3.5"},
		{Name: "a", Host: "1.2.3.4"},
	}

	got := RestartRequiredDiff(boot, eff)
	if slices.Contains(got, "centrals") {
		t.Errorf("reorder only: unexpected \"centrals\" in diff %v", got)
	}
}

// TestRestartRequiredDiff_CentralsMixedAddRemoveModify verifies a
// combined scenario: one central added, one removed, and one surviving
// central modified in place — only the modification triggers "centrals".
func TestRestartRequiredDiff_CentralsMixedAddRemoveModify(t *testing.T) {
	t.Parallel()

	boot := baseConfig()
	boot.Centrals = []CentralConfig{
		{Name: "a", Host: "1.2.3.4"},
		{Name: "b", Host: "1.2.3.5"},
	}
	eff := clone(boot)
	eff.Centrals = []CentralConfig{
		{Name: "a", Host: "1.2.3.99"}, // modified
		{Name: "c", Host: "1.2.3.6"},  // added ("b" removed)
	}

	got := RestartRequiredDiff(boot, eff)
	if !slices.Contains(got, "centrals") {
		t.Errorf("expected \"centrals\" in diff %v (survivor was modified)", got)
	}
}

// TestRestartRequiredDiff_CentralsIdentical verifies that identical
// Centrals slices produce no "centrals" diff entry.
func TestRestartRequiredDiff_CentralsIdentical(t *testing.T) {
	t.Parallel()

	boot := baseConfig()
	eff := clone(boot)

	got := RestartRequiredDiff(boot, eff)
	if slices.Contains(got, "centrals") {
		t.Errorf("identical: unexpected \"centrals\" in diff %v", got)
	}
}

// TestRestartRequiredDiff_MultipleChanges verifies that when several
// restart-required fields differ, all of them appear in the output.
func TestRestartRequiredDiff_MultipleChanges(t *testing.T) {
	t.Parallel()

	boot := baseConfig()
	eff := clone(boot)
	eff.DataDir = "/tmp/other"
	eff.North.Matter.Enabled = true
	eff.Callback.Port = 9999

	got := RestartRequiredDiff(boot, eff)
	for _, want := range []string{"data_dir", "north.matter.enabled", "callback.port"} {
		if !slices.Contains(got, want) {
			t.Errorf("expected %q in diff %v", want, got)
		}
	}
}

func ptrBoolConfig(b bool) *bool { return &b }

// TestRestartRequiredDiff_CCUAuth verifies that a change in the CCU-auth
// block surfaces "north.rest.auth.ccu" in the diff.
func TestRestartRequiredDiff_CCUAuth(t *testing.T) {
	t.Parallel()

	t.Run("enabled_pointer_changes", func(t *testing.T) {
		t.Parallel()
		boot := baseConfig()
		eff := clone(boot)
		eff.North.REST.Auth.CCU.Enabled = ptrBoolConfig(true)

		got := RestartRequiredDiff(boot, eff)
		if !slices.Contains(got, "north.rest.auth.ccu") {
			t.Errorf("expected \"north.rest.auth.ccu\" in diff %v", got)
		}
	})

	t.Run("primary_pointer_changes", func(t *testing.T) {
		t.Parallel()
		boot := baseConfig()
		boot.North.REST.Auth.CCU.Primary = ptrBoolConfig(true)
		eff := clone(boot)
		eff.North.REST.Auth.CCU.Primary = ptrBoolConfig(false)

		got := RestartRequiredDiff(boot, eff)
		if !slices.Contains(got, "north.rest.auth.ccu") {
			t.Errorf("expected \"north.rest.auth.ccu\" in diff %v", got)
		}
	})
}

// TestRestartRequiredDiff_CCUAuth_Identical verifies that identical
// CCU-auth configs produce no "north.rest.auth.ccu" diff entry.
func TestRestartRequiredDiff_CCUAuth_Identical(t *testing.T) {
	t.Parallel()

	boot := baseConfig()
	enabled := true
	boot.North.REST.Auth.CCU.Enabled = &enabled
	boot.North.REST.Auth.CCU.Central = "ccu1"
	eff := clone(boot)
	// CCU auth Enabled pointer must be deep-copied; clone only shallow-copies.
	enabledCopy := enabled
	eff.North.REST.Auth.CCU.Enabled = &enabledCopy

	got := RestartRequiredDiff(boot, eff)
	if slices.Contains(got, "north.rest.auth.ccu") {
		t.Errorf("unexpected \"north.rest.auth.ccu\" in diff %v for identical configs", got)
	}
}

// TestRestartRequiredDiff_BootWiredSurfacesAreDiffed covers the surfaces that
// carried the schema's restart-required badge while the diff ignored them: a
// save answered restart_required:false and /restart-pending stayed empty, so an
// operator who had just switched off Basic auth or retuned the alarm engine saw
// no hint that the change was inert until the next boot. The Matter identity
// and commissioning parameters are diffed for the same reason — the bridge and
// its commissionable mDNS records are built once during bring-up.
func TestRestartRequiredDiff_BootWiredSurfacesAreDiffed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path   string
		mutate func(c *Config)
	}{
		{
			path:   "north.rest.auth.basic_enabled",
			mutate: func(c *Config) { off := false; c.North.REST.Auth.BasicEnabled = &off },
		},
		{
			path:   "north.rest.auth.bearer_enabled",
			mutate: func(c *Config) { off := false; c.North.REST.Auth.BearerEnabled = &off },
		},
		{
			path:   "alarm.enabled",
			mutate: func(c *Config) { off := false; c.Alarm.Enabled = &off },
		},
		{
			path:   "alarm.default_siren_seconds",
			mutate: func(c *Config) { c.Alarm.DefaultSirenSeconds = 42 },
		},
		{
			path:   "alarm.max_acoustic_per_incident_seconds",
			mutate: func(c *Config) { c.Alarm.MaxAcousticPerIncidentSeconds = 1234 },
		},
		{
			path:   "alarm.stop_verify_seconds",
			mutate: func(c *Config) { c.Alarm.StopVerifySeconds = 77 },
		},
		{
			path:   "alarm.journal_retention_days",
			mutate: func(c *Config) { c.Alarm.JournalRetentionDays = 7 },
		},
		{
			path:   "alarm.restart_loop_breaker",
			mutate: func(c *Config) { c.Alarm.RestartLoopBreaker = 9 },
		},
		{
			path:   "alarm.duress_visibility",
			mutate: func(c *Config) { c.Alarm.DuressVisibility = "hidden" },
		},
		{
			path:   "north.matter.vendor_id",
			mutate: func(c *Config) { c.North.Matter.VendorID = 0x1234 },
		},
		{
			path:   "north.matter.product_id",
			mutate: func(c *Config) { c.North.Matter.ProductID = 0x4321 },
		},
		{
			path:   "north.matter.discriminator",
			mutate: func(c *Config) { c.North.Matter.Discriminator = 0xABC },
		},
		{
			path:   "north.matter.commissioning",
			mutate: func(c *Config) { c.North.Matter.Commissioning.Passcode = 20202021 },
		},
		{
			path:   "north.matter.commissioning",
			mutate: func(c *Config) { c.North.Matter.Commissioning.Iterations = 5000 },
		},
		{
			path:   "north.matter.commissioning",
			mutate: func(c *Config) { c.North.Matter.Commissioning.EphemeralWindow = true },
		},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			boot := baseConfig()
			eff := clone(boot)
			tc.mutate(eff)

			got := RestartRequiredDiff(boot, eff)
			if !slices.Contains(got, tc.path) {
				t.Errorf("expected %q in diff, got %v", tc.path, got)
			}
		})
	}
}

// TestRestartRequiredFieldPaths_DerivedFromRules pins the shape the schema
// endpoint consumes: every rule contributes at least one annotated field, and
// each annotated field belongs to the rule that reports it. Block-scoped rules
// (the auth blocks, Matter commissioning) list their leaves explicitly because
// the operator edits leaves, not blocks.
func TestRestartRequiredFieldPaths_DerivedFromRules(t *testing.T) {
	t.Parallel()

	paths := RestartRequiredFieldPaths()
	if len(paths) == 0 {
		t.Fatal("no restart-required field paths")
	}
	for _, r := range RestartRules() {
		if r.Path == "" {
			t.Error("rule with an empty path")
		}
		if r.Differs == nil {
			t.Errorf("rule %q has no comparison", r.Path)
		}
		fields := r.Fields
		if len(fields) == 0 {
			fields = []string{r.Path}
		}
		for _, f := range fields {
			if f != r.Path && !strings.HasPrefix(f, r.Path+".") {
				t.Errorf("rule %q annotates the unrelated field %q", r.Path, f)
			}
			if _, ok := paths[f]; !ok {
				t.Errorf("field %q of rule %q missing from RestartRequiredFieldPaths", f, r.Path)
			}
		}
	}
}
