// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
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

// TriggerBackup kicks off a CCU backup and returns `202 Accepted`
// with the job id.
func TriggerBackup(svc BackupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Backup service unavailable", ""))
			return
		}
		id, err := svc.TriggerBackup(r.Context())
		if err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeUpstreamUnavailable, r, "Backup trigger failed", err.Error()))
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
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeUpstreamUnavailable, r, "Backup list failed", err.Error()))
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
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeUpstreamUnavailable, r, "Backup restore failed", err.Error()))
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
		w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.sbk"`)
		if err := svc.Stream(r.Context(), id, w); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeUpstreamUnavailable, r, "Backup stream failed", err.Error()))
		}
	}
}
