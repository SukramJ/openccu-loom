// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// defaultIncidentLimit bounds an unfiltered listing. The ledger grows
// with every alarm, and a UI that renders "recent incidents" wants a
// page, not the retention window.
const defaultIncidentLimit = 50

// maxIncidentLimit caps ?limit= so a client cannot ask for the whole
// table in one response.
const maxIncidentLimit = 500

// ListAlarmIncidents serves the incident history of one zone.
//
// The store has carried ListByZone since the incident ledger existed,
// but nothing ever called it — an alarm's history was reachable only
// through the journal, which records events rather than episodes and
// holds no source identity.
func ListAlarmIncidents(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zoneID := r.URL.Query().Get("zone_id")
		if zoneID == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid query parameter",
					"zone_id is required"))
			return
		}
		limit := defaultIncidentLimit
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > maxIncidentLimit {
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeBadRequest, r, "Invalid query parameter",
						"limit must be between 1 and "+strconv.Itoa(maxIncidentLimit)))
				return
			}
			limit = n
		}
		stores := p.Stores()
		rows, err := stores.Incidents.ListByZone(r.Context(), zoneID, limit)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal,
				"Incident query failed", err)
			return
		}
		// One batched source query rather than one per incident: a page
		// of 50 would otherwise cost 51 statements.
		ids := make([]int64, 0, len(rows))
		for i := range rows {
			ids = append(ids, rows[i].ID)
		}
		sources := map[int64][]sqlitestore.AlarmIncidentSource{}
		if stores.IncidentSources != nil {
			sources, err = stores.IncidentSources.ListByIncidents(r.Context(), ids)
			if err != nil {
				writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal,
					"Incident source query failed", err)
				return
			}
		}
		out := make([]hmapi.AlarmIncident, 0, len(rows))
		for i := range rows {
			out = append(out, apiIncident(rows[i], sources[rows[i].ID]))
		}
		JSON(w, http.StatusOK, out)
	}
}

// GetAlarmIncident serves one incident with its full source ledger.
func GetAlarmIncident(p AlarmPanel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid incident id", ""))
			return
		}
		stores := p.Stores()
		row, found, err := stores.Incidents.Get(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal,
				"Incident query failed", err)
			return
		}
		if !found {
			writeAlarmNotFound(w, r)
			return
		}
		var sources []sqlitestore.AlarmIncidentSource
		if stores.IncidentSources != nil {
			sources, err = stores.IncidentSources.ListByIncident(r.Context(), id)
			if err != nil {
				writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal,
					"Incident source query failed", err)
				return
			}
		}
		JSON(w, http.StatusOK, apiIncident(row, sources))
	}
}

// apiIncident projects a stored incident plus its ledger onto the API
// shape.
func apiIncident(row sqlitestore.AlarmIncident, sources []sqlitestore.AlarmIncidentSource) hmapi.AlarmIncident {
	out := hmapi.AlarmIncident{
		ID:              row.ID,
		ZoneID:          row.ZoneID,
		Mode:            string(row.Mode),
		StartedAt:       msToTime(row.StartedAtMS),
		ClosedAt:        msToTime(row.ClosedAtMS),
		CloseReason:     row.CloseReason,
		Silenced:        row.Silenced,
		SilencedAt:      msToTime(row.SilencedAtMS),
		SilencedBy:      row.SilencedBy,
		RetriggerCycles: row.RetriggerCycles,
		AcousticSeconds: int(row.AcousticMS / 1000),
		Open:            row.ClosedAtMS == 0,
	}
	if cause, ok := decodeIncidentCause(row.CauseJSON); ok {
		out.Cause = cause.Kind
		out.CauseSensorID = cause.SensorID
		out.CauseSensorName = cause.SensorName
	}
	for i := range sources {
		out.Sources = append(out.Sources, apiIncidentSource(sources[i]))
	}
	return out
}

func apiIncidentSource(row sqlitestore.AlarmIncidentSource) hmapi.AlarmSource {
	return hmapi.AlarmSource{
		Ref:            row.Ref,
		Central:        row.CentralName,
		InterfaceID:    row.InterfaceID,
		ChannelAddress: row.ChannelAddress,
		DeviceAddress:  row.DeviceAddress,
		Parameter:      row.Parameter,
		SensorID:       row.SensorID,
		Name:           row.Name,
		SensorType:     row.SensorType,
		Class:          row.Class,
		Cause:          row.Cause,
		At:             msToTime(row.AtMS),
	}
}

// incidentCauseDoc mirrors the engine-owned cause document. The engine
// keeps its own private struct; this is the read-side projection, so a
// field the engine adds later simply stays unread rather than breaking
// the decode.
type incidentCauseDoc struct {
	Kind       string `json:"kind"`
	SensorID   string `json:"sensor_id"`
	SensorName string `json:"sensor_name"`
}

func decodeIncidentCause(raw string) (incidentCauseDoc, bool) {
	if raw == "" {
		return incidentCauseDoc{}, false
	}
	var doc incidentCauseDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return incidentCauseDoc{}, false
	}
	return doc, true
}

// msToTime converts Unix milliseconds to a time, mapping 0 onto the
// zero time so `omitzero` drops an unset timestamp from the response
// instead of rendering 1970.
func msToTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
