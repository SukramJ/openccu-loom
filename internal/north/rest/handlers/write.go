// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package handlers is the per-resource REST handler set. Each
// handler file declares a narrow facade interface for the domain it
// touches; the router wires the concrete central/hub/device
// objects on startup.
package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// maxRequestBodyBytes is the ceiling applied to every JSON request
// body by [DecodeJSON]. 1 MiB covers all legitimate operator
// payloads; exceeding it causes [DecodeJSON] to return a
// *http.MaxBytesError that callers should surface as a 400 or 413
// problem rather than allocating unbounded heap.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// IsBodyTooLargeError reports whether err was produced by
// http.MaxBytesReader when the body exceeded [maxRequestBodyBytes].
// Handlers that want to return 413 instead of the generic 400 can
// check with this predicate before calling problem.Write.
func IsBodyTooLargeError(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

// DecodeJSONStatus maps a [DecodeJSON] error to the HTTP status a
// handler should report: 413 when the body exceeded
// [maxRequestBodyBytes] ([IsBodyTooLargeError]), 400 for every other
// decode failure (malformed JSON, unknown field, validation-only nil
// error). Handlers pass this instead of a hard-coded
// http.StatusBadRequest so an oversized payload is reported as a
// memory-safety rejection rather than a generic client error.
func DecodeJSONStatus(err error) int {
	if IsBodyTooLargeError(err) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

// writeServerError logs the real failure via slog.Default() (never
// surfaced to the caller) and writes a problem+json body carrying
// only the static title as detail. Every 5xx problem response must go
// through this helper instead of embedding err.Error() directly:
// driver-specific SQL text, filesystem paths, and internal stack
// context are operator-only diagnostics, not something an
// unauthenticated or lower-privileged caller should see. 4xx
// validation failures are unaffected — those details stay put.
func writeServerError(w http.ResponseWriter, r *http.Request, status int, pType problem.Type, title string, err error) {
	slog.Default().ErrorContext(r.Context(), title, "error", err, "method", r.Method, "path", r.URL.Path)
	problem.Write(w, status, problem.New(pType, r, title, ""))
}

// ContentDispositionAttachment builds a `Content-Disposition:
// attachment; filename=...` header value for filename via
// [mime.FormatMediaType]. Handlers that stream a download must use
// this instead of hand-splicing the filename into the header string:
// an unescaped quote or semicolon in filename (backup/capture ids and
// device models ultimately trace back to CCU- or caller-influenced
// data) would let the value break out of the filename parameter and
// inject additional Content-Disposition directives.
func ContentDispositionAttachment(filename string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": filename})
}

// JSON renders v as JSON with an explicit status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// DecodeJSON parses the request body into v, returning an error when
// the payload is malformed or exceeds [maxRequestBodyBytes].
// Caller is responsible for calling `r.Body.Close()` via the
// framework; this helper only reads.
//
// Oversized bodies return *http.MaxBytesError; use [IsBodyTooLargeError]
// to detect that case and emit a 413 response.
func DecodeJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxRequestBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
