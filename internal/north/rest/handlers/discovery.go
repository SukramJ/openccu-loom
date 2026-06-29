// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/discovery/ssdp"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// DiscoveredCentralLister yields the central units currently seen on the LAN
// by the SSDP discoverer.
type DiscoveredCentralLister interface {
	List() []ssdp.DiscoveredCCU
}

// DiscoveryIgnoreStore persists the operator's "ignore this CCU" decisions.
type DiscoveryIgnoreStore interface {
	Add(ctx context.Context, e sqlite.IgnoredCCU) error
	Remove(ctx context.Context, serial string) (bool, error)
	List(ctx context.Context) ([]sqlite.IgnoredCCU, error)
	IgnoredSerials(ctx context.Context) (map[string]struct{}, error)
}

// ConfiguredCentralLister reads the already-configured centrals so discovery
// results can be marked "already configured".
type ConfiguredCentralLister interface {
	List(ctx context.Context) ([]sqlite.CentralRow, error)
}

// DiscoveryDeps bundles the dependencies of the discovery endpoints. A nil
// Discoverer (discovery disabled) makes the list endpoint return an empty set
// rather than an error, so the SPA can always render the surface.
type DiscoveryDeps struct {
	Discoverer DiscoveredCentralLister
	Ignore     DiscoveryIgnoreStore
	Centrals   ConfiguredCentralLister
}

// discoveredCCU is the wire shape of one discovered central.
type discoveredCCU struct {
	Serial            string    `json:"serial"`
	Name              string    `json:"name"`
	Host              string    `json:"host"`
	Manufacturer      string    `json:"manufacturer,omitempty"`
	Model             string    `json:"model,omitempty"`
	LastSeen          time.Time `json:"last_seen"`
	AlreadyConfigured bool      `json:"already_configured"`
}

// ListDiscoveredCCUs returns the central units found on the LAN, excluding the
// ones the operator ignored, each flagged whether it is already configured.
func ListDiscoveredCCUs(d *DiscoveryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := []discoveredCCU{}
		if d == nil || d.Discoverer == nil {
			JSON(w, http.StatusOK, out)
			return
		}
		ignored := map[string]struct{}{}
		if d.Ignore != nil {
			if set, err := d.Ignore.IgnoredSerials(r.Context()); err == nil {
				ignored = set
			}
		}
		configuredHosts := configuredHostSet(r.Context(), d.Centrals)
		for _, c := range d.Discoverer.List() {
			if _, skip := ignored[c.Serial]; skip {
				continue
			}
			out = append(out, discoveredCCU{
				Serial:            c.Serial,
				Name:              c.Name,
				Host:              c.Host,
				Manufacturer:      c.Manufacturer,
				Model:             c.Model,
				LastSeen:          c.LastSeen,
				AlreadyConfigured: hostConfigured(c.Host, configuredHosts),
			})
		}
		JSON(w, http.StatusOK, out)
	}
}

// IgnoreDiscoveredCCU records an ignore decision for the CCU named by the path
// serial, so it stops appearing in the discovery list.
func IgnoreDiscoveredCCU(d *DiscoveryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil || d.Ignore == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Discovery unavailable", "no ignore store"))
			return
		}
		serial := strings.TrimSpace(chi.URLParam(r, "serial"))
		if serial == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Missing serial", "path parameter serial is required"))
			return
		}
		// Capture name/host from the live discovery set for a friendlier
		// "ignored" management view; absence is fine (ignore still works).
		entry := sqlite.IgnoredCCU{Serial: serial, IgnoredBy: actorSubject(r), IgnoredAt: time.Now().UTC()}
		if d.Discoverer != nil {
			for _, c := range d.Discoverer.List() {
				if c.Serial == serial {
					entry.Name, entry.Host = c.Name, c.Host
					break
				}
			}
		}
		if err := d.Ignore.Add(r.Context(), entry); err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "Ignore failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// UnignoreDiscoveredCCU removes an ignore decision so the CCU can reappear.
func UnignoreDiscoveredCCU(d *DiscoveryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil || d.Ignore == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Discovery unavailable", "no ignore store"))
			return
		}
		serial := strings.TrimSpace(chi.URLParam(r, "serial"))
		if serial == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Missing serial", "path parameter serial is required"))
			return
		}
		removed, err := d.Ignore.Remove(r.Context(), serial)
		if err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "Un-ignore failed", err.Error()))
			return
		}
		if !removed {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Not ignored", "no ignore entry for that serial"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ListIgnoredCCUs returns the persisted ignore list so the SPA can offer an
// "un-ignore" management view.
func ListIgnoredCCUs(d *DiscoveryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := []sqlite.IgnoredCCU{}
		if d != nil && d.Ignore != nil {
			if list, err := d.Ignore.List(r.Context()); err == nil {
				out = list
			}
		}
		JSON(w, http.StatusOK, out)
	}
}

// configuredHostSet returns the hosts of every configured central, lower-cased
// for case-insensitive comparison. Empty on error / nil dependency.
func configuredHostSet(ctx context.Context, centrals ConfiguredCentralLister) map[string]struct{} {
	set := map[string]struct{}{}
	if centrals == nil {
		return set
	}
	rows, err := centrals.List(ctx)
	if err != nil {
		return set
	}
	for i := range rows {
		if h := strings.ToLower(strings.TrimSpace(rows[i].Host)); h != "" {
			set[h] = struct{}{}
		}
	}
	return set
}

func hostConfigured(host string, configured map[string]struct{}) bool {
	_, ok := configured[strings.ToLower(strings.TrimSpace(host))]
	return ok
}

// actorSubject returns the authenticated subject for audit metadata, or "".
func actorSubject(r *http.Request) string {
	if id, ok := auth.IdentityFrom(r.Context()); ok {
		return id.Subject
	}
	return ""
}
