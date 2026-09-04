// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine

import (
	"context"
	"time"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ZoneStore loads the configured alarm zones. Satisfied by
// *sqlitestore.AlarmZoneStore.
type ZoneStore interface {
	GetAll(ctx context.Context) ([]sqlitestore.AlarmZoneRow, error)
}

// SensorStore loads the enrolled sensors. Satisfied by
// *sqlitestore.AlarmSensorStore.
type SensorStore interface {
	GetAll(ctx context.Context) ([]sqlitestore.AlarmSensorRow, error)
}

// StateStore persists the per-zone arm state. Satisfied by
// *sqlitestore.AlarmStateStore.
type StateStore interface {
	Upsert(ctx context.Context, row sqlitestore.AlarmStateRow) error
	Get(ctx context.Context, zoneID string) (sqlitestore.AlarmStateRow, bool, error)
	Delete(ctx context.Context, zoneID string) error
}

// IncidentStore persists trigger episodes and their safety counters.
// Satisfied by *sqlitestore.AlarmIncidentStore.
type IncidentStore interface {
	Create(ctx context.Context, inc sqlitestore.AlarmIncident) (int64, error)
	Get(ctx context.Context, id int64) (sqlitestore.AlarmIncident, bool, error)
	GetOpenByZone(ctx context.Context, zoneID string) (sqlitestore.AlarmIncident, bool, error)
	MarkSilenced(ctx context.Context, id, atMS int64, by string) error
	IncrementRetriggerCycles(ctx context.Context, id int64) error
	IncrementRestoreRefires(ctx context.Context, id int64) error
	SetTriggerDeadline(ctx context.Context, id, deadlineMS int64) error
	Close(ctx context.Context, id, atMS int64, reason string) error
}

// RuntimeStore persists the engine's boot counter. Satisfied by
// *sqlitestore.AlarmRuntimeStore.
type RuntimeStore interface {
	IncrementBootCount(ctx context.Context, nowMS int64) (int64, error)
}

// FireOptions parameterizes one output activation cycle.
type FireOptions struct {
	// Cycle is the zero-based activation cycle within the incident
	// (0 = the initial trigger, 1.. = re-trigger cycles).
	Cycle int
	// Degraded restricts the cycle to optical + notification outputs.
	// Set by the restart-loop breaker: a crash-looping daemon must not
	// turn bounded acoustic activations into an unbounded nuisance.
	Degraded bool
	// Restored marks a restore-driven re-fire after a restart.
	Restored bool
	// PreAlarm restricts the cycle to the pre-alarm output classes
	// (chirp + notification + light) — the first phase of a two-phase
	// trigger. The driver filters by it; the full policy escalates when
	// the pre-alarm timer elapses.
	PreAlarm bool
	// Policy is the resolved output policy of the mode that was armed
	// at trigger time. The engine owns the configuration; the driver
	// layer only filters by it.
	Policy OutputPolicy
	// ZoneName and Sources snapshot the zone at fire time: its display
	// name and the data points that have contributed to the incident so
	// far, oldest first.
	//
	// They travel with the cycle instead of being looked up because the
	// driver's notification sink runs with the engine lock held (see
	// [OutputPort]) — a sink that asked the engine for either one would
	// self-deadlock on the first notification output an operator enrols.
	ZoneName string
	Sources  []hmevent.SecuritySourceRef
}

// ChirpKind names the confirmation/feedback chirp classes
// (notes/concepts/alarm-concept.md §7). Chirps are best-effort and degrade
// first under duty-cycle pressure (S5).
type ChirpKind string

// ChirpKind values.
const (
	// ChirpArmSquawk confirms a completed arm (1× squawk).
	ChirpArmSquawk ChirpKind = "arm_squawk"
	// ChirpDisarmSquawk confirms a disarm (2× squawk convention).
	ChirpDisarmSquawk ChirpKind = "disarm_squawk"
	// ChirpCountdownTick is one exit-delay countdown tick.
	ChirpCountdownTick ChirpKind = "countdown_tick"
	// ChirpEntryWarning is one entry-delay warning tick.
	ChirpEntryWarning ChirpKind = "entry_warning"
	// ChirpChime is the door-chime-while-disarmed tone.
	ChirpChime ChirpKind = "chime"
)

// ChirpRequest carries one chirp emission to the driver layer.
type ChirpRequest struct {
	Kind ChirpKind
	// Remaining and Total describe the countdown for tick kinds;
	// zero otherwise.
	Remaining time.Duration
	Total     time.Duration
}

// OutputPort is the engine's hand-off to the output-driver layer. The
// contract split: the engine accounts cycles and re-fires on the
// incident *before* calling FireCycle (over-counting on crash is safe,
// under-counting is not); the drivers clamp every acoustic duration,
// write the incident's acoustic ledger, and verify stops. StopAll
// silences every sounding output of the incident and never touches
// notification outputs.
//
// All methods run with the engine lock held: an implementation must
// never call back into an engine verb synchronously (self-deadlock on
// the non-reentrant mutex) — long-running device I/O belongs on the
// driver's own goroutines. Chirp is best-effort feedback: the driver
// may thin or drop requests (S5) and the engine only logs errors.
type OutputPort interface {
	FireCycle(ctx context.Context, zoneID string, incident sqlitestore.AlarmIncident, opts FireOptions) error
	StopAll(ctx context.Context, zoneID string, incidentID int64) error
	Chirp(ctx context.Context, zoneID string, req ChirpRequest) error
}

// MotionResetPort clears the latched motion state of enrolled sensors
// by writing their channel's RESET_MOTION parameter.
//
// An HmIP motion detector holds its MOTION flag for its configured
// blocking time unless the write-only RESET_MOTION action is fired.
// While it is held the sensor reads as open, which blocks an arm or
// forces an auto-bypass — so the engine needs a way to clear it rather
// than only report it.
//
// The blocking time is the MASTER parameter MOTION_ACTIVE_TIME, an
// ordinal 0..7 with default 4, not a duration: the CCU's own editor maps
// it 0=15 s, 1=30 s, 2=1 min, 3=2 min, 4=4 min, 5=8 min, 6=16 min,
// 7=32 min, each index written beside its value
// (../OpenCCU-Base/www/config/easymodes/hmip/MOTIONDETECTOR_TRANSCEIVER.tcl:130-137;
// the same table appears in PRESENCEDETECTOR_TRANSCEIVER.tcl). So the
// shipped default holds MOTION for four minutes — long enough that
// arming without a reset would routinely fail.
//
// The rule is HmIP-only, and narrower than "a motion detector":
//   - Classic BidCos detectors expose no RESET_MOTION at all; the CCU
//     un-latches them itself on a server-side timer. They report
//     Supports=false, which is correct — see resetParameterFor in
//     internal/alarm/motionreset.go for the sources.
//   - What a successful write does is UNVERIFIED in the CCU sources:
//     whether it clears MOTION at once or only ends the blocking
//     interval is not stated anywhere in them, and only a live trace
//     would settle it. Either way the sensor stops reading as held,
//     which is what the arming path needs.
//   - The CCU's own UI offers the reset button only when the device is a
//     permanent listener (it reads MASTER PERMANENT_FULL_RX and hides the
//     button while it is 0 —
//     ../OpenCCU-Base/www/rega/esp/controls/motiondetector.fn:112-118).
//     Supports below does not read that value, so on a non-permanent
//     listener the daemon writes and counts the sensor as resettable
//     where the CCU would not offer the action. Whether such a write
//     eventually lands is likewise unverified.
//
// Contract:
//   - Supports reports whether the sensor's channel exposes a writable
//     RESET_MOTION data point. It is the single source of truth for
//     "resettable": the engine derives both the reset set and the
//     reported count from it, so the count can never name a sensor the
//     reset would skip.
//   - Reset writes the parameter for one sensor. It is best-effort:
//     the caller records failures and carries on, because a
//     non-responding detector must not be able to block arming.
//
// Reset runs WITHOUT the engine lock held (unlike [OutputPort]) — the
// write goes to the radio and the engine must stay responsive to the
// events it triggers.
type MotionResetPort interface {
	Supports(row sqlitestore.AlarmSensorRow) bool
	Reset(ctx context.Context, row sqlitestore.AlarmSensorRow) error
}

// CodeValidator authenticates an alarm code for one verb on one zone
// (notes/concepts/alarm-concept.md §11). It is satisfied by the codes facade,
// which owns the code store; the engine only knows this port.
//
// Contract:
//   - A valid code returns its display identity and whether it is a
//     duress code, with a nil error. The verb then proceeds normally
//     (a duress code is not blocked — it fires a silent alarm instead).
//   - A supplied code that does not authenticate returns ErrInvalidCode.
//   - An empty code against an zone that has no applicable enabled code
//     returns a nil error with an empty identity: the requirement is
//     inert, so a code policy can never lock an zone out when no codes
//     exist. This resolves the "codes exist" half of the effective
//     disarm rule the engine cannot see through this port.
type CodeValidator interface {
	Validate(ctx context.Context, zoneID, verb, code, source string) (identity string, duress bool, err error)
}

// DuressMatcher is the side-effect-free half of [CodeValidator]: it
// reports whether a supplied code is an enabled duress code for the
// zone and verb, and does nothing else. It never consumes the source's
// rate-limit budget, never journals a fault, and never refuses a verb.
//
// The engine needs it wherever the verb's outcome is a no-op whatever
// the code is — a disarm of an already-disarmed zone. Duress detection
// has to stay available there, because coercion typically starts with
// the attacker disarming the zone; but running the full validator on a
// path that decides nothing would let anyone able to publish a disarm
// lock the code plane out for every zone by aiming wrong codes at a
// disarmed one, and would fill the journal with faults for attempts
// that were never a security decision.
//
// A CodeValidator that does not implement it loses duress detection on
// those no-op paths and gains nothing else; the codes facade does
// implement it.
type DuressMatcher interface {
	MatchDuress(ctx context.Context, zoneID, verb, code, source string) (identity string, duress bool)
}

// EventSink receives the engine's domain events for bus publishing.
type EventSink interface {
	Publish(e hmevent.Event)
}

// JournalEntry is one alarm-journal record as the engine emits it.
type JournalEntry struct {
	ZoneID     string
	Class      hmenum.AlarmJournalClass
	Event      string
	Actor      string
	Source     string
	IncidentID int64
	Hidden     bool
	Details    map[string]any
}

// Journal is the engine's journaling facade. Implementations persist
// the entry and publish the journal-appended event; a journal failure
// must never block an alarm action, so the engine logs returned errors
// and continues.
type Journal interface {
	Append(ctx context.Context, e JournalEntry) (int64, error)
}

// IncidentSourceLedger records every data point that contributed to an
// incident. Like [Journal] it is best-effort: the engine logs a failed
// append and continues, because losing an audit row must never mute an
// alarm. A nil ledger disables recording entirely.
//
// ListByIncident is the read half a restore needs: the in-memory
// accumulator is per-process, so a daemon that restarts mid-incident
// would otherwise resume with an empty source list and let the next
// detector to fire become the incident's headline sensor.
type IncidentSourceLedger interface {
	Append(ctx context.Context, row sqlitestore.AlarmIncidentSource) error
	ListByIncident(ctx context.Context, incidentID int64) ([]sqlitestore.AlarmIncidentSource, error)
}

// SensorReader supplies fresh sensor activation values during restore
// (a window opened while the daemon was down must be detected). A nil
// reader means no fresh values are available; restore then keeps the
// persisted view and relies on live events.
type SensorReader interface {
	CurrentActive(ctx context.Context, s sqlitestore.AlarmSensorRow) (active, known bool)
}

// TimerScheduler schedules engine callbacks after a delay. The
// returned cancel is idempotent; a cancelled timer never runs its
// callback, but a callback may already be in flight when cancel
// returns — the engine guards against stale fires with sequence
// numbers, not with the scheduler.
type TimerScheduler interface {
	Schedule(d time.Duration, fn func()) (cancel func())
}
