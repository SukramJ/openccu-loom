// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/SukramJ/openccu-loom/internal/addonupdate"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// AddonUpdateService is the domain-facing port for the CCU add-on
// self-updater (ADR 0057). *addonupdate.Updater satisfies it directly.
type AddonUpdateService interface {
	Status() addonupdate.Status
	Check(ctx context.Context) error
	// InstallAsync starts the download/verify/stage/install sequence
	// and returns once it has started, not once it has finished — see
	// [addonupdate.Updater.InstallAsync].
	InstallAsync(ctx context.Context) error
}

// AddonUpdateStatusResponse mirrors the OpenAPI AddonUpdateStatus
// schema verbatim; keep the json tags in lockstep with
// assets/openapi.yaml.
type AddonUpdateStatusResponse struct {
	Supported       bool   `json:"supported"`
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable bool   `json:"update_available,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
	LastCheck       string `json:"last_check,omitempty"`
	State           string `json:"state"`
	Error           string `json:"error,omitempty"`
}

// addonUpdateStatusResponse converts the domain snapshot to the wire
// DTO. A zero LastCheck (never checked) is omitted rather than
// serialised as the Go zero time.
func addonUpdateStatusResponse(s addonupdate.Status) AddonUpdateStatusResponse {
	resp := AddonUpdateStatusResponse{
		Supported:       s.Supported,
		CurrentVersion:  s.CurrentVersion,
		LatestVersion:   s.LatestVersion,
		UpdateAvailable: s.UpdateAvailable,
		ReleaseURL:      s.ReleaseURL,
		State:           string(s.State),
		Error:           s.Error,
	}
	if !s.LastCheck.IsZero() {
		resp.LastCheck = s.LastCheck.UTC().Format(time.RFC3339)
	}
	return resp
}

// GetAddonUpdate returns the add-on self-update status snapshot. Per
// the OpenAPI contract this ALWAYS answers 200 — a nil/unwired service
// (the platform capability probe failed, or this is not an add-on
// build at all) reports a minimal `{"supported":false,...}` body
// rather than a 404, so the SPA can gate its card on the `supported`
// field / the `addon_self_update` info capability without a probe
// request telling it apart from a genuinely broken route.
func GetAddonUpdate(svc AddonUpdateService) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if svc == nil {
			JSON(w, http.StatusOK, AddonUpdateStatusResponse{State: string(addonupdate.StateIdle)})
			return
		}
		JSON(w, http.StatusOK, addonUpdateStatusResponse(svc.Status()))
	}
}

// PostAddonUpdateCheck triggers an immediate release check. The
// result (success or failure) is observed via GET or the WS broadcast,
// not via this response — mirrors the documented "202: Check started;
// observe via GET or the WS broadcast." A GitHub API round trip is
// fast enough to await synchronously within the request's lifetime;
// [addonupdate.ErrBusy] (a check is already running) is a benign no-op
// here rather than an error response — the spec declares no error
// status for that case.
func PostAddonUpdateCheck(svc AddonUpdateService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil || !svc.Status().Supported {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Add-on self-update not supported", ""))
			return
		}
		_ = svc.Check(r.Context())
		w.WriteHeader(http.StatusAccepted)
	}
}

// PostAddonUpdateInstall triggers the download/verify/stage/install
// sequence (ADR 0057). The router gates it on the operator role.
//
// Responses: 202 once the sequence has started (the daemon restarts on
// success — the outcome is observed via GET or the WS broadcast, not
// this response), 404 when self-update is unsupported, 409 when no
// update is available or another Check/Install is already running.
func PostAddonUpdateInstall(svc AddonUpdateService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Add-on self-update not supported", ""))
			return
		}
		err := svc.InstallAsync(r.Context())
		switch {
		case err == nil:
			if rec != nil {
				note := ""
				if st := svc.Status(); st.LatestVersion != "" {
					note = st.LatestVersion
				}
				rec.Record(audit.Entry{
					User:   identityFromCtx(r.Context()),
					Action: audit.ActionAddonUpdateInstall,
					Note:   note,
				})
			}
			w.WriteHeader(http.StatusAccepted)
		case errors.Is(err, addonupdate.ErrUnsupported):
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Add-on self-update not supported", ""))
		case errors.Is(err, addonupdate.ErrNoUpdateAvailable):
			problem.Write(w, http.StatusConflict,
				problem.New(problem.TypeConflict, r, "No update available", ""))
		case errors.Is(err, addonupdate.ErrBusy):
			problem.Write(w, http.StatusConflict,
				problem.New(problem.TypeConflict, r, "An update operation is already running", ""))
		default:
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Install trigger failed", err)
		}
	}
}
