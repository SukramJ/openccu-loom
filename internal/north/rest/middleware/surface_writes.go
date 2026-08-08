// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"net/http"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/north/ui/surface"
)

// SurfacePolicy is the live view of the surface profile the middleware
// consults. Declared here rather than imported as a concrete type so
// tests can substitute a fixed answer.
type SurfacePolicy interface {
	// RefusedBy returns the surface whose hidden state refuses this
	// request, or "" when the request is not gated.
	RefusedBy(method, path string) surface.ID
}

// SurfaceWrites refuses writes that the live surface profile gates.
//
// It applies to ONE identity: the Home Assistant Ingress passthrough
// (ADR 0044), which is not a Loom credential but a trust assertion the
// Supervisor makes on behalf of an HA admin. In the embedded profile the
// operator decides through the profile which surfaces that assertion
// may write to — hiding the Configure tab stops Home Assistant writing
// paramsets, showing it hands the write back (ADR 0051, and
// notes/concepts/ui-surface-profiles.md §2.8).
//
// Everything else passes untouched, and that is the point: a Bearer
// token and a Loom account carry the rights they were granted, so a
// navigation switch can never widen or narrow them. Reads are never
// gated — the HA panel reads what it no longer edits.
func SurfaceWrites(policy SurfacePolicy, apiPrefix string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if policy == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isWriteMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			id, ok := auth.IdentityFrom(r.Context())
			if !ok || id.Scheme != auth.SchemeIngress {
				next.ServeHTTP(w, r)
				return
			}
			path := strings.TrimPrefix(r.URL.Path, apiPrefix)
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			if refusedBy := policy.RefusedBy(r.Method, path); refusedBy != "" {
				problem.Write(w, http.StatusForbidden, problem.New(problem.TypeForbidden, r,
					"Surface hidden for Home Assistant",
					"This daemon runs in embedded mode and the surface "+string(refusedBy)+
						" is hidden, so Home Assistant may not write here. "+
						"Enable it under Settings → Navigation & views, or make the change in Home Assistant."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isWriteMethod(m string) bool {
	switch strings.ToUpper(m) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
