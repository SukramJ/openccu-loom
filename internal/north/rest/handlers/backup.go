// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// BackupEntry is an alias for the canonical DTO in pkg/hmapi.
type BackupEntry = hmapi.BackupEntry

// BackupService is an alias for the canonical interface in pkg/interfaces.
type BackupService = interfaces.BackupService

// triggerBackupRequest is the optional JSON body for `POST /backups`. An
// absent body or an empty central_name backs up the first registered
// central (backward-compatible default); an explicit central_name backs
// up exactly that central via [interfaces.BackupService.TriggerBackupForCentral]
// — the multi-CCU-correct path (see ADR 0002).
type triggerBackupRequest struct {
	CentralName string `json:"central_name,omitempty"`
}

// decodeOptionalJSON decodes an optional JSON body into v. A missing or
// empty body is not an error — v is left at its zero value. Any other
// decode failure (malformed JSON, unknown fields, oversized body) is
// returned unchanged so the caller can map it to the right HTTP status.
func decodeOptionalJSON(r *http.Request, v any) error {
	if r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	if err := DecodeJSON(r, v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// TriggerBackup kicks off a CCU backup and returns `202 Accepted`
// with the job id. An optional JSON body `{"central_name": "..."}`
// selects the target central explicitly; omitted defaults to the first
// registered central for backward compatibility.
func TriggerBackup(svc BackupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Backup service unavailable", ""))
			return
		}
		var req triggerBackupRequest
		if err := decodeOptionalJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", ""))
			return
		}
		var (
			id  string
			err error
		)
		if req.CentralName != "" {
			id, err = svc.TriggerBackupForCentral(r.Context(), req.CentralName)
		} else {
			id, err = svc.TriggerBackup(r.Context())
		}
		if err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Backup trigger failed", err)
			return
		}
		w.Header().Set("Location", "/api/v1/backups/"+id)
		JSON(w, http.StatusAccepted, map[string]string{"id": id})
	}
}

// ListBackups renders every locally-stored backup.
func ListBackups(svc BackupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			JSON(w, http.StatusOK, []BackupEntry{})
			return
		}
		list, err := svc.List(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Backup list failed", err)
			return
		}
		JSON(w, http.StatusOK, list)
	}
}

// RestoreBackup re-installs a previously taken backup on the CCU.
func RestoreBackup(svc BackupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Backup service unavailable", ""))
			return
		}
		id := chi.URLParam(r, "id")
		jobID, err := svc.Restore(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Backup restore failed", err)
			return
		}
		JSON(w, http.StatusAccepted, map[string]string{"id": jobID})
	}
}

// DownloadBackup streams one backup .sbk file.
func DownloadBackup(svc BackupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Backup service unavailable", ""))
			return
		}
		id := chi.URLParam(r, "id")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", ContentDispositionAttachment(id+".sbk"))
		if err := svc.Stream(r.Context(), id, w); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Backup stream failed", err)
		}
	}
}
