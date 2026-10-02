// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package warnings computes the operator-facing warning list served at
// GET /api/v1/warnings: one server-side aggregate over the daemon's
// existing diagnostic surfaces — unhealthy health components, recent
// error-grade incidents, per-central service-message backlogs and, when
// wired, pending client-pairing requests — so the Status card renders
// one list instead of re-deriving each.
//
// A warning can be silenced per user for a fixed period. A silence ends
// early when its warning's condition clears, so a re-occurrence alerts
// again — silencing "MQTT broker unreachable" for a week must not
// swallow next month's outage. Expired and cleared silences are pruned
// lazily on every read; there is no background goroutine.
package warnings

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/restapi"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// incidentWindow bounds how far back an error-grade incident keeps a
// warning alive. Incidents are a journal, not a state — without a
// window every historic failure would warn forever. 24 hours keeps
// "something failed today" visible across an operator's day.
const incidentWindow = 24 * time.Hour

// Severity grades a warning for display ordering.
type Severity string

// Severity values, ordered.
const (
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Warning is one row of the operator warning list. MessageKey names the
// SPA catalogue entry that renders it; Args carries the interpolation
// values, so the daemon stays the naming authority and the client stays
// a renderer.
type Warning struct {
	ID            string            `json:"id"`
	Severity      Severity          `json:"severity"`
	Central       string            `json:"central,omitempty"`
	MessageKey    string            `json:"message_key"`
	Args          map[string]string `json:"args,omitempty"`
	Silenced      bool              `json:"silenced"`
	SilencedUntil *time.Time        `json:"silenced_until,omitempty"`
}

// HealthSource is the slice of the health tracker the aggregator reads.
type HealthSource interface {
	Snapshot() []health.Component
}

// IncidentSource is the incident journal read.
type IncidentSource interface {
	Incidents() []hmapi.Incident
}

// HubSource yields every central's hub for the service-message counts.
type HubSource interface {
	Hubs() []restapi.NamedHub
}

// PairingSource reports how many revealed client-pairing requests wait
// for a decision — each one deserves the operator's attention right
// now, because it expires in minutes.
type PairingSource interface {
	PendingCount() int
}

// SilenceStore persists per-user silences. The production implementation
// is sqlite.WarningSilenceStore.
type SilenceStore interface {
	Silences(ctx context.Context, username string) (map[string]time.Time, error)
	Set(ctx context.Context, username, warningID string, until, now time.Time) error
	Delete(ctx context.Context, username, warningID string) error
	DeleteExpired(ctx context.Context, now time.Time) error
	DeleteOtherThan(ctx context.Context, username string, activeIDs []string) error
}

// Aggregator computes the active warning set. Any nil source simply
// contributes nothing, so partial wirings (tests, reduced deployments)
// degrade to a shorter list rather than a panic.
type Aggregator struct {
	health    HealthSource
	incidents IncidentSource
	hubs      HubSource
	pairing   PairingSource
	silences  SilenceStore
}

// New wires an Aggregator.
func New(h HealthSource, inc IncidentSource, hubs HubSource, silences SilenceStore) *Aggregator {
	return &Aggregator{health: h, incidents: inc, hubs: hubs, silences: silences}
}

// WithPairing adds the client-pairing feed. A separate wither rather
// than a New parameter so the aggregator's callers grow one source at a
// time without a signature churn; the composition root chains it at
// construction, which the wiring pin drives.
func (a *Aggregator) WithPairing(p PairingSource) *Aggregator {
	a.pairing = p
	return a
}

// Active computes the current warning set, unsilenced, in stable order
// (errors first, then by ID).
func (a *Aggregator) Active() []Warning {
	now := time.Now()
	var out []Warning

	if a.health != nil {
		for _, c := range a.health.Snapshot() {
			switch c.Status {
			case health.StatusUnhealthy:
				out = append(out, Warning{
					ID: "health:" + c.Name, Severity: SeverityError,
					MessageKey: "warnings.health.unhealthy", Args: map[string]string{"component": c.Name},
				})
			case health.StatusDegraded:
				out = append(out, Warning{
					ID: "health:" + c.Name, Severity: SeverityWarning,
					MessageKey: "warnings.health.degraded", Args: map[string]string{"component": c.Name},
				})
			case health.StatusHealthy, health.StatusUnknown:
			}
		}
	}

	if a.incidents != nil {
		// One warning per component with error-grade incidents inside the
		// window; the newest timestamp wins the display.
		type agg struct {
			count int
			last  time.Time
		}
		byComponent := map[string]*agg{}
		for _, inc := range a.incidents.Incidents() {
			sev := hmenum.IncidentSeverity(inc.Severity)
			if sev != hmenum.IncidentSeverityError && sev != hmenum.IncidentSeverityCritical {
				continue
			}
			if now.Sub(inc.When) > incidentWindow {
				continue
			}
			e := byComponent[inc.Component]
			if e == nil {
				e = &agg{}
				byComponent[inc.Component] = e
			}
			e.count++
			if inc.When.After(e.last) {
				e.last = inc.When
			}
		}
		for component, e := range byComponent {
			out = append(out, Warning{
				ID: "incident:" + component, Severity: SeverityError,
				MessageKey: "warnings.incidents",
				Args:       map[string]string{"component": component, "count": strconv.Itoa(e.count)},
			})
		}
	}

	if a.hubs != nil {
		for _, nh := range a.hubs.Hubs() {
			if nh.Hub == nil {
				continue
			}
			if n := len(nh.Hub.ServiceMessages.List()); n > 0 {
				out = append(out, Warning{
					ID: "servicemsg:" + nh.Central, Severity: SeverityWarning, Central: nh.Central,
					MessageKey: "warnings.service_messages",
					Args:       map[string]string{"central": nh.Central, "count": strconv.Itoa(n)},
				})
			}
		}
	}

	if a.pairing != nil {
		if n := a.pairing.PendingCount(); n > 0 {
			out = append(out, Warning{
				ID: "pairing:pending", Severity: SeverityWarning,
				MessageKey: "warnings.pairing_pending",
				Args:       map[string]string{"count": strconv.Itoa(n)},
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity == SeverityError
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ForUser returns the active warnings annotated with the user's
// silences, pruning silences that expired or whose warning cleared.
func (a *Aggregator) ForUser(ctx context.Context, username string) ([]Warning, error) {
	active := a.Active()
	if a.silences == nil || username == "" {
		return active, nil
	}
	now := time.Now()
	if err := a.silences.DeleteExpired(ctx, now); err != nil {
		return nil, fmt.Errorf("warnings: prune expired silences: %w", err)
	}
	ids := make([]string, len(active))
	for i, w := range active {
		ids[i] = w.ID
	}
	if err := a.silences.DeleteOtherThan(ctx, username, ids); err != nil {
		return nil, fmt.Errorf("warnings: prune cleared silences: %w", err)
	}
	silences, err := a.silences.Silences(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("warnings: load silences: %w", err)
	}
	for i := range active {
		if until, ok := silences[active[i].ID]; ok {
			u := until
			active[i].Silenced = true
			active[i].SilencedUntil = &u
		}
	}
	return active, nil
}

// SilenceDays are the periods the API accepts, mirroring the three
// choices the UI offers.
var SilenceDays = map[int]bool{1: true, 7: true, 90: true}

// ErrUnknownWarning is returned when a silence targets an ID that is not
// currently active — silencing a warning that is not there would create
// an invisible mute that fires nobody-knows-when.
var ErrUnknownWarning = errors.New("warnings: no active warning with this id")

// ErrBadPeriod is returned for a silence period outside SilenceDays.
var ErrBadPeriod = errors.New("warnings: silence period must be 1, 7 or 90 days")

// SilenceWarning mutes one active warning for the given number of days.
func (a *Aggregator) SilenceWarning(ctx context.Context, username, warningID string, days int) error {
	if a.silences == nil {
		return errors.New("warnings: no silence store wired")
	}
	if !SilenceDays[days] {
		return ErrBadPeriod
	}
	for _, w := range a.Active() {
		if w.ID == warningID {
			now := time.Now()
			return a.silences.Set(ctx, username, warningID, now.Add(time.Duration(days)*24*time.Hour), now)
		}
	}
	return ErrUnknownWarning
}

// UnsilenceWarning removes the user's silence for one warning.
func (a *Aggregator) UnsilenceWarning(ctx context.Context, username, warningID string) error {
	if a.silences == nil {
		return errors.New("warnings: no silence store wired")
	}
	return a.silences.Delete(ctx, username, warningID)
}
