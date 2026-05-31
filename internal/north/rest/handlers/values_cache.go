// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// ValuesCacheService is the narrow facade the values-cache endpoints
// depend on. *sqlite.ValuesCacheStore satisfies it.
type ValuesCacheService interface {
	DeleteAll(ctx context.Context) error
	DeleteDevice(ctx context.Context, centralName, interfaceID, deviceAddress string) error
	Stats(ctx context.Context) (ValuesCacheStats, error)
	Metrics() ValuesCacheMetrics
}

// ValuesCacheStats mirrors sqlite.Stats one level above the store so
// the handler does not pull the sqlite package into its import graph.
type ValuesCacheStats struct {
	Rows          int64
	ValueJSONSize int64
}

// ValuesCacheMetrics mirrors sqlite.Metrics for the same reason.
type ValuesCacheMetrics struct {
	RestoredRows   int64
	CastFailures   int64
	GCRowsDeleted  int64
	FlushBatches   int64
	FlushedEntries int64
}

// ValuesCacheStatsResponse is the body of
// `GET /api/v1/admin/values-cache/stats`.
type ValuesCacheStatsResponse struct {
	Rows           int64 `json:"rows"`
	ValueJSONSize  int64 `json:"value_json_bytes"`
	RestoredRows   int64 `json:"restored_rows"`
	CastFailures   int64 `json:"cast_failures"`
	GCRowsDeleted  int64 `json:"gc_rows_deleted"`
	FlushBatches   int64 `json:"flush_batches"`
	FlushedEntries int64 `json:"flushed_entries"`
}

// DeviceLookup resolves an address to (central_name, interface_id).
// The handler depends on this so the values-cache reset never has to
// guess which central a device belongs to in a multi-CCU deployment.
type DeviceLookup interface {
	LocateDevice(addr string) (centralName, interfaceID string, ok bool)
}

// GetValuesCacheStats serves
// `GET /api/v1/admin/values-cache/stats`. Returns aggregated row and
// counter information for diagnostics dashboards.
func GetValuesCacheStats(svc ValuesCacheService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "values cache unwired", ""))
			return
		}
		stats, err := svc.Stats(r.Context())
		if err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "stats failed", err.Error()))
			return
		}
		metrics := svc.Metrics()
		resp := ValuesCacheStatsResponse{
			Rows:           stats.Rows,
			ValueJSONSize:  stats.ValueJSONSize,
			RestoredRows:   metrics.RestoredRows,
			CastFailures:   metrics.CastFailures,
			GCRowsDeleted:  metrics.GCRowsDeleted,
			FlushBatches:   metrics.FlushBatches,
			FlushedEntries: metrics.FlushedEntries,
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// ResetValuesCacheGlobal serves
// `POST /api/v1/admin/values-cache/reset`. Wipes every row.
func ResetValuesCacheGlobal(svc ValuesCacheService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "values cache unwired", ""))
			return
		}
		if err := svc.DeleteAll(r.Context()); err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "reset failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ResetValuesCacheDevice serves
// `POST /api/v1/devices/{addr}/values-cache/reset`. Drops every row
// for the device's channels. The lookup translates the bare address
// into the (central, interface) tuple the store rows are keyed by.
func ResetValuesCacheDevice(svc ValuesCacheService, lookup DeviceLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil || lookup == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "values cache unwired", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		if addr == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "missing address", ""))
			return
		}
		central, iface, ok := lookup.LocateDevice(addr)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "device not found", ""))
			return
		}
		if err := svc.DeleteDevice(r.Context(), central, iface, addr); err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "reset failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// writeJSON is a small marshal+write helper used by the handlers in
// this file. Mirrors the pattern from diagnostics_capture.go.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
