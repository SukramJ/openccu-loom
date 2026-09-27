// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
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

// CCUReadinessConfig tunes [waitReady].
type CCUReadinessConfig struct {
	// Timeout bounds the whole wait. Zero falls back to the parity default.
	// A NEGATIVE value waits indefinitely (until ctx is cancelled) — the
	// production gate uses this so a co-booting CCU is never abandoned and a
	// central never comes up in a partial, half-named state.
	Timeout time.Duration
	// Interval is the gap between probes. Zero falls back to the default.
	Interval time.Duration
}

const (
	defaultCCUReadinessTimeout  = 120 * time.Second
	defaultCCUReadinessInterval = 3 * time.Second
	defaultCCUReadinessProbeTTL = 5 * time.Second
)

// waitReady drives p until it reports ready, ctx is cancelled, or the
// configured timeout elapses, and returns true only when readiness was
// observed. The loop — timeouts, cadence, log keys — is the same for every
// system; the probe supplies what "ready" means.
func waitReady(ctx context.Context, name string, p ReadinessProbe, cfg CCUReadinessConfig, logger *slog.Logger) bool {
	timeout := cfg.Timeout
	unbounded := timeout < 0
	if timeout == 0 {
		timeout = defaultCCUReadinessTimeout
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultCCUReadinessInterval
	}

	// deadlineC fires when the bounded budget elapses; in unbounded mode it
	// stays nil so the select only resolves on readiness or ctx-cancel.
	var deadlineC <-chan time.Time
	if !unbounded {
		deadline := time.NewTimer(timeout)
		defer deadline.Stop()
		deadlineC = deadline.C
	}

	for attempt := 0; ; attempt++ {
		ready, reason := p.Probe(ctx)
		if ready {
			if logger != nil && attempt > 0 {
				logger.Info("wire.ccu_ready",
					slog.String("central", name),
					slog.Int("probes", attempt+1))
			}
			return true
		}
		if attempt == 0 && logger != nil {
			// Only log the wait once, at the point we discover the system is
			// not ready — a ready system returns on the first probe and stays
			// quiet.
			logger.Info("wire.ccu_not_ready_waiting",
				slog.String("central", name),
				slog.String("probe", p.Target()),
				slog.String("reason", reason),
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
					slog.String("central", name),
					slog.Duration("waited", timeout))
			}
			return false
		case <-t.C:
		}
	}
}

// ccuReadinessProbe is the CCU's readiness probe: one GET of the OCCU boot
// marker CGI, ready only on the literal body "OK".
//
// It gates the per-central southbound bring-up (device names via JSON-RPC
// AND the per-interface listDevices) so it runs only once ReGaHss is serving.
// Otherwise an add-on co-started with a (re)booting CCU sees
// `Device.listAllDetail` and `listDevices` warm up at DIFFERENT times, which
// surfaces as devices that appear without their CCU-assigned names until a
// restart. The production gate (gatedCentralBringUp) waits indefinitely so a
// central is never brought up half-loaded. Connection errors and non-OK
// bodies are treated identically — "keep waiting".
type ccuReadinessProbe struct {
	client *http.Client
	url    string
}

// newCCUReadinessProbe builds the probe for cc. A nil client selects the
// central's JSON-RPC TLS posture, or a short-timeout default without one.
func newCCUReadinessProbe(cc config.CentralConfig, client *http.Client) *ccuReadinessProbe {
	if client == nil {
		if client = jsonrpcHTTPClient(cc); client == nil {
			client = httpx.NewClient(defaultCCUReadinessProbeTTL)
		}
	}
	return &ccuReadinessProbe{client: client, url: ccuBaseURLFor(cc) + checkRegaPath}
}

// Probe implements [ReadinessProbe].
func (p *ccuReadinessProbe) Probe(ctx context.Context) (ready bool, reason string) {
	return probeCCUReady(ctx, p.client, p.url)
}

// Target implements [ReadinessProbe].
func (p *ccuReadinessProbe) Target() string { return p.url }

// probeCCUReady performs a single GET and reports whether the body is the
// literal readiness marker, with the reason when it is not. Any error
// (connection refused while lighttpd is still starting, non-200, non-OK body)
// reports false.
func probeCCUReady(ctx context.Context, client *http.Client, url string) (ready bool, reason string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return false, "request: " + err.Error()
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, "unreachable: " + err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// Drain a bounded amount so the connection can be reused.
		_, _ = io.CopyN(io.Discard, resp.Body, 1<<10)
		return false, "checkrega.cgi answered " + strconv.Itoa(resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return false, "read: " + err.Error()
	}
	if strings.TrimSpace(string(body)) != checkRegaReadyBody {
		return false, "ReGa not serving yet"
	}
	return true, ""
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
func newReconnectReadinessGate(cc config.CentralConfig, probe ReadinessProbe, logger *slog.Logger) func(context.Context) bool {
	if cc.Host == "" {
		return nil
	}
	return func(ctx context.Context) bool {
		return waitReady(ctx, cc.Name, probe, CCUReadinessConfig{Timeout: reconnectReadinessTimeout}, logger)
	}
}
