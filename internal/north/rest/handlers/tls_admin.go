// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"io"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// TLSCertService installs a new server certificate at runtime.
// *rest.CertReloader satisfies it; the certificate hot-reloads so the
// REST API and the SPA (same port) are re-secured without a restart.
type TLSCertService interface {
	// SaveAndReload validates the PEM key pair, persists it, and swaps
	// it into the live listener.
	SaveAndReload(certPEM, keyPEM []byte) error
}

// maxCertBytes caps an uploaded PEM blob.
const maxCertBytes = 1 << 20 // 1 MiB

// UploadTLSCertificate handles POST /admin/tls/certificate. The request
// is multipart/form-data with `cert` and `key` file (or text) parts,
// each PEM-encoded. Admin-gated by the router.
func UploadTLSCertificate(svc TLSCertService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "TLS not enabled", "set north.rest.tls_cert_file/tls_key_file to enable"))
			return
		}
		if err := r.ParseMultipartForm(2 * maxCertBytes); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid multipart form", err.Error()))
			return
		}
		certPEM, ok := readUploadPart(w, r, "cert")
		if !ok {
			return
		}
		keyPEM, ok := readUploadPart(w, r, "key")
		if !ok {
			return
		}
		if err := svc.SaveAndReload(certPEM, keyPEM); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Certificate rejected", err.Error()))
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:   identityFromCtx(r.Context()),
				Action: audit.ActionTLSCertUpload,
				Note:   "tls certificate replaced",
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// readUploadPart reads a PEM part by field name, accepting either a file
// upload or a plain form value. Writes a 400 and returns ok=false when
// the part is missing or too large.
func readUploadPart(w http.ResponseWriter, r *http.Request, field string) ([]byte, bool) {
	if f, _, err := r.FormFile(field); err == nil {
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(io.LimitReader(f, maxCertBytes+1))
		if err != nil || len(data) > maxCertBytes || len(data) == 0 {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid "+field+" upload", ""))
			return nil, false
		}
		return data, true
	}
	if v := r.FormValue(field); v != "" && len(v) <= maxCertBytes {
		return []byte(v), true
	}
	problem.Write(w, http.StatusBadRequest,
		problem.New(problem.TypeValidation, r, "Missing "+field+" part", ""))
	return nil, false
}
