// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package masterprofile

import (
	"errors"
	"strings"
	"testing"
)

func TestStoreDeviceTypesNonEmpty(t *testing.T) {
	s := New()
	types, err := s.DeviceTypes()
	if err != nil {
		t.Fatalf("DeviceTypes() err = %v", err)
	}
	if len(types) < 50 {
		t.Fatalf("expected >50 embedded device types, got %d", len(types))
	}
	// must be sorted ascending
	for i := 1; i < len(types); i++ {
		if types[i-1] >= types[i] {
			t.Fatalf("device types not sorted: %s >= %s", types[i-1], types[i])
		}
	}
}

func TestStoreLookupBlindGeneric(t *testing.T) {
	s := New()
	profiles, err := s.Profiles("BLIND", "")
	if err != nil {
		t.Fatalf("Profiles(BLIND, '') err = %v", err)
	}
	if len(profiles) == 0 {
		t.Fatalf("BLIND/KEY returned no profiles")
	}
	// First profile should be id=0 (Expert).
	if profiles[0].ID != 0 {
		t.Fatalf("expected id=0 first, got %d", profiles[0].ID)
	}
	if name := profiles[0].LocalisedName("de"); name == "" {
		t.Fatalf("expected localised de name, got empty")
	}
}

func TestStoreLocalisedFallback(t *testing.T) {
	s := New()
	p, err := s.Profile("BLIND", "", 0)
	if err != nil {
		t.Fatalf("Profile err: %v", err)
	}
	if got := p.LocalisedName("xx"); got == "" {
		t.Fatalf("expected fallback name for unknown locale, got empty")
	}
}

func TestStoreUnknownDeviceTypeReturnsNotFound(t *testing.T) {
	s := New()
	_, err := s.Profiles("DOES_NOT_EXIST", "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreEmptyDeviceTypeRejected(t *testing.T) {
	s := New()
	_, err := s.Profiles("", "")
	if err == nil || !strings.Contains(err.Error(), "empty device type") {
		t.Fatalf("expected empty-device-type error, got %v", err)
	}
}

func TestStoreChannelTypesIncludeKey(t *testing.T) {
	s := New()
	cts, err := s.ChannelTypes("ACTOR_SECURITY")
	if err != nil {
		t.Fatalf("ChannelTypes err: %v", err)
	}
	hasKey := false
	for _, c := range cts {
		if c == "KEY" {
			hasKey = true
		}
	}
	if !hasKey {
		t.Fatalf("expected KEY in channel types of ACTOR_SECURITY, got %v", cts)
	}
}

// TestProfilesMutatingResultDoesNotAffectCache verifies that the
// Name/Description/Params maps of a Profile returned by Profiles/Profile
// are independent copies: mutating them must not corrupt the store's
// cached data used by later lookups.
func TestProfilesMutatingResultDoesNotAffectCache(t *testing.T) {
	s := New()

	profiles1, err := s.Profiles("BLIND", "")
	if err != nil {
		t.Fatalf("Profiles(BLIND, '') err = %v", err)
	}
	if len(profiles1) == 0 || len(profiles1[0].Name) == 0 {
		t.Fatal("expected at least one profile with a non-empty Name map")
	}
	for k := range profiles1[0].Name {
		profiles1[0].Name[k] = "MUTATED"
	}
	for k := range profiles1[0].Params {
		profiles1[0].Params[k] = ParamConstraint{ConstraintType: "MUTATED"}
	}

	profiles2, err := s.Profiles("BLIND", "")
	if err != nil {
		t.Fatalf("second Profiles(BLIND, '') err = %v", err)
	}
	for k, v := range profiles2[0].Name {
		if v == "MUTATED" {
			t.Fatalf("cache corrupted: Name[%q] leaked mutation from prior caller", k)
		}
	}
	for k, c := range profiles2[0].Params {
		if c.ConstraintType == "MUTATED" {
			t.Fatalf("cache corrupted: Params[%q] leaked mutation from prior caller", k)
		}
	}
}

func TestStoreCaching(t *testing.T) {
	s := New()
	if _, err := s.Profiles("BLIND", ""); err != nil {
		t.Fatalf("first lookup err: %v", err)
	}
	// Second call hits cache (no error possible from re-decode failure).
	if _, err := s.Profiles("BLIND", "KEY"); err != nil {
		t.Fatalf("cached lookup err: %v", err)
	}
}

// TestProfileTextIsNotHTMLEscaped is the masterprofile counterpart of the
// linkprofile guard: the archive stores the CCU WebUI's HTML fragments
// ("Bew&auml;sserungsaktor"), and every consumer renders them as plain text,
// so the reference must be decoded on load.
func TestProfileTextIsNotHTMLEscaped(t *testing.T) {
	s := New()
	types, err := s.DeviceTypes()
	if err != nil {
		t.Fatalf("DeviceTypes: %v", err)
	}
	var checked, withUmlaut int
	for _, dt := range types {
		chTypes, err := s.ChannelTypes(dt)
		if err != nil {
			continue
		}
		for _, ct := range chTypes {
			profiles, err := s.Profiles(dt, ct)
			if err != nil {
				continue
			}
			for _, p := range profiles {
				for _, m := range []map[string]string{p.Name, p.Description} {
					for locale, text := range m {
						checked++
						if strings.Contains(text, "&") && strings.Contains(text, ";") {
							t.Errorf("%s/%s profile %d [%s] still carries an HTML reference: %q", dt, ct, p.ID, locale, text)
						}
						if strings.ContainsAny(text, "äöüÄÖÜß") {
							withUmlaut++
						}
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no profile text inspected — the archive lookup returned nothing")
	}
	if withUmlaut == 0 {
		t.Error("expected decoded umlauts somewhere in the archive")
	}
	t.Logf("inspected %d display strings, %d carry umlauts", checked, withUmlaut)
}
