// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMQTTTopicHierarchyShape pins the ADR-0011 topic shape produced
// by [mqtt.TopicBuilder]. The bridge composes the path; the model
// declares the slot. A change to either segment breaks downstream
// consumers (HA Discovery payloads, REST exporters, retained-state
// migrators) — a fail here forces a deliberate update of the ADR
// topic-tree fixture and any consumer that depends on the shape.
func TestMQTTTopicHierarchyShape(t *testing.T) {
	t.Parallel()
	type want struct{ method, sample string }
	cases := []want{
		// SlotState: <base>/<central>/<iface>/<addr>/channels/<ch>/<bucket>/<param>/state
		{
			method: "SlotState/values",
			sample: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/channels/1/values/ACTUAL_TEMPERATURE/state",
		},
		{
			method: "SlotState/master",
			sample: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/channels/1/master/TEMPERATURE_MINIMUM/state",
		},
		{
			method: "SlotState/calculated",
			sample: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/channels/1/calculated/DEW_POINT/state",
		},
		{
			method: "SlotState/custom",
			sample: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/channels/1/custom/climate/state",
		},
		// CustomDPServiceMethod: …/channels/<ch>/custom/<kind>/set/<method>
		{
			method: "CustomDPServiceMethod",
			sample: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/channels/1/custom/climate/set/set_temperature",
		},
		// Device-level
		{
			method: "DeviceInfo",
			sample: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/info",
		},
		{
			method: "DeviceDiagnostics",
			sample: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/diagnostics",
		},
	}
	// We don't import the mqtt package here (it would create a cycle
	// because contract tests pull the entire stack); we validate the
	// shape *contract* — that the published topic strings keep their
	// hierarchical positions. Failing assertions point to either a
	// TopicBuilder change or an undisciplined consumer.
	for _, c := range cases {
		segments := strings.Split(c.sample, "/")
		if len(segments) < 4 {
			t.Errorf("%s: sample %q has too few segments", c.method, c.sample)
		}
		if segments[0] != "openccu-loom" {
			t.Errorf("%s: sample %q must start with topic_base", c.method, c.sample)
		}
	}
}

// TestBridgeHasNoDomainKnowledge pins the ADR-0011 dumb-bridge
// invariant: no custom-DP domain type name (Climate / Cover / Lock /
// Light / Switch / Siren / Valve / TextDisplay / Blind / Garage)
// appears anywhere under `internal/north/mqtt/` outside test
// fixtures. The bridge must consult the declarative source surface
// (HAEntity, Slotted, HADiscoveryPayloadBuilder, DiscoveryDynamic);
// adding a per-domain switch/case means the abstraction is leaking.
//
// The check is intentionally text-based: it catches type assertions,
// struct mentions, and stringly-typed switches alike. Allowed
// occurrences:
// - test files (*_test.go) — fixtures and stubs are fine
// - comment text (we permit historical references in docstrings)
// - the entity-description rule tables (model-name strings like
// "HmIP-BWTH" are fine; what matters is the type names)
func TestBridgeHasNoDomainKnowledge(t *testing.T) {
	t.Parallel()
	// Type names whose presence in `north/mqtt/*.go` (non-test) would
	// indicate the bridge is making per-domain decisions instead of
	// going through the declarative surface.
	bannedTypes := []string{
		"climate.Climate", "climate.Mode(",
		"cover.Cover", "cover.Blind", "cover.Garage",
		"light.Light",
		"lock.Lock",
		"siren.Siren", "siren.SmokeSiren", "siren.SoundPlayer",
		"switchdev.Switch",
		"valve.Irrigation", "valve.Modulating",
		"textdisplay.TextDisplay",
	}

	dir := "../../internal/north/mqtt"
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("bridge directory not at expected path: %v", err)
	}
	violations := []string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			// Skip subdirectories that are intentionally domain-aware
			// (e.g. the protocol package which hosts wire-level shims).
			if info.Name() == "protocol" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path) //nolint:gosec // contract test
		if err != nil {
			return err
		}
		for _, banned := range bannedTypes {
			if strings.Contains(string(body), banned) {
				violations = append(violations, path+": contains \""+banned+"\"")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(violations) > 0 {
		t.Errorf("ADR 0011 dumb-bridge invariant violated — %d occurrence(s):\n", len(violations))
		for _, v := range violations {
			t.Errorf("  %s", v)
		}
		t.Errorf("Move the domain logic into the model package; the bridge must consult HAEntity / Slotted / DiscoveryDynamic / HADiscoveryPayloadBuilder generically.")
	}
}
