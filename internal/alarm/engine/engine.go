// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	// ErrUnknownZone reports an zone ID the engine does not manage.
	ErrUnknownZone = errors.New("engine: unknown zone")
	// ErrUnknownMode reports a mode the zone does not configure.
	ErrUnknownMode = errors.New("engine: mode not configured for zone")
	// ErrInvalidState reports a verb not allowed in the current
	// state (e.g. arming a triggered zone). Disarm and silence never
	// return it — they are never state-gated.
	ErrInvalidState = errors.New("engine: action not allowed in current state")
	// ErrNoIncident reports an acknowledge without an open incident.
	ErrNoIncident = errors.New("engine: no open incident")
	// ErrInvalidCode reports a verb refused because a required alarm
	// code was missing or did not authenticate (notes/concepts/alarm-concept.md
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
// (notes/concepts/alarm-concept.md §11 degradation). They still surface duress
// when a code is supplied. These tokens are the operator-session /
// break-glass surfaces; anonymous MQTT / sysvar paths are not listed
// and stay code-gated.
const (
	codeSourceRESTOperator = "rest-operator"
	codeSourceWSOperator   = "ws-operator"
	codeSourceHmcli        = "hmcli"
)

// Hardware-intent sources. A keypad or remote press is already
// authenticated by its slot / binding match in the intent router
// (which also enforces the code row's verb permissions), and the
// hardware carries no PIN that could be re-typed — so these sources
// bypass the engine-side code requirement like operator sources do.
// They never carry a code, so no duress detection applies here (WKP
// on-device slots are independent of engine codes by design).
const (
	codeSourceKeypad = "keypad"
	codeSourceRemote = "remote"
)

// IsOperatorSource reports whether source is an operator-session /
// break-glass surface that bypasses code requirements. Exported so the
// codes facade can exempt the same sources from rate limiting — a
// lockout on an already-authenticated surface protects nothing and
// would suppress duress detection (notes/concepts/alarm-concept.md §11).
func IsOperatorSource(source string) bool {
	switch source {
	case codeSourceRESTOperator, codeSourceWSOperator, codeSourceHmcli:
		return true
	default:
		return false
	}
}

// isOperatorSource keeps the package-internal call sites terse.
func isOperatorSource(source string) bool { return IsOperatorSource(source) }

// isPreAuthenticatedSource reports whether source arrives already
// authenticated (operator session or hardware binding) and therefore
// bypasses the code requirement in authorize.
func isPreAuthenticatedSource(source string) bool {
	return isOperatorSource(source) || source == codeSourceKeypad || source == codeSourceRemote
}

// NotReadyError reports a refused arm together with the blocking
// sensors, so surfaces can render the bypass sheet.
type NotReadyError struct {
	Blockers []string
	// Details carries the reason and the full source identity per
	// blocking sensor. Blockers holds bare row IDs, which are opaque to
	// an operator and cannot be resolved by a north-bound consumer.
	Details []hmevent.AlarmBlockerDetail
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
	// The identity components below are additive: rows written before
	// they existed decode with empty values and need no migration.
	SensorType     string `json:"sensor_type,omitempty"`
	InterfaceID    string `json:"interface_id,omitempty"`
	ChannelAddress string `json:"channel_address,omitempty"`
	Parameter      string `json:"parameter,omitempty"`
}

// pendingElapsedCause builds the cause of an expired entry delay from
// the sensor that opened it, so the incident names the detector that
// actually let the intruder in.
//
// The bare fallback is only for a pendingCause the zone no longer
// knows (a sensor un-enrolled mid-countdown): it costs the source
// ledger a row, which is better than attributing the incident to the
// wrong data point. The caller holds the lock.
func pendingElapsedCause(a *zone) incidentCause {
	if s, ok := a.sensors[a.pendingCause]; ok {
		return causeFromSensor(causeKindPendingElapsed, s.row)
	}
	return incidentCause{Kind: causeKindPendingElapsed, SensorID: a.pendingCause}
}

// causeFromSensor builds a cause document carrying the sensor's full
// data-point identity. Every cause that has a sensor behind it goes
// through here: a cause assembled by hand from the id and the name
// alone projects onto an empty source reference, which recordSource
// drops — so the incident's source list and the ledger stay empty and
// the report cannot say which detector fired.
func causeFromSensor(kind string, row sqlitestore.AlarmSensorRow) incidentCause {
	return incidentCause{
		Kind:           kind,
		SensorID:       row.ID,
		SensorName:     row.Name,
		Central:        row.CentralName,
		SensorType:     string(row.SensorType),
		InterfaceID:    row.InterfaceID,
		ChannelAddress: row.ChannelAddress,
		Parameter:      row.Parameter,
	}
}

// sourceRef projects a cause onto the domain-wide source reference.
// A cause without a channel address (central loss, an adopted siren)
// yields an empty reference, which callers drop.
func (c incidentCause) sourceRef(atMS int64) hmevent.SecuritySourceRef {
	if c.ChannelAddress == "" {
		return hmevent.SecuritySourceRef{}
	}
	ref := hmevent.NewSecuritySourceRef(c.Central, c.InterfaceID, c.ChannelAddress, c.Parameter)
	ref.SensorID = c.SensorID
	ref.Name = c.SensorName
	ref.SensorType = hmenum.AlarmSensorType(c.SensorType)
	// The sensor role decides the class for intrusion, panic and
	// tamper. A hazard sensor stays unclassified here: the role covers
	// smoke, water and gas alike, and separating them needs the device
	// model and channel type, which an enrollment does not carry.
	if class, ok := hmenum.SecurityClassForSensorType(ref.SensorType); ok {
		ref.Class = class
	}
	ref.AtMS = atMS
	return ref
}

// Deps wires the engine's ports. Stores are required; every other
// dependency has a safe default.
type Deps struct {
	Clock     clock.Clock
	Scheduler TimerScheduler
	Zones     ZoneStore
	Sensors   SensorStore
	State     StateStore
	Incidents IncidentStore
	Runtime   RuntimeStore
	Outputs   OutputPort
	// MotionReset clears latched motion detectors; nil disables both
	// the manual verb and the pre-arm pass.
	MotionReset MotionResetPort
	Sink        EventSink
	Journal     Journal
	// SourceLedger records the per-incident source ledger; nil disables it.
	SourceLedger IncidentSourceLedger
	SensorReader SensorReader
	// Validator resolves alarm codes for the code-policy checks. A nil
	// validator disables codes entirely: every CodePolicy is inert.
	Validator CodeValidator
	Logger    *slog.Logger
	// RestartLoopBreakerK caps restore-driven re-fires per incident
	// before output degradation; 0 selects the default.
	RestartLoopBreakerK int
}

// Engine hosts one arm-state machine per alarm zone. All mutating
// entry points (verbs, sensor events, timer fires) serialize on one
// mutex; persistence is write-through under that lock so the stored
// state never runs ahead of or behind the in-memory state.
type Engine struct {
	clk          clock.Clock
	sched        TimerScheduler
	zonesStore   ZoneStore
	sensorsStore SensorStore
	stateStore   StateStore
	incidents    IncidentStore
	runtime      RuntimeStore
	outputs      OutputPort
	motionReset  MotionResetPort
	sink         EventSink
	journal      Journal
	ledger       IncidentSourceLedger
	reader       SensorReader
	validator    CodeValidator
	// duress resolves a code without the validator's side effects, for
	// the no-op paths that must still open the covert channel. It is
	// the validator itself when that implements DuressMatcher.
	duress       DuressMatcher
	log          *slog.Logger
	loopBreakerK int

	mu          sync.Mutex
	started     bool
	bootCount   int64
	zones       map[string]*zone
	sensorIndex map[string]string // sensor ID → zone ID

	// lifeCtx bounds timer-driven work. Timer fires outlive the
	// request that scheduled them, so they deliberately detach from
	// the caller's context and run on the engine lifetime instead
	// (set by Start; one of the sanctioned ctx-in-struct seams).
	lifeCtx context.Context
}

// New constructs an engine. Call Start to load configuration and
// restore persisted state.
func New(deps Deps) (*Engine, error) {
	if deps.Zones == nil || deps.Sensors == nil || deps.State == nil || deps.Incidents == nil || deps.Runtime == nil {
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
	// A validator that can answer "is this the duress code?" without
	// side effects is resolved once here; see [DuressMatcher].
	matcher, _ := deps.Validator.(DuressMatcher)
	return &Engine{
		clk:          clk,
		sched:        sched,
		zonesStore:   deps.Zones,
		sensorsStore: deps.Sensors,
		stateStore:   deps.State,
		incidents:    deps.Incidents,
		runtime:      deps.Runtime,
		outputs:      outputs,
		motionReset:  deps.MotionReset,
		sink:         sink,
		journal:      journal,
		ledger:       deps.SourceLedger,
		reader:       deps.SensorReader,
		validator:    deps.Validator,
		duress:       matcher,
		log:          logger,
		loopBreakerK: k,
		zones:        map[string]*zone{},
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
	for _, a := range e.zones {
		e.persist(ctx, a)
		a.cancelTimers()
		// The auto-rearm timer is deliberately not part of cancelTimers,
		// so it has to be cancelled here or it fires on a stopped engine:
		// the rearm would chirp, drive outputs, and persist an armed row
		// over the snapshot above, leaving the next boot to restore a
		// zone nobody armed. The snapshot is already written, so the
		// pending rearm still survives into the next Start.
		a.cancelAutoRearm()
	}
}

// ZonesLoaded reports whether Start has finished loading the configured
// zone set, i.e. whether an empty [Engine.Zones] means "no zones are
// configured" rather than "the engine has not read its stores yet".
//
// Consumers that act on the ABSENCE of a zone need this: the MQTT
// publisher reconciles once eagerly at start-up, and treating that
// pre-load pass as authoritative would let the retained-config orphan
// sweep classify every live alarm panel as a leftover and wipe the
// plane.
func (e *Engine) ZonesLoaded() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.started
}

// Zones returns snapshots of every managed zone, ordered by ID.
func (e *Engine) Zones() []ZoneSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clk.Now()
	out := make([]ZoneSnapshot, 0, len(e.zones))
	for _, a := range e.zones {
		out = append(out, a.snapshot(now))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Zone returns the snapshot of one zone.
func (e *Engine) Zone(id string) (ZoneSnapshot, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.zones[id]
	if !ok {
		return ZoneSnapshot{}, false
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
	// Code is the alarm code supplied with the arm, when the zone's
	// CodePolicy requires one (or to surface a duress code). Empty when
	// none was supplied.
	Code   string
	By     string
	Source string
}

// ArmResult reports the outcome of an accepted arm.
type ArmResult struct {
	State     hmenum.AlarmZoneState
	Bypassed  []string
	ExitDelay time.Duration
}

// Arm starts arming zoneID into req.Mode. It fails with
// *NotReadyError when blockers remain and Force is not set; pending
// and triggered zones must be disarmed first.
func (e *Engine) Arm(ctx context.Context, zoneID string, req ArmRequest) (ArmResult, error) {
	// The preconditions are checked first so their errors keep precedence
	// over a code refusal, then the code is resolved without the lock,
	// then the preconditions are re-checked: the state can move while the
	// validator derives its hashes (see [Engine.resolveCode]).
	policy, err := e.armPreflight(zoneID, req.Mode)
	if err != nil {
		return ArmResult{}, err
	}
	identity, duress, cerr := e.resolveCode(ctx, zoneID, policy, codeVerbArm, req.Code, req.Source)
	if cerr != nil {
		return ArmResult{}, cerr
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	a, mcfg, err := e.armPreconditionsLocked(zoneID, req.Mode)
	if err != nil {
		return ArmResult{}, err
	}
	if identity != "" && req.By == "" {
		req.By = identity
	}
	if duress {
		e.fireDuress(ctx, a, codeVerbArm, req.By, req.Source)
	}
	return e.beginArm(ctx, a, req, mcfg)
}

// armPreflight runs the arm preconditions and returns the zone's code
// policy, so the code can be resolved outside the lock.
func (e *Engine) armPreflight(zoneID string, mode hmenum.AlarmMode) (CodePolicy, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, _, err := e.armPreconditionsLocked(zoneID, mode)
	if err != nil {
		return CodePolicy{}, err
	}
	return a.cfg.CodePolicy, nil
}

// armPreconditionsLocked resolves the zone and the mode configuration an
// arm needs, applying the state gates. Arm is refused on an engine that
// is not running; silence and disarm stay deliberately ungated (S3/S6).
// The caller holds the lock.
func (e *Engine) armPreconditionsLocked(zoneID string, mode hmenum.AlarmMode) (*zone, ModeConfig, error) {
	if !e.started {
		return nil, ModeConfig{}, ErrInvalidState
	}
	a, ok := e.zones[zoneID]
	if !ok {
		return nil, ModeConfig{}, ErrUnknownZone
	}
	if !mode.Armed() {
		return nil, ModeConfig{}, ErrUnknownMode
	}
	mcfg, ok := a.cfg.Modes[mode]
	if !ok {
		return nil, ModeConfig{}, ErrUnknownMode
	}
	if a.state == hmenum.AlarmZoneStatePending || a.state == hmenum.AlarmZoneStateTriggered {
		return nil, ModeConfig{}, ErrInvalidState
	}
	return a, mcfg, nil
}

// beginArm resolves blockers and drives the disarmed→arming/armed
// transition; the caller holds the lock and has validated the mode and
// any code. It is shared by the public Arm verb and the auto-rearm
// timer, and honors Force / Bypass exactly as Arm documents.
func (e *Engine) beginArm(ctx context.Context, a *zone, req ArmRequest, mcfg ModeConfig) (ArmResult, error) {
	// A fresh arm supersedes any pending auto-rearm.
	a.cancelAutoRearm()

	// A walk-test session is arm-less by definition (§12.4): an
	// operator forgetting to stop it before an arm — human or
	// AutoArm-scheduled — must never leave it consuming every future
	// disarmed-period sensor activation once the zone disarms again.
	e.abortWalkTestForArm(ctx, a, req.By, req.Source)

	// Clear latched motion detectors as early as possible, so the write
	// is on the radio before the exit delay starts ticking.
	//
	// This deliberately does NOT feed into the arm decision below. The
	// reset is asynchronous — its effect arrives later as MOTION=false
	// events — and letting it pre-empt the blocker check would mean
	// treating a detector that is latched *because someone is moving in
	// the room* as clear. The existing blocker and auto-bypass rules
	// stay in charge; the reset only shortens how long a stale latch
	// keeps a zone un-armable.
	e.resetTriggeredMotionForArm(ctx, a, req.By, req.Source)

	// Resolve blockers against the requested + automatic bypasses.
	bypass := map[string]bool{}
	for _, id := range req.Bypass {
		if _, exists := a.sensors[id]; exists {
			bypass[id] = true
		}
	}
	// Decide against current values, not against whatever happened to
	// arrive on the bus: a sensor the engine has never seen an event
	// for reads as "unknown", which the blocker policy cannot classify,
	// so the check would pass vacuously and the open-at-arm baseline
	// below would miss the sensor too.
	e.refreshSensorValues(ctx, a)
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
		blocking := map[string]bool{}
		for _, id := range remaining {
			blocking[id] = true
		}
		var details []hmevent.AlarmBlockerDetail
		for i := range rd.Details {
			if rd.Details[i].Blocking && blocking[rd.Details[i].SensorID] {
				details = append(details, rd.Details[i])
			}
		}
		return ArmResult{}, &NotReadyError{Blockers: remaining, Details: details}
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
		a.state = hmenum.AlarmZoneStateArming
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
func (e *Engine) completeArm(ctx context.Context, a *zone, from hmenum.AlarmZoneState, by, source string) {
	a.cancelTimers()
	a.state = hmenum.AlarmZoneStateArmed
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
func (e *Engine) recaptureOpenBaseline(a *zone) {
	a.openAtArm = map[string]bool{}
	for id, s := range a.sensors {
		if s.cfg.InMode(a.mode) && !a.bypassed[id] && s.activeKnown && s.active {
			a.openAtArm[id] = true
		}
	}
}

// resolveCode applies the zone's CodePolicy to one verb. It returns the
// resolved code identity (for changed-by attribution), whether the code
// is a duress code, and a refusal error (ErrInvalidCode) when a required
// code is missing or wrong. A nil CodeValidator disables codes entirely.
// Pre-authenticated sources (operator sessions, hardware keypad/remote
// bindings) bypass the requirement; operator sources still get duress
// detection on a supplied code.
//
// The caller must NOT hold the engine lock. A validator that stores
// argon2id hashes derives one key per enabled code, hundreds of
// milliseconds each, and this mutex serialises every mutating entry
// point — sensor activations, the entry/exit countdowns, every other
// zone's verbs. Verifying under it froze the whole alarm system for
// seconds on a single mistyped PIN, at exactly the moment somebody was
// standing in the doorway with the entry delay running.
//
// Resolving before the lock lets the zone move underneath the answer, so
// every caller re-checks its preconditions after taking the lock instead
// of trusting the snapshot the policy came from.
func (e *Engine) resolveCode(
	ctx context.Context, zoneID string, policy CodePolicy, verb, code, source string,
) (identity string, duress bool, refuse error) {
	if e.validator == nil {
		return "", false, nil
	}
	required := policy.requires(verb, source)
	if isPreAuthenticatedSource(source) {
		required = false
	}
	if !required && code == "" {
		return "", false, nil
	}
	identity, duress, err := e.validator.Validate(ctx, zoneID, verb, code, source)
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

// probeDuress resolves a supplied code against the zone's duress codes
// only, without any of the validator's side effects (see
// [DuressMatcher]). It is what the verbs use where the outcome is a
// no-op regardless of the code, so a wrong code neither refuses, nor
// counts toward a lockout, nor journals a fault. An absent or
// non-matching matcher simply reports "not duress".
//
// Like [Engine.resolveCode] it runs without the engine lock, and for the
// same reason: the match derives the same expensive hashes.
func (e *Engine) probeDuress(ctx context.Context, zoneID, verb, code, source string) (identity string, duress bool) {
	if code == "" || e.duress == nil {
		return "", false
	}
	return e.duress.MatchDuress(ctx, zoneID, verb, code, source)
}

// zoneCodeContext snapshots what resolving a code needs: the zone's code
// policy and the state that decides which resolution applies.
func (e *Engine) zoneCodeContext(zoneID string) (policy CodePolicy, state hmenum.AlarmZoneState, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, exists := e.zones[zoneID]
	if !exists {
		return CodePolicy{}, "", false
	}
	return a.cfg.CodePolicy, a.state, true
}

// fireDuress emits the silent duress fan-out: a Hidden journal entry
// (the journal facade suppresses the append event for hidden rows) plus
// a dedicated AlarmDuressEvent on the bus. The verb itself proceeds
// normally. The caller holds the lock.
//
// Who receives it: the webhook, and the Security & Safety domain, which
// applies alarm.duress_visibility and renders a report. The alarm MQTT
// publisher does NOT subscribe — a duress disarm therefore appears on
// the alarm plane as an ordinary disarm, which is what the covert design
// intends for a screen an attacker can see, but also means that at
// visibility `hidden` an installation without a webhook is told nothing
// at all. Stated here because the surrounding code once claimed MQTT as
// a consumer and no test disagreed.
func (e *Engine) fireDuress(ctx context.Context, a *zone, verb, by, source string) {
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
		ZoneID: a.id, ZoneName: a.name,
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

// Disarm ends any alarm and returns the zone to disarmed. It is never
// state-gated (S6) and implies silence: the open incident is marked
// silenced and closed, and all outputs stop. It is a code-free wrapper
// over DisarmWithCode for the many call sites that never carry a code.
func (e *Engine) Disarm(ctx context.Context, zoneID, by, source string) error {
	return e.DisarmWithCode(ctx, zoneID, by, source, "")
}

// DisarmWithCode is Disarm with an alarm code, honoring the zone's
// CodePolicy (notes/concepts/alarm-concept.md §11). Disarming an
// already-disarmed zone is an idempotent no-op: it never refuses, so a
// wrong or missing code changes nothing and cannot be used to probe
// which codes exist.
//
// A supplied code is still looked at there, for one reason: duress.
// Coercion frequently starts with the attacker disarming the zone, and
// gating duress detection on the arm state would make the covert
// channel unavailable in exactly that situation. The look-up goes
// through [DuressMatcher], not through the validator: on a path that
// decides nothing, a wrong code must cost nothing either — no
// rate-limit budget, no fault row. The fan-out is silent and the
// verb's own outcome stays unchanged.
func (e *Engine) DisarmWithCode(ctx context.Context, zoneID, by, source, code string) error {
	// Which resolution applies depends on the state, and the resolution
	// itself runs without the lock (see [Engine.resolveCode]), so the
	// state is read first and re-read afterwards.
	_, state, ok := e.zoneCodeContext(zoneID)
	if !ok {
		return ErrUnknownZone
	}
	if state == hmenum.AlarmZoneStateDisarmed {
		identity, duress := e.probeDuress(ctx, zoneID, codeVerbDisarm, code, source)
		if applied, err := e.disarmIdle(ctx, zoneID, by, source, identity, duress); applied {
			return err
		}
		// The zone was armed while the probe ran. A duress probe
		// authenticated nothing, so this must not disarm on it: fall
		// through and resolve the code for real.
	}
	policy, _, ok := e.zoneCodeContext(zoneID)
	if !ok {
		return ErrUnknownZone
	}
	identity, duress, cerr := e.resolveCode(ctx, zoneID, policy, codeVerbDisarm, code, source)
	if cerr != nil {
		return cerr
	}
	return e.disarmResolved(ctx, zoneID, by, source, identity, duress)
}

// disarmIdle applies the no-op branch of a disarm to an already-disarmed
// zone: the covert duress fan-out on a duress code, plus the cancel of a
// pending auto-rearm, because an explicit disarm means the operator wants
// the zone to stay off.
//
// It reports applied=false — without touching anything — when the zone
// left the disarmed state while the code was being resolved, so the
// caller can resolve the code properly instead of disarming on a probe.
func (e *Engine) disarmIdle(ctx context.Context, zoneID, by, source, identity string, duress bool) (applied bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.zones[zoneID]
	if !ok {
		return true, ErrUnknownZone
	}
	if a.state != hmenum.AlarmZoneStateDisarmed {
		return false, nil
	}
	if duress {
		if identity != "" && by == "" {
			by = identity
		}
		e.fireDuress(ctx, a, codeVerbDisarm, by, source)
	}
	if a.autoRearmCancel != nil {
		a.cancelAutoRearm()
		e.persist(ctx, a)
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassArm, Event: "auto_rearm_cancelled",
			Actor: by, Source: source,
		})
	}
	return true, nil
}

// disarmResolved applies a disarm whose code has already been resolved.
// A zone that returned to disarmed while the code was being verified
// takes the idempotent no-op branch, with the duress fan-out the resolved
// code carries.
func (e *Engine) disarmResolved(ctx context.Context, zoneID, by, source, identity string, duress bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.zones[zoneID]
	if !ok {
		return ErrUnknownZone
	}
	if identity != "" && by == "" {
		by = identity
	}
	if duress {
		e.fireDuress(ctx, a, codeVerbDisarm, by, source)
	}
	if a.state == hmenum.AlarmZoneStateDisarmed {
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
	prevPolicy := a.cfg.Modes[a.mode].Outputs
	e.disarmLocked(ctx, a, by, source)
	e.refreshReadiness(a)
	if prevPolicy.ArmDisarmChirps {
		e.chirp(ctx, a, ChirpRequest{Kind: ChirpDisarmSquawk})
	}
	return nil
}

// Silence stops all sounding outputs of the zone's current incident
// and persists the silenced flag so no engine path — including a
// restore — re-fires acoustic outputs for it (S3). State is
// unaffected; notification outputs are never cancelled. Silence never
// fails on state and is deliberately not gated. It is a code-free
// wrapper over SilenceWithCode.
func (e *Engine) Silence(ctx context.Context, zoneID, by, source string) error {
	return e.SilenceWithCode(ctx, zoneID, by, source, "")
}

// SilenceWithCode is Silence with an alarm code. Silence defaults to
// code-free (S3); a per-source RequireSilence policy or a supplied code
// engages the CodeValidator (notes/concepts/alarm-concept.md §11).
func (e *Engine) SilenceWithCode(ctx context.Context, zoneID, by, source, code string) error {
	policy, _, ok := e.zoneCodeContext(zoneID)
	if !ok {
		return ErrUnknownZone
	}
	identity, duress, cerr := e.resolveCode(ctx, zoneID, policy, codeVerbSilence, code, source)
	if cerr != nil {
		return cerr
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.zones[zoneID]
	if !ok {
		return ErrUnknownZone
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

// SilenceAll silences every zone (the global silence surface).
func (e *Engine) SilenceAll(ctx context.Context, by, source string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, id := range e.sortedZoneIDs() {
		e.silenceLocked(ctx, e.zones[id], by, source)
	}
}

// silenceLocked applies the silence verb to one zone. Even without an
// open incident it issues a StopAll — stopping more than necessary is
// always the safe direction. The caller holds the lock.
func (e *Engine) silenceLocked(ctx context.Context, a *zone, by, source string) {
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
func (e *Engine) silenceIncident(ctx context.Context, a *zone, by, source string) {
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

// Acknowledge marks the zone's open incident as seen. Journal-only —
// no state effect.
func (e *Engine) Acknowledge(ctx context.Context, zoneID, by, source string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.zones[zoneID]
	if !ok {
		return ErrUnknownZone
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
		a.state == hmenum.AlarmZoneStateDisarmed {
		e.journalFault(ctx, a, "tamper_while_disarmed", nil, 0)
	}

	// Always-on hazard/panic sensors bypass the arm-state machine: a
	// fresh activation fires the class output policy 24/7, independent
	// of zone state. Placed before the walk-test consumer so a real
	// hazard during a walk test is never suppressed.
	if s.cfg.AlwaysOn && active && !wasActive {
		e.alwaysOnFromSensor(ctx, a, s)
		return
	}

	// A running walk test consumes activations of the disarmed zone.
	if e.walkTestObserve(ctx, a, sensorID, active) && a.state == hmenum.AlarmZoneStateDisarmed {
		return
	}

	if !active {
		// Clearing before the hold window elapses discards the held
		// activation — the hold-time debounce contract.
		s.cancelHold()
		if a.openAtArm[sensorID] {
			delete(a.openAtArm, sensorID)
			e.persist(ctx, a)
		}
		// Closing during the exit delay may complete the arm early.
		if a.state == hmenum.AlarmZoneStateArming && s.cfg.ArmAfterClosing && s.cfg.InMode(a.mode) {
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
	if a.state == hmenum.AlarmZoneStateDisarmed {
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
	e.gateSensorActivation(ctx, a, s, sensorID)
}

// dispatchSensorActivation routes a fresh member-sensor activation
// through the arm-state machine. The caller holds the lock and has
// already filtered walk tests, always-on sensors, bypasses, and
// mode membership.
func (e *Engine) dispatchSensorActivation(ctx context.Context, a *zone, s *sensorState, sensorID string) {
	cause := causeFromSensor(causeKindSensor, s.row)

	switch a.state {
	case hmenum.AlarmZoneStateArming:
		// Instant sensors trigger during the exit delay; sensors
		// flagged use_exit_delay may be active while leaving.
		if !s.cfg.UseExitDelay {
			e.trigger(ctx, a, cause, FireOptions{})
		}
	case hmenum.AlarmZoneStateArmed:
		e.routeActivation(ctx, a, s, cause)
	case hmenum.AlarmZoneStatePending:
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
	case hmenum.AlarmZoneStateTriggered:
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
		// The zone stays triggered and the state machine starts no new
		// output cycle, but the contribution is recorded: "the kitchen
		// detector also went off" is exactly what attribution needs.
		if e.recordSource(ctx, a, incID, causeFromSensor(causeKindSensor, s.row)) {
			e.publishSourcesChanged(a, incID)
		}
	case hmenum.AlarmZoneStateDisarmed:
	}
}

// routeActivation routes an armed-state activation into pending or an
// instant trigger, per the sensor's flags. The caller holds the lock.
func (e *Engine) routeActivation(ctx context.Context, a *zone, s *sensorState, cause incidentCause) {
	mcfg := a.cfg.Modes[a.mode]
	if s.cfg.UseEntryDelay {
		if d := mcfg.entryDelay(s.cfg); d > 0 {
			from := a.state
			a.state = hmenum.AlarmZoneStatePending
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
	if s.cfg.TriggerWhenUnavailable && a.state == hmenum.AlarmZoneStateArmed {
		e.routeActivation(ctx, a, s, causeFromSensor(causeKindUnavailable, s.row))
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
// central. An armed zone with member sensors on a lost central reacts
// per its central-loss policy — loudly, never silently.
func (e *Engine) HandleCentralConnectivity(ctx context.Context, centralName string, connected bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return
	}
	for _, id := range e.sortedZoneIDs() {
		a := e.zones[id]
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

// trigger opens (or reuses) the zone's incident and enters triggered.
// Ordering is safety-first: the incident with its counters is
// persisted before outputs fire, so a crash can only over-count.
// A persist failure must not mute the alarm — the engine then runs an
// unpersisted incident and journals the degradation (S7).
// The caller holds the lock.
func (e *Engine) trigger(ctx context.Context, a *zone, cause incidentCause, opts FireOptions) {
	if a.state == hmenum.AlarmZoneStateTriggered {
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
			ZoneID:            a.id,
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

	// Two-phase pre-alarm (notes/concepts/alarm-concept.md §15 row 21): a fresh,
	// live (not restored) intrusion trigger with PreAlarmSeconds first
	// runs a pre-alarm phase — only the pre-alarm output classes fire —
	// then escalates to the full policy on elapse. A restore treats a
	// persisted pre-alarm phase as a full trigger, so it never re-enters
	// the pre-alarm phase here.
	preAlarm := newIncident && !opts.Restored && mcfg.PreAlarmSeconds > 0

	a.state = hmenum.AlarmZoneStateTriggered
	a.pendingCause = ""
	a.preAlarm = preAlarm

	if preAlarm {
		opts.PreAlarm = true
		e.scheduleStateTimer(a, timerKindPreAlarm, time.Duration(mcfg.PreAlarmSeconds)*time.Second)
	} else {
		e.scheduleStateTimer(a, timerKindTrigger, dur)
	}
	e.persist(ctx, a)
	e.recordSource(ctx, a, a.incident.ID, cause)

	e.fireCycle(ctx, a, *a.incident, opts)
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
		ZoneID: a.id, ZoneName: a.name,
		IncidentID: a.incident.ID,
		SensorID:   cause.SensorID, SensorName: cause.SensorName,
		Cause: cause.Kind, Mode: a.mode,
		Sources: a.sourcesCopy(),
	})
	e.publishState(a, from, "", "engine")
}

// onPreAlarmElapsed escalates a pre-alarm phase to the full trigger, or
// finishes the incident when it was silenced during the pre-alarm phase
// (a silence during pre-alarm cancels the full escalation). The caller
// holds the lock.
func (e *Engine) onPreAlarmElapsed(ctx context.Context, a *zone) {
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
		e.fireCycle(ctx, a, *inc, FireOptions{Policy: mcfg.Outputs})
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
func (e *Engine) onTriggerElapsed(ctx context.Context, a *zone) {
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
			e.fireCycle(ctx, a, *inc, FireOptions{Cycle: inc.RetriggerCycles, Policy: mcfg.Outputs})
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
func (e *Engine) finishTriggered(ctx context.Context, a *zone, journalEvent string) {
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
		a.state = hmenum.AlarmZoneStateDisarmed
		a.mode = hmenum.AlarmModeDisarmed
		a.bypassed = map[string]bool{}
		a.openAtArm = map[string]bool{}
		// Auto-rearm (notes/concepts/alarm-concept.md §15 row 22): schedule a
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
// from the zone, including the redundant silence marker. The caller
// holds the lock.
func (e *Engine) closeIncident(ctx context.Context, a *zone, reason string) {
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
	a.resetSources()
}

// scheduleStateTimer replaces the zone's state timer. The caller
// holds the lock.
func (e *Engine) scheduleStateTimer(a *zone, kind string, d time.Duration) {
	if a.timerCancel != nil {
		a.timerCancel()
	}
	a.timerSeq++
	seq := a.timerSeq
	a.timerKind = kind
	a.timerDeadline = e.clk.Now().Add(d)
	a.timerRemaining = d
	zoneID := a.id
	a.timerCancel = e.sched.Schedule(d, func() {
		e.onStateTimerFired(zoneID, seq)
	})
}

// onStateTimerFired dispatches a state-timer expiry, discarding stale
// fires from cancelled or replaced timers. It runs on the engine
// lifetime context — a countdown must not die with the request that
// scheduled it.
//
//nolint:contextcheck // timer fires deliberately detach from the scheduling caller's ctx (see lifeCtx)
func (e *Engine) onStateTimerFired(zoneID string, seq uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.zones[zoneID]
	if !ok || a.timerSeq != seq {
		return
	}
	kind := a.timerKind
	a.timerCancel = nil
	a.timerKind = ""
	ctx := e.lifeCtx
	switch kind {
	case timerKindExit:
		if a.state == hmenum.AlarmZoneStateArming {
			e.completeArm(ctx, a, a.state, "engine", "engine")
		}
	case timerKindEntry:
		if a.state == hmenum.AlarmZoneStatePending {
			e.trigger(ctx, a, pendingElapsedCause(a), FireOptions{})
		}
	case timerKindPreAlarm:
		if a.state == hmenum.AlarmZoneStateTriggered && a.preAlarm {
			e.onPreAlarmElapsed(ctx, a)
		}
	case timerKindTrigger:
		if a.state == hmenum.AlarmZoneStateTriggered {
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
func (e *Engine) scheduleArmCloseDebounce(a *zone, sensorID string) {
	a.cancelDebounce()
	a.debounceSeq++
	seq := a.debounceSeq
	zoneID := a.id
	a.debounceCancel = e.sched.Schedule(armAfterClosingDebounce, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		aa, ok := e.zones[zoneID]
		if !ok || aa.debounceSeq != seq || aa.state != hmenum.AlarmZoneStateArming {
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
func (e *Engine) chirp(ctx context.Context, a *zone, req ChirpRequest) {
	if err := e.outputs.Chirp(ctx, a.id, req); err != nil {
		e.log.Debug("alarm chirp failed", "zone", a.id, "kind", string(req.Kind), "error", err)
	}
}

// startTicks starts the 1 Hz countdown tick chain for the running
// exit or entry delay: every tick publishes an AlarmCountdownEvent
// for live UI countdowns, and — when the mode's policy enables
// countdown ticks — forwards a chirp request. The chain follows the
// state timer and dies with it. The caller holds the lock.
//
//nolint:contextcheck // tick fires deliberately detach from the scheduling caller's ctx (see lifeCtx)
func (e *Engine) startTicks(a *zone, timerKind string) {
	a.cancelTicks()
	total := a.timerRemaining
	if total <= 0 {
		return
	}
	a.tickSeq++
	seq := a.tickSeq
	zoneID := a.id
	var schedule func()
	schedule = func() {
		a.tickCancel = e.sched.Schedule(time.Second, func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			aa, ok := e.zones[zoneID]
			if !ok || aa.tickSeq != seq || aa.timerKind != timerKind || aa.timerCancel == nil {
				return
			}
			remaining := aa.timerDeadline.Sub(e.clk.Now())
			if remaining <= 0 {
				return
			}
			e.sink.Publish(hmevent.AlarmCountdownEvent{
				Base:   hmevent.NewBaseAt(e.clk.Now()),
				ZoneID: zoneID, Kind: timerKind,
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
// zone enters triggered without an engine-side output cycle — the
// hardware is already sounding; the driver layer arms the bounded
// stop watchdog instead.
func (e *Engine) AdoptSounding(ctx context.Context, zoneID string, outputIDs []string) (adopted bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.zones[zoneID]
	if !ok {
		return false, ErrUnknownZone
	}
	if a.state == hmenum.AlarmZoneStateTriggered && a.incident != nil {
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
			ZoneID:            a.id,
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
	a.state = hmenum.AlarmZoneStateTriggered
	e.scheduleStateTimer(a, timerKindTrigger, dur)
	e.persist(ctx, a)
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTrigger, Event: "sounding_siren_adopted",
		IncidentID: a.incident.ID,
		Details:    map[string]any{"outputs": outputIDs},
	})
	e.sink.Publish(hmevent.AlarmTriggeredEvent{
		Base:   hmevent.NewBaseAt(now),
		ZoneID: a.id, ZoneName: a.name,
		IncidentID: a.incident.ID,
		Cause:      causeKindAdopted, Mode: a.mode,
		Sources: a.sourcesCopy(),
	})
	e.publishState(a, from, "engine:reconcile", "engine")
	return true, nil
}

// ReevaluateSensors refreshes every armed zone's sensor values from
// the SensorReader and routes activations that happened during a
// blind window (notes/concepts/alarm-concept.md §10.1, CCU-reconnect row) —
// the same comparison against the open-at-arm baseline a restore
// runs. Safe to call repeatedly; disarmed zones are untouched.
func (e *Engine) ReevaluateSensors(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return
	}
	for _, id := range e.sortedZoneIDs() {
		a := e.zones[id]
		if a.state != hmenum.AlarmZoneStateArmed {
			continue
		}
		e.reEvaluateAfterRestore(ctx, a)
		e.refreshReadiness(a)
	}
}

// PanicTrigger fires a panic incident on zoneID independent of its arm
// state (notes/concepts/alarm-concept.md §7). silent suppresses acoustic outputs
// (silent panic / duress panic). by/source attribute the trigger. It is
// the verb behind keypad/remote panic keys and the MQTT TRIGGER payload.
func (e *Engine) PanicTrigger(ctx context.Context, zoneID string, silent bool, by, source string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return ErrInvalidState
	}
	a, ok := e.zones[zoneID]
	if !ok {
		return ErrUnknownZone
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
func (e *Engine) alwaysOnFromSensor(ctx context.Context, a *zone, s *sensorState) {
	causeKind := causeKindHazard
	policy := a.cfg.HazardOutputs
	if s.row.SensorType == hmenum.AlarmSensorTypePanic {
		causeKind = causeKindPanic
		policy = a.cfg.PanicOutputs
		if s.cfg.PanicSilent {
			policy.Silent = true
		}
	}
	cause := causeFromSensor(causeKind, s.row)
	e.alwaysOnFire(ctx, a, causeKind, policy, cause, s.row.Name, "engine")
}

// alwaysOnFire drives an always-on (hazard/panic) incident. It bypasses
// the arm-state machine but still drives the panel to triggered so the
// alarm is visible everywhere; the state it interrupts is recorded in
// preTriggerState and restored on post-trigger. When the zone is already
// triggered it only layers the class output cycle onto the running
// incident, leaving that incident's state/timer/return untouched. The
// caller holds the lock.
func (e *Engine) alwaysOnFire(ctx context.Context, a *zone, causeKind string, policy OutputPolicy, cause incidentCause, by, source string) {
	now := e.clk.Now()
	if a.state == hmenum.AlarmZoneStateTriggered {
		// An incident is already running: add the class outputs (unless
		// silenced) and journal, but do not disturb the running incident.
		if a.incident != nil && !a.incident.Silenced {
			e.fireCycle(ctx, a, *a.incident, FireOptions{Policy: policy})
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
		// A hazard detector joining a running intrusion incident is the
		// sharpest case for the ledger: the escalation class changes
		// even though the zone state does not.
		if e.recordSource(ctx, a, incID, cause) {
			e.publishSourcesChanged(a, incID)
		}
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
			ZoneID:            a.id,
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

	a.state = hmenum.AlarmZoneStateTriggered
	e.scheduleStateTimer(a, timerKindTrigger, dur)
	e.persist(ctx, a)
	e.recordSource(ctx, a, a.incident.ID, cause)
	e.fireCycle(ctx, a, *a.incident, FireOptions{Policy: policy})
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTrigger, Event: "triggered",
		Actor: by, Source: source, IncidentID: a.incident.ID,
		Details: map[string]any{"cause": causeKind, "sensor_id": cause.SensorID, "mode": string(a.mode)},
	})
	e.sink.Publish(hmevent.AlarmTriggeredEvent{
		Base:   hmevent.NewBaseAt(now),
		ZoneID: a.id, ZoneName: a.name,
		IncidentID: a.incident.ID,
		SensorID:   cause.SensorID, SensorName: cause.SensorName,
		Cause: causeKind, Mode: a.mode,
		Sources: a.sourcesCopy(),
	})
	e.publishState(a, from, by, source)
}

// finishAlwaysOn ends an always-on incident and returns the zone to the
// state it interrupted (armed if it was armed-side, disarmed otherwise —
// so a hazard during a disarmed period drops back to disarmed, and one
// while armed resumes protection). The caller holds the lock.
func (e *Engine) finishAlwaysOn(ctx context.Context, a *zone, journalEvent, actor string) {
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
		a.state = hmenum.AlarmZoneStateArmed
		a.mode = target
		e.recaptureOpenBaseline(a)
	} else {
		a.state = hmenum.AlarmZoneStateDisarmed
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
func alwaysOnPolicyForIncident(a *zone, inc *sqlitestore.AlarmIncident) OutputPolicy {
	var c incidentCause
	if inc != nil {
		_ = json.Unmarshal([]byte(inc.CauseJSON), &c)
	}
	if c.Kind == causeKindPanic {
		return a.cfg.PanicOutputs
	}
	return a.cfg.HazardOutputs
}

// scheduleAutoRearmIfConfigured arms the auto-rearm timer when the zone
// configures it and the pre-incident mode is armable. It sets the timer
// fields (so a following persist captures the tuple) and journals. The
// caller holds the lock.
func (e *Engine) scheduleAutoRearmIfConfigured(ctx context.Context, a *zone, rearmMode hmenum.AlarmMode, actor string) {
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

// scheduleAutoRearm replaces the zone's auto-rearm timer. The caller
// holds the lock. The callback runs on the engine lifetime context.
//
//nolint:contextcheck // timer fires deliberately detach from the scheduling caller's ctx (see lifeCtx)
func (e *Engine) scheduleAutoRearm(a *zone, mode hmenum.AlarmMode, d time.Duration) {
	a.cancelAutoRearm()
	a.autoRearmMode = mode
	a.autoRearmSeq++
	seq := a.autoRearmSeq
	a.autoRearmDeadline = e.clk.Now().Add(d)
	zoneID := a.id
	a.autoRearmCancel = e.sched.Schedule(d, func() {
		e.onAutoRearmFired(zoneID, seq)
	})
}

// deferAutoRearm restarts the auto-rearm quiet period after member
// activity. The caller holds the lock.
func (e *Engine) deferAutoRearm(ctx context.Context, a *zone, sensorID string) {
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
// A callback already in flight when Stop cancels the timer still
// reaches this point, so the stopped engine is checked here as well:
// arming after the final state snapshot would leave the persisted row
// describing a zone the next boot never armed.
//
//nolint:contextcheck // timer fires deliberately detach from the scheduling caller's ctx (see lifeCtx)
func (e *Engine) onAutoRearmFired(zoneID string, seq uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return
	}
	a, ok := e.zones[zoneID]
	if !ok || a.autoRearmSeq != seq || a.autoRearmCancel == nil {
		return
	}
	a.autoRearmCancel = nil
	e.onAutoRearmElapsed(e.lifeCtx, a)
}

// onAutoRearmElapsed attempts the quiet-period rearm. It arms with
// force=false: any remaining blocker journals a fail-visible
// failed-to-arm fault and leaves the zone disarmed (notes/concepts/alarm-concept.md
// §15 row 22). The caller holds the lock.
func (e *Engine) onAutoRearmElapsed(ctx context.Context, a *zone) {
	mode := a.autoRearmMode
	a.autoRearmMode = ""
	if a.state != hmenum.AlarmZoneStateDisarmed || !mode.Armed() {
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

// persist writes the zone's full state row. A failure is journaled
// and logged, never silently swallowed, and does not abort the
// transition — the machine keeps operating from memory (S7).
// The caller holds the lock.
func (e *Engine) persist(ctx context.Context, a *zone) {
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
		ZoneID:      a.id,
		State:       a.state,
		Mode:        a.mode,
		BypassJSON:  a.encodeBypass(),
		IncidentID:  incID,
		TimersJSON:  encodeTimers(timers),
		ContextJSON: a.encodeContext(),
		UpdatedAtMS: unixMS(now),
	}
	if err := e.stateStore.Upsert(ctx, row); err != nil {
		e.log.Error("alarm state persist failed", "zone", a.id, "error", err)
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassFault, Event: "state_persist_failed",
			Details: map[string]any{"error": err.Error()},
		})
	}
}

// publishState emits the state-changed event. The caller holds the
// lock and has already updated a.state.
func (e *Engine) publishState(a *zone, from hmenum.AlarmZoneState, by, source string) {
	incID := int64(0)
	if a.incident != nil {
		incID = a.incident.ID
	}
	e.sink.Publish(hmevent.AlarmStateChangedEvent{
		Base:   hmevent.NewBaseAt(e.clk.Now()),
		ZoneID: a.id, ZoneName: a.name,
		From: from, To: a.state, Mode: a.mode,
		ChangedBy: by, Source: source, IncidentID: incID,
	})
}

// journalEntry appends to the journal; failures are logged only — a
// journal outage must never block an alarm action.
func (e *Engine) journalEntry(ctx context.Context, a *zone, entry JournalEntry) {
	entry.ZoneID = a.id
	if _, err := e.journal.Append(ctx, entry); err != nil {
		e.log.Error("alarm journal append failed", "zone", a.id, "event", entry.Event, "error", err)
	}
}

// journalFault appends a fault-class entry (fail-visible, S7).
func (e *Engine) journalFault(ctx context.Context, a *zone, event string, cause error, incidentID int64) {
	details := map[string]any{}
	if cause != nil {
		details["error"] = cause.Error()
	}
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassFault, Event: event,
		IncidentID: incidentID, Details: details,
	})
}

// lookupSensor resolves a sensor ID to its zone and state. The caller
// holds the lock.
func (e *Engine) lookupSensor(sensorID string) (*zone, *sensorState) {
	zoneID, ok := e.sensorIndex[sensorID]
	if !ok {
		return nil, nil
	}
	a, ok := e.zones[zoneID]
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
func (e *Engine) isArmedState(st hmenum.AlarmZoneState) bool {
	switch st {
	case hmenum.AlarmZoneStateArmed, hmenum.AlarmZoneStateArming, hmenum.AlarmZoneStatePending:
		return true
	default:
		return false
	}
}

// sortedZoneIDs returns the managed zone IDs in stable order. The
// caller holds the lock.
func (e *Engine) sortedZoneIDs() []string {
	ids := make([]string, 0, len(e.zones))
	for id := range e.zones {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// noopOutputs is the OutputPort fallback for an Engine built without a
// wired driver layer (e.g. in a test that has no siren/output hardware
// to drive): it does nothing and reports success. Production wires
// Outputs to the real manager — see internal/alarm/service.go.
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
