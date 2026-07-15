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
		now := time.Now().UnixMilli()
		rows := make([]sqlitestore.AlarmSensorRow, 0, len(in))
		for i := range in {
			s := &in[i]
			sid := s.ID
			if sid == "" {
				sid = uuid.NewString()
			}
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
			if _, err := outputs.ParseOutputConfig(string(in[i].Config)); err != nil {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Invalid output configuration", err.Error()))
				return
			}
		}
		now := time.Now().UnixMilli()
		rows := make([]sqlitestore.AlarmOutputRow, 0, len(in))
		for i := range in {
			o := &in[i]
			oid := o.ID
			if oid == "" {
				oid = uuid.NewString()
			}
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
