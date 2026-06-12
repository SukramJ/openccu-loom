// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// system_hub.go — hub-singleton endpoints: CCU system-update info,
// hub metrics, and per-interface install-mode state. They expose the
// hub model objects external clients need to mirror the reference
// stack's hub singletons (system-update entity, system-health /
// connection-latency / last-event-age sensors, per-interface
// install-mode sensor + button).

package handlers

import (
	"net/http"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// SystemUpdateEntry is one entry in `GET /system/update` (one per central).
type SystemUpdateEntry struct {
	// Central is the CCU this update info belongs to.
	Central string `json:"central,omitempty"`
	// CurrentFirmware is the installed CCU firmware version.
	CurrentFirmware string `json:"current_firmware,omitempty"`
	// AvailableFirmware is the installable CCU firmware version.
	AvailableFirmware string `json:"available_firmware,omitempty"`
	// UpdateAvailable is true when AvailableFirmware is newer.
	UpdateAvailable bool `json:"update_available"`
	// InProgress is true while a system update is installing.
	InProgress bool `json:"in_progress"`
	// Observed is false until the first update-info fetch succeeded.
	Observed bool `json:"observed"`
}

// GetSystemUpdate returns the CCU system-update info per central.
func GetSystemUpdate(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		out := make([]SystemUpdateEntry, 0, 2)
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil || nh.Hub.Update == nil {
				continue
			}
			info, ok := nh.Hub.Update.UpdateInfo()
			out = append(out, SystemUpdateEntry{
				Central:           nh.Central,
				CurrentFirmware:   info.CurrentFirmware,
				AvailableFirmware: info.AvailableFirmware,
				UpdateAvailable:   info.UpdateAvailable,
				InProgress:        nh.Hub.Update.InProgress(),
				Observed:          ok,
			})
		}
		JSON(w, http.StatusOK, out)
	}
}

// PostSystemUpdateInstall triggers the CCU system update on one central
// (selected via the `central` query parameter; optional for single-CCU).
func PostSystemUpdateInstall(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := resolveHubForMutation(idx, r.URL.Query().Get("central"))
		if h == nil || h.Update == nil {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Central not found", "no matching central"))
			return
		}
		if err := h.Update.Install(r.Context()); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "System update failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// HubMetricsEntry is one entry in `GET /system/metrics` (one per central).
// Metric fields are pointers: nil means "not observed yet".
type HubMetricsEntry struct {
	// Central is the CCU these metrics belong to.
	Central string `json:"central,omitempty"`
	// SystemHealth is the aggregate health score in percent (0-100).
	SystemHealth *float64 `json:"system_health,omitempty"`
	// ConnectionLatencyMs is the average CCU round-trip latency.
	ConnectionLatencyMs *float64 `json:"connection_latency_ms,omitempty"`
	// LastEventAgeSeconds is the age of the newest backend event.
	LastEventAgeSeconds *float64 `json:"last_event_age_seconds,omitempty"`
}

// GetHubMetrics returns the hub metric samples per central as JSON —
// the machine-readable twin of the Prometheus `/metrics` endpoint for
// the three hub sensors external clients mirror.
func GetHubMetrics(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		out := make([]HubMetricsEntry, 0, 2)
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil || nh.Hub.Metrics == nil {
				continue
			}
			entry := HubMetricsEntry{Central: nh.Central}
			snap := nh.Hub.Metrics.Snapshot()
			if s, ok := snap[hub.MetricSystemHealth]; ok {
				v := s.Value
				entry.SystemHealth = &v
			}
			if s, ok := snap[hub.MetricConnectionLatMs]; ok {
				v := s.Value
				entry.ConnectionLatencyMs = &v
			}
			if s, ok := snap[hub.MetricLastEventAgeSecs]; ok {
				v := s.Value
				entry.LastEventAgeSeconds = &v
			}
			out = append(out, entry)
		}
		JSON(w, http.StatusOK, out)
	}
}

// InstallModeInterfaceEntry is one entry in `GET /install-mode/interfaces`.
type InstallModeInterfaceEntry struct {
	// Central is the CCU the interface belongs to.
	Central string `json:"central,omitempty"`
	// Interface is the interface ID the install-mode DP controls.
	Interface string `json:"interface"`
	// Active is true while install mode is running on the interface.
	Active bool `json:"active"`
	// Seconds is the remaining install-mode time (0 when inactive).
	Seconds int `json:"seconds"`
	// Observed is false until the first state fetch succeeded.
	Observed bool `json:"observed"`
}

// InstallModeInterfaceRequest is the body of `POST /install-mode/interfaces`.
type InstallModeInterfaceRequest struct {
	// Interface selects the install-mode DP (required).
	Interface string `json:"interface"`
	// Active starts (true) or stops (false) install mode.
	Active bool `json:"active"`
	// Seconds is the install-mode duration (default 60).
	Seconds int `json:"seconds,omitempty"`
}

// GetInstallModeInterfaces returns the per-interface install-mode state.
func GetInstallModeInterfaces(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		out := make([]InstallModeInterfaceEntry, 0, 4)
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil {
				continue
			}
			for _, dp := range nh.Hub.InstallModeDPs() {
				active, remaining, observed := dp.InstallState()
				out = append(out, InstallModeInterfaceEntry{
					Central:   nh.Central,
					Interface: dp.InterfaceID,
					Active:    active,
					Seconds:   int(remaining.Seconds()),
					Observed:  observed,
				})
			}
		}
		JSON(w, http.StatusOK, out)
	}
}

// PostInstallModeInterface starts or stops install mode on one interface.
func PostInstallModeInterface(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		var req InstallModeInterfaceRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.Interface == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "interface is required", ""))
			return
		}
		if req.Seconds < 0 {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "seconds must be >= 0", ""))
			return
		}
		duration := time.Duration(req.Seconds) * time.Second
		if duration == 0 {
			duration = 60 * time.Second
		}
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil {
				continue
			}
			for _, dp := range nh.Hub.InstallModeDPs() {
				if dp.InterfaceID != req.Interface {
					continue
				}
				var err error
				if req.Active {
					err = dp.Enable(r.Context(), duration)
				} else {
					err = dp.Disable(r.Context())
				}
				if err != nil {
					problem.Write(w, http.StatusBadGateway,
						problem.New(problem.TypeInternal, r, "Install mode write failed", err.Error()))
					return
				}
				w.WriteHeader(http.StatusAccepted)
				return
			}
		}
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, r, "Interface not found", "no install-mode DP for interface"))
	}
}
