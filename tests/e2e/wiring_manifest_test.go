// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// expectedWiringSeams are the per-central seams a booted daemon must
// declare (ADR 0065). Each name is the stable identifier its wiring code
// registers, not the Go function that registers it.
//
// The list is the point. A seam that stops being wired — the call
// deleted, skipped by a nil guard, or never reached — disappears from a
// running daemon's answer, and nothing else about the daemon changes:
// it starts, reports healthy, and serves every endpoint. That is the
// class of defect two full-codebase audits kept finding in the
// composition root, and the reason it survived is that "is X wired" was
// only ever answerable by reading.
var expectedWiringSeams = []string{
	"audit.incident_recorder",
	"client.value_writer_hooks",
	"config.assembler",
	"mqtt.hub_ready_restart",
	"mqtt.retained_orphan_sweep",
	"audit.program_execute",
	"audit.session_recorder_persistence",
	"jobs.scheduled_backup",
	"mqtt.system_status",
	"rest.system_status_buffer",
	"secret.config_store_crypto",
	"store.channel_flags_eviction",
	"store.master_values_eviction",
	"store.values_cache_eviction",
	"store.values_cache_flush",
	"visibility.un_ignore",
	"ws.device_lifecycle",
	"ws.device_trigger",
	"ws.hub_events",
	"ws.optimistic_rollback",
	"ws.system_status",
}

// configGatedWiringSeams are the seams a daemon wires only when the
// operator has switched the feature on. They are named here rather than
// omitted so the list above reads as a decision instead of an oversight,
// and they are deliberately not asserted absent: this harness may grow
// either option later, and a test that fails when a feature is *enabled*
// teaches the wrong lesson.
//
// That the manifest distinguishes these two groups at all is the point
// of serving it: "this deployment did not wire the history recorder"
// used to be answerable only by reading the config next to the code.
var configGatedWiringSeams = map[string]string{
	"history.recorder":       "persistence.history.enabled",
	"webhook.outbound":       "north.webhook.outbound",
	"security.index_refresh": "the alarm and security services (both need the persistence tier)",
}

// expectedOrderedSeams are the once-only seams whose position in the
// boot sequence is load-bearing, with the marks they declare.
//
// These are the constraints that used to live in prose hundreds of lines
// from the step they talk about. `webhook.alarm_bus` is the worked
// example: Outbound.Start reads the bus once and subscribes, so a bus
// handed over after the north bridges start is stored and never read —
// the setter reports nothing, the daemon looks healthy, and no alarm
// event is ever forwarded.
var expectedOrderedSeams = map[string]struct{ before, after []string }{
	"webhook.alarm_bus":            {before: []string{"northbridges.started"}},
	"webhook.security_bus":         {before: []string{"northbridges.started"}},
	"backup.cache_invalidator":     {after: []string{"southbound.wired"}},
	"central.devices_created_gate": {before: []string{"centrals.started"}},
	"jobs.standard_per_central":    {before: []string{"centrals.started"}},
	"mqtt.channel_hidden_gate":     {before: []string{"mqtt.supervisor.started"}},
}

// wiringSeam mirrors the GET /diagnostics/wiring element shape.
type wiringSeam struct {
	Name         string   `json:"name"`
	Collaborator string   `json:"collaborator"`
	Phase        string   `json:"phase"`
	Before       []string `json:"before"`
	After        []string `json:"after"`
	Why          string   `json:"why"`
	Violations   []string `json:"violations"`
}

// TestE2EDaemonDeclaresEverySeamItWires asks a running daemon what it
// wired and compares the answer against the seams the composition root
// is supposed to attach.
//
// This is the effect half of the manifest guard. Its static sibling
// (TestEveryRegistryObserverDeclaresItsSeam) proves every registration
// goes through the declaring call; only a booted daemon can show that
// the call was reached. A wiring line that compiles, is grepped
// successfully by every name-matching guard, and sits behind an `if`
// that is false in production is invisible to everything except this.
//
// The CCU boots NOT ready on purpose, as in the boot-order test: these
// observers replay over centrals already registered and run again for
// every later one, so a daemon that happened to finish south-bound
// bring-up first would declare the same set however the ordering was
// broken.
func TestE2EDaemonDeclaresEverySeamItWires(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{StartCCUNotReady: true})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	h.CCU().V().SetReady(true)

	seams := fetchWiringSeams(t, h)
	if len(seams) == 0 {
		t.Fatal("the daemon declares no wiring seams at all — either the manifest is not served " +
			"or nothing wires through it; both make every assertion below meaningless")
	}

	declared := make(map[string]wiringSeam, len(seams))
	for _, s := range seams {
		declared[s.Name] = s
	}

	var missing []string
	for _, want := range expectedWiringSeams {
		if _, ok := declared[want]; !ok {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		have := make([]string, 0, len(declared))
		for name := range declared {
			have = append(have, name)
		}
		sort.Strings(have)
		t.Errorf("the running daemon did not wire %d declared seam(s):\n  %s\n\ndeclared: %s\n\n"+
			"Each missing name is a collaborator that was never handed over, so the feature behind "+
			"it cannot work — and the daemon starts, reports healthy and serves every endpoint "+
			"regardless.", len(missing), strings.Join(missing, "\n  "), strings.Join(have, ", "))
	}

	// A config-gated seam must not appear in the unconditional list: the
	// two groups mean different things, and merging them would let a
	// seam that silently stopped wiring hide behind "it is optional".
	for name := range configGatedWiringSeams {
		for _, want := range expectedWiringSeams {
			if want == name {
				t.Errorf("seam %q is listed as both unconditional and config-gated", name)
			}
		}
	}

	// No seam may report a broken ordering constraint. This is the
	// assertion the ordered seams exist for, and the only one that can
	// see the defect at all: the collaborator IS wired in the broken
	// case, the setter returns nothing, and every other check — the
	// static guards, the pins, health, the route surface — stays green.
	for _, s := range seams {
		if len(s.Violations) > 0 {
			t.Errorf("seam %q was attached in the wrong order:\n  %s\n\n%s",
				s.Name, strings.Join(s.Violations, "\n  "), s.Why)
		}
	}

	// The ordered seams are named, because a constraint that quietly
	// stopped being declared reports no violation either.
	for name, want := range expectedOrderedSeams {
		got, ok := declared[name]
		if !ok {
			t.Errorf("ordered seam %q is not declared by the running daemon; its ordering is "+
				"back to being a property of line order in a 900-line function", name)
			continue
		}
		if got.Phase != "ordered" {
			t.Errorf("seam %q reports phase %q, want ordered", name, got.Phase)
		}
		if !sameStrings(got.Before, want.before) {
			t.Errorf("seam %q declares before=%v, want %v", name, got.Before, want.before)
		}
		if !sameStrings(got.After, want.after) {
			t.Errorf("seam %q declares after=%v, want %v", name, got.After, want.after)
		}
	}

	// Every entry must carry its four fields. The manifest's value to an
	// operator reading /diagnostics/wiring is the reason, not the name.
	for _, s := range seams {
		switch {
		case s.Collaborator == "":
			t.Errorf("seam %q declares no collaborator", s.Name)
		case s.Phase == "":
			t.Errorf("seam %q declares no phase", s.Name)
		case s.Why == "":
			t.Errorf("seam %q declares no reason", s.Name)
		}
	}
}

// fetchWiringSeams polls GET /diagnostics/wiring until it answers with a
// non-empty list. Polling separates "not yet" from "never": the
// observers are attached during bring-up, which is asynchronous.
func fetchWiringSeams(t *testing.T, h *harness.Harness) []wiringSeam {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	var last []wiringSeam
	var lastErr error
	for {
		seams, err := getWiringSeamsOnce(h)
		if err == nil && len(seams) > 0 {
			return seams
		}
		last, lastErr = seams, err
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("GET /api/v1/diagnostics/wiring: %v", lastErr)
	}
	return last
}

func getWiringSeamsOnce(h *harness.Harness) ([]wiringSeam, error) {
	req, err := h.REST().NewRequest(http.MethodGet, "/api/v1/diagnostics/wiring", nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.REST().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out []wiringSeam
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return out, nil
}

// sameStrings compares two mark lists, treating nil and empty as equal:
// the wire omits an observer seam's mark fields entirely.
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
