// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/backup/sbk"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
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

// BackupStorageInfo renders where the daemon keeps its CCU archives.
//
// Without it an operator cannot tell where a backup went: the path comes
// from `backup.dir`, which is empty in the common case, and on a CCU
// add-on install it is written by the service script from the CCU's own
// backup target — so it is neither in the config the SPA reads nor
// reconstructible from anything else the API exposes.
func BackupStorageInfo(svc BackupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			// Not an error: a daemon without a backup service simply has no
			// storage, which is exactly what the zero value says.
			JSON(w, http.StatusOK, hmapi.BackupStorageInfo{})
			return
		}
		info, err := svc.StorageInfo(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Backup storage query failed", err)
			return
		}
		JSON(w, http.StatusOK, info)
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
		switch {
		case errors.Is(err, sbk.ErrNotAnArchive), errors.Is(err, sbk.ErrIncomplete):
			// The stored archive did not survive inspection, so nothing was
			// uploaded. 422 with the reason, mirroring the upload endpoint:
			// a 502 would blame the CCU for a file this daemon refused to
			// send it, and would hide the one action that helps — replacing
			// the archive.
			title := "Stored backup is not a CCU system backup"
			if errors.Is(err, sbk.ErrIncomplete) {
				title = "Stored backup is incomplete"
			}
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, title, err.Error()))
			return
		case errors.Is(err, hmerr.ErrRestoreTargetAmbiguous):
			// Not an upstream failure: nothing was attempted. Saying so
			// with 422 and the reason beats the 502 this used to return,
			// which told an operator the CCU had refused when in fact the
			// daemon never asked it anything.
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "Restore target is ambiguous", err.Error()))
			return
		case errors.Is(err, hmerr.ErrRestoreUnsupported):
			// 501, not 502: nothing was sent to the CCU, so calling this
			// an upstream failure blames the wrong party. It is also the
			// only way a caller — or a black-box test — can tell "the
			// daemon cannot do this" apart from "the CCU refused", which
			// is exactly the distinction that let a dead restore path go
			// unnoticed.
			writeServerError(w, r, http.StatusNotImplemented, problem.TypeServiceUnready,
				"Backup restore is not configured", err)
			return
		case err != nil:
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Backup restore failed", err)
			return
		}
		JSON(w, http.StatusAccepted, map[string]string{"id": jobID})
	}
}

// DeleteBackup removes one stored archive.
//
// A missing archive answers 204 like a present one: the caller asked for
// it to be gone, and it is. Reporting 404 here would make the SPA's retry
// after a lost response look like a failure, and would let an operator
// deleting the same entry twice believe something is wrong with the
// storage.
func DeleteBackup(svc BackupService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Backup service unavailable", ""))
			return
		}
		id := chi.URLParam(r, "id")
		if err := svc.Delete(r.Context(), id); err != nil {
			if errors.Is(err, hmerr.ErrUnsupported) {
				// No storage is a deployment state, not a fault: there is
				// nothing to delete from, and saying "internal error" would
				// send the operator hunting for a bug that is not there.
				problem.Write(w, http.StatusServiceUnavailable,
					problem.New(problem.TypeServiceUnready, r, "Backup storage unavailable", err.Error()))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Backup delete failed", err)
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:   identityFromCtx(r.Context()),
				Action: audit.ActionBackupDelete,
				Note:   id,
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// DownloadBackup streams one backup .sbk file.
//
// The 200 is committed by the first payload byte, not before it. Setting
// the download headers up front made every failure after that point
// unreportable: the status was already on the wire, so the error handler
// appended a problem+json document to a half-written archive and the
// operator's browser saved the result as a perfectly ordinary-looking
// .sbk — one that a later restore would push at a CCU.
func DownloadBackup(svc BackupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Backup service unavailable", ""))
			return
		}
		id := chi.URLParam(r, "id")
		body := &downloadBody{w: w, filename: downloadFilename(r.Context(), svc, id)}
		err := svc.Stream(r.Context(), id, body)
		if err == nil && !body.started {
			// A clean stream that produced nothing is not a download: it
			// would save as a zero-byte .sbk. Say so instead.
			err = errEmptyBackupStream
		}
		switch {
		case err == nil:
			return
		case body.started:
			// The payload is already streaming, so the status cannot be
			// taken back. Abort the connection instead: the client then
			// reports a truncated transfer rather than writing out a short
			// file that looks complete. This is net/http's only mechanism
			// for it — the server recovers the sentinel itself and logs
			// nothing further.
			slog.Default().ErrorContext(r.Context(), "Backup stream aborted mid-transfer",
				"error", err, "method", r.Method, "path", r.URL.Path)
			panic(http.ErrAbortHandler)
		default:
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Backup stream failed", err)
		}
	}
}

// downloadFilename resolves the name the archive is served under: the
// CCU-convention name recorded when it was taken, falling back to the
// storage id. The id is a key, not a name — it carries no hostname and no
// firmware version, so an operator downloading two archives from two CCUs
// could not tell from the files which CCU either belongs to.
//
// A failed or missing lookup degrades to the id rather than failing the
// download: the archive is what the operator asked for, and a name is not
// worth losing it over.
func downloadFilename(ctx context.Context, svc BackupService, id string) string {
	entries, err := svc.List(ctx)
	if err != nil {
		return id + sbk.Extension
	}
	for _, e := range entries {
		if e.ID == id && e.Filename != "" {
			return e.Filename
		}
	}
	return id + sbk.Extension
}

// errEmptyBackupStream reports a stream that completed without writing a
// byte. The filesystem storage already refuses a zero-length archive at
// Open, so this covers the backends that do not.
var errEmptyBackupStream = errors.New("backup: stream produced no data")

// downloadBody defers the download headers and the 200 until the first
// payload byte arrives, so a stream that fails before producing anything
// can still be answered with a real error status.
type downloadBody struct {
	w        http.ResponseWriter
	filename string
	// started records whether any byte has been handed to the client. It
	// is the difference between "still reportable as an error" and "the
	// response is committed".
	started bool
}

func (d *downloadBody) Write(p []byte) (int, error) {
	if len(p) == 0 {
		// Not a payload byte: writing headers here would commit the
		// response for a stream that has produced nothing yet.
		return 0, nil
	}
	if !d.started {
		d.started = true
		d.w.Header().Set("Content-Type", "application/octet-stream")
		d.w.Header().Set("Content-Disposition", ContentDispositionAttachment(d.filename))
		d.w.WriteHeader(http.StatusOK)
	}
	return d.w.Write(p)
}

// BackupUploader stores an externally-supplied CCU backup so it becomes
// restorable through the ordinary restore path. Implemented by the
// filesystem backup storage via the cmd-layer adapter.
type BackupUploader interface {
	// SaveUploaded persists data under a generated id and returns the
	// entry describing it.
	SaveUploaded(ctx context.Context, filename string, data []byte) (hmapi.BackupEntry, error)
}

// maxUploadedBackupBytes bounds an uploaded .sbk. Real archives run to a
// few tens of megabytes; 512 MiB is far above any genuine backup and
// still keeps a hostile or mistaken upload from exhausting memory. The
// body is read into memory rather than streamed to disk because the
// archive has to be inspected before it is stored — writing an
// unvalidated file first would leave junk behind on every bad upload.
const maxUploadedBackupBytes = 512 << 20

// UploadBackup handles `POST /api/v1/backups/upload`: a multipart form
// with a single `file` part carrying a CCU `.sbk` archive. The archive is
// inspected before it is stored, so an operator who picks the wrong file
// is told immediately rather than at restore time, when the CCU is
// already being wiped.
//
// The daemon cannot verify the archive's signature — that needs the CCU's
// key material — so the check is deliberately structural: a readable tar
// carrying the configuration archive and its signature. The firmware
// version the backup came from is reported back so the operator can
// compare it against the target CCU, which is exactly what the CCU's own
// restore does before deciding whether the backup is usable.
func UploadBackup(svc BackupUploader, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Backup storage unavailable", ""))
			return
		}
		reader, err := r.MultipartReader()
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Expected multipart/form-data", err.Error()))
			return
		}
		var (
			data     []byte
			filename string
		)
		for {
			part, partErr := reader.NextPart()
			if errors.Is(partErr, io.EOF) {
				break
			}
			if partErr != nil {
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeBadRequest, r, "Malformed multipart body", partErr.Error()))
				return
			}
			if part.FormName() != "file" {
				_ = part.Close()
				continue
			}
			filename = part.FileName()
			data, err = io.ReadAll(io.LimitReader(part, maxUploadedBackupBytes+1))
			_ = part.Close()
			if err != nil {
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeBadRequest, r, "Upload failed", err.Error()))
				return
			}
			break
		}
		switch {
		case len(data) == 0:
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing file part", "a `file` part carrying the .sbk archive is required"))
			return
		case len(data) > maxUploadedBackupBytes:
			problem.Write(w, http.StatusRequestEntityTooLarge,
				problem.New(problem.TypeValidation, r, "Backup too large", "the archive exceeds the accepted size"))
			return
		}
		info, err := sbk.InspectBytes(data)
		if err != nil {
			// The two failures call for different things from the
			// operator: an unreadable archive usually means the wrong file
			// was picked, while a readable one missing a member means the
			// right kind of file arrived damaged or truncated. Saying
			// which saves a round of guessing.
			title := "Not a CCU system backup"
			if errors.Is(err, sbk.ErrIncomplete) {
				title = "Incomplete CCU system backup"
			}
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, title, err.Error()))
			return
		}
		entry, err := svc.SaveUploaded(r.Context(), filename, data)
		if err != nil {
			// A storage that cannot take in archives is a deployment
			// state, not a fault: reporting it as an internal error would
			// send the operator hunting for a bug that is not there.
			if errors.Is(err, hmerr.ErrUnsupported) {
				problem.Write(w, http.StatusServiceUnavailable,
					problem.New(problem.TypeServiceUnready, r, "Backup import unavailable", err.Error()))
				return
			}
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Storing the backup failed", err)
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:   identityFromCtx(r.Context()),
				Action: audit.ActionBackupUpload,
				Note:   entry.ID,
			})
		}
		JSON(w, http.StatusCreated, uploadedBackupResponse{
			BackupEntry:     entry,
			FirmwareVersion: info.FirmwareVersion,
			Product:         info.Product,
		})
	}
}

// uploadedBackupResponse is the 201 body: the stored entry plus what the
// inspection learned about where the archive came from.
type uploadedBackupResponse struct {
	hmapi.BackupEntry
	// FirmwareVersion and Product describe the CCU that produced the
	// archive, read from its firmware_version member. Empty when the
	// archive omits it (CCU2-era backups predate it).
	FirmwareVersion string `json:"firmware_version,omitempty"`
	Product         string `json:"product,omitempty"`
}
