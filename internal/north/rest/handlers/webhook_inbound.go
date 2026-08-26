// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// WebhookValueRequest is the body of POST /api/v1/webhook/value. Central is
// optional — device addresses are globally unique, so the writer resolves the
// owning CCU from Address. Priority is optional (defaults like a normal write).
type WebhookValueRequest struct {
	Central   string `json:"central,omitempty"`
	Address   string `json:"address"`
	Parameter string `json:"parameter"`
	Value     any    `json:"value"`
	Priority  string `json:"priority,omitempty"`
}

// WebhookProgramRequest is the body of POST /api/v1/webhook/program. Central
// is optional only when unambiguous — program IDs are not unique across CCUs,
// so a multi-CCU daemon requires it (400 otherwise).
type WebhookProgramRequest struct {
	Central string `json:"central,omitempty"`
	Program string `json:"program"`
}

// InboundWebhookAuth gates the inbound webhook routes: a request passes when it
// already carries an authenticated operator identity (the normal REST auth
// chain, resolved upstream) OR presents the configured inbound bearer token.
// The token path lets a header-only caller (e.g. a doorbell) POST without a
// session or user login. An empty token disables the token path, leaving only
// the normal chain. The token compare is constant-time.
//
// These are real device writes / program runs, so the bar is operator-grade —
// the same as the equivalent REST endpoints.
func InboundWebhookAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id, ok := auth.IdentityFrom(r.Context()); ok && id.HasRole(auth.RoleOperator) {
				next.ServeHTTP(w, r)
				return
			}
			if token != "" {
				if bearer := bearerToken(r); bearer != "" &&
					subtle.ConstantTimeCompare([]byte(bearer), []byte(token)) == 1 {
					next.ServeHTTP(w, r)
					return
				}
			}
			w.Header().Set("WWW-Authenticate", `Bearer`)
			problem.Write(w, http.StatusUnauthorized,
				problem.New(problem.TypeUnauthorized, r, "Authentication required",
					"operator identity or a valid inbound token is required"))
		})
	}
}

// bearerToken extracts the token from an `Authorization: Bearer <token>`
// header, or "" when absent.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
}

// WebhookInboundValue handles POST /api/v1/webhook/value: set a datapoint
// value from an external system. Reuses the same writer the REST/MCP write
// paths use; the value is coerced against the descriptor inside the writer.
func WebhookInboundValue(writer DataPointWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if writer == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Writer unavailable", "no writer wired"))
			return
		}
		var req WebhookValueRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if strings.TrimSpace(req.Address) == "" || strings.TrimSpace(req.Parameter) == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "address and parameter are required", ""))
			return
		}
		prio := parsePriority(req.Priority)
		if err := writer.SetValue(r.Context(), strings.TrimSpace(req.Address),
			hmenum.Parameter(strings.TrimSpace(req.Parameter)), req.Value, prio); err != nil {
			if problem.IsUpstreamUnavailable(err) {
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Upstream temporarily unavailable", err)
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Set failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// WebhookInboundProgram handles POST /api/v1/webhook/program: run a CCU
// program from an external system. Reuses the hub program path; requires the
// central name when more than one CCU is configured.
func WebhookInboundProgram(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		var req WebhookProgramRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if strings.TrimSpace(req.Program) == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "program is required", ""))
			return
		}
		h := resolveHubForMutation(idx, strings.TrimSpace(req.Central))
		if h == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central required (multiple CCUs)", ""))
			return
		}
		p, ok := h.Program(strings.TrimSpace(req.Program))
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Program not found", req.Program))
			return
		}
		if err := p.Execute(r.Context()); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Execute failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
