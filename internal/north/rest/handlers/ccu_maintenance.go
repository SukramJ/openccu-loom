// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// CCURebootPort reboots one CCU host addressed by central name.
// *adapter.CCUMaintenanceDomain satisfies it; the call resolves the
// central's primary backend and runs the reboot_ccu ReGa script.
type CCURebootPort interface {
	RebootCCU(ctx context.Context, central string) error
}

// PostCCUReboot reboots the named CCU. The router gates it on the admin
// role. It reboots the CCU hardware — not the OpenCCU-Loom daemon (that is
// POST /system/restart). The southbound connection to the central drops for
// the reboot and recovers automatically via the readiness gate.
//
// Responses: 202 once the reboot was triggered, 404 when the central is
// unknown, 502 when the CCU-side call failed, 503 when the service is
// unwired. Audit-logs via the AuditRecorder pattern of PostDeviceInstallMode.
func PostCCUReboot(svc CCURebootPort, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "CCU maintenance unwired", ""))
			return
		}
		centralName := chi.URLParam(r, "central")
		if centralName == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing central", "central path parameter is required"))
			return
		}
		if err := svc.RebootCCU(r.Context(), centralName); err != nil {
			if errors.Is(err, hmerr.ErrUnknownCentral) {
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Unknown central", centralName))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "CCU reboot failed", err)
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:   identityFromCtx(r.Context()),
				Action: audit.ActionSystemCCUReboot,
				Note:   centralName,
			})
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// CCUHostActionPort drives a CCU host's power state or boot mode.
// *adapter.CCUMaintenanceDomain satisfies it.
type CCUHostActionPort interface {
	PoweroffCCU(ctx context.Context, central string) error
	EnterSafeMode(ctx context.Context, central string) error
	EnterRecoveryMode(ctx context.Context, central string) error
}

// ccuHostAction names one host action for the shared handler below.
type ccuHostAction struct {
	run    func(svc CCUHostActionPort, ctx context.Context, central string) error
	audit  audit.Action
	failed string
}

var (
	ccuActionPoweroff = ccuHostAction{
		run:    func(svc CCUHostActionPort, ctx context.Context, c string) error { return svc.PoweroffCCU(ctx, c) },
		audit:  audit.ActionSystemCCUPoweroff,
		failed: "CCU poweroff failed",
	}
	ccuActionSafeMode = ccuHostAction{
		run:    func(svc CCUHostActionPort, ctx context.Context, c string) error { return svc.EnterSafeMode(ctx, c) },
		audit:  audit.ActionSystemCCUSafeMode,
		failed: "CCU safe-mode entry failed",
	}
	ccuActionRecoveryMode = ccuHostAction{
		run:    func(svc CCUHostActionPort, ctx context.Context, c string) error { return svc.EnterRecoveryMode(ctx, c) },
		audit:  audit.ActionSystemCCURecoveryMode,
		failed: "CCU recovery-mode entry failed",
	}
)

// ccuHostActionHandler is the shared body of the three host actions: they
// differ only in which call they make and what they audit, so the status
// ladder lives in one place instead of being copied three times.
//
// Responses: 202 once triggered, 400 on a missing central, 404 when the
// central is unknown, 422 when the backend cannot host the action (CUxD,
// Homegear, or a firmware without the method), 502 on a CCU-side failure,
// 503 when unwired.
func ccuHostActionHandler(svc CCUHostActionPort, rec audit.Recorder, act ccuHostAction) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "CCU maintenance unwired", ""))
			return
		}
		centralName := chi.URLParam(r, "central")
		if centralName == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing central", "central path parameter is required"))
			return
		}
		if err := act.run(svc, r.Context(), centralName); err != nil {
			switch {
			case errors.Is(err, hmerr.ErrUnknownCentral):
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Unknown central", centralName))
			case errors.Is(err, backends.ErrUnsupported):
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Action not supported by this central", err.Error()))
			default:
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, act.failed, err)
			}
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:   identityFromCtx(r.Context()),
				Action: act.audit,
				Note:   centralName,
			})
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// PostCCUPoweroff shuts the named CCU down. Nothing brings it back: the
// central stays in the readiness gate until it is powered on again.
func PostCCUPoweroff(svc CCUHostActionPort, rec audit.Recorder) http.HandlerFunc {
	return ccuHostActionHandler(svc, rec, ccuActionPoweroff)
}

// PostCCUSafeMode restarts the named CCU into safe mode, where the ReGa
// logic layer stays down so a broken configuration can be repaired.
func PostCCUSafeMode(svc CCUHostActionPort, rec audit.Recorder) http.HandlerFunc {
	return ccuHostActionHandler(svc, rec, ccuActionSafeMode)
}

// PostCCURecoveryMode restarts the named CCU into its recovery system.
// OpenCCU / RaspberryMatic only; a stock CCU3 has no such method and the
// resulting error is propagated rather than swallowed.
func PostCCURecoveryMode(svc CCUHostActionPort, rec audit.Recorder) http.HandlerFunc {
	return ccuHostActionHandler(svc, rec, ccuActionRecoveryMode)
}

// CCUPositionPort writes the astro reference position of one CCU host
// addressed by central name. *adapter.CCUMaintenanceDomain satisfies it.
type CCUPositionPort interface {
	SetCCUPosition(ctx context.Context, central string, longitude, latitude float64) error
}

// ccuPositionRequest is the PUT body. Both coordinates are required:
// writing only one would leave the CCU with a position that is half old
// and half new, which is worse than rejecting the request.
type ccuPositionRequest struct {
	Longitude *float64 `json:"longitude"`
	Latitude  *float64 `json:"latitude"`
}

// PutCCUPosition sets the named CCU's astro reference position. The router
// gates it on the admin role. Every sunrise/sunset time the CCU computes
// derives from this position, so it is a system-wide setting rather than a
// display preference.
//
// Responses: 204 on success, 400 on a missing central or malformed body,
// 422 when a coordinate is out of range or the backend has no ReGa path,
// 404 when the central is unknown, 502 when the CCU-side call failed, 503
// when the service is unwired.
func PutCCUPosition(svc CCUPositionPort, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "CCU maintenance unwired", ""))
			return
		}
		centralName := chi.URLParam(r, "central")
		if centralName == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing central", "central path parameter is required"))
			return
		}
		var req ccuPositionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON body", err.Error()))
			return
		}
		if req.Longitude == nil || req.Latitude == nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing coordinate",
					"longitude and latitude are both required"))
			return
		}
		if err := svc.SetCCUPosition(r.Context(), centralName, *req.Longitude, *req.Latitude); err != nil {
			switch {
			case errors.Is(err, hmerr.ErrUnknownCentral):
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Unknown central", centralName))
			case errors.Is(err, hmerr.ErrValidation), errors.Is(err, backends.ErrUnsupported):
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Position rejected", err.Error()))
			default:
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "CCU position write failed", err)
			}
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:   identityFromCtx(r.Context()),
				Action: audit.ActionSystemCCUPosition,
				Note:   centralName,
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// FirmwareDownloadPort tells a CCU to fetch a firmware image from a URL
// onto the central so it can be staged for installation.
// *adapter.CCUMaintenanceDomain satisfies it; the call resolves the
// central's primary backend and posts to the CCU's maintenance CGI.
type FirmwareDownloadPort interface {
	DownloadFirmware(ctx context.Context, central, firmwareURL string) error
}

// FirmwareDownloadRequest is the body of `POST /system/firmware/download`.
type FirmwareDownloadRequest struct {
	// URL is the http/https firmware image the CCU should fetch (required).
	URL string `json:"url"`
	// Central selects the target CCU; optional for single-CCU deployments.
	Central string `json:"central,omitempty"`
}

// PostSystemFirmwareDownload asks the named CCU to download a firmware
// image onto the central (the CCU fetches and stages it for a later
// install). The router gates it on the admin role.
//
// Responses: 202 once the download was triggered, 400 on a malformed
// body, 404 when the central is unknown, 422 when the URL is missing /
// not http(s) or the backend cannot download (CUxD, Homegear), 502 when
// the CCU-side call failed, 503 when the service is unwired.
func PostSystemFirmwareDownload(svc FirmwareDownloadPort, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Firmware download unwired", ""))
			return
		}
		var req FirmwareDownloadRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		req.URL = strings.TrimSpace(req.URL)
		if req.URL == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "url is required", ""))
			return
		}
		if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "url must be http or https", ""))
			return
		}
		if err := svc.DownloadFirmware(r.Context(), req.Central, req.URL); err != nil {
			switch {
			case errors.Is(err, hmerr.ErrUnknownCentral):
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Unknown central", req.Central))
			case errors.Is(err, backends.ErrUnsupported):
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Firmware download not supported by this backend", ""))
			default:
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Firmware download failed", err)
			}
			return
		}
		if rec != nil {
			note := req.URL
			if req.Central != "" {
				note = req.Central + " " + req.URL
			}
			rec.Record(audit.Entry{
				User:   identityFromCtx(r.Context()),
				Action: audit.ActionSystemFirmwareDownload,
				Note:   note,
			})
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
