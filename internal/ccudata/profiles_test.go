// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ccudata

import (
	"encoding/json"
	"testing"
)

// TestApplyCustomTableAllBranches exercises every applyCustomTable switch arm.
func TestApplyCustomTableAllBranches(t *testing.T) {
	tr := Empty()

	// device_icons arm.
	applyCustomTable(tr, "device_icons", map[string]string{"263 130": "icon.png"})
	if tr.DeviceIcons["263 130"] != "icon.png" {
		t.Fatal("applyCustomTable: device_icons arm failed")
	}

	// parameters_de arm.
	applyCustomTable(tr, "parameters_de", map[string]string{"level": "Niveau"})
	if tr.Parameters["de"]["level"] != "Niveau" {
		t.Fatal("applyCustomTable: parameters_de arm failed")
	}

	// parameter_values_en arm.
	applyCustomTable(tr, "parameter_values_en", map[string]string{"level=100": "Open"})
	if tr.ParameterValues["en"]["level=100"] != "Open" {
		t.Fatal("applyCustomTable: parameter_values_en arm failed")
	}

	// parameter_help_de arm.
	applyCustomTable(tr, "parameter_help_de", map[string]string{"level": "Hilfetext"})
	if tr.ParameterHelp["de"]["level"] != "Hilfetext" {
		t.Fatal("applyCustomTable: parameter_help_de arm failed")
	}

	// channel_types_de arm.
	applyCustomTable(tr, "channel_types_de", map[string]string{"shutter": "Rolladen"})
	if tr.ChannelTypes["de"]["shutter"] != "Rolladen" {
		t.Fatal("applyCustomTable: channel_types_de arm failed")
	}

	// device_models_de arm.
	applyCustomTable(tr, "device_models_de", map[string]string{"hmip-swdo": "Aktor"})
	if tr.DeviceModels["de"]["hmip-swdo"] != "Aktor" {
		t.Fatal("applyCustomTable: device_models_de arm failed")
	}

	// ui_labels_de arm.
	applyCustomTable(tr, "ui_labels_de", map[string]string{"btn.ok": "OK"})
	if tr.UILabels["de"]["btn.ok"] != "OK" {
		t.Fatal("applyCustomTable: ui_labels_de arm failed")
	}

	// Unknown stem — no-op, no panic.
	applyCustomTable(tr, "unknown_stem_de", map[string]string{"key": "val"})
}

// TestProfileStoreResolveNilReceiver exercises the nil-receiver guard.
func TestProfileStoreResolveNilReceiver(t *testing.T) {
	var s *ProfileStore
	_, ok := s.Resolve("ANYTHING")
	if ok {
		t.Fatal("nil ProfileStore.Resolve should return false")
	}
}

// TestProfileStoreResolveAlias exercises the alias path.
func TestProfileStoreResolveAlias(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"profiles": []any{}})
	s := &ProfileStore{
		Receivers: map[string]json.RawMessage{"canonical": raw},
		Aliases:   map[string]string{"alias": "canonical"},
	}
	got, ok := s.Resolve("alias")
	if !ok {
		t.Fatal("Resolve via alias should succeed")
	}
	if string(got) != string(raw) {
		t.Fatalf("Resolve alias mismatch: %s", got)
	}
}

// TestProfileStoreResolveNotFound exercises the "key not found" path.
func TestProfileStoreResolveNotFound(t *testing.T) {
	s := &ProfileStore{
		Receivers: map[string]json.RawMessage{},
		Aliases:   map[string]string{},
	}
	_, ok := s.Resolve("unknown")
	if ok {
		t.Fatal("Resolve unknown key should return false")
	}
}

// TestProfileStoreResolvedProfileNotFound exercises the "receiver not
// found" branch.
func TestProfileStoreResolvedProfileNotFound(t *testing.T) {
	s := &ProfileStore{
		Receivers: map[string]json.RawMessage{},
		Aliases:   map[string]string{},
	}
	_, ok := s.ResolvedProfile("UNKNOWN", 1, "de")
	if ok {
		t.Fatal("ResolvedProfile unknown receiver should return false")
	}
}

// TestProfileStoreResolvedProfileIDNotFound exercises the "id not in
// profiles" branch.
func TestProfileStoreResolvedProfileIDNotFound(t *testing.T) {
	profileJSON, _ := json.Marshal(map[string]any{
		"RCVTYPE": map[string]any{
			"profiles": []any{
				map[string]any{
					"id":          1,
					"name":        map[string]string{"en": "Auto"},
					"description": map[string]string{"en": "Automatic mode"},
				},
			},
		},
	})
	s := &ProfileStore{
		Receivers: map[string]json.RawMessage{"RCVTYPE": profileJSON},
		Aliases:   map[string]string{},
	}
	_, ok := s.ResolvedProfile("RCVTYPE", 99, "de")
	if ok {
		t.Fatal("ResolvedProfile with unknown id should return false")
	}
}

// TestProfileStoreResolvedProfileEnFallback exercises the locale → "en"
// fallback in name and the "Profile N" fallback when neither locale nor
// "en" has a name.
func TestProfileStoreResolvedProfileEnFallback(t *testing.T) {
	profileJSON, _ := json.Marshal(map[string]any{
		"RCVTYPE": map[string]any{
			"profiles": []any{
				map[string]any{
					"id":          2,
					"name":        map[string]string{"en": "Manual"},
					"description": map[string]string{},
				},
				map[string]any{
					"id":          3,
					"name":        map[string]string{},
					"description": map[string]string{},
				},
			},
		},
	})
	s := &ProfileStore{
		Receivers: map[string]json.RawMessage{"RCVTYPE": profileJSON},
		Aliases:   map[string]string{},
	}

	// id=2: locale "de" absent → falls back to "en" name.
	rp, ok := s.ResolvedProfile("RCVTYPE", 2, "de")
	if !ok {
		t.Fatal("ResolvedProfile id=2 should succeed")
	}
	if rp.Name != "Manual" {
		t.Fatalf("ResolvedProfile en-fallback name = %q, want Manual", rp.Name)
	}

	// id=3: neither locale nor "en" → "Profile 3".
	rp, ok = s.ResolvedProfile("RCVTYPE", 3, "de")
	if !ok {
		t.Fatal("ResolvedProfile id=3 should succeed")
	}
	if rp.Name != "Profile 3" {
		t.Fatalf("ResolvedProfile default name = %q, want 'Profile 3'", rp.Name)
	}
}
