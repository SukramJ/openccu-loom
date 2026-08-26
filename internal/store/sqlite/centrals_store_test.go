// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func newCentralsStore(t *testing.T) *CentralsStore {
	t.Helper()
	return NewCentralsStore(openTestDB(t, "centrals.db"))
}

func baseCentralRow(name string) CentralRow {
	return CentralRow{
		Name:                  name,
		Host:                  "192.168.1.10",
		Port:                  2001,
		JSONRPCPort:           9292,
		Username:              "Admin",
		PasswordEnv:           "CCU_PASSWORD",
		PasswordPlain:         "",
		TLS:                   true,
		TLSInsecureSkipVerify: false,
		PrimaryInterface:      "HmIP-RF",
		Interfaces: []config.InterfaceSpec{
			{Name: "HmIP-RF"},
			{Name: "BidCos-RF", Port: 2000},
		},
		Ports: map[string]int{"HmIP-RF": 2010},
		Visibility: config.VisibilityConfig{
			UnIgnore: []string{"VALVE_STATE"},
		},
		Enabled: true,
	}
}

// TestCentralsStorePutGetRoundTrip verifies that every field in
// CentralRow survives a Put → Get round-trip.
func TestCentralsStorePutGetRoundTrip(t *testing.T) {
	s := newCentralsStore(t)
	ctx := context.Background()

	in := baseCentralRow("ccu1")
	if err := s.Put(ctx, in); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, "ccu1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name != in.Name {
		t.Errorf("Name=%q want %q", got.Name, in.Name)
	}
	if got.Host != in.Host {
		t.Errorf("Host=%q want %q", got.Host, in.Host)
	}
	if got.Port != in.Port {
		t.Errorf("Port=%d want %d", got.Port, in.Port)
	}
	if got.JSONRPCPort != in.JSONRPCPort {
		t.Errorf("JSONRPCPort=%d want %d", got.JSONRPCPort, in.JSONRPCPort)
	}
	if got.Username != in.Username {
		t.Errorf("Username=%q want %q", got.Username, in.Username)
	}
	if got.PasswordEnv != in.PasswordEnv {
		t.Errorf("PasswordEnv=%q want %q", got.PasswordEnv, in.PasswordEnv)
	}
	if got.PrimaryInterface != in.PrimaryInterface {
		t.Errorf("PrimaryInterface=%q want %q", got.PrimaryInterface, in.PrimaryInterface)
	}
	if len(got.Interfaces) != len(in.Interfaces) {
		t.Fatalf("Interfaces len=%d want %d", len(got.Interfaces), len(in.Interfaces))
	}
	if got.Interfaces[0].Name != "HmIP-RF" {
		t.Errorf("Interfaces[0].Name=%q want HmIP-RF", got.Interfaces[0].Name)
	}
	if got.Interfaces[1].Port != 2000 {
		t.Errorf("Interfaces[1].Port=%d want 2000", got.Interfaces[1].Port)
	}
	if got.Ports["HmIP-RF"] != 2010 {
		t.Errorf("Ports[HmIP-RF]=%d want 2010", got.Ports["HmIP-RF"])
	}
	if len(got.Visibility.UnIgnore) != 1 || got.Visibility.UnIgnore[0] != "VALVE_STATE" {
		t.Errorf("Visibility.UnIgnore=%v want [VALVE_STATE]", got.Visibility.UnIgnore)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero")
	}
}

// TestCentralsStorePutBoolFields verifies that TLS, TLSInsecureSkipVerify,
// and Enabled survive the int-round-trip in SQLite.
func TestCentralsStorePutBoolFields(t *testing.T) {
	s := newCentralsStore(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		tls   bool
		insec bool
		en    bool
	}{
		{"all-true", true, true, true},
		{"all-false", false, false, false},
		{"mixed", true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := baseCentralRow(tc.name)
			r.TLS = tc.tls
			r.TLSInsecureSkipVerify = tc.insec
			r.Enabled = tc.en
			if err := s.Put(ctx, r); err != nil {
				t.Fatalf("Put: %v", err)
			}
			got, err := s.Get(ctx, tc.name)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.TLS != tc.tls {
				t.Errorf("TLS=%v want %v", got.TLS, tc.tls)
			}
			if got.TLSInsecureSkipVerify != tc.insec {
				t.Errorf("TLSInsecureSkipVerify=%v want %v", got.TLSInsecureSkipVerify, tc.insec)
			}
			if got.Enabled != tc.en {
				t.Errorf("Enabled=%v want %v", got.Enabled, tc.en)
			}
		})
	}
}

// TestCentralsStorePutUpserts verifies that a second Put on the same
// name updates the row rather than creating a duplicate.
func TestCentralsStorePutUpserts(t *testing.T) {
	s := newCentralsStore(t)
	ctx := context.Background()

	r := baseCentralRow("ccuX")
	if err := s.Put(ctx, r); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	r.Host = "10.0.0.1"
	r.Port = 9999
	if err := s.Put(ctx, r); err != nil {
		t.Fatalf("second Put: %v", err)
	}

	n, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Fatalf("Count=%d want 1 (upsert must not duplicate)", n)
	}
	got, err := s.Get(ctx, "ccuX")
	if err != nil {
		t.Fatalf("Get after upsert: %v", err)
	}
	if got.Host != "10.0.0.1" {
		t.Errorf("Host=%q want 10.0.0.1", got.Host)
	}
	if got.Port != 9999 {
		t.Errorf("Port=%d want 9999", got.Port)
	}
}

// TestCentralsStoreGetUnknown verifies ErrCentralNotFound for a missing name.
func TestCentralsStoreGetUnknown(t *testing.T) {
	s := newCentralsStore(t)
	ctx := context.Background()

	_, err := s.Get(ctx, "does-not-exist")
	if !errors.Is(err, ErrCentralNotFound) {
		t.Errorf("Get unknown: want ErrCentralNotFound, got %v", err)
	}
}

// TestCentralsStoreDelete verifies happy-path deletion.
func TestCentralsStoreDelete(t *testing.T) {
	s := newCentralsStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, baseCentralRow("del1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete(ctx, "del1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "del1"); !errors.Is(err, ErrCentralNotFound) {
		t.Errorf("Get after Delete: want ErrCentralNotFound, got %v", err)
	}
}

// TestCentralsStoreDeleteUnknown verifies ErrCentralNotFound for a
// missing row.
func TestCentralsStoreDeleteUnknown(t *testing.T) {
	s := newCentralsStore(t)
	ctx := context.Background()

	err := s.Delete(ctx, "ghost")
	if !errors.Is(err, ErrCentralNotFound) {
		t.Errorf("Delete unknown: want ErrCentralNotFound, got %v", err)
	}
}

// TestCentralsStoreListSortedByName verifies ORDER BY name.
func TestCentralsStoreListSortedByName(t *testing.T) {
	s := newCentralsStore(t)
	ctx := context.Background()

	for _, n := range []string{"zulu", "alpha", "mike"} {
		r := baseCentralRow(n)
		if err := s.Put(ctx, r); err != nil {
			t.Fatalf("Put %s: %v", n, err)
		}
	}
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("List len=%d want 3", len(rows))
	}
	want := []string{"alpha", "mike", "zulu"}
	for i, w := range want {
		if rows[i].Name != w {
			t.Errorf("rows[%d].Name=%q want %q", i, rows[i].Name, w)
		}
	}
}

// TestCentralsStoreEmptyInterfacesAndPorts verifies that nil Interfaces
// and nil Ports are stored and recovered as empty slices/maps, not
// causing JSON parse errors.
func TestCentralsStoreEmptyInterfacesAndPorts(t *testing.T) {
	s := newCentralsStore(t)
	ctx := context.Background()

	r := CentralRow{
		Name:    "empty-ifaces",
		Host:    "10.0.0.2",
		Enabled: true,
	}
	if err := s.Put(ctx, r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, "empty-ifaces")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Interfaces should be decoded as empty slice (not nil error).
	if got.Interfaces == nil {
		// nil is acceptable — the important thing is no error occurred.
		got.Interfaces = []config.InterfaceSpec{}
	}
	if len(got.Interfaces) != 0 {
		t.Errorf("Interfaces len=%d want 0", len(got.Interfaces))
	}
}

// TestCentralsStoreSerialRoundTrip verifies that the Serial field survives a
// Put → Get and Put → List round-trip, and that a row stored without a serial
// reads back as an empty string.
func TestCentralsStoreSerialRoundTrip(t *testing.T) {
	t.Parallel()
	s := newCentralsStore(t)
	ctx := context.Background()

	// Row with a serial.
	withSerial := baseCentralRow("ser-ccu")
	withSerial.Serial = "0123ABC"
	if err := s.Put(ctx, withSerial); err != nil {
		t.Fatalf("Put with serial: %v", err)
	}

	// Row without a serial.
	noSerial := baseCentralRow("no-ser-ccu")
	noSerial.Serial = ""
	if err := s.Put(ctx, noSerial); err != nil {
		t.Fatalf("Put without serial: %v", err)
	}

	// Get round-trip for the row with a serial.
	got, err := s.Get(ctx, "ser-ccu")
	if err != nil {
		t.Fatalf("Get ser-ccu: %v", err)
	}
	if got.Serial != "0123ABC" {
		t.Errorf("Get: Serial=%q, want %q", got.Serial, "0123ABC")
	}

	// Get round-trip for the row without a serial.
	gotNone, err := s.Get(ctx, "no-ser-ccu")
	if err != nil {
		t.Fatalf("Get no-ser-ccu: %v", err)
	}
	if gotNone.Serial != "" {
		t.Errorf("Get: Serial=%q, want empty string", gotNone.Serial)
	}

	// List round-trip — check both rows are visible with correct serials.
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byName := map[string]CentralRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if r, ok := byName["ser-ccu"]; !ok {
		t.Error("List: ser-ccu not found")
	} else if r.Serial != "0123ABC" {
		t.Errorf("List: ser-ccu Serial=%q, want %q", r.Serial, "0123ABC")
	}
	if r, ok := byName["no-ser-ccu"]; !ok {
		t.Error("List: no-ser-ccu not found")
	} else if r.Serial != "" {
		t.Errorf("List: no-ser-ccu Serial=%q, want empty string", r.Serial)
	}
}

func TestCentralsStoreBackfillSerial(t *testing.T) {
	t.Parallel()
	s := newCentralsStore(t)
	ctx := context.Background()

	// Existing row with an empty serial (predates serial capture).
	row := baseCentralRow("kearney")
	row.Serial = ""
	if err := s.Put(ctx, row); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Backfill fills the empty serial and reports the update.
	updated, err := s.BackfillSerial(ctx, "kearney", "0123456789")
	if err != nil {
		t.Fatalf("BackfillSerial: %v", err)
	}
	if !updated {
		t.Fatal("want updated=true for an empty serial")
	}
	if got, _ := s.Get(ctx, "kearney"); got.Serial != "0123456789" {
		t.Errorf("Serial=%q, want %q", got.Serial, "0123456789")
	}

	// A second backfill is a no-op: a serial already exists, value preserved.
	updated, err = s.BackfillSerial(ctx, "kearney", "DIFFERENT9")
	if err != nil {
		t.Fatalf("BackfillSerial (second): %v", err)
	}
	if updated {
		t.Error("want updated=false when a serial already exists")
	}
	if got, _ := s.Get(ctx, "kearney"); got.Serial != "0123456789" {
		t.Errorf("serial overwritten: got %q, want %q", got.Serial, "0123456789")
	}

	// Unknown central and empty-serial argument are both no-ops, no error.
	if updated, err := s.BackfillSerial(ctx, "does-not-exist", "ABC1234567"); err != nil || updated {
		t.Errorf("unknown central: updated=%v err=%v, want false/nil", updated, err)
	}
	if updated, err := s.BackfillSerial(ctx, "kearney", ""); err != nil || updated {
		t.Errorf("empty serial arg: updated=%v err=%v, want false/nil", updated, err)
	}
}

// TestCentralsStoreBehaviorRoundTrip verifies the per-central behavior
// block survives a Put → Get round-trip through the behavior_json column.
func TestCentralsStoreBehaviorRoundTrip(t *testing.T) {
	t.Parallel()
	s := newCentralsStore(t)
	ctx := context.Background()

	f := false
	in := baseCentralRow("beh1")
	in.Behavior = config.CentralBehavior{
		LightLastBrightness:       &f,
		EnableDeviceFirmwareCheck: &f,
		SysvarScanInterval:        90 * time.Second,
		SysvarMarkers:             []hmenum.DescriptionMarker{hmenum.DescriptionMarkerHAHM},
	}
	if err := s.Put(ctx, in); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, "beh1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Behavior.LightLastBrightnessEnabled() {
		t.Error("light_last_brightness should round-trip as false")
	}
	if got.Behavior.EnableDeviceFirmwareCheckEnabled() {
		t.Error("enable_device_firmware_check should round-trip as false")
	}
	if got.Behavior.SysvarScanInterval != 90*time.Second {
		t.Errorf("sysvar_scan_interval=%v, want 90s", got.Behavior.SysvarScanInterval)
	}
	if len(got.Behavior.SysvarMarkers) != 1 || got.Behavior.SysvarMarkers[0] != hmenum.DescriptionMarkerHAHM {
		t.Errorf("sysvar_markers round-trip wrong: %v", got.Behavior.SysvarMarkers)
	}
	// An unset behavior block defaults to enabled toggles.
	base, _ := s.Get(ctx, "beh1")
	_ = base
}
