// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/SukramJ/go-fabric/cluster/onoff"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// MatterOnOffConfig describes the OnOff cluster a bridged source serves
// through go-fabric's [onoff.Server] (ADR 0084 phase 2). The server owns
// the attribute values, the six commands, the OnTime / OffWaitTime
// countdowns and the scene paths; the source contributes the two things
// the stack cannot know — how a command reaches the CCU (Sink) and what
// the CCU says the state is ([onoff.Server.Set] from the data point's
// confirmed-update hook).
type MatterOnOffConfig struct {
	// Lighting advertises the LT feature. Every OnOff endpoint type this
	// daemon projects except the Speaker mandates it (OnOffPlugInUnit,
	// OnOffLight, DimmableLight, the colour lights).
	Lighting bool
	// DataVersion is the source's cluster data-version tracker; the
	// server bumps it on every change it stores, so a report after a
	// confirmed CCU update and a report after a command share one
	// counter.
	DataVersion *hmtypes.DataVersionTracker
	// Initial is the OnOff value the server starts with — the data
	// point's observed state, false while nothing was observed (Apple
	// Home drops an OnOff accessory whose first report carries no
	// boolean).
	Initial bool
	// Sink is the CCU write a controller's command becomes. An error
	// refuses the command (FAILURE) and keeps the stored attribute.
	Sink func(ctx context.Context, on bool) error
}

// matterOnOffSink adapts the config's function onto [onoff.Sink].
type matterOnOffSink func(ctx context.Context, on bool) error

func (f matterOnOffSink) SetOnOff(ctx context.Context, on bool) error { return f(ctx, on) }

// NewMatterOnOffServer builds the server for cfg. The configuration is
// decided by the profile at compile time (feature set, bridged), so the
// only error [onoff.New] can return is a programming error.
func NewMatterOnOffServer(cfg MatterOnOffConfig) (*onoff.Server, error) {
	var features onoff.Feature
	if cfg.Lighting {
		features = onoff.Feature(onoff.FeatureLighting)
	}
	var sink onoff.Sink
	if cfg.Sink != nil {
		sink = matterOnOffSink(cfg.Sink)
	}
	srv, err := onoff.New(onoff.Config{
		Features:    features,
		DataVersion: cfg.DataVersion,
		Sink:        sink,
		Initial:     map[uint32]any{onoff.AttrOnOff: cfg.Initial},
		Bridged:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("matter onoff server: %w", err)
	}
	return srv, nil
}

// MustMatterOnOffServer is [NewMatterOnOffServer] for the profiles' fixed
// configurations.
//
// invariant: the feature set and the initial value are the profile's
// constants; a refusal is a defect in the profile, not a runtime
// condition.
func MustMatterOnOffServer(cfg MatterOnOffConfig) *onoff.Server {
	srv, err := NewMatterOnOffServer(cfg)
	if err != nil {
		panic(err)
	}
	return srv
}

// matterOnOffFeedbackWait bounds how long a confirming goroutine waits
// for its value to land on the server before it moves on and leaves the
// store to the worker.
const matterOnOffFeedbackWait = 100 * time.Millisecond

// MatterOnOffFeedback carries the CCU's confirmed state onto an
// [onoff.Server] without ever blocking the goroutine that confirms it.
//
// [onoff.Server] runs a command's Sink under its operation lock and
// [onoff.Server.Set] takes the same lock. A CCU that answers a write with
// its event before the write's own reply — the simulator does, a real CCU
// may — therefore confirms from inside the Sink: the confirming goroutine
// (the RPC callback that handles every event of the interface) would wait
// for the lock the command holds while the command waits for the reply
// the callback is blocking. The feedback breaks that cycle: Set records
// the latest value and lets one worker goroutine store it; the caller
// waits a bounded time for its value to land, so the common,
// uncontended path stays synchronous and the contended one defers the
// store until the command returns. Coalescing keeps order: the worker
// always stores the newest value, never an older one after a newer one.
type MatterOnOffFeedback struct {
	srv *onoff.Server

	mu      sync.Mutex
	latest  bool
	gen     uint64 // generation of latest
	applied uint64 // generation the worker stored last
	running bool
	notify  chan struct{} // closed after each store, then replaced
}

// NewMatterOnOffFeedback returns the feedback for srv.
func NewMatterOnOffFeedback(srv *onoff.Server) *MatterOnOffFeedback {
	return &MatterOnOffFeedback{srv: srv, notify: make(chan struct{})}
}

// Set hands the CCU's confirmed value to the server; see the type's doc
// for why it returns even while the server is busy.
func (f *MatterOnOffFeedback) Set(on bool) {
	f.mu.Lock()
	f.latest = on
	f.gen++
	want := f.gen
	if !f.running {
		f.running = true
		go f.run()
	}
	f.mu.Unlock()

	timer := time.NewTimer(matterOnOffFeedbackWait)
	defer timer.Stop()
	for {
		f.mu.Lock()
		done := f.applied >= want
		ch := f.notify
		f.mu.Unlock()
		if done {
			return
		}
		select {
		case <-ch:
		case <-timer.C:
			return
		}
	}
}

// run stores the newest value until none is pending, then exits.
func (f *MatterOnOffFeedback) run() {
	for {
		f.mu.Lock()
		if f.applied == f.gen {
			f.running = false
			f.mu.Unlock()
			return
		}
		on, gen := f.latest, f.gen
		f.mu.Unlock()

		f.srv.Set(on)

		f.mu.Lock()
		f.applied = gen
		close(f.notify)
		f.notify = make(chan struct{})
		f.mu.Unlock()
	}
}

// MatterOnOffHolder keeps one [onoff.Server] per source across
// [contract.EndpointSource.MatterClusterServers] calls. The assembler
// re-reads the servers on every reassembly and the bridge keeps the ones
// it already serves; a server rebuilt each time would lose its timers,
// its data version and the subscription state behind it.
type MatterOnOffHolder struct {
	mu  sync.Mutex
	srv *onoff.Server
}

// Server returns the held server, building it through build on first
// use.
func (h *MatterOnOffHolder) Server(build func() *onoff.Server) *onoff.Server {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.srv == nil {
		h.srv = build()
	}
	return h.srv
}

// Quiesce stops the held server's countdowns when the source retires,
// so no timer outlives the data point it would write to. A source that
// never served Matter has nothing to stop.
func (h *MatterOnOffHolder) Quiesce() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.srv != nil {
		h.srv.MatterQuiesce()
	}
}
