// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// This file covers the §11 code-policy port: per-verb requirement
// resolution (arm/disarm/silence), the operator-source bypass, duress
// passthrough, and the ErrInvalidCode refusal. harness.start() (see
// harness_test.go) never wires a CodeValidator, so these tests build
// their own engine on the harness's stores/fakes via
// startWithValidator.

// codeValidateCall records one CodeValidator.Validate invocation.
type codeValidateCall struct {
	areaID, verb, code, source string
}

// codeResult is the scripted outcome for one code string.
type codeResult struct {
	identity string
	duress   bool
	err      error
}

// fakeCodeValidator resolves a fixed set of code strings; any code not
// present in results is refused with engine.ErrInvalidCode, matching the
// CodeValidator contract for "a supplied code that does not
// authenticate".
type fakeCodeValidator struct {
	mu      sync.Mutex
	results map[string]codeResult
	calls   []codeValidateCall
}

func newFakeCodeValidator(results map[string]codeResult) *fakeCodeValidator {
	return &fakeCodeValidator{results: results}
}

func (f *fakeCodeValidator) Validate(_ context.Context, areaID, verb, code, source string) (identity string, duress bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, codeValidateCall{areaID, verb, code, source})
	r, ok := f.results[code]
	if !ok {
		return "", false, engine.ErrInvalidCode
	}
	return r.identity, r.duress, r.err
}

func (f *fakeCodeValidator) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// startWithValidator builds and starts an engine on the harness's
// current stores and fakes with v wired as the CodeValidator, mirroring
// harness.start() (harness_test.go) but adding the code-policy port.
func (h *harness) startWithValidator(v engine.CodeValidator) {
	h.t.Helper()
	eng, err := engine.New(engine.Deps{
		Clock: h.clk, Scheduler: h.sched, Areas: h.areas, Sensors: h.sensors,
		State: h.states, Incidents: h.incidents, Runtime: h.runtime,
		Outputs: h.outputs, Sink: h.sink, Journal: h.journal, SensorReader: h.reader,
		Validator: v,
	})
	if err != nil {
		h.t.Fatalf("engine.New: %v", err)
	}
	h.eng = eng
	if err := h.eng.Start(h.ctx); err != nil {
		h.t.Fatalf("engine.Start: %v", err)
	}
}

// codePolicyAreaConfig builds the standard two-mode test area with an
// explicit CodePolicy.
func codePolicyAreaConfig(reqArm bool, reqDisarm *bool, reqSilence map[string]bool) engine.AreaConfig {
	cfg := defaultAreaConfig()
	cfg.CodePolicy = engine.CodePolicy{RequireArm: reqArm, RequireDisarm: reqDisarm, RequireSilence: reqSilence}
	return cfg
}

func boolPtr(b bool) *bool { return &b }

// mustJournalEntry returns the first recorded entry with event, or fails.
func mustJournalEntry(t *testing.T, j *fakeJournal, event string) engine.JournalEntry {
	t.Helper()
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, e := range j.entries {
		if e.Event == event {
			return e
		}
	}
	t.Fatalf("missing %q journal entry; got %v", event, j.entries)
	return engine.JournalEntry{}
}

func TestCodePolicy_DisarmDefaultRequiresACodeWhenOneExists(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea() // zero-value CodePolicy: RequireDisarm defaults "true when codes exist"
	v := newFakeCodeValidator(map[string]codeResult{"1234": {identity: "Alice"}})
	h.startWithValidator(v)
	h.armFull()

	if err := h.eng.DisarmWithCode(h.ctx, "eg", "", "mqtt", ""); !errors.Is(err, engine.ErrInvalidCode) {
		t.Fatalf("err = %v, want ErrInvalidCode", err)
	}
	h.wantState("eg", hmenum.AlarmAreaStateArmed)

	if err := h.eng.DisarmWithCode(h.ctx, "eg", "", "mqtt", "1234"); err != nil {
		t.Fatalf("disarm with a valid code: %v", err)
	}
	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)
}

func TestCodePolicy_DisarmPermittedWithEmptyCodeWhenNoCodesConfigured(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea() // RequireDisarm defaults true, but no codes exist
	// The validator resolves the "codes exist" half of the effective
	// disarm rule (§11): an empty code is permitted when there is
	// nothing to check it against.
	v := newFakeCodeValidator(map[string]codeResult{"": {}})
	h.startWithValidator(v)
	h.armFull()

	if err := h.eng.DisarmWithCode(h.ctx, "eg", "", "mqtt", ""); err != nil {
		t.Fatalf("disarm with an empty code and no configured codes: %v", err)
	}
	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)
}

func TestCodePolicy_ArmOnlyRequiresACodeWhenConfigured(t *testing.T) {
	v := newFakeCodeValidator(map[string]codeResult{"1234": {identity: "Alice"}})

	t.Run("RequireArm off: code-free arm succeeds", func(t *testing.T) {
		h := newHarness(t)
		h.seedArea("eg", "Erdgeschoss", codePolicyAreaConfig(false, boolPtr(false), nil))
		h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
		h.startWithValidator(v)

		if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
			t.Fatalf("arm without a code: %v", err)
		}
	})

	t.Run("RequireArm on: code-free arm is refused, a valid code succeeds", func(t *testing.T) {
		h := newHarness(t)
		h.seedArea("eg", "Erdgeschoss", codePolicyAreaConfig(true, boolPtr(false), nil))
		h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
		h.startWithValidator(v)

		if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); !errors.Is(err, engine.ErrInvalidCode) {
			t.Fatalf("err = %v, want ErrInvalidCode", err)
		}
		if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, Code: "1234"}); err != nil {
			t.Fatalf("arm with a valid code: %v", err)
		}
	})
}

func TestCodePolicy_SilenceIsPerSourcePolicy(t *testing.T) {
	h := newHarness(t)
	h.seedArea("eg", "Erdgeschoss", codePolicyAreaConfig(false, boolPtr(false), map[string]bool{"mqtt": true}))
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	v := newFakeCodeValidator(map[string]codeResult{"1234": {identity: "Alice"}})
	h.startWithValidator(v)
	h.armFull()

	if err := h.eng.SilenceWithCode(h.ctx, "eg", "", "mqtt", ""); !errors.Is(err, engine.ErrInvalidCode) {
		t.Fatalf("mqtt silence without a code: err = %v, want ErrInvalidCode", err)
	}
	if err := h.eng.SilenceWithCode(h.ctx, "eg", "", "app", ""); err != nil {
		t.Fatalf("app silence without a code (S3 default off): %v", err)
	}
	if err := h.eng.SilenceWithCode(h.ctx, "eg", "", "mqtt", "1234"); err != nil {
		t.Fatalf("mqtt silence with a valid code: %v", err)
	}
}

func TestCodePolicy_OperatorSourceBypassesTheRequirementWithoutConsultingTheValidator(t *testing.T) {
	h := newHarness(t)
	h.seedArea("eg", "Erdgeschoss", codePolicyAreaConfig(true, boolPtr(true), map[string]bool{"rest-operator": true}))
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	v := newFakeCodeValidator(nil)
	h.startWithValidator(v)
	// RequireArm is also true here, so arming must go through the same
	// operator bypass under test rather than the shared code-free
	// armFull() helper.
	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester", Source: "rest-operator"}); err != nil {
		t.Fatalf("operator arm without a code: %v", err)
	}
	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
	v.mu.Lock()
	v.calls = nil // reset call log: this test only asserts the disarm bypass below
	v.mu.Unlock()

	if err := h.eng.DisarmWithCode(h.ctx, "eg", "", "rest-operator", ""); err != nil {
		t.Fatalf("operator disarm without a code: %v", err)
	}
	if n := v.callCount(); n != 0 {
		t.Fatalf("validator calls = %d, want 0 (no code offered, the requirement is bypassed before the port is consulted)", n)
	}
}

func TestCodePolicy_OperatorSourceWithAWrongCodeStillBypasses(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	v := newFakeCodeValidator(map[string]codeResult{"1234": {identity: "Alice"}})
	h.startWithValidator(v)
	h.armFull()

	if err := h.eng.DisarmWithCode(h.ctx, "eg", "", "rest-operator", "wrong"); err != nil {
		t.Fatalf("operator disarm with a wrong code must still succeed: %v", err)
	}
	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)
	if n := v.callCount(); n != 1 {
		t.Fatalf("validator calls = %d, want 1 (a supplied code is still checked, only its failure is swallowed)", n)
	}
}

func TestCodePolicy_ErrInvalidCodeOnANonOperatorSource(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	v := newFakeCodeValidator(map[string]codeResult{"1234": {identity: "Alice"}})
	h.startWithValidator(v)
	h.armFull()

	err := h.eng.DisarmWithCode(h.ctx, "eg", "", "keypad", "wrong")
	if !errors.Is(err, engine.ErrInvalidCode) {
		t.Fatalf("err = %v, want ErrInvalidCode", err)
	}
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
}

func TestCodePolicy_DuressCodeActsNormallyAndFiresASilentEvent(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	v := newFakeCodeValidator(map[string]codeResult{"9999": {identity: "Bob", duress: true}})
	h.startWithValidator(v)
	h.armFull()

	if err := h.eng.DisarmWithCode(h.ctx, "eg", "", "keypad", "9999"); err != nil {
		t.Fatalf("duress disarm: %v", err)
	}
	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)

	// Visible journal: only the ordinary disarmed entry, attributed to
	// the duress code's identity — no visible trace of duress.
	visible := mustJournalEntry(t, h.journal, "disarmed")
	if visible.Actor != "Bob" || visible.Hidden {
		t.Fatalf("visible disarm entry = %+v, want Actor=Bob Hidden=false", visible)
	}

	// The hidden fan-out: a Hidden journal row plus a dedicated bus event.
	duress := mustJournalEntry(t, h.journal, "duress")
	if !duress.Hidden || duress.Actor != "Bob" {
		t.Fatalf("duress journal entry = %+v, want Hidden=true Actor=Bob", duress)
	}

	h.sink.mu.Lock()
	var found bool
	for _, ev := range h.sink.events {
		if de, ok := ev.(hmevent.AlarmDuressEvent); ok {
			found = true
			if de.By != "Bob" || de.Verb != "disarm" || de.AreaID != "eg" {
				t.Fatalf("duress event = %+v, want By=Bob Verb=disarm AreaID=eg", de)
			}
		}
	}
	h.sink.mu.Unlock()
	if !found {
		t.Fatal("expected an AlarmDuressEvent on the sink")
	}
}

func TestCodePolicy_OperatorSourceDuressCodeStillFiresDuress(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	v := newFakeCodeValidator(map[string]codeResult{"9999": {identity: "Bob", duress: true}})
	h.startWithValidator(v)
	h.armFull()

	if err := h.eng.DisarmWithCode(h.ctx, "eg", "", "rest-operator", "9999"); err != nil {
		t.Fatalf("operator duress disarm: %v", err)
	}
	if !h.journal.has("duress") {
		t.Fatalf("expected a duress journal entry even for an operator-session disarm; got %v", h.journal.events())
	}
}

func TestCodePolicy_NilValidatorDisablesEveryPolicy(t *testing.T) {
	h := newHarness(t)
	h.seedArea("eg", "Erdgeschoss", codePolicyAreaConfig(true, boolPtr(true), map[string]bool{"mqtt": true}))
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	h.start() // no Validator wired

	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm with codes disabled: %v", err)
	}
	if err := h.eng.SilenceWithCode(h.ctx, "eg", "", "mqtt", ""); err != nil {
		t.Fatalf("mqtt silence with codes disabled: %v", err)
	}
	if err := h.eng.DisarmWithCode(h.ctx, "eg", "", "mqtt", ""); err != nil {
		t.Fatalf("disarm with codes disabled: %v", err)
	}
}
