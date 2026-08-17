// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/reqctx"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Program is a CCU program entity. Callers execute it through the
// configured [ProgramWriter].
//
// Program embeds [HubDataPoint] and therefore satisfies [HubDataPointer]
// and [datapoint.BaseDataPoint]. The promoted Name and Description
// fields retain their previous call sites unchanged.
//
// Use [NewProgram] to construct a properly wired instance.
type Program struct {
	HubDataPoint // embeds Name, Description, EnabledDefault, BaseDataPointFields, StateUncertain
	ID           string
	Writer       ProgramWriter

	// ServiceRegistry implements the write-half of [payload.Source].
	// Each Program instance gets its own registry so service methods
	// are registered per-instance and there is no double-registration
	// if two Program values wrap the same underlying CCU program.
	payload.ServiceRegistry

	// IsInternal marks Tmp_*-programs created internally by the CCU. When
	// IsInternal is true, north-bound adapters should omit the program from
	// listings and discovery payloads.
	IsInternal bool

	// ExecuteNotifier is called by Execute after a successful CCU round-trip.
	// The hub coordinator wires this to publish a ProgramExecutedEvent on the
	// internal bus so all north-bound adapters can observe the execution.
	// Nil means no notification is sent (default until wired).
	ExecuteNotifier func(ctx context.Context, id string, trigger hmenum.ProgramTrigger, success bool)

	// ActiveNotifier is called by [OnActive] whenever the observed activity
	// flag changes. The hub coordinator wires this to publish a
	// ProgramChangedEvent on the internal bus. Nil means no notification is
	// sent (default until wired).
	ActiveNotifier func(id string, active bool)

	mu               sync.RWMutex
	active           bool
	hasActive        bool
	lastExecute      time.Time
	lastResult       bool
	hasResult        bool
	conditionSummary string
	activitySummary  string
	callbacks        []func(event ProgramEvent)
	removedHandlers  []func()
}

// NewProgram constructs a [Program] with a fully initialised
// [datapoint.BaseDataPointFields] embedded in the [HubDataPoint] base.
//
// - central — the Unit name for UniqueID scoping (multi-CCU safe).
// - id — the CCU program ID (the ISE object ID).
// - name — the CCU program name (used as both Name field and KeyName).
// - description — optional human-readable description.
// - isInternal — true for Tmp_*-programs created internally by the CCU.
// - writer — the execution backend; nil creates an execute-only program.
func NewProgram(centralName, id, name, description string, isInternal bool, writer ProgramWriter) *Program {
	p := &Program{
		HubDataPoint: NewHubDataPoint(centralName, name, description, true),
		ID:           id,
		IsInternal:   isInternal,
		Writer:       writer,
	}
	p.registerProgramServices()
	return p
}

// Button returns a [ProgramDpButton] view of this program. The returned value
// is non-nil whenever the program itself is non-nil.
func (p *Program) Button() *ProgramDpButton {
	return &ProgramDpButton{Program: p}
}

// Switch returns a [ProgramDpSwitch] view of this program. The returned
// value is non-nil whenever the program itself is non-nil.
func (p *Program) Switch() *ProgramDpSwitch {
	return &ProgramDpSwitch{Program: p}
}

// registerProgramServices wires Program operations onto the embedded ServiceRegistry.
func (p *Program) registerProgramServices() {
	p.RegisterService("trigger", func(ctx context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		// Fallback surface attribution: keep an ingress-stamped
		// operation when one is present, otherwise name the generic
		// service-invoke route so the execute audit never reads blank.
		// A nil ctx (tolerated by the registry contract) skips the stamp.
		if ctx != nil {
			if rc, ok := reqctx.FromContext(ctx); !ok || rc.Operation == "" {
				ctx = reqctx.WithOperation(ctx, "service:program-trigger")
			}
		}
		return p.Execute(ctx)
	})
}

// ProgramEventKind distinguishes the two things that can happen to a CCU
// program. They are separate controls, so a subscriber that only cares
// about one must be able to tell them apart: Success and Trigger are
// meaningless on an activity change, and the execution timestamp does not
// advance.
type ProgramEventKind string

const (
	// ProgramEventKindExecution reports a run of the program.
	ProgramEventKindExecution ProgramEventKind = "execution"
	// ProgramEventKindActivity reports a change of the activity flag —
	// the control that decides whether the program reacts at all.
	ProgramEventKindActivity ProgramEventKind = "activity"
)

// ProgramEvent describes an observable program state change.
type ProgramEvent struct {
	Kind    ProgramEventKind
	When    time.Time
	Active  bool
	Success bool
	Trigger hmenum.ProgramTrigger
}

// MQTTTopics implements [payload.MQTTAddressable] — the canonical
// ADR-0011 program topology. State carries the active-flag mirror so
// HA's `switch` entity can render its pip; Trigger is the HA → daemon
// invocation topic.
func (p *Program) MQTTTopics(base, centralName string) payload.MQTTTopicSet {
	if p.ID == "" {
		return payload.MQTTTopicSet{}
	}
	return payload.MQTTTopicSet{
		State:   naming.MQTTHubProgramState(base, centralName, p.ID),
		Set:     naming.MQTTHubProgramSet(base, centralName, p.ID),
		Trigger: naming.MQTTHubProgramTrigger(base, centralName, p.ID),
	}
}

// Role keys for the two controls a program surfaces. The activity toggle
// is the principal role and keeps the program's plain identity, which is
// what the switch already published before the execution grew a control
// of its own.
const (
	// ProgramRoleExecute addresses the "run it now" control.
	ProgramRoleExecute = "execute"
)

// MQTTRoles implements [payload.MQTTRoleAddressable].
//
// A CCU program is two controls, because the CCU treats it as two things:
// an activity flag that decides whether the program reacts at all, and an
// execution that runs it once. Running a deactivated program does
// nothing, so the execution carries its own availability topic, fed from
// [Program.State]'s ExecuteAvailable. Toggling activity has no such
// gate — it is what brings a deactivated program back.
func (p *Program) MQTTRoles(base, centralName string) []payload.MQTTRole {
	if p.ID == "" {
		return nil
	}
	return []payload.MQTTRole{
		{
			// Principal role: unchanged identity and topics.
			Component: "switch",
			Topics: payload.MQTTTopicSet{
				State: naming.MQTTHubProgramState(base, centralName, p.ID),
				Set:   naming.MQTTHubProgramSet(base, centralName, p.ID),
			},
		},
		{
			Key:        ProgramRoleExecute,
			Component:  "button",
			NameSuffix: "Execute",
			Topics: payload.MQTTTopicSet{
				Trigger:      naming.MQTTHubProgramTrigger(base, centralName, p.ID),
				Availability: naming.MQTTHubProgramExecuteAvailability(base, centralName, p.ID),
			},
		},
	}
}

// TranslationKey returns "program" as the HA entity translation key.
// The base HubDataPoint stub returns "" — Program overrides it so
// platform adapters can look up a human-readable display name without
// hard-coding the entity kind.
func (p *Program) TranslationKey() string { return "program" }

// Active returns the last observed enabled/disabled state and whether
// it has been observed yet.
func (p *Program) Active() (active, observed bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.active, p.hasActive
}

// LastExecution returns the timestamp of the most recent execution and
// whether one has been observed.
func (p *Program) LastExecution() (time.Time, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastExecute, !p.lastExecute.IsZero()
}

// LastExecuteTimeString returns the timestamp of the most recent
// execution as an RFC 3339 string, or an empty string when no
// execution has been recorded yet. This is the consistent string
// representation expected by north-bound adapters (MQTT / REST / WS)
// and mirrors ProgramDpSwitch (hub/switch.py) which returns an
// ISO-format string.
func (p *Program) LastExecuteTimeString() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.lastExecute.IsZero() {
		return ""
	}
	return p.lastExecute.UTC().Format(time.RFC3339)
}

// LastResult returns the last observed success/failure flag and
// whether one has been recorded.
func (p *Program) LastResult() (success, observed bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastResult, p.hasResult
}

// RuleSummary returns the compact, language-neutral summaries of the
// program's root rule: the trigger conditions and the resulting
// activities. Both are empty until [SetRuleSummary] has been called with
// non-empty values (the program has no rule, or rule scanning found none).
func (p *Program) RuleSummary() (condition, activity string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.conditionSummary, p.activitySummary
}

// SetRuleSummary records the compact rule summaries resolved by the hub
// scan. Safe to call on every refresh; the latest values win.
func (p *Program) SetRuleSummary(condition, activity string) {
	p.mu.Lock()
	p.conditionSummary = condition
	p.activitySummary = activity
	p.mu.Unlock()
}

// UpdateMetadata refreshes the mutable CCU-side fields on an existing
// program entry without replacing the pointer. Callers that hold a
// reference to this Program (e.g. via OnUpdate subscriptions) continue
// to observe the updated state without re-wiring.
func (p *Program) UpdateMetadata(name string, isInternal bool, writer ProgramWriter) {
	// The name lives on the embedded data point, which guards it with its
	// own lock; writing it under the program lock would leave the readers
	// (Signature / FullName / LegacyName) racing against the refresh.
	p.SetName(name)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.IsInternal = isInternal
	if writer != nil {
		p.Writer = writer
	}
}

// Internal reports whether the CCU created this program for its own
// bookkeeping (the Tmp_* family), so north-bound listings can omit it.
//
// The flag is refreshed in place by [UpdateMetadata] on every hub scan, so
// it is read under the program lock rather than off the field.
func (p *Program) Internal() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.IsInternal
}

// programWriter returns the currently installed execution backend.
//
// [UpdateMetadata] replaces the writer in place while commands are in
// flight, and an interface value is two words wide — reading the field
// directly can observe one half of the old value and one of the new. Every
// command path therefore takes one snapshot through this accessor and uses
// it for the whole call.
func (p *Program) programWriter() ProgramWriter {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Writer
}

// OnActive records an observed active/inactive state and notifies
// subscribers when the flag actually changed (a first observation counts as
// a change). Re-observing the same value on every hub scan is silent.
//
// The notification matters because the activity flag gates the program's
// other control: a deactivated program refuses to run, so a consumer
// offering "run now" has to be told when the answer flips. Without it, the
// only path that ever fired was [OnExecution] — leaving every consumer's
// view of execute-availability stale until the program next ran.
func (p *Program) OnActive(active bool) {
	p.mu.Lock()
	if p.hasActive && p.active == active {
		p.mu.Unlock()
		return
	}
	p.active = active
	p.hasActive = true
	cbs := make([]func(event ProgramEvent), len(p.callbacks))
	copy(cbs, p.callbacks)
	notifier := p.ActiveNotifier
	id := p.ID
	p.mu.Unlock()

	ev := ProgramEvent{Kind: ProgramEventKindActivity, When: time.Now(), Active: active}
	for _, cb := range cbs {
		if cb != nil {
			cb(ev)
		}
	}
	if notifier != nil {
		notifier(id, active)
	}
}

// SeedLastExecution records an execution the CCU performed on its own —
// a scheduled or condition-triggered run the daemon never saw — from the
// timestamp the hub scan reads off the CCU.
//
// It deliberately does NOT fire subscribers and does NOT touch the
// success flag: the run happened before this observation, often long
// before, and announcing it as a fresh [ProgramEventKindExecution] would
// replay every program's last run onto MQTT / WS / webhooks on each boot.
// The value only moves forward, so a refresh reporting an older
// timestamp than one the daemon itself observed cannot walk it back. A
// zero ts is ignored — the CCU reports "never ran" that way.
func (p *Program) SeedLastExecution(ts time.Time) {
	if ts.IsZero() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !ts.After(p.lastExecute) {
		return
	}
	p.lastExecute = ts
}

// OnExecution records an execution event emitted by the hub
// coordinator. Fires subscribers. Clears [HubDataPoint.StateUncertain]
func (p *Program) OnExecution(success bool, trigger hmenum.ProgramTrigger) {
	now := time.Now()
	p.mu.Lock()
	p.lastExecute = now
	p.lastResult = success
	p.hasResult = true
	cbs := make([]func(event ProgramEvent), len(p.callbacks))
	copy(cbs, p.callbacks)
	active := p.active
	p.mu.Unlock()
	p.markCertain()
	ev := ProgramEvent{Kind: ProgramEventKindExecution, When: now, Active: active, Success: success, Trigger: trigger}
	for _, cb := range cbs {
		if cb != nil {
			cb(ev)
		}
	}
}

// Execute runs the program via the writer and fires the ExecuteNotifier
// if one is wired. The notifier receives the trigger type API and success=true
// on a clean round-trip, success=false when the writer returns an error.
func (p *Program) Execute(ctx context.Context) error {
	w := p.programWriter()
	if w == nil {
		return fmt.Errorf("program %q: no writer configured", p.ID)
	}
	err := w.ExecuteProgram(ctx, p.ID)
	p.mu.RLock()
	notifier := p.ExecuteNotifier
	p.mu.RUnlock()
	if notifier != nil {
		notifier(ctx, p.ID, hmenum.ProgramTriggerAPI, err == nil)
	}
	return err
}

// ExecuteWithConditionCheck evaluates the program's "if" condition on the
// CCU and runs the program only when the condition is satisfied. It reports
// whether the program actually executed.
//
// When the configured writer implements [ConditionalProgramWriter] the
// condition is evaluated on the CCU; the ExecuteNotifier fires only when the
// program actually ran (or the round-trip failed), so a condition that is not
// met records neither an execution nor a notification. When the writer does
// not support condition checking, the call falls back to the unconditional
// [Program.Execute] path and reports executed=true on a clean round-trip.
func (p *Program) ExecuteWithConditionCheck(ctx context.Context) (bool, error) {
	w := p.programWriter()
	if w == nil {
		return false, fmt.Errorf("program %q: no writer configured", p.ID)
	}
	cw, ok := w.(ConditionalProgramWriter)
	if !ok {
		if err := p.Execute(ctx); err != nil {
			return false, err
		}
		return true, nil
	}
	executed, err := cw.ExecuteProgramConditional(ctx, p.ID)
	p.mu.RLock()
	notifier := p.ExecuteNotifier
	p.mu.RUnlock()
	if notifier != nil && (executed || err != nil) {
		notifier(ctx, p.ID, hmenum.ProgramTriggerAPI, err == nil)
	}
	return executed, err
}

// SetEnabled flips the program's active state.
func (p *Program) SetEnabled(ctx context.Context, enabled bool) error {
	w := p.programWriter()
	if w == nil {
		return fmt.Errorf("program %q: no writer configured", p.ID)
	}
	if err := w.SetProgramEnabled(ctx, p.ID, enabled); err != nil {
		return err
	}
	p.OnActive(enabled)
	return nil
}

// Delete removes the program from the CCU via the configured writer. The
// writer must implement [ProgramDeleter]; otherwise Delete returns
// [ErrProgramDeleteUnsupported]. Delete does not touch the hub cache — the
// owning [Hub.DeleteProgramRemote] drops the entry (and fires
// [Program.NotifyRemoved]) only after the CCU round-trip succeeds.
func (p *Program) Delete(ctx context.Context) error {
	w := p.programWriter()
	if w == nil {
		return fmt.Errorf("program %q: no writer configured", p.ID)
	}
	d, ok := w.(ProgramDeleter)
	if !ok {
		return ErrProgramDeleteUnsupported
	}
	return d.DeleteProgram(ctx, p.ID)
}

// OnUpdate registers a subscription for execution events. Returns an
// idempotent unsubscribe closure.
func (p *Program) OnUpdate(fn func(ProgramEvent)) func() {
	p.mu.Lock()
	p.callbacks = append(p.callbacks, fn)
	idx := len(p.callbacks) - 1
	p.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if idx < len(p.callbacks) {
				p.callbacks[idx] = nil
			}
		})
	}
}

// OnRemoved registers a lifecycle hook fired when [NotifyRemoved] is called
// (typically from [Hub.RemoveProgram]). Returns an idempotent unsubscribe
// closure.
func (p *Program) OnRemoved(fn func()) func() {
	p.mu.Lock()
	p.removedHandlers = append(p.removedHandlers, fn)
	idx := len(p.removedHandlers) - 1
	p.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if idx < len(p.removedHandlers) {
				p.removedHandlers[idx] = nil
			}
		})
	}
}

// NotifyRemoved fires every registered removal hook. Called by
// [Hub.RemoveProgram] right before the entry is dropped so subscribers
// can clean up MQTT topics, UI elements, etc.
func (p *Program) NotifyRemoved() {
	p.mu.Lock()
	cbs := make([]func(), len(p.removedHandlers))
	copy(cbs, p.removedHandlers)
	p.removedHandlers = nil
	p.mu.Unlock()
	for _, cb := range cbs {
		if cb != nil {
			cb()
		}
	}
}
