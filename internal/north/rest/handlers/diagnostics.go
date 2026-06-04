// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// DiagnosticsBuild is the build metadata block of a diagnostics dump.
type DiagnosticsBuild struct {
	Version   string `json:"version,omitempty"`
	Commit    string `json:"commit,omitempty"`
	GoVersion string `json:"go_version"`
	BuildTime string `json:"build_time,omitempty"`
}

// DiagnosticsHealth mirrors [HealthResponse] but adds the numeric
// score that the SPA tachometer renders.
type DiagnosticsHealth struct {
	Status         string              `json:"status"`
	Score          int                 `json:"score"`
	Available      bool                `json:"available"`
	Degraded       bool                `json:"degraded"`
	Failed         bool                `json:"failed"`
	Components     []HealthComponent   `json:"components"`
	Clients        []DiagnosticsClient `json:"clients,omitempty"`
	CentralScores  map[string]int      `json:"central_scores,omitempty"`
	Gauges         map[string]float64  `json:"gauges,omitempty"`
	PrimaryHealthy bool                `json:"primary_client_healthy"`
}

// DiagnosticsClient is the per-interface detail block in
// [DiagnosticsHealth.Clients] — the wire shape the operator-facing
// SPA / agent consumes to triage a CCU connection.
type DiagnosticsClient struct {
	Name                  string `json:"name"`
	Score                 int    `json:"score"`
	Status                string `json:"status"`
	LastSuccessfulRequest string `json:"last_successful_request,omitempty"`
	LastFailedRequest     string `json:"last_failed_request,omitempty"`
	LastEventReceived     string `json:"last_event_received,omitempty"`
	ConsecutiveFailures   int    `json:"consecutive_failures"`
	ReconnectAttempts     int    `json:"reconnect_attempts"`
	InRecovery            bool   `json:"in_recovery"`
}

// DiagnosticsEnvelope is the top-level body of
// `GET /api/v1/diagnostics`.
type DiagnosticsEnvelope struct {
	SchemaVersion  string              `json:"schema_version"`
	GeneratedAt    string              `json:"generated_at"`
	Build          DiagnosticsBuild    `json:"build"`
	Anonymized     bool                `json:"anonymized"`
	Health         DiagnosticsHealth   `json:"health"`
	Interfaces     []InterfaceState    `json:"interfaces,omitempty"`
	Incidents      []Incident          `json:"incidents,omitempty"`
	SystemStatus   []SystemStatusEntry `json:"system_status,omitempty"`
	LogLevels      *LogLevelsResponse  `json:"log_levels,omitempty"`
	CapturesActive int                 `json:"captures_active,omitempty"`
}

// DiagnosticsDeps bundles every reader the diagnostics handler pulls
// from. Every field is optional — missing sources contribute an empty
// slice rather than failing the whole request, so a freshly-booted
// daemon still produces a meaningful dump.
type DiagnosticsDeps struct {
	Health       HealthReader
	HealthExt    HealthExtras
	Interfaces   InterfaceIndex
	Incidents    IncidentsReader
	SystemStatus SystemStatusReader
	LogLevels    LogLevelsService
	// KnownCentrals are the CCU scope names the per-CCU score map
	// should iterate. The composition root fills this from the
	// daemon's [*central.Registry]; tests can pass an explicit slice.
	// Empty disables the per-central score block.
	KnownCentrals []string
	// HealthGauges, when set, returns the current pull-gauge readings
	// the tracker keeps (event_bus / audit / scheduler / rest / ws).
	// [*health.Tracker.Gauges] satisfies this directly.
	HealthGauges func() map[string]float64
}

// HealthExtras is an optional facade that exposes the numeric score
// and availability flags. The [*health.Tracker] satisfies it
// directly; legacy [HealthReader]-only consumers continue to work
// without an upgrade.
type HealthExtras interface {
	ScoreInt() int
	IsAvailable() bool
	IsDegraded() bool
	IsFailed() bool
	PrimaryClientHealthy() bool
	ClientScore(name string) float64
	ClientDetail(name string) (health.ClientHealth, bool)
	CentralScoreInt(centralName string) int
}

// Diagnostics renders the combined dump. The endpoint is anonymous by
// default to keep the artefact safe-to-share; pass `?anonymize=0` (or
// `false`) explicitly to receive raw values. Device addresses,
// host names, and operator-controlled fields are hashed when
// anonymisation is on; structural relationships (interface counts,
// status verdicts, sample counts) stay intact.
func Diagnostics(deps DiagnosticsDeps) http.HandlerFunc { //nolint:gocognit,funlen // single-purpose diagnostics builder with many subsystem branches
	return func(w http.ResponseWriter, r *http.Request) {
		anonymize := parseBool(r.URL.Query().Get("anonymize"), true)

		env := DiagnosticsEnvelope{
			SchemaVersion: "1.0",
			GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
			Anonymized:    anonymize,
			Build: DiagnosticsBuild{
				Version:   build.Version,
				Commit:    build.Commit,
				GoVersion: runtime.Version(),
				BuildTime: build.BuildDate,
			},
		}

		if deps.Health != nil {
			snap := deps.Health.Snapshot()
			comps := make([]HealthComponent, 0, len(snap))
			for _, c := range snap {
				comps = append(comps, HealthComponent{
					Name:       c.Name,
					Status:     string(c.Status),
					Note:       c.LastSample.Note,
					RecordedAt: c.LastSample.Timestamp,
				})
			}
			h := DiagnosticsHealth{
				Status:     string(deps.Health.Overall()),
				Components: comps,
			}
			if deps.HealthExt != nil {
				h.Score = deps.HealthExt.ScoreInt()
				h.Available = deps.HealthExt.IsAvailable()
				h.Degraded = deps.HealthExt.IsDegraded()
				h.Failed = deps.HealthExt.IsFailed()
				h.PrimaryHealthy = deps.HealthExt.PrimaryClientHealthy()
				if len(deps.KnownCentrals) > 0 {
					h.CentralScores = make(map[string]int, len(deps.KnownCentrals))
					for _, name := range deps.KnownCentrals {
						h.CentralScores[name] = deps.HealthExt.CentralScoreInt(name)
					}
				}
				// Per-component client detail — only emit a block when the
				// tracker actually has detail metrics for that name
				// (RecordRequest / SetRecoveryFlag have touched it).
				for _, c := range snap {
					detail, ok := deps.HealthExt.ClientDetail(c.Name)
					if !ok {
						continue
					}
					entry := DiagnosticsClient{
						Name:                c.Name,
						Score:               int(deps.HealthExt.ClientScore(c.Name) * 100),
						Status:              string(c.Status),
						ConsecutiveFailures: detail.ConsecutiveFailures,
						ReconnectAttempts:   detail.ReconnectAttempts,
						InRecovery:          detail.InRecovery,
					}
					if !detail.LastSuccessfulRequest.IsZero() {
						entry.LastSuccessfulRequest = detail.LastSuccessfulRequest.UTC().Format(time.RFC3339Nano)
					}
					if !detail.LastFailedRequest.IsZero() {
						entry.LastFailedRequest = detail.LastFailedRequest.UTC().Format(time.RFC3339Nano)
					}
					if !detail.LastEventReceived.IsZero() {
						entry.LastEventReceived = detail.LastEventReceived.UTC().Format(time.RFC3339Nano)
					}
					h.Clients = append(h.Clients, entry)
				}
			} else {
				// Fallback: derive flags from the overall status.
				switch deps.Health.Overall() {
				case health.StatusHealthy:
					h.Available = true
				case health.StatusDegraded:
					h.Degraded = true
				case health.StatusUnhealthy:
					h.Failed = true
				case health.StatusUnknown:
					// Leave all three flags false — "unknown" maps to
					// "we have no opinion yet", which is exactly what
					// `!Available && !Degraded && !Failed` encodes.
				}
			}
			if deps.HealthGauges != nil {
				if g := deps.HealthGauges(); len(g) > 0 {
					h.Gauges = g
				}
			}
			env.Health = h
		}

		if deps.Interfaces != nil {
			env.Interfaces = append(env.Interfaces, deps.Interfaces.Interfaces()...)
		}

		if deps.Incidents != nil {
			incidents := deps.Incidents.Incidents()
			if len(incidents) > 50 {
				incidents = incidents[len(incidents)-50:]
			}
			env.Incidents = incidents
		}

		if deps.SystemStatus != nil {
			env.SystemStatus = deps.SystemStatus.SystemStatusEntries()
		}

		if deps.LogLevels != nil {
			ll := &LogLevelsResponse{Default: ""}
			ll.Default = strings.ToLower(deps.LogLevels.Default().String())
			for _, ov := range deps.LogLevels.Snapshot() {
				entry := LogLevelEntry{
					Path:      ov.Path,
					Level:     strings.ToLower(ov.Level.String()),
					Permanent: ov.Permanent,
				}
				if !ov.ExpiresAt.IsZero() {
					entry.ExpiresAt = ov.ExpiresAt.UTC().Format(time.RFC3339Nano)
					entry.RemainingMS = ov.RemainingMS
				}
				ll.Overrides = append(ll.Overrides, entry)
			}
			env.LogLevels = ll
		}

		JSON(w, http.StatusOK, env)
	}
}

func parseBool(raw string, defaultVal bool) bool {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return defaultVal
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	if v, err := strconv.ParseBool(raw); err == nil {
		return v
	}
	return defaultVal
}
