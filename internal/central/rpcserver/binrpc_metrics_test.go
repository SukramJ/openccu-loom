// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rpcserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// recordingCallbackObserver captures the route keys a listener observed.
type recordingCallbackObserver struct {
	mu       sync.Mutex
	started  []string
	finished []string
}

func (o *recordingCallbackObserver) CallbackStarted(routeKey string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started = append(o.started, routeKey)
}

func (o *recordingCallbackObserver) CallbackFinished(routeKey string, _ time.Duration, _ bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.finished = append(o.finished, routeKey)
}

func (o *recordingCallbackObserver) snapshot() (started, finished []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.started...), append([]string(nil), o.finished...)
}

// binrpcCall builds a bare (non-batched) callback for interfaceID.
func binrpcCall(method, interfaceID string) *binrpc.Request {
	return &binrpc.Request{
		Method: method,
		Params: []xmlrpc.Value{
			xmlrpc.StringValue(interfaceID),
			xmlrpc.StringValue("CENTRAL"),
			xmlrpc.StringValue("PONG"),
			xmlrpc.StringValue("tok1"),
		},
	}
}

// TestBINRPCServerObservesOnlyRoutableCallbacks pins the observer
// contract on the BIN-RPC side.
//
// The listener is daemon-wide while the metrics it feeds are per
// central, so an observation is only meaningful for a callback that
// resolved to a registered route. A push carrying an interface id
// nothing is registered for belongs to no central — a CCU left with a
// dangling registration after an unclean daemon shutdown keeps sending
// exactly that — and observing it charges a route key that either
// belongs to no one or, worse, to the central that just deregistered,
// reporting a healthy CCU as failing.
func TestBINRPCServerObservesOnlyRoutableCallbacks(t *testing.T) {
	t.Parallel()

	const ifaceID = "loom-ccu-CUxD"
	obs := &recordingCallbackObserver{}
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0", Metrics: obs})
	if err != nil {
		t.Fatalf("NewBINRPCServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.Register(ifaceID, &binrpcRecordingHandlers{})

	ctx := context.Background()
	if _, err := srv.dispatch(ctx, binrpcCall("event", ifaceID)); err != nil {
		t.Fatalf("dispatch of a routable callback: %v", err)
	}
	started, finished := obs.snapshot()
	if len(started) != 1 || started[0] != ifaceID || len(finished) != 1 || finished[0] != ifaceID {
		t.Fatalf("routable callback observed as started=%v finished=%v, want one pair for %q",
			started, finished, ifaceID)
	}

	// An interface id the listener has no route for.
	if _, err := srv.dispatch(ctx, binrpcCall("event", "loom-ccu-Orphan")); err == nil {
		t.Fatal("dispatch of an unregistered interface id succeeded, want ErrNoHandlers")
	}
	// A registration the daemon dropped while the CCU kept pushing.
	srv.Deregister(ifaceID)
	if _, err := srv.dispatch(ctx, binrpcCall("event", ifaceID)); err == nil {
		t.Fatal("dispatch after deregistration succeeded, want ErrNoHandlers")
	}

	started, finished = obs.snapshot()
	if len(started) != 1 || len(finished) != 1 {
		t.Errorf("unroutable callbacks were observed: started=%v finished=%v, want the single "+
			"routable pair only", started, finished)
	}
}

// TestBINRPCServerDoesNotObserveIntrospection keeps the other
// non-routed shape out of the metrics: system.listMethods answers the
// same list for every peer and is deliberately exempt from the route
// lookup, so it belongs to no central either.
func TestBINRPCServerDoesNotObserveIntrospection(t *testing.T) {
	t.Parallel()

	obs := &recordingCallbackObserver{}
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0", Metrics: obs})
	if err != nil {
		t.Fatalf("NewBINRPCServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if _, err := srv.dispatch(context.Background(), &binrpc.Request{Method: "system.listMethods"}); err != nil {
		t.Fatalf("dispatch of system.listMethods: %v", err)
	}
	if started, finished := obs.snapshot(); len(started) != 0 || len(finished) != 0 {
		t.Errorf("introspection observed as started=%v finished=%v, want neither", started, finished)
	}
}
