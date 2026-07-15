// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	alarmpkg "github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/auth"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// alarmSourceWS is the surface token attributed to every alarm-engine
// verb invoked over the WebSocket transport (mirrors the "ws" source
// the REST layer records).
const alarmSourceWS = "ws"

// AlarmPanelQuery is the narrow facade the alarm_panel.* command family
// consumes. *alarm.Service satisfies it directly via its Engine() and
// Stores() accessors; tests supply a stub.
type AlarmPanelQuery interface {
	// Engine exposes the arm-state machine for the mutating verbs and
	// the live state / readiness / walk-test reads.
	Engine() *engine.Engine
	// Stores exposes the alarm store bundle so the journal read can
	// query the persistent event log.
	Stores() *alarmpkg.Stores
}

// AlarmPanelCommandsConfig bundles the alarm-panel facade consumed by
// [RegisterAlarmPanelCommands]. A nil Panel skips the whole family —
// the daemon leaves it nil when the alarm service is disabled.
type AlarmPanelCommandsConfig struct {
	Panel AlarmPanelQuery
}

// RegisterAlarmPanelCommands wires the alarm_panel.* command family onto
// router. It is a no-op when the router or the alarm facade is nil, so a
// daemon without the alarm service simply never advertises the commands
// (clients receive "unknown_command" rather than a panic).
func RegisterAlarmPanelCommands(router *Router, cfg AlarmPanelCommandsConfig) {
	if router == nil || cfg.Panel == nil {
		return
	}
	router.Register("alarm_panel.arm", alarmArmHandler(cfg.Panel))
	router.Register("alarm_panel.disarm", alarmDisarmHandler(cfg.Panel))
	router.Register("alarm_panel.silence", alarmSilenceHandler(cfg.Panel))
	router.Register("alarm_panel.silence_all", alarmSilenceAllHandler(cfg.Panel))
	router.Register("alarm_panel.acknowledge", alarmAcknowledgeHandler(cfg.Panel))
	router.Register("alarm_panel.state", alarmStateHandler(cfg.Panel))
	router.Register("alarm_panel.readiness", alarmReadinessHandler(cfg.Panel))
	router.Register("alarm_panel.journal", alarmJournalHandler(cfg.Panel))
	router.Register("alarm_panel.walktest_status", alarmWalkTestStatusHandler(cfg.Panel))
}

// alarmActor resolves the acting identity for the engine's `by`
// attribution. Mirrors the REST layer's identityFromCtx: the
// authenticated subject, or "anonymous" when unattributed. The write
// commands are already role-gated in Dispatch, so a reaching call
// always carries an operator identity.
func alarmActor(ctx context.Context) string {
	id, ok := auth.IdentityFrom(ctx)
	if !ok || id.Subject == "" {
		return "anonymous"
	}
	return id.Subject
}

// --- argument shapes ---

// alarmAreaArgs is the shared shape for the per-area verbs and reads.
type alarmAreaArgs struct {
	AreaID string `json:"area_id"`
}

// alarmArmArgs is the shape for alarm_panel.arm.
type alarmArmArgs struct {
	AreaID    string   `json:"area_id"`
	Mode      string   `json:"mode"`
	Force     bool     `json:"force,omitempty"`
	SkipDelay bool     `json:"skip_delay,omitempty"`
	Bypass    []string `json:"bypass,omitempty"`
}

// alarmJournalArgs is the shape for alarm_panel.journal. From/To are
// RFC3339 timestamps; empty leaves the bound open.
type alarmJournalArgs struct {
	AreaID string `json:"area_id,omitempty"`
	Class  string `json:"class,omitempty"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// --- write commands ---

func alarmArmHandler(q AlarmPanelQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args alarmArmArgs
		if err := decodeOrEmpty(raw, &args); err != nil {
			return nil, err
		}
		if args.AreaID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "area_id required")
		}
		if args.Mode == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "mode required")
		}
		eng := q.Engine()
		if eng == nil {
			return nil, NewCommandError(CommandErrorInternal, "alarm engine not available")
		}
		res, err := eng.Arm(ctx, args.AreaID, engine.ArmRequest{
			Mode:      hmenum.AlarmMode(args.Mode),
			Force:     args.Force,
			SkipDelay: args.SkipDelay,
			Bypass:    args.Bypass,
			By:        alarmActor(ctx),
			Source:    alarmSourceWS,
		})
		if err != nil {
			return nil, alarmEngineError(err)
		}
		return hmapi.AlarmArmAccepted{
			State:      string(res.State),
			Bypassed:   res.Bypassed,
			ExitDelayS: int(res.ExitDelay / time.Second),
		}, nil
	}
}

func alarmDisarmHandler(q AlarmPanelQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		areaID, eng, err := alarmAreaTarget(q, raw)
		if err != nil {
			return nil, err
		}
		if err := eng.Disarm(ctx, areaID, alarmActor(ctx), alarmSourceWS); err != nil {
			return nil, alarmEngineError(err)
		}
		return map[string]any{"disarmed": true, "area_id": areaID}, nil
	}
}

func alarmSilenceHandler(q AlarmPanelQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		areaID, eng, err := alarmAreaTarget(q, raw)
		if err != nil {
			return nil, err
		}
		if err := eng.Silence(ctx, areaID, alarmActor(ctx), alarmSourceWS); err != nil {
			return nil, alarmEngineError(err)
		}
		return map[string]any{"silenced": true, "area_id": areaID}, nil
	}
}

func alarmSilenceAllHandler(q AlarmPanelQuery) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		eng := q.Engine()
		if eng == nil {
			return nil, NewCommandError(CommandErrorInternal, "alarm engine not available")
		}
		eng.SilenceAll(ctx, alarmActor(ctx), alarmSourceWS)
		return map[string]any{"silenced": true}, nil
	}
}

func alarmAcknowledgeHandler(q AlarmPanelQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		areaID, eng, err := alarmAreaTarget(q, raw)
		if err != nil {
			return nil, err
		}
		if err := eng.Acknowledge(ctx, areaID, alarmActor(ctx), alarmSourceWS); err != nil {
			return nil, alarmEngineError(err)
		}
		return map[string]any{"acknowledged": true, "area_id": areaID}, nil
	}
}

// --- read commands ---

func alarmStateHandler(q AlarmPanelQuery) CommandHandler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		eng := q.Engine()
		if eng == nil {
			return nil, NewCommandError(CommandErrorInternal, "alarm engine not available")
		}
		snaps := eng.Areas()
		areas := make([]hmapi.AlarmAreaStatus, 0, len(snaps))
		for i := range snaps {
			areas = append(areas, alarmAreaStatus(eng, snaps[i]))
		}
		return map[string]any{"areas": areas}, nil
	}
}

func alarmReadinessHandler(q AlarmPanelQuery) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var args alarmAreaArgs
		if err := decodeOrEmpty(raw, &args); err != nil {
			return nil, err
		}
		if args.AreaID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "area_id required")
		}
		eng := q.Engine()
		if eng == nil {
			return nil, NewCommandError(CommandErrorInternal, "alarm engine not available")
		}
		snap, ok := eng.Area(args.AreaID)
		if !ok {
			return nil, NewCommandError("not_found", "no alarm area "+args.AreaID)
		}
		return map[string]any{
			"area_id":   args.AreaID,
			"readiness": alarmReadinessDTO(snap.Readiness),
		}, nil
	}
}

func alarmJournalHandler(q AlarmPanelQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args alarmJournalArgs
		if err := decodeOrEmpty(raw, &args); err != nil {
			return nil, err
		}
		stores := q.Stores()
		if stores == nil || stores.Journal == nil {
			return nil, NewCommandError(CommandErrorInternal, "alarm journal not available")
		}
		// Limits and semantics mirror the REST journal endpoint: an
		// omitted limit defaults to 500, the cap is 5000, `to` is
		// exclusive, and an unknown class is a client error instead of
		// a silently empty result.
		limit := args.Limit
		if limit <= 0 {
			limit = 500
		}
		if limit > 5000 {
			limit = 5000
		}
		if args.Class != "" && !hmenum.AlarmJournalClass(args.Class).Valid() {
			return nil, NewCommandError(CommandErrorBadRequest, "class: unknown journal class")
		}
		filter := sqlitestore.AlarmJournalFilter{
			AreaID: args.AreaID,
			Class:  hmenum.AlarmJournalClass(args.Class),
			Limit:  limit,
		}
		if args.From != "" {
			t, err := time.Parse(time.RFC3339, args.From)
			if err != nil {
				return nil, NewCommandError(CommandErrorBadRequest, "from: "+err.Error())
			}
			filter.FromMS = t.UnixMilli()
		}
		if args.To != "" {
			t, err := time.Parse(time.RFC3339, args.To)
			if err != nil {
				return nil, NewCommandError(CommandErrorBadRequest, "to: "+err.Error())
			}
			filter.ToMS = t.UnixMilli() - 1
		}
		rows, err := stores.Journal.Query(ctx, filter)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "journal query: "+err.Error())
		}
		entries := make([]hmapi.AlarmJournalEntry, 0, len(rows))
		for i := range rows {
			entries = append(entries, alarmJournalDTO(rows[i]))
		}
		return map[string]any{"entries": entries}, nil
	}
}

func alarmWalkTestStatusHandler(q AlarmPanelQuery) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var args alarmAreaArgs
		if err := decodeOrEmpty(raw, &args); err != nil {
			return nil, err
		}
		if args.AreaID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "area_id required")
		}
		eng := q.Engine()
		if eng == nil {
			return nil, NewCommandError(CommandErrorInternal, "alarm engine not available")
		}
		wt, err := eng.WalkTestStatus(args.AreaID)
		if err != nil {
			return nil, alarmEngineError(err)
		}
		return alarmWalkTestDTO(wt), nil
	}
}

// --- shared helpers ---

// alarmAreaTarget decodes the shared {area_id} body and resolves the
// engine, returning the bad_request / internal command errors the
// per-area verbs share.
func alarmAreaTarget(q AlarmPanelQuery, raw json.RawMessage) (string, *engine.Engine, error) {
	var args alarmAreaArgs
	if err := decodeOrEmpty(raw, &args); err != nil {
		return "", nil, err
	}
	if args.AreaID == "" {
		return "", nil, NewCommandError(CommandErrorBadRequest, "area_id required")
	}
	eng := q.Engine()
	if eng == nil {
		return "", nil, NewCommandError(CommandErrorInternal, "alarm engine not available")
	}
	return args.AreaID, eng, nil
}

// alarmAreaStatus renders one engine snapshot as the REST-shaped status
// DTO. The exit/entry countdown carries only remaining_s here — the
// snapshot does not retain the countdown total; the live alarm.countdown
// broadcast carries total_ms for surfaces that need the full bar.
func alarmAreaStatus(eng *engine.Engine, snap engine.AreaSnapshot) hmapi.AlarmAreaStatus {
	st := hmapi.AlarmAreaStatus{
		ID:        snap.ID,
		Name:      snap.Name,
		State:     string(snap.State),
		Bypassed:  snap.Bypassed,
		Readiness: alarmReadinessDTO(snap.Readiness),
	}
	if snap.State != hmenum.AlarmAreaStateDisarmed {
		st.Mode = string(snap.Mode)
	}
	if snap.IncidentID != 0 {
		st.Incident = &hmapi.AlarmIncidentRef{
			ID:       strconv.FormatInt(snap.IncidentID, 10),
			Silenced: snap.IncidentSilenced,
		}
	}
	if snap.TimerKind != "" && snap.TimerRemaining > 0 {
		st.Countdown = &hmapi.AlarmCountdown{
			Kind:       snap.TimerKind,
			RemainingS: int(snap.TimerRemaining / time.Second),
		}
	}
	if wt, err := eng.WalkTestStatus(snap.ID); err == nil {
		st.WalkTestActive = wt.Active
	}
	return st
}

// alarmReadinessDTO converts the engine's per-mode readiness map into
// the REST-shaped, mode-keyed DTO map. Returns nil for an empty input so
// the omitempty status field drops cleanly.
func alarmReadinessDTO(r map[hmenum.AlarmMode]hmevent.AlarmModeReadiness) map[string]hmapi.AlarmModeReadiness {
	if len(r) == 0 {
		return nil
	}
	out := make(map[string]hmapi.AlarmModeReadiness, len(r))
	for m, v := range r {
		out[string(m)] = hmapi.AlarmModeReadiness{
			Ready:    v.Ready,
			Blockers: v.Blockers,
			Warnings: v.Warnings,
		}
	}
	return out
}

// alarmJournalDTO converts one persisted journal row into the REST DTO.
func alarmJournalDTO(r sqlitestore.AlarmJournalEntry) hmapi.AlarmJournalEntry {
	e := hmapi.AlarmJournalEntry{
		ID:         r.ID,
		When:       time.UnixMilli(r.TsMS).UTC(),
		AreaID:     r.AreaID,
		Class:      string(r.Class),
		Event:      r.Event,
		Actor:      r.Actor,
		Source:     r.Source,
		IncidentID: r.IncidentID,
	}
	if r.DetailsJSON != "" && r.DetailsJSON != "{}" {
		e.Details = json.RawMessage(r.DetailsJSON)
	}
	return e
}

// alarmWalkTestDTO converts an engine walk-test status into the REST DTO.
func alarmWalkTestDTO(wt engine.WalkTestStatus) hmapi.AlarmWalkTestStatus {
	out := hmapi.AlarmWalkTestStatus{
		Active:  wt.Active,
		Sensors: make([]hmapi.AlarmWalkTestSensor, 0, len(wt.Sensors)),
	}
	if !wt.StartedAt.IsZero() {
		started := wt.StartedAt.UTC()
		out.StartedAt = &started
	}
	for _, s := range wt.Sensors {
		row := hmapi.AlarmWalkTestSensor{
			ID:     s.SensorID,
			Name:   s.Name,
			Tested: !s.SeenAt.IsZero(),
		}
		if !s.SeenAt.IsZero() {
			seen := s.SeenAt.UTC()
			row.LastTriggeredAt = &seen
		}
		out.Sensors = append(out.Sensors, row)
	}
	return out
}

// alarmEngineError maps the engine's sentinel + typed errors onto WS
// command-error codes, mirroring the REST status mapping: unknown area →
// not_found, unknown mode → bad_request, wrong state / no incident →
// conflict, and a refused arm → not_ready with the blocking sensor ids.
func alarmEngineError(err error) *CommandError {
	var nr *engine.NotReadyError
	if errors.As(err, &nr) {
		msg := "not ready to arm"
		if len(nr.Blockers) > 0 {
			msg += ": blocked by " + strings.Join(nr.Blockers, ", ")
		}
		return NewCommandError("not_ready", msg)
	}
	switch {
	case errors.Is(err, engine.ErrUnknownArea):
		return NewCommandError("not_found", err.Error())
	case errors.Is(err, engine.ErrUnknownMode):
		return NewCommandError(CommandErrorBadRequest, err.Error())
	case errors.Is(err, engine.ErrInvalidState), errors.Is(err, engine.ErrNoIncident):
		return NewCommandError("conflict", err.Error())
	default:
		return NewCommandError(CommandErrorInternal, err.Error())
	}
}
