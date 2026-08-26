// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// RecordingOverrideService is the per-datapoint recording-toggle surface
// the SPA history tab consumes (SV10). A present override forces
// recording on/off for one (central, interface, channel, parameter)
// tuple; clearing it reverts to the parameter-name glob policy.
type RecordingOverrideService interface {
	// Effective reports the current recording decision for a data point
	// and whether it comes from an explicit override or the glob policy.
	Effective(ctx context.Context, central, iface, channel, parameter string) (record bool, source string, err error)
	// Set forces recording on/off for a data point.
	Set(ctx context.Context, central, iface, channel, parameter string, record bool, updatedBy string) error
	// Clear removes the override, reverting the data point to the policy.
	Clear(ctx context.Context, central, iface, channel, parameter, updatedBy string) error
}

// recordingResponse is the body of GET /api/v1/history/recording.
type recordingResponse struct {
	// Record is the effective decision: whether this data point's live
	// values are currently persisted to history.
	Record bool `json:"record"`
	// Source is "override" when an explicit toggle applies, "policy" when
	// the parameter-name glob policy decides.
	Source string `json:"source"`
}

// recordingWriteRequest is the body of PUT /api/v1/history/recording.
// A null `record` clears the override (revert to policy); true/false
// forces recording on/off.
type recordingWriteRequest struct {
	Central   string `json:"central"`
	Interface string `json:"interface_id"`
	Channel   string `json:"channel"`
	Parameter string `json:"parameter"`
	Record    *bool  `json:"record"`
}

// GetRecordingOverride serves GET /api/v1/history/recording — the
// effective recording state for one data point.
func GetRecordingOverride(svc RecordingOverrideService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Recording overrides unavailable", ""))
			return
		}
		q := r.URL.Query()
		central, iface := q.Get("central"), q.Get("interface_id")
		channel, parameter := q.Get("channel"), q.Get("parameter")
		if central == "" || iface == "" || channel == "" || parameter == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r,
					"central, interface_id, channel and parameter are required", ""))
			return
		}
		record, source, err := svc.Effective(r.Context(), central, iface, channel, parameter)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		JSON(w, http.StatusOK, recordingResponse{Record: record, Source: source})
	}
}

// PutRecordingOverride serves PUT /api/v1/history/recording — set or
// clear a data point's recording override.
func PutRecordingOverride(svc RecordingOverrideService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Recording overrides unavailable", ""))
			return
		}
		var req recordingWriteRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeValidation, r, "invalid request body", ""))
			return
		}
		if req.Central == "" || req.Interface == "" || req.Channel == "" || req.Parameter == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r,
					"central, interface_id, channel and parameter are required", ""))
			return
		}
		user := identityFromCtx(r.Context())
		var (
			err  error
			note string
		)
		if req.Record == nil {
			err = svc.Clear(r.Context(), req.Central, req.Interface, req.Channel, req.Parameter, user)
			note = fmt.Sprintf("clear %s/%s/%s/%s", req.Central, req.Interface, req.Channel, req.Parameter)
		} else {
			err = svc.Set(r.Context(), req.Central, req.Interface, req.Channel, req.Parameter, *req.Record, user)
			note = fmt.Sprintf("record=%t %s/%s/%s/%s", *req.Record, req.Central, req.Interface, req.Channel, req.Parameter)
		}
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:          user,
				Action:        audit.ActionRecordingToggle,
				DeviceAddress: req.Channel,
				Note:          note,
			})
		}
		// Report the resulting effective state so the SPA can reflect it.
		record, source, effErr := svc.Effective(r.Context(), req.Central, req.Interface, req.Channel, req.Parameter)
		if effErr != nil {
			JSON(w, http.StatusOK, recordingResponse{Record: req.Record != nil && *req.Record, Source: "override"})
			return
		}
		JSON(w, http.StatusOK, recordingResponse{Record: record, Source: source})
	}
}
