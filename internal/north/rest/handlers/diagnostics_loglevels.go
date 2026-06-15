// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// LogLevelsService is the narrow facade the diagnostics-log-levels
// endpoint needs. *hmlog.LevelRegistry satisfies it directly.
type LogLevelsService interface {
	Default() slog.Level
	Set(path string, level slog.Level, ttl time.Duration)
	Reset(path string) bool
	Snapshot() []hmlog.OverrideInfo
}

// LogLevelEntry is one override line returned by the GET endpoint.
type LogLevelEntry struct {
	Path        string `json:"path"`
	Level       string `json:"level"`
	Permanent   bool   `json:"permanent"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	RemainingMS int64  `json:"remaining_ms,omitempty"`
}

// LogLevelsResponse is the body of `GET /api/v1/diagnostics/log-levels`.
type LogLevelsResponse struct {
	Default   string          `json:"default"`
	Overrides []LogLevelEntry `json:"overrides"`
}

// LogLevelPutRequest is the body of `PUT /api/v1/diagnostics/log-levels/{path}`.
type LogLevelPutRequest struct {
	Level      string `json:"level"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"` // 0 ⇒ permanent
}

// maxLogLevelTTL is the largest TTL the REST endpoint accepts.
// Permanent overrides (TTL = 0) are still allowed; the cap only
// applies to user-supplied positive values to keep a forgetful
// operator from leaving debug-level subsystems running forever.
const maxLogLevelTTL = 24 * time.Hour

// ListLogLevels returns a handler that emits the current default
// level plus every configured override. Safe to call without auth in
// development; production deployments should gate it behind the same
// admin role that controls /backup.
func ListLogLevels(svc LogLevelsService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "log-levels service unwired", ""))
			return
		}
		out := LogLevelsResponse{Default: hmlog.FormatLevel(svc.Default())}
		for _, ov := range svc.Snapshot() {
			entry := LogLevelEntry{
				Path:      ov.Path,
				Level:     hmlog.FormatLevel(ov.Level),
				Permanent: ov.Permanent,
			}
			if !ov.ExpiresAt.IsZero() {
				entry.ExpiresAt = ov.ExpiresAt.UTC().Format(time.RFC3339Nano)
				entry.RemainingMS = ov.RemainingMS
			}
			out.Overrides = append(out.Overrides, entry)
		}
		JSON(w, http.StatusOK, out)
	}
}

// PutLogLevel installs or replaces an override for {path}. Body
// carries the level + an optional TTL in seconds. TTL <= 0 produces
// a permanent override.
func PutLogLevel(svc LogLevelsService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "log-levels service unwired", ""))
			return
		}
		path := chi.URLParam(r, "path")
		if path == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "log level path required", ""))
			return
		}
		var req LogLevelPutRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "invalid body", err.Error()))
			return
		}
		level, err := hmlog.ParseLevel(req.Level)
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "invalid level", err.Error()))
			return
		}
		ttl := time.Duration(req.TTLSeconds) * time.Second
		if ttl > maxLogLevelTTL {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "ttl_seconds exceeds 24h cap", ""))
			return
		}
		svc.Set(path, level, ttl)
		if rec != nil {
			note := "level=" + hmlog.FormatLevel(level)
			if ttl > 0 {
				note += " ttl=" + ttl.String()
			} else {
				note += " ttl=permanent"
			}
			rec.Record(audit.Entry{
				Action: audit.Action("logging.override_set"),
				Note:   path + " " + note,
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteLogLevel removes an override. Returns 204 whether the override
// existed or not — idempotent reset.
func DeleteLogLevel(svc LogLevelsService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "log-levels service unwired", ""))
			return
		}
		path := chi.URLParam(r, "path")
		if path == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "log level path required", ""))
			return
		}
		removed := svc.Reset(path)
		if removed && rec != nil {
			rec.Record(audit.Entry{
				Action: audit.Action("logging.override_reset"),
				Note:   path,
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
