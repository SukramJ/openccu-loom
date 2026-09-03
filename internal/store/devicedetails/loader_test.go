// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package devicedetails

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeClient is a test-double for jsonClientLike.
type fakeClient struct {
	deviceDetails    []map[string]any
	deviceDetailsErr error

	rooms    []rawEntry
	roomsErr error

	functions    []rawEntry
	functionsErr error

	// callCounts tracks the number of times each method was invoked.
	callCounts struct {
		details   int
		rooms     int
		functions int
	}
}

func (f *fakeClient) GetDeviceDetails(_ context.Context) ([]map[string]any, error) {
	f.callCounts.details++
	return f.deviceDetails, f.deviceDetailsErr
}

func (f *fakeClient) GetAllRoomsRaw(_ context.Context) ([]rawEntry, error) {
	f.callCounts.rooms++
	return f.rooms, f.roomsErr
}

func (f *fakeClient) GetAllFunctionsRaw(_ context.Context) ([]rawEntry, error) {
	f.callCounts.functions++
	return f.functions, f.functionsErr
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func newTestLoader(client jsonClientLike) (*Cache, *Loader) {
	c := New()
	l := NewLoader(c, client, "test-central", discardLogger())
	return c, l
}

// ─── Test: successful Load fills names, iseIDs, interfaces ────────────────────

func TestLoaderLoad_FillsNamesAndISEIDs(t *testing.T) {
	t.Parallel()

	client := &fakeClient{
		deviceDetails: []map[string]any{
			{
				"address":   "VCU1234567",
				"name":      "Wohnzimmer Heizung",
				"id":        "42",
				"interface": "HmIP-RF",
				"channels": []any{
					map[string]any{"address": "VCU1234567:0", "name": "Wohnzimmer Heizung Kanal 0", "id": "43"},
					map[string]any{"address": "VCU1234567:1", "name": "Wohnzimmer Heizung Kanal 1", "id": "44"},
				},
			},
		},
		rooms:     []rawEntry{},
		functions: []rawEntry{},
	}

	cache, loader := newTestLoader(client)
	if err := loader.Load(context.Background(), true); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Device name
	if got := cache.GetName("VCU1234567"); got != "Wohnzimmer Heizung" {
		t.Errorf("device name = %q, want Wohnzimmer Heizung", got)
	}
	// Device ISE-ID
	if got := cache.GetAddressID("VCU1234567"); got != 42 {
		t.Errorf("device ISE-ID = %d, want 42", got)
	}
	// Device interface
	if got, ok := cache.GetInterface("VCU1234567"); !ok || got != hmenum.InterfaceHmIPRF {
		t.Errorf("device interface = (%v, %t), want (HmIP-RF, true)", got, ok)
	}
	// Channel names and ISE-IDs
	if got := cache.GetName("VCU1234567:0"); got != "Wohnzimmer Heizung Kanal 0" {
		t.Errorf("ch:0 name = %q, want Wohnzimmer Heizung Kanal 0", got)
	}
	if got := cache.GetAddressID("VCU1234567:1"); got != 44 {
		t.Errorf("ch:1 ISE-ID = %d, want 44", got)
	}
}

// ─── Test: unknown interface falls back to BidCos-RF ─────────────────────────

func TestLoaderLoad_FallbackInterfaceBidCosRF(t *testing.T) {
	t.Parallel()

	client := &fakeClient{
		deviceDetails: []map[string]any{
			{
				"address":   "VCU0000001",
				"name":      "Gerät ohne Interface",
				"id":        "1",
				"interface": "UnknownInterface",
				"channels":  []any{},
			},
		},
		rooms:     []rawEntry{},
		functions: []rawEntry{},
	}

	cache, loader := newTestLoader(client)
	if err := loader.Load(context.Background(), true); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// The loader writes the fallback tag explicitly, so it is cached — ok is
	// true, which is what separates it from an address the loader never saw.
	if got, ok := cache.GetInterface("VCU0000001"); !ok || got != hmenum.InterfaceBidCosRF {
		t.Errorf("unknown interface should be cached as BidCos-RF, got (%v, %t)", got, ok)
	}
}

// ─── Test: GetAllRoomsRaw → AddChannelRoom ────────────────────────────────────

func TestLoaderLoad_RoomsAreAdded(t *testing.T) {
	t.Parallel()

	client := &fakeClient{
		deviceDetails: []map[string]any{
			{
				"address":   "VCU2000001",
				"name":      "Licht",
				"id":        "100",
				"interface": "HmIP-RF",
				"channels": []any{
					map[string]any{"address": "VCU2000001:1", "name": "Licht Kanal 1", "id": "101"},
				},
			},
		},
		rooms: []rawEntry{
			{ID: "50", Name: "Wohnzimmer", ChannelIDs: []string{"101"}},
			{ID: "51", Name: "Erdgeschoss", ChannelIDs: []string{"101"}},
		},
		functions: []rawEntry{},
	}

	cache, loader := newTestLoader(client)
	if err := loader.Load(context.Background(), true); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	rooms := cache.GetChannelRooms("VCU2000001:1")
	if len(rooms) != 2 {
		t.Fatalf("expected 2 rooms for VCU2000001:1, got %v", rooms)
	}
	if rooms[0] != "Erdgeschoss" || rooms[1] != "Wohnzimmer" {
		t.Errorf("rooms = %v, want [Erdgeschoss Wohnzimmer]", rooms)
	}
	// Device should also inherit the rooms (via AddChannelRoom → deviceRooms).
	deviceRooms := cache.GetDeviceRooms("VCU2000001")
	if len(deviceRooms) != 2 {
		t.Fatalf("device expected 2 rooms, got %v", deviceRooms)
	}
}

// ─── Test: GetAllFunctionsRaw → AddFunction ───────────────────────────────────

func TestLoaderLoad_FunctionsAreAdded(t *testing.T) {
	t.Parallel()

	client := &fakeClient{
		deviceDetails: []map[string]any{
			{
				"address":   "VCU3000001",
				"name":      "Thermostat",
				"id":        "200",
				"interface": "BidCos-RF",
				"channels": []any{
					map[string]any{"address": "VCU3000001:1", "name": "Thermostat Kanal 1", "id": "201"},
				},
			},
		},
		rooms: []rawEntry{},
		functions: []rawEntry{
			{ID: "60", Name: "Heizung", ChannelIDs: []string{"201"}},
		},
	}

	cache, loader := newTestLoader(client)
	if err := loader.Load(context.Background(), true); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	fns := cache.GetFunctions("VCU3000001:1")
	if len(fns) != 1 || fns[0] != "Heizung" {
		t.Errorf("functions = %v, want [Heizung]", fns)
	}
}

// ─── Test: cache-age gate skips reload when direct=false ─────────────────────

func TestLoaderLoad_CacheAgeGatePreventsReload(t *testing.T) {
	t.Parallel()

	client := &fakeClient{
		deviceDetails: []map[string]any{
			{
				"address": "VCU4000001", "name": "Gerät", "id": "300",
				"interface": "HmIP-RF", "channels": []any{},
			},
		},
		rooms:     []rawEntry{},
		functions: []rawEntry{},
	}

	_, loader := newTestLoader(client)

	// First call: direct=true, always loads.
	if err := loader.Load(context.Background(), true); err != nil {
		t.Fatalf("first Load() error: %v", err)
	}
	if client.callCounts.details != 1 {
		t.Fatalf("expected 1 call after direct Load, got %d", client.callCounts.details)
	}

	// Second call: direct=false, cache was just refreshed → skip.
	if err := loader.Load(context.Background(), false); err != nil {
		t.Fatalf("second Load() error: %v", err)
	}
	if client.callCounts.details != 1 {
		t.Errorf("expected 1 call total after skip-Load, got %d", client.callCounts.details)
	}
}

// ─── Test: directCall=true forces reload even when cache is fresh ─────────────

func TestLoaderLoad_DirectCallForcesReload(t *testing.T) {
	t.Parallel()

	client := &fakeClient{
		deviceDetails: []map[string]any{
			{
				"address": "VCU5000001", "name": "Gerät", "id": "400",
				"interface": "HmIP-RF", "channels": []any{},
			},
		},
		rooms:     []rawEntry{},
		functions: []rawEntry{},
	}

	cache, loader := newTestLoader(client)

	// Pre-stamp the refreshedAt to "now" — cache looks fresh.
	cache.MarkRefreshed(time.Now())

	// directCall=true must bypass the age gate and call the backend.
	if err := loader.Load(context.Background(), true); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if client.callCounts.details != 1 {
		t.Errorf("directCall=true should bypass gate; callCount = %d", client.callCounts.details)
	}
}

// ─── Test: stale cache triggers reload when direct=false ─────────────────────

func TestLoaderLoad_StaleCacheTriggerReload(t *testing.T) {
	t.Parallel()

	client := &fakeClient{
		deviceDetails: []map[string]any{
			{
				"address": "VCU6000001", "name": "Gerät", "id": "500",
				"interface": "HmIP-RF", "channels": []any{},
			},
		},
		rooms:     []rawEntry{},
		functions: []rawEntry{},
	}

	cache, loader := newTestLoader(client)

	// Stamp refreshedAt well in the past (beyond the 3 s gate).
	cache.MarkRefreshed(time.Now().Add(-1 * time.Hour))

	if err := loader.Load(context.Background(), false); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if client.callCounts.details != 1 {
		t.Errorf("stale cache should trigger reload; callCount = %d", client.callCounts.details)
	}
}

// ─── Test: GetDeviceDetails error propagated ──────────────────────────────────

func TestLoaderLoad_DeviceDetailsError(t *testing.T) {
	t.Parallel()

	errFake := errors.New("CCU unreachable")
	client := &fakeClient{
		deviceDetailsErr: errFake,
		rooms:            []rawEntry{},
		functions:        []rawEntry{},
	}

	_, loader := newTestLoader(client)
	err := loader.Load(context.Background(), true)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, errFake) {
		t.Errorf("error chain should contain errFake; got %v", err)
	}
}

// ─── Test: rooms error propagated ────────────────────────────────────────────

func TestLoaderLoad_RoomsError(t *testing.T) {
	t.Parallel()

	errFake := errors.New("rooms RPC failed")
	client := &fakeClient{
		deviceDetails: []map[string]any{},
		roomsErr:      errFake,
		functions:     []rawEntry{},
	}

	_, loader := newTestLoader(client)
	err := loader.Load(context.Background(), true)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, errFake) {
		t.Errorf("error chain should contain errFake; got %v", err)
	}
}

// ─── Test: functions error propagated ────────────────────────────────────────

func TestLoaderLoad_FunctionsError(t *testing.T) {
	t.Parallel()

	errFake := errors.New("functions RPC failed")
	client := &fakeClient{
		deviceDetails: []map[string]any{},
		rooms:         []rawEntry{},
		functionsErr:  errFake,
	}

	_, loader := newTestLoader(client)
	err := loader.Load(context.Background(), true)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, errFake) {
		t.Errorf("error chain should contain errFake; got %v", err)
	}
}

// ─── Test: a failed refresh keeps the previous generation ────────────────────

// TestLoaderLoad_FailedRefreshKeepsPreviousGeneration covers the periodic
// 5-minute refresh meeting a CCU hiccup, and the forced refresh the hot-plug
// ingest runs (whose error is only logged before the ingest proceeds). A
// device materialised while the cache is empty keeps its address-derived name
// and ISE-ID 0 for the daemon's lifetime, because the pipeline reads these
// values once, at creation.
func TestLoaderLoad_FailedRefreshKeepsPreviousGeneration(t *testing.T) {
	// Sequential on purpose: the subtests share one fake client and one cache,
	// each flipping a different round-trip to failing.
	client := &fakeClient{
		deviceDetails: []map[string]any{
			{
				"address":   "VCU1234567",
				"name":      "Wohnzimmer Heizung",
				"id":        "42",
				"interface": "HmIP-RF",
				"channels": []any{
					map[string]any{"address": "VCU1234567:1", "name": "Kanal 1", "id": "44"},
				},
			},
		},
		rooms:     []rawEntry{{ID: "1", Name: "Wohnzimmer", ChannelIDs: []string{"44"}}},
		functions: []rawEntry{{ID: "2", Name: "Heizung", ChannelIDs: []string{"44"}}},
	}

	cache, loader := newTestLoader(client)
	if err := loader.Load(context.Background(), true); err != nil {
		t.Fatalf("first Load() error: %v", err)
	}
	firstRefresh := cache.RefreshedAt()

	for _, tc := range []struct {
		name string
		fail func()
	}{
		{"device details fail", func() { client.deviceDetailsErr = errors.New("CCU unreachable") }},
		{"rooms fail", func() { client.roomsErr = errors.New("rooms RPC failed") }},
		{"functions fail", func() { client.functionsErr = errors.New("functions RPC failed") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client.deviceDetailsErr, client.roomsErr, client.functionsErr = nil, nil, nil
			tc.fail()

			if err := loader.Load(context.Background(), true); err == nil {
				t.Fatal("expected the failing round-trip to be reported")
			}
			if got := cache.GetName("VCU1234567"); got != "Wohnzimmer Heizung" {
				t.Errorf("device name = %q, want the previous generation to survive", got)
			}
			if got := cache.GetAddressID("VCU1234567:1"); got != 44 {
				t.Errorf("channel ISE-ID = %d, want 44 (previous generation)", got)
			}
			if got := cache.GetChannelRooms("VCU1234567:1"); len(got) != 1 || got[0] != "Wohnzimmer" {
				t.Errorf("channel rooms = %v, want [Wohnzimmer]", got)
			}
			if got := cache.GetFunctions("VCU1234567:1"); len(got) != 1 || got[0] != "Heizung" {
				t.Errorf("functions = %v, want [Heizung]", got)
			}
			if got := cache.RefreshedAt(); !got.Equal(firstRefresh) {
				t.Errorf("RefreshedAt = %v, want the successful load's stamp %v", got, firstRefresh)
			}
		})
	}
}

// ─── Test: MarkRefreshed is called on successful load ─────────────────────────

func TestLoaderLoad_MarkRefreshedOnSuccess(t *testing.T) {
	t.Parallel()

	client := &fakeClient{
		deviceDetails: []map[string]any{},
		rooms:         []rawEntry{},
		functions:     []rawEntry{},
	}

	cache, loader := newTestLoader(client)
	before := time.Now()
	if err := loader.Load(context.Background(), true); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	after := time.Now()

	refreshed := cache.RefreshedAt()
	if refreshed.IsZero() {
		t.Fatal("RefreshedAt should not be zero after successful load")
	}
	if refreshed.Before(before) || refreshed.After(after) {
		t.Errorf("RefreshedAt %v not in [%v, %v]", refreshed, before, after)
	}
}

// ─── parseIntStr ──────────────────────────────────────────────────────────────

func TestParseIntStr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  int64
	}{
		{"42", 42},
		{"0", 0},
		{"", 0},
		// Non-numeric character → 0.
		{"12x", 0},
		{"abc", 0},
		// Leading digit then non-digit → 0.
		{"1.5", 0},
	}
	for _, tc := range cases {
		if got := parseIntStr(tc.input); got != tc.want {
			t.Errorf("parseIntStr(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

// ─── decodeDeviceDetail ───────────────────────────────────────────────────────

func TestDecodeDeviceDetail_ChannelsTypeError(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"address":   "VCU1234",
		"name":      "test",
		"id":        "1",
		"interface": "HmIP-RF",
		// "channels" must be a JSON array; use a string to force a type error.
		"channels": "not-an-array",
	}
	// The decode should fail because "channels" is a string, not an array.
	_, err := decodeDeviceDetail(raw)
	if err == nil {
		t.Fatal("expected error when channels field has wrong type, got nil")
	}
}

// ─── Loader — empty room / function names are skipped ─────────────────────────

func TestLoaderLoad_EmptyRoomNameSkipped(t *testing.T) {
	t.Parallel()
	client := &fakeClient{
		deviceDetails: []map[string]any{
			{
				"address": "VCU_R1", "name": "Dev", "id": "10",
				"interface": "HmIP-RF",
				"channels": []any{
					map[string]any{"address": "VCU_R1:1", "name": "Ch1", "id": "11"},
				},
			},
		},
		rooms: []rawEntry{
			{ID: "50", Name: "", ChannelIDs: []string{"11"}}, // empty name → skip
		},
		functions: []rawEntry{},
	}
	cache, loader := newTestLoader(client)
	if err := loader.Load(context.Background(), true); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if rooms := cache.GetChannelRooms("VCU_R1:1"); len(rooms) != 0 {
		t.Errorf("expected no rooms for empty-name room, got %v", rooms)
	}
}

func TestLoaderLoad_EmptyFunctionNameSkipped(t *testing.T) {
	t.Parallel()
	client := &fakeClient{
		deviceDetails: []map[string]any{
			{
				"address": "VCU_F1", "name": "Dev", "id": "20",
				"interface": "HmIP-RF",
				"channels": []any{
					map[string]any{"address": "VCU_F1:1", "name": "Ch1", "id": "21"},
				},
			},
		},
		rooms: []rawEntry{},
		functions: []rawEntry{
			{ID: "60", Name: "", ChannelIDs: []string{"21"}}, // empty name → skip
		},
	}
	cache, loader := newTestLoader(client)
	if err := loader.Load(context.Background(), true); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if fns := cache.GetFunctions("VCU_F1:1"); len(fns) != 0 {
		t.Errorf("expected no functions for empty-name function, got %v", fns)
	}
}

// ─── Loader — decodeDeviceDetail error is logged and skipped (not fatal) ──────

func TestLoaderLoad_BadChannelsSkipped(t *testing.T) {
	t.Parallel()
	client := &fakeClient{
		deviceDetails: []map[string]any{
			// This entry has a non-array "channels" value — decodeDeviceDetail
			// will fail and the loader should log a warning and continue.
			{
				"address":   "VCU_BAD",
				"name":      "bad device",
				"id":        "99",
				"interface": "HmIP-RF",
				"channels":  "not-an-array",
			},
			// This entry is valid and must still be processed.
			{
				"address":   "VCU_GOOD",
				"name":      "good device",
				"id":        "100",
				"interface": "HmIP-RF",
				"channels":  []any{},
			},
		},
		rooms:     []rawEntry{},
		functions: []rawEntry{},
	}

	cache, loader := newTestLoader(client)
	if err := loader.Load(context.Background(), true); err != nil {
		t.Fatalf("Load() should not error on a bad single entry: %v", err)
	}
	// The bad entry was skipped; the good one was processed.
	if got := cache.GetName("VCU_GOOD"); got != "good device" {
		t.Errorf("VCU_GOOD name = %q, want %q", got, "good device")
	}
}
