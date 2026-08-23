// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
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
	SchemaVersion string              `json:"schema_version"`
	GeneratedAt   string              `json:"generated_at"`
	Build         DiagnosticsBuild    `json:"build"`
	Anonymized    bool                `json:"anonymized"`
	Health        DiagnosticsHealth   `json:"health"`
	Interfaces    []InterfaceState    `json:"interfaces,omitempty"`
	Incidents     []Incident          `json:"incidents,omitempty"`
	SystemStatus  []SystemStatusEntry `json:"system_status,omitempty"`
	LogLevels     *LogLevelsResponse  `json:"log_levels,omitempty"`
	// CapturesActive is reserved for a future debug-capture count; no
	// producer in this tree assigns it, so it always serializes as
	// absent (omitempty).
	CapturesActive int `json:"captures_active,omitempty"`
	// Metrics carries one typed metrics snapshot per central, keyed by
	// central name — the structured twin of the flat Prometheus
	// exposition at `/metrics`. Counters only (requests, recovery
	// attempts, cache sizes, data-point counts per category): the
	// section names no device and needs no anonymisation.
	Metrics map[string]metrics.MetricsSnapshot `json:"metrics,omitempty"`
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
	// KnownCentrals returns the CCU scope names the per-CCU score map
	// should iterate. It is resolved on every request rather than held
	// as a slice because the set is not fixed: a CCU adopted at runtime
	// must show up in the next dump, and one that was removed must
	// disappear from it. The composition root fills this from the
	// daemon's [*central.Registry]. Nil, or an empty result, disables
	// the per-central score block.
	KnownCentrals func() []string
	// HealthGauges, when set, returns the current pull-gauge readings
	// the tracker keeps (event_bus / audit / scheduler / rest / ws).
	// [*health.Tracker.Gauges] satisfies this directly.
	HealthGauges func() map[string]float64
	// CentralMetrics, when set, returns the typed metrics snapshot of
	// every central that has one. The composition root fills this from
	// the per-CCU aggregators the daemon wires at boot; nil (or an
	// empty result) omits the block rather than reporting zeroes.
	CentralMetrics func(ctx context.Context) map[string]metrics.MetricsSnapshot
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
// `false`) explicitly to receive raw values. See [anonymiseDiagnostics]
// for what anonymisation covers; structural relationships (interface
// counts, status verdicts, sample counts) stay intact either way.
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
				if deps.KnownCentrals != nil {
					if names := deps.KnownCentrals(); len(names) > 0 {
						h.CentralScores = make(map[string]int, len(names))
						for _, name := range names {
							h.CentralScores[name] = deps.HealthExt.CentralScoreInt(name)
						}
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

		if deps.CentralMetrics != nil {
			if m := deps.CentralMetrics(r.Context()); len(m) > 0 {
				env.Metrics = m
			}
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

		if anonymize {
			anonymiseDiagnostics(&env)
		}
		JSON(w, http.StatusOK, env)
	}
}

// anonymiseDiagnostics replaces the site-identifying values in env in place.
//
// The dump is positioned as the artefact an operator attaches to a bug report,
// and it says so itself in `anonymized`. What it carries that names a site is
// the CCU host of every interface plus the free text of incidents, health
// notes and system-status reasons — journal excerpts and error strings that
// splice in device addresses and the CCU's address. Those are tokenised with
// the same stable 12-hex SHA-256 scheme [anonymiseSnapshot] uses, so two dumps
// of the same installation still correlate.
//
// Deliberately left intact: central names, interface ids, component names and
// every counter. They are this envelope's join keys — the per-central score
// map, the metrics map and the status entries are all keyed on them — and
// tokenising them would cost the whole diagnostic value while hiding nothing
// the free-text redaction does not already cover.
func anonymiseDiagnostics(env *DiagnosticsEnvelope) {
	for i := range env.Health.Components {
		env.Health.Components[i].Note = anonymiseFreeText(env.Health.Components[i].Note)
	}
	for i := range env.Interfaces {
		env.Interfaces[i].Host = anonToken("host", env.Interfaces[i].Host)
		env.Interfaces[i].Note = anonymiseFreeText(env.Interfaces[i].Note)
	}
	// Copy before rewriting: the readers hand out slices whose elements
	// (and whose nested string slices) may still be owned by a live store
	// or ring buffer, and an anonymised response must not mutate them.
	env.Incidents = slices.Clone(env.Incidents)
	for i := range env.Incidents {
		env.Incidents[i].Summary = anonymiseFreeText(env.Incidents[i].Summary)
		env.Incidents[i].Detail = anonymiseFreeText(env.Incidents[i].Detail)
	}
	env.SystemStatus = slices.Clone(env.SystemStatus)
	for i := range env.SystemStatus {
		e := &env.SystemStatus[i]
		e.Reason = anonymiseFreeText(e.Reason)
		if len(e.Issues) == 0 {
			continue
		}
		issues := make([]string, len(e.Issues))
		for j, issue := range e.Issues {
			issues[j] = anonymiseFreeText(issue)
		}
		e.Issues = issues
	}
}

var (
	// addressLikeRe matches a token shaped like a Homematic device address or
	// serial — "LEQ1234567", "VCU0000123", "0001D3C99C1234", optionally with a
	// ":<channel>" suffix. Incident details and status reasons splice these
	// into free text, so they have to be redacted wherever they appear rather
	// than only in dedicated address fields. [addressLike] applies the
	// letters-and-digits test RE2 cannot express without lookahead.
	addressLikeRe = regexp.MustCompile(`\b[0-9A-Z]{8,20}(?::\d{1,3})?\b`)
	// ipv4Re matches a dotted-quad literal. The CCU's address reaches free
	// text through connection errors ("dial tcp 10.0.0.5:2010: …").
	ipv4Re = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
)

// anonymiseFreeText tokenises the address- and host-shaped substrings of s,
// leaving the surrounding prose readable — an operator triaging from the dump
// still sees which error occurred, only not on which device or host.
func anonymiseFreeText(s string) string {
	if s == "" {
		return ""
	}
	s = ipv4Re.ReplaceAllStringFunc(s, func(m string) string { return anonToken("host", m) })
	return addressLikeRe.ReplaceAllStringFunc(s, func(m string) string {
		if !addressLike(m) {
			return m
		}
		return anonToken("addr", m)
	})
}

// addressLike reports whether an uppercase-alphanumeric token is plausibly a
// device address rather than a protocol constant. Homematic serials always mix
// letters with several digits, while the constants that share the character
// class ("UNREACH", "CONFIG_PENDING") carry no digits at all.
func addressLike(tok string) bool {
	var letters, digits int
	for _, r := range tok {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r >= 'A' && r <= 'Z':
			letters++
		}
	}
	return letters >= 1 && digits >= 2
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
