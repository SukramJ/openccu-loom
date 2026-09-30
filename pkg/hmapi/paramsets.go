// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmapi

// ParamsetWriteResult is the response body of a MASTER or LINK paramset
// write (`PUT /devices/{addr}/paramsets/{key}`, `PUT /devices/{addr}/link-ps/{peer}`).
// After a configuration write the daemon re-reads the stored paramset and
// compares it against what was sent, because an interface process may answer
// ok and still drop, clamp or coerce values it does not apply. An empty
// `readback_divergences` with no `readback_error` means every sent value is
// stored as sent.
type ParamsetWriteResult struct {
	// Written lists the parameter names that were sent to the CCU, sorted.
	Written []string `json:"written"`
	// ReadbackDivergences lists every parameter whose stored value after
	// the write differs from the sent value.
	ReadbackDivergences []ParamsetDivergence `json:"readback_divergences"`
	// ReadbackError is set when the post-write read failed; the
	// divergences are unknown in that case, not empty.
	ReadbackError string `json:"readback_error,omitempty"`
}

// ParamsetDivergence names one parameter whose stored value after a
// configuration write differs from the value that was sent.
type ParamsetDivergence struct {
	Parameter string `json:"parameter"`
	Sent      any    `json:"sent"`
	// Stored is null when the parameter is absent from the stored paramset.
	Stored any `json:"stored"`
}
