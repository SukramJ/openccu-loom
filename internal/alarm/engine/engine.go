// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// Sentinel errors returned by the engine's verbs.
var (
	// ErrUnknownArea reports an area ID the engine does not manage.
	ErrUnknownArea = errors.New("engine: unknown area")
	// ErrUnknownMode reports a mode the area does not configure.
	ErrUnknownMode = errors.New("engine: mode not configured for area")
	// ErrInvalidState reports a verb not allowed in the current
	// state (e.g. arming a triggered area). Disarm and silence never
	// return it — they are never state-gated.
	ErrInvalidState = errors.New("engine: action not allowed in current state")
	// ErrNoIncident reports an acknowledge without an open incident.
	ErrNoIncident = errors.New("engine: no open incident")
	// ErrInvalidCode reports a verb refused because a required alarm
	// code was missing or did not authenticate (docs/alarm-concept.md
	// §11). The CodeValidator returns it; the engine surfaces it.
	ErrInvalidCode = errors.New("engine: invalid alarm code")
)

// Code verbs passed to the CodeValidator. The strings are part of the
// port contract — keep them stable.
const (
	codeVerbArm     = "arm"
	codeVerbDisarm  = "disarm"
	codeVerbSilence = "silence"
)

// Strongly-authenticated sources that bypass a code requirement
// (docs/alarm-concept.md §11 degradation). They still surface duress
// when a code is supplied. These tokens are the operator-session /
// break-glass surfaces; anonymous MQTT / sysvar / keypad paths are not
// listed and stay code-gated.
const (
	codeSourceRESTOperator = "rest-operator"
	codeSourceWSOperator   = "ws-operator"
	codeSourceHmcli        = "hmcli"
)

// isOperatorSource reports whether source bypasses code requirements.
func isOperatorSource(source string) bool {
	switch source {
	case codeSourceRESTOperator, codeSourceWSOperator, codeSourceHmcli:
		return true
	default:
		return false
	}
}

// NotReadyError reports a refused arm together with the blocking
// sensors, so surfaces can render the bypass sheet.
type NotReadyError struct {
	Blockers []string
}

// Error implements error.
func (e *NotReadyError) Error() string {
	return fmt.Sprintf("engine: not ready to arm: %d blocking sensor(s)", len(e.Blockers))
}

// Incident cause kinds persisted in alarm_incidents.cause_json.
const (
	causeKindSensor         = "sensor"
	causeKindPendingElapsed = "pending_elapsed"
	causeKindCentralLost    = "central_lost"
	causeKindUnavailable    = "sensor_unavailable"
	causeKindDowntime       = "activation_during_downtime"
	causeKindAdopted        = "adopted"
	causeKindHazard         = "hazard"
	causeKindPanic          = "panic"
)

// Incident close reasons persisted in alarm_incidents.close_reason.
const (
	closeReasonDisarm      = "disarm"
	closeReasonPostTrigger = "post_trigger"
	closeReasonLost        = "incident_lost"
)

// incidentCause is the persisted cause document of an incident.
type incidentCause struct {
	Kind       string `json:"kind"`
	SensorID   string `json:"sensor_id,omitempty"`
	SensorName string `json:"sensor_name,omitempty"`
	Central    string `json:"central,omitempty"`
}

// Deps wires the engine's ports. Stores are required; every other
// dependency has a safe default.
type Deps struct {
	Clock        clock.Clock
	Scheduler    TimerScheduler
	Areas        AreaStore
	Sensors      SensorStore
	State        StateStore
	Incidents    IncidentStore
	Runtime      RuntimeStore
	Outputs      OutputPort
	Sink         EventSink
	Journal      Journal
	SensorReader SensorReader
	// Validator resolves alarm codes for the code-policy checks. A nil
	// validator disables codes entirely: every CodePolicy is inert.
	Validator CodeValidator
	Logger    *slog.Logger
	// RestartLoopBreakerK caps restore-driven re-fires per incident
	// before output degradation; 0 selects the default.
	RestartLoopBreakerK int
}

// Engine hosts one arm-state machine per alarm area. All mutating
// entry points (verbs, sensor events, timer fires) serialize on one
// mutex; persistence is write-through under that lock so the stored
// state never runs ahead of or behind the in-memory state.
type Engine struct {
	clk          clock.Clock
	sched        TimerScheduler
	areasStore   AreaStore
	sensorsStore SensorStore
	stateStore   StateStore
	incidents    IncidentStore
	runtime      RuntimeStore
	outputs      OutputPort
	sink         EventSink
	journal      Journal
	reader       SensorReader
	validator    CodeValidator
	log          *slog.Logger
	loopBreakerK int

	mu          sync.Mutex
	started     bool
	bootCount   int64
	areas       map[string]*area
	sensorIndex map[string]string // sensor ID → area ID

	// lifeCtx bounds timer-driven work. Timer fires outlive the
	// request that scheduled them, so they deliberately detach from
	// the caller's context and run on the engine lifetime instead
	// (set by Start; one of the sanctioned ctx-in-struct seams).
	lifeCtx context.Context
}

// New constructs an engine. Call Start to load configuration and
// restore persisted state.
func New(deps Deps) (*Engine, error) {
	if deps.Areas == nil || deps.Sensors == nil || deps.State == nil || deps.Incidents == nil || deps.Runtime == nil {
		return nil, errors.New("engine: missing required store dependency")
	}
	clk := deps.Clock
	if clk == nil {
		clk = clock.New()
	}
	sched := deps.Scheduler
	if sched == nil {
		sched = NewClockScheduler(clk)
	}
	outputs := deps.Outputs
	if outputs == nil {
		outputs = noopOutputs{}
	}
	sink := deps.Sink
	if sink == nil {
		sink = noopSink{}
	}
	journal := deps.Journal
	if journal == nil {
		journal = noopJournal{}
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	k := deps.RestartLoopBreakerK
	if k <= 0 {
		k = DefaultRestartLoopBreakerK
	}
	return &Engine{
		clk:          clk,
		sched:        sched,
		areasStore:   deps.Areas,
		sensorsStore: deps.Sensors,
		stateStore:   deps.State,
		incidents:    deps.Incidents,
		runtime:      deps.Runtime,
		outputs:      outputs,
		sink:         sink,
		journal:      journal,
		reader:       deps.SensorReader,
		validator:    deps.Validator,
		log:          logger,
		loopBreakerK: k,
		areas:        map[string]*area{},
		sensorIndex:  map[string]string{},
		lifeCtx:      context.Background(),
	}, nil
}

// Stop cancels all timers and persists a final state snapshot with
// fresh remaining durations, so the next Start restores precisely.
func (e *Engine) Stop(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return
	}
	e.started = false
	for _, a := range e.areas {
		e.persist(ctx, a)
		a.cancelTimers()
	}
}

// Areas returns snapshots of every managed area, ordered by ID.
func (e *Engine) Areas() []AreaSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clk.Now()
	out := make([]AreaSnapshot, 0, len(e.areas))
	for _, a := range e.areas {
		out = append(out, a.snapshot(now))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Area returns the snapshot of one area.
func (e *Engine) Area(id string) (AreaSnapshot, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.areas[id]
	if !ok {
		return AreaSnapshot{}, false
	}
	return a.snapshot(e.clk.Now()), true
}

// ArmRequest parameterizes an arm verb.
type ArmRequest struct {
	Mode hmenum.AlarmMode
	// Force arms despite blockers; every remaining blocker is
	// recorded on the bypass list and journaled — nothing is bypassed
	// silently.
	Force bool
	// Bypass lists sensor IDs the caller explicitly bypasses.
	Bypass []string
	// SkipDelay arms without the exit delay.
	SkipDelay bool
	// Code is the alarm code supplied with the arm, when the area's
	// CodePolicy requires one (or to surface a duress code). Empty when
	// none was supplied.
	Code   string
	By     string
	Source string
}

// ArmResult reports the outcome of an accepted arm.
type ArmResult struct {
	State     hmenum.AlarmAreaState
	Bypassed  []string
	ExitDelay time.Duration
}

// Arm starts arming areaID into req.Mode. It fails with
// *NotReadyError when blockers remain and Force is not set; pending
// and triggered areas must be disarmed first.
func (e *Engine) Arm(ctx context.Context, areaID string, req ArmRequest) (ArmResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// Arm is refused on an engine that is not running; silence and
	// disarm stay deliberately ungated (S3/S6).
	if !e.started {
		return ArmResult{}, ErrInvalidState
	}
	a, ok := e.areas[areaID]
	if !ok {
		return ArmResult{}, ErrUnknownArea
	}
	if !req.Mode.Armed() {
		return ArmResult{}, ErrUnknownMode
	}
	mcfg, ok := a.cfg.Modes[req.Mode]
	if !ok {
		return ArmResult{}, ErrUnknownMode
	}
	if a.state == hmenum.AlarmAreaStatePending || a.state == hmenum.AlarmAreaStateTriggered {
		return ArmResult{}, ErrInvalidState
	}

	identity, duress, cerr := e.authorize(ctx, a, codeVerbArm, req.Code, req.Source)
	if cerr != nil {
		return ArmResult{}, cerr
	}
	if identity != "" && req.By == "" {
		req.By = identity
	}
	if duress {
		e.fireDuress(ctx, a, codeVerbArm, req.By, req.Source)
	}
	return e.beginArm(ctx, a, req, mcfg)
}

// beginArm resolves blockers and drives the disarmed→arming/armed
// transition; the caller holds the lock and has validated the mode and
// any code. It is shared by the public Arm verb and the auto-rearm
// timer, and honors Force / Bypass exactly as Arm documents.
func (e *Engine) beginArm(ctx context.Context, a *area, req ArmRequest, mcfg ModeConfig) (ArmResult, error) {
	// A fresh arm supersedes any pending auto-rearm.
	a.cancelAutoRearm()

	// Resolve blockers against the requested + automatic bypasses.
	bypass := map[string]bool{}
	for _, id := range req.Bypass {
		if _, exists := a.sensors[id]; exists {
			bypass[id] = true
		}
	}
	rd, autoBypass := a.readinessDetail(req.Mode)
	for _, id := range autoBypass {
		bypass[id] = true
	}
	var remaining []string
	for _, id := range rd.Blockers {
		if !bypass[id] {
			remaining = append(remaining, id)
		}
	}
	if len(remaining) > 0 && !req.Force {
		return ArmResult{}, &NotReadyError{Blockers: remaining}
	}
	for _, id := range remaining {
		bypass[id] = true
	}

	from := a.state
	a.cancelTimers()
	a.bypassed = bypass
	a.mode = req.Mode
	a.pendingCause = ""
	a.openAtArm = map[string]bool{}

	for id := range bypass {
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassBypass, Event: "sensor_bypassed",
			Actor: req.By, Source: req.Source, Details: map[string]any{"sensor_id": id},
		})
	}

	exit := time.Duration(mcfg.ExitDelaySeconds) * time.Second
	if req.SkipDelay {
		exit = 0
	}
	res := ArmResult{ExitDelay: exit}
	for id := range bypass {
		res.Bypassed = append(res.Bypassed, id)
	}
	sort.Strings(res.Bypassed)

	if exit > 0 {
		a.state = hmenum.AlarmAreaStateArming
		e.scheduleStateTimer(a, timerKindExit, exit)
		e.startTicks(a, timerKindExit)
		e.persist(ctx, a)
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassArm, Event: "arming_started",
			Actor: req.By, Source: req.Source,
			Details: map[string]any{"mode": string(req.Mode), "exit_delay_s": int(exit.Seconds())},
		})
		e.publishState(a, from, req.By, req.Source)
	} else {
		e.completeArm(ctx, a, from, req.By, req.Source)
	}
	res.State = a.state
	return res, nil
}

// completeArm finishes the transition into armed: it captures the
// open-at-arm baseline and persists. The caller holds the lock.
func (e *Engine) completeArm(ctx context.Context, a *area, from hmenum.AlarmAreaState, by, source string) {
	a.cancelTimers()
	a.state = hmenum.AlarmAreaStateArmed
	e.recaptureOpenBaseline(a)
	e.persist(ctx, a)
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassArm, Event: "armed",
		Actor: by, Source: source, Details: map[string]any{"mode": string(a.mode)},
	})
	e.publishState(a, from, by, source)
	if a.cfg.Modes[a.mode].Outputs.ArmDisarmChirps {
		e.chirp(ctx, a, ChirpRequest{Kind: ChirpArmSquawk})
	}
}

// recaptureOpenBaseline records the member sensors that are active for
// the current mode as the open-at-arm baseline, so a door still standing
// open does not instantly (re-)trigger. The caller holds the lock.
func (e *Engine) recaptureOpenBaseline(a *area) {
	a.openAtArm = map[string]bool{}
	for id, s := range a.sensors {
		if s.cfg.InMode(a.mode) && !a.bypassed[id] && s.activeKnown && s.active {
			a.openAtArm[id] = true
		}
	}
}

// authorize applies the area's CodePolicy to one verb. It returns the
// resolved code identity (for changed-by attribution), whether the code
// is a duress code, and a refusal error (ErrInvalidCode) when a required
// code is missing or wrong. A nil CodeValidator disables codes entirely.
// Operator sources bypass the requirement but still get duress detection
// on a supplied code. The caller holds the lock.
func (e *Engine) authorize(ctx context.Context, a *area, verb, code, source string) (identity string, duress bool, refuse error) {
	if e.validator == nil {
		return "", false, nil
	}
	required := a.cfg.CodePolicy.requires(verb, source)
	if isOperatorSource(source) {
		required = false
	}
	if !required && code == "" {
		return "", false, nil
	}
	identity, duress, err := e.validator.Validate(ctx, a.id, verb, code, source)
	if err != nil {
		if isOperatorSource(source) {
			// The operator session is the second factor: a bad or absent
			// code never blocks it (§11 break-glass). No duress on a code
			// that did not authenticate.
			return "", false, nil
		}
		return "", false, err
	}
	return identity, duress, nil
}

// fireDuress emits the silent duress fan-out: a Hidden journal entry
// (the journal facade suppresses the append event for hidden rows) plus
// a dedicated AlarmDuressEvent on the bus for the MQTT/webhook
// consumers. The verb itself proceeds normally. The caller holds the
// lock.
func (e *Engine) fireDuress(ctx context.Context, a *area, verb, by, source string) {
	incID := int64(0)
	if a.incident != nil {
		incID = a.incident.ID
	}
	e.journalEntry(ctx, a, JournalEntry{
		Class: duressJournalClass(verb), Event: "duress",
		Actor: by, Source: source, IncidentID: incID, Hidden: true,
	})
	e.sink.Publish(hmevent.AlarmDuressEvent{
		Base:   hmevent.NewBaseAt(e.clk.Now()),
		AreaID: a.id, AreaName: a.name,
		Verb: verb, By: by, Source: source, IncidentID: incID,
	})
}

// duressJournalClass buckets a duress entry under the verb it
// accompanied.
func duressJournalClass(verb string) hmenum.AlarmJournalClass {
	switch verb {
	case codeVerbArm:
		return hmenum.AlarmJournalClassArm
	case codeVerbSilence:
		return hmenum.AlarmJournalClassSilence
	default:
		return hmenum.AlarmJournalClassDisarm
	}
}

// Disarm ends any alarm and returns the area to disarmed. It is never
// state-gated (S6) and implies silence: the open incident is marked
// silenced and closed, and all outputs stop. It is a code-free wrapper
// over DisarmWithCode for the many call sites that never carry a code.
func (e *Engine) Disarm(ctx context.Context, areaID, by, source string) error {
	return e.DisarmWithCode(ctx, areaID, by, source, "")
}

// DisarmWithCode is Disarm with an alarm code, honoring the area's
// CodePolicy (docs/alarm-concept.md §11). An already-disarmed area
// short-circuits before the code check: disarming a disarmed area is an
// idempotent no-op and needs no security decision.
func (e *Engine) DisarmWithCode(ctx context.Context, areaID, by, source, code string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.areas[areaID]
	if !ok {
		return ErrUnknownArea
	}
	if a.state == hmenum.AlarmAreaStateDisarmed {
		// An explicit disarm of an already-disarmed area is a no-op, but
		// it cancels any pending auto-rearm — the operator wants it to
		// stay off.
		if a.autoRearmCancel != nil {
			a.cancelAutoRearm()
			e.persist(ctx, a)
			e.journalEntry(ctx, a, JournalEntry{
				Class: hmenum.AlarmJournalClassArm, Event: "auto_rearm_cancelled",
				Actor: by, Source: source,
			})
		}
		return nil
	}
	identity, duress, cerr := e.authorize(ctx, a, codeVerbDisarm, code, source)
	if cerr != nil {
		return cerr
	}
	if identity != "" && by == "" {
		by = identity
	}
	if duress {
		e.fireDuress(ctx, a, codeVerbDisarm, by, source)
	}
	prevPolicy := a.cfg.Modes[a.mode].Outputs
	e.disarmLocked(ctx, a, by, source)
	e.refreshReadiness(a)
	if prevPolicy.ArmDisarmChirps {
		e.chirp(ctx, a, ChirpRequest{Kind: ChirpDisarmSquawk})
	}
	return nil
}

// Silence stops all sounding outputs of the area's current incident
// and persists the silenced flag so no engine path — including a
// restore — re-fires acoustic outputs for it (S3). State is
// unaffected; notification outputs are never cancelled. Silence never
// fails on state and is deliberately not gated. It is a code-free
// wrapper over SilenceWithCode.
func (e *Engine) Silence(ctx context.Context, areaID, by, source string) error {
	return e.SilenceWithCode(ctx, areaID, by, source, "")
}

// SilenceWithCode is Silence with an alarm code. Silence defaults to
// code-free (S3); a per-source RequireSilence policy or a supplied code
// engages the CodeValidator (docs/alarm-concept.md §11).
func (e *Engine) SilenceWithCode(ctx context.Context, areaID, by, source, code string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.areas[areaID]
	if !ok {
		return ErrUnknownArea
	}
	identity, duress, cerr := e.authorize(ctx, a, codeVerbSilence, code, source)
	if cerr != nil {
		return cerr
	}
	if identity != "" && by == "" {
		by = identity
	}
	if duress {
		e.fireDuress(ctx, a, codeVerbSilence, by, source)
	}
	e.silenceLocked(ctx, a, by, source)
	return nil
}

// SilenceAll silences every area (the global silence surface).
func (e *Engine) SilenceAll(ctx context.Context, by, source string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, id := range e.sortedAreaIDs() {
		e.silenceLocked(ctx, e.areas[id], by, source)
	}
}

// silenceLocked applies the silence verb to one area. Even without an
// open incident it issues a StopAll — stopping more than necessary is
// always the safe direction. The caller holds the lock.
func (e *Engine) silenceLocked(ctx context.Context, a *area, by, source string) {
	if a.incident != nil {
		e.silenceIncident(ctx, a, by, source)
		// The state row carries the redundant silence marker, so this
		// persist is the second durability path for S3.
		e.persist(ctx, a)
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassSilence, Event: "silenced",
			Actor: by, Source: source, IncidentID: a.incident.ID,
		})
		return
	}
	if err := e.outputs.StopAll(ctx, a.id, 0); err != nil {
		e.journalFault(ctx, a, "output_stop_failed", err, 0)
	}
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassSilence, Event: "silence_requested",
		Actor: by, Source: source,
	})
}

// silenceIncident marks the open incident silenced (in memory first —
// that can never be lost to an I/O failure), persists the flag, and
// stops outputs. The caller holds the lock.
func (e *Engine) silenceIncident(ctx context.Context, a *area, by, source string) {
	inc := a.incident
	if inc == nil || inc.Silenced {
		if inc != nil {
			// Repeat silences still counter-stop outputs.
			if err := e.outputs.StopAll(ctx, a.id, inc.ID); err != nil {
				e.journalFault(ctx, a, "output_stop_failed", err, inc.ID)
			}
		}
		return
	}
	nowMS := unixMS(e.clk.Now())
	inc.Silenced = true
	inc.SilencedAtMS = nowMS
	inc.SilencedBy = by
	if source != "" && by == "" {
		inc.SilencedBy = source
	}
	a.silencedIncidentID = inc.ID
	if inc.ID != 0 {
		if err := e.incidents.MarkSilenced(ctx, inc.ID, nowMS, inc.SilencedBy); err != nil {
			// Not fatal for S3: the state-row marker
			// (silencedIncidentID) is persisted independently and a
			// restore honors either record.
			e.journalFault(ctx, a, "silence_persist_failed", err, inc.ID)
		}
	}
	if err := e.outputs.StopAll(ctx, a.id, inc.ID); err != nil {
		e.journalFault(ctx, a, "output_stop_failed", err, inc.ID)
	}
}

// Acknowledge marks the area's open incident as seen. Journal-only —
// no state effect.
func (e *Engine) Acknowledge(ctx context.Context, areaID, by, source string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.areas[areaID]
	if !ok {
		return ErrUnknownArea
	}
	if a.incident == nil {
		return ErrNoIncident
	}
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassSilence, Event: "acknowledged",
		Actor: by, Source: source, IncidentID: a.incident.ID,
	})
	return nil
}

// HandleSensorEvent feeds a normalized sensor activation transition
// into the state machine. Wiring layers translate data-point events
// into these calls.
func (e *Engine) HandleSensorEvent(ctx context.Context, sensorID string, active bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return
	}
	a, s := e.lookupSensor(sensorID)
	if a == nil {
		return
	}
	wasActive := s.activeKnown && s.active
	s.active = active
	s.activeKnown = true
	defer e.refreshReadiness(a)

	if s.row.SensorType == hmenum.AlarmSensorTypeTamper && active &&
		a.state == hmenum.AlarmAreaStateDisarmed {
		e.journalFault(ctx, a, "tamper_while_disarmed", nil, 0)
	}

	// Always-on hazard/panic sensors bypass the arm-state machine: a
	// fresh activation fires the class output policy 24/7, independent
	// of area state. Placed before the walk-test consumer so a real
	// hazard during a walk test is never suppressed.
	if s.cfg.AlwaysOn && active && !wasActive {
		e.alwaysOnFromSensor(ctx, a, s)
		return
	}

	// A running walk test consumes activations of the disarmed area.
	if e.walkTestObserve(ctx, a, sensorID, active) && a.state == hmenum.AlarmAreaStateDisarmed {
		return
	}

	if !active {
		if a.openAtArm[sensorID] {
			delete(a.openAtArm, sensorID)
			e.persist(ctx, a)
		}
		// Closing during the exit delay may complete the arm early.
		if a.state == hmenum.AlarmAreaStateArming && s.cfg.ArmAfterClosing && s.cfg.InMode(a.mode) {
			e.scheduleArmCloseDebounce(a, sensorID)
		}
		return
	}
	if wasActive {
		return
	}
	// While disarmed, a member sensor's activity defers a pending
	// auto-rearm (the quiet period restarts) and — if the sensor is a
	// chime source — sounds the door chime. Neither happens during a
	// walk test: the consumer above already returned.
	if a.state == hmenum.AlarmAreaStateDisarmed {
		if a.autoRearmCancel != nil && s.cfg.InMode(a.autoRearmMode) {
			e.deferAutoRearm(ctx, a, sensorID)
		}
		if s.cfg.Chime {
			e.chirp(ctx, a, ChirpRequest{Kind: ChirpChime})
		}
	}
	if !s.cfg.InMode(a.mode) || a.bypassed[sensorID] {
		return
	}
	e.dispatchSensorActivation(ctx, a, s, sensorID)
}

// dispatchSensorActivation routes a fresh member-sensor activation
// through the arm-state machine. The caller holds the lock and has
// already filtered walk tests, always-on sensors, bypasses, and
// mode membership.
func (e *Engine) dispatchSensorActivation(ctx context.Context, a *area, s *sensorState, sensorID string) {
	cause := incidentCause{Kind: causeKindSensor, SensorID: sensorID, SensorName: s.row.Name}

	switch a.state {
	case hmenum.AlarmAreaStateArming:
		// Instant sensors trigger during the exit delay; sensors
		// flagged use_exit_delay may be active while leaving.
		if !s.cfg.UseExitDelay {
			e.trigger(ctx, a, cause, FireOptions{})
		}
	case hmenum.AlarmAreaStateArmed:
		e.routeActivation(ctx, a, s, cause)
	case hmenum.AlarmAreaStatePending:
		// An instant sensor escalates immediately; delayed sensors
		// are journaled but do not extend the countdown.
		if !s.cfg.UseEntryDelay {
			e.trigger(ctx, a, cause, FireOptions{})
		} else {
			e.journalEntry(ctx, a, JournalEntry{
				Class: hmenum.AlarmJournalClassTrigger, Event: "sensor_activity_pending",
				Details: map[string]any{"sensor_id": sensorID},
			})
		}
	case hmenum.AlarmAreaStateTriggered:
		// Journaled for verification value; no new output cycles
		// beyond the incident's re-trigger policy.
		incID := int64(0)
		if a.incident != nil {
			incID = a.incident.ID
		}
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassTrigger, Event: "sensor_activity",
			IncidentID: incID, Details: map[string]any{"sensor_id": sensorID},
		})
	case hmenum.AlarmAreaStateDisarmed:
	}
}

// routeActivation routes an armed-state activation into pending or an
// instant trigger, per the sensor's flags. The caller holds the lock.
func (e *Engine) routeActivation(ctx context.Context, a *area, s *sensorState, cause incidentCause) {
	mcfg := a.cfg.Modes[a.mode]
	if s.cfg.UseEntryDelay {
		if d := mcfg.entryDelay(s.cfg); d > 0 {
			from := a.state
			a.state = hmenum.AlarmAreaStatePending
			a.pendingCause = cause.SensorID
			e.scheduleStateTimer(a, timerKindEntry, d)
			e.startTicks(a, timerKindEntry)
			e.persist(ctx, a)
			e.journalEntry(ctx, a, JournalEntry{
				Class: hmenum.AlarmJournalClassTrigger, Event: "pending_started",
				Details: map[string]any{"sensor_id": cause.SensorID, "entry_delay_s": int(d.Seconds())},
			})
			e.publishState(a, from, "", "engine")
			return
		}
	}
	e.trigger(ctx, a, cause, FireOptions{})
}

// SensorHealth carries device health flags for one sensor.
type SensorHealth struct {
	Sabotage   bool
	LowBattery bool
}

// SetSensorAvailability updates reachability. While armed, an
// unavailable member sensor either triggers (per its flag) or raises
// a fail-visible warning — never nothing.
func (e *Engine) SetSensorAvailability(ctx context.Context, sensorID string, available bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return
	}
	a, s := e.lookupSensor(sensorID)
	if a == nil || s.available == available {
		return
	}
	s.available = available
	defer e.refreshReadiness(a)
	if available || !e.isArmedState(a.state) || !s.cfg.InMode(a.mode) || a.bypassed[sensorID] {
		return
	}
	// The activation route (pending/instant) only applies from armed:
	// during arming or pending it would replace the running countdown
	// (§5 documents no such edge), so those states warn instead.
	if s.cfg.TriggerWhenUnavailable && a.state == hmenum.AlarmAreaStateArmed {
		e.routeActivation(ctx, a, s, incidentCause{
			Kind: causeKindUnavailable, SensorID: sensorID, SensorName: s.row.Name,
		})
		return
	}
	e.journalFault(ctx, a, "sensor_unavailable_while_armed", nil, 0)
}

// SetSensorHealth updates the sabotage / low-battery flags. Sabotage
// raises a fault entry in every state (24/7 tamper visibility).
func (e *Engine) SetSensorHealth(ctx context.Context, sensorID string, h SensorHealth) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return
	}
	a, s := e.lookupSensor(sensorID)
	if a == nil {
		return
	}
	newSabotage := h.Sabotage && !s.sabotage
	s.sabotage = h.Sabotage
	s.lowBattery = h.LowBattery
	if newSabotage {
		e.journalFault(ctx, a, "sensor_sabotage", nil, 0)
	}
	e.refreshReadiness(a)
}

// HandleCentralConnectivity degrades or restores every sensor of a
// central. An armed area with member sensors on a lost central reacts
// per its central-loss policy — loudly, never silently.
func (e *Engine) HandleCentralConnectivity(ctx context.Context, centralName string, connected bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return
	}
	for _, id := range e.sortedAreaIDs() {
		a := e.areas[id]
		affected := false
		var firstAffected *sensorState
		for _, s := range a.sensors {
			if s.row.CentralName != centralName {
				continue
			}
			s.available = connected
			if s.cfg.InMode(a.mode) && !a.bypassed[s.row.ID] {
				affected = true
				if firstAffected == nil {
					firstAffected = s
				}
			}
		}
		if !affected {
			continue
		}
		e.refreshReadiness(a)
		if connected {
			e.journalEntry(ctx, a, JournalEntry{
				Class: hmenum.AlarmJournalClassFault, Event: "central_restored",
				Details: map[string]any{"central": centralName},
			})
			continue
		}
		if !e.isArmedState(a.state) {
			continue
		}
		if a.cfg.CentralLoss == hmenum.AlarmCentralLossTrigger {
			e.trigger(ctx, a, incidentCause{Kind: causeKindCentralLost, Central: centralName}, FireOptions{})
			continue
		}
		e.journalFault(ctx, a, "central_lost_while_armed", nil, 0)
	}
}

// trigger opens (or reuses) the area's incident and enters triggered.
// Ordering is safety-first: the incident with its counters is
// persisted before outputs fire, so a crash can only over-count.
// A persist failure must not mute the alarm — the engine then runs an
// unpersisted incident and journals the degradation (S7).
// The caller holds the lock.
func (e *Engine) trigger(ctx context.Context, a *area, cause incidentCause, opts FireOptions) {
	if a.state == hmenum.AlarmAreaStateTriggered {
		return
	}
	from := a.state
	mcfg := a.cfg.Modes[a.mode]
	opts.Policy = mcfg.Outputs
	now := e.clk.Now()
	dur := mcfg.triggerDuration()

	newIncident := a.incident == nil
	if newIncident {
		causeJSON, err := json.Marshal(cause)
		if err != nil {
			causeJSON = []byte("{}")
		}
		inc := sqlitestore.AlarmIncident{
			AreaID:            a.id,
			Mode:              a.mode,
			CauseJSON:         string(causeJSON),
			StartedAtMS:       unixMS(now),
			TriggerDeadlineMS: unixMS(now.Add(dur)),
		}
		id, err := e.incidents.Create(ctx, inc)
		if err != nil {
			e.journalFault(ctx, a, "incident_persist_failed", err, 0)
		} else {
			inc.ID = id
		}
		a.incident = &inc
	}

	// Two-phase pre-alarm (docs/alarm-concept.md §15 row 21): a fresh,
	// live (not restored) intrusion trigger with PreAlarmSeconds first
	// runs a pre-alarm phase — only the pre-alarm output classes fire —
	// then escalates to the full policy on elapse. A restore treats a
	// persisted pre-alarm phase as a full trigger, so it never re-enters
	// the pre-alarm phase here.
	preAlarm := newIncident && !opts.Restored && mcfg.PreAlarmSeconds > 0

	a.state = hmenum.AlarmAreaStateTriggered
	a.pendingCause = ""
	a.preAlarm = preAlarm

	if preAlarm {
		opts.PreAlarm = true
		e.scheduleStateTimer(a, timerKindPreAlarm, time.Duration(mcfg.PreAlarmSeconds)*time.Second)
	} else {
		e.scheduleStateTimer(a, timerKindTrigger, dur)
	}
	e.persist(ctx, a)

	if err := e.outputs.FireCycle(ctx, a.id, *a.incident, opts); err != nil {
		e.journalFault(ctx, a, "output_fire_failed", err, a.incident.ID)
	}
	event := "triggered"
	if preAlarm {
		event = "pre_alarm_started"
	}
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTrigger, Event: event,
		IncidentID: a.incident.ID,
		Details: map[string]any{
			"cause": cause.Kind, "sensor_id": cause.SensorID, "mode": string(a.mode),
		},
	})
	e.sink.Publish(hmevent.AlarmTriggeredEvent{
		Base:   hmevent.NewBaseAt(now),
		AreaID: a.id, AreaName: a.name,
		IncidentID: a.incident.ID,
		SensorID:   cause.SensorID, SensorName: cause.SensorName,
		Cause: cause.Kind, Mode: a.mode,
	})
	e.publishState(a, from, "", "engine")
}

// onPreAlarmElapsed escalates a pre-alarm phase to the full trigger, or
// finishes the incident when it was silenced during the pre-alarm phase
// (a silence during pre-alarm cancels the full escalation). The caller
// holds the lock.
func (e *Engine) onPreAlarmElapsed(ctx context.Context, a *area) {
	a.preAlarm = false
	inc := a.incident
	if inc != nil && inc.Silenced {
		// The full phase was cancelled by silence — leave triggered via
		// the post-trigger policy, like a silenced full phase does.
		e.finishTriggered(ctx, a, "pre_alarm_silenced")
		return
	}
	mcfg := a.cfg.Modes[a.mode]
	dur := mcfg.triggerDuration()
	if inc != nil {
		inc.TriggerDeadlineMS = unixMS(e.clk.Now().Add(dur))
		if inc.ID != 0 {
			if err := e.incidents.SetTriggerDeadline(ctx, inc.ID, inc.TriggerDeadlineMS); err != nil {
				e.journalFault(ctx, a, "incident_persist_failed", err, inc.ID)
			}
		}
	}
	e.scheduleStateTimer(a, timerKindTrigger, dur)
	e.persist(ctx, a)
	if inc != nil {
		if err := e.outputs.FireCycle(ctx, a.id, *inc, FireOptions{Policy: mcfg.Outputs}); err != nil {
			e.journalFault(ctx, a, "output_fire_failed", err, inc.ID)
		}
	}
	incID := int64(0)
	if inc != nil {
		incID = inc.ID
	}
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTrigger, Event: "pre_alarm_escalated", IncidentID: incID,
	})
}

// onTriggerElapsed runs when the trigger-time deadline fires: either
// the next bounded re-trigger cycle starts (accounted before firing;
// an accounting failure skips the cycle — the safe direction), or the
// post-trigger policy executes. The caller holds the lock.
func (e *Engine) onTriggerElapsed(ctx context.Context, a *area) {
	// Always-on (hazard/panic) incidents do not re-trigger; they return
	// to the state they interrupted.
	if a.preTriggerState != "" {
		e.finishAlwaysOn(ctx, a, "always_on_elapsed", "engine")
		return
	}
	inc := a.incident
	mcfg := a.cfg.Modes[a.mode]
	if inc != nil && !inc.Silenced && inc.RetriggerCycles < mcfg.MaxRetriggerCycles {
		accounted := true
		if inc.ID != 0 {
			if err := e.incidents.IncrementRetriggerCycles(ctx, inc.ID); err != nil {
				accounted = false
				e.journalFault(ctx, a, "retrigger_account_failed", err, inc.ID)
			}
		}
		if accounted {
			inc.RetriggerCycles++
			dur := mcfg.triggerDuration()
			deadline := e.clk.Now().Add(dur)
			inc.TriggerDeadlineMS = unixMS(deadline)
			if inc.ID != 0 {
				if err := e.incidents.SetTriggerDeadline(ctx, inc.ID, inc.TriggerDeadlineMS); err != nil {
					e.journalFault(ctx, a, "incident_persist_failed", err, inc.ID)
				}
			}
			e.scheduleStateTimer(a, timerKindTrigger, dur)
			e.persist(ctx, a)
			if err := e.outputs.FireCycle(ctx, a.id, *inc, FireOptions{Cycle: inc.RetriggerCycles, Policy: mcfg.Outputs}); err != nil {
				e.journalFault(ctx, a, "output_fire_failed", err, inc.ID)
			}
			e.journalEntry(ctx, a, JournalEntry{
				Class: hmenum.AlarmJournalClassTrigger, Event: "retrigger_cycle",
				IncidentID: inc.ID, Details: map[string]any{"cycle": inc.RetriggerCycles},
			})
			return
		}
	}
	e.finishTriggered(ctx, a, "trigger_time_elapsed")
}

// finishTriggered leaves the triggered state via the post-trigger
// policy. Outputs are stopped (the alarm-light lifecycle ends with
// the episode) and the incident closes. The caller holds the lock.
func (e *Engine) finishTriggered(ctx context.Context, a *area, journalEvent string) {
	from := a.state
	incID := int64(0)
	if a.incident != nil {
		incID = a.incident.ID
	}
	if err := e.outputs.StopAll(ctx, a.id, incID); err != nil {
		e.journalFault(ctx, a, "output_stop_failed", err, incID)
	}
	e.closeIncident(ctx, a, closeReasonPostTrigger)
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTrigger, Event: journalEvent, IncidentID: incID,
	})
	if a.cfg.PostTrigger == hmenum.AlarmPostTriggerDisarm {
		rearmMode := a.mode
		a.cancelTimers()
		a.state = hmenum.AlarmAreaStateDisarmed
		a.mode = hmenum.AlarmModeDisarmed
		a.bypassed = map[string]bool{}
		a.openAtArm = map[string]bool{}
		// Auto-rearm (docs/alarm-concept.md §15 row 22): schedule a
		// return to the pre-incident mode after a quiet period. Set
		// before persist so the timer lands in timers_json.
		e.scheduleAutoRearmIfConfigured(ctx, a, rearmMode, "engine")
		e.persist(ctx, a)
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassDisarm, Event: "disarmed_post_trigger", Actor: "engine",
		})
		e.publishState(a, from, "engine", "engine")
		return
	}
	// Return to armed: re-capture the open baseline so a door still
	// standing open does not instantly re-trigger; a fresh transition
	// is required for a new incident.
	e.completeArm(ctx, a, from, "engine", "engine")
}

// closeIncident closes the open incident (idempotent) and detaches it
// from the area, including the redundant silence marker. The caller
// holds the lock.
func (e *Engine) closeIncident(ctx context.Context, a *area, reason string) {
	inc := a.incident
	if inc == nil {
		return
	}
	if inc.ID != 0 {
		if err := e.incidents.Close(ctx, inc.ID, unixMS(e.clk.Now()), reason); err != nil {
			e.journalFault(ctx, a, "incident_persist_failed", err, inc.ID)
		}
	}
	a.incident = nil
	a.silencedIncidentID = 0
}

// scheduleStateTimer replaces the area's state timer. The caller
// holds the lock.
func (e *Engine) scheduleStateTimer(a *area, kind string, d time.Duration) {
	if a.timerCancel != nil {
		a.timerCancel()
	}
	a.timerSeq++
	seq := a.timerSeq
	a.timerKind = kind
	a.timerDeadline = e.clk.Now().Add(d)
	a.timerRemaining = d
	areaID := a.id
	a.timerCancel = e.sched.Schedule(d, func() {
		e.onStateTimerFired(areaID, seq)
	})
}

// onStateTimerFired dispatches a state-timer expiry, discarding stale
// fires from cancelled or replaced timers. It runs on the engine
// lifetime context — a countdown must not die with the request that
// scheduled it.
//
//nolint:contextcheck // timer fires deliberately detach from the scheduling caller's ctx (see lifeCtx)
func (e *Engine) onStateTimerFired(areaID string, seq uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.areas[areaID]
	if !ok || a.timerSeq != seq {
		return
	}
	kind := a.timerKind
	a.timerCancel = nil
	a.timerKind = ""
	ctx := e.lifeCtx
	switch kind {
	case timerKindExit:
		if a.state == hmenum.AlarmAreaStateArming {
			e.completeArm(ctx, a, a.state, "engine", "engine")
		}
	case timerKindEntry:
		if a.state == hmenum.AlarmAreaStatePending {
			cause := incidentCause{Kind: causeKindPendingElapsed, SensorID: a.pendingCause}
			if s, ok := a.sensors[a.pendingCause]; ok {
				cause.SensorName = s.row.Name
			}
			e.trigger(ctx, a, cause, FireOptions{})
		}
	case timerKindPreAlarm:
		if a.state == hmenum.AlarmAreaStateTriggered && a.preAlarm {
			e.onPreAlarmElapsed(ctx, a)
		}
	case timerKindTrigger:
		if a.state == hmenum.AlarmAreaStateTriggered {
			e.onTriggerElapsed(ctx, a)
		}
	}
}

// scheduleArmCloseDebounce arms the 5 s settle timer that completes
// the exit delay early after an arm_after_closing sensor closes. The
// caller holds the lock. The callback runs on the engine lifetime
// context like every timer fire.
//
//nolint:contextcheck // timer fires deliberately detach from the scheduling caller's ctx (see lifeCtx)
func (e *Engine) scheduleArmCloseDebounce(a *area, sensorID string) {
	a.cancelDebounce()
	a.debounceSeq++
	seq := a.debounceSeq
	areaID := a.id
	a.debounceCancel = e.sched.Schedule(armAfterClosingDebounce, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		aa, ok := e.areas[areaID]
		if !ok || aa.debounceSeq != seq || aa.state != hmenum.AlarmAreaStateArming {
			return
		}
		s, ok := aa.sensors[sensorID]
		if !ok || (s.activeKnown && s.active) {
			return
		}
		aa.debounceCancel = nil
		e.journalEntry(e.lifeCtx, aa, JournalEntry{
			Class: hmenum.AlarmJournalClassArm, Event: "armed_after_closing",
			Details: map[string]any{"sensor_id": sensorID},
		})
		e.completeArm(e.lifeCtx, aa, aa.state, "engine", "engine")
	})
}

// chirp forwards a chirp request to the driver layer; errors are
// logged only — feedback tones are best-effort by design (S5).
func (e *Engine) chirp(ctx context.Context, a *area, req ChirpRequest) {
	if err := e.outputs.Chirp(ctx, a.id, req); err != nil {
		e.log.Debug("alarm chirp failed", "area", a.id, "kind", string(req.Kind), "error", err)
	}
}

// startTicks starts the 1 Hz countdown tick chain for the running
// exit or entry delay: every tick publishes an AlarmCountdownEvent
// for live UI countdowns, and — when the mode's policy enables
// countdown ticks — forwards a chirp request. The chain follows the
// state timer and dies with it. The caller holds the lock.
//
//nolint:contextcheck // tick fires deliberately detach from the scheduling caller's ctx (see lifeCtx)
func (e *Engine) startTicks(a *area, timerKind string) {
	a.cancelTicks()
	total := a.timerRemaining
	if total <= 0 {
		return
	}
	a.tickSeq++
	seq := a.tickSeq
	areaID := a.id
	var schedule func()
	schedule = func() {
		a.tickCancel = e.sched.Schedule(time.Second, func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			aa, ok := e.areas[areaID]
			if !ok || aa.tickSeq != seq || aa.timerKind != timerKind || aa.timerCancel == nil {
				return
			}
			remaining := aa.timerDeadline.Sub(e.clk.Now())
			if remaining <= 0 {
				return
			}
			e.sink.Publish(hmevent.AlarmCountdownEvent{
				Base:   hmevent.NewBaseAt(e.clk.Now()),
				AreaID: areaID, Kind: timerKind,
				RemainingMS: remaining.Milliseconds(), TotalMS: total.Milliseconds(),
			})
			if aa.cfg.Modes[aa.mode].Outputs.CountdownTicks {
				kind := ChirpCountdownTick
				if timerKind == timerKindEntry {
					kind = ChirpEntryWarning
				}
				e.chirp(e.lifeCtx, aa, ChirpRequest{Kind: kind, Remaining: remaining, Total: total})
			}
			schedule()
		})
	}
	schedule()
}

// AdoptSounding turns an already-sounding siren discovered by
// reconciliation into a triggered incident (S4 adopt-before-stop): it
// is evidence of a trigger during the blind window, not an error. The
// area enters triggered without an engine-side output cycle — the
// hardware is already sounding; the driver layer arms the bounded
// stop watchdog instead.
func (e *Engine) AdoptSounding(ctx context.Context, areaID string, outputIDs []string) (adopted bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.areas[areaID]
	if !ok {
		return false, ErrUnknownArea
	}
	if a.state == hmenum.AlarmAreaStateTriggered && a.incident != nil {
		// Already alarming: the sounding outputs belong to the open
		// incident; nothing to adopt (and nothing to re-account).
		return false, nil
	}
	from := a.state
	mcfg := a.cfg.Modes[a.mode]
	now := e.clk.Now()
	dur := mcfg.triggerDuration()
	if a.incident == nil {
		cause := incidentCause{Kind: causeKindAdopted}
		causeJSON, err := json.Marshal(cause)
		if err != nil {
			causeJSON = []byte("{}")
		}
		inc := sqlitestore.AlarmIncident{
			AreaID:            a.id,
			Mode:              a.mode,
			CauseJSON:         string(causeJSON),
			StartedAtMS:       unixMS(now),
			TriggerDeadlineMS: unixMS(now.Add(dur)),
		}
		id, err := e.incidents.Create(ctx, inc)
		if err != nil {
			e.journalFault(ctx, a, "incident_persist_failed", err, 0)
		} else {
			inc.ID = id
		}
		a.incident = &inc
	}
	a.state = hmenum.AlarmAreaStateTriggered
	e.scheduleStateTimer(a, timerKindTrigger, dur)
	e.persist(ctx, a)
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTrigger, Event: "sounding_siren_adopted",
		IncidentID: a.incident.ID,
		Details:    map[string]any{"outputs": outputIDs},
	})
	e.sink.Publish(hmevent.AlarmTriggeredEvent{
		Base:   hmevent.NewBaseAt(now),
		AreaID: a.id, AreaName: a.name,
		IncidentID: a.incident.ID,
		Cause:      causeKindAdopted, Mode: a.mode,
	})
	e.publishState(a, from, "engine:reconcile", "engine")
	return true, nil
}

// ReevaluateSensors refreshes every armed area's sensor values from
// the SensorReader and routes activations that happened during a
// blind window (docs/alarm-concept.md §10.1, CCU-reconnect row) —
// the same comparison against the open-at-arm baseline a restore
// runs. Safe to call repeatedly; disarmed areas are untouched.
func (e *Engine) ReevaluateSensors(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return
	}
	for _, id := range e.sortedAreaIDs() {
		a := e.areas[id]
		if a.state != hmenum.AlarmAreaStateArmed {
			continue
		}
		e.reEvaluateAfterRestore(ctx, a)
		e.refreshReadiness(a)
	}
}

// PanicTrigger fires a panic incident on areaID independent of its arm
// state (docs/alarm-concept.md §7). silent suppresses acoustic outputs
// (silent panic / duress panic). by/source attribute the trigger. It is
// the verb behind keypad/remote panic keys and the MQTT TRIGGER payload.
func (e *Engine) PanicTrigger(ctx context.Context, areaID string, silent bool, by, source string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return ErrInvalidState
	}
	a, ok := e.areas[areaID]
	if !ok {
		return ErrUnknownArea
	}
	policy := a.cfg.PanicOutputs
	if silent {
		policy.Silent = true
	}
	e.alwaysOnFire(ctx, a, causeKindPanic, policy, incidentCause{Kind: causeKindPanic, SensorName: by}, by, source)
	e.refreshReadiness(a)
	return nil
}

// alwaysOnFromSensor routes an always-on hazard/panic sensor activation
// to the class output policy. The caller holds the lock.
func (e *Engine) alwaysOnFromSensor(ctx context.Context, a *area, s *sensorState) {
	causeKind := causeKindHazard
	policy := a.cfg.HazardOutputs
	if s.row.SensorType == hmenum.AlarmSensorTypePanic {
		causeKind = causeKindPanic
		policy = a.cfg.PanicOutputs
		if s.cfg.PanicSilent {
			policy.Silent = true
		}
	}
	cause := incidentCause{Kind: causeKind, SensorID: s.row.ID, SensorName: s.row.Name}
	e.alwaysOnFire(ctx, a, causeKind, policy, cause, s.row.Name, "engine")
}

// alwaysOnFire drives an always-on (hazard/panic) incident. It bypasses
// the arm-state machine but still drives the panel to triggered so the
// alarm is visible everywhere; the state it interrupts is recorded in
// preTriggerState and restored on post-trigger. When the area is already
// triggered it only layers the class output cycle onto the running
// incident, leaving that incident's state/timer/return untouched. The
// caller holds the lock.
func (e *Engine) alwaysOnFire(ctx context.Context, a *area, causeKind string, policy OutputPolicy, cause incidentCause, by, source string) {
	now := e.clk.Now()
	if a.state == hmenum.AlarmAreaStateTriggered {
		// An incident is already running: add the class outputs (unless
		// silenced) and journal, but do not disturb the running incident.
		if a.incident != nil && !a.incident.Silenced {
			if err := e.outputs.FireCycle(ctx, a.id, *a.incident, FireOptions{Policy: policy}); err != nil {
				e.journalFault(ctx, a, "output_fire_failed", err, a.incident.ID)
			}
		}
		incID := int64(0)
		if a.incident != nil {
			incID = a.incident.ID
		}
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassTrigger, Event: "always_on_activation",
			Actor: by, Source: source, IncidentID: incID,
			Details: map[string]any{"cause": causeKind, "sensor_id": cause.SensorID},
		})
		return
	}

	from := a.state
	a.preTriggerState = a.state
	a.preTriggerMode = a.mode
	dur := a.cfg.Modes[a.mode].triggerDuration()

	if a.incident == nil {
		causeJSON, err := json.Marshal(cause)
		if err != nil {
			causeJSON = []byte("{}")
		}
		inc := sqlitestore.AlarmIncident{
			AreaID:            a.id,
			Mode:              a.mode,
			CauseJSON:         string(causeJSON),
			StartedAtMS:       unixMS(now),
			TriggerDeadlineMS: unixMS(now.Add(dur)),
		}
		id, err := e.incidents.Create(ctx, inc)
		if err != nil {
			e.journalFault(ctx, a, "incident_persist_failed", err, 0)
		} else {
			inc.ID = id
		}
		a.incident = &inc
	}

	a.state = hmenum.AlarmAreaStateTriggered
	e.scheduleStateTimer(a, timerKindTrigger, dur)
	e.persist(ctx, a)
	if err := e.outputs.FireCycle(ctx, a.id, *a.incident, FireOptions{Policy: policy}); err != nil {
		e.journalFault(ctx, a, "output_fire_failed", err, a.incident.ID)
	}
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTrigger, Event: "triggered",
		Actor: by, Source: source, IncidentID: a.incident.ID,
		Details: map[string]any{"cause": causeKind, "sensor_id": cause.SensorID, "mode": string(a.mode)},
	})
	e.sink.Publish(hmevent.AlarmTriggeredEvent{
		Base:   hmevent.NewBaseAt(now),
		AreaID: a.id, AreaName: a.name,
		IncidentID: a.incident.ID,
		SensorID:   cause.SensorID, SensorName: cause.SensorName,
		Cause: causeKind, Mode: a.mode,
	})
	e.publishState(a, from, by, source)
}

// finishAlwaysOn ends an always-on incident and returns the area to the
// state it interrupted (armed if it was armed-side, disarmed otherwise —
// so a hazard during a disarmed period drops back to disarmed, and one
// while armed resumes protection). The caller holds the lock.
func (e *Engine) finishAlwaysOn(ctx context.Context, a *area, journalEvent, actor string) {
	from := a.state
	target := a.preTriggerMode
	incID := int64(0)
	if a.incident != nil {
		incID = a.incident.ID
	}
	if err := e.outputs.StopAll(ctx, a.id, incID); err != nil {
		e.journalFault(ctx, a, "output_stop_failed", err, incID)
	}
	e.closeIncident(ctx, a, closeReasonPostTrigger)
	a.cancelTimers()
	a.preTriggerState = ""
	a.preTriggerMode = ""
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTrigger, Event: journalEvent, IncidentID: incID,
	})
	if target.Armed() {
		a.state = hmenum.AlarmAreaStateArmed
		a.mode = target
		e.recaptureOpenBaseline(a)
	} else {
		a.state = hmenum.AlarmAreaStateDisarmed
		a.mode = hmenum.AlarmModeDisarmed
		a.bypassed = map[string]bool{}
		a.openAtArm = map[string]bool{}
	}
	e.persist(ctx, a)
	e.publishState(a, from, actor, "engine")
}

// alwaysOnPolicyForIncident recovers the class output policy of a
// persisted always-on incident from its cause kind (its per-activation
// silent override is not persisted, so a restored panic uses the
// configured PanicOutputs policy).
func alwaysOnPolicyForIncident(a *area, inc *sqlitestore.AlarmIncident) OutputPolicy {
	var c incidentCause
	if inc != nil {
		_ = json.Unmarshal([]byte(inc.CauseJSON), &c)
	}
	if c.Kind == causeKindPanic {
		return a.cfg.PanicOutputs
	}
	return a.cfg.HazardOutputs
}

// scheduleAutoRearmIfConfigured arms the auto-rearm timer when the area
// configures it and the pre-incident mode is armable. It sets the timer
// fields (so a following persist captures the tuple) and journals. The
// caller holds the lock.
func (e *Engine) scheduleAutoRearmIfConfigured(ctx context.Context, a *area, rearmMode hmenum.AlarmMode, actor string) {
	if a.cfg.AutoRearmSeconds <= 0 || !rearmMode.Armed() {
		return
	}
	if _, ok := a.cfg.Modes[rearmMode]; !ok {
		return
	}
	d := time.Duration(a.cfg.AutoRearmSeconds) * time.Second
	e.scheduleAutoRearm(a, rearmMode, d)
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassArm, Event: "auto_rearm_scheduled", Actor: actor,
		Details: map[string]any{"mode": string(rearmMode), "delay_s": a.cfg.AutoRearmSeconds},
	})
}

// scheduleAutoRearm replaces the area's auto-rearm timer. The caller
// holds the lock. The callback runs on the engine lifetime context.
//
//nolint:contextcheck // timer fires deliberately detach from the scheduling caller's ctx (see lifeCtx)
func (e *Engine) scheduleAutoRearm(a *area, mode hmenum.AlarmMode, d time.Duration) {
	a.cancelAutoRearm()
	a.autoRearmMode = mode
	a.autoRearmSeq++
	seq := a.autoRearmSeq
	a.autoRearmDeadline = e.clk.Now().Add(d)
	areaID := a.id
	a.autoRearmCancel = e.sched.Schedule(d, func() {
		e.onAutoRearmFired(areaID, seq)
	})
}

// deferAutoRearm restarts the auto-rearm quiet period after member
// activity. The caller holds the lock.
func (e *Engine) deferAutoRearm(ctx context.Context, a *area, sensorID string) {
	mode := a.autoRearmMode
	e.scheduleAutoRearm(a, mode, time.Duration(a.cfg.AutoRearmSeconds)*time.Second)
	e.persist(ctx, a)
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassArm, Event: "auto_rearm_deferred",
		Details: map[string]any{"sensor_id": sensorID},
	})
}

// onAutoRearmFired dispatches an auto-rearm timer expiry, discarding
// stale fires. It runs on the engine lifetime context.
//
//nolint:contextcheck // timer fires deliberately detach from the scheduling caller's ctx (see lifeCtx)
func (e *Engine) onAutoRearmFired(areaID string, seq uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.areas[areaID]
	if !ok || a.autoRearmSeq != seq || a.autoRearmCancel == nil {
		return
	}
	a.autoRearmCancel = nil
	e.onAutoRearmElapsed(e.lifeCtx, a)
}

// onAutoRearmElapsed attempts the quiet-period rearm. It arms with
// force=false: any remaining blocker journals a fail-visible
// failed-to-arm fault and leaves the area disarmed (docs/alarm-concept.md
// §15 row 22). The caller holds the lock.
func (e *Engine) onAutoRearmElapsed(ctx context.Context, a *area) {
	mode := a.autoRearmMode
	a.autoRearmMode = ""
	if a.state != hmenum.AlarmAreaStateDisarmed || !mode.Armed() {
		e.persist(ctx, a)
		return
	}
	mcfg, ok := a.cfg.Modes[mode]
	if !ok {
		e.journalFault(ctx, a, "auto_rearm_mode_unavailable", nil, 0)
		e.persist(ctx, a)
		return
	}
	_, err := e.beginArm(ctx, a, ArmRequest{Mode: mode, By: "engine", Source: "engine:auto_rearm"}, mcfg)
	if err != nil {
		var nr *NotReadyError
		if errors.As(err, &nr) {
			e.journalEntry(ctx, a, JournalEntry{
				Class: hmenum.AlarmJournalClassFault, Event: "failed_to_arm",
				Actor: "engine", Source: "engine:auto_rearm",
				Details: map[string]any{"mode": string(mode), "blockers": nr.Blockers},
			})
		} else {
			e.journalFault(ctx, a, "auto_rearm_failed", err, 0)
		}
		e.persist(ctx, a)
		return
	}
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassArm, Event: "auto_rearmed",
		Actor: "engine", Source: "engine:auto_rearm", Details: map[string]any{"mode": string(mode)},
	})
}

// persist writes the area's full state row. A failure is journaled
// and logged, never silently swallowed, and does not abort the
// transition — the machine keeps operating from memory (S7).
// The caller holds the lock.
func (e *Engine) persist(ctx context.Context, a *area) {
	now := e.clk.Now()
	var timers []persistedTimer
	if a.timerCancel != nil {
		remaining := a.timerDeadline.Sub(now)
		if remaining < 0 {
			remaining = 0
		}
		timers = append(timers, persistedTimer{
			Kind:          a.timerKind,
			DeadlineMS:    unixMS(a.timerDeadline),
			RemainingMS:   remaining.Milliseconds(),
			PersistedAtMS: unixMS(now),
			BootCount:     e.bootCount,
		})
	}
	if a.autoRearmCancel != nil {
		remaining := a.autoRearmDeadline.Sub(now)
		if remaining < 0 {
			remaining = 0
		}
		timers = append(timers, persistedTimer{
			Kind:          timerKindAutoRearm,
			DeadlineMS:    unixMS(a.autoRearmDeadline),
			RemainingMS:   remaining.Milliseconds(),
			PersistedAtMS: unixMS(now),
			BootCount:     e.bootCount,
		})
	}
	incID := int64(0)
	if a.incident != nil {
		incID = a.incident.ID
	}
	row := sqlitestore.AlarmStateRow{
		AreaID:      a.id,
		State:       a.state,
		Mode:        a.mode,
		BypassJSON:  a.encodeBypass(),
		IncidentID:  incID,
		TimersJSON:  encodeTimers(timers),
		ContextJSON: a.encodeContext(),
		UpdatedAtMS: unixMS(now),
	}
	if err := e.stateStore.Upsert(ctx, row); err != nil {
		e.log.Error("alarm state persist failed", "area", a.id, "error", err)
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassFault, Event: "state_persist_failed",
			Details: map[string]any{"error": err.Error()},
		})
	}
}

// publishState emits the state-changed event. The caller holds the
// lock and has already updated a.state.
func (e *Engine) publishState(a *area, from hmenum.AlarmAreaState, by, source string) {
	incID := int64(0)
	if a.incident != nil {
		incID = a.incident.ID
	}
	e.sink.Publish(hmevent.AlarmStateChangedEvent{
		Base:   hmevent.NewBaseAt(e.clk.Now()),
		AreaID: a.id, AreaName: a.name,
		From: from, To: a.state, Mode: a.mode,
		ChangedBy: by, Source: source, IncidentID: incID,
	})
}

// journalEntry appends to the journal; failures are logged only — a
// journal outage must never block an alarm action.
func (e *Engine) journalEntry(ctx context.Context, a *area, entry JournalEntry) {
	entry.AreaID = a.id
	if _, err := e.journal.Append(ctx, entry); err != nil {
		e.log.Error("alarm journal append failed", "area", a.id, "event", entry.Event, "error", err)
	}
}

// journalFault appends a fault-class entry (fail-visible, S7).
func (e *Engine) journalFault(ctx context.Context, a *area, event string, cause error, incidentID int64) {
	details := map[string]any{}
	if cause != nil {
		details["error"] = cause.Error()
	}
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassFault, Event: event,
		IncidentID: incidentID, Details: details,
	})
}

// lookupSensor resolves a sensor ID to its area and state. The caller
// holds the lock.
func (e *Engine) lookupSensor(sensorID string) (*area, *sensorState) {
	areaID, ok := e.sensorIndex[sensorID]
	if !ok {
		return nil, nil
	}
	a, ok := e.areas[areaID]
	if !ok {
		return nil, nil
	}
	s, ok := a.sensors[sensorID]
	if !ok {
		return nil, nil
	}
	return a, s
}

// isArmedState reports whether st is an armed-side state (armed,
// arming, or pending).
func (e *Engine) isArmedState(st hmenum.AlarmAreaState) bool {
	switch st {
	case hmenum.AlarmAreaStateArmed, hmenum.AlarmAreaStateArming, hmenum.AlarmAreaStatePending:
		return true
	default:
		return false
	}
}

// sortedAreaIDs returns the managed area IDs in stable order. The
// caller holds the lock.
func (e *Engine) sortedAreaIDs() []string {
	ids := make([]string, 0, len(e.areas))
	for id := range e.areas {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// noopOutputs is the default OutputPort until the driver layer is
// wired: it does nothing and reports success.
type noopOutputs struct{}

func (noopOutputs) FireCycle(context.Context, string, sqlitestore.AlarmIncident, FireOptions) error {
	return nil
}
func (noopOutputs) StopAll(context.Context, string, int64) error { return nil }

func (noopOutputs) Chirp(context.Context, string, ChirpRequest) error { return nil }

// noopSink drops events (unwired engine).
type noopSink struct{}

func (noopSink) Publish(hmevent.Event) {}

// noopJournal drops journal entries (unwired engine).
type noopJournal struct{}

func (noopJournal) Append(context.Context, JournalEntry) (int64, error) { return 0, nil }
