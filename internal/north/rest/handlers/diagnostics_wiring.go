// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/wiring"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// WiringManifestReader is the read side of the composition root's
// wiring manifest. *central.Registry satisfies it via Manifest().
//
// loom:reachable:reason="the declared type of rest.Deps.WiringManifest, filled at cmd/openccu-loom/daemon_rest_mount.go with central.Registry.Manifest(); an interface reached only by assignment"
type WiringManifestReader interface {
	Seams() []wiring.Seam
}

// DiagnosticsWiring serves GET /diagnostics/wiring — the seams the
// running daemon declared as it wired them (ADR 0065).
//
// A nil reader answers with an empty list rather than 503. The list
// being empty is itself the finding this endpoint exists to surface:
// "the daemon wired none of these" and "the daemon cannot tell you what
// it wired" would otherwise be two different HTTP statuses for the same
// operator question, and only one of them is checkable from a test.
func DiagnosticsWiring(reader WiringManifestReader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		out := []hmapi.WiringSeam{}
		if reader != nil {
			for _, s := range reader.Seams() {
				out = append(out, hmapi.WiringSeam{
					Name:         s.Name,
					Collaborator: s.Collaborator,
					Phase:        string(s.Phase),
					Why:          s.Why,
				})
			}
		}
		JSON(w, http.StatusOK, out)
	}
}
