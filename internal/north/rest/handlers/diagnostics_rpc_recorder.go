// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// RPCRecorderService is an alias for the canonical interface in pkg/interfaces.
type RPCRecorderService = interfaces.RPCRecorderService

// RPCRecordingStatus is an alias for the canonical DTO in pkg/hmapi.
type RPCRecordingStatus = hmapi.RPCRecordingStatus

// Audit actions for the RPC-recorder lifecycle. Starting a recorder
// changes what the daemon captures about its own CCU traffic, which is
// why it is audited alongside the other operator-diagnostics verbs. The
// dotted strings are the values already written to the audit store, kept
// verbatim so existing rows keep resolving.
const (
	actionRPCRecordingStart audit.Action = "diagnostics.rpc_recording_start"
	actionRPCRecordingStop  audit.Action = "diagnostics.rpc_recording_stop"
)

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
				problem.Write(w, DecodeJSONStatus(err),
					problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
				return
			}
		}
		out := svc.Start(req.Centrals, req.DurationSeconds, req.Randomize)
		auditRecord(rec, r, actionRPCRecordingStart, req.Centrals)
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
				problem.Write(w, DecodeJSONStatus(err),
					problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
				return
			}
		}
		out := svc.Stop(req.Centrals)
		auditRecord(rec, r, actionRPCRecordingStop, req.Centrals)
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
		centralName := chi.URLParam(r, "central")
		format := r.URL.Query().Get("format")
		if format != "golden" {
			format = "map"
		}
		out, ok := svc.Export(centralName, format)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Central not found", centralName))
			return
		}
		w.Header().Set("Content-Disposition",
			"attachment; filename=\"rpc-recording-"+centralName+"-"+format+".json\"")
		JSON(w, http.StatusOK, out)
	}
}

// auditRecord appends a recorder lifecycle entry when an audit recorder is
// wired. The requesting operator is stamped as the actor and the central
// scope is carried in the Note.
func auditRecord(rec audit.Recorder, r *http.Request, action audit.Action, centrals []string) {
	if rec == nil {
		return
	}
	note := "centrals=all"
	if len(centrals) > 0 {
		note = "centrals=" + joinComma(centrals)
	}
	rec.Record(audit.Entry{
		User:   identityFromCtx(r.Context()),
		Action: action,
		Note:   note,
	})
}

func joinComma(s []string) string {
	var out strings.Builder
	for i, v := range s {
		if i > 0 {
			out.WriteString(",")
		}
		out.WriteString(v)
	}
	return out.String()
}
