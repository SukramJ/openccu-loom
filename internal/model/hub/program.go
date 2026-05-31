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

	mu              sync.RWMutex
	active          bool
	hasActive       bool
	lastExecute     time.Time
	lastResult      bool
	hasResult       bool
	callbacks       []func(event ProgramEvent)
	removedHandlers []func()
}

// NewProgram constructs a [Program] with a fully initialised
// [datapoint.BaseDataPointFields] embedded in the [HubDataPoint] base.
//
// - central — the CentralUnit name for UniqueID scoping (multi-CCU safe).
// - id — the CCU program ID (the ISE object ID).
// - name — the CCU program name (used as both Name field and KeyName).
// - description — optional human-readable description.
// - isInternal — true for Tmp_*-programs created internally by the CCU.
// - writer — the execution backend; nil creates an execute-only program.
func NewProgram(central, id, name, description string, isInternal bool, writer ProgramWriter) *Program {
	p := &Program{
		HubDataPoint: NewHubDataPoint(central, name, description, true),
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
		return p.Execute(ctx)
	})
}

// ProgramEvent describes an observable program state change.
type ProgramEvent struct {
	When    time.Time
	Active  bool
	Success bool
	Trigger hmenum.ProgramTrigger
}

// MQTTTopics implements [payload.MQTTAddressable] — the canonical
// ADR-0011 program topology. State carries the active-flag mirror so
// HA's `switch` entity can render its pip; Trigger is the HA → daemon
// invocation topic.
func (p *Program) MQTTTopics(base, central string) payload.MQTTTopicSet {
	if p.ID == "" {
		return payload.MQTTTopicSet{}
	}
	return payload.MQTTTopicSet{
		State:   naming.MQTTHubProgramState(base, central, p.ID),
		Trigger: naming.MQTTHubProgramTrigger(base, central, p.ID),
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
// And mirrors
// ProgramDpSwitch (hub/switch.py) which returns an ISO-format string.
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

// UpdateMetadata refreshes the mutable CCU-side fields on an existing
// program entry without replacing the pointer. Callers that hold a
// reference to this Program (e.g. via OnUpdate subscriptions) continue
// to observe the updated state without re-wiring.
func (p *Program) UpdateMetadata(name string, isInternal bool, writer ProgramWriter) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Name = name
	p.IsInternal = isInternal
	if writer != nil {
		p.Writer = writer
	}
}

// OnActive records an observed active/inactive state.
func (p *Program) OnActive(active bool) {
	p.mu.Lock()
	p.active = active
	p.hasActive = true
	p.mu.Unlock()
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
	ev := ProgramEvent{When: now, Active: active, Success: success, Trigger: trigger}
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
	if p.Writer == nil {
		return fmt.Errorf("program %q: no writer configured", p.ID)
	}
	err := p.Writer.ExecuteProgram(ctx, p.ID)
	p.mu.RLock()
	notifier := p.ExecuteNotifier
	p.mu.RUnlock()
	if notifier != nil {
		notifier(ctx, p.ID, hmenum.ProgramTriggerAPI, err == nil)
	}
	return err
}

// SetEnabled flips the program's active state.
func (p *Program) SetEnabled(ctx context.Context, enabled bool) error {
	if p.Writer == nil {
		return fmt.Errorf("program %q: no writer configured", p.ID)
	}
	if err := p.Writer.SetProgramEnabled(ctx, p.ID, enabled); err != nil {
		return err
	}
	p.OnActive(enabled)
	return nil
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
