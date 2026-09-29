// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/pairing"
)

// PairingService is the slice of internal/pairing the routes need.
type PairingService interface {
	RequestFrom(a pairing.Ask, addr string) (pairing.Answer, error)
	Poll(ctx context.Context, id, pollSecret, clientNonce string, wait time.Duration) (pairing.Result, error)
	Withdraw(id, pollSecret string) error
	Pending() []pairing.View
	Approve(ctx context.Context, id, code, by string) (pairing.View, error)
	Reject(id, by string) (pairing.View, error)
}

// pairingHost is the peer's address without the port. RemoteAddr on
// purpose — see pairing.LocalHost.
func pairingHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// pollSecretFrom reads the `Authorization: Pairing <secret>` header the
// asking client authenticates its own request with. The scheme name
// keeps it out of the Basic/Bearer resolvers' way, and a header (not a
// query parameter) keeps the secret out of access logs.
func pollSecretFrom(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if v, ok := strings.CutPrefix(h, "Pairing "); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// writePairingError maps the manager's sentinels onto problem responses
// whose `code` an asking client can branch on without parsing prose.
func writePairingError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, pairing.ErrOff):
		problem.Write(w, http.StatusServiceUnavailable,
			problem.New(problem.TypePairingOff, r, "Pairing is switched off", err.Error()))
	case errors.Is(err, pairing.ErrNotLocal):
		problem.Write(w, http.StatusForbidden,
			problem.New(problem.TypePairingNotLocal, r, "Pairing is local-network only", err.Error()))
	case errors.Is(err, pairing.ErrSlowDown):
		w.Header().Set("Retry-After", strconv.Itoa(int(pairing.Interval/time.Second)))
		problem.Write(w, http.StatusTooManyRequests,
			problem.New(problem.TypePairingSlowDown, r, "Poll less often", err.Error()))
	case errors.Is(err, pairing.ErrLimit), errors.Is(err, pairing.ErrMuted):
		problem.Write(w, http.StatusTooManyRequests,
			problem.New(problem.TypeRateLimited, r, "Too many pairing requests", err.Error()))
	case errors.Is(err, pairing.ErrUnknown):
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, r, "No such pairing request", err.Error()))
	case errors.Is(err, pairing.ErrPollAuth):
		problem.Write(w, http.StatusUnauthorized,
			problem.New(problem.TypeUnauthorized, r, "Not this request's poll secret", ""))
	case errors.Is(err, pairing.ErrNotReady):
		problem.Write(w, http.StatusConflict,
			problem.New(problem.TypeConflict, r, "The client has not revealed its code half yet", err.Error()))
	case errors.Is(err, pairing.ErrWrongCode):
		problem.Write(w, http.StatusConflict,
			problem.New(problem.TypeConflict, r, "Wrong code — the request is rejected", err.Error()))
	case errors.Is(err, pairing.ErrInvalid):
		problem.Write(w, http.StatusBadRequest,
			problem.New(problem.TypeValidation, r, "Invalid pairing request", err.Error()))
	default:
		problem.WriteFromError(w, r, err)
	}
}

// StartPairing serves POST /pairing: an unauthenticated client asks for
// a token. The protocol carries its own limits; the shared login rate
// limiter sits in front of the route.
func StartPairing(svc PairingService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writePairingError(w, r, pairing.ErrOff)
			return
		}
		var ask pairing.Ask
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&ask); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid body", err.Error()))
			return
		}
		ans, err := svc.RequestFrom(ask, pairingHost(r))
		if err != nil {
			writePairingError(w, r, err)
			return
		}
		JSON(w, http.StatusAccepted, ans)
	}
}

// PollPairing serves GET /pairing/{id}?client_nonce=&wait= with the
// Pairing authorization header. wait long-polls, capped by the manager.
func PollPairing(svc PairingService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writePairingError(w, r, pairing.ErrOff)
			return
		}
		wait := 0
		if v := r.URL.Query().Get("wait"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeValidation, r, "Invalid wait", "wait is seconds"))
				return
			}
			wait = n
		}
		res, err := svc.Poll(r.Context(), chi.URLParam(r, "id"), pollSecretFrom(r),
			r.URL.Query().Get("client_nonce"), time.Duration(wait)*time.Second)
		if err != nil {
			writePairingError(w, r, err)
			return
		}
		JSON(w, http.StatusOK, res)
	}
}

// WithdrawPairing serves DELETE /pairing/{id}: the asking client gives
// up, authenticated by its poll secret.
func WithdrawPairing(svc PairingService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writePairingError(w, r, pairing.ErrOff)
			return
		}
		if err := svc.Withdraw(chi.URLParam(r, "id"), pollSecretFrom(r)); err != nil {
			writePairingError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type pairingRequestsResponse struct {
	Items []pairing.View `json:"items"`
}

// ListPairingRequests serves the admin card: GET /pairing-requests.
func ListPairingRequests(svc PairingService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			JSON(w, http.StatusOK, pairingRequestsResponse{Items: []pairing.View{}})
			return
		}
		JSON(w, http.StatusOK, pairingRequestsResponse{Items: svc.Pending()})
	}
}

type pairingApproveRequest struct {
	// Code is the six digits the administrator read on the asking
	// client and types here — the comparison IS the authentication.
	Code string `json:"code"`
}

// ApprovePairing serves POST /pairing-requests/{id}/approve.
func ApprovePairing(svc PairingService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writePairingError(w, r, pairing.ErrOff)
			return
		}
		var req pairingApproveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid body", err.Error()))
			return
		}
		by := identityFromCtx(r.Context())
		view, err := svc.Approve(r.Context(), chi.URLParam(r, "id"), req.Code, by)
		if rec != nil {
			action, note := audit.ActionPairingApprove, "app="+view.App+" instance="+view.Instance+" address="+view.Address+" role="+view.Role
			if err != nil {
				action, note = audit.ActionPairingReject, "reason="+err.Error()
			}
			rec.Record(audit.Entry{User: by, Action: action, Note: note})
		}
		if err != nil {
			writePairingError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// RejectPairing serves POST /pairing-requests/{id}/reject.
func RejectPairing(svc PairingService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writePairingError(w, r, pairing.ErrOff)
			return
		}
		by := identityFromCtx(r.Context())
		view, err := svc.Reject(chi.URLParam(r, "id"), by)
		if err != nil {
			writePairingError(w, r, err)
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User: by, Action: audit.ActionPairingReject,
				Note: "app=" + view.App + " instance=" + view.Instance + " address=" + view.Address,
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
