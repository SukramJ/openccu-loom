// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// RPCRecorderService is the facade the RPC-session-recording endpoints depend
// on. It activates/deactivates the per-central [session.Recorder] (which
// captures XML/JSON-RPC call→response pairs for deterministic golden replay)
// and exports the recorded trace.
// *adapter.RPCRecorderAdapter satisfies it.
type RPCRecorderService interface {
	// Start activates the recorder on the named centrals (empty = all),
	// bounded by durationSeconds (0 = open, clamped to a safety cap) and
	// optionally anonymising the exported trace, returning the status.
	Start(centrals []string, durationSeconds int, randomize bool) []RPCRecordingStatus
	// Stop deactivates the recorder on the named centrals (empty = all).
	Stop(centrals []string) []RPCRecordingStatus
	// Status returns the current recorder status for every central.
	Status() []RPCRecordingStatus
	// Export serialises a central's recorded trace. format selects the shape
	// ("golden" = ordered replay slice, else the keyed map). Returns false
	// when the central is unknown.
	Export(central, format string) (any, bool)
}

// RPCRecordingStatus is one central's recorder status.
type RPCRecordingStatus struct {
	Central string `json:"central"`
	Active  bool   `json:"active"`
	// Entries is the number of distinct recorded call slots
	// (rpc_type + method + params).
	Entries int `json:"entries"`
	// EndsAt is the auto-stop deadline (RFC3339) while recording; empty when
	// idle.
	EndsAt string `json:"ends_at,omitempty"`
	// Randomize reports whether this recording's export is anonymised.
	Randomize bool `json:"randomize,omitempty"`
}

// RPCRecordingStartRequest is the body of `POST /diagnostics/rpc-recording`.
type RPCRecordingStartRequest struct {
	// Centrals scopes the recording; empty/omitted records on every CCU.
	Centrals []string `json:"centrals,omitempty"`
	// DurationSeconds bounds the recording; 0/omitted runs open (until stop)
	// and is clamped to the server's safety cap regardless.
	DurationSeconds int `json:"duration_seconds,omitempty"`
	// Randomize anonymises operator-identifying values in the exported trace.
	Randomize bool `json:"randomize,omitempty"`
}

// StartRPCRecording activates the RPC session recorder. The recorder then
// captures every CCU call/response until stopped, surviving a daemon restart.
func StartRPCRecording(svc RPCRecorderService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "RPC recorder unavailable", ""))
			return
		}
		var req RPCRecordingStartRequest
		if r.ContentLength > 0 {
			if err := DecodeJSON(r, &req); err != nil {
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
				return
			}
		}
		out := svc.Start(req.Centrals, req.DurationSeconds, req.Randomize)
		auditRecord(rec, "diagnostics.rpc_recording_start", req.Centrals)
		JSON(w, http.StatusAccepted, out)
	}
}

// StopRPCRecording deactivates the recorder. The trace stays available for
// download until the next start (which clears it) or daemon shutdown.
func StopRPCRecording(svc RPCRecorderService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "RPC recorder unavailable", ""))
			return
		}
		var req RPCRecordingStartRequest
		if r.ContentLength > 0 {
			if err := DecodeJSON(r, &req); err != nil {
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
				return
			}
		}
		out := svc.Stop(req.Centrals)
		auditRecord(rec, "diagnostics.rpc_recording_stop", req.Centrals)
		JSON(w, http.StatusOK, out)
	}
}

// ListRPCRecordings returns the recorder status for every central.
func ListRPCRecordings(svc RPCRecorderService) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if svc == nil {
			JSON(w, http.StatusOK, []RPCRecordingStatus{})
			return
		}
		JSON(w, http.StatusOK, svc.Status())
	}
}

// DownloadRPCRecording streams a central's recorded trace as JSON. The
// `central` path parameter selects the CCU.
func DownloadRPCRecording(svc RPCRecorderService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "RPC recorder unavailable", ""))
			return
		}
		central := chi.URLParam(r, "central")
		format := r.URL.Query().Get("format")
		if format != "golden" {
			format = "map"
		}
		out, ok := svc.Export(central, format)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Central not found", central))
			return
		}
		w.Header().Set("Content-Disposition",
			"attachment; filename=\"rpc-recording-"+central+"-"+format+".json\"")
		JSON(w, http.StatusOK, out)
	}
}

// auditRecord appends a recorder lifecycle entry when an audit recorder is
// wired. The central scope is carried in the Note.
func auditRecord(rec audit.Recorder, action string, centrals []string) {
	if rec == nil {
		return
	}
	note := "centrals=all"
	if len(centrals) > 0 {
		note = "centrals=" + joinComma(centrals)
	}
	rec.Record(audit.Entry{Action: audit.Action(action), Note: note})
}

func joinComma(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
