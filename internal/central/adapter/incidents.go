// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// incidentsReadTimeout bounds the synchronous per-call SQLite read. The
// IncidentsReader contract carries no context, so the read uses its own
// short-lived one.
const incidentsReadTimeout = 5 * time.Second

// IncidentsStoreReader is the live implementation of handlers.IncidentsReader.
// It serves the incidents persisted by the SQLite incident store (the same
// store the reliability layer records into) across every registered central,
// replacing the former empty-list MVP stub. Without it, recorded incidents are
// never surfaced by GET /api/v1/incidents or the diagnostics envelope.
type IncidentsStoreReader struct {
	store  *sqlite.IncidentStore
	reg    *central.Registry
	logger *slog.Logger
}

// NewIncidentsStoreReader wires the live reader. A nil store (incident
// persistence disabled) yields an empty list rather than an error, matching
// the graceful-degradation contract callers already expect.
func NewIncidentsStoreReader(store *sqlite.IncidentStore, reg *central.Registry, logger *slog.Logger) *IncidentsStoreReader {
	if logger == nil {
		logger = slog.Default()
	}
	return &IncidentsStoreReader{store: store, reg: reg, logger: logger}
}

// Incidents implements handlers.IncidentsReader. It reads each central's
// persisted incidents and maps them to the REST contract type. Synchronous;
// per-central read errors are logged and skipped so one bad central does not
// blank the whole list.
func (r *IncidentsStoreReader) Incidents() []hmapi.Incident {
	if r == nil || r.store == nil || r.reg == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), incidentsReadTimeout)
	defer cancel()

	var out []hmapi.Incident
	for _, u := range r.reg.List() {
		if u == nil {
			continue
		}
		recs, err := r.store.GetAllIncidents(ctx, u.Name())
		if err != nil {
			r.logger.Warn("incidents.read.failed",
				slog.String("central", u.Name()), slog.String("err", err.Error()))
			continue
		}
		for i := range recs {
			out = append(out, toAPIIncident(&recs[i]))
		}
	}
	return out
}

// toAPIIncident maps a stored incident to the REST contract shape. Component
// names the source (central, plus interface when interface-scoped); Summary
// falls back to the incident type when no message was recorded; Detail folds in
// the journal excerpt.
func toAPIIncident(s *sqlite.Incident) hmapi.Incident {
	component := s.CentralName
	if s.InterfaceID != "" {
		component = s.CentralName + "/" + s.InterfaceID
	}
	summary := s.Message
	if summary == "" {
		summary = string(s.Type)
	}
	detail := s.Details
	if s.JournalExcerpt != "" {
		if detail != "" {
			detail += "\n"
		}
		detail += s.JournalExcerpt
	}
	return hmapi.Incident{
		ID:        strconv.FormatInt(s.ID, 10),
		When:      s.LastSeen,
		Component: component,
		Severity:  string(s.Severity),
		Summary:   summary,
		Detail:    detail,
	}
}
