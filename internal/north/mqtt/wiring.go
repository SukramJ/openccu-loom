// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/SukramJ/openccu-loom/internal/payload"
)

// Wiring bridges domain-level value-change events into the MQTT
// bridge. The implementation is deliberately Hub-agnostic: callers
// hand individual [Event] values to [Publish]; the central-layer
// coordinator translates its own DataPointValueChanged events into
// these calls.
//
// Wiring also owns the tiny drift-detection record set — the
// `mqtt_discovery_state` table (see ADR 0011). In the MVP it
// lives in memory; the SQLite backing is a planned future improvement.
//
// The wrapped [*Bridge] sits behind an [atomic.Pointer] so an external
// supervisor can swap the bridge (and the underlying transport
// client) at runtime without re-wiring every subscriber. EventBridge
// and HubMQTTPublisher continue holding their *Wiring reference; the
// next Publish call sees the new bridge.
type Wiring struct {
	bridge atomic.Pointer[Bridge]
	logger *slog.Logger

	mu             sync.Mutex
	lastDiscovered map[string]string // objectID → sha hash of config
}

// NewWiring constructs a Wiring bound to b.
func NewWiring(b *Bridge, logger *slog.Logger) *Wiring {
	if logger == nil {
		logger = slog.Default()
	}
	w := &Wiring{logger: logger, lastDiscovered: make(map[string]string)}
	w.bridge.Store(b)
	return w
}

// Bridge returns the currently-active bridge so adapters can reach
// the topic builder without re-constructing it. The pointer may
// change across [SwapBridge] calls; callers that cache it should
// re-fetch through Wiring instead.
func (w *Wiring) Bridge() *Bridge { return w.bridge.Load() }

// SwapBridge atomically replaces the bridge. After this call every
// subsequent [Publish] / [PublishProgramState] / [PublishSysvar]
// routes through newBridge. The discovery hash cache is reset so
// the next value-change re-emits the discovery payload against the
// new broker.
//
// Returns the previous bridge so the supervisor can run any
// bridge-scoped teardown (currently a no-op — Bridge has no
// reservable resources of its own).
func (w *Wiring) SwapBridge(newBridge *Bridge) *Bridge {
	prev := w.bridge.Swap(newBridge)
	w.mu.Lock()
	w.lastDiscovered = make(map[string]string)
	w.mu.Unlock()
	return prev
}

// Publish forwards ev. Errors are logged at warn level but not
// returned — the caller's event bus must not be blocked by a broker
// hiccup.
func (w *Wiring) Publish(ctx context.Context, ev Event) {
	b := w.bridge.Load()
	if b == nil {
		return
	}
	if err := b.PublishState(ctx, ev); err != nil {
		w.logger.Warn("mqtt.publish",
			slog.String("topic", ev.Parameter),
			slog.String("device", ev.DeviceAddress),
			slog.String("err", err.Error()))
	}
}

// PublishProgramState is a convenience wrapper. `prog` is the model
// object that owns its MQTT topics; the wiring is a pure pass-through.
func (w *Wiring) PublishProgramState(ctx context.Context, central string, prog payload.MQTTAddressable, active bool) {
	b := w.bridge.Load()
	if b == nil {
		return
	}
	if err := b.PublishProgram(ctx, central, prog, active); err != nil {
		w.logger.Warn("mqtt.publish_program",
			slog.String("err", err.Error()))
	}
}

// PublishSysvar is a convenience wrapper. `sv` is the model object
// that owns its MQTT topics.
func (w *Wiring) PublishSysvar(ctx context.Context, central string, sv payload.MQTTAddressable, value any) {
	b := w.bridge.Load()
	if b == nil {
		return
	}
	if err := b.PublishSysvar(ctx, central, sv, value); err != nil {
		w.logger.Warn("mqtt.publish_sysvar",
			slog.String("err", err.Error()))
	}
}

// MarkDiscovered records that a discovery payload has been sent for
// objectID with payloadHash. Returns true when the hash differed
// from the cached one (i.e. the payload genuinely changed).
func (w *Wiring) MarkDiscovered(objectID, payloadHash string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	prev, ok := w.lastDiscovered[objectID]
	if ok && prev == payloadHash {
		return false
	}
	w.lastDiscovered[objectID] = payloadHash
	return true
}

// DiscoveryCount reports how many discovery entries are tracked.
func (w *Wiring) DiscoveryCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.lastDiscovered)
}
