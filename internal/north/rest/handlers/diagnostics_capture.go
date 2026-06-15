// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/diagnostics"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// CaptureService is the narrow facade the capture endpoints depend
// on. *diagnostics.Manager satisfies it.
type CaptureService interface {
	Start(opts diagnostics.StartOptions) (diagnostics.Summary, error)
	Stop(id string) (diagnostics.Summary, error)
	List() []diagnostics.Summary
	Get(id string) (diagnostics.Summary, error)
	OpenArchive(id string) ([]byte, error)
}

// CaptureStartRequest is the body of `POST /api/v1/diagnostics/capture`.
type CaptureStartRequest struct {
	DurationSeconds int               `json:"duration_seconds,omitempty"`
	LogLevels       map[string]string `json:"log_levels,omitempty"`
	Anonymise       *bool             `json:"anonymise,omitempty"`
}

// StartCapture begins a new capture session. Returns 202 + summary;
// the operator polls `GET .../capture/{id}` to follow progress.
func StartCapture(svc CaptureService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "capture service unwired", ""))
			return
		}
		var req CaptureStartRequest
		if r.ContentLength > 0 {
			if err := DecodeJSON(r, &req); err != nil {
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeBadRequest, r, "invalid body", err.Error()))
				return
			}
		}
		anon := true
		if req.Anonymise != nil {
			anon = *req.Anonymise
		}
		opts := diagnostics.StartOptions{
			Duration:          time.Duration(req.DurationSeconds) * time.Second,
			LogLevelOverrides: req.LogLevels,
			Anonymise:         anon,
		}
		summary, err := svc.Start(opts)
		if err != nil {
			switch {
			case errors.Is(err, diagnostics.ErrCaptureBusy):
				problem.Write(w, http.StatusConflict,
					problem.New(problem.TypeConflict, r, "capture already running", ""))
			case errors.Is(err, diagnostics.ErrCaptureDurationTooLong):
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeBadRequest, r, "duration exceeds 30-minute cap", ""))
			default:
				problem.Write(w, http.StatusInternalServerError,
					problem.New(problem.TypeInternal, r, "capture start failed", err.Error()))
			}
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				Action: audit.Action("diagnostics.capture_start"),
				Note:   summary.ID,
			})
		}
		JSON(w, http.StatusAccepted, summary)
	}
}

// StopCapture stops the active capture (or the supplied id) and
// finalises the archive.
func StopCapture(svc CaptureService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "capture service unwired", ""))
			return
		}
		id := chi.URLParam(r, "id")
		summary, err := svc.Stop(id)
		if err != nil {
			switch {
			case errors.Is(err, diagnostics.ErrCaptureNotActive):
				problem.Write(w, http.StatusConflict,
					problem.New(problem.TypeConflict, r, "no active capture", ""))
			case errors.Is(err, diagnostics.ErrCaptureNotFound):
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "capture id not found", id))
			default:
				problem.Write(w, http.StatusInternalServerError,
					problem.New(problem.TypeInternal, r, "capture stop failed", err.Error()))
			}
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				Action: audit.Action("diagnostics.capture_stop"),
				Note:   summary.ID,
			})
		}
		JSON(w, http.StatusOK, summary)
	}
}

// ListCaptures lists every known capture (active + archived).
func ListCaptures(svc CaptureService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			JSON(w, http.StatusOK, []diagnostics.Summary{})
			return
		}
		JSON(w, http.StatusOK, svc.List())
	}
}

// GetCapture returns a single capture's status.
func GetCapture(svc CaptureService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "capture service unwired", ""))
			return
		}
		id := chi.URLParam(r, "id")
		summary, err := svc.Get(id)
		if err != nil {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "capture id not found", id))
			return
		}
		JSON(w, http.StatusOK, summary)
	}
}

// DownloadCapture streams the tar.gz archive for a finished capture.
func DownloadCapture(svc CaptureService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "capture service unwired", ""))
			return
		}
		id := chi.URLParam(r, "id")
		data, err := svc.OpenArchive(id)
		if err != nil {
			switch {
			case errors.Is(err, diagnostics.ErrCaptureNotFound):
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "capture id not found", id))
			case errors.Is(err, diagnostics.ErrCaptureNotActive):
				problem.Write(w, http.StatusConflict,
					problem.New(problem.TypeConflict, r, "capture still running", ""))
			default:
				problem.Write(w, http.StatusInternalServerError,
					problem.New(problem.TypeInternal, r, "capture archive unavailable", err.Error()))
			}
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.tar.gz"`)
		// data is a gzip-wrapped tar produced by the diagnostics
		// manager; never reflects user-controlled HTML. The gosec
		// XSS warning is a false positive on a binary download.
		_, _ = w.Write(data) //nolint:gosec // tar.gz binary, not user-controlled HTML; see #20
	}
}
