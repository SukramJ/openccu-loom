// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/store/session"
)

// buildRecorderRegistry returns a registry with one Unit (named n)
// whose Recorder is already wired via central.New (which calls
// cache.SetSessionRecorder during construction).
func buildRecorderRegistry(t *testing.T, name string) (*central.Registry, *central.Unit) {
	t.Helper()
	unit, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	return reg, unit
}

// TestRPCRecorderAdapter_StatusBeforeStart verifies that a freshly
// constructed adapter reports Active=false for every central.
func TestRPCRecorderAdapter_StatusBeforeStart(t *testing.T) {
	t.Parallel()
	reg, _ := buildRecorderRegistry(t, "OttoGo")
	a := NewRPCRecorderAdapter(reg, t.TempDir())

	statuses := a.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Active {
		t.Error("recorder should be inactive before Start")
	}
	if statuses[0].Central != "OttoGo" {
		t.Errorf("unexpected central name: %s", statuses[0].Central)
	}
}

// TestRPCRecorderAdapter_StartActivatesAndCreatesMarker verifies that
// Start sets Active=true and writes the active-marker file.
func TestRPCRecorderAdapter_StartActivatesAndCreatesMarker(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg, _ := buildRecorderRegistry(t, "OttoGo")
	a := NewRPCRecorderAdapter(reg, dir)

	a.Start(nil, 0, false)

	statuses := a.Status()
	if len(statuses) != 1 || !statuses[0].Active {
		t.Fatalf("expected Active=true after Start, got %+v", statuses)
	}
	markerPath := filepath.Join(dir, "active_rpc_recording.json")
	if _, err := os.Stat(markerPath); os.IsNotExist(err) {
		t.Error("marker file should exist after Start")
	}
}

// TestRPCRecorderAdapter_CacheFeedThrough is the critical feed-through
// test: traffic injected via unit.Cache.RecordSession must appear in the
// recorder's entries after Start.
func TestRPCRecorderAdapter_CacheFeedThrough(t *testing.T) {
	t.Parallel()
	reg, unit := buildRecorderRegistry(t, "OttoGo")
	a := NewRPCRecorderAdapter(reg, t.TempDir())

	a.Start(nil, 0, false)

	// Inject a call/response pair through the cache coordinator.
	unit.Cache.RecordSession(
		session.RPCTypeXML,
		"listDevices",
		[]string{},
		[]map[string]any{{"address": "ABC123", "type": "HmIP-STH"}},
	)

	statuses := a.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status entry, got %d", len(statuses))
	}
	if statuses[0].Entries == 0 {
		t.Error("expected Entries > 0 after cache feed-through; recorder did not capture the call")
	}

	exported, ok := a.Export("OttoGo", "map")
	if !ok {
		t.Fatal("Export returned ok=false for known central")
	}
	m, ok := exported.(map[string]any)
	if !ok {
		t.Fatalf("Export returned type %T, want map[string]any", exported)
	}
	if len(m) == 0 {
		t.Error("exported map is empty; recorded slot was not serialised")
	}
}

// TestRPCRecorderAdapter_StopDeactivatesAndRemovesMarker verifies that
// Stop sets Active=false and removes the marker file.
func TestRPCRecorderAdapter_StopDeactivatesAndRemovesMarker(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg, _ := buildRecorderRegistry(t, "OttoGo")
	a := NewRPCRecorderAdapter(reg, dir)

	a.Start(nil, 0, false)
	a.Stop(nil)

	statuses := a.Status()
	if len(statuses) != 1 || statuses[0].Active {
		t.Fatalf("expected Active=false after Stop, got %+v", statuses)
	}
	markerPath := filepath.Join(dir, "active_rpc_recording.json")
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Error("marker file should be removed after Stop")
	}
}

// TestRPCRecorderAdapter_ExportUnknownCentral verifies Export returns
// ok=false for a central that is not in the registry.
func TestRPCRecorderAdapter_ExportUnknownCentral(t *testing.T) {
	t.Parallel()
	reg, _ := buildRecorderRegistry(t, "OttoGo")
	a := NewRPCRecorderAdapter(reg, t.TempDir())

	_, ok := a.Export("ghost-ccu", "map")
	if ok {
		t.Error("Export should return ok=false for unknown central")
	}
}

// TestRPCRecorderAdapter_ResumeFromMarker verifies that a fresh adapter
// constructed over the same registry+dir resumes an active recording that
// was persisted as a marker by a previous adapter.
func TestRPCRecorderAdapter_ResumeFromMarker(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg, _ := buildRecorderRegistry(t, "OttoGo")

	// First adapter: start recording and leave the marker in place.
	a1 := NewRPCRecorderAdapter(reg, dir)
	a1.Start(nil, 0, false)

	// Verify the marker was written before we construct the second adapter.
	markerPath := filepath.Join(dir, "active_rpc_recording.json")
	raw, err := os.ReadFile(markerPath) //nolint:gosec // test, fixed path
	if err != nil {
		t.Fatalf("marker not written by first adapter: %v", err)
	}
	var st struct {
		Centrals []string `json:"centrals"`
	}
	if json.Unmarshal(raw, &st) != nil {
		t.Fatal("marker is not valid JSON")
	}

	// Stop the first adapter so the recorder is inactive.
	a1.Stop(nil)

	// Write the marker back manually so ResumeFromMarker sees it
	// (Stop would have removed it; we are simulating a daemon restart where
	// the file survived because Stop was never called).
	// Use the current time so the deadline (now + 60min cap) is in the future.
	markerContent, _ := json.Marshal(struct {
		Centrals        []string `json:"centrals"`
		StartedAt       int64    `json:"started_at"`
		DurationSeconds int64    `json:"duration_seconds"`
		Randomize       bool     `json:"randomize"`
	}{Centrals: nil, StartedAt: time.Now().Unix(), DurationSeconds: 3600, Randomize: false})
	if writeErr := os.WriteFile(markerPath, markerContent, 0o600); writeErr != nil {
		t.Fatalf("could not write marker: %v", writeErr)
	}

	// Second adapter over the same registry + dir: simulates daemon restart.
	a2 := NewRPCRecorderAdapter(reg, dir)
	resumed := a2.ResumeFromMarker(context.Background())
	// resumed may be nil (centrals=nil in marker means "all"), but the
	// recorder must be active again.
	_ = resumed

	statuses := a2.Status()
	if len(statuses) != 1 || !statuses[0].Active {
		t.Fatalf("expected Active=true after ResumeFromMarker, got %+v", statuses)
	}
}

// TestRPCRecorderAdapter_StartScopedToCentral verifies that Start with a
// specific central name only activates that central's recorder.
func TestRPCRecorderAdapter_StartScopedToCentral(t *testing.T) {
	t.Parallel()
	reg, _ := buildRecorderRegistry(t, "alpha")

	unitBeta, err := central.New(central.Config{Name: "beta"})
	if err != nil {
		t.Fatalf("central.New beta: %v", err)
	}
	if err := reg.Register(unitBeta); err != nil {
		t.Fatalf("register beta: %v", err)
	}

	a := NewRPCRecorderAdapter(reg, t.TempDir())
	a.Start([]string{"alpha"}, 0, false)

	for _, s := range a.Status() {
		if s.Central == "alpha" && !s.Active {
			t.Error("alpha should be active")
		}
		if s.Central == "beta" && s.Active {
			t.Error("beta should remain inactive when Start was scoped to alpha")
		}
	}
}

// ---------------------------------------------------------------------------
// effectiveDuration
// ---------------------------------------------------------------------------

// TestEffectiveDuration_ZeroClampsToCap verifies that a zero duration
// (open recording) resolves to the maximum cap.
func TestEffectiveDuration_ZeroClampsToCap(t *testing.T) {
	t.Parallel()
	got := effectiveDuration(0)
	if got != rpcRecordingMaxDuration {
		t.Errorf("effectiveDuration(0)=%v want %v", got, rpcRecordingMaxDuration)
	}
}

// TestEffectiveDuration_NegativeClampsToCap verifies that a negative
// duration also resolves to the cap.
func TestEffectiveDuration_NegativeClampsToCap(t *testing.T) {
	t.Parallel()
	got := effectiveDuration(-10)
	if got != rpcRecordingMaxDuration {
		t.Errorf("effectiveDuration(-10)=%v want %v", got, rpcRecordingMaxDuration)
	}
}

// TestEffectiveDuration_ValidSeconds verifies that a value within the cap
// is returned as-is.
func TestEffectiveDuration_ValidSeconds(t *testing.T) {
	t.Parallel()
	got := effectiveDuration(30)
	if got != 30*time.Second {
		t.Errorf("effectiveDuration(30)=%v want 30s", got)
	}
	got600 := effectiveDuration(600)
	if got600 != 600*time.Second {
		t.Errorf("effectiveDuration(600)=%v want 600s", got600)
	}
}

// TestEffectiveDuration_AboveCapClamps verifies that a duration above
// the 3600-second cap is clamped to the cap.
func TestEffectiveDuration_AboveCapClamps(t *testing.T) {
	t.Parallel()
	got := effectiveDuration(9999)
	if got != rpcRecordingMaxDuration {
		t.Errorf("effectiveDuration(9999)=%v want %v (cap)", got, rpcRecordingMaxDuration)
	}
}

// ---------------------------------------------------------------------------
// Auto-stop timer
// ---------------------------------------------------------------------------

// TestRPCRecorderAdapter_AutoStop verifies that a 1-second recording is
// automatically stopped after the timer fires. This is the only timing-
// sensitive test; it uses a tight 1.2-second window.
func TestRPCRecorderAdapter_AutoStop(t *testing.T) {
	// Not run in parallel: it sleeps.
	dir := t.TempDir()
	reg, _ := buildRecorderRegistry(t, "OttoGo")
	a := NewRPCRecorderAdapter(reg, dir)

	a.Start(nil, 1, false) // 1-second duration

	// Wait long enough for the auto-stop timer to fire.
	time.Sleep(1200 * time.Millisecond)

	statuses := a.Status()
	if len(statuses) != 1 || statuses[0].Active {
		t.Errorf("expected Active=false after auto-stop, got %+v", statuses)
	}
	// Marker file must be gone after auto-stop.
	markerPath := filepath.Join(dir, "active_rpc_recording.json")
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Error("marker file should be removed by auto-stop timer")
	}
}

// ---------------------------------------------------------------------------
// Anonymise / randomize
// ---------------------------------------------------------------------------

// TestRPCRecorderAdapter_RandomizeAnonymisesAddresses verifies that when a
// recording is started with randomize=true, the response values in the
// exported trace have Homematic device addresses replaced with hashed tokens
// while the channel suffix is preserved. The anonymiser operates on entry
// values (params field, response field), not on the keyed slot string.
func TestRPCRecorderAdapter_RandomizeAnonymisesAddresses(t *testing.T) {
	t.Parallel()
	reg, unit := buildRecorderRegistry(t, "OttoGo")
	a := NewRPCRecorderAdapter(reg, t.TempDir())

	a.Start(nil, 0, true)

	const addr = "00021BE9957782"
	unit.Cache.RecordSession(
		session.RPCTypeXML,
		"getValue",
		[]any{addr + ":4", "STATE"},
		map[string]any{"ADDRESS": addr + ":4"},
	)

	out, ok := a.Export("OttoGo", "map")
	if !ok {
		t.Fatal("Export returned ok=false")
	}
	// The export is map[slotKey]entryMap. Inspect the entry values.
	outMap, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("Export type %T want map[string]any", out)
	}
	// The slot KEY ("rpc_type|method|frozen_params") embeds the address too;
	// it must be anonymised, not just the value fields.
	for k := range outMap {
		if contains(k, addr) {
			t.Errorf("anonymised slot key must not contain literal address %q, got key: %s", addr, k)
		}
	}
	// Extract the single entry's response field.
	var entryMap map[string]any
	for _, v := range outMap {
		entryMap, _ = v.(map[string]any)
	}
	if entryMap == nil {
		t.Fatal("no entry map found in export")
	}
	// The params field inside the entry carries the frozen address too.
	if pv, hasParams := entryMap["params"]; hasParams {
		if ps, isStr := pv.(string); isStr && contains(ps, addr) {
			t.Errorf("anonymised params must not contain literal address %q, got: %s", addr, ps)
		}
	}
	responseVal, hasResp := entryMap["response"]
	if !hasResp {
		t.Fatal("entry missing response field")
	}
	raw, err := json.Marshal(responseVal)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	s := string(raw)
	if contains(s, addr) {
		t.Errorf("anonymised response must not contain literal address %q, got: %s", addr, s)
	}
	if !contains(s, "anon:") {
		t.Errorf("anonymised response must contain anon: prefix, got: %s", s)
	}
	// Channel suffix :4 must survive.
	if !contains(s, ":4") {
		t.Errorf("anonymised response must preserve channel suffix :4, got: %s", s)
	}
}

// TestRPCRecorderAdapter_NoRandomizeKeepsAddresses verifies that a
// recording started without randomize keeps address values in clear text.
func TestRPCRecorderAdapter_NoRandomizeKeepsAddresses(t *testing.T) {
	t.Parallel()
	reg, unit := buildRecorderRegistry(t, "OttoGo")
	a := NewRPCRecorderAdapter(reg, t.TempDir())

	a.Start(nil, 0, false)

	const addr = "00021BE9957782"
	unit.Cache.RecordSession(
		session.RPCTypeXML,
		"getValue",
		[]any{addr + ":4", "STATE"},
		map[string]any{"ADDRESS": addr + ":4"},
	)

	out, ok := a.Export("OttoGo", "map")
	if !ok {
		t.Fatal("Export returned ok=false")
	}
	outMap, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("Export type %T want map[string]any", out)
	}
	var entryMap map[string]any
	for _, v := range outMap {
		entryMap, _ = v.(map[string]any)
	}
	if entryMap == nil {
		t.Fatal("no entry map found in export")
	}
	raw, err := json.Marshal(entryMap["response"])
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if !contains(string(raw), addr) {
		t.Errorf("non-randomized export must keep literal address %q in response", addr)
	}
}

// ---------------------------------------------------------------------------
// Golden format via adapter
// ---------------------------------------------------------------------------

// TestRPCRecorderAdapter_ExportGoldenFormat verifies that Export with
// format="golden" returns a []session.GoldenRecord.
func TestRPCRecorderAdapter_ExportGoldenFormat(t *testing.T) {
	t.Parallel()
	reg, unit := buildRecorderRegistry(t, "OttoGo")
	a := NewRPCRecorderAdapter(reg, t.TempDir())

	a.Start(nil, 0, false)
	unit.Cache.RecordSession(session.RPCTypeXML, "listDevices", []string{}, []any{"dev1"})

	out, ok := a.Export("OttoGo", "golden")
	if !ok {
		t.Fatal("Export returned ok=false")
	}
	records, ok := out.([]session.GoldenRecord)
	if !ok {
		t.Fatalf("Export(golden) returned type %T, want []session.GoldenRecord", out)
	}
	if len(records) == 0 {
		t.Error("expected at least one golden record, got 0")
	}
}

// ---------------------------------------------------------------------------
// Status: EndsAt and Randomize fields
// ---------------------------------------------------------------------------

// TestRPCRecorderAdapter_StatusCarriesEndsAt verifies that Status reports a
// non-empty EndsAt (RFC3339) while a bounded recording is active.
func TestRPCRecorderAdapter_StatusCarriesEndsAt(t *testing.T) {
	t.Parallel()
	reg, _ := buildRecorderRegistry(t, "OttoGo")
	a := NewRPCRecorderAdapter(reg, t.TempDir())

	a.Start(nil, 60, false) // 60-second bounded recording

	statuses := a.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].EndsAt == "" {
		t.Error("EndsAt must be set while a timed recording is active")
	}
	// Must be parseable RFC3339.
	_, err := time.Parse(time.RFC3339, statuses[0].EndsAt)
	if err != nil {
		t.Errorf("EndsAt=%q is not valid RFC3339: %v", statuses[0].EndsAt, err)
	}
}

// TestRPCRecorderAdapter_StatusCarriesRandomize verifies that Status
// reflects the Randomize flag set at Start time.
func TestRPCRecorderAdapter_StatusCarriesRandomize(t *testing.T) {
	t.Parallel()
	reg, _ := buildRecorderRegistry(t, "OttoGo")
	a := NewRPCRecorderAdapter(reg, t.TempDir())

	a.Start(nil, 0, true)

	statuses := a.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if !statuses[0].Randomize {
		t.Error("Status.Randomize must be true when started with randomize=true")
	}
}

// ---------------------------------------------------------------------------
// ResumeFromMarker: fresh-deadline and expired-deadline cases
// ---------------------------------------------------------------------------

// TestRPCRecorderAdapter_ResumeFromMarker_FreshDeadline verifies that a
// marker whose deadline is in the future re-activates the recorder on a
// fresh adapter.
func TestRPCRecorderAdapter_ResumeFromMarker_FreshDeadline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg, _ := buildRecorderRegistry(t, "OttoGo")

	// Write a marker with a far-future deadline.
	markerPath := filepath.Join(dir, "active_rpc_recording.json")
	markerContent, _ := json.Marshal(markerState{
		Centrals:        nil,
		StartedAt:       time.Now().Unix(),
		DurationSeconds: 3600, // 1 hour
		Randomize:       false,
	})
	if err := os.WriteFile(markerPath, markerContent, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	a := NewRPCRecorderAdapter(reg, dir)
	a.ResumeFromMarker(context.Background())

	statuses := a.Status()
	if len(statuses) != 1 || !statuses[0].Active {
		t.Fatalf("expected Active=true after resume with fresh deadline, got %+v", statuses)
	}
}

// TestRPCRecorderAdapter_ResumeFromMarker_ExpiredDeadline verifies that a
// marker whose deadline has already passed is removed without activating
// the recorder.
func TestRPCRecorderAdapter_ResumeFromMarker_ExpiredDeadline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg, _ := buildRecorderRegistry(t, "OttoGo")

	// Write a marker whose deadline has already passed: started 10 seconds
	// ago with only a 5-second duration.
	markerPath := filepath.Join(dir, "active_rpc_recording.json")
	markerContent, _ := json.Marshal(markerState{
		Centrals:        []string{"OttoGo"},
		StartedAt:       time.Now().Add(-10 * time.Second).Unix(),
		DurationSeconds: 5,
		Randomize:       false,
	})
	if err := os.WriteFile(markerPath, markerContent, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	a := NewRPCRecorderAdapter(reg, dir)
	resumed := a.ResumeFromMarker(context.Background())

	if resumed != nil {
		t.Errorf("ResumeFromMarker with expired deadline must return nil, got %v", resumed)
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Error("expired marker must be removed by ResumeFromMarker")
	}
	statuses := a.Status()
	if len(statuses) != 1 || statuses[0].Active {
		t.Errorf("recorder must remain inactive after expired marker, got %+v", statuses)
	}
}

// contains reports whether substr is present in s.
func contains(s, substr string) bool {
	if substr == "" {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
