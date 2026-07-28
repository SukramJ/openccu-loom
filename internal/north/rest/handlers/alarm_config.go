// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/alarm/outputs"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ListAlarmAreas renders every configured alarm area.
func ListAlarmAreas(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := p.Stores().Areas.GetAll(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "List alarm areas failed", err)
			return
		}
		out := make([]hmapi.AlarmArea, 0, len(rows))
		for i := range rows {
			out = append(out, apiArea(rows[i]))
		}
		JSON(w, http.StatusOK, out)
	}
}

// GetAlarmArea renders a single alarm area by id.
func GetAlarmArea(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		row, ok, err := p.Stores().Areas.Get(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm area failed", err)
			return
		}
		if !ok {
			writeAlarmNotFound(w, r)
			return
		}
		JSON(w, http.StatusOK, apiArea(row))
	}
}

// CreateAlarmArea persists a new alarm area with a server-generated id.
func CreateAlarmArea(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in hmapi.AlarmArea
		if err := DecodeJSON(r, &in); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		cfgJSON := string(in.Config)
		if _, err := engine.ParseAreaConfig(cfgJSON); err != nil {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "Invalid area configuration", err.Error()))
			return
		}
		now := time.Now().UnixMilli()
		row := sqlitestore.AlarmAreaRow{
			ID:          uuid.NewString(),
			Name:        in.Name,
			Position:    in.Position,
			ConfigJSON:  cfgJSON,
			CreatedAtMS: now,
			UpdatedAtMS: now,
		}
		if err := p.Stores().Areas.Upsert(r.Context(), row); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Create alarm area failed", err)
			return
		}
		if err := p.Reload(r.Context()); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Alarm reload failed", err)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmConfigChange, "area_create="+row.ID)
		JSON(w, http.StatusCreated, apiArea(row))
	}
}

// PutAlarmArea replaces an existing alarm area.
func PutAlarmArea(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		existing, ok, err := p.Stores().Areas.Get(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm area failed", err)
			return
		}
		if !ok {
			writeAlarmNotFound(w, r)
			return
		}
		var in hmapi.AlarmArea
		if err := DecodeJSON(r, &in); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		cfgJSON := string(in.Config)
		if _, err := engine.ParseAreaConfig(cfgJSON); err != nil {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "Invalid area configuration", err.Error()))
			return
		}
		row := sqlitestore.AlarmAreaRow{
			ID:          id,
			Name:        in.Name,
			Position:    in.Position,
			ConfigJSON:  cfgJSON,
			CreatedAtMS: existing.CreatedAtMS,
			UpdatedAtMS: time.Now().UnixMilli(),
		}
		if err := p.Stores().Areas.Upsert(r.Context(), row); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Update alarm area failed", err)
			return
		}
		if err := p.Reload(r.Context()); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Alarm reload failed", err)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmConfigChange, "area_update="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteAlarmArea removes an alarm area (and its enrolled sensors and
// outputs). It refuses with 409 while the area is not disarmed.
func DeleteAlarmArea(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		_, ok, err := p.Stores().Areas.Get(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm area failed", err)
			return
		}
		if !ok {
			writeAlarmNotFound(w, r)
			return
		}
		if snap, live := p.Engine().Area(id); live && snap.State != hmenum.AlarmAreaStateDisarmed {
			problem.Write(w, http.StatusConflict,
				problem.New(problem.TypeConflict, r, "Area must be disarmed before deletion",
					"current state: "+string(snap.State)))
			return
		}
		if _, err := p.Stores().Sensors.DeleteByArea(r.Context(), id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Delete alarm sensors failed", err)
			return
		}
		if _, err := p.Stores().Outputs.DeleteByArea(r.Context(), id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Delete alarm outputs failed", err)
			return
		}
		if err := p.Stores().Areas.Delete(r.Context(), id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Delete alarm area failed", err)
			return
		}
		if err := p.Reload(r.Context()); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Alarm reload failed", err)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmConfigChange, "area_delete="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// ListAlarmAreaSensors renders the sensors enrolled in an alarm area.
func ListAlarmAreaSensors(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if ok, err := alarmAreaExists(p, r, id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm area failed", err)
			return
		} else if !ok {
			writeAlarmNotFound(w, r)
			return
		}
		rows, err := p.Stores().Sensors.ListByArea(r.Context(), id)
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

// PutAlarmAreaSensors replaces the full sensor set of an alarm area.
func PutAlarmAreaSensors(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if ok, err := alarmAreaExists(p, r, id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm area failed", err)
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
			if _, err := engine.ParseSensorConfig(string(in[i].Config)); err != nil {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Invalid sensor configuration", err.Error()))
				return
			}
		}
		existing, err := p.Stores().Sensors.ListByArea(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "List alarm sensors failed", err)
			return
		}
		ownIDs := make(map[string]struct{}, len(existing))
		for i := range existing {
			ownIDs[existing[i].ID] = struct{}{}
		}
		seen := make(map[string]struct{}, len(in))
		now := time.Now().UnixMilli()
		rows := make([]sqlitestore.AlarmSensorRow, 0, len(in))
		for i := range in {
			s := &in[i]
			sid := resolveRowID(ownIDs, seen, s.ID)
			rows = append(rows, sqlitestore.AlarmSensorRow{
				ID:             sid,
				AreaID:         id,
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
		if err := p.Stores().Sensors.ReplaceByArea(r.Context(), id, rows); err != nil {
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

// ListAlarmAreaOutputs renders the outputs enrolled in an alarm area.
func ListAlarmAreaOutputs(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if ok, err := alarmAreaExists(p, r, id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm area failed", err)
			return
		} else if !ok {
			writeAlarmNotFound(w, r)
			return
		}
		rows, err := p.Stores().Outputs.ListByArea(r.Context(), id)
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

// PutAlarmAreaOutputs replaces the full output set of an alarm area.
func PutAlarmAreaOutputs(p AlarmPanel, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if ok, err := alarmAreaExists(p, r, id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm area failed", err)
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
		existing, err := p.Stores().Outputs.ListByArea(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "List alarm outputs failed", err)
			return
		}
		ownIDs := make(map[string]struct{}, len(existing))
		for i := range existing {
			ownIDs[existing[i].ID] = struct{}{}
		}
		seen := make(map[string]struct{}, len(in))
		now := time.Now().UnixMilli()
		rows := make([]sqlitestore.AlarmOutputRow, 0, len(in))
		for i := range in {
			o := &in[i]
			oid := resolveRowID(ownIDs, seen, o.ID)
			rows = append(rows, sqlitestore.AlarmOutputRow{
				ID:             oid,
				AreaID:         id,
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
		if err := p.Stores().Outputs.ReplaceByArea(r.Context(), id, rows); err != nil {
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
// sensor/output replace row. A client-supplied id is kept only when it
// round-trips one of THIS area's existing rows and has not been used
// earlier in the same payload; everything else gets a fresh UUID. Ids
// are an intra-area stability hint, not a global identity — clients
// have derived them from the channel key, so enrolling the same channel
// in a second area collided with the first area's PRIMARY KEY and
// failed the whole replace as an opaque 500.
func resolveRowID(ownIDs, seen map[string]struct{}, id string) string {
	if id != "" {
		if _, own := ownIDs[id]; own {
			if _, dup := seen[id]; !dup {
				seen[id] = struct{}{}
				return id
			}
		}
	}
	fresh := uuid.NewString()
	seen[fresh] = struct{}{}
	return fresh
}

// alarmAreaExists reports whether an area id is persisted.
func alarmAreaExists(p AlarmPanel, r *http.Request, id string) (bool, error) {
	_, ok, err := p.Stores().Areas.Get(r.Context(), id)
	return ok, err
}

// apiArea maps a stored area row onto the wire DTO.
func apiArea(row sqlitestore.AlarmAreaRow) hmapi.AlarmArea {
	a := hmapi.AlarmArea{ID: row.ID, Name: row.Name, Position: row.Position}
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
