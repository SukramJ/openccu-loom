// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/codes"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// This file drives the alarm-control-panel entity projection
// (panels.go) through a real alarm.Service — a real engine, a real
// codes facade, real SQLite stores — so the tests exercise the exact
// EffectiveCodePolicy resolution production wires seedPanels and
// refreshPanelCodePolicies through (docs/alarm-concept.md §11/§13.3).

// panelsTestStart is the harness wall-clock origin, kept after the
// engine's clock-plausibility epoch, mirroring intents_test.go's
// convention.
var panelsTestStart = time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

// panelsHarness bundles a real SQLite-backed alarm.Service for driving
// seedPanels / refreshPanelCodePolicies against real area configs and
// real PIN-code rows through the codes facade.
type panelsHarness struct {
	t   *testing.T
	ctx context.Context
	clk *clock.Fake
	svc *Service
}

// newPanelsHarness opens a fresh temp-file SQLite database and builds
// the alarm.Service on it, mirroring newIntentsHarness's setup.
func newPanelsHarness(t *testing.T) *panelsHarness {
	t.Helper()
	dsn := sqlitestore.FileDSN(filepath.Join(t.TempDir(), "alarm-panels.db"))
	db, err := sqlitestore.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	clk := clock.NewFake(panelsTestStart)
	svc, err := NewService(Deps{
		Settings: Settings{Enabled: true},
		Registry: central.NewRegistry(),
		Stores:   NewStores(db),
		Clock:    clk,
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return &panelsHarness{t: t, ctx: context.Background(), clk: clk, svc: svc}
}

// seedArea persists an area row carrying an explicit CodePolicy so the
// code-policy tests can control both halves of EffectiveCodePolicy.
func (h *panelsHarness) seedArea(id, name string, policy engine.CodePolicy) {
	h.t.Helper()
	cfg := engine.AreaConfig{
		Modes:      map[hmenum.AlarmMode]engine.ModeConfig{hmenum.AlarmModeFull: {}},
		CodePolicy: policy,
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		h.t.Fatalf("marshal area config: %v", err)
	}
	now := h.clk.Now().UnixMilli()
	if err := h.svc.Stores().Areas.Upsert(h.ctx, sqlitestore.AlarmAreaRow{
		ID: id, Name: name, ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		h.t.Fatalf("seed area %s: %v", id, err)
	}
}

// seedPINCode persists an enabled or disabled pin-kind alarm-code row
// directly through the store, mirroring
// internal/north/mqtt/alarm_publisher_test.go's fixture (there is no
// facade "create" path in this package). Every verb permission is
// granted: the code-policy tests care only about whether an applicable
// enabled pin code exists, never which verb it authorizes. A nil areas
// list applies to every area, matching the store's own "[]" catch-all
// convention.
func (h *panelsHarness) seedPINCode(id, name, pin string, enabled bool, areas []string) {
	h.t.Helper()
	hash, err := codes.HashPIN(pin)
	if err != nil {
		h.t.Fatalf("HashPIN: %v", err)
	}
	areasJSON := "[]"
	if len(areas) > 0 {
		b, err := json.Marshal(areas)
		if err != nil {
			h.t.Fatalf("marshal areas: %v", err)
		}
		areasJSON = string(b)
	}
	now := h.clk.Now().UnixMilli()
	row := sqlitestore.AlarmCodeRow{
		ID: id, Name: name, Kind: string(codes.KindPIN), Hash: hash,
		PermsJSON: `{"arm":true,"disarm":true,"silence":true}`,
		AreasJSON: areasJSON, BindingJSON: "{}",
		Enabled: enabled, CreatedAtMS: now, UpdatedAtMS: now,
	}
	if err := h.svc.Stores().Codes.Upsert(h.ctx, row); err != nil {
		h.t.Fatalf("seed pin code %s: %v", id, err)
	}
}

// deletePINCode removes a pin-code row outright, flipping any area it
// applied to back to "no applicable code exists".
func (h *panelsHarness) deletePINCode(id string) {
	h.t.Helper()
	if err := h.svc.Stores().Codes.Delete(h.ctx, id); err != nil {
		h.t.Fatalf("delete pin code %s: %v", id, err)
	}
}

// start starts the service (loads config, runs the initial seedPanels)
// and registers cleanup.
func (h *panelsHarness) start() {
	h.t.Helper()
	if err := h.svc.Start(h.ctx); err != nil {
		h.t.Fatalf("service start: %v", err)
	}
	h.t.Cleanup(func() { _ = h.svc.Stop(context.Background()) })
}

// panel returns the current projection for areaID, failing the test if
// no such panel is registered.
func (h *panelsHarness) panel(areaID string) alarmpanel.Panel {
	h.t.Helper()
	for _, p := range h.svc.Panels() {
		if p.AreaID == areaID {
			return p
		}
	}
	h.t.Fatalf("no panel for area %q; got %+v", areaID, h.svc.Panels())
	return alarmpanel.Panel{}
}

// boolPtr returns a pointer to b, used to set CodePolicy.RequireDisarm
// explicitly away from its nil ("required once a code exists") default.
func boolPtr(b bool) *bool { return &b }

// --- seedPanels: per-area code-policy derivation ---

// TestSeedPanels_CodePolicyFlagsFollowPINCodeExistence verifies
// seedPanels derives an area panel's CodeArmRequired/CodeDisarmRequired
// flags from both halves of EffectiveCodePolicy: the area's CodePolicy
// AND whether an applicable enabled PIN code currently exists. A policy
// requiring a code with no PIN code enrolled advertises no requirement
// at all (docs/alarm-concept.md §11/§13.3), so a client never prompts
// for a code the engine cannot ask for.
func TestSeedPanels_CodePolicyFlagsFollowPINCodeExistence(t *testing.T) {
	t.Parallel()
	h := newPanelsHarness(t)
	h.seedArea("eg", "Erdgeschoss", engine.CodePolicy{RequireArm: true})
	h.start()

	p := h.panel("eg")
	if p.CodeArmRequired || p.CodeDisarmRequired {
		t.Fatalf("panel policy with no PIN code = %+v, want both false", p)
	}

	h.seedPINCode("c1", "Markus", "1234", true, nil)
	h.svc.seedPanels(h.ctx)

	p = h.panel("eg")
	if !p.CodeArmRequired {
		t.Errorf("code_arm_required = false, want true once an enabled PIN exists")
	}
	if !p.CodeDisarmRequired {
		t.Errorf("code_disarm_required = false, want true (RequireDisarm nil defaults to required once a code exists)")
	}
}

// --- master aggregate: any-area-requires union ---

// TestSeedPanels_MasterPanelUnionsCodePolicyAcrossAreas verifies the
// aggregate master panel (present with >= 2 areas, last in Panels())
// carries the OR of every area's effective code-policy flags, while an
// area with no applicable requirement of its own keeps both its own
// flags false (masterLocked in panels.go).
func TestSeedPanels_MasterPanelUnionsCodePolicyAcrossAreas(t *testing.T) {
	t.Parallel()
	h := newPanelsHarness(t)
	// "eg" requires both verbs and has an applicable, area-scoped PIN
	// code, so its effective policy resolves to true/true.
	h.seedArea("eg", "Erdgeschoss", engine.CodePolicy{RequireArm: true})
	// "og" opts out of the disarm default explicitly, so it carries no
	// requirement regardless of any PIN code that exists elsewhere.
	h.seedArea("og", "Obergeschoss", engine.CodePolicy{RequireDisarm: boolPtr(false)})
	h.seedPINCode("c1", "Markus", "1234", true, []string{"eg"})
	h.start()

	panels := h.svc.Panels()
	if len(panels) != 3 {
		t.Fatalf("panels = %+v, want 3 (2 areas + master)", panels)
	}
	master := panels[len(panels)-1]
	if !master.Master {
		t.Fatalf("last panel = %+v, want the master aggregate", master)
	}
	if !master.CodeArmRequired || !master.CodeDisarmRequired {
		t.Errorf("master code policy = %+v, want both true (any-area union)", master)
	}

	eg := h.panel("eg")
	if !eg.CodeArmRequired || !eg.CodeDisarmRequired {
		t.Errorf("eg panel = %+v, want both true", eg)
	}
	og := h.panel("og")
	if og.CodeArmRequired || og.CodeDisarmRequired {
		t.Errorf("og panel = %+v, want both false (no applicable requirement)", og)
	}
}

// --- refreshPanelCodePolicies / NotifyCodesChanged ---

// TestNotifyCodesChanged_RepublishesOnlyFlippedPanels verifies that
// removing the only applicable PIN code and calling NotifyCodesChanged
// re-derives the area's effective code policy, publishes exactly one
// AlarmPanelChangedEvent carrying the flipped (now-false) flags, and
// updates the live Panels() snapshot — but a second call with nothing
// left to flip republishes nothing, so an unrelated code-store write
// never spams panel-changed events on every surface.
func TestNotifyCodesChanged_RepublishesOnlyFlippedPanels(t *testing.T) {
	t.Parallel()
	h := newPanelsHarness(t)
	h.seedArea("eg", "Erdgeschoss", engine.CodePolicy{RequireArm: true})
	h.seedPINCode("c1", "Markus", "1234", true, nil)
	h.start()

	if p := h.panel("eg"); !p.CodeArmRequired || !p.CodeDisarmRequired {
		t.Fatalf("initial panel policy = %+v, want both true", p)
	}

	var received []hmevent.AlarmPanelChangedEvent
	unsub := events.Subscribe(h.svc.Bus(), func(e hmevent.AlarmPanelChangedEvent) {
		received = append(received, e)
	})
	defer unsub()

	h.deletePINCode("c1")
	h.svc.NotifyCodesChanged()

	var found bool
	for _, e := range received {
		if e.AreaID != "eg" {
			continue
		}
		found = true
		if e.CodeArmRequired || e.CodeDisarmRequired {
			t.Errorf("panel-changed event policy flags = %+v, want both false", e)
		}
	}
	if !found {
		t.Fatalf("no alarm.panel_changed event observed for area eg; got %+v", received)
	}
	if p := h.panel("eg"); p.CodeArmRequired || p.CodeDisarmRequired {
		t.Errorf("panel snapshot after refresh = %+v, want both false", p)
	}

	before := len(received)
	h.svc.NotifyCodesChanged() // nothing left to flip
	if after := len(received); after != before {
		t.Errorf("NotifyCodesChanged with no policy change republished %d panel events, want 0", after-before)
	}
}
