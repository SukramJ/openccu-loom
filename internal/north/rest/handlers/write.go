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
	"net/http"
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
