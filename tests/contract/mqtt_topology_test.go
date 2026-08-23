// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mqtt "github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// TestMQTTTopicHierarchyShape pins the ADR-0011 topic shape actually
// produced by [mqtt.TopicBuilder] — every case below calls the real
// builder method rather than restating its output as a literal, so a
// TopicBuilder change that breaks the hierarchy (a swapped segment, a
// method that stops delegating to the model layer) fails here instead
// of silently reaching HA Discovery / REST exporters / retained-state
// migrators.
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
		wantSegments int
		// checks maps a 0-indexed segment position to its required value.
		checks map[int]string
	}
	cases := []want{
		{
			method:       "SlotState/values",
			topic:        tb.SlotState(central, iface, payload.TopicSlot{Address: addr, Channel: channel, Bucket: payload.BucketValues, Parameter: "ACTUAL_TEMPERATURE"}),
			wantSegments: 7,
			checks:       map[int]string{4: "1", 5: "values", 6: "ACTUAL_TEMPERATURE"},
		},
		{
			method:       "SlotState/master",
			topic:        tb.SlotState(central, iface, payload.TopicSlot{Address: addr, Channel: channel, Bucket: payload.BucketMaster, Parameter: "TEMPERATURE_MINIMUM"}),
			wantSegments: 7,
			checks:       map[int]string{5: "master", 6: "TEMPERATURE_MINIMUM"},
		},
		{
			method:       "SlotState/calculated",
			topic:        tb.SlotState(central, iface, payload.TopicSlot{Address: addr, Channel: channel, Bucket: payload.BucketCalculated, Parameter: "DEW_POINT"}),
			wantSegments: 7,
			checks:       map[int]string{5: "calculated", 6: "DEW_POINT"},
		},
		{
			method:       "SlotState/custom",
			topic:        tb.SlotState(central, iface, payload.TopicSlot{Address: addr, Channel: channel, Bucket: payload.BucketCustom, Parameter: "climate"}),
			wantSegments: 7,
			checks:       map[int]string{4: "1", 5: "custom", 6: "climate"},
		},
		{
			method:       "CustomDPServiceMethod",
			topic:        tb.CustomDPServiceMethod(central, iface, payload.TopicSlot{Address: addr, Channel: channel, Bucket: payload.BucketCustom, Parameter: "climate"}, "set_temperature"),
			wantSegments: 9,
			checks:       map[int]string{4: "1", 5: "custom", 6: "climate", 7: "set", 8: "set_temperature"},
		},
		{
			method:       "DeviceInfo",
			topic:        tb.DeviceInfo(central, iface, addr),
			wantSegments: 5,
			checks:       map[int]string{4: "info"},
		},
		{
			method:       "DeviceDiagnostics",
			topic:        tb.DeviceDiagnostics(central, iface, addr),
			wantSegments: 5,
			checks:       map[int]string{4: "diagnostics"},
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
		if segments[0] != base {
			t.Errorf("%s: topic %q segment[0] = %q, want base %q", c.method, c.topic, segments[0], base)
		}
		if segments[1] != central {
			t.Errorf("%s: topic %q segment[1] = %q, want central %q", c.method, c.topic, segments[1], central)
		}
		if segments[2] != iface {
			t.Errorf("%s: topic %q segment[2] = %q, want interface %q", c.method, c.topic, segments[2], iface)
		}
		if segments[3] != addr {
			t.Errorf("%s: topic %q segment[3] = %q, want device address %q", c.method, c.topic, segments[3], addr)
		}
		for pos, want := range c.checks {
			if pos >= len(segments) || segments[pos] != want {
				t.Errorf("%s: topic %q segment[%d] = %q, want %q", c.method, c.topic, pos, segments[pos], want)
			}
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
