// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

// The openccu-lite wire contract, pinned against the production client
// (internal/client/transport/occulited) driving the MIT fake box
// (tests/harness/litefake). Every fact comes from the condensed contract
// in notes/plans/openccu-lite-backend.md, Appendix A. Each test also runs
// with the fake deviating from the pinned fact and asserts that the
// client notices; without that run a green test would only prove that
// the fake agrees with itself.

const (
	liteWait       = 5 * time.Second
	liteBidCosRF   = "BidCos-RF"
	liteHmIPRF     = "HmIP-RF"
	liteSwitch     = "VCU0000321:1" // HM-LC-Sw1-Pl on BidCos-RF, default godevccu fleet
	liteReader     = "olt_reader"
	liteMetaWriter = "olt_metawriter"
)

func startLiteFake(t *testing.T, opts litefake.Options) *litefake.Fake {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if opts.Tokens == nil {
		opts.Tokens = map[string][]string{
			litefake.DefaultToken: {"*"},
			liteReader:            {"rpc:read"},
			liteMetaWriter:        {"meta:write"},
		}
	}
	f, err := litefake.Start(ctx, opts)
	if err != nil {
		t.Fatalf("litefake.Start: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func liteClient(t *testing.T, f *litefake.Fake, token string) *occulited.Client {
	t.Helper()
	c, err := occulited.New(occulited.Config{BaseURL: f.URL(), Token: token})
	if err != nil {
		t.Fatalf("occulited.New: %v", err)
	}
	return c
}

// liteXMLRPC is Loom's XML-RPC client pointed at the lite-rpc proxy over
// the occulited transport, the way the lite backend composes them.
func liteXMLRPC(t *testing.T, f *litefake.Fake, iface, token string) *xmlrpc.Client {
	t.Helper()
	c := liteClient(t, f, token)
	x, err := xmlrpc.NewClient(xmlrpc.Config{URL: c.XMLRPCURL(iface), HTTPClient: c.HTTPClient(), Interface: iface})
	if err != nil {
		t.Fatalf("xmlrpc.NewClient: %v", err)
	}
	return x
}

func liteBackoff() occulited.Backoff {
	return occulited.Backoff{
		Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, Jitter: 0.01,
		Unauthorized: 100 * time.Millisecond, TooManyStreams: 100 * time.Millisecond, Starting: 100 * time.Millisecond,
	}
}

func liteEvents(t *testing.T, f *litefake.Fake, opts occulited.EventsOptions) *occulited.Stream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if opts.Backoff == (occulited.Backoff{}) {
		opts.Backoff = liteBackoff()
	}
	s := liteClient(t, f, litefake.DefaultToken).Events(ctx, opts)
	t.Cleanup(func() {
		cancel()
		<-s.Done()
	})
	return s
}

// liteNext returns the next message of one of kinds, skipping others.
func liteNext(t *testing.T, s *occulited.Stream, kinds ...occulited.Kind) occulited.Message {
	t.Helper()
	deadline := time.After(liteWait)
	for {
		select {
		case m, ok := <-s.Messages():
			if !ok {
				t.Fatal("stream reader stopped")
			}
			for _, k := range kinds {
				if m.Kind == k {
					return m
				}
			}
		case <-deadline:
			t.Fatalf("no %v message in time", kinds)
		}
	}
}

// liteQuiet asserts that no message of kind arrives within d.
func liteQuiet(t *testing.T, s *occulited.Stream, kind occulited.Kind, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m := <-s.Messages():
			if m.Kind == kind {
				t.Fatalf("unexpected %s: %+v", kind, m)
			}
		case <-deadline:
			return
		}
	}
}

func liteFireSwitch(t *testing.T, f *litefake.Fake, on bool) {
	t.Helper()
	if err := f.V().InterfaceRPC(liteBidCosRF).SimulateDeviceEvent(liteSwitch, "STATE", on); err != nil {
		t.Fatalf("SimulateDeviceEvent: %v", err)
	}
}

// lastProxyCall returns the newest recorded proxy request.
func lastProxyCall(t *testing.T, f *litefake.Fake) litefake.Call {
	t.Helper()
	calls := f.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if strings.HasPrefix(calls[i].Path, "/api/rpc/v1/xmlrpc/") {
			return calls[i]
		}
	}
	t.Fatal("no proxy call recorded")
	return litefake.Call{}
}

// TestLiteRPCInitIsRefusedAsFaultOver200 pins A.3: init, alone and
// inside system.multicall, is refused with fault -1 over HTTP 200 and the
// exact text "init is not available remotely on openccu-lite: subscribe
// to /api/rpc/v1/events - see docs/rpc-remote.md".
func TestLiteRPCInitIsRefusedAsFaultOver200(t *testing.T) {
	initParams := []xmlrpc.Value{xmlrpc.StringValue("http://127.0.0.1:1/"), xmlrpc.StringValue("loom")}
	multicall := []xmlrpc.Value{xmlrpc.ArrayValue{xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "methodName", Value: xmlrpc.StringValue("init")},
		{Name: "params", Value: xmlrpc.ArrayValue(initParams)},
	}}}}

	t.Run("contract", func(t *testing.T) {
		f := startLiteFake(t, litefake.Options{})
		x := liteXMLRPC(t, f, liteHmIPRF, litefake.DefaultToken)
		for _, c := range []struct {
			method string
			params []xmlrpc.Value
		}{{"init", initParams}, {"system.multicall", multicall}} {
			_, err := x.Call(context.Background(), c.method, c.params)
			if !occulited.IsInitRefusal(err) || !errors.Is(occulited.ClassifyFault(err), occulited.ErrInitRefused) {
				t.Errorf("%s: %v, want the init refusal", c.method, err)
			}
			if call := lastProxyCall(t, f); call.Status != http.StatusOK || call.RPCMethods[0] != "init" && c.method == "init" {
				t.Errorf("%s: recorded %+v, want HTTP 200", c.method, call)
			}
		}
	})
	t.Run("deviation", func(t *testing.T) {
		f := startLiteFake(t, litefake.Options{})
		f.Deviate(litefake.DeviateInitFaultText, true)
		_, err := liteXMLRPC(t, f, liteHmIPRF, litefake.DefaultToken).Call(context.Background(), "init", initParams)
		if err == nil || occulited.IsInitRefusal(err) || errors.Is(occulited.ClassifyFault(err), occulited.ErrInitRefused) {
			t.Errorf("a different fault text still classified as the init refusal: %v", err)
		}
	})
}

// TestLiteRPCDownIs503JSONAndClassifiedAsNoConnection pins A.3: an
// interface process that does not answer yields 503 {"error":"down"},
// which the client classifies as hmerr.ErrNoConnection.
func TestLiteRPCDownIs503JSONAndClassifiedAsNoConnection(t *testing.T) {
	run := func(t *testing.T, deviate bool) error {
		t.Helper()
		f := startLiteFake(t, litefake.Options{})
		f.Deviate(litefake.DeviateDownErrorCode, deviate)
		if err := f.SetInterfaceDown(liteBidCosRF, true); err != nil {
			t.Fatal(err)
		}
		_, err := liteXMLRPC(t, f, liteBidCosRF, litefake.DefaultToken).Call(context.Background(), "listDevices", nil)
		if call := lastProxyCall(t, f); call.Status != http.StatusServiceUnavailable {
			t.Fatalf("proxy answered %d, want 503", call.Status)
		}
		return err
	}
	t.Run("contract", func(t *testing.T) {
		err := run(t, false)
		var apiErr *occulited.APIError
		if !errors.Is(err, hmerr.ErrNoConnection) || !errors.As(err, &apiErr) || apiErr.Code != occulited.CodeDown {
			t.Errorf("down interface: %v, want ErrNoConnection with code down", err)
		}
	})
	t.Run("deviation", func(t *testing.T) {
		if err := run(t, true); errors.Is(err, hmerr.ErrNoConnection) {
			t.Errorf("a 503 with another error code classified as no connection: %v", err)
		}
	})
}

// TestLiteRPCTierFaultBecomesScopeMissing pins A.3/A.4: a method above
// the credential's tier is refused with fault -1 "not permitted:
// <method> needs rpc:<tier>", which the client turns into
// hmerr.ErrScopeMissing carrying the scope.
func TestLiteRPCTierFaultBecomesScopeMissing(t *testing.T) {
	setValue := []xmlrpc.Value{xmlrpc.StringValue(liteSwitch), xmlrpc.StringValue("STATE"), xmlrpc.BoolValue(true)}
	run := func(t *testing.T, deviate bool) error {
		t.Helper()
		f := startLiteFake(t, litefake.Options{})
		f.Deviate(litefake.DeviateTierFaultText, deviate)
		_, err := liteXMLRPC(t, f, liteBidCosRF, liteReader).Call(context.Background(), "setValue", setValue)
		return occulited.ClassifyFault(err)
	}
	t.Run("contract", func(t *testing.T) {
		err := run(t, false)
		var sm *hmerr.ScopeMissingError
		if !errors.Is(err, hmerr.ErrScopeMissing) || !errors.As(err, &sm) || sm.Scope != "rpc:operate" || sm.Operation != "setValue" {
			t.Errorf("tier refusal: %v, want ScopeMissing(rpc:operate)", err)
		}
	})
	t.Run("deviation", func(t *testing.T) {
		err := run(t, true)
		if err == nil || errors.Is(err, hmerr.ErrScopeMissing) {
			t.Errorf("a differently phrased refusal classified as a missing scope: %v", err)
		}
	})
}

// TestLiteStreamFramingAndHello pins A.5: ": connected" first, then a
// hello frame with an id "<boot_id>-<seq>", then id/event/data frames.
func TestLiteStreamFramingAndHello(t *testing.T) {
	t.Run("contract", func(t *testing.T) {
		f := startLiteFake(t, litefake.Options{})
		s := liteEvents(t, f, occulited.EventsOptions{Types: []string{"event"}, Keys: []string{"STATE"}})
		if m := <-s.Messages(); m.Kind != occulited.KindOpen {
			t.Fatalf("first %+v, want open", m)
		}
		if m := <-s.Messages(); m.Kind != occulited.KindComment || m.Comment != "connected" {
			t.Fatalf("second %+v, want the connected comment", m)
		}
		hello := <-s.Messages()
		if hello.Kind != occulited.KindHello || hello.Hello == nil ||
			hello.ID != hello.Hello.BootID+"-"+strconv.FormatUint(hello.Hello.Seq, 10) || hello.Hello.BootID != f.BootID() {
			t.Fatalf("third %+v, want hello with id <boot>-<seq>", hello)
		}
		liteFireSwitch(t, f, true)
		ev := liteNext(t, s, occulited.KindEvent, occulited.KindClosed)
		if ev.Kind != occulited.KindEvent || ev.Type != "event" || !strings.HasPrefix(ev.ID, f.BootID()+"-") {
			t.Errorf("event %+v", ev)
		}
	})
	for _, d := range []litefake.Deviation{litefake.DeviateHelloWithoutID, litefake.DeviateEventBeforeHello} {
		t.Run("deviation/"+string(d), func(t *testing.T) {
			f := startLiteFake(t, litefake.Options{})
			f.Deviate(d, true)
			s := liteEvents(t, f, occulited.EventsOptions{})
			m := liteNext(t, s, occulited.KindHello, occulited.KindClosed, occulited.KindEvent)
			if m.Kind != occulited.KindClosed || !errors.Is(m.Err, occulited.ErrProtocol) {
				t.Errorf("got %+v, want the connection closed as a protocol violation", m)
			}
		})
	}
}

// TestLiteStreamResumeReplaysExactlyOnce pins A.5: a reconnect with
// Last-Event-ID replays every ring message after that position, so the
// consumer sees each message once: none lost, none duplicated.
func TestLiteStreamResumeReplaysExactlyOnce(t *testing.T) {
	run := func(t *testing.T, deviate bool) (values []string, dups uint64) {
		t.Helper()
		f := startLiteFake(t, litefake.Options{})
		f.Deviate(litefake.DeviateReplayIncludesSince, deviate)
		// The reconnect waits long enough that the next two values are
		// published while the reader is away and arrive as a replay.
		s := liteEvents(t, f, occulited.EventsOptions{
			Types: []string{"event"}, Keys: []string{"STATE"},
			Backoff: occulited.Backoff{Initial: 400 * time.Millisecond, Max: 400 * time.Millisecond, Jitter: 0.01},
		})
		liteNext(t, s, occulited.KindHello)
		liteFireSwitch(t, f, true)
		first := liteNext(t, s, occulited.KindEvent)
		values = append(values, string(first.Event.Value))
		f.DropStreams()
		liteNext(t, s, occulited.KindClosed)
		liteFireSwitch(t, f, false)
		liteFireSwitch(t, f, true)
		liteNext(t, s, occulited.KindHello)
		for range 2 {
			values = append(values, string(liteNext(t, s, occulited.KindEvent).Event.Value))
		}
		liteFireSwitch(t, f, false)
		values = append(values, string(liteNext(t, s, occulited.KindEvent).Event.Value))
		liteQuiet(t, s, occulited.KindEvent, 200*time.Millisecond)
		return values, s.Duplicates()
	}
	want := "true,false,true,false"
	t.Run("contract", func(t *testing.T) {
		values, dups := run(t, false)
		if strings.Join(values, ",") != want || dups != 0 {
			t.Errorf("delivered %v with %d duplicates, want %s exactly once", values, dups, want)
		}
	})
	t.Run("deviation", func(t *testing.T) {
		values, dups := run(t, true)
		if strings.Join(values, ",") != want || dups != 1 {
			t.Errorf("delivered %v, %d duplicates dropped; want %s with the replayed position noticed once", values, dups, want)
		}
	})
}

// TestLiteStreamResyncBootGapOverflow pins A.5: resync{boot} after a
// restart, resync{gap} when the ring no longer covers the position,
// resync{overflow} (then the stream ends) after lost messages; a resync
// frame never has an id.
func TestLiteStreamResyncBootGapOverflow(t *testing.T) {
	t.Run("contract", func(t *testing.T) {
		f := startLiteFake(t, litefake.Options{HeartbeatInterval: 100 * time.Millisecond})
		s := liteEvents(t, f, occulited.EventsOptions{
			Backoff: occulited.Backoff{Initial: 300 * time.Millisecond, Max: 300 * time.Millisecond, Jitter: 0.01},
		})
		liteNext(t, s, occulited.KindHello)
		expectResync := func(reason string) {
			t.Helper()
			m := liteNext(t, s, occulited.KindResync, occulited.KindClosed)
			if m.Kind == occulited.KindClosed {
				m = liteNext(t, s, occulited.KindResync, occulited.KindClosed)
			}
			if m.Kind != occulited.KindResync || m.Resync.Reason != reason || m.ID != "" {
				t.Fatalf("got %+v, want resync{%s} without id", m, reason)
			}
		}

		if err := f.RestartBoot(context.Background()); err != nil {
			t.Fatal(err)
		}
		expectResync(occulited.ResyncBoot)
		if !strings.HasPrefix(s.Position(), f.BootID()+"-") {
			t.Errorf("position %q not on the new boot", s.Position())
		}

		f.DropStreams()
		liteNext(t, s, occulited.KindClosed)
		liteFireSwitch(t, f, true)
		f.ForceGap()
		expectResync(occulited.ResyncGap)

		f.ForceOverflow()
		expectResync(occulited.ResyncOverflow)
		if m := liteNext(t, s, occulited.KindClosed); !errors.Is(m.Err, occulited.ErrStreamEnded) {
			t.Errorf("after overflow: %v, want the stream ended", m.Err)
		}
	})
	t.Run("deviation", func(t *testing.T) {
		f := startLiteFake(t, litefake.Options{})
		s := liteEvents(t, f, occulited.EventsOptions{})
		liteNext(t, s, occulited.KindHello)
		f.Deviate(litefake.DeviateResyncWithID, true)
		if err := f.RestartBoot(context.Background()); err != nil {
			t.Fatal(err)
		}
		liteNext(t, s, occulited.KindClosed) // the restart ends the stream
		m := liteNext(t, s, occulited.KindResync, occulited.KindClosed)
		if m.Kind != occulited.KindClosed || !errors.Is(m.Err, occulited.ErrProtocol) {
			t.Errorf("got %+v, want a resync with an id refused as a protocol violation", m)
		}
	})
}

// TestLiteStreamHeartbeatDeadAfterSilence pins A.5: the box sends
// ": ping" every 15 s and a client treats 45 s of silence as a dead
// stream (here scaled: 100 ms heartbeat, 400 ms timeout).
func TestLiteStreamHeartbeatDeadAfterSilence(t *testing.T) {
	opts := occulited.EventsOptions{HeartbeatTimeout: 400 * time.Millisecond}
	t.Run("contract", func(t *testing.T) {
		f := startLiteFake(t, litefake.Options{HeartbeatInterval: 100 * time.Millisecond})
		s := liteEvents(t, f, opts)
		liteNext(t, s, occulited.KindHello)
		pings := 0
		deadline := time.After(1200 * time.Millisecond)
		for done := false; !done; {
			select {
			case m := <-s.Messages():
				switch {
				case m.Kind == occulited.KindClosed:
					t.Fatalf("a heartbeating stream was closed: %v", m.Err)
				case m.Kind == occulited.KindComment && m.Comment == "ping":
					pings++
				}
			case <-deadline:
				done = true
			}
		}
		if pings < 3 {
			t.Errorf("%d heartbeats in 1.2 s", pings)
		}
	})
	t.Run("deviation", func(t *testing.T) {
		f := startLiteFake(t, litefake.Options{HeartbeatInterval: 100 * time.Millisecond})
		f.Deviate(litefake.DeviateNoHeartbeat, true)
		s := liteEvents(t, f, opts)
		liteNext(t, s, occulited.KindHello)
		m := liteNext(t, s, occulited.KindClosed)
		if !errors.Is(m.Err, occulited.ErrHeartbeatTimeout) {
			t.Fatalf("closed with %v, want the heartbeat timeout", m.Err)
		}
		liteNext(t, s, occulited.KindHello) // and it reconnected
	})
}

// TestLiteStreamLimitIs429 pins A.5: two streams per token; a third is
// refused with 429 {"error":"too-many-streams"}, which the client backs
// off from with its stream-limit delay and retries.
func TestLiteStreamLimitIs429(t *testing.T) {
	occupy := func(t *testing.T, f *litefake.Fake) []context.CancelFunc {
		t.Helper()
		cancels := make([]context.CancelFunc, 0, 2)
		for range 2 {
			ctx, cancel := context.WithCancel(context.Background())
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.URL()+"/api/rpc/v1/events", http.NoBody)
			req.Header.Set("Authorization", "Bearer "+litefake.DefaultToken)
			resp, err := f.Client().Do(req)
			if err != nil || resp.StatusCode != http.StatusOK {
				cancel()
				t.Fatalf("occupying stream: %v", err)
			}
			t.Cleanup(func() { _ = resp.Body.Close() })
			cancels = append(cancels, cancel)
		}
		t.Cleanup(func() {
			for _, c := range cancels {
				c()
			}
		})
		return cancels
	}
	bo := liteBackoff()
	bo.TooManyStreams = 777 * time.Millisecond
	t.Run("contract", func(t *testing.T) {
		f := startLiteFake(t, litefake.Options{})
		cancels := occupy(t, f)
		s := liteEvents(t, f, occulited.EventsOptions{Backoff: bo})
		m := liteNext(t, s, occulited.KindClosed, occulited.KindHello)
		if !errors.Is(m.Err, occulited.ErrTooManyStreams) || m.RetryIn != bo.TooManyStreams {
			t.Fatalf("got %+v, want too-many-streams with the stream-limit delay", m)
		}
		cancels[0]()
		liteNext(t, s, occulited.KindHello)
	})
	t.Run("deviation", func(t *testing.T) {
		f := startLiteFake(t, litefake.Options{})
		f.Deviate(litefake.DeviateStreamLimitCode, true)
		occupy(t, f)
		s := liteEvents(t, f, occulited.EventsOptions{Backoff: bo})
		m := liteNext(t, s, occulited.KindClosed, occulited.KindHello)
		if m.Kind != occulited.KindClosed || errors.Is(m.Err, occulited.ErrTooManyStreams) || m.RetryIn == bo.TooManyStreams {
			t.Errorf("got %+v, a 429 with another code must not read as the stream limit", m)
		}
	})
}

// metaRevisionGap is the gap rule a consumer of the metadata stream
// applies (A.6): events carry the store revision, several events may
// share one, and a revision above last+1 means events were lost and the
// snapshot must be read again. Acting on it is the adapter's job; the
// wire client's part, pinned here, is to deliver the revisions exactly
// as the box sent them.
func metaRevisionGap(last, next int) bool { return next > last+1 }

// TestMetaStreamGapTriggersResnapshot pins A.6: one mutation may emit
// several events with the same revision (accepted), and a revision jump
// is visible to the consumer, which re-snapshots.
func TestMetaStreamGapTriggersResnapshot(t *testing.T) {
	run := func(t *testing.T, deviate bool) (revs []int, gap bool) {
		t.Helper()
		f := startLiteFake(t, litefake.Options{Meta: litefake.DefaultMeta()})
		f.Deviate(litefake.DeviateMetaDropEvent, deviate)
		c := liteClient(t, f, liteMetaWriter)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		start := f.Meta().Revision()
		s := c.MetaEvents(ctx, occulited.MetaStreamOptions{Since: &start, Backoff: liteBackoff()})
		defer func() { cancel(); <-s.Done() }()
		waitOpen(t, s)
		a, b := "Bulk A", "Bulk B"
		if _, err := c.Bulk(ctx, occulited.BulkRequest{Set: map[string]occulited.ObjectPatch{
			"BidCos-RF.VCU0000321": {Name: &a}, "HmIP-RF.VCU2128127": {Name: &b},
		}}, occulited.WriteOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := f.Meta().Rename("BidCos-RF.VCU0000321", "After"); err != nil {
			t.Fatal(err)
		}
		final := f.Meta().Revision()
		last := start
		deadline := time.After(liteWait)
		for last < final {
			select {
			case m := <-s.Messages():
				if m.Kind != occulited.MetaChange {
					continue
				}
				revs = append(revs, m.Event.Revision)
				gap = gap || metaRevisionGap(last, m.Event.Revision)
				last = max(last, m.Event.Revision)
			case <-deadline:
				t.Fatalf("revisions %v, never reached %d", revs, final)
			}
		}
		return revs, gap
	}
	t.Run("contract", func(t *testing.T) {
		revs, gap := run(t, false)
		if gap || len(revs) != 3 || revs[0] != revs[1] || revs[2] != revs[1]+1 {
			t.Errorf("revisions %v (gap %v), want two events of one revision then the next", revs, gap)
		}
	})
	t.Run("deviation", func(t *testing.T) {
		revs, gap := run(t, true)
		if !gap {
			t.Errorf("revisions %v: the dropped revision went unnoticed", revs)
		}
	})
}

func waitOpen(t *testing.T, s *occulited.MetaStream) {
	t.Helper()
	select {
	case m := <-s.Messages():
		if m.Kind != occulited.MetaOpen {
			t.Fatalf("first meta message %+v, want open", m)
		}
	case <-time.After(liteWait):
		t.Fatal("meta stream did not open")
	}
}

// TestMetaUnchangedMutationIs304WithETag pins A.6/A.11: a write that
// changes nothing answers 304 with an empty body and the revision only
// in ETag.
func TestMetaUnchangedMutationIs304WithETag(t *testing.T) {
	name := "Stehlampe"
	run := func(t *testing.T, deviate bool) (*litefake.Fake, occulited.Mutation, error) {
		t.Helper()
		f := startLiteFake(t, litefake.Options{Meta: litefake.DefaultMeta()})
		f.Deviate(litefake.DeviateMeta304WithoutETag, deviate)
		m, err := liteClient(t, f, liteMetaWriter).PatchObject(context.Background(), "BidCos-RF.VCU0000321",
			occulited.ObjectPatch{Name: &name}, occulited.WriteOptions{})
		return f, m, err
	}
	t.Run("contract", func(t *testing.T) {
		f, m, err := run(t, false)
		if err != nil || m.Changed || m.Revision != f.Meta().Revision() {
			t.Fatalf("unchanged write: %+v %v (store at %d)", m, err, f.Meta().Revision())
		}
		calls := f.Calls()
		if last := calls[len(calls)-1]; last.Status != http.StatusNotModified {
			t.Errorf("box answered %d, want 304", last.Status)
		}
	})
	t.Run("deviation", func(t *testing.T) {
		if _, _, err := run(t, true); !errors.Is(err, occulited.ErrProtocol) {
			t.Errorf("304 without ETag: %v, want a protocol error", err)
		}
	})
}

// TestMetaWritesSendBodyOnEveryNonGET pins A.1: requests with a body
// method need a Content-Length through the box's web server (411
// otherwise), so the client sends "{}" when it has nothing to send.
func TestMetaWritesSendBodyOnEveryNonGET(t *testing.T) {
	run := func(t *testing.T, deviate bool) ([]error, *litefake.Fake) {
		t.Helper()
		f := startLiteFake(t, litefake.Options{Meta: litefake.DefaultMeta()})
		f.Deviate(litefake.DeviateLengthRequiredAlways, deviate)
		c := liteClient(t, f, liteMetaWriter)
		ctx := context.Background()
		no := occulited.WriteOptions{}
		errs := make([]error, 0, 3)
		_, err := c.DeleteObject(ctx, "BidCos-RF.VCU0000321:1", no)
		errs = append(errs, err)
		_, err = c.DeleteNode(ctx, "room", "og", true, no)
		errs = append(errs, err)
		_, err = c.DeleteEnum(ctx, "function", true, no)
		errs = append(errs, err)
		return errs, f
	}
	t.Run("contract", func(t *testing.T) {
		errs, f := run(t, false)
		for i, err := range errs {
			if err != nil {
				t.Errorf("delete %d: %v", i, err)
			}
		}
		// The fake's length gate answers 411 to a body method without a
		// Content-Length, so a success is the proof; the handlers do not
		// read a DELETE body, so the recorded body cannot show the "{}".
		n := 0
		for _, call := range f.Calls() {
			if call.Method == http.MethodDelete {
				n++
				if call.Status == http.StatusLengthRequired {
					t.Errorf("%s %s answered 411", call.Method, call.Path)
				}
			}
		}
		if n != 3 {
			t.Errorf("%d DELETE requests recorded, want 3", n)
		}
	})
	t.Run("deviation", func(t *testing.T) {
		errs, _ := run(t, true)
		for i, err := range errs {
			var apiErr *occulited.APIError
			if !errors.As(err, &apiErr) || apiErr.Status != http.StatusLengthRequired {
				t.Errorf("delete %d: %v, want the 411 surfaced as an error", i, err)
			}
		}
	})
}

// TestDetectionClassifies pins §7.1 over A.6/A.1: the meta version JSON
// means lite; 503 starting means lite, not ready; an HTML answer means
// not lite; a higher API major is refused.
func TestDetectionClassifies(t *testing.T) {
	detect := func(t *testing.T, opts litefake.Options, d litefake.Deviation) (occulited.Detection, error) {
		t.Helper()
		f := startLiteFake(t, opts)
		if d != "" {
			f.Deviate(d, true)
		}
		return liteClient(t, f, "").Detect(context.Background())
	}
	t.Run("contract/lite", func(t *testing.T) {
		d, err := detect(t, litefake.Options{}, "")
		if err != nil || d.Kind != occulited.DetectLite || d.Majors["rpc"] != occulited.SupportedRPCMajor {
			t.Errorf("%+v %v, want lite", d, err)
		}
	})
	t.Run("contract/not-ready", func(t *testing.T) {
		d, err := detect(t, litefake.Options{StartNotReady: true}, "")
		if err != nil || d.Kind != occulited.DetectLiteNotReady {
			t.Errorf("%+v %v, want lite-not-ready", d, err)
		}
	})
	t.Run("deviation/html", func(t *testing.T) {
		d, err := detect(t, litefake.Options{}, litefake.DeviateVersionHTML)
		if err != nil || d.Kind == occulited.DetectLite || d.Kind == occulited.DetectLiteNotReady || d.Kind == occulited.DetectCCU {
			t.Errorf("%+v %v, want neither lite nor CCU (the shell answers checkrega with HTML)", d, err)
		}
	})
	t.Run("deviation/major", func(t *testing.T) {
		_, err := detect(t, litefake.Options{}, litefake.DeviateVersionMajor)
		if !errors.Is(err, occulited.ErrUnsupportedMajor) {
			t.Errorf("%v, want the higher rpc major refused", err)
		}
	})
}

// TestPairingCodeFormula pins the pairing code against the formula text of
// the box's contract, computed here independently: take SHA-256 over the
// hex-decoded nonce, the client nonce and the certificate fingerprint
// bytes, read the first four digest bytes big-endian, reduce modulo one
// million, pad to six digits. The client and the fake must both produce
// it; a client that hashes a different input or order shows a code the
// administrator can never confirm.
func TestPairingCodeFormula(t *testing.T) {
	t.Parallel()
	nonce, _ := hex.DecodeString("00112233445566778899aabbccddeeff")
	clientNonce := []byte("0123456789abcdef0123456789abcdef")
	fp := sha256.Sum256([]byte("a certificate DER"))

	h := sha256.New()
	h.Write(nonce)
	h.Write(clientNonce)
	h.Write(fp[:])
	d := h.Sum(nil)
	n := uint32(d[0])<<24 | uint32(d[1])<<16 | uint32(d[2])<<8 | uint32(d[3])
	want := strconv.FormatUint(uint64(n%1_000_000), 10)
	want = strings.Repeat("0", 6-len(want)) + want

	if got := occulited.PairingCode(nonce, clientNonce, fp[:]); got != want {
		t.Errorf("client code = %s, want %s from the formula", got, want)
	}
	if got := litefake.PairingCode(nonce, clientNonce, fp[:]); got != want {
		t.Errorf("fake code = %s, want %s from the formula", got, want)
	}
	// Over plain HTTP the fingerprint is empty; the order still matters.
	if occulited.PairingCode(nonce, clientNonce, nil) == occulited.PairingCode(clientNonce, nonce, nil) {
		t.Error("swapping the nonces yields the same code; the order is not pinned")
	}
}
