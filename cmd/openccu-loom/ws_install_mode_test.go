// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// fakeInstallModeWriter records SetInstallMode calls for assertions.
type fakeInstallModeWriter struct {
	calls []installModeCall
	err   error // if non-nil, returned on every call
}

type installModeCall struct {
	interfaceID string
	enabled     bool
	duration    time.Duration
}

func (f *fakeInstallModeWriter) SetInstallMode(
	_ context.Context,
	interfaceID string,
	enabled bool,
	duration time.Duration,
) error {
	f.calls = append(f.calls, installModeCall{interfaceID: interfaceID, enabled: enabled, duration: duration})
	return f.err
}

// buildHubAdapter wires a Hub with the given install-mode trackers into a
// Registry / HubAdapter so wsHubQuery.hub.Hub() returns it.
func buildHubAdapter(h *hub.Hub) (*adapter.HubAdapter, *central.Registry) {
	reg := central.NewRegistry()
	cu, err := central.New(central.Config{Name: "test-ccu"})
	if err != nil {
		// Only reachable in broken test environment — panic is acceptable in
		// test helpers (per CLAUDE.md: "no bare panic from library code").
		panic("central.New: " + err.Error())
	}
	// Replace the auto-created hub with the caller-supplied one so we control
	// which InstallMode trackers are registered.
	cu.HubModel = h
	if err := reg.Register(cu); err != nil {
		panic("reg.Register: " + err.Error())
	}
	return adapter.NewHubAdapter(reg), reg
}

// TestWSHubQuery_InstallModeStatus_EmptyHub verifies that an empty hub
// (no trackers registered) returns an empty map with no error.
func TestWSHubQuery_InstallModeStatus_EmptyHub(t *testing.T) {
	t.Parallel()

	h := hub.NewHub("test-ccu")
	hubAdapter, reg := buildHubAdapter(h)
	q := &wsHubQuery{hub: hubAdapter, registry: reg}

	got, err := q.InstallModeStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty map, got %v", got)
	}
}

// TestWSHubQuery_InstallModeStatus_NilHubAdapter verifies that a HubAdapter
// backed by an empty registry (Hub() returns nil) propagates the
// "hub not available" error.
func TestWSHubQuery_InstallModeStatus_NilHubAdapter(t *testing.T) {
	t.Parallel()

	// NewRegistry with no units → Hub() returns nil.
	emptyReg := central.NewRegistry()
	hubAdapter := adapter.NewHubAdapter(emptyReg)
	q := &wsHubQuery{hub: hubAdapter, registry: emptyReg}

	_, err := q.InstallModeStatus(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); !containsStr(got, "hub not available") {
		t.Fatalf("error %q does not contain 'hub not available'", got)
	}
}

// TestWSHubQuery_InstallModeStatus_TwoInterfaces registers two trackers, drives
// one to enabled, and asserts the serialised map is correct.
func TestWSHubQuery_InstallModeStatus_TwoInterfaces(t *testing.T) {
	t.Parallel()

	h := hub.NewHub("test-ccu")

	writerA := &fakeInstallModeWriter{}
	writerB := &fakeInstallModeWriter{}
	imA := hub.NewInstallMode("HmIP-RF", writerA)
	imB := hub.NewInstallMode("BidCos-RF", writerB)
	h.PutInstallMode(imA)
	h.PutInstallMode(imB)

	// Drive HmIP-RF to enabled=true, 60 s remaining.
	imA.OnState(true, 60*time.Second)

	hubAdapter, reg := buildHubAdapter(h)
	q := &wsHubQuery{hub: hubAdapter, registry: reg}

	got, err := q.InstallModeStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %v", len(got), got)
	}

	// Assert HmIP-RF entry.
	entryA, ok := got["HmIP-RF"]
	if !ok {
		t.Fatal("missing key HmIP-RF")
	}
	mA, ok := entryA.(map[string]any) //nolint:forcetypeassert // test assertion: wrong type is a test failure
	if !ok {
		t.Fatalf("HmIP-RF value is %T, want map[string]any", entryA)
	}
	if mA["enabled"] != true {
		t.Errorf("HmIP-RF enabled: got %v, want true", mA["enabled"])
	}
	remainSecs, _ := mA["remaining_seconds"].(int) //nolint:forcetypeassert // test assertion
	if remainSecs <= 0 {
		t.Errorf("HmIP-RF remaining_seconds: got %d, want > 0", remainSecs)
	}
	if mA["observed"] != true {
		t.Errorf("HmIP-RF observed: got %v, want true", mA["observed"])
	}

	// Assert BidCos-RF entry — disabled, not yet observed.
	entryB, ok := got["BidCos-RF"]
	if !ok {
		t.Fatal("missing key BidCos-RF")
	}
	mB, ok := entryB.(map[string]any) //nolint:forcetypeassert // test assertion
	if !ok {
		t.Fatalf("BidCos-RF value is %T, want map[string]any", entryB)
	}
	if mB["enabled"] != false {
		t.Errorf("BidCos-RF enabled: got %v, want false", mB["enabled"])
	}
}

// TestWSHubQuery_EnableInstallMode_Success verifies that EnableInstallMode
// delegates correctly to the InstallModeWriter.
func TestWSHubQuery_EnableInstallMode_Success(t *testing.T) {
	t.Parallel()

	h := hub.NewHub("test-ccu")
	writer := &fakeInstallModeWriter{}
	im := hub.NewInstallMode("HmIP-RF", writer)
	h.PutInstallMode(im)

	hubAdapter, reg := buildHubAdapter(h)
	q := &wsHubQuery{hub: hubAdapter, registry: reg}

	if err := q.EnableInstallMode(context.Background(), "HmIP-RF", 60); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(writer.calls) != 1 {
		t.Fatalf("want 1 writer call, got %d", len(writer.calls))
	}
	c := writer.calls[0]
	if c.interfaceID != "HmIP-RF" {
		t.Errorf("interfaceID: got %q, want HmIP-RF", c.interfaceID)
	}
	if !c.enabled {
		t.Error("enabled: got false, want true")
	}
	if c.duration != 60*time.Second {
		t.Errorf("duration: got %v, want 60s", c.duration)
	}
}

// TestWSHubQuery_EnableInstallMode_UnknownInterface verifies that an
// unregistered interface ID returns an error containing the ID and
// "not registered".
func TestWSHubQuery_EnableInstallMode_UnknownInterface(t *testing.T) {
	t.Parallel()

	h := hub.NewHub("test-ccu")
	hubAdapter, reg := buildHubAdapter(h)
	q := &wsHubQuery{hub: hubAdapter, registry: reg}

	err := q.EnableInstallMode(context.Background(), "HmIP-RF", 60)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !containsStr(err.Error(), "HmIP-RF") {
		t.Errorf("error %q should contain interface ID", err.Error())
	}
	if !containsStr(err.Error(), "not registered") {
		t.Errorf("error %q should contain 'not registered'", err.Error())
	}
}

// TestWSHubQuery_EnableInstallMode_ZeroDuration verifies that a zero-second
// duration propagates hub.ErrInstallModeInvalidDuration.
func TestWSHubQuery_EnableInstallMode_ZeroDuration(t *testing.T) {
	t.Parallel()

	h := hub.NewHub("test-ccu")
	writer := &fakeInstallModeWriter{}
	im := hub.NewInstallMode("HmIP-RF", writer)
	h.PutInstallMode(im)

	hubAdapter, reg := buildHubAdapter(h)
	q := &wsHubQuery{hub: hubAdapter, registry: reg}

	err := q.EnableInstallMode(context.Background(), "HmIP-RF", 0)
	if !errors.Is(err, hub.ErrInstallModeInvalidDuration) {
		t.Errorf("want ErrInstallModeInvalidDuration, got %v", err)
	}
}

// TestWSHubQuery_DisableInstallMode_Success verifies that DisableInstallMode
// delegates correctly to the InstallModeWriter.
func TestWSHubQuery_DisableInstallMode_Success(t *testing.T) {
	t.Parallel()

	h := hub.NewHub("test-ccu")
	writer := &fakeInstallModeWriter{}
	im := hub.NewInstallMode("HmIP-RF", writer)
	h.PutInstallMode(im)

	hubAdapter, reg := buildHubAdapter(h)
	q := &wsHubQuery{hub: hubAdapter, registry: reg}

	if err := q.DisableInstallMode(context.Background(), "HmIP-RF"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(writer.calls) != 1 {
		t.Fatalf("want 1 writer call, got %d", len(writer.calls))
	}
	c := writer.calls[0]
	if c.interfaceID != "HmIP-RF" {
		t.Errorf("interfaceID: got %q, want HmIP-RF", c.interfaceID)
	}
	if c.enabled {
		t.Error("enabled: got true, want false")
	}
	if c.duration != 0 {
		t.Errorf("duration: got %v, want 0", c.duration)
	}
}

// TestWSHubQuery_DisableInstallMode_UnknownInterface mirrors
// TestWSHubQuery_EnableInstallMode_UnknownInterface for the Disable path.
func TestWSHubQuery_DisableInstallMode_UnknownInterface(t *testing.T) {
	t.Parallel()

	h := hub.NewHub("test-ccu")
	hubAdapter, reg := buildHubAdapter(h)
	q := &wsHubQuery{hub: hubAdapter, registry: reg}

	err := q.DisableInstallMode(context.Background(), "HmIP-RF")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !containsStr(err.Error(), "HmIP-RF") {
		t.Errorf("error %q should contain interface ID", err.Error())
	}
	if !containsStr(err.Error(), "not registered") {
		t.Errorf("error %q should contain 'not registered'", err.Error())
	}
}

// TestWSHubQuery_InstallMode_NilHub verifies that both Enable and Disable
// return "hub not available" when the registry is empty.
func TestWSHubQuery_InstallMode_NilHub(t *testing.T) {
	t.Parallel()

	emptyReg := central.NewRegistry()
	hubAdapter := adapter.NewHubAdapter(emptyReg)
	q := &wsHubQuery{hub: hubAdapter, registry: emptyReg}

	t.Run("Enable", func(t *testing.T) {
		t.Parallel()
		err := q.EnableInstallMode(context.Background(), "HmIP-RF", 60)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !containsStr(err.Error(), "hub not available") {
			t.Errorf("error %q does not contain 'hub not available'", err.Error())
		}
	})

	t.Run("Disable", func(t *testing.T) {
		t.Parallel()
		err := q.DisableInstallMode(context.Background(), "HmIP-RF")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !containsStr(err.Error(), "hub not available") {
			t.Errorf("error %q does not contain 'hub not available'", err.Error())
		}
	})
}

// containsStr reports whether s contains sub. Avoids importing strings in
// every test function body.
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || sub == "" || indexStr(s, sub) >= 0)
}

func indexStr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
