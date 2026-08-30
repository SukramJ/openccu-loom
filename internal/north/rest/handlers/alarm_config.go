// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/alarm/outputs"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ListAlarmZones renders every configured alarm zone.
func ListAlarmZones(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := p.Stores().Zones.GetAll(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "List alarm zones failed", err)
			return
		}
		out := make([]hmapi.AlarmZone, 0, len(rows))
		for i := range rows {
			out = append(out, apiZone(rows[i]))
		}
		JSON(w, http.StatusOK, out)
	}
}

// GetAlarmZone renders a single alarm zone by id.
func GetAlarmZone(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		row, ok, err := p.Stores().Zones.Get(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm zone failed", err)
			return
		}
		if !ok {
			writeAlarmNotFound(w, r)
			return
		}
		JSON(w, http.StatusOK, apiZone(row))
	}
}

// CreateAlarmZone persists a new alarm zone with a server-generated id.
func CreateAlarmZone(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in hmapi.AlarmZone
		if err := DecodeJSON(r, &in); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		// The name is the only operator-facing identifier in the zone
		// list and every arm/disarm confirm dialog — an empty one is an
		// unlabelled row the operator cannot tell apart on a surface
		// where picking the wrong one has physical consequences.
		if strings.TrimSpace(in.Name) == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "Missing name", "zone name is required"))
			return
		}
		cfgJSON := string(in.Config)
		if _, err := engine.ParseZoneConfig(cfgJSON); err != nil {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "Invalid zone configuration", err.Error()))
			return
		}
		now := time.Now().UnixMilli()
		existing, err := p.Stores().Zones.GetAll(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal,
				"List alarm zones failed", err)
			return
		}
		row := sqlitestore.AlarmZoneRow{
			ID: uuid.NewString(),
			// The slug is assigned once, here, and never again: it ends
			// up in consumer entity ids and MQTT topics, so a later
			// rename must not move it.
			Slug:        uniqueZoneSlug(in.Name, existing),
			Name:        in.Name,
			Position:    in.Position,
			ConfigJSON:  cfgJSON,
			CreatedAtMS: now,
			UpdatedAtMS: now,
		}
		if err := p.Stores().Zones.Upsert(r.Context(), row); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Create alarm zone failed", err)
			return
		}
		if err := p.Reload(r.Context()); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Alarm reload failed", err)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmConfigChange, "zone_create="+row.ID)
		JSON(w, http.StatusCreated, apiZone(row))
	}
}

// PutAlarmZone replaces an existing alarm zone.
func PutAlarmZone(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		existing, ok, err := p.Stores().Zones.Get(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm zone failed", err)
			return
		}
		if !ok {
			writeAlarmNotFound(w, r)
			return
		}
		var in hmapi.AlarmZone
		if err := DecodeJSON(r, &in); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		// See the identical check in CreateAlarmZone: the name is the
		// only operator-facing identifier for this zone.
		if strings.TrimSpace(in.Name) == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "Missing name", "zone name is required"))
			return
		}
		cfgJSON := string(in.Config)
		if _, err := engine.ParseZoneConfig(cfgJSON); err != nil {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "Invalid zone configuration", err.Error()))
			return
		}
		row := sqlitestore.AlarmZoneRow{
			ID:          id,
			Name:        in.Name,
			Position:    in.Position,
			ConfigJSON:  cfgJSON,
			CreatedAtMS: existing.CreatedAtMS,
			UpdatedAtMS: time.Now().UnixMilli(),
		}
		if err := p.Stores().Zones.Upsert(r.Context(), row); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Update alarm zone failed", err)
			return
		}
		if err := p.Reload(r.Context()); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Alarm reload failed", err)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmConfigChange, "zone_update="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteAlarmZone removes an alarm zone (and its enrolled sensors and
// outputs). It refuses with 409 while the zone is not disarmed.
func DeleteAlarmZone(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		_, ok, err := p.Stores().Zones.Get(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm zone failed", err)
			return
		}
		if !ok {
			writeAlarmNotFound(w, r)
			return
		}
		if snap, live := p.Engine().Zone(id); live && snap.State != hmenum.AlarmZoneStateDisarmed {
			problem.Write(w, http.StatusConflict,
				problem.New(problem.TypeConflict, r, "Zone must be disarmed before deletion",
					"current state: "+string(snap.State)))
			return
		}
		if _, err := p.Stores().Sensors.DeleteByZone(r.Context(), id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Delete alarm sensors failed", err)
			return
		}
		if _, err := p.Stores().Outputs.DeleteByZone(r.Context(), id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Delete alarm outputs failed", err)
			return
		}
		if err := p.Stores().Zones.Delete(r.Context(), id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Delete alarm zone failed", err)
			return
		}
		if err := p.Reload(r.Context()); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Alarm reload failed", err)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmConfigChange, "zone_delete="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// ListAlarmZoneSensors renders the sensors enrolled in an alarm zone.
func ListAlarmZoneSensors(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if ok, err := alarmZoneExists(p, r, id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm zone failed", err)
			return
		} else if !ok {
			writeAlarmNotFound(w, r)
			return
		}
		rows, err := p.Stores().Sensors.ListByZone(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "List alarm sensors failed", err)
			return
		}
		out := make([]hmapi.AlarmSensor, 0, len(rows))
		for i := range rows {
			out = append(out, apiSensor(rows[i]))
		}
		JSON(w, http.StatusOK, out)
	}
}

// PutAlarmZoneSensors replaces the full sensor set of an alarm zone.
func PutAlarmZoneSensors(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if ok, err := alarmZoneExists(p, r, id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm zone failed", err)
			return
		} else if !ok {
			writeAlarmNotFound(w, r)
			return
		}
		var in []hmapi.AlarmSensor
		if err := DecodeJSON(r, &in); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		// Validate every row before touching the store so a bad entry
		// never leaves the set half-replaced.
		for i := range in {
			if t := in[i].Type; t != "" && !hmenum.AlarmSensorType(t).Valid() {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Invalid sensor type", "unknown sensor type: "+t))
				return
			}
			cfg, err := engine.ParseSensorConfig(string(in[i].Config))
			if err != nil {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Invalid sensor configuration", err.Error()))
				return
			}
			// A hazard sensor that is not always-on falls into the
			// arm-state machine and therefore only fires while the zone
			// is armed in one of its listed modes. With an empty mode
			// list — the normal shape for a smoke detector — it never
			// fires at all. Coupling the two server-side means the
			// failure cannot be configured, rather than merely being
			// documented.
			if in[i].Type == string(hmenum.AlarmSensorTypeHazard) && !cfg.AlwaysOn {
				cfg.AlwaysOn = true
				patched, err := json.Marshal(cfg)
				if err != nil {
					writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal,
						"Sensor configuration re-encode failed", err)
					return
				}
				in[i].Config = patched
			}
		}
		all, err := p.Stores().Sensors.GetAll(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "List alarm sensors failed", err)
			return
		}
		foreignIDs := make(map[string]struct{}, len(all))
		for i := range all {
			if all[i].ZoneID != id {
				foreignIDs[all[i].ID] = struct{}{}
			}
		}
		seen := make(map[string]struct{}, len(in))
		now := time.Now().UnixMilli()
		rows := make([]sqlitestore.AlarmSensorRow, 0, len(in))
		for i := range in {
			s := &in[i]
			sid := resolveRowID(foreignIDs, seen, s.ID)
			rows = append(rows, sqlitestore.AlarmSensorRow{
				ID:             sid,
				ZoneID:         id,
				CentralName:    s.Central,
				InterfaceID:    s.InterfaceID,
				ChannelAddress: s.ChannelAddress,
				Parameter:      s.Parameter,
				SensorType:     hmenum.AlarmSensorType(s.Type),
				Name:           s.Name,
				ConfigJSON:     string(s.Config),
				CreatedAtMS:    now,
				UpdatedAtMS:    now,
			})
		}
		// One transaction: a mid-write failure must never persist a
		// truncated sensor set the next reload would silently adopt.
		if err := p.Stores().Sensors.ReplaceByZone(r.Context(), id, rows); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Replace alarm sensors failed", err)
			return
		}
		if err := p.Reload(r.Context()); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Alarm reload failed", err)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmConfigChange, "sensors_replace="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// ListAlarmZoneOutputs renders the outputs enrolled in an alarm zone.
func ListAlarmZoneOutputs(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if ok, err := alarmZoneExists(p, r, id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm zone failed", err)
			return
		} else if !ok {
			writeAlarmNotFound(w, r)
			return
		}
		rows, err := p.Stores().Outputs.ListByZone(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "List alarm outputs failed", err)
			return
		}
		out := make([]hmapi.AlarmOutput, 0, len(rows))
		for i := range rows {
			out = append(out, apiOutput(rows[i]))
		}
		JSON(w, http.StatusOK, out)
	}
}

// PutAlarmZoneOutputs replaces the full output set of an alarm zone.
func PutAlarmZoneOutputs(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if ok, err := alarmZoneExists(p, r, id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm zone failed", err)
			return
		} else if !ok {
			writeAlarmNotFound(w, r)
			return
		}
		var in []hmapi.AlarmOutput
		if err := DecodeJSON(r, &in); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		for i := range in {
			if c := in[i].Class; c == "" || !hmenum.AlarmOutputClass(c).Valid() {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Invalid output class", "unknown output class: "+c))
				return
			}
			ocfg, err := outputs.ParseOutputConfig(string(in[i].Config))
			if err != nil {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Invalid output configuration", err.Error()))
				return
			}
			// A sysvar mirror without a variable name would be a silent
			// no-op (the mirror skips nameless targets) — reject it.
			if hmenum.AlarmOutputClass(in[i].Class) == hmenum.AlarmOutputClassSysvarMirror && ocfg.SysvarName == "" {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Sysvar mirror needs a variable name",
						"sysvar_name is required for class sysvar_mirror"))
				return
			}
			// Soft target validation: reject a channel that resolves but
			// cannot carry the class (the runtime driver would fault on
			// every fire). Unresolvable targets pass — a CCU that is down
			// or still booting must never block a config save; the fault
			// journal remains their safety net.
			if eligible, known := p.OutputTargetEligible(in[i].Central, in[i].ChannelAddress,
				hmenum.AlarmOutputClass(in[i].Class)); known && !eligible {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Output class not supported by channel",
						in[i].ChannelAddress+" cannot back class "+in[i].Class))
				return
			}
		}
		all, err := p.Stores().Outputs.GetAll(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "List alarm outputs failed", err)
			return
		}
		foreignIDs := make(map[string]struct{}, len(all))
		for i := range all {
			if all[i].ZoneID != id {
				foreignIDs[all[i].ID] = struct{}{}
			}
		}
		seen := make(map[string]struct{}, len(in))
		now := time.Now().UnixMilli()
		rows := make([]sqlitestore.AlarmOutputRow, 0, len(in))
		for i := range in {
			o := &in[i]
			oid := resolveRowID(foreignIDs, seen, o.ID)
			rows = append(rows, sqlitestore.AlarmOutputRow{
				ID:             oid,
				ZoneID:         id,
				Class:          hmenum.AlarmOutputClass(o.Class),
				CentralName:    o.Central,
				ChannelAddress: o.ChannelAddress,
				Name:           o.Name,
				ConfigJSON:     string(o.Config),
				CreatedAtMS:    now,
				UpdatedAtMS:    now,
			})
		}
		// One transaction — no partial output sets (mirrors sensors).
		if err := p.Stores().Outputs.ReplaceByZone(r.Context(), id, rows); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Replace alarm outputs failed", err)
			return
		}
		if err := p.Reload(r.Context()); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Alarm reload failed", err)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmConfigChange, "outputs_replace="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// resolveRowID returns the row id to persist for one incoming
// sensor/output replace row. A client-supplied id round-trips (rows of
// this zone keep their identity, and a client may choose fresh ids)
// UNLESS it already belongs to ANOTHER zone's row or repeats within
// the payload — those get a fresh UUID instead of failing the whole
// replace on the PRIMARY KEY. Clients have derived ids from the
// channel key, so enrolling the same channel in a second zone collided
// with the first zone's row and 500ed opaquely.
func resolveRowID(foreignIDs, seen map[string]struct{}, id string) string {
	if id != "" {
		_, foreign := foreignIDs[id]
		_, dup := seen[id]
		if !foreign && !dup {
			seen[id] = struct{}{}
			return id
		}
	}
	fresh := uuid.NewString()
	seen[fresh] = struct{}{}
	return fresh
}

// alarmZoneExists reports whether an zone id is persisted.
func alarmZoneExists(p AlarmPanel, r *http.Request, id string) (bool, error) {
	_, ok, err := p.Stores().Zones.Get(r.Context(), id)
	return ok, err
}

// apiZone maps a stored zone row onto the wire DTO.
func apiZone(row sqlitestore.AlarmZoneRow) hmapi.AlarmZone {
	a := hmapi.AlarmZone{ID: row.ID, Name: row.Name, Position: row.Position}
	if row.ConfigJSON != "" {
		a.Config = json.RawMessage(row.ConfigJSON)
	}
	return a
}

// apiSensor maps a stored sensor row onto the wire DTO.
func apiSensor(row sqlitestore.AlarmSensorRow) hmapi.AlarmSensor {
	s := hmapi.AlarmSensor{
		ID:             row.ID,
		Central:        row.CentralName,
		InterfaceID:    row.InterfaceID,
		ChannelAddress: row.ChannelAddress,
		Parameter:      row.Parameter,
		Type:           string(row.SensorType),
		Name:           row.Name,
	}
	if row.ConfigJSON != "" {
		s.Config = json.RawMessage(row.ConfigJSON)
	}
	return s
}

// apiOutput maps a stored output row onto the wire DTO.
func apiOutput(row sqlitestore.AlarmOutputRow) hmapi.AlarmOutput {
	o := hmapi.AlarmOutput{
		ID:             row.ID,
		Class:          string(row.Class),
		Central:        row.CentralName,
		ChannelAddress: row.ChannelAddress,
		Name:           row.Name,
	}
	if row.ConfigJSON != "" {
		o.Config = json.RawMessage(row.ConfigJSON)
	}
	return o
}

// uniqueZoneSlug derives a stable external identifier from a zone name,
// appending a numeric suffix on collision.
//
// It falls back to a fixed stem when the name yields nothing sluggable —
// a zone named only with emoji still needs an identifier, and the UUID
// is unusable in an entity id, which is the reason the slug exists.
func uniqueZoneSlug(name string, existing []sqlitestore.AlarmZoneRow) string {
	taken := make(map[string]bool, len(existing))
	for i := range existing {
		// A blank stored slug (pre-migration rows the charset migration
		// reset, or a row that has never been read through the security
		// domain's refreshZoneSlugs) still resolves to an effective slug at
		// read time, so it must reserve that slug here too — including the
		// stem, for a name that slugs to nothing. Reserving only the
		// non-blank derivations left the stem free and handed a new zone the
		// identity an emoji-named one already answered to.
		taken[routingkey.EffectiveSlug(existing[i].Slug, existing[i].Name, routingkey.ZoneSlugStem)] = true
	}
	return routingkey.UniqueSlug(name, routingkey.ZoneSlugStem, taken)
}
