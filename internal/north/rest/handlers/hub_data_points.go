// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// HubDataPoints is the aggregated hub-singleton snapshot for one central,
// returned by `GET /api/v1/hub/data-points`. It gathers the hub singletons
// (alarm / service messages, inbox, firmware update, metrics, per-interface
// connectivity and install-mode) into the coordinator shape a client builds
// its singleton entities from. The same values are also reachable through the
// dedicated list endpoints; this endpoint exists so a hub coordinator can be
// constructed from a single fetch without orchestrating six requests. Mirrors
// the reference stack's hub data-point set.
type HubDataPoints struct {
	Central         string                     `json:"central,omitempty"`
	AlarmMessages   HubCountDataPoint          `json:"alarm_messages"`
	ServiceMessages HubCountDataPoint          `json:"service_messages"`
	Inbox           HubCountDataPoint          `json:"inbox"`
	Update          HubUpdateDataPoint         `json:"update"`
	Metrics         []HubMetricDataPoint       `json:"metrics,omitempty"`
	Connectivity    []HubConnectivityDataPoint `json:"connectivity,omitempty"`
	InstallMode     []HubInstallModeDataPoint  `json:"install_mode,omitempty"`
}

// HubCountDataPoint is a count-valued hub singleton (alarm / service messages,
// inbox). LegacyName matches the reference stack's sensor name so the client
// derives a stable unique id the same way it does for sysvars / programs.
type HubCountDataPoint struct {
	LegacyName string `json:"legacy_name"`
	Value      int    `json:"value"`
}

// HubUpdateDataPoint mirrors the firmware-update singleton.
type HubUpdateDataPoint struct {
	LegacyName      string `json:"legacy_name"`
	UpdateAvailable bool   `json:"update_available"`
	InProgress      bool   `json:"in_progress"`
}

// HubMetricDataPoint is one hub metric (system health, connection latency,
// last-event age) with its unit.
type HubMetricDataPoint struct {
	LegacyName string  `json:"legacy_name"`
	Value      float64 `json:"value"`
	Unit       string  `json:"unit,omitempty"`
}

// HubConnectivityDataPoint is the per-interface reachability singleton.
type HubConnectivityDataPoint struct {
	InterfaceID string `json:"interface_id"`
	Reachable   bool   `json:"reachable"`
}

// HubInstallModeDataPoint is the per-interface install-mode singleton.
// RemainingS is the seconds left while enabled; Observed is false until the
// first state has been read from the CCU.
type HubInstallModeDataPoint struct {
	InterfaceID string `json:"interface_id"`
	Enabled     bool   `json:"enabled"`
	RemainingS  int    `json:"remaining_s"`
	Observed    bool   `json:"observed"`
}

// GetHubDataPoints returns the aggregated hub-singleton snapshot for every
// central as a JSON array (one entry per central).
func GetHubDataPoints(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if idx == nil {
			JSON(w, http.StatusOK, []HubDataPoints{})
			return
		}
		hubs := idx.Hubs()
		out := make([]HubDataPoints, 0, len(hubs))
		for _, nh := range hubs {
			if nh.Hub == nil {
				continue
			}
			out = append(out, hubDataPoints(nh.Central, nh.Hub))
		}
		JSON(w, http.StatusOK, out)
	}
}

func hubDataPoints(central string, h *hub.Hub) HubDataPoints {
	dp := HubDataPoints{
		Central:         central,
		AlarmMessages:   HubCountDataPoint{LegacyName: "alarm_messages"},
		ServiceMessages: HubCountDataPoint{LegacyName: "service_messages"},
		Inbox:           HubCountDataPoint{LegacyName: "inbox"},
		Update:          HubUpdateDataPoint{LegacyName: "system_update"},
	}
	if h.Messages != nil {
		dp.AlarmMessages.Value = h.Messages.Count()
	}
	if h.ServiceMessages != nil {
		dp.ServiceMessages.Value = h.ServiceMessages.Count()
	}
	if h.Inbox != nil {
		dp.Inbox.Value = h.Inbox.Count()
	}
	if h.Update != nil {
		if info, ok := h.Update.UpdateInfo(); ok {
			dp.Update.UpdateAvailable = info.UpdateAvailable
		}
		dp.Update.InProgress = h.Update.InProgress()
	}
	if h.Metrics != nil {
		snap := h.Metrics.Snapshot()
		// Emit in a fixed order so the wire shape is deterministic.
		for _, kind := range []hub.MetricKind{
			hub.MetricSystemHealth,
			hub.MetricConnectionLatMs,
			hub.MetricLastEventAgeSecs,
		} {
			sample, ok := snap[kind]
			if !ok {
				continue
			}
			// A negative system_health is the not-ready sentinel
			// ([hub.MetricSystemHealthUnknown]): omit it rather than surface a
			// stale percentage or a bogus negative when the central is FAILED.
			if kind == hub.MetricSystemHealth && sample.Value < 0 {
				continue
			}
			dp.Metrics = append(dp.Metrics, HubMetricDataPoint{
				LegacyName: string(kind),
				Value:      sample.Value,
				Unit:       hub.MetricSensorUnit(kind),
			})
		}
	}
	if conn := h.ConnectivityDataPoints(); conn != nil {
		for _, r := range conn.List() {
			dp.Connectivity = append(dp.Connectivity, HubConnectivityDataPoint{
				InterfaceID: r.InterfaceID,
				Reachable:   r.Reachable,
			})
		}
	}
	for _, im := range h.InstallModeDPs() {
		enabled, remaining, observed := im.InstallState()
		dp.InstallMode = append(dp.InstallMode, HubInstallModeDataPoint{
			InterfaceID: im.InterfaceID,
			Enabled:     enabled,
			RemainingS:  int(remaining.Seconds()),
			Observed:    observed,
		})
	}
	return dp
}
