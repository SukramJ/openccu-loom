// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/httpx"
)

// regaLivenessInterval is how often each CCU's `/ise/checkrega.cgi` is
// polled once the central is wired.
//
// Thirty seconds, and the number is chosen against both of its costs.
//
// The load cost: the probe is one GET against lighttpd, answered by the
// OCCU boot page's own readiness CGI — the same endpoint the CCU's WebUI
// polls in a JS loop at a far tighter cadence while the box is booting, and
// the same one this daemon's bring-up gate already hits every three seconds
// while it waits. One request per CCU per thirty seconds is roughly 2 880 a
// day, which is an order of magnitude below the sysvar and program refresh
// traffic this daemon already puts on the same web server, and it does no
// radio work at all. A CCU2 serves it out of lighttpd without touching the
// BidCoS stack.
//
// The staleness cost: this is the interval over which a hung ReGa keeps
// every sysvar, program and system score of that CCU showing its last value
// as current. It is deliberately the same thirty seconds as
// `defaultCheckConnectionSlot`, the cadence at which the daemon already
// re-examines a central's connection, so the two liveness signals that feed
// the same gate observe at the same rate rather than one of them setting
// the fleet's real detection latency on its own.
const regaLivenessInterval = 30 * time.Second

// regaLivenessProbeTimeout bounds one probe.
//
// Short on purpose: the endpoint is a static-ish CGI, and a CCU that needs
// more than five seconds to answer it is not a CCU whose ReGa is serving
// normally. It is also well inside [regaLivenessInterval], so a probe can
// never still be in flight when the next tick arrives.
const regaLivenessProbeTimeout = 5 * time.Second

// regaLivenessFailureThreshold is how many CONSECUTIVE probe failures are
// required before the tracker calls ReGa dead.
//
// This is the distinction between a negative answer and a failure to get
// one, and the two are not the same evidence. A CCU that ANSWERS with
// something other than "OK" has told us ReGa is not serving — that is the
// exact signal the OCCU boot splash reads, and it is acted on at once. A
// probe that does not complete at all is ambiguous: the CCU may be gone,
// but it may equally be a momentarily saturated lighttpd, a duty-cycle
// burst, or a DNS hiccup on the daemon's side. Acting on the first one
// would let a single transient grey out every hub entity of a healthy CCU,
// and — because the same transient tends to hit every CCU on a shared
// network at once — grey out the whole fleet together.
//
// Three consecutive failures at a thirty-second cadence means roughly
// ninety seconds of a CCU consistently not answering its own web server
// before its ReGa-scoped entities are marked stale, and the gate's own
// fifteen-second dwell sits on top of that. That is the case the
// connectivity probe provably cannot see: `Interface.listInterfaces`
// erroring changes no tracker entry (coordinators/reconciler.go:214-216),
// so before this signal existed a CCU that had stopped answering entirely
// still folded to `online`. Ninety seconds of silence is evidence; five is
// noise.
const regaLivenessFailureThreshold = 3

// regaProbeResult is one probe's verdict. The four cases are deliberately
// distinct at this level rather than collapsed into a bool the way
// [probeCCUReady] collapses them, because the gate treats them differently:
// only one of them is evidence that ReGa is down NOW, one is evidence only
// when it repeats, and one says the question cannot be asked at all.
type regaProbeResult uint8

const (
	// regaProbeServing is a 200 whose body is the literal readiness marker:
	// ReGaHss is up and answering.
	regaProbeServing regaProbeResult = iota
	// regaProbeNotServing is an ANSWER that is not the marker — the CCU's
	// web server replied, ReGa did not. This is the hung/dead-ReGa case the
	// gate exists to catch, and it is acted on immediately.
	regaProbeNotServing
	// regaProbeNoAnswer is the absence of an answer: a transport error, a
	// refused connection, a timeout, or a server error status. Ambiguous on
	// its own; only a run of them is treated as down.
	regaProbeNoAnswer
	// regaProbeUnsupported is an answer that says this daemon may not ask:
	// 401/403 (the CGI is behind auth on this firmware) or 404 (it is not
	// there at all). Never evidence about ReGa, so the tracker latches the
	// probe off for that CCU rather than drawing a conclusion from it.
	regaProbeUnsupported
)

// regaLivenessState is the per-CCU conclusion [ccuReachable] conjoins with
// the interface fold.
type regaLivenessState uint8

const (
	// regaLivenessUnknown means no usable probe result exists for this CCU:
	// none has run yet, or the endpoint turned out to be unaskable. It folds
	// to reachable — see [ccuReachable] for why absence of evidence must not
	// grey out a hub plane.
	regaLivenessUnknown regaLivenessState = iota
	// regaLivenessServing means the most recent usable evidence says ReGa is
	// answering.
	regaLivenessServing
	// regaLivenessDown means ReGa answered "not ready", or stopped answering
	// for [regaLivenessFailureThreshold] consecutive probes.
	regaLivenessDown
)

// regaLivenessTracker holds one liveness conclusion per CCU.
//
// It survives [HubMQTTPublisher.Start], which is what makes it correct
// across a broker reconnect: Start re-wires every central and restarts every
// poller, and a tracker rebuilt with it would forget a ReGa it already knows
// to be dead and let the gate's seed republish `online` for it.
type regaLivenessTracker struct {
	mu      sync.Mutex
	entries map[string]*regaLivenessEntry
}

type regaLivenessEntry struct {
	state regaLivenessState
	// failures counts CONSECUTIVE [regaProbeNoAnswer] results. Any answer —
	// serving or not — resets it, because the run is what carries the
	// evidence and a single answer in the middle breaks it.
	failures int
	// unsupported latches once the CCU has told us the endpoint cannot be
	// asked. The poller stops, and the state returns to unknown so the gate
	// behaves exactly as it did before this signal existed.
	unsupported bool
}

func newRegaLivenessTracker() *regaLivenessTracker {
	return &regaLivenessTracker{entries: map[string]*regaLivenessEntry{}}
}

// observe folds one probe result into the CCU's tracked state and reports
// the state that results.
func (t *regaLivenessTracker) observe(central string, res regaProbeResult) regaLivenessState {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entries[central]
	if e == nil {
		e = &regaLivenessEntry{}
		t.entries[central] = e
	}
	switch res {
	case regaProbeServing:
		e.failures = 0
		e.state = regaLivenessServing
	case regaProbeNotServing:
		e.failures = 0
		e.state = regaLivenessDown
	case regaProbeNoAnswer:
		e.failures++
		if e.failures >= regaLivenessFailureThreshold {
			e.state = regaLivenessDown
		}
		// Below the threshold the previous conclusion stands, unchanged: a
		// single missed probe is not an edge in either direction.
	case regaProbeUnsupported:
		e.unsupported = true
		e.failures = 0
		e.state = regaLivenessUnknown
	}
	return e.state
}

// state returns the CCU's tracked liveness, or [regaLivenessUnknown] for one
// that has never been probed.
func (t *regaLivenessTracker) state(central string) regaLivenessState {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e := t.entries[central]; e != nil {
		return e.state
	}
	return regaLivenessUnknown
}

// unsupported reports whether the CCU has latched the probe off.
func (t *regaLivenessTracker) unsupported(central string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e := t.entries[central]; e != nil {
		return e.unsupported
	}
	return false
}

// forget drops one CCU's liveness state, for a central leaving the fleet.
// The counterpart of [mqtt.Bridge.RetractHubStatus]: once the gate topic is
// retracted nothing should keep a conclusion about a CCU that is gone.
func (t *regaLivenessTracker) forget(central string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, central)
}

// regaLivenessTarget is one CCU's probe: what to call and how often.
//
// probe is a function rather than a URL so a test can drive the poller
// deterministically without an HTTP server, and so the classification in
// [probeRegaLiveness] can be tested on its own against one.
type regaLivenessTarget struct {
	interval time.Duration
	probe    func(context.Context) regaProbeResult
}

// newRegaLivenessTarget builds the production target for one central, or
// nil when cc carries no host to probe (the same guard
// [newReconnectReadinessGate] makes).
func newRegaLivenessTarget(cc *config.CentralConfig) *regaLivenessTarget {
	if cc.Host == "" {
		return nil
	}
	url := ccuBaseURLFor(*cc) + checkRegaPath
	client := regaLivenessClient(*cc)
	return &regaLivenessTarget{
		interval: regaLivenessInterval,
		probe: func(ctx context.Context) regaProbeResult {
			return probeRegaLiveness(ctx, client, url)
		},
	}
}

// regaLivenessClient is the HTTP client the probe uses: the central's
// TLS posture (including the operator's explicit insecure opt-in) with this
// probe's own short timeout rather than the JSON-RPC client's long one. A
// probe that could outlive its own poll interval would be a queue, not a
// probe.
func regaLivenessClient(cc config.CentralConfig) *http.Client {
	if c := jsonrpcHTTPClient(cc); c != nil {
		return &http.Client{Timeout: regaLivenessProbeTimeout, Transport: c.Transport}
	}
	return httpx.NewClient(regaLivenessProbeTimeout)
}

// probeRegaLiveness performs one GET and classifies the outcome.
//
// It deliberately does NOT reuse [probeCCUReady], which answers a different
// question. That one gates bring-up and folds every non-ready outcome to
// "keep waiting", which is right when the only thing that can happen next is
// another probe. Here the outcome decides whether to publish `offline` for a
// whole CCU, and "the CCU refused to answer" must not be confused with "the
// CCU answered that ReGa is not serving".
func probeRegaLiveness(ctx context.Context, client *http.Client, url string) regaProbeResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return regaProbeNoAnswer
	}
	resp, err := client.Do(req)
	if err != nil {
		return regaProbeNoAnswer
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		// fall through to the body check below.
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		drainProbeBody(resp.Body)
		return regaProbeUnsupported
	default:
		drainProbeBody(resp.Body)
		return regaProbeNoAnswer
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return regaProbeNoAnswer
	}
	if strings.TrimSpace(string(body)) == checkRegaReadyBody {
		return regaProbeServing
	}
	return regaProbeNotServing
}

// drainProbeBody reads a bounded amount so the connection can be reused —
// the same courtesy [probeCCUReady] pays on its non-200 path.
func drainProbeBody(r io.Reader) {
	_, _ = io.CopyN(io.Discard, r, 1<<10)
}
