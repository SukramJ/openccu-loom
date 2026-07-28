// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/alarm/outputs"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// AlarmPanel is the narrow facade the /alarm handlers drive: the arm
// engine, the output-driver layer, the alarm store bundle, and the
// post-write reload. *alarm.Service satisfies it — the router wires the
// concrete service on startup, or leaves the field nil to unmount the
// alarm surface entirely.
type AlarmPanel interface {
	Engine() *engine.Engine
	Manager() *outputs.Manager
	Stores() *alarm.Stores
	Reload(ctx context.Context) error
	Panels() []alarmpanel.Panel
	// OutputCandidates enumerates the channels that can back a
	// device-backed output class (empty class returns all).
	OutputCandidates(class hmenum.AlarmOutputClass) []alarm.OutputCandidate
	// OutputTargetEligible soft-validates an enrollment target:
	// known=false (central/channel unresolvable) must be treated as
	// eligible so a CCU outage never blocks a config save.
	OutputTargetEligible(centralName, channelAddress string, class hmenum.AlarmOutputClass) (eligible, known bool)
	// RemoteKeyCandidates enumerates the physical remote/wall-button
	// key channels a remote-key code binding can dispatch on.
	RemoteKeyCandidates() []alarm.RemoteKeyCandidate
}

// Compile-time proof the daemon-level alarm service satisfies the
// handler facade.
var _ AlarmPanel = (*alarm.Service)(nil)

// alarmSourceREST tags every alarm-journal entry and audit record this
// surface produces with the originating surface. The `-operator` suffix
// marks it as a strongly-authenticated operator session, which the
// engine's code policy recognises as a break-glass surface that bypasses
// a required arm/disarm/silence code while still surfacing duress
// (docs/alarm-concept.md §11, S6). Every /alarm write route is
// operator-gated, so a reaching call is always an operator session.
const alarmSourceREST = "rest-operator"

const (
	// Journal query limits mirror the ?limit bounds documented for
	// GET /alarm/journal in assets/openapi.yaml.
	alarmJournalDefaultLimit = 500
	alarmJournalMaxLimit     = 5000
)

// Countdown kinds echoed on GET /alarm/state. The values are wire-stable
// and match the engine's exit/entry timer kinds; the trigger-time timer
// is never surfaced as a countdown.
const (
	alarmCountdownExit  = "exit_delay"
	alarmCountdownEntry = "entry_delay"
)

// alarmStateResponse is the envelope of GET /alarm/state.
type alarmStateResponse struct {
	Zones []hmapi.AlarmZoneStatus `json:"zones"`
}

// AlarmState renders the live status of every alarm zone.
func AlarmState(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snaps := p.Engine().Zones()
		zones := make([]hmapi.AlarmZoneStatus, 0, len(snaps))
		for i := range snaps {
			zones = append(zones, alarmZoneStatus(r.Context(), p, snaps[i]))
		}
		JSON(w, http.StatusOK, alarmStateResponse{Zones: zones})
	}
}

// GetAlarmZoneReadiness renders the per-mode arm-readiness of one zone,
// keyed by mode name.
func GetAlarmZoneReadiness(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		snap, ok := p.Engine().Zone(id)
		if !ok {
			writeAlarmNotFound(w, r)
			return
		}
		out := make(map[string]hmapi.AlarmModeReadiness, len(snap.Readiness))
		for mode, rd := range snap.Readiness {
			out[string(mode)] = apiReadiness(rd)
		}
		JSON(w, http.StatusOK, out)
	}
}

// ArmAlarmZone arms an zone into the requested mode, mapping a
// not-ready refusal to a 409 whose problem detail carries the blocking
// sensor ids.
func ArmAlarmZone(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req hmapi.AlarmArmRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		mode := hmenum.AlarmMode(req.Mode)
		if !mode.Armed() {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid arm mode",
					"mode must be one of perimeter, full, night, vacation, custom"))
			return
		}
		res, err := p.Engine().Arm(r.Context(), id, engine.ArmRequest{
			Mode:      mode,
			Force:     req.Force,
			SkipDelay: req.SkipDelay,
			Bypass:    req.Bypass,
			Code:      req.Code,
			By:        identityFromCtx(r.Context()),
			Source:    alarmSourceREST,
		})
		if err != nil {
			var notReady *engine.NotReadyError
			switch {
			case errors.Is(err, engine.ErrUnknownZone):
				writeAlarmNotFound(w, r)
			case errors.Is(err, engine.ErrInvalidCode):
				writeAlarmInvalidCode(w, r)
			case errors.As(err, &notReady):
				writeArmNotReady(w, r, notReady.Blockers)
			case errors.Is(err, engine.ErrUnknownMode):
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeBadRequest, r, "Mode not configured for zone", ""))
			case errors.Is(err, engine.ErrInvalidState):
				problem.Write(w, http.StatusConflict,
					problem.New(problem.TypeConflict, r, "Zone cannot be armed in its current state", ""))
			default:
				writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Arm failed", err)
			}
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmArm, "zone="+id+" mode="+req.Mode)
		JSON(w, http.StatusOK, hmapi.AlarmArmAccepted{
			State:      string(res.State),
			Bypassed:   res.Bypassed,
			ExitDelayS: durationSeconds(res.ExitDelay),
		})
	}
}

// DisarmAlarmZone returns an zone to disarmed. The optional body carries
// a disarm code (docs/alarm-concept.md §11); an absent body disarms
// code-free, which the operator-session source is permitted to do (S6).
func DisarmAlarmZone(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		code, ok := decodeAlarmVerbCode(w, r)
		if !ok {
			return
		}
		if err := p.Engine().DisarmWithCode(r.Context(), id, identityFromCtx(r.Context()), alarmSourceREST, code); err != nil {
			writeAlarmVerbError(w, r, err, "Disarm failed")
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmDisarm, "zone="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// SilenceAlarmZone silences the active incident of one zone without
// disarming it. The optional body carries a silence code for surfaces
// whose per-surface policy requires one (silence is code-free by default
// per S3).
func SilenceAlarmZone(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		code, ok := decodeAlarmVerbCode(w, r)
		if !ok {
			return
		}
		if err := p.Engine().SilenceWithCode(r.Context(), id, identityFromCtx(r.Context()), alarmSourceREST, code); err != nil {
			writeAlarmVerbError(w, r, err, "Silence failed")
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmSilence, "zone="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// AcknowledgeAlarmZone marks the zone's open incident as seen. It accepts
// the shared optional code body for surface symmetry, but acknowledge is
// journal-only with no code gate, so a supplied code is inert here.
func AcknowledgeAlarmZone(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if _, ok := decodeAlarmVerbCode(w, r); !ok {
			return
		}
		err := p.Engine().Acknowledge(r.Context(), id, identityFromCtx(r.Context()), alarmSourceREST)
		switch {
		case err == nil:
		case errors.Is(err, engine.ErrUnknownZone):
			writeAlarmNotFound(w, r)
			return
		case errors.Is(err, engine.ErrNoIncident):
			problem.Write(w, http.StatusConflict,
				problem.New(problem.TypeConflict, r, "No open incident to acknowledge", ""))
			return
		default:
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Acknowledge failed", err)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmAcknowledge, "zone="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// SilenceAllAlarmZones silences every zone's active incident at once.
func SilenceAllAlarmZones(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.Engine().SilenceAll(r.Context(), identityFromCtx(r.Context()), alarmSourceREST)
		recordAlarm(rec, r, audit.ActionAlarmSilence, "zone=all")
		w.WriteHeader(http.StatusNoContent)
	}
}

// ListAlarmJournal queries the append-only alarm event journal.
func ListAlarmJournal(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, errMsg := parseAlarmJournalFilter(r)
		if errMsg != "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid query parameter", errMsg))
			return
		}
		rows, err := p.Stores().Journal.Query(r.Context(), f)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Journal query failed", err)
			return
		}
		out := make([]hmapi.AlarmJournalEntry, 0, len(rows))
		for i := range rows {
			out = append(out, apiJournalEntry(rows[i]))
		}
		JSON(w, http.StatusOK, out)
	}
}

// StartAlarmWalkTest begins a walk-test session on a disarmed zone.
func StartAlarmWalkTest(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		err := p.Engine().WalkTestStart(r.Context(), id, identityFromCtx(r.Context()), alarmSourceREST)
		switch {
		case err == nil:
		case errors.Is(err, engine.ErrUnknownZone):
			writeAlarmNotFound(w, r)
			return
		case errors.Is(err, engine.ErrWalkTestActive):
			problem.Write(w, http.StatusConflict,
				problem.New(problem.TypeConflict, r, "Walk test already active", ""))
			return
		case errors.Is(err, engine.ErrInvalidState):
			problem.Write(w, http.StatusConflict,
				problem.New(problem.TypeConflict, r, "Zone must be disarmed to start a walk test", ""))
			return
		default:
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Walk test start failed", err)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmWalkTest, "zone="+id+" op=start")
		w.WriteHeader(http.StatusNoContent)
	}
}

// StopAlarmWalkTest ends the running walk-test session of an zone.
func StopAlarmWalkTest(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		_, err := p.Engine().WalkTestStop(r.Context(), id, identityFromCtx(r.Context()), alarmSourceREST)
		switch {
		case err == nil:
		case errors.Is(err, engine.ErrUnknownZone):
			writeAlarmNotFound(w, r)
			return
		case errors.Is(err, engine.ErrInvalidState):
			problem.Write(w, http.StatusConflict,
				problem.New(problem.TypeConflict, r, "No active walk test", ""))
			return
		default:
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Walk test stop failed", err)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmWalkTest, "zone="+id+" op=stop")
		w.WriteHeader(http.StatusNoContent)
	}
}

// GetAlarmWalkTestStatus renders the live status of an zone's walk-test
// session.
func GetAlarmWalkTestStatus(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		st, err := p.Engine().WalkTestStatus(id)
		if err != nil {
			writeAlarmVerbError(w, r, err, "Walk test status failed")
			return
		}
		JSON(w, http.StatusOK, apiWalkTestStatus(st))
	}
}

// TestAlarmOutput fires a single output briefly for a walk test. The
// `{id}` param arrives percent-decoded (the router routes on the
// decoded path), so the pipe-separated output ID matches the enrolled
// rows directly.
func TestAlarmOutput(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req hmapi.AlarmOutputTestRequest
		// The request body is optional (default: full test fire); an
		// empty body decodes as the zero request.
		if err := DecodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		err := p.Manager().TestFire(r.Context(), id, req.OpticalOnly)
		switch {
		case err == nil:
		case errors.Is(err, outputs.ErrUnknownOutput):
			writeAlarmNotFound(w, r)
			return
		case errors.Is(err, outputs.ErrTestFireUnsupported):
			problem.Write(w, http.StatusConflict,
				problem.New(problem.TypeConflict, r, "Output class cannot be live-tested", ""))
			return
		default:
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Alarm output test failed", err)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmOutputTest, "output="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- mapping + shared helpers ---

// alarmZoneStatus maps an engine zone snapshot onto the wire status DTO.
func alarmZoneStatus(ctx context.Context, p AlarmPanel, snap engine.ZoneSnapshot) hmapi.AlarmZoneStatus {
	st := hmapi.AlarmZoneStatus{
		ID:       snap.ID,
		Name:     snap.Name,
		State:    string(snap.State),
		Bypassed: snap.Bypassed,
	}
	// The engine keeps a disarmed zone's mode at "disarmed"; the wire
	// leaves it empty so surfaces render mode without a nullable field.
	if snap.Mode != hmenum.AlarmModeDisarmed {
		st.Mode = string(snap.Mode)
	}
	if snap.IncidentID != 0 {
		st.Incident = &hmapi.AlarmIncidentRef{
			ID:       strconv.FormatInt(snap.IncidentID, 10),
			Silenced: snap.IncidentSilenced,
		}
	}
	st.Countdown = alarmCountdown(ctx, p, snap)
	if len(snap.Readiness) > 0 {
		st.Readiness = make(map[string]hmapi.AlarmModeReadiness, len(snap.Readiness))
		for mode, rd := range snap.Readiness {
			st.Readiness[string(mode)] = apiReadiness(rd)
		}
	}
	if wt, err := p.Engine().WalkTestStatus(snap.ID); err == nil {
		st.WalkTestActive = wt.Active
	}
	return st
}

// alarmCountdown surfaces a running exit/entry countdown. The engine
// snapshot carries only the remaining duration, so the total is sourced
// from the zone's mode configuration; when it is unavailable the total
// degrades to the remaining value rather than a misleading zero.
func alarmCountdown(ctx context.Context, p AlarmPanel, snap engine.ZoneSnapshot) *hmapi.AlarmCountdown {
	kind := snap.TimerKind
	if kind != alarmCountdownExit && kind != alarmCountdownEntry {
		return nil
	}
	remaining := durationSeconds(snap.TimerRemaining)
	total := remaining
	if row, ok, err := p.Stores().Zones.Get(ctx, snap.ID); err == nil && ok {
		if cfg, cerr := engine.ParseZoneConfig(row.ConfigJSON); cerr == nil {
			if mc, present := cfg.Modes[snap.Mode]; present {
				switch {
				case kind == alarmCountdownExit && mc.ExitDelaySeconds > 0:
					total = mc.ExitDelaySeconds
				case kind == alarmCountdownEntry && mc.EntryDelaySeconds > 0:
					total = mc.EntryDelaySeconds
				}
			}
		}
	}
	if total < remaining {
		total = remaining
	}
	return &hmapi.AlarmCountdown{Kind: kind, RemainingS: remaining, TotalS: total}
}

// apiReadiness maps an engine readiness verdict onto the wire DTO.
func apiReadiness(rd hmevent.AlarmModeReadiness) hmapi.AlarmModeReadiness {
	return hmapi.AlarmModeReadiness{
		Ready:    rd.Ready,
		Blockers: rd.Blockers,
		Warnings: rd.Warnings,
	}
}

// apiJournalEntry maps a persisted journal row onto the wire DTO.
func apiJournalEntry(e sqlitestore.AlarmJournalEntry) hmapi.AlarmJournalEntry {
	out := hmapi.AlarmJournalEntry{
		ID:         e.ID,
		When:       time.UnixMilli(e.TsMS).UTC(),
		ZoneID:     e.ZoneID,
		Class:      string(e.Class),
		Event:      e.Event,
		Actor:      e.Actor,
		Source:     e.Source,
		IncidentID: e.IncidentID,
	}
	if e.DetailsJSON != "" && e.DetailsJSON != "{}" {
		out.Details = json.RawMessage(e.DetailsJSON)
	}
	return out
}

// apiWalkTestStatus maps an engine walk-test status onto the wire DTO.
// The sensors slice is always a non-nil array so the response matches
// the required-field contract.
func apiWalkTestStatus(st engine.WalkTestStatus) hmapi.AlarmWalkTestStatus {
	out := hmapi.AlarmWalkTestStatus{
		Active:  st.Active,
		Sensors: make([]hmapi.AlarmWalkTestSensor, 0, len(st.Sensors)),
	}
	if !st.StartedAt.IsZero() {
		started := st.StartedAt.UTC()
		out.StartedAt = &started
	}
	for _, s := range st.Sensors {
		row := hmapi.AlarmWalkTestSensor{ID: s.SensorID, Name: s.Name, Tested: !s.SeenAt.IsZero()}
		if !s.SeenAt.IsZero() {
			seen := s.SeenAt.UTC()
			row.LastTriggeredAt = &seen
		}
		out.Sensors = append(out.Sensors, row)
	}
	return out
}

// parseAlarmJournalFilter extracts the GET /alarm/journal query
// parameters. It returns a non-empty errMsg for a malformed class or
// RFC3339 timestamp so the handler can answer 400.
func parseAlarmJournalFilter(r *http.Request) (f sqlitestore.AlarmJournalFilter, errMsg string) { //nolint:gocritic // named returns clarify the dual-return semantics
	q := r.URL.Query()
	f = sqlitestore.AlarmJournalFilter{
		ZoneID: q.Get("zone"),
		Limit:  alarmJournalDefaultLimit,
	}
	if cl := q.Get("class"); cl != "" {
		class := hmenum.AlarmJournalClass(cl)
		if !class.Valid() {
			return sqlitestore.AlarmJournalFilter{}, "class: unknown journal class: " + cl
		}
		f.Class = class
	}
	if fq := q.Get("from"); fq != "" {
		t, err := time.Parse(time.RFC3339, fq)
		if err != nil {
			return sqlitestore.AlarmJournalFilter{}, "from: invalid RFC3339 timestamp: " + fq
		}
		f.FromMS = t.UnixMilli()
	}
	if tq := q.Get("to"); tq != "" {
		t, err := time.Parse(time.RFC3339, tq)
		if err != nil {
			return sqlitestore.AlarmJournalFilter{}, "to: invalid RFC3339 timestamp: " + tq
		}
		// The store bound is inclusive (ts_ms <= ToMS); `to` is
		// documented exclusive, so drop it by one millisecond.
		f.ToMS = t.UnixMilli() - 1
	}
	if lq := q.Get("limit"); lq != "" {
		if n, err := strconv.Atoi(lq); err == nil {
			switch {
			case n <= 0:
				f.Limit = alarmJournalDefaultLimit
			case n > alarmJournalMaxLimit:
				f.Limit = alarmJournalMaxLimit
			default:
				f.Limit = n
			}
		}
	}
	return f, ""
}

// writeArmNotReady writes the 409 arm refusal, echoing the blocking
// sensor ids both in the detail line and the problem `errors` array
// (mirrors AlarmModeReadiness.blockers).
func writeArmNotReady(w http.ResponseWriter, r *http.Request, blockers []string) {
	d := problem.New(problem.TypeConflict, r, "Not ready to arm",
		"readiness blockers present: "+strings.Join(blockers, ", "))
	d.Errors = make([]problem.FieldError, 0, len(blockers))
	for _, id := range blockers {
		d.Errors = append(d.Errors, problem.FieldError{Field: id, Reason: "blocker"})
	}
	problem.Write(w, http.StatusConflict, d)
}

// writeAlarmVerbError maps a bare engine verb error onto a problem
// response: an unknown zone is a 404, a refused code a 403, everything
// else a masked 500.
func writeAlarmVerbError(w http.ResponseWriter, r *http.Request, err error, title string) {
	switch {
	case errors.Is(err, engine.ErrUnknownZone):
		writeAlarmNotFound(w, r)
	case errors.Is(err, engine.ErrInvalidCode):
		writeAlarmInvalidCode(w, r)
	default:
		writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, title, err)
	}
}

// writeAlarmInvalidCode writes the 403 for a missing or wrong alarm code.
// The detail stays deliberately opaque ("invalid_code") so a probing
// caller learns nothing about which codes exist (docs/alarm-concept.md
// §16).
func writeAlarmInvalidCode(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, http.StatusForbidden,
		problem.New(problem.TypeForbidden, r, "Invalid alarm code", "invalid_code"))
}

// decodeAlarmVerbCode decodes the shared optional {code} body of the
// disarm / silence / acknowledge verbs. The body is optional: an absent
// (EOF) body yields an empty code. A malformed body answers 400 and
// reports ok=false so the caller returns without acting.
func decodeAlarmVerbCode(w http.ResponseWriter, r *http.Request) (code string, ok bool) {
	var req hmapi.AlarmVerbRequest
	if err := DecodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		problem.Write(w, DecodeJSONStatus(err),
			problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
		return "", false
	}
	return req.Code, true
}

// writeAlarmNotFound writes the shared 404 for an unknown alarm zone or
// output id.
func writeAlarmNotFound(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, http.StatusNotFound,
		problem.New(problem.TypeNotFound, r, "Unknown alarm resource", ""))
}

// recordAlarm appends an audit row for an alarm mutation when a recorder
// is wired.
func recordAlarm(rec audit.Recorder, r *http.Request, action audit.Action, note string) {
	if rec == nil {
		return
	}
	rec.Record(audit.Entry{
		User:   identityFromCtx(r.Context()),
		Action: action,
		Note:   note,
	})
}

// durationSeconds rounds a countdown duration to whole seconds.
func durationSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(d.Round(time.Second) / time.Second)
}

// ListAlarmPanels serves the alarm-control-panel entity projection:
// the same HA-state view MQTT discovery and the WebSocket broadcast
// carry, including the aggregate master panel.
func ListAlarmPanels(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		panels := p.Panels()
		out := make([]hmapi.AlarmPanelEntity, 0, len(panels))
		for i := range panels {
			pan := &panels[i]
			modes := make([]string, 0, len(pan.Modes))
			for _, m := range pan.Modes {
				modes = append(modes, string(m))
			}
			out = append(out, hmapi.AlarmPanelEntity{
				UniqueID:           pan.UniqueID,
				ZoneID:             pan.ZoneID,
				Name:               pan.Name,
				Category:           string(pan.Category()),
				State:              pan.State,
				SupportedModes:     modes,
				Available:          pan.Available,
				Master:             pan.Master,
				CodeArmRequired:    pan.CodeArmRequired,
				CodeDisarmRequired: pan.CodeDisarmRequired,
			})
		}
		JSON(w, http.StatusOK, out)
	}
}
