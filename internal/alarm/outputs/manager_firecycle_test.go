// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package outputs

import (
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestFireCycle_AcousticSirenActivatesWithConfiguredDuration covers
// S1 case 1: an acoustic siren fires at its own configured duration,
// the ledger write lands before the device write, and the duration
// is never zero.
func TestFireCycle_AcousticSirenActivatesWithConfiguredDuration(t *testing.T) {
	h := newHarness(t)
	h.seedOutputs(outputRow("sirA", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{DurationSeconds: 120, AcousticTone: "FREQ_HIGH"}))

	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(1, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	calls := h.siren("sirA").turnOnCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("sirA TurnOn calls = %d, want 1", len(calls))
	}
	call := calls[0]
	if call.Cfg.Duration != 120*time.Second {
		t.Fatalf("sirA duration = %v, want 120s", call.Cfg.Duration)
	}
	if call.Cfg.Duration == 0 {
		t.Fatal("sirA duration must never be zero")
	}
	if call.Priority != hmenum.CommandPriorityHigh {
		t.Fatalf("sirA priority = %v, want High", call.Priority)
	}
	if call.Cfg.AcousticTone != "FREQ_HIGH" {
		t.Fatalf("sirA tone = %q, want FREQ_HIGH", call.Cfg.AcousticTone)
	}
	// The selection pointer is the value that reaches the wire — the
	// tone field alone is validation-only and would leave the device
	// on its previous tone.
	if call.Cfg.AcousticSelection == nil || *call.Cfg.AcousticSelection != "FREQ_HIGH" {
		t.Fatalf("sirA AcousticSelection = %v, want FREQ_HIGH pointer", call.Cfg.AcousticSelection)
	}

	ledgerCall, ok := h.ledger.callWithDelta(120_000)
	if !ok {
		t.Fatal("expected a ledger AddAcousticMS(120000) call")
	}
	if ledgerCall.Seq >= call.Seq {
		t.Fatalf("ledger write (seq %d) must precede device TurnOn (seq %d)", ledgerCall.Seq, call.Seq)
	}
}

// TestFireCycle_AcousticDurationDefaultsToManagerDefault covers S1
// case 2: an output without duration_s uses the manager's configured
// default siren duration.
func TestFireCycle_AcousticDurationDefaultsToManagerDefault(t *testing.T) {
	h := newHarness(t)
	h.seedOutputs(outputRow("sirDefault", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{}))

	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(1, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	calls := h.siren("sirDefault").turnOnCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("sirDefault TurnOn calls = %d, want 1", len(calls))
	}
	if calls[0].Cfg.Duration != 180*time.Second {
		t.Fatalf("sirDefault duration = %v, want 180s (manager default)", calls[0].Cfg.Duration)
	}
}

// TestFireCycle_AcousticDurationClampsToHardCeiling covers S1 case 3:
// an output requesting an oversized duration_s clamps to the
// MaxAcousticSeconds hard ceiling, independent of the per-incident
// budget (the harness uses a budget well above the ceiling so the
// ceiling — not the budget — is the binding constraint).
func TestFireCycle_AcousticDurationClampsToHardCeiling(t *testing.T) {
	h := newHarness(t)
	h.build(func(c *Config) { c.MaxAcousticPerIncident = 1000 * time.Second })
	h.seedOutputs(outputRow("sirCeil", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{DurationSeconds: 9999}))

	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(1, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	calls := h.siren("sirCeil").turnOnCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("sirCeil TurnOn calls = %d, want 1", len(calls))
	}
	if calls[0].Cfg.Duration != MaxAcousticSeconds*time.Second {
		t.Fatalf("sirCeil duration = %v, want %ds hard ceiling", calls[0].Cfg.Duration, MaxAcousticSeconds)
	}
}

// TestFireCycle_CumulativeAcousticBudgetClampsAndExhausts covers S1
// case 4: the per-incident acoustic budget clamps a later cycle to
// whatever remains, and a third cycle with an exhausted budget fires
// nothing and journals acoustic_budget_exhausted.
func TestFireCycle_CumulativeAcousticBudgetClampsAndExhausts(t *testing.T) {
	h := newHarness(t)
	h.seedOutputs(outputRow("sirA", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{DurationSeconds: 120}))
	const incidentID = int64(4)

	// First cycle: fresh incident, full 300 s budget available.
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(incidentID, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle #1: %v", err)
	}
	calls := h.siren("sirA").turnOnCallsSnapshot()
	if len(calls) != 1 || calls[0].Cfg.Duration != 120*time.Second {
		t.Fatalf("cycle 1 calls = %+v, want one 120s activation", calls)
	}

	// Second cycle: ledger already carries 240 s, only 60 s remain.
	h.ledger.seedGet(incidentID, 240_000)
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(incidentID, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle #2: %v", err)
	}
	calls = h.siren("sirA").turnOnCallsSnapshot()
	if len(calls) != 2 {
		t.Fatalf("cycle 2 total calls = %d, want 2", len(calls))
	}
	if calls[1].Cfg.Duration != 60*time.Second {
		t.Fatalf("cycle 2 duration = %v, want 60s (clamped to remaining budget)", calls[1].Cfg.Duration)
	}

	// Third cycle: ledger reports the budget fully spent.
	h.ledger.seedGet(incidentID, 300_000)
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(incidentID, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle #3: %v", err)
	}
	calls = h.siren("sirA").turnOnCallsSnapshot()
	if len(calls) != 2 {
		t.Fatalf("cycle 3 must not activate the device: total calls = %d, want 2", len(calls))
	}
	if !h.journal.hasForOutput("acoustic_budget_exhausted", "sirA") {
		t.Fatal("expected an acoustic_budget_exhausted journal entry for sirA")
	}
}

// TestFireCycle_LedgerWriteFailureBlocksActivation covers S1 case 5:
// a ledger accounting failure must block the device write — the safe
// direction is to under-activate, never to activate unaccounted.
func TestFireCycle_LedgerWriteFailureBlocksActivation(t *testing.T) {
	h := newHarness(t)
	h.seedOutputs(outputRow("sirA", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{DurationSeconds: 120}))
	h.ledger.setAddErr(errors.New("ledger unavailable"))

	err := h.mgr.FireCycle(h.ctx, "eg", newIncident(1, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy})
	if err == nil {
		t.Fatal("expected FireCycle to report the ledger failure")
	}
	if h.siren("sirA").turnOnCount() != 0 {
		t.Fatal("sirA must not activate when the ledger write fails")
	}
	if !h.journal.hasForOutput("output_fire_failed", "sirA") {
		t.Fatal("expected an output_fire_failed journal entry for sirA")
	}
}

// TestFireCycle_SilentPolicySuppressesAcousticOutputsOnly covers S1
// case 6: Policy.Silent suppresses every acoustic-class output but
// leaves the optical siren and the alarm light firing.
func TestFireCycle_SilentPolicySuppressesAcousticOutputsOnly(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()

	opts := engine.FireOptions{Policy: engine.OutputPolicy{Silent: true}}
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(6, hmenum.AlarmModeFull), opts); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	if n := h.siren("sirA").turnOnCount(); n != 0 {
		t.Fatalf("sirA (acoustic) TurnOn calls = %d, want 0 under Silent", n)
	}
	if n := h.actuator("plug").boundedCallCount(); n != 0 {
		t.Fatalf("plug (switched siren, acoustic) calls = %d, want 0 under Silent", n)
	}
	if n := h.smoke("smoke").turnOnCount(); n != 0 {
		t.Fatalf("smoke TurnOn calls = %d, want 0 under Silent", n)
	}
	if n := h.siren("sirO").turnOnCount(); n != 1 {
		t.Fatalf("sirO (optical) TurnOn calls = %d, want 1 under Silent", n)
	}
	if n := h.actuator("light").steadyCallCount(); n != 1 {
		t.Fatalf("light TurnOnSteady calls = %d, want 1 under Silent", n)
	}
}

// TestFireCycle_DegradedRestrictsToOpticalAndLight covers S1 case 7:
// the restart-loop breaker restricts a cycle to optical + light
// outputs; every acoustic class (including smoke) is skipped.
func TestFireCycle_DegradedRestrictsToOpticalAndLight(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()

	opts := engine.FireOptions{Degraded: true, Policy: engine.OutputPolicy{SmokeSounders: true}}
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(7, hmenum.AlarmModeFull), opts); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	if n := h.siren("sirA").turnOnCount(); n != 0 {
		t.Fatalf("sirA TurnOn calls = %d, want 0 under Degraded", n)
	}
	if n := h.actuator("plug").boundedCallCount(); n != 0 {
		t.Fatalf("plug calls = %d, want 0 under Degraded", n)
	}
	if n := h.smoke("smoke").turnOnCount(); n != 0 {
		t.Fatalf("smoke calls = %d, want 0 under Degraded even with SmokeSounders policy", n)
	}
	if n := h.siren("sirO").turnOnCount(); n != 1 {
		t.Fatalf("sirO TurnOn calls = %d, want 1 under Degraded", n)
	}
	if n := h.actuator("light").steadyCallCount(); n != 1 {
		t.Fatalf("light TurnOnSteady calls = %d, want 1 under Degraded", n)
	}
}

// TestFireCycle_ExcludeOutdoorSkipsOutdoorSiren covers S1 case 8: an
// outdoor-flagged siren is skipped when the mode policy excludes
// outdoor sirens, and fires normally otherwise.
func TestFireCycle_ExcludeOutdoorSkipsOutdoorSiren(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()

	excluded := engine.FireOptions{Policy: engine.OutputPolicy{ExcludeOutdoor: true}}
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(81, hmenum.AlarmModeFull), excluded); err != nil {
		t.Fatalf("FireCycle (excluded): %v", err)
	}
	if n := h.siren("sirOut").turnOnCount(); n != 0 {
		t.Fatalf("sirOut TurnOn calls = %d, want 0 with ExcludeOutdoor", n)
	}
	if n := h.siren("sirA").turnOnCount(); n != 1 {
		t.Fatalf("sirA (indoor) TurnOn calls = %d, want 1 with ExcludeOutdoor", n)
	}

	included := engine.FireOptions{Policy: engine.OutputPolicy{ExcludeOutdoor: false}}
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(82, hmenum.AlarmModeFull), included); err != nil {
		t.Fatalf("FireCycle (included): %v", err)
	}
	if n := h.siren("sirOut").turnOnCount(); n != 1 {
		t.Fatalf("sirOut TurnOn calls = %d, want 1 without ExcludeOutdoor", n)
	}
}

// TestFireCycle_SmokeSoundersGatedByPolicy covers S1 case 9: the
// smoke-sounder class fires only when Policy.SmokeSounders is set.
func TestFireCycle_SmokeSoundersGatedByPolicy(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()

	off := engine.FireOptions{Policy: engine.OutputPolicy{SmokeSounders: false}}
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(91, hmenum.AlarmModeFull), off); err != nil {
		t.Fatalf("FireCycle (off): %v", err)
	}
	if n := h.smoke("smoke").turnOnCount(); n != 0 {
		t.Fatalf("smoke TurnOn calls = %d, want 0 with SmokeSounders off", n)
	}

	on := engine.FireOptions{Policy: engine.OutputPolicy{SmokeSounders: true}}
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(92, hmenum.AlarmModeFull), on); err != nil {
		t.Fatalf("FireCycle (on): %v", err)
	}
	if n := h.smoke("smoke").turnOnCount(); n != 1 {
		t.Fatalf("smoke TurnOn calls = %d, want 1 with SmokeSounders on", n)
	}
}

// TestFireCycle_ModeFilterSkipsOutputsOutsideMode covers S1 case 10:
// an output configured for a subset of modes does not fire for an
// incident outside that subset, while an unrestricted output still
// does.
func TestFireCycle_ModeFilterSkipsOutputsOutsideMode(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()

	opts := engine.FireOptions{Policy: engine.OutputPolicy{ExcludeOutdoor: true}}
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(10, hmenum.AlarmModePerimeter), opts); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	if n := h.actuator("modeOnly").steadyCallCount(); n != 0 {
		t.Fatalf("modeOnly (modes=[full]) calls = %d, want 0 for perimeter mode", n)
	}
	if n := h.actuator("light").steadyCallCount(); n != 1 {
		t.Fatalf("light (unrestricted) calls = %d, want 1 for perimeter mode", n)
	}
	if n := h.siren("sirA").turnOnCount(); n != 1 {
		t.Fatalf("sirA (unrestricted) calls = %d, want 1 for perimeter mode", n)
	}
}

// TestFireCycle_SwitchedSirenActivatesBoundedWithLedgerBeforeWrite
// covers S1 case 11: a switch-actuator-backed siren activates via
// TurnOnBounded at the clamped duration and configured level, with
// the ledger write landing first.
func TestFireCycle_SwitchedSirenActivatesBoundedWithLedgerBeforeWrite(t *testing.T) {
	h := newHarness(t)
	level := 0.7
	h.seedOutputs(outputRow("plug", hmenum.AlarmOutputClassSwitchedSiren, OutputConfig{DurationSeconds: 60, Level: &level}))

	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(11, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	calls := h.actuator("plug").boundedCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("plug TurnOnBounded calls = %d, want 1", len(calls))
	}
	call := calls[0]
	if call.D != 60*time.Second {
		t.Fatalf("plug duration = %v, want 60s", call.D)
	}
	if call.Level == nil || *call.Level != level {
		t.Fatalf("plug level = %v, want %v", call.Level, level)
	}
	if call.Priority != hmenum.CommandPriorityHigh {
		t.Fatalf("plug priority = %v, want High", call.Priority)
	}
	ledgerCall, ok := h.ledger.callWithDelta(60_000)
	if !ok {
		t.Fatal("expected a ledger AddAcousticMS(60000) call")
	}
	if ledgerCall.Seq >= call.Seq {
		t.Fatalf("ledger write (seq %d) must precede device write (seq %d)", ledgerCall.Seq, call.Seq)
	}
}

// TestFireCycle_SmokeSounderCancelsWatchdogOnActivationError covers
// S1 case 12: the smoke-sounder path arms its stop watchdog before
// the activation write, so a failing write must cancel the pending
// watchdog rather than leave it to fire a stray stop later.
func TestFireCycle_SmokeSounderCancelsWatchdogOnActivationError(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.smoke("smoke").setTurnOnErr(errors.New("smoke activation failed"))

	opts := engine.FireOptions{Policy: engine.OutputPolicy{SmokeSounders: true, ExcludeOutdoor: true}}
	err := h.mgr.FireCycle(h.ctx, "eg", newIncident(12, hmenum.AlarmModeFull), opts)
	if err == nil {
		t.Fatal("expected FireCycle to report the smoke activation failure")
	}
	if !h.journal.hasForOutput("output_fire_failed", "smoke") {
		t.Fatal("expected an output_fire_failed journal entry for smoke")
	}

	h.advance(200 * time.Second)
	if n := h.smoke("smoke").turnOffCount(); n != 0 {
		t.Fatalf("smoke TurnOff calls = %d, want 0 (watchdog must have been cancelled)", n)
	}
}

// TestFireCycle_NotificationOutputNotifiesInMode verifies an in-mode
// notification-class output calls the wired Notify sink exactly once
// per cycle, forwarding the exact row and incident FireCycle received.
func TestFireCycle_NotificationOutputNotifiesInMode(t *testing.T) {
	h := newHarness(t)
	h.build(func(c *Config) { c.Notify = h.recordNotify })
	h.seedOutputs(outputRow("notify1", hmenum.AlarmOutputClassNotification, OutputConfig{}))

	incident := newIncident(21, hmenum.AlarmModeFull)
	if err := h.mgr.FireCycle(h.ctx, "eg", incident, engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	calls := h.notifyCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("notify calls = %+v, want exactly 1", calls)
	}
	if calls[0].row.ID != "notify1" {
		t.Fatalf("notify row ID = %q, want notify1", calls[0].row.ID)
	}
	if calls[0].incident.ID != incident.ID || calls[0].incident.Mode != incident.Mode {
		t.Fatalf("notify incident = %+v, want %+v", calls[0].incident, incident)
	}
}

// TestFireCycle_NotificationOutputSkippedOutOfMode verifies a
// notification output restricted to a mode the incident is not in
// never reaches the Notify sink.
func TestFireCycle_NotificationOutputSkippedOutOfMode(t *testing.T) {
	h := newHarness(t)
	h.build(func(c *Config) { c.Notify = h.recordNotify })
	h.seedOutputs(outputRow("notify1", hmenum.AlarmOutputClassNotification, OutputConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}}))

	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(22, hmenum.AlarmModePerimeter), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	if calls := h.notifyCallsSnapshot(); len(calls) != 0 {
		t.Fatalf("notify calls = %+v, want none for an out-of-mode incident", calls)
	}
}

// TestFireCycle_NotificationFiresUnderSilentPolicy verifies the
// notification class is not an acoustic class (classEligible only
// suppresses acoustic classes under Policy.Silent), so a silenced
// incident still notifies.
func TestFireCycle_NotificationFiresUnderSilentPolicy(t *testing.T) {
	h := newHarness(t)
	h.build(func(c *Config) { c.Notify = h.recordNotify })
	h.seedOutputs(outputRow("notify1", hmenum.AlarmOutputClassNotification, OutputConfig{}))

	opts := engine.FireOptions{Policy: engine.OutputPolicy{Silent: true}}
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(23, hmenum.AlarmModeFull), opts); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	if calls := h.notifyCallsSnapshot(); len(calls) != 1 {
		t.Fatalf("notify calls = %+v, want exactly 1 under a silent policy", calls)
	}
}

// TestFireCycle_NilNotifySinkDoesNotPanic verifies a notification
// output fires safely when no Notify sink is wired (Config.Notify's
// documented nil-drops-the-signal contract).
func TestFireCycle_NilNotifySinkDoesNotPanic(t *testing.T) {
	h := newHarness(t)
	h.seedOutputs(outputRow("notify1", hmenum.AlarmOutputClassNotification, OutputConfig{}))

	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(24, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}
}

// TestOutputConfig_NotifyMQTTEnabledDefaultsToTrue verifies the MQTT
// delivery-plane flag resolves to true when unset and honours an
// explicit false.
func TestOutputConfig_NotifyMQTTEnabledDefaultsToTrue(t *testing.T) {
	if !(OutputConfig{}).NotifyMQTTEnabled() {
		t.Error("NotifyMQTTEnabled() with nil NotifyMQTT = false, want true (default on)")
	}
	off := false
	if (OutputConfig{NotifyMQTT: &off}).NotifyMQTTEnabled() {
		t.Error("NotifyMQTTEnabled() with NotifyMQTT=false = true, want false")
	}
}

// TestOutputConfig_NotifyWebhookEnabledDefaultsToTrue verifies the
// webhook delivery-plane flag resolves to true when unset and honours
// an explicit false.
func TestOutputConfig_NotifyWebhookEnabledDefaultsToTrue(t *testing.T) {
	if !(OutputConfig{}).NotifyWebhookEnabled() {
		t.Error("NotifyWebhookEnabled() with nil NotifyWebhook = false, want true (default on)")
	}
	off := false
	if (OutputConfig{NotifyWebhook: &off}).NotifyWebhookEnabled() {
		t.Error("NotifyWebhookEnabled() with NotifyWebhook=false = true, want false")
	}
}

// TestFireCycle_PerOutputFailureIsolatesRemainingOutputs covers S1
// case 13: one output's activation failure is journaled and joined
// into the returned error but never stops sibling outputs from
// firing.
func TestFireCycle_PerOutputFailureIsolatesRemainingOutputs(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.siren("sirA").setTurnOnErr(errors.New("sirA activation failed"))

	opts := engine.FireOptions{Policy: engine.OutputPolicy{ExcludeOutdoor: true}}
	err := h.mgr.FireCycle(h.ctx, "eg", newIncident(13, hmenum.AlarmModeFull), opts)
	if err == nil {
		t.Fatal("expected FireCycle to return a non-nil error")
	}
	if !h.journal.hasForOutput("output_fire_failed", "sirA") {
		t.Fatal("expected an output_fire_failed journal entry for sirA")
	}
	if n := h.actuator("plug").boundedCallCount(); n != 1 {
		t.Fatalf("plug TurnOnBounded calls = %d, want 1 (sirA's failure must not block it)", n)
	}
}
