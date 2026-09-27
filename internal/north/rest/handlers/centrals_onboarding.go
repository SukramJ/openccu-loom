// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// CentralOnboarding identifies a system before it is added and pairs the
// daemon with an openccu-lite box. *adapter.LiteOnboarding satisfies it.
// The token an approved pairing yields is taken by the central create or
// update that names the pairing, and never reaches a client.
type CentralOnboarding interface {
	Probe(ctx context.Context, in hmapi.CentralProbeRequest) (hmapi.CentralProbeResult, error)
	StartPairing(ctx context.Context, in hmapi.CentralPairingRequest) (hmapi.CentralPairingStarted, error)
	PairingStatus(ctx context.Context, id string, wait time.Duration) (hmapi.CentralPairingStatus, error)
	CancelPairing(ctx context.Context, id string) error
	PairingToken(id string) (token, fingerprint string, err error)
	ForgetPairing(id string)
}

// onboardingGate decides whether an onboarding route may run. The admin
// routes are gated by the router; the setup routes gate themselves on the
// first-run state, like POST /setup.
type onboardingGate func(w http.ResponseWriter, r *http.Request) bool

// setupOnboardingGate opens the setup variants only while first-run setup
// is required and allowed.
func setupOnboardingGate(s *SetupService) onboardingGate {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if s == nil {
			problem.Write(w, http.StatusServiceUnavailable, problem.New(problem.TypeServiceUnready, r, "Setup unavailable", ""))
			return false
		}
		if s.FirstRunAllowed != nil && !s.FirstRunAllowed() {
			problem.Write(w, http.StatusForbidden, problem.New(problem.TypeForbidden, r, "First-run setup disabled",
				"bootstrap.allow_first_run_setup is false"))
			return false
		}
		if !setupRequired(r.Context(), s) {
			problem.Write(w, http.StatusConflict, problem.New(problem.TypeConflict, r, "Setup already completed",
				"an authentication source already exists"))
			return false
		}
		return true
	}
}

func openGate(http.ResponseWriter, *http.Request) bool { return true }

// ProbeCentral serves POST /centrals/probe: what answers at an address.
func ProbeCentral(o CentralOnboarding) http.HandlerFunc { return probeCentral(o, openGate) }

// SetupProbeCentral serves POST /setup/probe, open only during first-run
// setup.
func SetupProbeCentral(o CentralOnboarding, s *SetupService) http.HandlerFunc {
	return probeCentral(o, setupOnboardingGate(s))
}

func probeCentral(o CentralOnboarding, gate onboardingGate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !onboardingReady(w, r, o) || !gate(w, r) {
			return
		}
		var req hmapi.CentralProbeRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err), problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		res, err := o.Probe(r.Context(), req)
		if err != nil {
			writeOnboardingError(w, r, "Probe failed", err)
			return
		}
		JSON(w, http.StatusOK, res)
	}
}

// StartCentralPairing serves POST /centrals/pairing.
func StartCentralPairing(o CentralOnboarding) http.HandlerFunc { return startPairing(o, openGate) }

// SetupStartCentralPairing serves POST /setup/pairing.
func SetupStartCentralPairing(o CentralOnboarding, s *SetupService) http.HandlerFunc {
	return startPairing(o, setupOnboardingGate(s))
}

func startPairing(o CentralOnboarding, gate onboardingGate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !onboardingReady(w, r, o) || !gate(w, r) {
			return
		}
		var req hmapi.CentralPairingRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err), problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		res, err := o.StartPairing(r.Context(), req)
		if err != nil {
			writeOnboardingError(w, r, "Pairing failed", err)
			return
		}
		JSON(w, http.StatusCreated, res)
	}
}

// CentralPairingStatus serves GET /centrals/pairing/{id}; `?wait=<s>`
// long-polls up to 25 s.
func CentralPairingStatus(o CentralOnboarding) http.HandlerFunc { return pairingStatus(o, openGate) }

// SetupCentralPairingStatus serves GET /setup/pairing/{id}.
func SetupCentralPairingStatus(o CentralOnboarding, s *SetupService) http.HandlerFunc {
	return pairingStatus(o, setupOnboardingGate(s))
}

func pairingStatus(o CentralOnboarding, gate onboardingGate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !onboardingReady(w, r, o) || !gate(w, r) {
			return
		}
		wait := time.Duration(0)
		if v := r.URL.Query().Get("wait"); v != "" {
			secs, err := strconv.Atoi(v)
			if err != nil || secs < 0 {
				problem.Write(w, http.StatusBadRequest, problem.New(problem.TypeBadRequest, r, "Invalid wait", v))
				return
			}
			wait = time.Duration(secs) * time.Second
		}
		res, err := o.PairingStatus(r.Context(), chi.URLParam(r, "id"), wait)
		if err != nil {
			writeOnboardingError(w, r, "Pairing status failed", err)
			return
		}
		JSON(w, http.StatusOK, res)
	}
}

// CancelCentralPairing serves DELETE /centrals/pairing/{id}.
func CancelCentralPairing(o CentralOnboarding) http.HandlerFunc { return cancelPairing(o, openGate) }

// SetupCancelCentralPairing serves DELETE /setup/pairing/{id}.
func SetupCancelCentralPairing(o CentralOnboarding, s *SetupService) http.HandlerFunc {
	return cancelPairing(o, setupOnboardingGate(s))
}

func cancelPairing(o CentralOnboarding, gate onboardingGate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !onboardingReady(w, r, o) || !gate(w, r) {
			return
		}
		if err := o.CancelPairing(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeOnboardingError(w, r, "Pairing cancel failed", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func onboardingReady(w http.ResponseWriter, r *http.Request, o CentralOnboarding) bool {
	if o == nil {
		problem.Write(w, http.StatusServiceUnavailable, problem.New(problem.TypeServiceUnready, r, "Onboarding unavailable", ""))
		return false
	}
	return true
}

// writeOnboardingError maps an onboarding failure: bad input is 422, an
// unknown pairing 404, a pairing not yet approved 409, a system that
// cannot be reached 502.
func writeOnboardingError(w http.ResponseWriter, r *http.Request, title string, err error) {
	switch {
	case errors.Is(err, hmerr.ErrValidation):
		problem.Write(w, http.StatusUnprocessableEntity, problem.New(problem.TypeValidation, r, title, err.Error()))
	case errors.Is(err, hmerr.ErrPairingNotFound):
		problem.Write(w, http.StatusNotFound, problem.New(problem.TypeNotFound, r, "Pairing not found", ""))
	case errors.Is(err, hmerr.ErrPermissionDenied):
		problem.Write(w, http.StatusForbidden, problem.New(problem.TypeForbidden, r, title, err.Error()))
	case errors.Is(err, hmerr.ErrPairingNotApproved):
		problem.Write(w, http.StatusConflict, problem.New(problem.TypeConflict, r, "Pairing not approved", ""))
	default:
		writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, title, err)
	}
}
