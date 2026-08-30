// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Intent-routing sentinel faults, surfaced in the journal so a
// misconfigured or unsupported hardware binding is visible (S7).
var (
	// errUnknownAction reports a remote binding whose action string is
	// not one of arm:<mode> / disarm / silence / panic.
	errUnknownAction = errors.New("alarm: unknown remote binding action")
	// errBindingIncomplete reports a binding missing its target zone.
	errBindingIncomplete = errors.New("alarm: code binding missing zone")
)

// CodeKind classifies an alarm-code row (notes/concepts/alarm-concept.md §11).
type CodeKind string

// CodeKind values.
const (
	// CodeKindPIN is a typed PIN (argon2id-hashed; validated by the
	// codes facade, not the intent router).
	CodeKindPIN CodeKind = "pin"
	// CodeKindKeypadSlot binds a WKP on-device user slot to an identity.
	CodeKindKeypadSlot CodeKind = "keypad_slot"
	// CodeKindRemoteKey binds a remote-control key press to a verb.
	CodeKindRemoteKey CodeKind = "remote_key"
)

// CodePerms are the per-code verb permissions (perms_json).
type CodePerms struct {
	Arm     bool `json:"arm"`
	Disarm  bool `json:"disarm"`
	Silence bool `json:"silence"`
}

// CodeBinding is the parsed binding_json union for the hardware code
// kinds. keypad_slot uses Central/DeviceAddress/Slot plus the arm
// target (ArmMode/ZoneID); remote_key uses Central/ChannelAddress/
// Parameter plus the action target (Action/ZoneID).
type CodeBinding struct {
	Central        string `json:"central,omitempty"`
	DeviceAddress  string `json:"device_address,omitempty"`
	Slot           int    `json:"slot,omitempty"`
	ArmMode        string `json:"arm_mode,omitempty"`
	ChannelAddress string `json:"channel_address,omitempty"`
	Parameter      string `json:"parameter,omitempty"`
	Action         string `json:"action,omitempty"`
	ZoneID         string `json:"zone_id,omitempty"`
}

// CodeRow is one parsed alarm_codes row (migration 028) as the intent
// router consumes it. The codes facade decodes the stored JSON columns
// into these fields; the argon2id hash is never part of this
// projection — hardware-code routing needs identity + binding, never
// the secret.
type CodeRow struct {
	ID           string
	Name         string
	Kind         CodeKind
	Duress       bool
	Perms        CodePerms
	Zones        []string
	Binding      CodeBinding
	ValidFromMS  int64
	ValidUntilMS int64
	Enabled      bool
}

// CodeSource supplies the parsed alarm-code rows to the intent router.
// The codes facade (internal/alarm/codes) implements it; until it is
// wired a nil source keeps hardware-code intent routing inert.
type CodeSource interface {
	Rows(ctx context.Context) ([]CodeRow, error)
}

// wkpCorrelationWindow bounds how long a WKP CODE_ID/CODE_STATE scan
// stays valid for correlation with a following PRESS_LOCK/PRESS_UNLOCK
// (notes/reference/alarm-assumptions.md Q4).
const wkpCorrelationWindow = 2 * time.Second

// wkpCodeStateKnownIndex is the VALUE_LIST index of
// KNOWN_CODE_ID_RECEIVED in the WKP CODE_STATE enum
// ([IDLE, KNOWN_CODE_ID_RECEIVED, UNKNOWN_CODE_DETECTED]).
const wkpCodeStateKnownIndex = 1

// wkpCodeStateKnownLabel is the string form of the same enum member,
// accepted alongside the integer index because the enum may surface
// either representation depending on the wire-decode path.
const wkpCodeStateKnownLabel = "KNOWN_CODE_ID_RECEIVED"

// wkpCodeContext caches the most recent CODE_ID / CODE_STATE scan of a
// keypad so a following press can be attributed to a user slot.
type wkpCodeContext struct {
	codeID int
	known  bool
	at     time.Time
}

// intentRouter turns keypad and remote edge events into engine verbs.
// WKP presses are correlated against the CODE_ID/CODE_STATE scan on the
// device's channel 0; remote keys are matched directly against their
// bound channel + parameter. It holds no engine lock of its own — the
// engine verbs it calls take theirs.
type intentRouter struct {
	svc *Service

	mu  sync.Mutex
	wkp map[string]*wkpCodeContext // central|device → last scan
}

// newIntentRouter builds the router bound to its owning service.
func newIntentRouter(svc *Service) *intentRouter {
	return &intentRouter{svc: svc, wkp: map[string]*wkpCodeContext{}}
}

// onEvent routes one data-point change. It is a no-op until a code
// source is wired: with no codes configured there is no identity a
// keypad or remote press could resolve to.
func (r *intentRouter) onEvent(ctx context.Context, centralName string, e hmevent.DataPointValueChangedEvent) {
	if r.svc.codeSourceRef() == nil {
		return
	}
	switch hmenum.Parameter(e.Key.Parameter) {
	case hmenum.ParameterCodeID:
		r.trackCodeID(centralName, e)
	case hmenum.ParameterCodeState:
		r.trackCodeState(centralName, e)
	case hmenum.ParameterPressLock:
		r.handleKeypadPress(ctx, centralName, e, true)
	case hmenum.ParameterPressUnlock:
		r.handleKeypadPress(ctx, centralName, e, false)
	default:
		if IsRemotePressParameter(hmenum.Parameter(e.Key.Parameter)) {
			r.handleRemotePress(ctx, centralName, e)
		}
		// Otherwise: not an intent-carrying parameter.
	}
}

// RemotePressParameters returns the press parameters a remote binding can
// fire on — the exact set [intentRouter.onEvent] routes to handleRemotePress.
//
// Exported because a binding is validated on write, far from here: a binding
// stored against a parameter this router never routes is accepted by the wire
// schema and then never fires, which is indistinguishable from a broken
// remote. The write path rejects it instead, and reads this set to know what
// to reject.
func RemotePressParameters() []hmenum.Parameter {
	return []hmenum.Parameter{hmenum.ParameterPressShort, hmenum.ParameterPressLong}
}

// IsRemotePressParameter reports whether p is one of [RemotePressParameters].
func IsRemotePressParameter(p hmenum.Parameter) bool {
	return slices.Contains(RemotePressParameters(), p)
}

// IsDispatchableRemoteAction reports whether [intentRouter.dispatchRemoteAction]
// has a branch for action: the fixed verbs, or an "arm:<mode>" prefix (a bare
// "arm:" is valid — dispatchArm defaults an empty mode to full protection).
//
// Same reason as [RemotePressParameters]: a verb the dispatcher does not know
// is stored inert rather than refused, so the write path asks here.
func IsDispatchableRemoteAction(action string) bool {
	switch action {
	case remoteActionDisarm, remoteActionSilence, remoteActionPanic:
		return true
	default:
		return strings.HasPrefix(action, remoteActionArmPrefix)
	}
}

// trackCodeID records the scanned user slot on the device's context.
func (r *intentRouter) trackCodeID(centralName string, e hmevent.DataPointValueChangedEvent) {
	id, ok := paramValueInt(e.NewValue)
	if !ok {
		return
	}
	key := devKey(centralName, deviceAddress(e.Key.ChannelAddress))
	r.mu.Lock()
	c := r.wkp[key]
	if c == nil {
		c = &wkpCodeContext{}
		r.wkp[key] = c
	}
	c.codeID = id
	c.at = r.svc.clk.Now()
	r.mu.Unlock()
}

// trackCodeState records whether the last scan was a recognised code.
func (r *intentRouter) trackCodeState(centralName string, e hmevent.DataPointValueChangedEvent) {
	key := devKey(centralName, deviceAddress(e.Key.ChannelAddress))
	r.mu.Lock()
	c := r.wkp[key]
	if c == nil {
		c = &wkpCodeContext{}
		r.wkp[key] = c
	}
	c.known = codeStateKnown(e.NewValue)
	c.at = r.svc.clk.Now()
	r.mu.Unlock()
}

// handleKeypadPress correlates a WKP lock/unlock press with the last
// scan and dispatches the corresponding verb. An uncorrelated or
// unbound press is journaled as a fault — an unauthenticated hardware
// interaction must be visible, never silently actioned or dropped.
func (r *intentRouter) handleKeypadPress(ctx context.Context, centralName string, e hmevent.DataPointValueChangedEvent, lock bool) {
	pairIdx, ok := wkpPairIndex(e.Key.ChannelAddress)
	if !ok {
		return
	}
	dev := deviceAddress(e.Key.ChannelAddress)

	r.mu.Lock()
	c := r.wkp[devKey(centralName, dev)]
	var codeID int
	var known bool
	var at time.Time
	if c != nil {
		codeID, known, at = c.codeID, c.known, c.at
	}
	r.mu.Unlock()

	now := r.svc.clk.Now()
	correlated := c != nil && known && codeID >= 1 && codeID <= 8 &&
		codeID == pairIdx && now.Sub(at) <= wkpCorrelationWindow
	if !correlated {
		r.journalKeypadUnmatched(ctx, centralName, dev, lock, codeID, pairIdx)
		return
	}

	row, found := r.lookupKeypadRow(ctx, centralName, dev, codeID, now)
	if !found {
		r.journalKeypadUnmatched(ctx, centralName, dev, lock, codeID, pairIdx)
		return
	}

	if lock {
		if !row.Perms.Arm {
			r.journalPermissionDenied(ctx, row, "keypad", "arm")
			return
		}
		r.dispatchArm(ctx, row, row.Binding.ArmMode, "keypad")
	} else {
		if !row.Perms.Disarm {
			r.journalPermissionDenied(ctx, row, "keypad", "disarm")
			return
		}
		r.dispatchDisarm(ctx, row, "keypad")
	}
}

// handleRemotePress matches a remote key press against its binding and
// dispatches the bound action. Unlike a keypad, an unbound remote press
// is not an alarm event, so a miss is silent — only bound keys act.
func (r *intentRouter) handleRemotePress(ctx context.Context, centralName string, e hmevent.DataPointValueChangedEvent) {
	rows, err := r.svc.codeRows(ctx)
	if err != nil {
		return
	}
	now := r.svc.clk.Now()
	for i := range rows {
		row := &rows[i]
		if row.Kind != CodeKindRemoteKey {
			continue
		}
		b := row.Binding
		if !centralMatches(b.Central, centralName) ||
			b.ChannelAddress != e.Key.ChannelAddress ||
			b.Parameter != e.Key.Parameter {
			continue
		}
		if !codeUsable(row, now) {
			continue
		}
		r.dispatchRemoteAction(ctx, row)
		return
	}
}

// dispatchRemoteAction executes the verb encoded in a remote binding's
// action ("arm:<mode>" | "disarm" | "silence" | "panic").
// The verbs a remote binding's action may carry. Named here because both the
// dispatcher below and the write-time validation in the REST handler must
// agree on them; a verb known to one and not the other is stored and never
// fires.
const (
	remoteActionArmPrefix = "arm:"
	remoteActionDisarm    = "disarm"
	remoteActionSilence   = "silence"
	remoteActionPanic     = "panic"
)

func (r *intentRouter) dispatchRemoteAction(ctx context.Context, row *CodeRow) {
	action := row.Binding.Action
	switch {
	case strings.HasPrefix(action, remoteActionArmPrefix):
		if !row.Perms.Arm {
			r.journalPermissionDenied(ctx, row, "remote", "arm")
			return
		}
		r.dispatchArm(ctx, row, strings.TrimPrefix(action, "arm:"), "remote")
	case action == remoteActionDisarm:
		if !row.Perms.Disarm {
			r.journalPermissionDenied(ctx, row, "remote", "disarm")
			return
		}
		r.dispatchDisarm(ctx, row, "remote")
	case action == remoteActionSilence:
		if !row.Perms.Silence {
			r.journalPermissionDenied(ctx, row, "remote", "silence")
			return
		}
		if err := r.svc.engine.Silence(ctx, row.Binding.ZoneID, row.Name, "remote"); err != nil {
			r.journalActionFault(ctx, row, "remote", "silence", err)
		}
	case action == remoteActionPanic:
		r.dispatchPanic(ctx, row)
	default:
		r.journalActionFault(ctx, row, "remote", action, errUnknownAction)
	}
}

// dispatchArm arms the code's bound zone in the requested mode. An empty
// mode defaults to full protection (notes/concepts/alarm-concept.md §11).
func (r *intentRouter) dispatchArm(ctx context.Context, row *CodeRow, mode, source string) {
	zoneID := row.Binding.ZoneID
	if zoneID == "" {
		r.journalActionFault(ctx, row, source, "arm", errBindingIncomplete)
		return
	}
	m := hmenum.AlarmMode(mode)
	if m == "" {
		m = hmenum.AlarmModeFull
	}
	if _, err := r.svc.engine.Arm(ctx, zoneID, engine.ArmRequest{Mode: m, By: row.Name, Source: source}); err != nil {
		r.journalActionFault(ctx, row, source, "arm", err)
	}
}

// dispatchDisarm disarms the code's bound zone.
func (r *intentRouter) dispatchDisarm(ctx context.Context, row *CodeRow, source string) {
	zoneID := row.Binding.ZoneID
	if zoneID == "" {
		r.journalActionFault(ctx, row, source, "disarm", errBindingIncomplete)
		return
	}
	if err := r.svc.engine.Disarm(ctx, zoneID, row.Name, source); err != nil {
		r.journalActionFault(ctx, row, source, "disarm", err)
	}
}

// dispatchPanic routes a remote panic key to the engine's always-on
// panic path (notes/concepts/alarm-concept.md §6.1/§7).
//
// The call is direct rather than routed through an optional port: the
// engine is a concrete dependency of this router, so an interface
// assertion buys no decoupling and hides a signature drift as a runtime
// no-op — which is what silently disabled every bound panic key.
func (r *intentRouter) dispatchPanic(ctx context.Context, row *CodeRow) {
	zoneID := row.Binding.ZoneID
	if zoneID == "" {
		r.journalActionFault(ctx, row, "remote", "panic", errBindingIncomplete)
		return
	}
	if err := r.svc.engine.PanicTrigger(ctx, zoneID, false, row.Name, "remote"); err != nil {
		r.journalActionFault(ctx, row, "remote", "panic", err)
	}
}

// lookupKeypadRow finds the enabled, in-validity keypad_slot row bound
// to (central, device, slot).
func (r *intentRouter) lookupKeypadRow(ctx context.Context, centralName, dev string, slot int, now time.Time) (*CodeRow, bool) {
	rows, err := r.svc.codeRows(ctx)
	if err != nil {
		return nil, false
	}
	for i := range rows {
		row := &rows[i]
		if row.Kind != CodeKindKeypadSlot {
			continue
		}
		b := row.Binding
		if centralMatches(b.Central, centralName) && b.DeviceAddress == dev && b.Slot == slot && codeUsable(row, now) {
			return row, true
		}
	}
	return nil, false
}

// journalKeypadUnmatched records an uncorrelated or unbound keypad press
// as a fault (notes/concepts/alarm-concept.md §11 wrong-code handling).
func (r *intentRouter) journalKeypadUnmatched(ctx context.Context, centralName, dev string, lock bool, codeID, pairIdx int) {
	r.append(ctx, engine.JournalEntry{
		Class:  hmenum.AlarmJournalClassFault,
		Event:  "keypad_press_unmatched",
		Source: "keypad",
		Details: map[string]any{
			"central":    centralName,
			"device":     dev,
			"lock":       lock,
			"code_id":    codeID,
			"pair_index": pairIdx,
		},
	})
}

// journalPermissionDenied records a resolved identity refused for
// lacking the verb permission (fail-visible, S7).
func (r *intentRouter) journalPermissionDenied(ctx context.Context, row *CodeRow, source, verb string) {
	r.append(ctx, engine.JournalEntry{
		ZoneID: row.Binding.ZoneID,
		Class:  hmenum.AlarmJournalClassFault,
		Event:  "code_permission_denied",
		Actor:  row.Name,
		Source: source,
		Details: map[string]any{
			"code_id": row.ID,
			"verb":    verb,
		},
	})
}

// journalActionFault records a verb that resolved but failed to execute.
func (r *intentRouter) journalActionFault(ctx context.Context, row *CodeRow, source, verb string, cause error) {
	details := map[string]any{"code_id": row.ID, "verb": verb}
	if cause != nil {
		details["error"] = cause.Error()
	}
	r.append(ctx, engine.JournalEntry{
		ZoneID:  row.Binding.ZoneID,
		Class:   hmenum.AlarmJournalClassFault,
		Event:   "code_action_failed",
		Actor:   row.Name,
		Source:  source,
		Details: details,
	})
}

// append persists a journal entry, logging (never blocking on) a failure.
func (r *intentRouter) append(ctx context.Context, entry engine.JournalEntry) {
	if _, err := r.svc.journal.Append(ctx, entry); err != nil {
		r.svc.log.Error("alarm intent journal append failed", "event", entry.Event, "error", err)
	}
}

// wkpPairIndex maps a WKP ACCESS_TRANSCEIVER channel (1..16) to its
// 1-based user-slot pair index. Channels alternate lock (odd) / unlock
// (even), so pair n is channels (2n-1, 2n).
func wkpPairIndex(channelAddress string) (int, bool) {
	i := strings.LastIndexByte(channelAddress, ':')
	if i < 0 {
		return 0, false
	}
	ch, err := strconv.Atoi(channelAddress[i+1:])
	if err != nil || ch < 1 || ch > 16 {
		return 0, false
	}
	return (ch + 1) / 2, true
}

// codeStateKnown reports whether a CODE_STATE value is
// KNOWN_CODE_ID_RECEIVED, accepting either the enum index or its label.
func codeStateKnown(v hmtypes.ParamValue) bool {
	switch v.Kind {
	case hmtypes.ValueKindInt:
		return v.Int == wkpCodeStateKnownIndex
	case hmtypes.ValueKindString:
		return v.String == wkpCodeStateKnownLabel
	default:
		return false
	}
}

// codeUsable reports whether a code row is enabled and inside its
// validity window (0 bounds are open-ended).
func codeUsable(row *CodeRow, now time.Time) bool {
	if !row.Enabled {
		return false
	}
	ms := now.UnixMilli()
	if row.ValidFromMS != 0 && ms < row.ValidFromMS {
		return false
	}
	if row.ValidUntilMS != 0 && ms > row.ValidUntilMS {
		return false
	}
	return true
}

// centralMatches allows an unscoped binding (empty central) to match any
// central, otherwise requires an exact central-name match.
func centralMatches(bindingCentral, centralName string) bool {
	return bindingCentral == "" || bindingCentral == centralName
}

// paramValueInt extracts an integer wire value (INTEGER params; enum
// indices arrive as ints too).
func paramValueInt(v hmtypes.ParamValue) (int, bool) {
	switch v.Kind {
	case hmtypes.ValueKindInt:
		return v.Int, true
	case hmtypes.ValueKindFloat:
		return int(v.Float), true
	default:
		return 0, false
	}
}
