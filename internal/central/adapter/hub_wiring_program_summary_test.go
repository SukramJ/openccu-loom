// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestTruncateRuleSummary verifies the display cap keeps short summaries
// verbatim and truncates long ones on a rune boundary with an ellipsis.
func TestTruncateRuleSummary(t *testing.T) {
	t.Parallel()

	short := "Wohnzimmer >= 20.00"
	if got := truncateRuleSummary(short); got != short {
		t.Errorf("short summary altered: got %q, want %q", got, short)
	}

	// Exactly at the cap is preserved.
	exact := strings.Repeat("a", ruleSummaryMaxRunes)
	if got := truncateRuleSummary(exact); got != exact {
		t.Errorf("at-cap summary altered: len(got)=%d, want %d", utf8.RuneCountInString(got), ruleSummaryMaxRunes)
	}

	// Over the cap is truncated to cap runes plus the ellipsis.
	long := strings.Repeat("b", ruleSummaryMaxRunes+50)
	got := truncateRuleSummary(long)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated summary must end with an ellipsis, got %q", got)
	}
	if n := utf8.RuneCountInString(got); n != ruleSummaryMaxRunes+1 {
		t.Errorf("truncated rune count = %d, want %d (cap + ellipsis)", n, ruleSummaryMaxRunes+1)
	}

	// Multibyte runes must not be split mid-character.
	multibyte := strings.Repeat("ä", ruleSummaryMaxRunes+10)
	gm := truncateRuleSummary(multibyte)
	if !utf8.ValidString(gm) {
		t.Error("truncation split a multibyte rune (invalid UTF-8)")
	}
	if n := utf8.RuneCountInString(gm); n != ruleSummaryMaxRunes+1 {
		t.Errorf("multibyte truncated rune count = %d, want %d", n, ruleSummaryMaxRunes+1)
	}
}

// TestProgramMetadataNilRunner verifies a nil runner yields an empty map
// (no summaries) rather than panicking — the path taken when a central has
// no ReGa runner wired.
func TestProgramMetadataNilRunner(t *testing.T) {
	t.Parallel()
	if got := programMetadata(context.Background(), nil); len(got) != 0 {
		t.Errorf("nil runner must yield empty metadata, got %d entries", len(got))
	}
}

// programSummaryServer serves Program.getAll (one fixed program, "p1") plus
// the get_program_descriptions ReGa script for loadPrograms/programMetadata
// wiring tests. The ReGa result is swappable via regaResult so a refresh
// scenario (updated rule summary, degraded script) can be exercised against
// the same programs list.
type programSummaryServer struct {
	srv        *httptest.Server
	regaResult atomic.Value // string: raw script stdout (a JSON array)
}

func newProgramSummaryServer(t *testing.T, regaOut string) *programSummaryServer {
	t.Helper()
	s := &programSummaryServer{}
	s.regaResult.Store(regaOut)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var result any
		switch req["method"] {
		case "Program.getAll":
			result = []map[string]any{
				{"id": "p1", "name": "Heater", "isActive": true, "isInternal": false},
			}
		case "ReGa.runScript":
			result, _ = s.regaResult.Load().(string)
		default:
			result = nil
		}
		resp, _ := json.Marshal(map[string]any{"result": result, "error": nil})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func newProgramSummaryRunner(t *testing.T, srv *httptest.Server) (*jsonrpc.Client, *rega.Runner) {
	t.Helper()
	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	runner, err := rega.NewRunner(rega.Config{Client: jc})
	if err != nil {
		t.Fatalf("rega.NewRunner: %v", err)
	}
	return jc, runner
}

// TestProgramMetadataHappyPath verifies programMetadata decodes the
// URL-encoded description and rule summaries emitted by the
// get_program_descriptions ReGa script, including a multibyte channel name.
func TestProgramMetadataHappyPath(t *testing.T) {
	t.Parallel()
	s := newProgramSummaryServer(t, `[{"id":"p1","description":"HAHM%20lamp",`+
		`"condition_summary":"Wohnzimmer%20%3E%3D%2020.00",`+
		`"activity_summary":"B%C3%BCcherregal%20%3A%3D%201.00"}]`)
	_, runner := newProgramSummaryRunner(t, s.srv)

	meta := programMetadata(context.Background(), runner)
	m, ok := meta["p1"]
	if !ok {
		t.Fatal("expected metadata for program p1")
	}
	if m.description != "HAHM lamp" {
		t.Errorf("description = %q, want %q", m.description, "HAHM lamp")
	}
	if m.conditionSummary != "Wohnzimmer >= 20.00" {
		t.Errorf("conditionSummary = %q, want %q", m.conditionSummary, "Wohnzimmer >= 20.00")
	}
	if m.activitySummary != "Bücherregal := 1.00" {
		t.Errorf("activitySummary = %q, want %q", m.activitySummary, "Bücherregal := 1.00")
	}
}

// TestProgramMetadataCCUErrorReturnsEmptyMap verifies a failed ReGa script
// run (unparsable output, as a degraded CCU-side script would produce)
// degrades to an empty metadata map instead of propagating an error —
// loadPrograms falls back to blank descriptions and summaries rather than
// aborting the whole scan.
func TestProgramMetadataCCUErrorReturnsEmptyMap(t *testing.T) {
	t.Parallel()
	s := newProgramSummaryServer(t, "not json")
	_, runner := newProgramSummaryRunner(t, s.srv)

	meta := programMetadata(context.Background(), runner)
	if len(meta) != 0 {
		t.Errorf("expected empty metadata on script failure, got %d entries", len(meta))
	}
}

// TestLoadProgramsRuleSummaryUnconditionalNewProgram verifies a newly loaded
// program without any program markers configured gets its rule summary
// applied unconditionally, while the Description field stays gated blank —
// matching the prior marker-only behaviour of the plain description.
func TestLoadProgramsRuleSummaryUnconditionalNewProgram(t *testing.T) {
	t.Parallel()
	s := newProgramSummaryServer(t, `[{"id":"p1","description":"HAHM%20lamp",`+
		`"condition_summary":"Wohnzimmer%20%3E%3D%2020.00",`+
		`"activity_summary":"B%C3%BCcherregal%20%3A%3D%201.00"}]`)
	jc, runner := newProgramSummaryRunner(t, s.srv)

	h := hub.NewHub("c")
	if err := loadPrograms(context.Background(), jc, runner, h, &noopProgramWriter{},
		hubScanOptions{enableProgramScan: true}); err != nil {
		t.Fatalf("loadPrograms: %v", err)
	}
	prog, ok := h.Program("p1")
	if !ok {
		t.Fatal("program p1 not loaded")
	}
	if prog.Description != "" {
		t.Errorf("Description = %q, want empty without markers configured", prog.Description)
	}
	if cond, act := prog.RuleSummary(); cond != "Wohnzimmer >= 20.00" || act != "Bücherregal := 1.00" {
		t.Errorf("RuleSummary = (%q, %q), want (%q, %q)", cond, act, "Wohnzimmer >= 20.00", "Bücherregal := 1.00")
	}
	if prog.EnabledDefault {
		t.Error("EnabledDefault should be false without markers configured")
	}
}

// TestLoadProgramsDescriptionExposedWhenMarkerConfiguredAtCreation verifies
// a program first loaded while program markers are already configured
// surfaces the decoded description and is enabled by default once its
// description matches a marker.
func TestLoadProgramsDescriptionExposedWhenMarkerConfiguredAtCreation(t *testing.T) {
	t.Parallel()
	s := newProgramSummaryServer(t, `[{"id":"p1","description":"HAHM%20lamp",`+
		`"condition_summary":"","activity_summary":""}]`)
	jc, runner := newProgramSummaryRunner(t, s.srv)

	h := hub.NewHub("c")
	if err := loadPrograms(context.Background(), jc, runner, h, &noopProgramWriter{},
		hubScanOptions{
			enableProgramScan: true,
			programMarkers:    []hmenum.DescriptionMarker{hmenum.DescriptionMarkerHAHM},
		}); err != nil {
		t.Fatalf("loadPrograms: %v", err)
	}
	prog, ok := h.Program("p1")
	if !ok {
		t.Fatal("program p1 not loaded")
	}
	if prog.Description != "HAHM lamp" {
		t.Errorf("Description = %q, want %q", prog.Description, "HAHM lamp")
	}
	if !prog.EnabledDefault {
		t.Error("EnabledDefault should be true once the description matches the configured marker")
	}
}

// TestLoadProgramsRefreshUpdatesRuleSummaryOnExistingPointer verifies a
// refresh scan keeps the same Program pointer (so MQTT/REST subscribers
// wired via OnUpdate remain valid) and applies the new rule summary and
// EnabledDefault in place.
//
// Description is deliberately NOT asserted to change here: unlike
// EnabledDefault and the rule summary, [Program.UpdateMetadata] does not
// touch Description, so an already-loaded program's Description stays at
// whatever it was set to on creation regardless of a later marker
// reconfiguration or CCU-side description edit. This differs from the
// sysvar scan, where upsertSysvar re-applies the decoded description on
// every refresh.
func TestLoadProgramsRefreshUpdatesRuleSummaryOnExistingPointer(t *testing.T) {
	t.Parallel()
	s := newProgramSummaryServer(t, `[{"id":"p1","description":"HAHM%20lamp",`+
		`"condition_summary":"Wohnzimmer%20%3E%3D%2020.00",`+
		`"activity_summary":"B%C3%BCcherregal%20%3A%3D%201.00"}]`)
	jc, runner := newProgramSummaryRunner(t, s.srv)

	h := hub.NewHub("c")
	if err := loadPrograms(context.Background(), jc, runner, h, &noopProgramWriter{},
		hubScanOptions{enableProgramScan: true}); err != nil {
		t.Fatalf("loadPrograms (initial): %v", err)
	}
	prog, ok := h.Program("p1")
	if !ok {
		t.Fatal("program p1 not loaded")
	}

	// Refresh with markers now configured and an updated rule summary.
	s.regaResult.Store(`[{"id":"p1","description":"HAHM%20lamp",` +
		`"condition_summary":"Flur%20%3D%3D%201.00",` +
		`"activity_summary":"B%C3%BCcherregal%20%3A%3D%200.00"}]`)
	if err := loadPrograms(context.Background(), jc, runner, h, &noopProgramWriter{},
		hubScanOptions{
			enableProgramScan: true,
			programMarkers:    []hmenum.DescriptionMarker{hmenum.DescriptionMarkerHAHM},
		}); err != nil {
		t.Fatalf("loadPrograms (refresh): %v", err)
	}
	refreshed, ok := h.Program("p1")
	if !ok {
		t.Fatal("program p1 missing after refresh")
	}
	if refreshed != prog {
		t.Error("refresh should update the existing Program pointer in place, not replace it")
	}
	if !refreshed.EnabledDefault {
		t.Error("EnabledDefault should be true once the description matches the configured marker")
	}
	if cond, act := refreshed.RuleSummary(); cond != "Flur == 1.00" || act != "Bücherregal := 0.00" {
		t.Errorf("refreshed RuleSummary = (%q, %q), want (%q, %q)", cond, act, "Flur == 1.00", "Bücherregal := 0.00")
	}
}

// TestLoadProgramsSeedsLastExecutionFromCCU pins where a program's last
// execution comes from. Most CCU programs run on their own schedule, so
// the daemon never observes the run; without the seed the model reports
// "never executed" for every one of them and last_executed is absent from
// REST, MCP and the MQTT program payload until the daemon itself triggers
// the program. The CCU reports the instant as a Unix epoch through the
// description script (its Program.getAll counterpart is a zone-less local
// wall-clock string).
func TestLoadProgramsSeedsLastExecutionFromCCU(t *testing.T) {
	t.Parallel()
	const epoch = 1786970913
	s := newProgramSummaryServer(t, `[{"id":"p1","description":"",`+
		`"condition_summary":"","activity_summary":"","last_execute_seconds":1786970913}]`)
	jc, runner := newProgramSummaryRunner(t, s.srv)

	h := hub.NewHub("c")
	if err := loadPrograms(context.Background(), jc, runner, h, &noopProgramWriter{},
		hubScanOptions{enableProgramScan: true}); err != nil {
		t.Fatalf("loadPrograms: %v", err)
	}
	prog, ok := h.Program("p1")
	if !ok {
		t.Fatal("program p1 not loaded")
	}
	ts, has := prog.LastExecution()
	if !has {
		t.Fatal("LastExecution not recorded for a program the CCU has run")
	}
	if want := time.Unix(epoch, 0).UTC(); !ts.Equal(want) {
		t.Fatalf("LastExecution = %s, want %s", ts, want)
	}
	// The same instant in the RFC 3339 form the north-bound surfaces publish.
	if got, wantStr := prog.LastExecuteTimeString(), time.Unix(epoch, 0).UTC().Format(time.RFC3339); got != wantStr {
		t.Fatalf("LastExecuteTimeString = %q, want %q", got, wantStr)
	}
}

// TestLoadProgramsNeverExecutedStaysEmpty verifies the CCU's "never ran"
// answer (epoch 0, which its own JSON API renders as 1970-01-01) does not
// become a 1970 timestamp on the north-bound surfaces.
func TestLoadProgramsNeverExecutedStaysEmpty(t *testing.T) {
	t.Parallel()
	s := newProgramSummaryServer(t, `[{"id":"p1","description":"",`+
		`"condition_summary":"","activity_summary":"","last_execute_seconds":0}]`)
	jc, runner := newProgramSummaryRunner(t, s.srv)

	h := hub.NewHub("c")
	if err := loadPrograms(context.Background(), jc, runner, h, &noopProgramWriter{},
		hubScanOptions{enableProgramScan: true}); err != nil {
		t.Fatalf("loadPrograms: %v", err)
	}
	prog, ok := h.Program("p1")
	if !ok {
		t.Fatal("program p1 not loaded")
	}
	if ts, has := prog.LastExecution(); has {
		t.Fatalf("LastExecution = %s, want none for a program that never ran", ts)
	}
	if got := prog.LastExecuteTimeString(); got != "" {
		t.Fatalf("LastExecuteTimeString = %q, want empty", got)
	}
}

// TestLoadProgramsSeedNeverWalksBackAnObservedExecution verifies the seed
// only moves the timestamp forward: a run the daemon performed after the
// CCU's reported one must survive the next refresh, which still carries
// the older CCU value.
func TestLoadProgramsSeedNeverWalksBackAnObservedExecution(t *testing.T) {
	t.Parallel()
	s := newProgramSummaryServer(t, `[{"id":"p1","description":"",`+
		`"condition_summary":"","activity_summary":"","last_execute_seconds":1786970913}]`)
	jc, runner := newProgramSummaryRunner(t, s.srv)

	h := hub.NewHub("c")
	opts := hubScanOptions{enableProgramScan: true}
	if err := loadPrograms(context.Background(), jc, runner, h, &noopProgramWriter{}, opts); err != nil {
		t.Fatalf("loadPrograms (initial): %v", err)
	}
	prog, ok := h.Program("p1")
	if !ok {
		t.Fatal("program p1 not loaded")
	}
	prog.OnExecution(true, hmenum.ProgramTriggerAPI)
	observed, has := prog.LastExecution()
	if !has {
		t.Fatal("OnExecution did not record a timestamp")
	}

	if err := loadPrograms(context.Background(), jc, runner, h, &noopProgramWriter{}, opts); err != nil {
		t.Fatalf("loadPrograms (refresh): %v", err)
	}
	after, _ := prog.LastExecution()
	if !after.Equal(observed) {
		t.Fatalf("refresh walked the last execution back to %s, want %s", after, observed)
	}
}
