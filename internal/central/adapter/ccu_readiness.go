// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/httpx"
)

// checkRegaPath is the CCU's own readiness endpoint. The OCCU WebUI boot
// page (the "CCU is not yet ready" splash) polls this exact CGI in a JS
// loop and only proceeds once the body is the literal "OK".
// It is part of the OCCU package, so it works uniformly across eQ-3
// CCU2/CCU3, OpenCCU and OpenCCU — the only manufacturer-sanctioned,
// SSH-free, cross-variant "system fully started" signal.
const checkRegaPath = "/ise/checkrega.cgi"

// checkRegaReadyBody is the exact body the CGI returns once ReGaHss is up
// and serving. A 200 with any other body (or a connection error while
// lighttpd is still coming up) means "still booting".
const checkRegaReadyBody = "OK"

// CCUReadinessConfig tunes [WaitForCCUReady].
type CCUReadinessConfig struct {
	// Timeout bounds the whole wait. Zero falls back to the parity default.
	// A NEGATIVE value waits indefinitely (until ctx is cancelled) — the
	// production gate uses this so a co-booting CCU is never abandoned and a
	// central never comes up in a partial, half-named state.
	Timeout time.Duration
	// Interval is the gap between probes. Zero falls back to the default.
	Interval time.Duration
	// Client overrides the HTTP client (TLS-insecure path / tests). Nil uses
	// a short-timeout default.
	Client *http.Client
}

const (
	defaultCCUReadinessTimeout  = 120 * time.Second
	defaultCCUReadinessInterval = 3 * time.Second
	defaultCCUReadinessProbeTTL = 5 * time.Second
)

// WaitForCCUReady blocks until the CCU answers `/ise/checkrega.cgi` with the
// literal body "OK", ctx is cancelled, or the configured timeout elapses. It
// returns true only when readiness was observed.
//
// This gates the per-central southbound bring-up (device names via JSON-RPC
// AND the per-interface listDevices) so it runs only once ReGaHss is serving.
// Otherwise an add-on co-started with a (re)booting CCU sees `Device.listAllDetail`
// and `listDevices` warm up at DIFFERENT times, which surfaces as devices that
// appear without their CCU-assigned names until a restart. The production gate
// (gatedCentralBringUp) passes a negative timeout to wait indefinitely so a
// central is never brought up half-loaded.
//
// Connection errors and non-OK bodies are treated identically — "keep waiting".
func WaitForCCUReady(ctx context.Context, cc config.CentralConfig, cfg CCUReadinessConfig, logger *slog.Logger) bool {
	timeout := cfg.Timeout
	unbounded := timeout < 0
	if timeout == 0 {
		timeout = defaultCCUReadinessTimeout
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultCCUReadinessInterval
	}
	client := cfg.Client
	if client == nil {
		if client = jsonrpcHTTPClient(cc); client == nil {
			client = httpx.NewClient(defaultCCUReadinessProbeTTL)
		}
	}
	url := ccuBaseURLFor(cc) + checkRegaPath

	// deadlineC fires when the bounded budget elapses; in unbounded mode it
	// stays nil so the select only resolves on readiness or ctx-cancel.
	var deadlineC <-chan time.Time
	if !unbounded {
		deadline := time.NewTimer(timeout)
		defer deadline.Stop()
		deadlineC = deadline.C
	}

	for attempt := 0; ; attempt++ {
		if probeCCUReady(ctx, client, url) {
			if logger != nil && attempt > 0 {
				logger.Info("wire.ccu_ready",
					slog.String("central", cc.Name),
					slog.Int("probes", attempt+1))
			}
			return true
		}
		if attempt == 0 && logger != nil {
			// Only log the wait once, at the point we discover the CCU is not
			// ready — a ready CCU returns on the first probe and stays quiet.
			logger.Info("wire.ccu_not_ready_waiting",
				slog.String("central", cc.Name),
				slog.String("probe", url),
				slog.Bool("unbounded", unbounded))
		}

		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return false
		case <-deadlineC:
			t.Stop()
			if logger != nil {
				logger.Warn("wire.ccu_ready_timeout",
					slog.String("central", cc.Name),
					slog.Duration("waited", timeout))
			}
			return false
		case <-t.C:
		}
	}
}

// probeCCUReady performs a single GET and reports whether the body is the
// literal readiness marker. Any error (connection refused while lighttpd is
// still starting, non-200, non-OK body) reports false.
func probeCCUReady(ctx context.Context, client *http.Client, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// Drain a bounded amount so the connection can be reused.
		_, _ = io.CopyN(io.Discard, resp.Body, 1<<10)
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(body)) == checkRegaReadyBody
}

// reconnectReadinessTimeout bounds the readiness wait a reconnect performs.
// Unlike the boot gate — which waits indefinitely rather than bring a central
// up half-loaded — a reconnect must return to its caller: the client state
// machine drives its own backoff loop, and blocking here would stall the
// transitions that loop depends on. A CCU still booting after this long is
// simply retried on the next cycle.
const reconnectReadinessTimeout = 30 * time.Second

// activateReadinessProbeTimeout bounds the readiness re-check
// wireInterface's activate() performs immediately before Deinit/Init on
// every ingest-loop attempt, not just the first. Deliberately short: the
// one-time outer gate (gatedCentralBringUp) already waited out the CCU's
// initial boot, so this probe only needs to catch a second drop inside the
// activate retry window, not wait out a whole reboot — that is what the
// retry loop's own backoff is for.
const activateReadinessProbeTimeout = 5 * time.Second

// newReconnectReadinessGate returns the readiness gate the reconnect path
// consults before re-registering its callback with the CCU, or nil when cc
// carries no host to probe.
//
// It exists because a rebooting CCU serves XML-RPC before it is fully up. The
// `deinit` that precedes a re-registration then fails while the `init`
// succeeds, leaving the previous registration in place: the CCU keeps both and
// pushes every event twice, once per registration. Anything reacting to those
// events runs twice as well, which is what surfaced as CCU programs executing
// twice after a restart.
func newReconnectReadinessGate(cc config.CentralConfig, logger *slog.Logger) func(context.Context) bool {
	if cc.Host == "" {
		return nil
	}
	return func(ctx context.Context) bool {
		return WaitForCCUReady(ctx, cc, CCUReadinessConfig{Timeout: reconnectReadinessTimeout}, logger)
	}
}
