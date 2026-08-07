// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

// ccu_wiring_activate_readiness_test.go pins the per-attempt CCU-readiness
// gate wireInterface's activate() consults immediately before Deinit/Init on
// every ingestLoop retry, not just the first — see the activate()
// readiness-probe comment in ccu_wiring.go and [activateReadinessProbeTimeout]
// in ccu_readiness.go.
//
// This drives the real wireInterface function end-to-end (a real backend
// wired to a real xmlrpc.Client, a real WaitForCCUReady HTTP probe) rather
// than reimplementing activate()'s gate check by hand: a test that hand-rolls
// the collaboration proves only that the pieces CAN work together, not that
// the production ingestLoop actually consults the gate before touching the
// wire. The fake CCU below answers both `/ise/checkrega.cgi` (readiness) and
// the XML-RPC `listDevices` / `init` calls wireInterface issues.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// gatedActivateCCU is a fake CCU that answers both the XML-RPC endpoint
// wireInterface's backend calls and the checkrega.cgi readiness probe
// activate() consults before Deinit/Init.
//
// Readiness is keyed off listDevicesCalls rather than a wall-clock timer or
// a test-driven flip: the CCU only reports ready once the ingest attempt
// that is ALLOWED to succeed (the third listDevices call) has been issued.
// This makes the scenario deterministic by construction — the CCU cannot
// report ready during the retry attempt under test (the second listDevices
// call) regardless of scheduling jitter, because nothing advances
// listDevicesCalls to 3 until that attempt has already failed and backed
// off.
type gatedActivateCCU struct {
	mu               sync.Mutex
	listDevicesCalls int
	checkregaProbes  int
	calls            []recordedCall // every "init"-method call (Deinit and Init both use it)
	srv              *httptest.Server
}

func newGatedActivateCCU(t *testing.T) *gatedActivateCCU {
	t.Helper()
	f := &gatedActivateCCU{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *gatedActivateCCU) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == checkRegaPath {
		f.mu.Lock()
		f.checkregaProbes++
		ready := f.listDevicesCalls >= 3
		f.mu.Unlock()
		if ready {
			_, _ = w.Write([]byte(checkRegaReadyBody))
		} else {
			_, _ = w.Write([]byte("ReGaHss not ready"))
		}
		return
	}

	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	call := parseXMLRPCCall(string(body))
	w.Header().Set("Content-Type", "text/xml")

	switch call.method {
	case "listDevices":
		f.mu.Lock()
		f.listDevicesCalls++
		n := f.listDevicesCalls
		f.mu.Unlock()
		if n == 1 {
			// The first ingest attempt fails with an unclassified CCU
			// fault code (not in the retrier's retryable set), so the
			// client retrier's isNonRetryable check short-circuits
			// after exactly one try instead of masking the ingestLoop
			// retry under test behind its own internal backoff. This
			// forces wireInterface's ingestLoop into a genuine retry —
			// the scenario the fix targets is a retry attempt, not the
			// first one (the one-time outer gate already covers that).
			_, _ = w.Write([]byte(gateTestFaultXML(-99, "synthetic ingest failure")))
			return
		}
		_, _ = w.Write([]byte(`<?xml version="1.0"?><methodResponse><params><param>` +
			`<value><array><data/></array></value></param></params></methodResponse>`))
	case "init":
		// Both Deinit (1 param: callback URL) and Init (2 params: callback
		// URL, interface ID) ride the CCU's "init" method — see
		// xmlrpcAnnouncer's doc comment. Recording every call lets the
		// test assert on arity to tell them apart, mirroring
		// announcer_wire_shape_test.go's assertInitDeinitShape.
		f.mu.Lock()
		f.calls = append(f.calls, call)
		f.mu.Unlock()
		_, _ = w.Write([]byte(`<?xml version="1.0"?><methodResponse><params><param>` +
			`<value><string></string></value></param></params></methodResponse>`))
	default:
		_, _ = w.Write([]byte(`<?xml version="1.0"?><methodResponse><params><param>` +
			`<value><string></string></value></param></params></methodResponse>`))
	}
}

// snapshot returns a consistent, race-free read of the counters a caller
// needs to reason about ingest-attempt progress and wire calls observed so
// far.
func (f *gatedActivateCCU) snapshot() (listDevicesCalls int, calls []recordedCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedCall, len(f.calls))
	copy(out, f.calls)
	return f.listDevicesCalls, out
}

// gateTestFaultXML renders a minimal XML-RPC <fault> response. Code -99 is
// deliberately not one of pkg/hmerr's retryable CCU fault codes, so the
// client's own internal retrier gives up after a single attempt instead of
// retrying underneath wireInterface's ingestLoop.
func gateTestFaultXML(code int, message string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="ISO-8859-1"?>`+
		`<methodResponse><fault><value><struct>`+
		`<member><name>faultCode</name><value><i4>%d</i4></value></member>`+
		`<member><name>faultString</name><value><string>%s</string></value></member>`+
		`</struct></value></fault></methodResponse>`, code, message)
}

// TestWireInterfaceActivateRetryGatesOnCCUReadiness drives the real
// wireInterface function against a fake CCU and proves the per-attempt
// readiness gate added to activate(): on a retry attempt (not the first),
// while the CCU still reports not-ready, backend.Deinit/Init must not fire —
// and once the CCU reports ready, a subsequent attempt must go on to call
// them.
//
// Timeline (all durations are wireInterface's own hardcoded constants, not
// test-configurable — see ccu_wiring.go's ingestBackoff and
// activateReadinessProbeTimeout in ccu_readiness.go):
//
//  1. Attempt 0: listDevices fails (forced) -> ingestLoop backs off 1s.
//  2. Attempt 1 (the retry under test): listDevices succeeds, but the CCU
//     is not yet "ready" (by construction, since readiness is keyed off a
//     3rd listDevices call that has not happened yet) -> the readiness gate
//     blocks for its full 5s timeout -> activate() returns an error without
//     ever touching Deinit/Init -> ingestLoop backs off 2s.
//  3. Attempt 2: listDevices succeeds AND the CCU now reports ready (this is
//     the call that flips readiness) -> the gate passes immediately ->
//     Deinit then Init fire on the real wire.
func TestWireInterfaceActivateRetryGatesOnCCUReadiness(t *testing.T) {
	t.Parallel()

	ccu := newGatedActivateCCU(t)

	u, err := url.Parse(ccu.srv.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("atoi port: %v", err)
	}

	cc := config.CentralConfig{
		Name: "ccu-activate-gate",
		Host: host,
		// Both the XML-RPC endpoint (interfaceURL -> interfacePortOverride)
		// and the checkrega.cgi readiness probe (ccuBaseURLFor) are pointed
		// at the same fake server.
		Port:        port,
		JSONRPCPort: port,
	}

	unit, err := central.New(central.Config{Name: cc.Name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	pipeline := NewDevicePipeline(unit)
	writer := client.NewValueWriter()
	logger := slog.New(slog.DiscardHandler)

	const callbackURL = "http://127.0.0.1:9/RPC2/ccu-activate-gate"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type wireResult struct {
		closer func()
		err    error
	}
	resultCh := make(chan wireResult, 1)
	go func() {
		closer, err := wireInterface(
			ctx, cc, hmenum.InterfaceHmIPRF, unit, pipeline, writer,
			nil, // runner: nil skips the ReGa fetch_all_device_data call: not needed
			// to observe listDevices/init/deinit, and it would require
			// faking a second (JSON-RPC) surface.
			callbackURL,
			config.ReliabilityConfig{},
			nil, // masterValues: HmIP-RF is gated to a nil MasterPoller (see
			// newMasterPollerForInterface/isHmIPInterface), so it is never
			// dereferenced.
			newBackendRegistry(),
			nil, // jsonCaller: CcuBackend only needs XML-RPC for the methods
			// this scenario exercises (listDevices, Init, Deinit).
			nil, "", // BIN-RPC callback server/addr: unused outside the CUxD branch.
			logger,
		)
		resultCh <- wireResult{closer, err}
	}()

	// Watches for the defect this test guards against: a Deinit/Init call
	// landing on the wire before the CCU-readiness probe has seen the ready
	// signal for the current ingest attempt (listDevicesCalls < 3). Without
	// the fix, attempt 1 would call Deinit/Init directly after its
	// (successful) listDevices call, i.e. while listDevicesCalls == 2.
	var prematureCall atomic.Bool
	stopWatch := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopWatch:
				return
			case <-ticker.C:
				n, calls := ccu.snapshot()
				if n < 3 && len(calls) > 0 {
					prematureCall.Store(true)
				}
			}
		}
	}()

	var result wireResult
	select {
	case result = <-resultCh:
	case <-time.After(20 * time.Second):
		t.Fatal("wireInterface did not return within 20s")
	}
	close(stopWatch)
	<-watchDone

	if result.closer != nil {
		t.Cleanup(result.closer)
	}
	if result.err != nil {
		t.Fatalf("wireInterface returned an error: %v", result.err)
	}

	if prematureCall.Load() {
		t.Fatal("backend.Deinit/Init were called before the CCU reported ready on a retry attempt — " +
			"the activate() readiness gate did not block them")
	}

	listDevicesCalls, calls := ccu.snapshot()
	if listDevicesCalls != 3 {
		t.Fatalf("listDevices was called %d times, want exactly 3 "+
			"(1 forced failure, 1 blocked-by-not-ready retry, 1 that finally reached a ready CCU)",
			listDevicesCalls)
	}

	if len(calls) != 2 {
		t.Fatalf("want exactly 2 wire calls (deinit, init) once the CCU reported ready, got %d: %+v",
			len(calls), calls)
	}
	deinitCall, initCall := calls[0], calls[1]
	if deinitCall.method != "init" || len(deinitCall.params) != 1 {
		t.Fatalf("deinit: want method init with 1 param, got %s/%d: %+v",
			deinitCall.method, len(deinitCall.params), deinitCall)
	}
	if deinitCall.params[0] != callbackURL {
		t.Errorf("deinit param = %q, want callback URL %q", deinitCall.params[0], callbackURL)
	}
	if initCall.method != "init" || len(initCall.params) != 2 {
		t.Fatalf("init: want method init with 2 params, got %s/%d: %+v",
			initCall.method, len(initCall.params), initCall)
	}
	if initCall.params[0] != callbackURL {
		t.Errorf("init param[0] = %q, want callback URL %q", initCall.params[0], callbackURL)
	}
}
