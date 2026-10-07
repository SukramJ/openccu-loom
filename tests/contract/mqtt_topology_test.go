// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mqtt "github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// TestMQTTTopicHierarchyShape pins the mqtt-smarthome 2.0 topic shape
// (ADR 0083) actually produced by [mqtt.TopicBuilder]:
// `<name>/<function>/<central>/<iface>/<addr>/…`. Every case below calls the
// real builder method rather than restating its output as a literal, so a
// TopicBuilder change that breaks the hierarchy (a swapped segment, a function
// moved back into a suffix, a method that stops delegating to the model layer)
// fails here instead of silently reaching HA Discovery / REST exporters /
// retained-state migrators.
func TestMQTTTopicHierarchyShape(t *testing.T) {
	t.Parallel()

	const (
		base    = "openccu-loom"
		central = "GoOtto"
		iface   = "HmIP-RF"
		addr    = "000C9709AEF157"
		channel = 1
	)
	tb := mqtt.NewTopicBuilder(base)

	type want struct {
		method       string
		topic        string
		function     string
		wantSegments int
		// checks maps a 0-indexed segment position to its required value.
		checks map[int]string
	}
	cases := []want{
		{
			method:       "SlotState/values",
			topic:        tb.SlotState(central, iface, payload.TopicSlot{Address: addr, Channel: channel, Bucket: payload.BucketValues, Parameter: "ACTUAL_TEMPERATURE"}),
			function:     "status",
			wantSegments: 8,
			checks:       map[int]string{5: "1", 6: "values", 7: "ACTUAL_TEMPERATURE"},
		},
		{
			method:       "SlotState/master",
			topic:        tb.SlotState(central, iface, payload.TopicSlot{Address: addr, Channel: channel, Bucket: payload.BucketMaster, Parameter: "TEMPERATURE_MINIMUM"}),
			function:     "status",
			wantSegments: 8,
			checks:       map[int]string{6: "master", 7: "TEMPERATURE_MINIMUM"},
		},
		{
			method:       "SlotState/calculated",
			topic:        tb.SlotState(central, iface, payload.TopicSlot{Address: addr, Channel: channel, Bucket: payload.BucketCalculated, Parameter: "DEW_POINT"}),
			function:     "status",
			wantSegments: 8,
			checks:       map[int]string{6: "calculated", 7: "DEW_POINT"},
		},
		{
			method:       "SlotState/custom",
			topic:        tb.SlotState(central, iface, payload.TopicSlot{Address: addr, Channel: channel, Bucket: payload.BucketCustom, Parameter: "climate"}),
			function:     "status",
			wantSegments: 8,
			checks:       map[int]string{5: "1", 6: "custom", 7: "climate"},
		},
		{
			method:       "SlotConfig/values",
			topic:        tb.SlotConfig(central, iface, payload.TopicSlot{Address: addr, Channel: channel, Bucket: payload.BucketValues, Parameter: "ACTUAL_TEMPERATURE"}),
			function:     "meta",
			wantSegments: 8,
			checks:       map[int]string{5: "1", 6: "values", 7: "ACTUAL_TEMPERATURE"},
		},
		{
			method:       "ParameterCommand",
			topic:        tb.ParameterCommand(central, iface, addr, channel, payload.BucketValues, "SET_POINT_TEMPERATURE"),
			function:     "set",
			wantSegments: 8,
			checks:       map[int]string{5: "1", 6: "values", 7: "SET_POINT_TEMPERATURE"},
		},
		{
			method:       "CustomDPServiceMethod",
			topic:        tb.CustomDPServiceMethod(central, iface, payload.TopicSlot{Address: addr, Channel: channel, Bucket: payload.BucketCustom, Parameter: "climate"}, "set_temperature"),
			function:     "set",
			wantSegments: 9,
			checks:       map[int]string{5: "1", 6: "custom", 7: "climate", 8: "set_temperature"},
		},
		{
			method:       "DeviceAvailability",
			topic:        tb.DeviceAvailability(central, iface, addr),
			function:     "status",
			wantSegments: 6,
			checks:       map[int]string{5: "online"},
		},
		{
			method:       "DeviceInfo",
			topic:        tb.DeviceInfo(central, iface, addr),
			function:     "status",
			wantSegments: 6,
			checks:       map[int]string{5: "info"},
		},
		{
			method:       "DeviceDiagnostics",
			topic:        tb.DeviceDiagnostics(central, iface, addr),
			function:     "status",
			wantSegments: 6,
			checks:       map[int]string{5: "diagnostics"},
		},
	}

	for _, c := range cases {
		segments := strings.Split(c.topic, "/")
		if c.topic == "" {
			t.Errorf("%s: TopicBuilder returned an empty topic", c.method)
			continue
		}
		if len(segments) != c.wantSegments {
			t.Errorf("%s: topic %q has %d segments, want %d", c.method, c.topic, len(segments), c.wantSegments)
			continue
		}
		for pos, want := range map[int]string{0: base, 1: c.function, 2: central, 3: iface, 4: addr} {
			if segments[pos] != want {
				t.Errorf("%s: topic %q segment[%d] = %q, want %q", c.method, c.topic, pos, segments[pos], want)
			}
		}
		for pos, want := range c.checks {
			if pos >= len(segments) || segments[pos] != want {
				t.Errorf("%s: topic %q segment[%d] = %q, want %q", c.method, c.topic, pos, segments[pos], want)
			}
		}
	}

	// The instance topics sit directly below the name, with no item level.
	for got, want := range map[string]string{
		tb.Connected():          base + "/connected",
		tb.Info():               base + "/info",
		tb.Maintenance("stats"): base + "/maintenance/stats",
		tb.HubStatus(central):   base + "/status/" + central + "/online",
		tb.AddonUpdateState():   base + "/status/system/addon_update",
		tb.AddonUpdateCommand(): base + "/set/system/addon_update",
	} {
		if got != want {
			t.Errorf("instance/daemon-level topic = %q, want %q", got, want)
		}
	}
}

// TestBridgeHasNoDomainKnowledge pins the ADR-0011 dumb-bridge
// invariant: no custom-DP domain type name (Climate / Cover / Lock /
// Light / Switch / Siren / Valve / TextDisplay / Blind / Garage)
// appears anywhere under `internal/north/mqtt/` outside test
// fixtures. The bridge must consult the declarative source surface
// (HAEntity, Slotted, HADiscoveryEntityBuilder, DiscoveryDynamic);
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
		t.Errorf("Move the domain logic into the model package; the bridge must consult HAEntity / Slotted / DiscoveryDynamic / HADiscoveryEntityBuilder generically.")
	}
}
