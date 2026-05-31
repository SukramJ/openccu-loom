// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package handlers is the per-resource REST handler set. Each
// handler file declares a narrow facade interface for the domain it
// touches; the router wires the concrete central/hub/device
// objects on startup.
package handlers

import (
	"encoding/json"
	"net/http"
)

// JSON renders v as JSON with an explicit status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// DecodeJSON parses the request body into v, returning an error when
// the payload is malformed. Caller is responsible for calling
// `r.Body.Close()` via the framework; this helper only reads.
func DecodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
