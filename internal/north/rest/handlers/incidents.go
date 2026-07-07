// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Incident is an alias for the canonical DTO in pkg/hmapi.
type Incident = hmapi.Incident

// IncidentsReader is an alias for the canonical interface in pkg/interfaces.
type IncidentsReader = interfaces.IncidentsReader

// IncidentsQuerier is the optional filtered read path. When the wired
// [IncidentsReader] also implements it, ListIncidents pushes the
// central / timestamp / limit filters down to the store (mirrors
// [AuditQuerier] for GET /audit) instead of filtering the full
// cross-central set in memory. central empty means "every registered
// central"; a zero since/until disables that bound; limit<=0 means "no
// cap" for the fallback in-memory path but is capped to
// [incidentsMaxLimit] before being handed to the querier.
type IncidentsQuerier interface {
	IncidentsFiltered(central string, since, until time.Time, limit int) []hmapi.Incident
}

const (
	incidentsDefaultLimit = 500
	incidentsMaxLimit     = 5000
)

// incidentsFilter holds parsed query-parameter values for GET /incidents.
type incidentsFilter struct {
	central string
	since   time.Time
	until   time.Time
	limit   int
}

// parseIncidentsFilter extracts and validates the query parameters from r.
// It returns a non-nil errMsg when a parameter is malformed (invalid
// RFC3339 timestamp).
func parseIncidentsFilter(r *http.Request) (f incidentsFilter, errMsg string) { //nolint:gocritic // named returns clarify dual-return semantics
	q := r.URL.Query()
	f = incidentsFilter{
		central: q.Get("central"),
		limit:   incidentsDefaultLimit,
	}
	if lq := q.Get("limit"); lq != "" {
		if n, err := strconv.Atoi(lq); err == nil {
			switch {
			case n <= 0:
				f.limit = incidentsDefaultLimit
			case n > incidentsMaxLimit:
				f.limit = incidentsMaxLimit
			default:
				f.limit = n
			}
		}
	}
	if sq := q.Get("since"); sq != "" {
		t, err := time.Parse(time.RFC3339, sq)
		if err != nil {
			return incidentsFilter{}, "since: invalid RFC3339 timestamp: " + sq
		}
		f.since = t
	}
	if uq := q.Get("until"); uq != "" {
		t, err := time.Parse(time.RFC3339, uq)
		if err != nil {
			return incidentsFilter{}, "until: invalid RFC3339 timestamp: " + uq
		}
		f.until = t
	}
	return f, ""
}

// componentMatchesCentral reports whether an [hmapi.Incident.Component]
// value ("<central>" or "<central>/<interface>", see toAPIIncident in
// internal/central/adapter/incidents.go) belongs to central.
func componentMatchesCentral(component, central string) bool {
	return component == central || strings.HasPrefix(component, central+"/")
}

// applyIncidentsFilter runs the in-memory filter pass over the unfiltered
// cross-central set for readers that only implement [IncidentsReader].
func applyIncidentsFilter(entries []hmapi.Incident, f incidentsFilter) []hmapi.Incident {
	out := make([]hmapi.Incident, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		if f.central != "" && !componentMatchesCentral(e.Component, f.central) {
			continue
		}
		if !f.since.IsZero() && e.When.Before(f.since) {
			continue
		}
		if !f.until.IsZero() && !e.When.Before(f.until) {
			continue
		}
		out = append(out, *e)
		if f.limit > 0 && len(out) == f.limit {
			break
		}
	}
	return out
}

// ListIncidents renders the current incident list.
//
// Supported query parameters:
//
//	?central=<name>           only incidents belonging to the named CCU
//	?since=<RFC3339>          only incidents at-or-after the timestamp
//	?until=<RFC3339>          only incidents strictly before the timestamp
//	?limit=<int>              max results, default 500, max 5000
func ListIncidents(reader IncidentsReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if reader == nil {
			JSON(w, http.StatusOK, []Incident{})
			return
		}
		f, errMsg := parseIncidentsFilter(r)
		if errMsg != "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid query parameter", errMsg))
			return
		}
		if querier, ok := reader.(IncidentsQuerier); ok {
			JSON(w, http.StatusOK, querier.IncidentsFiltered(f.central, f.since, f.until, f.limit))
			return
		}
		JSON(w, http.StatusOK, applyIncidentsFilter(reader.Incidents(), f))
	}
}

// IncidentsClearer is the write contract for DELETE /incidents. Clears
// every registered central's incident rows; shares the domain call with
// the WS `incidents.clear` command ([ws.IncidentClearer] in
// internal/north/rest/ws). *adapter.IncidentsStoreReader satisfies it
// directly alongside [IncidentsReader].
type IncidentsClearer interface {
	ClearIncidents(ctx context.Context) error
}

// DeleteIncidents clears the incident store across every registered
// central.
func DeleteIncidents(clearer IncidentsClearer, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if clearer == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Incident store unavailable", ""))
			return
		}
		if err := clearer.ClearIncidents(r.Context()); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Clear incidents failed", err)
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:   identityFromCtx(r.Context()),
				Action: audit.ActionIncidentsClear,
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
