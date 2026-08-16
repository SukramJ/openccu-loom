// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// LogFeedService is the narrow facade the log-viewer endpoints need.
// *hmlog.LiveLog satisfies it directly.
type LogFeedService interface {
	// Snapshot returns the most recent records at or above minLevel,
	// oldest-first. limit <= 0 returns the whole filtered ring.
	Snapshot(limit int, minLevel slog.Level) []hmlog.LogRecord
	// Since returns retained records with Seq > seq at or above minLevel.
	Since(seq uint64, minLevel slog.Level) []hmlog.LogRecord
	// Subscribe attaches a live consumer; cancel detaches it.
	Subscribe(minLevel slog.Level) (<-chan hmlog.LogRecord, func())
	// LastSeq is the newest recorded sequence number.
	LastSeq() uint64
}

// LogDefaultLevelService backs the log-viewer level dropdown (R1). It
// changes the global default level without touching per-subsystem
// overrides. *hmlog.LevelRegistry satisfies it.
type LogDefaultLevelService interface {
	Default() slog.Level
	SetDefault(level slog.Level)
}

const (
	// defaultLogBackfill is the number of records returned by GET
	// /diagnostics/logs when no limit is given.
	defaultLogBackfill = 500
	// maxLogBackfill caps the limit query so a single request cannot
	// pull an unbounded slice of the ring.
	maxLogBackfill = hmlog.DefaultLiveLogCapacity
	// logStreamHeartbeat is the SSE keep-alive interval. Comments
	// (`: ping`) keep proxies from closing an idle connection and let
	// the server notice a dead client on the next write.
	logStreamHeartbeat = 15 * time.Second
)

// LogsResponse is the body of `GET /api/v1/diagnostics/logs` in JSON
// (non-ndjson) mode.
type LogsResponse struct {
	LastSeq uint64            `json:"last_seq"`
	Records []hmlog.LogRecord `json:"records"`
}

// parseMinLevel reads the `min_level` query parameter, defaulting to
// debug (everything) on absence or a parse error.
func parseMinLevel(r *http.Request) slog.Level {
	raw := r.URL.Query().Get("min_level")
	if raw == "" {
		return slog.LevelDebug
	}
	if lvl, err := hmlog.ParseLevel(raw); err == nil {
		return lvl
	}
	return slog.LevelDebug
}

// ListLogs serves the ring buffer as a backfill / download. Query:
// `limit=N` (default 500, capped), `min_level=`, `format=ndjson`,
// `download=1` (sets a Content-Disposition attachment header).
func ListLogs(svc LogFeedService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "log feed unwired", ""))
			return
		}
		limit := defaultLogBackfill
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > maxLogBackfill {
			limit = maxLogBackfill
		}
		records := svc.Snapshot(limit, parseMinLevel(r))
		download := r.URL.Query().Get("download") == "1"

		if r.URL.Query().Get("format") == "ndjson" {
			w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
			if download {
				w.Header().Set("Content-Disposition",
					`attachment; filename="openccu-loom-logs.ndjson"`)
			}
			w.WriteHeader(http.StatusOK)
			enc := json.NewEncoder(w)
			for i := range records {
				_ = enc.Encode(records[i])
			}
			return
		}
		if download {
			w.Header().Set("Content-Disposition",
				`attachment; filename="openccu-loom-logs.json"`)
		}
		JSON(w, http.StatusOK, LogsResponse{LastSeq: svc.LastSeq(), Records: records})
	}
}

// StreamLogs serves a Server-Sent-Events live tail of the log ring.
// Resume is supported via the `since=<seq>` query parameter or the
// `Last-Event-ID` header (the query wins when both are present). The
// `min_level` query filters server-side. Each event carries the record
// Seq as its id so a reconnecting EventSource resumes without gaps or
// duplicates.
func StreamLogs(svc LogFeedService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "log feed unwired", ""))
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "streaming unsupported", ""))
			return
		}
		minLevel := parseMinLevel(r)
		resumeSeq := resumeFrom(r)

		// Subscribe first so records arriving during backfill are
		// buffered; the Seq filter below dedupes the overlap.
		ch, cancel := svc.Subscribe(minLevel)
		defer cancel()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// Disable proxy buffering (nginx) so events arrive promptly.
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		var lastSent uint64
		for _, rec := range svc.Since(resumeSeq, minLevel) {
			writeLogEvent(w, rec)
			lastSent = rec.Seq
		}
		flusher.Flush()

		ctx := r.Context()
		ticker := time.NewTicker(logStreamHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			case rec, open := <-ch:
				if !open {
					return
				}
				if rec.Seq <= lastSent {
					continue // already delivered via backfill
				}
				writeLogEvent(w, rec)
				lastSent = rec.Seq
				flusher.Flush()
			}
		}
	}
}

// resumeFrom resolves the resume cursor from the `since` query
// parameter or the `Last-Event-ID` header (query takes precedence).
func resumeFrom(r *http.Request) uint64 {
	if raw := r.URL.Query().Get("since"); raw != "" {
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
			return n
		}
	}
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// writeLogEvent renders one SSE event with the record Seq as its id.
func writeLogEvent(w http.ResponseWriter, rec hmlog.LogRecord) {
	payload, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "id: %d\nevent: log\ndata: %s\n\n", rec.Seq, payload)
}

// LogDefaultLevelResponse is the body of `GET/PUT
// /api/v1/diagnostics/log-level`.
type LogDefaultLevelResponse struct {
	Default string `json:"default"`
}

// LogDefaultLevelRequest is the body of `PUT /api/v1/diagnostics/log-level`.
type LogDefaultLevelRequest struct {
	Level string `json:"level"`
}

// GetDefaultLogLevel returns the current global default log level.
func GetDefaultLogLevel(svc LogDefaultLevelService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "log-levels service unwired", ""))
			return
		}
		JSON(w, http.StatusOK, LogDefaultLevelResponse{Default: hmlog.FormatLevel(svc.Default())})
	}
}

// PutDefaultLogLevel changes the global default log level (R1, the
// log-viewer level dropdown). Per-subsystem overrides are untouched.
func PutDefaultLogLevel(svc LogDefaultLevelService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "log-levels service unwired", ""))
			return
		}
		var req LogDefaultLevelRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "invalid body", err.Error()))
			return
		}
		level, err := hmlog.ParseLevel(req.Level)
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "invalid level", err.Error()))
			return
		}
		svc.SetDefault(level)
		if rec != nil {
			rec.Record(audit.Entry{
				User:   identityFromCtx(r.Context()),
				Action: audit.Action("logging.default_level_set"),
				Note:   "level=" + hmlog.FormatLevel(level),
			})
		}
		JSON(w, http.StatusOK, LogDefaultLevelResponse{Default: hmlog.FormatLevel(level)})
	}
}
