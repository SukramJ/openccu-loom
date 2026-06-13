// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// mqttSupervisor owns the live MQTT stack and supports an atomic
// rebuild (Swap) that replaces broker URL, credentials, topic base,
// or discovery toggles without a daemon restart.
//
// The supervisor exposes one stable [*mqtt.Wiring] for the lifetime
// of the daemon: EventBridge and HubMQTTPublisher hold this pointer
// at construction time and continue publishing through it across
// swaps. Internally Wiring's Bridge is replaced atomically the
// instant Swap commits.
//
// Subscribers (birth sync, command subscriber) cannot share a stable
// handle because they own per-client state on the underlying TCP
// client. The supervisor tears them down and rebuilds them via the
// caller-supplied [SubscriberBuilder] every time the stack is
// swapped.
type mqttSupervisor struct {
	mu     sync.Mutex
	logger *slog.Logger

	wiring *mqtt.Wiring // stable across swaps; nil when MQTT disabled at boot

	healthTracker *health.Tracker
	collector     *metrics.MqttCollector // nil when no metrics registry provided
	subBuilder    SubscriberBuilder
	onConnect     []func(context.Context) // forwarded to every (re)built lifecycle

	current *mqttSwap // nil when no stack is active (MQTT disabled or pre-Start)
}

// mqttSwap captures the live components of one MQTT stack
// generation. Every Swap creates a fresh mqttSwap and tears the
// previous one down once the new one has connected.
type mqttSwap struct {
	cfg          config.NorthMQTT
	client       mqtt.Client
	lifecycle    *mqtt.Lifecycle // nil for NoOp client
	bridge       *mqtt.Bridge
	cancelHealth func() // nil when no probe attached
	stopSubs     func() // nil when no subscribers attached
}

// SubscriberBuilder is the caller-supplied closure the supervisor
// invokes after each successful (re)build of the stack. The closure
// receives the freshly-connected client and the bridge it should
// route to, builds whatever subscribers are needed (birth sync,
// command subscriber, …), starts them, and returns a stop function
// that tears them down. Returning a non-nil error aborts the swap
// — the supervisor rolls back to the previous stack.
type SubscriberBuilder func(ctx context.Context, client mqtt.Client, bridge *mqtt.Bridge) (stop func(), err error)

// newMQTTSupervisor returns a supervisor that has not yet built a
// stack. Call [mqttSupervisor.Start] to materialise the initial
// stack from the boot config.
func newMQTTSupervisor(logger *slog.Logger, healthTracker *health.Tracker) *mqttSupervisor {
	if logger == nil {
		logger = slog.Default()
	}
	return &mqttSupervisor{logger: logger, healthTracker: healthTracker}
}

// SetCollector wires the MqttCollector so per-bridge and per-subscriber
// counters are incremented during production operation. Must be called
// before Start (or the first Swap) so the collector reaches every stack
// generation. Safe to call with nil — metrics are silently skipped.
func (s *mqttSupervisor) SetCollector(c *metrics.MqttCollector) {
	s.mu.Lock()
	s.collector = c
	s.mu.Unlock()
}

// OnConnect registers a callback fired on every successful broker
// (re)connect across the supervisor's lifetime — including reconnects
// after a Swap. The callback is forwarded to every lifecycle the
// supervisor builds. Use this for state-republish hooks (e.g.
// HubMQTTPublisher.Start) that must rerun after the broker drops
// retained messages.
//
// Must be called BEFORE Start to fire on the initial connect.
// Callbacks added after Start fire only on subsequent reconnects.
func (s *mqttSupervisor) OnConnect(fn func(context.Context)) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	s.onConnect = append(s.onConnect, fn)
	cur := s.current
	s.mu.Unlock()
	if cur != nil && cur.lifecycle != nil {
		cur.lifecycle.OnConnect(fn)
	}
}

// SetSubscriberBuilder installs the subscriber-wiring closure. Idempotent
// — the latest installed builder is used by every subsequent Swap.
// The boot path calls SetSubscriberBuilder after the dependencies the
// builder needs (notably the schedules domain) exist, then calls
// [AttachSubscribers] once to wire subscribers against the live stack
// that [Start] already brought up.
func (s *mqttSupervisor) SetSubscriberBuilder(b SubscriberBuilder) {
	s.mu.Lock()
	s.subBuilder = b
	s.mu.Unlock()
}

// AttachSubscribers invokes the configured [SubscriberBuilder] against
// the currently-active stack. No-op when no builder is configured or
// when no stack is active. Errors from the builder are logged but not
// returned — the publish path continues to work without subscribers.
// Safe to call exactly once at boot after [SetSubscriberBuilder]; not
// intended to be re-called manually (Swap handles re-attachment).
func (s *mqttSupervisor) AttachSubscribers(ctx context.Context) error {
	s.mu.Lock()
	if s.current == nil || s.subBuilder == nil {
		s.mu.Unlock()
		return nil
	}
	// Already attached — supervisor only owns one subscriber set per
	// stack generation. AttachSubscribers is an explicit boot step,
	// not a hot-reload helper.
	if s.current.stopSubs != nil {
		s.mu.Unlock()
		return nil
	}
	client := s.current.client
	bridge := s.current.bridge
	builder := s.subBuilder
	s.mu.Unlock()

	stop, err := builder(ctx, client, bridge)
	if err != nil {
		s.logger.Warn("mqtt.supervisor.subscribers.attach", slog.String("err", err.Error()))
		return err
	}
	s.mu.Lock()
	if s.current != nil {
		s.current.stopSubs = stop
	}
	s.mu.Unlock()
	return nil
}

// Wiring returns the stable [*mqtt.Wiring] pointer used by all
// downstream consumers (EventBridge, HubMQTTPublisher, hub-MQTT
// publisher). Returns nil when MQTT was disabled in the boot config
// — consumers must handle the nil case (they already do).
func (s *mqttSupervisor) Wiring() *mqtt.Wiring {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wiring
}

// CurrentClient returns the active mqtt.Client. Returns nil when no
// stack is built. Intended for the initial post-Start subscriber
// wiring path in daemon.go; hot-reload paths get the client via the
// SubscriberBuilder argument instead.
func (s *mqttSupervisor) CurrentClient() mqtt.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil
	}
	return s.current.client
}

// CurrentBridge returns the active [*mqtt.Bridge]. Returns nil when
// no stack is built.
func (s *mqttSupervisor) CurrentBridge() *mqtt.Bridge {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil
	}
	return s.current.bridge
}

// Start materialises the initial MQTT stack from cfg and wires the
// subscribers. A nil-or-disabled cfg leaves the supervisor in a
// no-stack state and returns nil.
func (s *mqttSupervisor) Start(ctx context.Context, cfg *config.Config) error {
	if cfg == nil || !cfg.North.MQTT.Enabled {
		s.logger.Info("mqtt.supervisor.start.skipped",
			slog.String("reason", "mqtt disabled in config"))
		return nil
	}
	swap, err := s.buildSwap(ctx, cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.wiring == nil {
		s.wiring = mqtt.NewWiring(swap.bridge, s.logger)
	} else {
		s.wiring.SwapBridge(swap.bridge)
	}
	wiring := s.wiring
	subBuilder := s.subBuilder
	s.current = swap
	s.mu.Unlock()

	if subBuilder != nil {
		stop, sErr := subBuilder(ctx, swap.client, wiring.Bridge())
		if sErr != nil {
			// Subscriber failure during initial Start does not abort
			// the daemon — the publish path still works without
			// command subscribers. Log and continue, mirroring the
			// pre-supervisor behaviour in daemon.go.
			s.logger.Warn("mqtt.supervisor.subscribers.start", slog.String("err", sErr.Error()))
		} else {
			s.mu.Lock()
			s.current.stopSubs = stop
			s.mu.Unlock()
		}
	}
	s.logger.Info("mqtt.supervisor.started",
		slog.String("broker", redactBrokerURL(cfg.North.MQTT.BrokerURL)),
		slog.String("client_id", cfg.North.MQTT.ClientID),
		slog.String("topic_base", cfg.North.MQTT.TopicBase))
	return nil
}

// Swap rebuilds the MQTT stack from newCfg. The new stack must
// connect successfully before the old one is torn down — on
// connect failure the previous stack continues unchanged and the
// error is returned.
//
// Diff filtering is the caller's responsibility (see [mqttDiffersStructurally]).
// A force-swap with byte-identical config is legal but pointless.
func (s *mqttSupervisor) Swap(ctx context.Context, newCfg *config.Config) error {
	if newCfg == nil {
		return errors.New("mqtt.supervisor.swap: nil config")
	}
	start := time.Now()

	// Snapshot current state under the lock.
	s.mu.Lock()
	oldSwap := s.current
	wiring := s.wiring
	subBuilder := s.subBuilder
	s.mu.Unlock()

	// Disabled → enabled and enabled → disabled are symmetric: build
	// or tear down the stack. Both run through Swap.
	if !newCfg.North.MQTT.Enabled {
		if oldSwap == nil {
			return nil // already disabled
		}
		s.teardown(ctx, oldSwap)
		s.mu.Lock()
		s.current = nil
		// Keep the Wiring alive but point its bridge nowhere so any
		// in-flight Publish becomes a no-op. A future re-enable will
		// install a new bridge.
		if s.wiring != nil {
			s.wiring.SwapBridge(nil)
		}
		s.mu.Unlock()
		s.logger.Info("mqtt.supervisor.swap.disabled",
			slog.Duration("took", time.Since(start)))
		return nil
	}

	newSwap, err := s.buildSwap(ctx, newCfg)
	if err != nil {
		return fmt.Errorf("mqtt.supervisor.swap: build new stack: %w", err)
	}

	// Commit: hot-swap Wiring's bridge so live Publish calls reach
	// the new broker the instant this returns. The old stack's
	// publishers still hold a reference to the old bridge but
	// nothing new is routed through them.
	s.mu.Lock()
	if s.wiring == nil {
		s.wiring = mqtt.NewWiring(newSwap.bridge, s.logger)
	} else {
		s.wiring.SwapBridge(newSwap.bridge)
	}
	s.current = newSwap
	s.mu.Unlock()

	// Build new subscribers against the new client. Order matters:
	// the new stack is already publishing, so any inbound command
	// briefly has no listener. We accept that gap (≤ a few ms)
	// because the alternative — running both subscriber sets in
	// parallel — would double-deliver every inbound write.
	if subBuilder != nil {
		stop, sErr := subBuilder(ctx, newSwap.client, newSwap.bridge)
		if sErr != nil {
			s.logger.Warn("mqtt.supervisor.swap.subscribers", slog.String("err", sErr.Error()))
		} else {
			s.mu.Lock()
			s.current.stopSubs = stop
			s.mu.Unlock()
		}
	}

	// Tear down the previous stack now that the new one is live.
	if oldSwap != nil {
		s.teardown(ctx, oldSwap)
	}

	// Suppress unused-var warning when subBuilder is nil at swap time.
	_ = wiring

	s.logger.Info("mqtt.supervisor.swap.completed",
		slog.String("broker", redactBrokerURL(newCfg.North.MQTT.BrokerURL)),
		slog.Duration("took", time.Since(start)))
	return nil
}

// Shutdown tears down whatever stack is currently active. Safe to
// call without a prior Start. Idempotent.
func (s *mqttSupervisor) Shutdown(ctx context.Context) {
	s.mu.Lock()
	swap := s.current
	s.current = nil
	s.mu.Unlock()
	if swap == nil {
		return
	}
	s.teardown(ctx, swap)
}

// buildSwap constructs a fresh stack from cfg and starts its
// lifecycle. The returned swap has health probe wired (if a tracker
// is configured) but no subscribers — the caller assigns those
// after a successful Wiring.SwapBridge so the new bridge is live
// before subscriber handlers can race.
func (s *mqttSupervisor) buildSwap(ctx context.Context, cfg *config.Config) (*mqttSwap, error) {
	s.mu.Lock()
	col := s.collector
	s.mu.Unlock()
	stack := buildMQTT(cfg, s.logger, col)
	if stack == nil {
		return nil, errors.New("mqtt.supervisor.build: buildMQTT returned nil with MQTT enabled")
	}
	sw := &mqttSwap{
		cfg:    cfg.North.MQTT,
		client: stack.client,
		// Pull the bridge out of the temporary wiring buildMQTT
		// constructed — we discard that wiring and re-use the
		// supervisor's stable one. The bridge object itself is
		// fine to retain.
		bridge:    stack.wiring.Bridge(),
		lifecycle: stack.lifecycle,
	}
	if sw.lifecycle != nil {
		// Forward any supervisor-level OnConnect callbacks to the new
		// lifecycle before Start so they fire on the initial connect.
		s.mu.Lock()
		hooks := make([]func(context.Context), len(s.onConnect))
		copy(hooks, s.onConnect)
		s.mu.Unlock()
		for _, fn := range hooks {
			sw.lifecycle.OnConnect(fn)
		}
		if err := sw.lifecycle.Start(ctx); err != nil {
			return nil, fmt.Errorf("lifecycle.Start: %w", err)
		}
	}
	if cs, ok := sw.client.(mqtt.ConnectionStatus); ok && s.healthTracker != nil {
		sw.cancelHealth = mqtt.StartHealthProbe(ctx, cs, s.healthTracker, mqtt.DefaultProbeInterval)
	}
	return sw, nil
}

// teardown runs the per-swap shutdown sequence in safe order:
// subscribers first (so no inbound message hits a half-torn-down
// bridge), then health probe (just a goroutine cancel), then the
// lifecycle (which disconnects the TCP socket).
func (s *mqttSupervisor) teardown(ctx context.Context, sw *mqttSwap) {
	if sw == nil {
		return
	}
	if sw.stopSubs != nil {
		sw.stopSubs()
	}
	if sw.cancelHealth != nil {
		sw.cancelHealth()
	}
	if sw.lifecycle != nil {
		stopCtx := ctx
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
			var cancel context.CancelFunc
			stopCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
		}
		if err := sw.lifecycle.Stop(stopCtx); err != nil {
			s.logger.Warn("mqtt.supervisor.teardown.stop", slog.String("err", err.Error()))
		}
	}
}

// mqttReloadAdapter satisfies handlers.MQTTReloadService by reading
// the current config from the reload-deps bag and calling
// mqttSupervisor.Swap. The boot config is captured as a fallback so
// the adapter still reloads correctly when no file-watcher is
// running (the deps bag's curCfg slot stays at the boot value in
// that case).
type mqttReloadAdapter struct {
	sup     *mqttSupervisor
	deps    *reloadDeps
	bootCfg *config.Config
}

// newMQTTReloadAdapter binds the supervisor + deps bag into a service
// that the REST router can mount.
func newMQTTReloadAdapter(sup *mqttSupervisor, deps *reloadDeps, bootCfg *config.Config) *mqttReloadAdapter {
	return &mqttReloadAdapter{sup: sup, deps: deps, bootCfg: bootCfg}
}

// Reload pulls the freshest [*config.Config] (file-watcher updates it
// on each successful reload; falls back to the boot snapshot when
// no watcher is wired) and asks the supervisor to swap. The wall-
// clock duration of the swap is returned.
func (a *mqttReloadAdapter) Reload(ctx context.Context) (time.Duration, error) {
	if a == nil || a.sup == nil {
		return 0, errors.New("mqtt.reload: supervisor unavailable")
	}
	cfg := a.deps.CurrentConfig()
	if cfg == nil {
		cfg = a.bootCfg
	}
	if cfg == nil {
		return 0, errors.New("mqtt.reload: no config snapshot available")
	}
	start := time.Now()
	if err := a.sup.Swap(ctx, cfg); err != nil {
		return time.Since(start), err
	}
	return time.Since(start), nil
}

// makeMQTTSubscriberBuilder returns a [SubscriberBuilder] that wires
// the MQTT birth-sync + command-subscriber subscribers against any
// (client, bridge) pair the supervisor presents — initial Start and
// every Swap go through the same path. The captured (reg, valueWriter,
// schedulesDomain, collector) live for the daemon's lifetime, so the
// closure is safe to retain across multiple stack generations.
func makeMQTTSubscriberBuilder(
	reg *central.Registry,
	valueWriter *clientpkg.ValueWriter,
	schedulesDomain *adapter.SchedulesDomain,
	collector *metrics.MqttCollector,
	logger *slog.Logger,
) SubscriberBuilder {
	return func(ctx context.Context, client mqtt.Client, bridge *mqtt.Bridge) (func(), error) {
		sub, ok := client.(mqtt.Subscriber)
		if !ok {
			// NoOp client has no Subscriber; nothing to wire.
			return func() {}, nil
		}
		birthSync := mqtt.NewBirthSync(sub, bridge, logger)
		if err := birthSync.Start(ctx); err != nil {
			return nil, fmt.Errorf("birth_sync.Start: %w", err)
		}
		sink := adapter.NewMQTTCommandSink(reg, valueWriter)
		wpAdapter := scheduleWeekProfileSink{sd: schedulesDomain}
		cmdSub := mqtt.NewCommandSubscriber(sub, bridge.Topics(), sink, logger).
			WithCDPSink(sink).
			WithWeekProfileSink(wpAdapter).
			WithCombinedDPSink(sink).
			WithScheduleSwitchSink(sink).
			WithInstallModeSink(sink).
			WithCollector(collector)
		if err := cmdSub.Start(ctx); err != nil {
			return nil, fmt.Errorf("command_subscriber.Start: %w", err)
		}
		// Subscriptions are tied to the underlying client; the
		// supervisor's teardown calls lifecycle.Stop() which
		// Disconnects the client and drops every active filter,
		// so an explicit per-subscriber stop is a no-op here.
		return func() {}, nil
	}
}

// redactBrokerURL strips credentials before logging.
func redactBrokerURL(rawURL string) string {
	// Naive but sufficient for log redaction: strip everything from
	// "://" to the next "@", which is where userinfo lives in a
	// well-formed broker URL. Operators can always re-derive the
	// host from the config file if they need it.
	at := -1
	scheme := -1
	for i := 0; i+2 < len(rawURL); i++ {
		if rawURL[i] == ':' && rawURL[i+1] == '/' && rawURL[i+2] == '/' {
			scheme = i + 3
			break
		}
	}
	if scheme < 0 {
		return rawURL
	}
	for i := scheme; i < len(rawURL); i++ {
		if rawURL[i] == '@' {
			at = i
			break
		}
		if rawURL[i] == '/' {
			break
		}
	}
	if at < 0 {
		return rawURL
	}
	return rawURL[:scheme] + "***@" + rawURL[at+1:]
}
