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

	"github.com/SukramJ/openccu-loom/internal/addonupdate"
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
	// lifecycleMu serialises whole stack transitions — Start, Swap and
	// Shutdown — against each other. mu only guards the field reads and
	// writes inside them, which is not enough: Swap snapshots the live
	// stack, releases mu to build and connect the replacement, then
	// re-takes mu to commit. Two concurrent Swaps (the config watcher's
	// tick and POST /admin/mqtt/reload are independent callers) therefore
	// capture the same predecessor, both commit, and the loser's
	// fully-connected client plus its subscribers are referenced by
	// nothing that could ever tear them down. Held across the build so a
	// transition is atomic end to end.
	lifecycleMu sync.Mutex

	mu     sync.Mutex
	logger *slog.Logger

	wiring *mqtt.Wiring // stable across swaps; allocated by Start, nil before it

	healthTracker *health.Tracker
	collector     *metrics.MqttCollector // nil when no metrics registry provided
	// channelHidden reports whether the operator has hidden a channel (G12) so
	// the (re)built bridge skips its MQTT state. Nil disables the gate.
	channelHidden func(central, channelAddress string) bool
	// centralNames resolves the centrals the daemon currently serves, for the
	// retained bridge/health payload every (re)built bridge republishes. Nil
	// falls back to the boot config, which misses a CCU adopted at runtime.
	centralNames func() []string
	subBuilder   SubscriberBuilder
	onConnect    []func(context.Context) // forwarded to every (re)built lifecycle

	current *mqttSwap // nil when no stack is active (MQTT disabled or pre-Start)

	// retryCancel stops the background connect-retry loop [Start] launches
	// when the very first broker connect fails. Nil while no retry runs.
	retryCancel context.CancelFunc
	// retryDelay / retryMaxDelay bound that loop's exponential backoff.
	// Fields rather than constants so a test can drive the loop without
	// waiting out real broker timings.
	retryDelay    time.Duration
	retryMaxDelay time.Duration
}

// Bounds of the boot-connect retry backoff. The upper bound is what an
// operator waits at worst after the broker comes back, and it also paces the
// connect-failure log line each attempt emits.
const (
	defaultMQTTRetryDelay    = 5 * time.Second
	defaultMQTTRetryMaxDelay = 2 * time.Minute
)

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
	// hooksAttached is set by [mqttSupervisor.announceConnected] once it has
	// forwarded the supervisor's connect callbacks to this generation's
	// lifecycle. Until then [mqttSupervisor.OnConnect] must NOT forward a
	// newly registered callback itself: the swap is published before the
	// callbacks are forwarded, so a callback landing in that window would be
	// registered by both paths and then fire twice on every reconnect —
	// republishing the initial snapshot and re-Starting the hub publisher one
	// extra time per drop. Written and read under mqttSupervisor.mu.
	hooksAttached bool
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
//
// centralNames resolves the live central set for every stack generation the
// supervisor builds; a nil func makes each generation fall back to the boot
// config. It is a constructor argument rather than a setter because a bridge
// captures the supplier at build time and offers no way to re-point it.
func newMQTTSupervisor(logger *slog.Logger, healthTracker *health.Tracker, centralNames func() []string) *mqttSupervisor {
	if logger == nil {
		logger = slog.Default()
	}
	return &mqttSupervisor{
		logger:        logger,
		healthTracker: healthTracker,
		centralNames:  centralNames,
		retryDelay:    defaultMQTTRetryDelay,
		retryMaxDelay: defaultMQTTRetryMaxDelay,
	}
}

// SetChannelHidden wires the operator-hidden-channel gate (G12) that every
// (re)built bridge consults. A nil fn disables the gate.
//
// The bridge captures the gate at build time and offers no setter afterwards,
// so this MUST run before [mqttSupervisor.Start] — a gate installed later
// reaches only the bridges a subsequent Swap builds, and the boot-built bridge
// that lives for the whole daemon lifetime keeps publishing hidden channels.
// [wireSharedInfrastructure] takes the overlay as a parameter for exactly that
// reason.
func (s *mqttSupervisor) SetChannelHidden(fn func(central, channelAddress string) bool) {
	s.mu.Lock()
	s.channelHidden = fn
	s.mu.Unlock()
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
	// Forward only to a generation that has already been handed the callback
	// list; one that has not will pick fn up from s.onConnect itself. Both
	// facts are read under the same lock announceConnected takes, so fn is
	// registered exactly once whichever side of that window it lands on.
	forward := cur != nil && cur.lifecycle != nil && cur.hooksAttached
	s.mu.Unlock()
	if forward {
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
// publisher). [Start] allocates it whatever the boot config says, so a
// consumer that binds it after Start binds the pointer every later stack
// is installed into. Returns nil only before Start has run — consumers
// must still handle that case (they already do).
func (s *mqttSupervisor) Wiring() *mqtt.Wiring {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wiring
}

// ensureWiring returns the process-stable Wiring pointing at b, allocating
// it on first use. Every north-bound consumer captures this pointer once at
// composition time, so the supervisor has to keep handing out the same one
// for its whole lifetime; a bridge-less Wiring drops publishes instead of
// routing them, which is exactly what "MQTT is off right now" should do.
func (s *mqttSupervisor) ensureWiring(b *mqtt.Bridge) *mqtt.Wiring {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wiring == nil {
		s.wiring = mqtt.NewWiring(b, s.logger)
		return s.wiring
	}
	s.wiring.SwapBridge(b)
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
// no-stack state — but with the stable Wiring allocated — and returns nil.
func (s *mqttSupervisor) Start(ctx context.Context, cfg *config.Config) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if cfg == nil || !cfg.North.MQTT.Enabled {
		// The Wiring is allocated even with MQTT off, for the same reason the
		// failed-connect branch below allocates it: every consumer binds this
		// pointer once at composition time, so handing them nil is permanent.
		// An operator who enables MQTT later (SPA save + reload, config
		// watcher) would otherwise get a connected broker that no bridge, hub
		// or system-status publisher ever reaches — the Swap installs its
		// bridge into a Wiring nobody holds. A bridge-less Wiring publishes
		// nowhere, so it costs nothing while MQTT stays off.
		s.ensureWiring(nil)
		s.logger.Info("mqtt.supervisor.start.skipped",
			slog.String("reason", "mqtt disabled in config"))
		return nil
	}
	swap, err := s.buildSwap(ctx, cfg)
	if err != nil {
		// A broker that is not accepting connections yet — the co-located
		// broker add-on still booting is the common case — must not disable
		// the MQTT plane for the process lifetime. Two things make that
		// happen, and both are handled here.
		//
		// First, every consumer captures the [mqtt.Wiring] pointer once at
		// composition time; handing them nil is permanent, and a later Swap
		// builds a Wiring they do not hold. Publishing through a
		// bridge-less Wiring is a no-op, so the stable pointer costs nothing
		// while the link is down.
		//
		// Second, the lifecycle's reconnect loop only starts after a
		// successful first connect, so nothing retries on its own.
		s.ensureWiring(nil)
		s.retryInitialConnect(ctx, cfg)
		return err
	}
	wiring := s.ensureWiring(swap.bridge)
	s.mu.Lock()
	subBuilder := s.subBuilder
	s.current = swap
	s.mu.Unlock()
	// Only now, with the new bridge live behind the shared Wiring, may the
	// connect callbacks run — see [mqttSupervisor.announceConnected].
	s.announceConnected(ctx, swap)

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
			// Record against the stack this Start built, not against
			// whatever s.current happens to hold: Shutdown clears it.
			if s.current == swap {
				s.current.stopSubs = stop
			} else {
				stop()
			}
			s.mu.Unlock()
		}
	}
	s.logger.Info("mqtt.supervisor.started",
		slog.String("broker", redactBrokerURL(cfg.North.MQTT.BrokerURL)),
		slog.String("client_id", cfg.North.MQTT.ClientID),
		slog.String("topic_base", cfg.North.MQTT.TopicBase))
	return nil
}

// retryInitialConnect re-attempts the boot-time stack build in the background
// until the broker accepts a connection, then returns. It goes through [Swap]
// so a recovered link installs the new bridge into the Wiring every consumer
// already holds, re-attaches the subscribers and fires the OnConnect
// republish hooks — i.e. the daemon ends up in the state a successful boot
// would have produced, without a restart.
//
// ctx is the daemon-lifetime context [Start] received; [Shutdown] cancels the
// loop early. Only one loop runs at a time, and it stops as soon as any other
// path (a config reload, POST /mqtt/reload) has built a stack.
func (s *mqttSupervisor) retryInitialConnect(ctx context.Context, cfg *config.Config) {
	s.mu.Lock()
	if s.retryCancel != nil {
		s.mu.Unlock()
		return
	}
	retryCtx, cancel := context.WithCancel(ctx)
	s.retryCancel = cancel
	delay, maxDelay := s.retryDelay, s.retryMaxDelay
	s.mu.Unlock()

	if delay <= 0 {
		delay = defaultMQTTRetryDelay
	}
	if maxDelay < delay {
		maxDelay = delay
	}

	go func() {
		defer func() {
			cancel()
			s.mu.Lock()
			s.retryCancel = nil
			s.mu.Unlock()
		}()
		for {
			select {
			case <-retryCtx.Done():
				return
			case <-time.After(delay):
			}
			s.mu.Lock()
			live := s.current != nil
			s.mu.Unlock()
			if live {
				return
			}
			if err := s.Swap(retryCtx, cfg); err == nil {
				s.logger.Info("mqtt.supervisor.connect.recovered",
					slog.String("broker", redactBrokerURL(cfg.North.MQTT.BrokerURL)))
				return
			}
			if delay *= 2; delay > maxDelay {
				delay = maxDelay
			}
		}
	}()
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
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	start := time.Now()

	// Snapshot current state under the lock.
	s.mu.Lock()
	oldSwap := s.current
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
	s.ensureWiring(newSwap.bridge)
	s.mu.Lock()
	s.current = newSwap
	s.mu.Unlock()
	// Republish hooks run against the committed bridge, never against the
	// predecessor — see [mqttSupervisor.announceConnected].
	s.announceConnected(ctx, newSwap)

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
			// Shutdown may have raced in and cleared s.current; stop the
			// subscribers we just built rather than writing through a nil
			// pointer or hanging them off a foreign generation.
			if s.current == newSwap {
				s.current.stopSubs = stop
			} else {
				stop()
			}
			s.mu.Unlock()
		}
	}

	// Tear down the previous stack now that the new one is live.
	if oldSwap != nil {
		s.teardown(ctx, oldSwap)
	}

	s.logger.Info("mqtt.supervisor.swap.completed",
		slog.String("broker", redactBrokerURL(newCfg.North.MQTT.BrokerURL)),
		slog.Duration("took", time.Since(start)))
	return nil
}

// Shutdown tears down whatever stack is currently active. Safe to
// call without a prior Start. Idempotent.
func (s *mqttSupervisor) Shutdown(ctx context.Context) {
	// Cancel the boot-connect retry before waiting on lifecycleMu: that loop
	// runs Swap, which holds the lock, and a shutdown that queued behind a
	// full reconnect attempt would stall the daemon exit.
	s.mu.Lock()
	if s.retryCancel != nil {
		s.retryCancel()
	}
	s.mu.Unlock()

	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
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
	hidden := s.channelHidden
	names := s.centralNames
	s.mu.Unlock()
	stack := buildMQTT(cfg, s.logger, col, hidden, names)
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
		// The supervisor's OnConnect callbacks are deliberately NOT forwarded
		// here: Lifecycle.Start performs the first connect synchronously and
		// fires them from inside it, which is before the caller commits this
		// bridge into the shared Wiring — so every republish would route
		// through the predecessor bridge (or, after a failed boot connect,
		// through none at all) and silently reach nothing.
		// [mqttSupervisor.announceConnected] attaches and runs them once the
		// commit has happened.
		if err := sw.lifecycle.Start(ctx); err != nil {
			logConnectFailure(s.logger, cfg.North.MQTT, err)
			return nil, fmt.Errorf("lifecycle.Start: %w", err)
		}
	}
	if cs, ok := sw.client.(mqtt.ConnectionStatus); ok && s.healthTracker != nil {
		sw.cancelHealth = mqtt.StartHealthProbe(ctx, cs, s.healthTracker, mqtt.DefaultProbeInterval)
	}
	return sw, nil
}

// announceConnected hands the supervisor's connect callbacks to the freshly
// built lifecycle — so later reconnects fire them — and runs them once for the
// connect [buildSwap] already performed.
//
// It must be called AFTER the new bridge is committed into the shared Wiring.
// Every callback republishes through that Wiring (hub discovery, the raw-plane
// snapshot), and Lifecycle.Start fires the first connect synchronously, before
// any commit could have happened: a callback attached ahead of Start therefore
// publishes onto the previous bridge, or onto no bridge at all when the boot
// connect had failed. That is what left a recovered link with a live broker and
// no state on it.
func (s *mqttSupervisor) announceConnected(ctx context.Context, sw *mqttSwap) {
	if sw == nil || sw.lifecycle == nil {
		return
	}
	s.mu.Lock()
	hooks := make([]func(context.Context), len(s.onConnect))
	copy(hooks, s.onConnect)
	// From here on [mqttSupervisor.OnConnect] forwards to this generation
	// itself; everything registered before this point is in hooks.
	sw.hooksAttached = true
	s.mu.Unlock()
	for _, fn := range hooks {
		sw.lifecycle.OnConnect(fn)
	}
	for _, fn := range hooks {
		fn(ctx)
	}
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

// mqttReloadAdapter satisfies handlers.MQTTReloadService by re-assembling the
// effective config and calling mqttSupervisor.Swap. The boot config is
// captured as a last-resort fallback so the adapter still reloads when neither
// an assembler nor a file-watcher is wired.
type mqttReloadAdapter struct {
	sup     mqttSwapper
	deps    *reloadDeps
	bootCfg *config.Config
	logger  *slog.Logger
}

// mqttSwapper is the one supervisor capability the reload adapter needs.
// Naming it keeps the adapter's own logic — which config it hands over, and
// what it records afterwards — testable without standing up a broker stack.
type mqttSwapper interface {
	Swap(ctx context.Context, newCfg *config.Config) error
}

// newMQTTReloadAdapter binds the supervisor + deps bag into a service
// that the REST router can mount.
func newMQTTReloadAdapter(sup *mqttSupervisor, deps *reloadDeps, bootCfg *config.Config, logger *slog.Logger) *mqttReloadAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	a := &mqttReloadAdapter{deps: deps, bootCfg: bootCfg, logger: logger}
	// Guard the typed-nil trap: assigning a nil *mqttSupervisor to the
	// interface field would produce a non-nil interface holding a nil pointer,
	// and the "supervisor unavailable" check in Reload would never fire.
	if sup != nil {
		a.sup = sup
	}
	return a
}

// Reload re-derives the effective config and asks the supervisor to swap,
// returning the wall-clock duration of the swap.
//
// The assembly must be fresh: an operator who edits north.mqtt in the SPA
// writes the DB-tier section, which no file-watcher event follows, so reading
// only the last recorded snapshot would rebuild the stack from the config the
// daemon booted with — the reload would appear to succeed while the broker
// link kept the previous credentials. On success the newly assembled config
// becomes the recorded snapshot so subsequent readers agree with the running
// stack.
func (a *mqttReloadAdapter) Reload(ctx context.Context) (time.Duration, error) {
	if a == nil || a.sup == nil {
		return 0, errors.New("mqtt.reload: supervisor unavailable")
	}
	cfg, fresh := a.deps.AssembleConfig(ctx)
	if cfg == nil {
		cfg = a.bootCfg
	}
	if cfg == nil {
		return 0, errors.New("mqtt.reload: no config snapshot available")
	}
	if !fresh {
		a.logger.Warn("mqtt.reload.stale_config",
			slog.String("effect", "reloading from the last recorded snapshot; section edits saved since boot may not be applied"))
	}
	start := time.Now()
	if err := a.sup.Swap(ctx, cfg); err != nil {
		return time.Since(start), err
	}
	a.deps.SetCurrentConfig(cfg)
	return time.Since(start), nil
}

// makeMQTTSubscriberBuilder returns a [SubscriberBuilder] that wires
// the MQTT birth-sync + command-subscriber subscribers against any
// (client, bridge) pair the supervisor presents — initial Start and
// every Swap go through the same path. The captured (reg, valueWriter,
// schedulesDomain, collector) live for the daemon's lifetime, so the
// closure is safe to retain across multiple stack generations.
func makeMQTTSubscriberBuilder(
	lifecycleCtx context.Context,
	reg *central.Registry,
	valueWriter *clientpkg.ValueWriter,
	schedulesDomain *adapter.SchedulesDomain,
	collector *metrics.MqttCollector,
	alarmSink *alarmMQTTSink,
	addonUpdater *addonupdate.Updater,
	selectionLabels mqtt.ValueListLabeler,
	logger *slog.Logger,
) SubscriberBuilder {
	return func(ctx context.Context, client mqtt.Client, bridge *mqtt.Bridge) (func(), error) {
		sub, ok := client.(mqtt.Subscriber)
		if !ok {
			// NoOp client has no Subscriber; nothing to wire.
			return func() {}, nil
		}
		// Bound RepublishDiscovery to the daemon-lifetime ctx (not the
		// per-Start/Swap ctx) so a broker-restart-triggered republish is
		// cancelled on shutdown but survives a broker swap — same rationale
		// as the command subscriber below.
		//
		// Both subscriber constructors start their dispatcher worker
		// goroutines immediately, and only Close stops them — so every early
		// return from here on closes what it already built. Without that a
		// failed build (a broker ACL rejecting one of the command
		// subscriptions) leaves nine goroutines running with no handle left
		// to stop them.
		birthSync := mqtt.NewBirthSync(sub, bridge, logger).WithLifecycleContext(lifecycleCtx)
		if err := birthSync.Start(ctx); err != nil {
			birthSync.Close()
			return nil, fmt.Errorf("birth_sync.Start: %w", err)
		}
		// The labeler turns a localised siren tone or light effect the
		// operator picked in HA back into the wire token the device
		// speaks; without it those commands resolve to nothing.
		sink := adapter.NewMQTTCommandSink(reg, valueWriter).WithSelectionLabeler(selectionLabels)
		wpAdapter := scheduleWeekProfileSink{sd: schedulesDomain}
		cmdSub := mqtt.NewCommandSubscriber(sub, bridge.Topics(), sink, logger).
			WithCDPSink(sink).
			WithWeekProfileSink(wpAdapter).
			WithCombinedDPSink(sink).
			WithScheduleSwitchSink(sink).
			WithInstallModeSink(sink).
			// The `<central>` topic segment is TopicSafe-escaped on the
			// way out; the registry is what turns it back into the
			// configured name every sink is keyed on.
			WithCentralNames(reg).
			WithCollector(collector).
			// Capture the daemon-lifetime ctx (not the per-Start/Swap ctx,
			// which on a reload-triggered swap is request-scoped) so command
			// handlers cancel on shutdown but survive a broker swap.
			WithLifecycleContext(lifecycleCtx)
		// The alarm sink is a concrete pointer so the nil case is a clean
		// pointer check — passing a typed-nil through the interface would
		// make the subscriber treat the alarm plane as wired.
		if alarmSink != nil {
			cmdSub = cmdSub.WithAlarmSink(alarmSink)
		}
		// Same nil-pointer-check rationale as the alarm sink above.
		addonSink := newAddonUpdateMQTTSink(addonUpdater)
		if addonSink != nil {
			cmdSub = cmdSub.WithAddonUpdateSink(addonSink)
		}
		if err := cmdSub.Start(ctx); err != nil {
			cmdSub.Close()
			birthSync.Close()
			return nil, fmt.Errorf("command_subscriber.Start: %w", err)
		}

		// Add-on self-update (ADR 0057): one daemon-level HA `update`
		// entity, re-published on every broker (re)connect like the hub
		// singletons. OnChange is (re-)subscribed here rather than once at
		// daemon boot because the callback must publish through THIS
		// bridge instance — a broker swap builds a fresh bridge, and the
		// teardown this closure returns drops the previous subscription
		// before Swap installs the new one, so no publisher ever targets a
		// stale bridge.
		var addonUnsub func()
		if addonUpdater != nil {
			disco := bridge.DefaultBuilder()
			if disco == nil {
				disco = mqtt.NewDefaultDiscoveryBuilder(bridge.Topics(), "")
			}
			_ = bridge.PublishHubDiscovery(ctx, disco.BuildAddonUpdateDiscovery())
			publishState := func(st addonupdate.Status) {
				inProgress := st.State == addonupdate.StateChecking ||
					st.State == addonupdate.StateDownloading ||
					st.State == addonupdate.StateInstalling
				if err := bridge.PublishAddonUpdateState(lifecycleCtx, st.CurrentVersion, st.LatestVersion, inProgress); err != nil {
					logger.Warn("mqtt.publish_addon_update_state", slog.String("err", err.Error()))
				}
			}
			publishState(addonUpdater.Status())
			addonUnsub = addonUpdater.OnChange(publishState)
		}

		// Subscriptions are tied to the underlying client; the
		// supervisor's teardown calls lifecycle.Stop() which
		// Disconnects the client and drops every active filter, so an
		// explicit per-subscriber unsubscribe is a no-op for them.
		//
		// Their dispatchers are not: each subscriber owns worker goroutines
		// started in its constructor that exit only on Close, and every
		// stack swap builds a fresh pair. Closing them here — after the
		// queued republishes and commands have drained — keeps a reload from
		// leaking nine goroutines per generation. The addon-update OnChange
		// subscription is bound to the Updater rather than to the mqtt
		// client, so it needs its own unsubscribe.
		return func() {
			if addonUnsub != nil {
				addonUnsub()
			}
			cmdSub.Close()
			birthSync.Close()
		}, nil
	}
}

// redactBrokerURL strips credentials before logging.
// logConnectFailure records everything needed to place a rejected CONNECT
// without touching the broker or the daemon's config: which broker was dialled,
// under which identity and dialect, and — decisively — whether a username and
// password were actually on the wire.
//
// A broker answers a missing credential and a wrong one with the same
// "Not authorized (0x87)", so the error alone cannot distinguish "the operator
// typed the wrong password" from "the daemon sent none". Only the presence
// flags separate those, and they are what turns that ambiguity into a
// one-line diagnosis. Presence and length only — never the secret itself,
// since these lines end up in support logs and diagnostic bundles.
func logConnectFailure(logger *slog.Logger, cfg config.NorthMQTT, err error) {
	if logger == nil {
		return
	}
	protocol := cfg.ProtocolVersion
	if protocol == "" {
		protocol = "5"
	}
	logger.Error("mqtt.connect.failed",
		slog.String("err", err.Error()),
		slog.String("broker", redactBrokerURL(cfg.BrokerURL)),
		slog.String("client_id", cfg.ClientID),
		slog.String("protocol_version", protocol),
		slog.Bool("username_set", cfg.Username != ""),
		slog.Bool("password_set", cfg.Password != ""),
		slog.Int("password_len", len(cfg.Password)))
}

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
