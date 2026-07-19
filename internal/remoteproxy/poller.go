// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package remoteproxy

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// InstanceStatus is one instance's last probe result, rendered on the
// overview page and served as JSON on /-/status.
type InstanceStatus struct {
	Name string `json:"name"`
	// Status is the remote daemon's composite health when reachable
	// (healthy | degraded | unhealthy), "unreachable" on probe failure,
	// and "unknown" before the first probe completes.
	Status     string    `json:"status"`
	Version    string    `json:"version,omitempty"`
	APIVersion string    `json:"api_version,omitempty"`
	Uptime     string    `json:"uptime,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

// Probe statuses beyond the remote daemon's own health vocabulary.
const (
	statusUnknown     = "unknown"
	statusUnreachable = "unreachable"
)

// defaultProbeInterval balances tile freshness against load on remote
// daemons: two read-only requests per instance per tick.
const defaultProbeInterval = 15 * time.Second

// probeTimeout bounds one status request; a wedged remote must not
// stall the poll loop into the next tick.
const probeTimeout = 5 * time.Second

// poller keeps per-instance status snapshots fresh in the background.
type poller struct {
	instances []*instanceProxy
	interval  time.Duration
	clk       clock.Clock
	log       *slog.Logger

	mu       sync.RWMutex
	statuses map[string]InstanceStatus
}

func newPoller(instances []*instanceProxy, clk clock.Clock, log *slog.Logger) *poller {
	p := &poller{
		instances: instances,
		interval:  defaultProbeInterval,
		clk:       clk,
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
		out = append(out, p.statuses[ip.inst.Name])
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
	st := InstanceStatus{Name: ip.inst.Name, CheckedAt: p.clk.Now()}

	var health healthProbeResponse
	// /api/v1/health answers 200 or, when unhealthy, 503 — both carry
	// the JSON body, so the status code is deliberately not checked.
	if err := ip.getJSON(ctx, "/api/v1/health", &health); err != nil || health.Status == "" {
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

// getJSON issues one bounded GET against the instance and decodes the
// JSON body. It reuses the proxy transport (connection pool, TLS mode)
// and the instance token, mirroring what a proxied browser call sees.
func (ip *instanceProxy) getJSON(ctx context.Context, path string, into any) error {
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, ip.target.String()+path, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if ip.inst.Token != "" {
		req.Header.Set("Authorization", "Bearer "+ip.inst.Token)
	}
	resp, err := ip.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return json.NewDecoder(resp.Body).Decode(into)
}
