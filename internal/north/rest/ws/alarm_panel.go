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
	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// alarmSourceWS is the surface token attributed to every alarm-engine
// verb invoked over the WebSocket transport. The `-operator` suffix marks
// it as a strongly-authenticated operator session — the alarm_panel write
// commands are operator-gated in Dispatch — which the engine's code
// policy recognises as a break-glass surface that bypasses a required
// arm/disarm/silence code while still surfacing duress
// (docs/alarm-concept.md §11, S6).
const alarmSourceWS = "ws-operator"

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
	// Panels exposes the alarm-control-panel entity projections.
	Panels() []alarmpanel.Panel
}

// AlarmCodeAdmin is the alarm-code CRUD facade the codes_* commands
// drive (docs/alarm-concept.md §11). It mirrors the REST handler facade
// and is satisfied structurally by the codes facade; a nil value serves
// the codes_* commands as an "unavailable" command error. The hash and
// cleartext PIN are never returned on the [hmapi.AlarmCode] projection.
type AlarmCodeAdmin interface {
	ListCodes(ctx context.Context) ([]hmapi.AlarmCode, error)
	GetCode(ctx context.Context, id string) (code hmapi.AlarmCode, ok bool, err error)
	CreateCode(ctx context.Context, req hmapi.AlarmCodeRequest) (hmapi.AlarmCode, error)
	UpdateCode(ctx context.Context, id string, req hmapi.AlarmCodeRequest) (code hmapi.AlarmCode, ok bool, err error)
	DeleteCode(ctx context.Context, id string) (ok bool, err error)
}

// AlarmPanelCommandsConfig bundles the alarm-panel facade consumed by
// [RegisterAlarmPanelCommands]. A nil Panel skips the whole family —
// the daemon leaves it nil when the alarm service is disabled. A nil
// Codes leaves the codes_* commands registered but serving "unavailable"
// until the codes facade is wired.
type AlarmPanelCommandsConfig struct {
	Panel AlarmPanelQuery
	Codes AlarmCodeAdmin
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
	router.Register("alarm_panel.panels", alarmPanelPanelsHandler(cfg.Panel))
	router.Register("alarm_panel.readiness", alarmReadinessHandler(cfg.Panel))
	router.Register("alarm_panel.journal", alarmJournalHandler(cfg.Panel))
	router.Register("alarm_panel.walktest_status", alarmWalkTestStatusHandler(cfg.Panel))
	router.Register("alarm_panel.codes_list", alarmCodesListHandler(cfg.Codes))
	router.Register("alarm_panel.codes_create", alarmCodesCreateHandler(cfg.Codes))
	router.Register("alarm_panel.codes_update", alarmCodesUpdateHandler(cfg.Codes))
	router.Register("alarm_panel.codes_delete", alarmCodesDeleteHandler(cfg.Codes))
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

// alarmZoneArgs is the shared shape for the per-zone verbs and reads. The
// optional code carries an alarm code for the code-gated verbs
// (docs/alarm-concept.md §11); it is ignored by the reads.
type alarmZoneArgs struct {
	ZoneID string `json:"zone_id"`
	Code   string `json:"code,omitempty"`
}

// alarmArmArgs is the shape for alarm_panel.arm.
type alarmArmArgs struct {
	ZoneID    string   `json:"zone_id"`
	Mode      string   `json:"mode"`
	Force     bool     `json:"force,omitempty"`
	SkipDelay bool     `json:"skip_delay,omitempty"`
	Bypass    []string `json:"bypass,omitempty"`
	Code      string   `json:"code,omitempty"`
}

// alarmJournalArgs is the shape for alarm_panel.journal. From/To are
// RFC3339 timestamps; empty leaves the bound open.
type alarmJournalArgs struct {
	ZoneID string `json:"zone_id,omitempty"`
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
		if args.ZoneID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "zone_id required")
		}
		if args.Mode == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "mode required")
		}
		eng := q.Engine()
		if eng == nil {
			return nil, NewCommandError(CommandErrorInternal, "alarm engine not available")
		}
		res, err := eng.Arm(ctx, args.ZoneID, engine.ArmRequest{
			Mode:      hmenum.AlarmMode(args.Mode),
			Force:     args.Force,
			SkipDelay: args.SkipDelay,
			Bypass:    args.Bypass,
			Code:      args.Code,
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
		args, eng, err := alarmZoneTarget(q, raw)
		if err != nil {
			return nil, err
		}
		if err := eng.DisarmWithCode(ctx, args.ZoneID, alarmActor(ctx), alarmSourceWS, args.Code); err != nil {
			return nil, alarmEngineError(err)
		}
		return map[string]any{"disarmed": true, "zone_id": args.ZoneID}, nil
	}
}

func alarmSilenceHandler(q AlarmPanelQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		args, eng, err := alarmZoneTarget(q, raw)
		if err != nil {
			return nil, err
		}
		if err := eng.SilenceWithCode(ctx, args.ZoneID, alarmActor(ctx), alarmSourceWS, args.Code); err != nil {
			return nil, alarmEngineError(err)
		}
		return map[string]any{"silenced": true, "zone_id": args.ZoneID}, nil
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
		// Acknowledge accepts the shared {zone_id, code} shape for symmetry
		// but is journal-only with no code gate, so a supplied code is
		// inert here.
		args, eng, err := alarmZoneTarget(q, raw)
		if err != nil {
			return nil, err
		}
		if err := eng.Acknowledge(ctx, args.ZoneID, alarmActor(ctx), alarmSourceWS); err != nil {
			return nil, alarmEngineError(err)
		}
		return map[string]any{"acknowledged": true, "zone_id": args.ZoneID}, nil
	}
}

// --- read commands ---

func alarmStateHandler(q AlarmPanelQuery) CommandHandler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		eng := q.Engine()
		if eng == nil {
			return nil, NewCommandError(CommandErrorInternal, "alarm engine not available")
		}
		snaps := eng.Zones()
		zones := make([]hmapi.AlarmZoneStatus, 0, len(snaps))
		for i := range snaps {
			zones = append(zones, alarmZoneStatus(eng, snaps[i]))
		}
		return map[string]any{"zones": zones}, nil
	}
}

func alarmReadinessHandler(q AlarmPanelQuery) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var args alarmZoneArgs
		if err := decodeOrEmpty(raw, &args); err != nil {
			return nil, err
		}
		if args.ZoneID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "zone_id required")
		}
		eng := q.Engine()
		if eng == nil {
			return nil, NewCommandError(CommandErrorInternal, "alarm engine not available")
		}
		snap, ok := eng.Zone(args.ZoneID)
		if !ok {
			return nil, NewCommandError("not_found", "no alarm zone "+args.ZoneID)
		}
		return map[string]any{
			"zone_id":   args.ZoneID,
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
			ZoneID: args.ZoneID,
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
		var args alarmZoneArgs
		if err := decodeOrEmpty(raw, &args); err != nil {
			return nil, err
		}
		if args.ZoneID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "zone_id required")
		}
		eng := q.Engine()
		if eng == nil {
			return nil, NewCommandError(CommandErrorInternal, "alarm engine not available")
		}
		wt, err := eng.WalkTestStatus(args.ZoneID)
		if err != nil {
			return nil, alarmEngineError(err)
		}
		return alarmWalkTestDTO(wt), nil
	}
}

// --- shared helpers ---

// alarmZoneTarget decodes the shared {zone_id, code} body and resolves
// the engine, returning the bad_request / internal command errors the
// per-zone verbs share.
func alarmZoneTarget(q AlarmPanelQuery, raw json.RawMessage) (alarmZoneArgs, *engine.Engine, error) {
	var args alarmZoneArgs
	if err := decodeOrEmpty(raw, &args); err != nil {
		return alarmZoneArgs{}, nil, err
	}
	if args.ZoneID == "" {
		return alarmZoneArgs{}, nil, NewCommandError(CommandErrorBadRequest, "zone_id required")
	}
	eng := q.Engine()
	if eng == nil {
		return alarmZoneArgs{}, nil, NewCommandError(CommandErrorInternal, "alarm engine not available")
	}
	return args, eng, nil
}

// alarmZoneStatus renders one engine snapshot as the REST-shaped status
// DTO. The exit/entry countdown carries only remaining_s here — the
// snapshot does not retain the countdown total; the live alarm.countdown
// broadcast carries total_ms for surfaces that need the full bar.
func alarmZoneStatus(eng *engine.Engine, snap engine.ZoneSnapshot) hmapi.AlarmZoneStatus {
	st := hmapi.AlarmZoneStatus{
		ID:        snap.ID,
		Name:      snap.Name,
		State:     string(snap.State),
		Bypassed:  snap.Bypassed,
		Readiness: alarmReadinessDTO(snap.Readiness),
	}
	if snap.State != hmenum.AlarmZoneStateDisarmed {
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
		ZoneID:     r.ZoneID,
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
// command-error codes, mirroring the REST status mapping: unknown zone →
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
	case errors.Is(err, engine.ErrUnknownZone):
		return NewCommandError("not_found", err.Error())
	case errors.Is(err, engine.ErrInvalidCode):
		// A missing/wrong code is a forbidden action; the message stays
		// opaque so a prober learns nothing about which codes exist.
		return NewCommandError(CommandErrorForbidden, "invalid_code")
	case errors.Is(err, engine.ErrUnknownMode):
		return NewCommandError(CommandErrorBadRequest, err.Error())
	case errors.Is(err, engine.ErrInvalidState), errors.Is(err, engine.ErrNoIncident):
		return NewCommandError("conflict", err.Error())
	default:
		return NewCommandError(CommandErrorInternal, err.Error())
	}
}

// --- code CRUD commands ---
//
// These are operator-gated writes (see writeCommandRoles): codes are
// security material, so even the list is not viewer-open. A nil admin
// (codes facade not yet wired) answers "unavailable" rather than
// panicking.

// alarmCodeUpsertArgs is the shared create/update body. For update the
// id names the target; for create it is ignored (server-generated).
type alarmCodeUpsertArgs struct {
	ID   string                 `json:"id,omitempty"`
	Code hmapi.AlarmCodeRequest `json:"code"`
}

func alarmCodesUnavailable() *CommandError {
	return NewCommandError("unavailable", "alarm code subsystem not available")
}

func alarmCodesListHandler(admin AlarmCodeAdmin) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		if admin == nil {
			return nil, alarmCodesUnavailable()
		}
		codes, err := admin.ListCodes(ctx)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, err.Error())
		}
		if codes == nil {
			codes = []hmapi.AlarmCode{}
		}
		return map[string]any{"codes": codes}, nil
	}
}

func alarmCodesCreateHandler(admin AlarmCodeAdmin) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		if admin == nil {
			return nil, alarmCodesUnavailable()
		}
		var args alarmCodeUpsertArgs
		if err := decodeOrEmpty(raw, &args); err != nil {
			return nil, err
		}
		if err := validateAlarmCodeReq(args.Code); err != nil {
			return nil, err
		}
		created, err := admin.CreateCode(ctx, args.Code)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, err.Error())
		}
		return created, nil
	}
}

func alarmCodesUpdateHandler(admin AlarmCodeAdmin) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		if admin == nil {
			return nil, alarmCodesUnavailable()
		}
		var args alarmCodeUpsertArgs
		if err := decodeOrEmpty(raw, &args); err != nil {
			return nil, err
		}
		if args.ID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "id required")
		}
		if err := validateAlarmCodeReq(args.Code); err != nil {
			return nil, err
		}
		updated, ok, err := admin.UpdateCode(ctx, args.ID, args.Code)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, err.Error())
		}
		if !ok {
			return nil, NewCommandError("not_found", "no alarm code "+args.ID)
		}
		return updated, nil
	}
}

func alarmCodesDeleteHandler(admin AlarmCodeAdmin) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		if admin == nil {
			return nil, alarmCodesUnavailable()
		}
		var args struct {
			ID string `json:"id"`
		}
		if err := decodeOrEmpty(raw, &args); err != nil {
			return nil, err
		}
		if args.ID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "id required")
		}
		ok, err := admin.DeleteCode(ctx, args.ID)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, err.Error())
		}
		if !ok {
			return nil, NewCommandError("not_found", "no alarm code "+args.ID)
		}
		return map[string]any{"deleted": true, "id": args.ID}, nil
	}
}

// validateAlarmCodeReq mirrors the REST create/update validation: a name
// and a known kind are required before the write reaches the facade.
func validateAlarmCodeReq(req hmapi.AlarmCodeRequest) error {
	if req.Name == "" {
		return NewCommandError(CommandErrorBadRequest, "name is required")
	}
	switch req.Kind {
	case "pin", "keypad_slot", "remote_key":
		return nil
	default:
		return NewCommandError(CommandErrorBadRequest, "kind must be one of pin, keypad_slot, remote_key")
	}
}

// alarmPanelPanelsHandler serves the entity projection (same view as
// GET /api/v1/alarm/panels and the MQTT discovery entities).
func alarmPanelPanelsHandler(svc AlarmPanelQuery) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		panels := svc.Panels()
		out := make([]hmapi.AlarmPanelEntity, 0, len(panels))
		for i := range panels {
			p := &panels[i]
			modes := make([]string, 0, len(p.Modes))
			for _, m := range p.Modes {
				modes = append(modes, string(m))
			}
			out = append(out, hmapi.AlarmPanelEntity{
				UniqueID:           p.UniqueID,
				ZoneID:             p.ZoneID,
				Name:               p.Name,
				Category:           string(p.Category()),
				State:              p.State,
				SupportedModes:     modes,
				Available:          p.Available,
				Master:             p.Master,
				CodeArmRequired:    p.CodeArmRequired,
				CodeDisarmRequired: p.CodeDisarmRequired,
			})
		}
		return map[string]any{"panels": out}, nil
	}
}
