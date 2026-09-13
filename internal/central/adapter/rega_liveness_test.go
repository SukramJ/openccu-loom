// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// TestCCUReachableConjoinsTheInterfaceFoldWithRegaLiveness is the defect in
// one table: the gate protects ReGa-scoped entities, so an interface fold
// alone cannot decide them.
//
// The load-bearing row is "interfaces up, ReGa down" — a ReGaHss that died
// or hung while `rfd`/`HMIPServer` keep serving. Before the conjunction it
// folded to `online`, and every sysvar, program and system score of that CCU
// kept showing its last value as current, indefinitely.
//
// The other two ReGa states are pinned here as well because they are
// decisions, not defaults: unknown must fold as if the signal did not exist
// (absence of evidence), and the two halves must be conjoined so that
// neither can veto the other.
//
// Falsifiability: drop the `&& rega != regaLivenessDown` term from
// [ccuReachable] and the two "rega down" rows fail.
func TestCCUReachableConjoinsTheInterfaceFoldWithRegaLiveness(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		interfaces map[string]bool
		rega       regaLivenessState
		want       bool
	}{
		{"interfaces up, rega serving", map[string]bool{"HmIP-RF": true}, regaLivenessServing, true},
		{"interfaces up, rega never probed", map[string]bool{"HmIP-RF": true}, regaLivenessUnknown, true},
		{"interfaces up, rega down", map[string]bool{"HmIP-RF": true}, regaLivenessDown, false},
		{"one interface down, rega serving", map[string]bool{"HmIP-RF": true, "CUxD": false}, regaLivenessServing, true},
		{"every interface down, rega serving", map[string]bool{"HmIP-RF": false}, regaLivenessServing, false},
		{"every interface down, rega down", map[string]bool{"HmIP-RF": false}, regaLivenessDown, false},
		{"nothing observed at all", nil, regaLivenessUnknown, true},
		{"nothing observed, rega down", nil, regaLivenessDown, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			conn := hub.NewConnectivity()
			for iface, up := range tc.interfaces {
				conn.OnState(iface, up)
			}
			hubModel := hub.NewHub("ccu-01").SetConnectivity(conn)
			if got := ccuReachable(hubModel, tc.rega); got != tc.want {
				t.Fatalf("ccuReachable(interfaces=%v, rega=%v) = %v, want %v",
					tc.interfaces, tc.rega, got, tc.want)
			}
		})
	}
}

// TestRegaLivenessSeparatesANegativeAnswerFromAFailureToGetOne pins the
// asymmetry that keeps a transient from flapping the fleet.
//
// An answer that is not "OK" is ReGa itself saying it is not serving, and it
// is acted on at once. A probe that does not complete is ambiguous — a
// saturated lighttpd, a duty-cycle burst, a DNS blip on the daemon's side —
// and the same transient tends to hit every CCU on a shared network
// together, so acting on one would grey out the whole fleet at once.
//
// Falsifiability: set regaLivenessFailureThreshold to 1, or route
// regaProbeNoAnswer through the same branch as regaProbeNotServing, and the
// "below the threshold" assertions fail.
func TestRegaLivenessSeparatesANegativeAnswerFromAFailureToGetOne(t *testing.T) {
	t.Parallel()

	t.Run("a not-serving answer flips immediately", func(t *testing.T) {
		t.Parallel()
		tr := newRegaLivenessTracker()
		tr.observe("ccu-01", regaProbeServing)
		if got := tr.observe("ccu-01", regaProbeNotServing); got != regaLivenessDown {
			t.Fatalf("a CCU that answered %q with something other than the readiness marker "+
				"is still %v — that is the hung-ReGa case the gate exists to catch",
				checkRegaPath, got)
		}
	})

	t.Run("failures below the threshold hold the previous conclusion", func(t *testing.T) {
		t.Parallel()
		// A threshold of one would make this whole distinction vanish: every
		// transient would be evidence, which is the fleet-wide flap the
		// constant exists to prevent. Assert it rather than let the loop
		// below silently run zero times.
		if regaLivenessFailureThreshold < 2 {
			t.Fatalf("regaLivenessFailureThreshold is %d: a single failed probe is then "+
				"enough to grey out a healthy CCU's whole hub plane",
				regaLivenessFailureThreshold)
		}
		tr := newRegaLivenessTracker()
		tr.observe("ccu-01", regaProbeServing)
		for i := 1; i < regaLivenessFailureThreshold; i++ {
			if got := tr.observe("ccu-01", regaProbeNoAnswer); got != regaLivenessServing {
				t.Fatalf("probe failure %d of %d already flipped the state to %v: one transient "+
					"would grey out every hub entity of a healthy CCU",
					i, regaLivenessFailureThreshold, got)
			}
		}
	})

	t.Run("the threshold-th consecutive failure is evidence", func(t *testing.T) {
		t.Parallel()
		tr := newRegaLivenessTracker()
		tr.observe("ccu-01", regaProbeServing)
		var got regaLivenessState
		for range regaLivenessFailureThreshold {
			got = tr.observe("ccu-01", regaProbeNoAnswer)
		}
		if got != regaLivenessDown {
			t.Fatalf("%d consecutive silences folded to %v: a CCU that has stopped answering "+
				"its own web server is the case the connectivity probe provably cannot see",
				regaLivenessFailureThreshold, got)
		}
	})

	t.Run("any answer breaks the run", func(t *testing.T) {
		t.Parallel()
		tr := newRegaLivenessTracker()
		tr.observe("ccu-01", regaProbeServing)
		for i := 1; i < regaLivenessFailureThreshold; i++ {
			tr.observe("ccu-01", regaProbeNoAnswer)
		}
		tr.observe("ccu-01", regaProbeServing)
		for i := 1; i < regaLivenessFailureThreshold; i++ {
			if got := tr.observe("ccu-01", regaProbeNoAnswer); got != regaLivenessServing {
				t.Fatalf("the failure run was not reset by an answer: state %v after %d "+
					"post-answer failures", got, i)
			}
		}
	})
}

// TestRegaLivenessLatchesOffAnEndpointTheCCURefuses is the guard against
// turning a firmware difference into a fleet-wide outage.
//
// A 401/403 (the CGI behind auth on that firmware) or a 404 (not there at
// all) is an answer about AUTHORISATION, never about ReGa. Folding it to
// "down" would grey out the hub plane of every CCU whose firmware differs;
// folding it to "serving" would claim evidence nobody gave. It latches the
// probe off instead and returns the CCU to unknown, which is exactly how the
// gate behaved before this signal existed.
//
// Falsifiability: classify 401/403/404 as regaProbeNotServing in
// [probeRegaLiveness], or drop the unsupported branch from
// [regaLivenessTracker.observe], and this fails.
func TestRegaLivenessLatchesOffAnEndpointTheCCURefuses(t *testing.T) {
	t.Parallel()
	tr := newRegaLivenessTracker()
	tr.observe("ccu-01", regaProbeNotServing)
	if got := tr.observe("ccu-01", regaProbeUnsupported); got != regaLivenessUnknown {
		t.Fatalf("an unaskable endpoint left the CCU at %v; it must return to unknown so the "+
			"gate folds as it did before the probe existed", got)
	}
	if !tr.unsupported("ccu-01") {
		t.Fatal("the probe was not latched off: the daemon would keep asking a question the " +
			"CCU has refused, every interval, forever")
	}
}

// TestProbeRegaLivenessClassifiesWhatTheCCUSaid covers the wire half: the
// four verdicts against a real HTTP server.
//
// Falsifiability: return regaProbeServing for a 200 with a non-marker body —
// the exact collapse [probeCCUReady] makes, which is right for a bring-up
// gate and wrong here — and the "still booting" case fails.
func TestProbeRegaLivenessClassifiesWhatTheCCUSaid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		status  int
		body    string
		want    regaProbeResult
		closeIt bool
	}{
		{name: "ready", status: http.StatusOK, body: "OK\n", want: regaProbeServing},
		{name: "still booting", status: http.StatusOK, body: "", want: regaProbeNotServing},
		{name: "rega not answering", status: http.StatusOK, body: "NOK", want: regaProbeNotServing},
		{name: "behind auth", status: http.StatusUnauthorized, body: "denied", want: regaProbeUnsupported},
		{name: "forbidden", status: http.StatusForbidden, body: "denied", want: regaProbeUnsupported},
		{name: "absent", status: http.StatusNotFound, body: "nope", want: regaProbeUnsupported},
		{name: "server error", status: http.StatusBadGateway, body: "boom", want: regaProbeNoAnswer},
		{name: "nothing listening", closeIt: true, want: regaProbeNoAnswer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			url := srv.URL + checkRegaPath
			if tc.closeIt {
				srv.Close()
			} else {
				defer srv.Close()
			}
			got := probeRegaLiveness(context.Background(), srv.Client(), url)
			if got != tc.want {
				t.Fatalf("probeRegaLiveness = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAHungRegaPublishesOfflineWhileEveryInterfaceIsUp is the end-to-end
// claim, and it is the user-visible defect: the CCU's radio processes keep
// serving, every interface reads reachable, and the gate has to say `offline`
// anyway because the values behind it are frozen.
//
// It drives one poll directly rather than waiting out the ticker, and it does
// so on a publisher that was never started — [HubMQTTPublisher.publish] then
// runs the job inline — so the assertion is about the fold and the byte, not
// about scheduling. It is also the poll's FIRST observation for this CCU,
// which is the one write the gate's dwell exempts; the dwell itself is the
// bridge's and is tested there.
//
// Falsifiability: make [HubMQTTPublisher.publishCCUReachability] ignore the
// tracker (pass regaLivenessUnknown) and the offline case fails.
func TestAHungRegaPublishesOfflineWhileEveryInterfaceIsUp(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		result regaProbeResult
		want   string
	}{
		{"rega serving", regaProbeServing, "online"},
		{"rega answered that it is not serving", regaProbeNotServing, "offline"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, pub, publisher := hubDiscoveryFixture(t)
			conn := hub.NewConnectivity()
			conn.OnState("HmIP-RF", true)
			conn.OnState("BidCos-RF", true)
			c.HubModel.SetConnectivity(conn)

			target := &regaLivenessTarget{
				interval: time.Hour,
				probe:    func(context.Context) regaProbeResult { return tc.result },
			}
			b := publisher.wiring.Bridge()
			if ok := publisher.pollRegaLivenessOnce(
				context.Background(), b, "ccu-01", c.HubModel, target,
			); !ok {
				t.Fatal("the poller stopped after a usable probe result")
			}

			var payload string
			var seen bool
			for _, p := range pub.Published() {
				if p.Topic == "openccu-loom/ccu-01/hub/status" {
					payload, seen = string(p.Payload), true
				}
			}
			if !seen {
				t.Fatalf("the per-CCU gate was never written; topics=%v", publishedTopics(pub))
			}
			if payload != tc.want {
				t.Fatalf("hub/status = %q, want %q — every interface of this CCU reads "+
					"reachable, so only the ReGa half can decide it", payload, tc.want)
			}
		})
	}
}

// TestTheRegaTrackerSurvivesARewire is a claim about what a broker reconnect
// must NOT undo.
//
// [HubMQTTPublisher.Start] runs again on every broker connect and re-wires
// every central from scratch. The gate's own entries survive that by design
// (Reset re-opens levels without forgetting CCUs) and the first write after
// a reset is exempt from the dwell — so a liveness tracker rebuilt with the
// wiring would forget a ReGa already known to be dead and immediately
// republish a retained `online` for a CCU whose sysvars are frozen.
//
// Falsifiability: move the `rega: newRegaLivenessTracker()` initialisation
// out of the constructor and into Start, and this fails.
func TestTheRegaTrackerSurvivesARewire(t *testing.T) {
	t.Parallel()
	c, _, publisher := hubDiscoveryFixture(t)
	c.SetSystemInformation(central.SystemInfo{Serial: "3014F711A0001F0123456789"})
	publisher.rega.observe("ccu-01", regaProbeNotServing)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	if got := publisher.rega.state("ccu-01"); got != regaLivenessDown {
		t.Fatalf("the re-wire forgot that ReGa is down (state %v): the gate's first write "+
			"after a reset is exempt from the dwell, so this republishes `online` for a "+
			"CCU whose sysvars are frozen", got)
	}
}

// TestRegaLivenessTargetsSkipACentralWithoutAHost pins the one configuration
// that cannot be probed. A central with no host has no URL to build, and a
// central with no target is never polled — its liveness stays unknown, which
// folds exactly as the gate did before this signal existed.
func TestRegaLivenessTargetsSkipACentralWithoutAHost(t *testing.T) {
	t.Parallel()
	_, _, publisher := hubDiscoveryFixture(t)
	publisher.SetRegaLivenessTargets([]config.CentralConfig{
		{Name: "ccu-01", Host: "ccu.local"},
		{Name: "ccu-02"},
	})
	if publisher.regaTargetFor("ccu-01") == nil {
		t.Fatal("a central with a host got no probe target")
	}
	if publisher.regaTargetFor("ccu-02") != nil {
		t.Fatal("a central without a host got a probe target: there is no URL to build")
	}
}

// TestStopWaitsForTheRegaPollerToLeave pins the lifecycle contract the
// publisher makes for every source it attaches: when Stop returns, nothing
// it wired is still running.
//
// Cancelling the probe's context is not enough on its own. Stop tears the
// fan-out worker down straight after the closers run, and a poller still
// inside a probe would come back to enqueue onto a queue nobody drains, on a
// publisher whose next Start has already begun wiring a second poller
// against the same CCU. The closer therefore WAITS for the goroutine, and
// that wait is what this test is about.
//
// Falsifiability: replace the addUnsub closer in
// [HubMQTTPublisher.startRegaLivenessPoll] with a bare cancel() — dropping
// the `<-done` receive — and Stop returns while the probe is still in
// flight, so inFlight is non-zero here.
func TestStopWaitsForTheRegaPollerToLeave(t *testing.T) {
	t.Parallel()
	c, _, publisher := hubDiscoveryFixture(t)
	c.SetSystemInformation(central.SystemInfo{Serial: "3014F711A0001F0123456789"})

	var inFlight atomic.Int32
	entered := make(chan struct{}, 1)
	publisher.mu.Lock()
	publisher.regaTargets = map[string]*regaLivenessTarget{
		"ccu-01": {
			interval: time.Millisecond,
			probe: func(context.Context) regaProbeResult {
				inFlight.Add(1)
				defer inFlight.Add(-1)
				select {
				case entered <- struct{}{}:
				default:
				}
				// A real probe is blocking network I/O against a CCU that may
				// be exactly the one that stopped answering; this stands in
				// for one that has not returned yet.
				time.Sleep(150 * time.Millisecond)
				return regaProbeServing
			},
		},
	}
	publisher.mu.Unlock()

	publisher.Start(context.Background())
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		publisher.Stop()
		t.Fatal("the poller never ran: the wiring pass did not start it")
	}
	publisher.Stop()
	if got := inFlight.Load(); got != 0 {
		t.Fatalf("Stop returned with %d probe(s) still in flight: the poller outlived its "+
			"wiring generation, and the next Start wires a second one against the same CCU",
			got)
	}
}
