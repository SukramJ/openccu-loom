// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

// "Security & Safety" is spelled in two production places — the fallback
// literal in [SecurityMQTTPublisher.declareEntities] and the
// `discovery.security_system` entry in each i18n catalogue — and in six test
// files. Every one of those test spellings is an *input*: they hand the name
// to BuildSecurityDiscovery and read it back out, which pins the builder's
// plumbing and says nothing about what the publisher resolves. Delete the key
// from both catalogues and all six keep passing.
//
// That is the untied constant. It matters here more than it would elsewhere
// because the name is the Home Assistant *device* name for the whole safety
// plane: change it and every entity in that plane moves to a differently
// named device, which is a migration for anyone who built a dashboard or an
// automation on it, with no error anywhere.
//
// The tests below drive `reconcile` end to end and assert the name it
// actually put on the wire, in both shipped locales.

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// securityPlaneDeviceName runs one reconcile in the given locale and returns
// the `device.name` carried by the security plane's discovery configs.
//
// It goes through the publisher rather than the builder because the
// publisher is where the catalogue lookup lives: everything the builder sees
// has already been resolved.
func securityPlaneDeviceName(t *testing.T, locale string) string {
	t.Helper()

	const base = "openccu-loom"
	obs := newObservedPlane()
	bridge := NewBridge(BridgeConfig{
		Base: base, CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
	}, obs)
	if err := bridge.AnnounceOnline(context.Background()); err != nil {
		t.Fatalf("bridge announce: %v", err)
	}

	p := NewSecurityMQTTPublisher(staticSecuritySnapshot{roundTripSnapshot()},
		NewWiring(bridge, slog.Default()), locale, "", slog.Default())
	bus := events.NewBus()
	p.Start(bus)
	t.Cleanup(p.Stop)

	events.Publish(bus, hmevent.SecurityStateChangedEvent{Base: hmevent.NewBaseAt(time.Now())})
	obs.settle(t, p)

	names := map[string]bool{}
	for _, rec := range obs.records() {
		if !isDiscoveryConfigTopic(rec.topic) {
			continue
		}
		var body struct {
			Device struct {
				Name string `json:"name"`
			} `json:"device"`
		}
		if err := json.Unmarshal([]byte(rec.payload), &body); err != nil {
			continue
		}
		if body.Device.Name != "" {
			names[body.Device.Name] = true
		}
	}
	if len(names) == 0 {
		t.Fatalf("locale %s: the security plane published no discovery config carrying a device name; "+
			"this test cannot see what it set out to check", locale)
	}
	if len(names) > 1 {
		t.Fatalf("locale %s: the security plane's entities disagree on their device name: %v — Home "+
			"Assistant would split one safety plane across several devices", locale, names)
	}
	for name := range names {
		return name
	}
	return ""
}

// TestSecurityPlaneDeviceNameIsTheResolvedCatalogueEntry ties the literal in
// security_reconcile.go and the `discovery.security_system` entry in each
// catalogue to what reaches the broker.
//
// Removing the key from a catalogue, or changing its value, fails here. So
// does changing the Go fallback while the catalogues still carry the old
// value, because the fallback is only reachable when the lookup misses, and
// asserting the resolved name is the only way to tell those two apart.
func TestSecurityPlaneDeviceNameIsTheResolvedCatalogueEntry(t *testing.T) {
	t.Parallel()

	for locale, want := range map[string]string{
		"en": "Security & Safety",
		"de": "Sicherheit & Gefahrenmelder",
	} {
		t.Run(locale, func(t *testing.T) {
			t.Parallel()
			if got := securityPlaneDeviceName(t, locale); got != want {
				t.Errorf("locale %s: the security plane published device name %q, want %q — this is "+
					"the Home Assistant device every safety entity hangs off; moving it is a "+
					"migration, not a rename", locale, got, want)
			}
		})
	}
}

// TestSecurityPlaneDeviceNameUnknownLocaleUsesTheDefaultCatalogue covers the
// other branch an operator can actually reach: a `locale` in the config that
// matches no shipped catalogue must resolve through the default one rather
// than render the raw key `discovery.security_system` as the device name.
//
// It does NOT exercise the Go literal in
// [SecurityMQTTPublisher.declareEntities]. That fallback is reached only when
// catalogue construction itself fails, which [NewSecurityMQTTPublisher] does
// not let a caller arrange — measured: changing the literal alone leaves this
// file green, and the claim that it catches a drifted fallback would be the
// same kind of overclaiming comment this test replaced. What holds the
// literal to the catalogues is
// TestSecurityCatalogueKeyIsPresentInEveryShippedLocale plus the resolved
// assertion above; the literal is a last resort nothing routine depends on.
func TestSecurityPlaneDeviceNameUnknownLocaleUsesTheDefaultCatalogue(t *testing.T) {
	t.Parallel()

	got := securityPlaneDeviceName(t, "xx-unknown")
	if got != "Security & Safety" {
		t.Errorf("unknown locale resolved the security device name to %q, want the default catalogue's "+
			"%q", got, "Security & Safety")
	}
}

// TestSecurityCatalogueKeyIsPresentInEveryShippedLocale states the other half
// directly: the two assertions above would both still pass if `de` silently
// fell back to English, because the fallback is English. This one fails if a
// shipped locale loses the key.
func TestSecurityCatalogueKeyIsPresentInEveryShippedLocale(t *testing.T) {
	t.Parallel()

	cat, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("load catalogs: %v", err)
	}
	const key = "discovery.security_system"
	locales := cat.Locales()
	if len(locales) < 2 {
		t.Fatalf("only %d locale(s) loaded; this repository ships en and de", len(locales))
	}
	for _, locale := range locales {
		if v := cat.T(locale, key); v == "" || v == key {
			t.Errorf("locale %q has no %s entry (T returned %q); the security plane's Home Assistant "+
				"device silently falls back to English for it", locale, key, v)
		}
	}
}
