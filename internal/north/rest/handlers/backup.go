// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// BackupEntry is one entry in the backup list.
type BackupEntry struct {
	ID        string    `json:"id"`
	Central   string    `json:"central"`
	Bytes     int64     `json:"bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// BackupService is the facade the backup endpoints need.
type BackupService interface {
	TriggerBackup(ctx context.Context) (string, error) // returns job id
	List(ctx context.Context) ([]BackupEntry, error)
	Stream(ctx context.Context, id string, w io.Writer) error
	// Restore reinstalls a previously taken backup on the CCU. The
	// returned id is the (re-used) backup id so the caller can poll
	// for completion via the same job-tracking endpoints.
	Restore(ctx context.Context, id string) (string, error)
}

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
				problem.New(problem.TypeInternal, r, "Backup trigger failed", err.Error()))
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
				problem.New(problem.TypeInternal, r, "Backup list failed", err.Error()))
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
				problem.New(problem.TypeInternal, r, "Backup restore failed", err.Error()))
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
				problem.New(problem.TypeInternal, r, "Backup stream failed", err.Error()))
		}
	}
}
