// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package remoteproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// InstanceStatus is one instance's last probe result, rendered on the
// overview page and served as JSON on /-/status.
// loom:reachable:reason="marshaled reflectively into the /-/status JSON feed and rendered by the overview html/template; reflection use is invisible to the SSA walk"
type InstanceStatus struct {
	Name string `json:"name"`
	// Status is the remote daemon's composite health when reachable
	// (healthy | degraded | unhealthy), "unreachable" on probe failure,
	// "unauthorized" when the remote or the box in front of it refuses
	// the credential (401/403), and "unknown" before the first probe.
	Status string `json:"status"`
	// Auth names the credential the proxy injects: "options", "paired"
	// or empty. It is read live, not from the last probe.
	Auth       string    `json:"auth,omitempty"`
	Version    string    `json:"version,omitempty"`
	APIVersion string    `json:"api_version,omitempty"`
	Uptime     string    `json:"uptime,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

// Probe statuses beyond the remote daemon's own health vocabulary.
const (
	statusUnknown      = "unknown"
	statusUnreachable  = "unreachable"
	statusUnauthorized = "unauthorized"
)

// defaultProbeInterval balances tile freshness against load on remote
// daemons: two read-only requests per instance per tick.
const defaultProbeInterval = 15 * time.Second

// probeTimeout bounds one status request; a wedged remote must not
// stall the poll loop into the next tick.
const probeTimeout = 5 * time.Second

// maxProbeBody caps how much of a probe response is buffered for JSON
// decoding — a compromised or broken remote must not be able to feed
// the poller an unbounded body. Real health/info payloads are a few KB.
const maxProbeBody = 1 << 20

// poller keeps per-instance status snapshots fresh in the background.
type poller struct {
	instances []*instanceProxy
	interval  time.Duration
	log       *slog.Logger

	mu       sync.RWMutex
	statuses map[string]InstanceStatus
}

func newPoller(instances []*instanceProxy, log *slog.Logger) *poller {
	p := &poller{
		instances: instances,
		interval:  defaultProbeInterval,
		log:       log,
		statuses:  make(map[string]InstanceStatus, len(instances)),
	}
	for _, ip := range instances {
		p.statuses[ip.inst.Name] = InstanceStatus{Name: ip.inst.Name, Status: statusUnknown}
	}
	return p
}

// start launches one probe loop per instance. The loops stop when ctx
// is canceled; probes are staggered so a fleet of instances is not hit
// in lockstep.
func (p *poller) start(ctx context.Context) {
	for i, ip := range p.instances {
		go p.loop(ctx, ip, time.Duration(i)*250*time.Millisecond)
	}
}

func (p *poller) loop(ctx context.Context, ip *instanceProxy, stagger time.Duration) {
	timer := time.NewTimer(stagger)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		p.store(p.probe(ctx, ip))
		timer.Reset(p.interval)
	}
}

func (p *poller) store(st InstanceStatus) {
	p.mu.Lock()
	p.statuses[st.Name] = st
	p.mu.Unlock()
}

// snapshot returns the statuses in configured instance order.
func (p *poller) snapshot() []InstanceStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]InstanceStatus, 0, len(p.instances))
	for _, ip := range p.instances {
		st := p.statuses[ip.inst.Name]
		st.Auth = ip.authSource()
		out = append(out, st)
	}
	return out
}

// healthProbeResponse is the subset of `GET /api/v1/health` the tiles
// need; infoProbeResponse the subset of `GET /api/v1/info`.
type healthProbeResponse struct {
	Status string `json:"status"`
}

type infoProbeResponse struct {
	Version    string `json:"version"`
	APIVersion string `json:"api_version"`
	Uptime     string `json:"uptime"`
}

// probe performs the two read-only status requests against one remote
// instance. The health endpoint decides reachability/status; the info
// endpoint enriches the tile and is best-effort.
func (p *poller) probe(ctx context.Context, ip *instanceProxy) InstanceStatus {
	st := InstanceStatus{Name: ip.inst.Name, CheckedAt: time.Now()}

	var health healthProbeResponse
	// /api/v1/health answers 200 or, when unhealthy, 503 — both carry
	// the JSON body, so the status code is deliberately not checked.
	err := ip.getJSON(ctx, "/api/v1/health", &health)
	var refused *refusedError
	if errors.As(err, &refused) {
		st.Status = statusUnauthorized
		// Only a 401 says the token itself is gone (unknown or expired on
		// the box); a 403 is a token that is valid but does not open this
		// path, which a new pairing would not change by itself.
		if refused.status == http.StatusUnauthorized && ip.authSource() == authSourcePaired {
			ip.forgetPaired("the box no longer accepts the paired token")
		}
		return st
	}
	if err != nil || health.Status == "" {
		if err != nil {
			p.log.Debug("health probe failed", "instance", ip.inst.Name, "error", err)
		}
		st.Status = statusUnreachable
		return st
	}
	st.Status = health.Status

	var info infoProbeResponse
	if err := ip.getJSON(ctx, "/api/v1/info", &info); err == nil {
		st.Version = info.Version
		st.APIVersion = info.APIVersion
		st.Uptime = info.Uptime
	}
	return st
}

// refusedError is a probe the remote (or the box gate in front of it)
// refused with 401 or 403.
type refusedError struct{ status int }

func (e *refusedError) Error() string { return fmt.Sprintf("credential refused with %d", e.status) }

// getJSON issues one bounded GET against the instance and decodes the
// JSON body. It reuses the proxy transport (connection pool, TLS mode)
// and the instance token, mirroring what a proxied browser call sees.
// A 401 or 403 is a *refusedError.
func (ip *instanceProxy) getJSON(ctx context.Context, path string, into any) error {
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, ip.target.String()+path, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if token := ip.authToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := ip.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &refusedError{status: resp.StatusCode}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxProbeBody)).Decode(into)
}
