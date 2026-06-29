// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ssdp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"
)

const (
	maxDescriptionBytes = 64 * 1024
	fetchTimeout        = 5 * time.Second
	// staleAfter drops a CCU that has not answered for this long, so a central
	// taken off the network disappears from the list within a few scans.
	staleAfter = 5 * time.Minute
)

// Discoverer runs the periodic SSDP scan and holds the set of currently-seen
// central units. It follows the Start/Stop lifecycle of the mDNS advertiser so
// the daemon wires and tears it down the same way. List() is safe for
// concurrent reads from REST handlers while the scan loop writes.
type Discoverer struct {
	interval time.Duration
	logger   *slog.Logger
	http     *http.Client
	now      func() time.Time

	mu     sync.RWMutex
	found  map[string]DiscoveredCCU
	cancel context.CancelFunc
	done   chan struct{}
}

// New builds a Discoverer scanning every interval. A nil logger defaults to
// slog.Default(); interval ≤ 0 falls back to 60s.
func New(interval time.Duration, logger *slog.Logger) *Discoverer {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &Discoverer{
		interval: interval,
		logger:   logger,
		http:     &http.Client{Timeout: fetchTimeout},
		now:      time.Now,
		found:    make(map[string]DiscoveredCCU),
	}
}

// Start launches the scan loop in the background and returns immediately. The
// loop runs until the passed ctx is cancelled or Stop is called.
func (d *Discoverer) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.cancel = cancel
	d.done = make(chan struct{})
	d.mu.Unlock()
	go d.loop(ctx)
	return nil
}

// Stop cancels the scan loop and waits for it to exit.
func (d *Discoverer) Stop() error {
	d.mu.Lock()
	cancel, done := d.cancel, d.done
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return nil
}

func (d *Discoverer) loop(ctx context.Context) {
	defer close(d.done)
	d.scan(ctx) // initial scan so the UI has data without waiting a full interval
	t := time.NewTicker(d.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.scan(ctx)
		}
	}
}

// scan runs one full discovery cycle: M-SEARCH on every source interface,
// fetch + parse each responder's device description, refresh the found-set, and
// drop entries that have gone stale.
func (d *Discoverer) scan(ctx context.Context) {
	locations := make(map[string]struct{})
	for _, ip := range multicastSourceIPs() {
		locs, err := searchFrom(ctx, ip)
		if err != nil {
			d.logger.Debug("discovery.ssdp.search_failed",
				slog.String("src", ip.String()), slog.String("err", err.Error()))
			continue
		}
		for _, l := range locs {
			locations[l] = struct{}{}
		}
	}

	now := d.now()
	for loc := range locations {
		if ctx.Err() != nil {
			return
		}
		ccu, ok := d.fetch(ctx, loc)
		if !ok {
			continue
		}
		ccu.LastSeen = now
		d.mu.Lock()
		d.found[ccu.Serial] = ccu
		d.mu.Unlock()
	}

	d.mu.Lock()
	for serial, ccu := range d.found {
		if now.Sub(ccu.LastSeen) > staleAfter {
			delete(d.found, serial)
		}
	}
	d.mu.Unlock()
}

// fetch GETs a device-description URL and parses it into a DiscoveredCCU.
func (d *Discoverer) fetch(ctx context.Context, loc string) (DiscoveredCCU, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, http.NoBody)
	if err != nil {
		return DiscoveredCCU{}, false
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return DiscoveredCCU{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return DiscoveredCCU{}, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDescriptionBytes))
	if err != nil {
		return DiscoveredCCU{}, false
	}
	return parseDeviceDescription(body, loc)
}

// List returns the currently-seen central units, sorted by name for a stable
// display order. Never returns nil.
func (d *Discoverer) List() []DiscoveredCCU {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]DiscoveredCCU, 0, len(d.found))
	for _, ccu := range d.found {
		out = append(out, ccu)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Serial < out[j].Serial
	})
	return out
}
