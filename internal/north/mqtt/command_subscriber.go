// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
	hagomqtt "github.com/SukramJ/go-hamqtt/publisher/gomqtt"

	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"

	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
)

// CommandSink is the domain-facing write contract. The composition
// root wires this to the central's ValueWriter; tests can stub it.
type CommandSink interface {
	SetValue(ctx context.Context, centralName, interfaceID, channelAddress string,
		parameter hmenum.Parameter, value any, priority hmenum.CommandPriority) error
	SetMasterValue(ctx context.Context, centralName, interfaceID, channelAddress string,
		parameter hmenum.Parameter, value any, priority hmenum.CommandPriority) error
	SetSysvar(ctx context.Context, centralName, name string, payload any) error
	TriggerProgram(ctx context.Context, centralName, id string) error
	SetProgramEnabled(ctx context.Context, centralName, id string, enabled bool) error
}

// CentralNameLister supplies the configured central names so the
// subscriber can turn the `<central>` topic segment back into the name
// the domain is keyed on. The composition root wires the central
// registry; when nil the segment is passed through unchanged.
//
// It exists because every publisher escapes the central through
// [naming.TopicSafe] before it reaches the wire (space / `+` / `#` /
// `/` all become `_`), while every sink does an exact-key lookup —
// `Registry.Get(name)`, `ValueWriter`'s per-central backend map. A CCU
// configured as `Wohn Zimmer` therefore had every one of its MQTT
// commands dropped with "no backend", while its state topics kept
// updating and the plane looked healthy.
type CentralNameLister interface {
	// Names returns the configured central names, unescaped.
	Names() []string
}

// WeekProfileSink is the optional domain-facing contract for
// week-profile active-profile selection. The composition root wires
// this to the same backend as the REST `POST .../schedule/active-profile`
// endpoint. Optional because non-climate deployments need no profile
// switching; when nil, the subscriber drops profile commands with a
// debug breadcrumb.
//
// The interface carries the REST `ScheduleService.SetActiveProfile`
// arguments (address, channel, profile key) plus the central, interface
// and priority the MQTT topic supplies and REST resolves itself, so one
// implementation backs both surfaces behind a thin adapter. `profileKey`
// is one of "P1".."PN" — the implementation validates the key against
// the channel's [AvailableProfiles].
type WeekProfileSink interface {
	SetActiveProfile(ctx context.Context,
		centralName, interfaceID, deviceAddress string, channel int,
		profileKey string,
		priority hmenum.CommandPriority) error
}

// ScheduleSwitchSink is the optional domain-facing contract for
// schedule-channel switch writes (ScheduleChannelSwitch.TurnOn/Off).
// The composition root wires this to the central registry; when nil,
// the subscriber drops schedule-switch commands with a debug breadcrumb.
//
// `key` is the "<actor>_<sub>" channel key (e.g. "1_1"). The
// implementation resolves the device + ProfileDataPoint and dispatches
// SetScheduleEnabled.
type ScheduleSwitchSink interface {
	SetScheduleSwitch(ctx context.Context,
		centralName, interfaceID, deviceAddress string, channel int,
		key string, enabled bool,
		priority hmenum.CommandPriority) error
}

// CombinedDPSink is the optional domain-facing contract for combined-DP
// writes. The composition root wires this to the central registry; when
// nil, the subscriber drops combined-DP commands with a debug breadcrumb.
//
// `kind` matches the topic segment in the discovery payload's
// command_topic (e.g. "duration", "door_mode"). The implementation
// resolves the combined DP on the (central, iface, deviceAddr, channel)
// tuple and hands it the raw payload; the data point parses its own
// value. The subscriber deliberately does not parse — it used to coerce
// every combined payload to a float64 before dispatch, which made the
// transport the gatekeeper for value types it cannot know.
type CombinedDPSink interface {
	SetCombinedValue(ctx context.Context,
		centralName, interfaceID, deviceAddress string, channel int,
		kind, raw string,
		priority hmenum.CommandPriority) error
}

// InstallModeSink is the optional domain-facing contract for activating
// install/pairing mode on one interface. The composition root wires this
// to the central registry; when nil, the subscriber drops install-mode
// button presses with a debug breadcrumb.
//
// `iface` selects the interface's install-mode data point; `seconds` is
// the pairing-window duration (the implementation applies its own
// default when zero). Mirrors the REST `POST /install-mode/interfaces`
// shape so a single backend serves both surfaces.
type InstallModeSink interface {
	ActivateInstallMode(ctx context.Context,
		centralName, interfaceID string, seconds int) error
}

// AlarmSink is the optional domain-facing contract for the daemon-level
// alarm engine. The composition root wires it over the alarm service's
// engine (source "mqtt"); when nil the subscriber drops alarm commands
// with a debug breadcrumb. Zones are daemon-level, so the command topic
// omits the <central> segment every other command topic carries — a
// deliberate extension of the raw command plane (notes/concepts/alarm-concept.md
// §13.3). The reserved zone segment "master" routes to the aggregate
// arm/disarm verbs.
type AlarmSink interface {
	// Arm / Disarm / Silence carry the optional code parsed from the JSON
	// command envelope (notes/concepts/alarm-concept.md §11). The sink validates it
	// through the engine's code policy; an empty code is code-free.
	Arm(ctx context.Context, zoneID string, mode hmenum.AlarmMode, code string) error
	Disarm(ctx context.Context, zoneID, code string) error
	Silence(ctx context.Context, zoneID, code string) error
	// Panic fires the engine's loud panic path for an zone (the HA
	// TRIGGER command, notes/concepts/alarm-concept.md §7).
	Panic(ctx context.Context, zoneID string) error
	// Master verbs act on every zone at once and stay code-free — a single
	// code cannot express the union of per-zone policies (the individual
	// zone panels carry the code prompt).
	MasterArm(ctx context.Context, mode hmenum.AlarmMode) error
	MasterDisarm(ctx context.Context) error
	// ResetMotion clears the latched motion detectors of one zone; the
	// master form covers every zone. Unlike silence and trigger it has
	// a meaningful fleet-wide shape, so both are wired.
	ResetMotion(ctx context.Context, zoneID string) error
	MasterResetMotion(ctx context.Context) error
}

// AddonUpdateSink is the optional domain-facing contract for the
// daemon-level CCU add-on self-updater (ADR 0057). The composition
// root wires it over *addonupdate.Updater; when nil the subscriber
// drops the HA `update` entity's INSTALL command with a debug
// breadcrumb. Unlike every other sink this one is daemon-level (no
// central/interface/device scoping) — mirrors [AlarmSink]'s
// central-less command topic.
type AddonUpdateSink interface {
	// TriggerInstall starts the download/verify/stage/install
	// sequence. Mirrors [addonupdate.Updater.InstallAsync] — it
	// returns once the sequence has started, not once it finishes.
	TriggerInstall(ctx context.Context) error
}

// CDPInvocationSink is the domain-facing contract for Custom-DP
// operation dispatch. The composition root wires this to
// [adapter.MQTTCommandSink] which delegates to
// [adapter.CustomDPDispatcher]. Tests can stub it.
//
// The `central` argument is extracted from the MQTT topic so the sink
// can scope the device lookup to the right central registry entry.
type CDPInvocationSink interface {
	InvokeCustomDP(ctx context.Context,
		centralName, deviceAddress, name, operation string,
		params map[string]any,
		priority hmenum.CommandPriority) error

	// InvokeChannelService dispatches a service-method call to the
	// custom DP attached to the channel
	// (central, interfaceID, deviceAddress, channel). ADR 0009 — the
	// bridge calls this when an HA-Discovery command-topic write
	// arrives on `…/<chan>/svc/<method>/set`.
	//
	// Implementations look up the channel's custom DP via the central
	// registry and call its `Source.Invoke(ctx, method, params, priority)`.
	// Returns an error when no custom DP is attached or when the
	// service method is unknown.
	InvokeChannelService(ctx context.Context,
		centralName, interfaceID, deviceAddress string, channel int,
		method string, params map[string]any,
		priority hmenum.CommandPriority) error
}

// CDPInvokePayload is the JSON body expected on the CustomDPInvoke
// topic. `params` is forwarded verbatim to the CustomDPWriter;
// `priority` defaults to "high" when absent or empty.
type CDPInvokePayload struct {
	Params   map[string]any `json:"params"`
	Priority string         `json:"priority"`
}

// The worker count and per-worker backlog this plane runs on are the shared
// module's [hapublisher.DefaultCommandWorkers] (8) and
// [hapublisher.DefaultCommandQueueDepth] (32), left unstated in
// [hapublisher.CommandConfig] so the two cannot drift. They are not
// coincidentally the same numbers this file used to declare: the library took
// them from here, and its doc comments cite the same reasoning — high enough
// that one interface stalled behind the retry stack does not stall unrelated
// devices, low enough to bound the goroutine and CCU-request fan-out one
// command burst can produce.

// CommandSubscriber wires the bridge's inbound /set and /invoke topics
// back into the domain. It owns the typed sinks, the central-name
// resolution, the payload coercion and the metrics the shared module
// deliberately left to its consumers; the subscriptions, the routing and the
// worker pool underneath it are [hapublisher.CommandRouter]'s.
//
// It registers one wildcard route per inbound shape — data points (which
// carry the week-profile, combined-DP and schedule-switch shapes too),
// sysvars, programs, install mode, custom-DP invoke and service methods,
// alarm commands, add-on updates — over the raw-plane schema (ADR 0011):
//
//	<base>/<central>/<interface>/<device>/<channel>/<parameter>/set
//	<base>/<central>/sysvars/<name>/set
//	<base>/<central>/programs/<id>/trigger
//	<base>/<central>/devices/<device>/cdps/<name>/<operation>/invoke
type CommandSubscriber struct {
	sub       Subscriber
	topics    *TopicBuilder
	sink      CommandSink
	collector *metrics.MqttCollector // may be nil; counter increments are no-ops when nil
	cdpSink   CDPInvocationSink      // may be nil; CDP invocations are silently dropped when nil
	wpSink    WeekProfileSink        // may be nil; week-profile commands are dropped with a debug log when nil
	cmbSink   CombinedDPSink         // may be nil; combined-DP commands are dropped with a debug log when nil
	schedSink ScheduleSwitchSink     // may be nil; schedule-switch commands are dropped with a debug log when nil
	imSink    InstallModeSink        // may be nil; install-mode button presses are dropped with a debug log when nil
	alarmSink AlarmSink              // may be nil; alarm commands are dropped with a debug log when nil
	addonSink AddonUpdateSink        // may be nil; the add-on update INSTALL command is dropped with a debug log when nil
	// centrals resolves the escaped `<central>` topic segment back to a
	// configured central name (see [CentralNameLister]). May be nil, in
	// which case the segment is used verbatim.
	centrals CentralNameLister
	// qos is the QoS level every inbound command subscription registers
	// at. Defaults to QoS1 (at-least-once) in [NewCommandSubscriber] —
	// matching [DefaultQoS.Commands] — and can be overridden via
	// [CommandSubscriber.WithQoS], typically from the bridge's own
	// [BridgeConfig.QoS.Commands] so the two stay in lockstep.
	qos    QoS
	logger *slog.Logger
	// lifecycleCtx is the daemon-lifetime context wired via
	// WithLifecycleContext and handed to the router as
	// [hapublisher.CommandConfig.Lifecycle]; every command's own context
	// derives from it, so an in-flight CCU write is cancelled when the
	// daemon shuts down instead of running on a detached background
	// context. Defaults to context.Background() until wired.
	lifecycleCtx context.Context

	// routerMu guards router, which [CommandSubscriber.Start] installs and
	// [CommandSubscriber.Close] / [CommandSubscriber.WaitIdle] read. The
	// three are reachable from different goroutines in the supervisor: Start
	// runs on the stack builder, Close on the teardown a broker swap
	// triggers.
	routerMu sync.Mutex
	// router owns every command subscription and the worker pool each
	// handler runs on. It replaces this file's own ten Subscribe calls and
	// its boundedDispatcher, and it is built in
	// [CommandSubscriber.Start] rather than in the constructor for two
	// reasons: [hapublisher.CommandConfig] is read once, at construction, so
	// it has to be complete — every With* setter runs between the
	// constructor and Start — and [hapublisher.NewCommandRouter] starts no
	// goroutines, so a subscriber that never reaches Start owns none either.
	router *hapublisher.CommandRouter
}

// NewCommandSubscriber constructs the subscriber. Call
// [CommandSubscriber.Close] on teardown to unsubscribe every route and
// drain the handlers already accepted.
func NewCommandSubscriber(sub Subscriber, topics *TopicBuilder, sink CommandSink, logger *slog.Logger) *CommandSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &CommandSubscriber{
		sub: sub, topics: topics, sink: sink, qos: QoS1, logger: logger, lifecycleCtx: context.Background(),
	}
}

// Close unsubscribes every command route and blocks until every in-flight or
// already-queued command has finished running. Safe to call on a
// zero-value or nil *CommandSubscriber, before Start, and twice.
//
// The context is deliberately [context.Background] and not the lifecycle
// context: Close is a shutdown path, the lifecycle context is cancelled on
// the way into one, and an UNSUBSCRIBE sent under a dead context fails
// before it reaches the broker. The unsubscribes are best-effort either way
// — the supervisor's teardown disconnects the client right after, which
// drops every filter — so an error is a debug breadcrumb rather than a
// failure the caller can act on. The drain is not best-effort: it is the
// half that keeps a queued CCU write from being abandoned.
func (c *CommandSubscriber) Close() {
	if c == nil {
		return
	}
	r := c.currentRouter()
	if r == nil {
		return
	}
	if err := r.Stop(context.Background()); err != nil {
		c.logger.Debug("mqtt.command.stop", slog.String("err", err.Error()))
	}
}

// WaitIdle blocks until every command accepted before this call has run. It
// is a deterministic test barrier for callers that assert on a fake sink
// right after delivering a message — handlers run on the router's worker
// pool, not on the caller's goroutine, so such an assertion is otherwise a
// race. Production code does not need it: commands are fire-and-forget by
// design and shutdown is [CommandSubscriber.Close]'s job. Safe to call on a
// nil *CommandSubscriber and before Start (no-op).
func (c *CommandSubscriber) WaitIdle() {
	if c == nil {
		return
	}
	if r := c.currentRouter(); r != nil {
		r.WaitIdle()
	}
}

// currentRouter reads the router [CommandSubscriber.Start] installed, or nil
// before it has.
func (c *CommandSubscriber) currentRouter() *hapublisher.CommandRouter {
	c.routerMu.Lock()
	defer c.routerMu.Unlock()
	return c.router
}

// attributed reports whether the router accepted an overlapping pair of
// command filters and therefore subscribes with MQTT 5.0 Subscription
// Identifiers (§3.8.2.1.2).
//
// Unexported, like subscriptionID and checkDisjoint below: every caller is a
// test in this package, and an exported accessor nothing in the daemon
// reaches is dead production surface the dead-code ratchet would carry.
//
// It must be false here, and [CommandSubscriber.Start] refuses to put
// anything on the wire when it is not. This plane's ten filters are pairwise
// disjoint, so there is nothing to attribute and every subscription goes out
// exactly as it did before the router existed — No Local, no identifier.
//
// The reason the mode matters rather than merely being observable:
// attribution is MQTT 5.0 only, `north.mqtt.protocol_version: "3.1.1"` is an
// operator-reachable config key, and the router never retries an attributed
// route unattributed. So on an overlapping filter set that one key stops
// being a dialect choice and becomes a refused Start — the whole command
// plane down, at boot, with the state plane unaffected and looking healthy.
// Disjoint filters are what keep the two independent.
func (c *CommandSubscriber) attributed() bool {
	r := c.currentRouter()
	if r == nil {
		return false
	}
	return r.Attributed()
}

// subscriptionID reports the MQTT 5.0 Subscription Identifier the
// subscription for filter carries, or 0 when it carries none — which is
// every route of this plane, because it registers no overlapping pair. See
// [CommandSubscriber.attributed].
func (c *CommandSubscriber) subscriptionID(filter string) uint32 {
	r := c.currentRouter()
	if r == nil {
		return 0
	}
	return r.SubscriptionID(filter)
}

// checkDisjoint reports whether any of topics would be delivered back into
// this daemon's own command handlers.
//
// It is the shared module's own predicate, which makes it the one worth
// running: the broker has no notion of "my own message" and fans every
// publish out to every matching subscription, including this connection's,
// with the retain flag clear because live routing is not a retained replay.
// A state topic that a command filter matches is therefore the daemon
// issuing itself a command every time it reports state — which this daemon
// shipped once, mirroring a program's state onto that program's own trigger
// topic, so the echo ran the program on the CCU on every boot, on every hub
// republish and once per freshly discovered program, with nothing in the
// logs.
//
// MQTT 5.0's No Local closes that class for this process (the shared
// transport sets it, and the router asks for it), but only on a v5 link:
// `north.mqtt.protocol_version: "3.1.1"` silently ignores the option, and no
// option says anything about a second process publishing the same tree. So
// this stays load-bearing. Returns nil before [CommandSubscriber.Start],
// when no route can claim anything.
func (c *CommandSubscriber) checkDisjoint(topics ...string) error {
	r := c.currentRouter()
	if r == nil {
		return nil
	}
	return r.CheckDisjoint(topics...)
}

// WithQoS overrides the QoS level every Subscribe call in [Start]
// registers at (default QoS1). Callers typically pass the bridge's own
// [BridgeConfig.QoS.Commands] so the inbound command subscriptions and
// the bridge's own QoS policy do not drift apart. Returns the receiver
// for call-site chaining.
func (c *CommandSubscriber) WithQoS(qos QoS) *CommandSubscriber {
	c.qos = qos
	return c
}

// WithCentralNames attaches the central-name source used to resolve the
// escaped `<central>` topic segment back to the configured name.
// Returns the receiver for call-site chaining.
func (c *CommandSubscriber) WithCentralNames(l CentralNameLister) *CommandSubscriber {
	c.centrals = l
	return c
}

// resolveCentral maps the `<central>` topic segment onto a configured
// central name. The segment is [naming.TopicSafe]d by every publisher,
// so a central named `Wohn Zimmer` arrives as `Wohn_Zimmer` and would
// miss every exact-key lookup downstream.
//
// Resolution order: the segment itself when it names a configured
// central (the common case — most names need no escaping), otherwise
// the unique central whose escaped name equals the segment. An
// ambiguous segment (two centrals that escape to the same string) is
// refused with a warning rather than routed to an arbitrary one of
// them; the caller drops the command. An unknown segment is passed
// through unchanged so the sink reports the unknown central as it
// always has.
func (c *CommandSubscriber) resolveCentral(topic, segment string) (string, bool) {
	if c.centrals == nil || segment == "" {
		return segment, true
	}
	var (
		match   string
		matches int
	)
	for _, name := range c.centrals.Names() {
		if name == segment {
			return name, true
		}
		if naming.TopicSafe(name) == segment {
			match = name
			matches++
		}
	}
	switch matches {
	case 0:
		return segment, true
	case 1:
		return match, true
	default:
		c.logger.Warn("mqtt.command.ambiguous_central",
			slog.String("topic", topic),
			slog.String("segment", segment),
			slog.String("detail", "several configured centrals escape to this topic segment; rename one of them"))
		return "", false
	}
}

// WithCollector attaches the metrics collector so the subscriber can
// increment the ReceivedCommands counter on every dispatched message.
// Returns the receiver for call-site chaining.
func (c *CommandSubscriber) WithCollector(col *metrics.MqttCollector) *CommandSubscriber {
	c.collector = col
	return c
}

// WithCDPSink attaches the Custom-DP invocation sink. Returns the
// receiver for call-site chaining.
func (c *CommandSubscriber) WithCDPSink(s CDPInvocationSink) *CommandSubscriber {
	c.cdpSink = s
	return c
}

// WithWeekProfileSink attaches the week-profile active-profile sink.
// Returns the receiver for call-site chaining.
func (c *CommandSubscriber) WithWeekProfileSink(s WeekProfileSink) *CommandSubscriber {
	c.wpSink = s
	return c
}

// WithCombinedDPSink attaches the combined-DP sink. Returns the receiver
// for call-site chaining.
func (c *CommandSubscriber) WithCombinedDPSink(s CombinedDPSink) *CommandSubscriber {
	c.cmbSink = s
	return c
}

// WithScheduleSwitchSink attaches the schedule-switch sink. Returns the
// receiver for call-site chaining.
func (c *CommandSubscriber) WithScheduleSwitchSink(s ScheduleSwitchSink) *CommandSubscriber {
	c.schedSink = s
	return c
}

// WithInstallModeSink attaches the install-mode sink. Returns the
// receiver for call-site chaining.
func (c *CommandSubscriber) WithInstallModeSink(s InstallModeSink) *CommandSubscriber {
	c.imSink = s
	return c
}

// WithAlarmSink attaches the daemon-level alarm sink. Returns the
// receiver for call-site chaining.
func (c *CommandSubscriber) WithAlarmSink(s AlarmSink) *CommandSubscriber {
	c.alarmSink = s
	return c
}

// WithAddonUpdateSink attaches the daemon-level add-on self-update
// sink. Returns the receiver for call-site chaining.
func (c *CommandSubscriber) WithAddonUpdateSink(s AddonUpdateSink) *CommandSubscriber {
	c.addonSink = s
	return c
}

// WithLifecycleContext sets the daemon-lifetime context that command handlers
// derive each per-command context from. Wiring this — rather than reusing
// Start's ctx, which on a hot-reload broker swap is request-scoped and dies
// when the reload returns — ensures in-flight CCU writes cancel on daemon
// shutdown yet survive a broker swap. A nil ctx is ignored. Returns the
// receiver for call-site chaining.
func (c *CommandSubscriber) WithLifecycleContext(ctx context.Context) *CommandSubscriber {
	if ctx != nil {
		c.lifecycleCtx = ctx
	}
	return c
}

// The literal segments the two data-point handlers dispatch on instead of
// treating as a CCU parameter or bucket name.
//
// They are constants shared by the dispatch in
// [CommandSubscriber.handleDataPoint] and by the validation in each
// receiving handler, because the two have to agree exactly: the dispatch
// decides which handler a topic reaches and the handler re-checks the same
// segment before indexing around it.
//
// Why these shapes have no subscription of their own: the data-point plane
// needs two wildcard catch-alls — `<base>/+/+/+/+/+/set` (legacy,
// bucket-less) and `<base>/+/+/+/+/+/+/set` (bucket-aware) — and every
// command shape of the same length is therefore matched by one of them. MQTT
// has no exclusion wildcard, so a narrower sibling filter cannot subtract
// itself from a catch-all: it only adds a second matching subscription. A
// broker then sends one copy per matching subscription (MQTT 3.1.1 §4.7.3 /
// 5.0 §3.3.4) and the client re-matches every copy against its whole local
// filter list, so the two fan-outs multiply and one operator action runs the
// handler N times. Dispatching from inside the catch-all is the only shape
// that resolves the overlap rather than guarding its symptoms.
const (
	// segWeekProfile owns `<…>/<channel>/week_profile/set`, six segments
	// below the base — the same length as the legacy bucket-less
	// data-point shape. Declared by
	// [DefaultDiscoveryBuilder.BuildWeekProfileDiscovery], handled by
	// [CommandSubscriber.handleWeekProfile].
	segWeekProfile = "week_profile"
	// segCombined owns `<…>/<channel>/combined/<kind>/set`, seven
	// segments below the base, sitting where the bucket-aware shape
	// carries its bucket. Declared by
	// [DefaultDiscoveryBuilder.BuildCombinedTimerDiscovery], handled by
	// [CommandSubscriber.handleCombinedDP].
	segCombined = "combined"
	// segSchedule owns `<…>/<channel>/schedule/<key>/set`, likewise at
	// the bucket position. Declared by
	// [DefaultDiscoveryBuilder.BuildScheduleSwitchDiscovery], handled by
	// [CommandSubscriber.handleScheduleSwitch].
	segSchedule = "schedule"
)

// inboundOnlyPublisher is the publish half [hagomqtt.Split] insists on for a
// plane that only reads.
//
// [hapublisher.CommandRouter] never publishes — it subscribes, routes and
// unsubscribes — but the shared transport is one interface covering both
// directions, and the honest way to supply a half this plane does not have
// is a value that says so. The alternative, handing it the bridge's
// publisher, would make a future publish from the command plane work by
// accident and land on the wire through a path nothing here reviews.
type inboundOnlyPublisher struct{}

// errCommandPlaneIsInboundOnly is what a publish through the command plane's
// transport reports. Reaching it is a programming error, not an operational
// one.
var errCommandPlaneIsInboundOnly = errors.New("mqtt/command: the command plane is inbound only and publishes nothing")

// Publish implements [Publisher].
func (inboundOnlyPublisher) Publish(context.Context, string, []byte, QoS, bool, ...PublishOption) error {
	return errCommandPlaneIsInboundOnly
}

// commandRoute is one filter and the handler the router hands its messages
// to.
type commandRoute struct {
	filter  string
	handler hapublisher.CommandHandler
}

// routes is the exact, ordered command-filter set this plane answers on.
//
// Order is registration order, and it is load-bearing twice over: the router
// subscribes in it, so a broker that refuses one filter rolls back the ones
// before it in reverse, and [hapublisher.CommandRouter.Handle] reports an
// ambiguous pair naming the earlier filter first. It is pinned by
// TestCommandFilterSetIsPinned.
//
// The set is pairwise disjoint, and that is a property of the set rather than
// a coincidence of it. The data-point plane needs two wildcard catch-alls at
// six and seven segments below the base, so any sibling filter of the same
// length overlaps one of them; MQTT has no exclusion wildcard, so a narrower
// filter cannot subtract itself from a catch-all — it only adds a second
// matching subscription, and the broker's per-subscription copy then
// multiplies with the client's local re-match. The week-profile, combined-DP
// and schedule-switch shapes therefore have no filter of their own;
// [CommandSubscriber.handleDataPoint] dispatches them off the segment the
// filter used to carry. See the segWeekProfile / segCombined / segSchedule
// block, and TestCommandFiltersArePairwiseDisjoint.
func (c *CommandSubscriber) routes(base string) []commandRoute {
	return []commandRoute{
		// Bucket-aware data-point command topology (the canonical shape
		// the discovery builder advertises since the Option-B migration):
		//   <base>/<central>/<interface>/<addr>/<channel>/<bucket>/<param>/set
		// where `<bucket>` is `values` / `master` / `calculated`. The
		// switch / lock / select / number entities all publish here, so
		// the subscriber MUST register the 8-segment shape — without it
		// HA's `payload_on=true` to a Custom-DP switch arrives at the
		// broker but never reaches the daemon.
		//
		// Also carries the combined-DP and schedule-switch shapes, which
		// put a literal where the bucket sits.
		{base + "/+/+/+/+/+/+/set", c.handleDataPoint},
		// Legacy 7-segment shape (no bucket infix) — still emitted by
		// some hand-built tools and by automations written against the
		// pre-bucket topology. Keep it active so they don't break.
		// Also carries the week-profile shape, which is the same length.
		{base + "/+/+/+/+/+/set", c.handleDataPoint},
		// Canonical (ADR 0011): {base}/{central}/hub/sysvars/{name}/set.
		{base + "/+/hub/sysvars/+/set", c.handleSysvar},
		// Canonical (ADR 0011): {base}/{central}/hub/programs/{id}/set.
		// Activation is a separate control from execution — a deactivated
		// program refuses to run — so it has its own topic (see
		// hub.Program.MQTTRoles).
		{base + "/+/hub/programs/+/set", c.handleProgramEnable},
		{base + "/+/hub/programs/+/trigger", c.handleProgram},
		// Per-interface install-mode activation button:
		// {base}/{central}/hub/install_mode/{iface}/set — HA publishes the
		// press token; the handler activates pairing on the named interface.
		{base + "/+/hub/install_mode/+/set", c.handleInstallMode},
		// {base}/{central}/devices/{device}/cdps/{name}/{operation}/invoke
		{base + "/+/devices/+/cdps/+/+/invoke", c.handleCDPInvoke},
		// Canonical ADR-0011 per-service-method form:
		// {base}/{central}/{interface}/{address}/{channel}/custom/{kind}/set/{method}
		{base + "/+/+/+/+/custom/+/set/+", c.handleServiceMethod},
		// {base}/alarm/{zone}/set — the daemon-level alarm arm/disarm/silence
		// plane. Zones are daemon-level, so the topic carries no <central>
		// segment; the reserved <zone> "master" routes to the aggregate verbs.
		{base + "/alarm/+/set", c.handleAlarmCommand},
		// {base}/system/addon_update/set — the daemon-level CCU add-on
		// self-update INSTALL command (ADR 0057). Daemon-level like the
		// alarm plane above, so the topic carries no <central> segment.
		{base + "/system/addon_update/set", c.handleAddonUpdateCommand},
	}
}

// Start registers every command route with a [hapublisher.CommandRouter] and
// puts the whole set on the wire.
//
// Three properties come from the router rather than from this file, and each
// replaces something this plane used to do by hand:
//
//   - A failure on any one subscription aborts the start AND unsubscribes
//     the routes already registered. Coming up with a partial filter set
//     accepts some commands and silently ignores the rest, which from the
//     outside is a broken CCU rather than a broken subscribe. This
//     subscriber used to abort and leave the partial set live; the rollback
//     is the one place the shared module deliberately did not copy it.
//   - Handlers run on a worker pool, never on the transport's read loop.
//     See [hapublisher.CommandHandler] for the contract and its three costs.
//   - Retained messages never reach a handler
//     ([hapublisher.CommandConfig.DeliverRetained] is off), so a stale
//     `mosquitto_pub -r` on a `/set` topic is dropped once by the router
//     instead of by the identical guard each handler used to open with.
//
// The filter set must stay pairwise disjoint. [hapublisher.CommandRouter.Handle]
// would otherwise either refuse the pair outright or — on this transport,
// which can attribute a delivery through an MQTT 5.0 Subscription Identifier
// — accept it and switch the whole plane into attributed mode. That mode is
// v5-only and is never retried unattributed, so it turns
// `north.mqtt.protocol_version: "3.1.1"` from a dialect choice into a boot
// failure of the entire command plane. Start therefore refuses to subscribe
// anything at all in attributed mode, which makes a filter edit that
// reintroduces an overlap fail here rather than on a downgraded broker.
func (c *CommandSubscriber) Start(ctx context.Context) error {
	if c.sub == nil {
		return errors.New("mqtt/command: no subscriber")
	}
	// A second Start would build a second router over the same client, and
	// both would stay live: the first one's subscriptions keep their
	// handlers, its worker pool keeps its goroutines, and only the handle
	// this subscriber holds is replaced — so nothing could ever stop it. The
	// composition root builds one subscriber per stack generation and starts
	// it once; a second call is a wiring mistake and is named as one.
	if c.currentRouter() != nil {
		return errors.New("mqtt/command: already started; build a new CommandSubscriber per stack generation")
	}
	base := c.topics.Base
	// The base is free-form operator config (`north.mqtt.topic_base`), and a
	// wildcard in it is not merely a bad name: every route's parsed
	// [hapublisher.Command.Wildcards] is positional, so a `+` contributed by
	// the base shifts every handler's reading of central, interface, address
	// and parameter by one. That is a CCU write to the wrong parameter, not a
	// dropped message, so it is refused before anything is registered. There
	// is no config-side validation for this today.
	if strings.ContainsAny(base, "+#") {
		return fmt.Errorf("mqtt/command: topic base %q carries an MQTT wildcard (`+` or `#`): "+
			"every command route would capture it as a wildcard level and every handler would read "+
			"the topic one segment out of step", base)
	}
	router := hapublisher.NewCommandRouter(
		hagomqtt.Split(inboundOnlyPublisher{}, c.sub),
		hapublisher.CommandConfig{
			QoS: runtimeQoS(c.qos),
			// The daemon-lifetime context, not Start's: on a hot-reload
			// broker swap Start's ctx is request-scoped and dies when the
			// reload returns, which cancelled in-flight CCU writes on a
			// plane that was otherwise perfectly alive. This is the field
			// [CommandSubscriber.WithLifecycleContext] exists for, and the
			// library's own doc comment names the same defect.
			Lifecycle:    c.lifecycleCtx,
			OnUnroutable: c.onUnroutable,
			Logger:       c.logger,
		},
	)
	for _, rt := range c.routes(base) {
		if err := router.Handle(rt.filter, rt.handler); err != nil {
			return fmt.Errorf("mqtt/command: route %s: %w", rt.filter, err)
		}
	}
	if router.Attributed() {
		return fmt.Errorf("mqtt/command: the command filter set is no longer pairwise disjoint, so the "+
			"router would subscribe with MQTT 5.0 subscription identifiers (%d filters registered): "+
			"attribution is v5-only and is never retried unattributed, so this would make "+
			"north.mqtt.protocol_version: \"3.1.1\" a boot failure of the whole command plane; "+
			"register the general shape only and dispatch the narrow one from inside its handler",
			len(router.Filters()))
	}
	c.routerMu.Lock()
	c.router = router
	c.routerMu.Unlock()
	if err := router.Start(ctx); err != nil {
		// One increment, as before: Start aborts on the first refused
		// subscribe, so exactly one subscribe failed. The counter is
		// unlabeled because every filter answers for every configured
		// central at once and a broker-refused subscribe has no single
		// central to blame.
		c.incSubscribeFailures()
		return fmt.Errorf("mqtt/command: %w", err)
	}
	return nil
}

// onUnroutable is [hapublisher.CommandConfig.OnUnroutable]: a message was
// delivered on one of this plane's subscriptions and no route claims it.
//
// It keeps the event name the per-handler shape checks used to emit, because
// that is what an operator greps for. It cannot fire for a topic a route
// matched — the route IS the shape check now, so the mismatch each handler
// used to re-derive from segment positions is unreachable — which leaves the
// case it is really for: traffic on a shared broker that is none of this
// daemon's business, and a route removed while its subscription lingers.
// Both are diagnostics, never failures.
func (c *CommandSubscriber) onUnroutable(topic string, _ []byte) {
	c.logger.Warn("mqtt.command.unknown_topic", slog.String("topic", topic))
}

// routeWildcards returns the levels the route's `+` positions matched, when
// the command carries exactly want of them.
//
// Every handler below reads its topic positionally out of that slice, so a
// route bound to a handler expecting a different shape would index past its
// end — and a handler panic is not survivable here: the router runs handlers
// on a worker pool with no recover of its own, so an out-of-range read takes
// the daemon down instead of dropping one command. [CommandSubscriber.routes]
// is what makes the mismatch unreachable, and the delivery tests in
// command_subscriber_topic_base_test.go drive every row of it under two
// bases. This is what happens if both are ever wrong at once, and a logged
// drop is the better of the two failure modes.
func (c *CommandSubscriber) routeWildcards(cmd hapublisher.Command, want int) ([]string, bool) {
	if len(cmd.Wildcards) != want {
		c.logger.Warn("mqtt.command.route_arity",
			slog.String("topic", cmd.Topic),
			slog.String("filter", cmd.Filter),
			slog.Int("wildcards", len(cmd.Wildcards)),
			slog.Int("want", want),
			slog.String("detail", "a command route is bound to a handler that reads a different shape"))
		return nil, false
	}
	return cmd.Wildcards, true
}

// incReceivedCommands increments the received_commands counter for
// centralName when a collector is wired. Pass "" only for a genuinely
// daemon-level command topic (no <central> segment) — see the call
// sites in handleAlarmCommand and handleAddonUpdateCommand.
func (c *CommandSubscriber) incReceivedCommands(centralName string) {
	if c.collector != nil {
		c.collector.ReceivedCommands(centralName).Inc()
	}
}

// incSubscribeFailures increments the subscribe_failures counter when a
// collector is wired. Unlabeled: every registered route is one wildcard
// filter answering for every configured central at once, so a
// broker-rejected subscribe has no single central to blame.
func (c *CommandSubscriber) incSubscribeFailures() {
	if c.collector != nil {
		c.collector.SubscribeFailures.Inc()
	}
}

// handleScheduleSwitch dispatches a payload from
// `<base>/<central>/<iface>/<addr>/<chan>/schedule/<key>/set` into the
// schedule-switch sink. Payload is "true" or "false" (HA's standard
// switch payload).
//
// Reached only from [CommandSubscriber.handleDataPoint]'s bucket branch,
// which has already established that the shape carries [segSchedule] where
// the bucket sits — the schedule shape has no filter of its own, because one
// would overlap the bucket-aware catch-all. So it does not re-check that
// literal: a guard on a condition its only caller has just proved cannot
// fail is a guard that tests nothing. The arity check that remains is the
// panic bound [CommandSubscriber.routeWildcards] documents, which is a
// different question.
func (c *CommandSubscriber) handleScheduleSwitch(ctx context.Context, cmd hapublisher.Command) {
	topic := cmd.Topic
	w, ok := c.routeWildcards(cmd, 6)
	if !ok {
		return
	}
	centralName, ok := c.resolveCentral(topic, w[0])
	if !ok {
		return
	}
	iface, deviceAddr, key := w[1], w[2], w[5]
	channel, err := strconv.Atoi(w[3])
	if err != nil {
		c.logger.Warn("mqtt.command.schedule.bad_channel", slog.String("topic", topic))
		return
	}
	raw := strings.ToLower(strings.TrimSpace(string(cmd.Payload)))
	var enabled bool
	switch raw {
	case "true", "on", "1":
		enabled = true
	case "false", "off", "0":
		enabled = false
	default:
		c.logger.Warn("mqtt.command.schedule.bad_payload",
			slog.String("topic", topic),
			slog.String("payload", raw))
		return
	}
	if c.schedSink == nil {
		c.logger.Debug("mqtt.command.schedule.no_sink",
			slog.String("topic", topic),
			slog.String("detail", "ScheduleSwitchSink not wired"))
		return
	}
	if err := c.schedSink.SetScheduleSwitch(ctx, centralName, iface, deviceAddr, channel, key, enabled, hmenum.CommandPriorityHigh); err != nil {
		c.logger.Warn("mqtt.command.schedule.set",
			slog.String("topic", topic),
			slog.String("key", key),
			slog.Bool("enabled", enabled),
			slog.String("err", err.Error()))
	}
}

// handleWeekProfile dispatches a payload from
// `<base>/<central>/<iface>/<addr>/<chan>/week_profile/set` to the
// week-profile sink. The payload is the profile key ("P1".."PN") as
// a plain string (no quotes / no JSON envelope) — the same shape HA
// publishes for `select` entities.
//
// Reached only from [CommandSubscriber.handleDataPoint]'s six-segment
// branch, which has already established the [segWeekProfile] literal — see
// the note on [CommandSubscriber.handleScheduleSwitch].
func (c *CommandSubscriber) handleWeekProfile(ctx context.Context, cmd hapublisher.Command) {
	topic := cmd.Topic
	w, ok := c.routeWildcards(cmd, 5)
	if !ok {
		return
	}
	centralName, ok := c.resolveCentral(topic, w[0])
	if !ok {
		return
	}
	iface, deviceAddr := w[1], w[2]
	channel, err := strconv.Atoi(w[3])
	if err != nil {
		c.logger.Warn("mqtt.command.wp.bad_channel", slog.String("topic", topic))
		return
	}
	profile := strings.TrimSpace(string(cmd.Payload))
	if profile == "" {
		c.logger.Warn("mqtt.command.wp.empty_payload", slog.String("topic", topic))
		return
	}
	if c.wpSink == nil {
		c.logger.Debug("mqtt.command.wp.no_sink",
			slog.String("topic", topic),
			slog.String("detail", "WeekProfileSink not wired; ignoring active-profile command"))
		return
	}
	if err := c.wpSink.SetActiveProfile(ctx, centralName, iface, deviceAddr, channel, profile, hmenum.CommandPriorityHigh); err != nil {
		c.logger.Warn("mqtt.command.wp.set_active_profile",
			slog.String("topic", topic),
			slog.String("profile", profile),
			slog.String("err", err.Error()))
	}
}

// handleCombinedDP dispatches a payload from
// `<base>/<central>/<iface>/<addr>/<chan>/combined/<kind>/set` into the
// combined-DP sink.
//
// The payload is forwarded verbatim. HA publishes a bare scalar for both
// shapes this carries today — "30" from a number entity, "OPEN" from a
// select — and which of them a kind expects is the data point's to know,
// not the transport's.
//
// Reached only from [CommandSubscriber.handleDataPoint]'s bucket branch,
// which has already established the [segCombined] literal — see the note on
// [CommandSubscriber.handleScheduleSwitch].
func (c *CommandSubscriber) handleCombinedDP(ctx context.Context, cmd hapublisher.Command) {
	topic := cmd.Topic
	w, ok := c.routeWildcards(cmd, 6)
	if !ok {
		return
	}
	centralName, ok := c.resolveCentral(topic, w[0])
	if !ok {
		return
	}
	iface, deviceAddr, kind := w[1], w[2], w[5]
	channel, err := strconv.Atoi(w[3])
	if err != nil {
		c.logger.Warn("mqtt.command.combined.bad_channel", slog.String("topic", topic))
		return
	}
	raw := strings.TrimSpace(string(cmd.Payload))
	if raw == "" {
		c.logger.Warn("mqtt.command.combined.bad_payload",
			slog.String("topic", topic),
			slog.String("detail", "empty payload"))
		return
	}
	if c.cmbSink == nil {
		c.logger.Debug("mqtt.command.combined.no_sink",
			slog.String("topic", topic),
			slog.String("detail", "CombinedDPSink not wired; ignoring combined-DP write"))
		return
	}
	if err := c.cmbSink.SetCombinedValue(ctx, centralName, iface, deviceAddr, channel, kind, raw, hmenum.CommandPriorityHigh); err != nil {
		c.logger.Warn("mqtt.command.combined.set",
			slog.String("topic", topic),
			slog.String("kind", kind),
			slog.String("value", raw),
			slog.String("err", err.Error()))
	}
}

// handleServiceMethod dispatches a payload from the canonical
// ADR-0011 per-service-method topic
// `<base>/<central>/<iface>/<addr>/<chan>/custom/<kind>/set/<method>`
// into `Source.Invoke`.
//
// The MQTT payload may be a JSON object (forwarded verbatim as
// `params`) or a scalar (wrapped under the canonical argument name
// for `method` — see `service_method_routing.go`).
func (c *CommandSubscriber) handleServiceMethod(ctx context.Context, cmd hapublisher.Command) {
	topic := cmd.Topic
	w, ok := c.routeWildcards(cmd, 6)
	if !ok {
		return
	}
	centralName, ok := c.resolveCentral(topic, w[0])
	if !ok {
		return
	}
	// The `custom` and `set` literals and the segment count are the route's,
	// not this handler's: the filter is
	// `<base>/+/+/+/+/custom/+/set/+`, so a delivered message has them by
	// construction. Wildcard 4 is the `<kind>` segment, which this shape
	// carries for the discovery payload's benefit and the dispatch ignores.
	iface, deviceAddr, method := w[1], w[2], w[5]
	channel, err := strconv.Atoi(w[3])
	if err != nil {
		c.logger.Warn("mqtt.command.svc.bad_channel", slog.String("topic", topic))
		return
	}
	if c.cdpSink == nil {
		c.logger.Warn("mqtt.command.svc.no_sink",
			slog.String("topic", topic),
			slog.String("detail", "CDPInvocationSink not wired; ignoring service-method invoke"))
		return
	}
	params, err := scalarPayloadToParams(method, cmd.Payload, payload.GlobalScalarArgKey)
	if err != nil {
		c.logger.Warn("mqtt.command.svc.bad_payload",
			slog.String("topic", topic),
			slog.String("err", err.Error()))
		return
	}
	if err := c.cdpSink.InvokeChannelService(ctx, centralName, iface, deviceAddr, channel, method, params, hmenum.CommandPriorityHigh); err != nil {
		c.logger.Warn("mqtt.command.svc.invoke",
			slog.String("topic", topic),
			slog.String("method", method),
			slog.String("err", err.Error()))
	}
}

// handleDataPoint owns both data-point command shapes and, because they are
// wildcard catch-alls nothing can be carved out of, the three narrow shapes
// of the same lengths.
//
// Two accepted data-point shapes — both end with `/set`, and both are read
// off [hapublisher.Command.Wildcards], the levels the route's `+` positions
// matched, rather than off a split of the topic:
//
//  1. Bucket-aware (canonical, emitted by the discovery builder):
//     <central>/<iface>/<addr>/<channel>/<bucket>/<param>/set
//     — six wildcards; `<bucket>` is `values`/`master`/`calculated`.
//  2. Legacy bucket-less shape, still produced by hand-built tools and by
//     automations written against the pre-bucket topology:
//     <central>/<iface>/<addr>/<channel>/<param>/set
//     — five wildcards.
//
// The bucket-less form always routes to VALUES. In the bucket-aware form
// `values` routes to SetValue, `master` routes to SetMasterValue, and
// `calculated` is read-only and dropped with a debug log.
//
// Three shapes of those two lengths are not data-point writes at all and are
// dispatched from here rather than from a filter of their own, because a
// filter of their own would overlap one of these two catch-alls and MQTT
// cannot express the exclusion: `week_profile` at five wildcards, `combined`
// and `schedule` at the bucket position of the six-wildcard shape.
//
// Counting wildcards rather than topic segments is what makes the reading
// independent of the topic base. The base is free-form operator config and
// may carry levels of its own ("home/loom"); this handler used to count
// absolute segments, which shifted every index by the base's depth and
// dropped every inbound command on such an installation while the state
// plane kept publishing normally — an outage that reads exactly like a
// broker or CCU fault. A wildcard list cannot have that bug, and
// [CommandSubscriber.Start] refuses a base that would contribute a wildcard
// level of its own.
func (c *CommandSubscriber) handleDataPoint(ctx context.Context, cmd hapublisher.Command) {
	topic := cmd.Topic
	w := cmd.Wildcards
	// Deliberately not routeWildcards: this handler owns two routes of
	// different arity and discriminates on the count itself, so the `default`
	// arm below IS the guard.
	var parameter string
	isMaster := false
	switch len(w) {
	case 5:
		// This route is the only five-wildcard subscription on the plane,
		// so a shape of that length which is not a data-point write is
		// dispatched from here rather than owned by a sibling filter that
		// would overlap it — see the segWeekProfile block.
		if w[4] == segWeekProfile {
			c.handleWeekProfile(ctx, cmd)
			return
		}
		parameter = w[4]
	case 6:
		switch bucket := w[4]; bucket {
		case "values":
			// Default VALUES write — no special flag needed.
		case "master":
			isMaster = true
		case segCombined:
			// Not a bucket: the combined-DP shape carries its own
			// literal where the bucket sits, and is dispatched from
			// here for the reason the segCombined block gives.
			c.handleCombinedDP(ctx, cmd)
			return
		case segSchedule:
			c.handleScheduleSwitch(ctx, cmd)
			return
		default:
			// `calculated` and any unknown bucket are read-only; drop
			// with a debug breadcrumb so operators can diagnose
			// mis-directed writes.
			c.logger.Debug("mqtt.command.unsupported_bucket",
				slog.String("topic", topic),
				slog.String("bucket", bucket))
			return
		}
		parameter = w[5]
	default:
		// Unreachable through the two registered routes, which capture
		// exactly five or six wildcards. It is kept as the failure mode for
		// a third route bound to this handler by mistake: a silent wrong
		// read of w[4] would be a CCU write to a parameter named after
		// somebody else's literal.
		c.logger.Warn("mqtt.command.unknown_topic", slog.String("topic", topic))
		return
	}
	centralName, ok := c.resolveCentral(topic, w[0])
	if !ok {
		return
	}
	channel, err := strconv.Atoi(w[3])
	if err != nil {
		c.logger.Warn("mqtt.command.bad_channel", slog.String("topic", topic))
		return
	}
	iface := w[1]
	value := parseCommandPayload(cmd.Payload)
	channelAddress := fmt.Sprintf("%s:%d", w[2], channel)
	c.incReceivedCommands(centralName)
	if isMaster {
		if err := c.sink.SetMasterValue(ctx, centralName, iface, channelAddress,
			hmenum.Parameter(parameter), value, hmenum.CommandPriorityHigh); err != nil {
			c.logger.Warn("mqtt.command.setmasterparam",
				slog.String("topic", topic),
				slog.String("err", err.Error()))
		}
		return
	}
	if err := c.sink.SetValue(ctx, centralName, iface, channelAddress,
		hmenum.Parameter(parameter), value, hmenum.CommandPriorityHigh); err != nil {
		c.logger.Warn("mqtt.command.setvalue",
			slog.String("topic", topic),
			slog.String("err", err.Error()))
	}
}

// handleSysvar writes a system variable from the canonical ADR-0011 topic
// `<base>/<central>/hub/sysvars/<name>/set`. The `hub` and `sysvars`
// literals are the route's, so the wildcards are the central and the name.
func (c *CommandSubscriber) handleSysvar(ctx context.Context, cmd hapublisher.Command) {
	w, ok := c.routeWildcards(cmd, 2)
	if !ok {
		return
	}
	centralName, ok := c.resolveCentral(cmd.Topic, w[0])
	if !ok {
		return
	}
	value := parseCommandPayload(cmd.Payload)
	c.incReceivedCommands(centralName)
	if err := c.sink.SetSysvar(ctx, centralName, w[1], value); err != nil {
		c.logger.Warn("mqtt.command.setsysvar",
			slog.String("topic", cmd.Topic), slog.String("err", err.Error()))
	}
}

// handleProgram executes a CCU program from the canonical ADR-0011 topic
// `<base>/<central>/hub/programs/<id>/trigger`.
func (c *CommandSubscriber) handleProgram(ctx context.Context, cmd hapublisher.Command) {
	topic := cmd.Topic
	// An empty payload is not a command. It is what the retain-cleanup
	// pass publishes to evict a parked retained message from the trigger
	// topic — the broker forwards that eviction to this very subscription as
	// a live (non-retained) message, so the router's retained drop does NOT
	// catch it, and executing a CCU program because a topic was cleaned
	// would repeat the state-mirror defect this guard exists to keep out.
	if strings.TrimSpace(string(cmd.Payload)) == "" {
		c.logger.Debug("mqtt.command.program.empty_drop", slog.String("topic", topic))
		return
	}
	w, ok := c.routeWildcards(cmd, 2)
	if !ok {
		return
	}
	centralName, ok := c.resolveCentral(topic, w[0])
	if !ok {
		return
	}
	c.incReceivedCommands(centralName)
	// Stamp the surface so the program-execute audit/log subscriber
	// can attribute the run to the MQTT command plane.
	ctx = hmreqctx.WithOperation(ctx, "mqtt:program-trigger")
	if err := c.sink.TriggerProgram(ctx, centralName, w[1]); err != nil {
		c.logger.Warn("mqtt.command.program",
			slog.String("topic", topic), slog.String("err", err.Error()))
	}
}

// handleProgramEnable toggles a program's CCU-side activity flag from
// `<base>/<central>/hub/programs/<id>/set`. While the flag is off the CCU
// ignores the program's triggers and refuses a manual run, so this is the
// control that decides whether the paired execute button does anything.
func (c *CommandSubscriber) handleProgramEnable(ctx context.Context, cmd hapublisher.Command) {
	topic := cmd.Topic
	w, ok := c.routeWildcards(cmd, 2)
	if !ok {
		return
	}
	centralName, ok := c.resolveCentral(topic, w[0])
	if !ok {
		return
	}
	enabled, ok := parseBoolPayload(cmd.Payload)
	if !ok {
		c.logger.Warn("mqtt.command.program_enable.bad_payload",
			slog.String("topic", topic), slog.String("payload", string(cmd.Payload)))
		return
	}
	c.incReceivedCommands(centralName)
	if err := c.sink.SetProgramEnabled(ctx, centralName, w[1], enabled); err != nil {
		c.logger.Warn("mqtt.command.program_enable",
			slog.String("topic", topic), slog.String("err", err.Error()))
	}
}

// parseBoolPayload accepts the on/off spellings HA and hand-written
// clients publish. An unrecognised payload is rejected rather than
// guessed, so a typo does not silently deactivate a program.
func parseBoolPayload(body []byte) (value, ok bool) {
	switch strings.ToLower(strings.TrimSpace(string(body))) {
	case "true", "on", "1", "yes":
		return true, true
	case "false", "off", "0", "no":
		return false, true
	}
	return false, false
}

// handleInstallMode activates pairing/install mode on one interface from
// the per-interface button command topic
// `<base>/<central>/hub/install_mode/<iface>/set`. HA's button entity
// publishes the press token ("PRESS"); the sink applies its own default
// pairing duration. A numeric payload is honoured as the duration in
// seconds for tools that publish one directly.
func (c *CommandSubscriber) handleInstallMode(ctx context.Context, cmd hapublisher.Command) {
	// <base>/<central>/hub/install_mode/<iface>/set
	topic := cmd.Topic
	w, ok := c.routeWildcards(cmd, 2)
	if !ok {
		return
	}
	centralName, ok := c.resolveCentral(topic, w[0])
	if !ok {
		return
	}
	iface := w[1]
	if c.imSink == nil {
		c.logger.Debug("mqtt.command.install_mode.no_sink",
			slog.String("topic", topic),
			slog.String("detail", "InstallModeSink not wired; ignoring install-mode press"))
		return
	}
	// "PRESS" (HA button) and empty payloads request the default
	// duration (seconds=0 → sink default); a bare integer overrides it.
	seconds := 0
	if raw := strings.TrimSpace(string(cmd.Payload)); raw != "" && !strings.EqualFold(raw, "PRESS") {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			seconds = n
		}
	}
	c.incReceivedCommands(centralName)
	if err := c.imSink.ActivateInstallMode(ctx, centralName, iface, seconds); err != nil {
		c.logger.Warn("mqtt.command.install_mode.activate",
			slog.String("topic", topic),
			slog.String("interface", iface),
			slog.String("err", err.Error()))
	}
}

// alarmCommandPayload is the JSON envelope accepted on the alarm command
// topic (`{"action":"ARM_AWAY","code":"1234"}`). The bare-string HA
// payload is accepted too; a bare string carries no code.
type alarmCommandPayload struct {
	Action string `json:"action"`
	Code   string `json:"code"`
}

// handleAlarmCommand routes a payload from `<base>/alarm/<zone>/set` into
// the alarm sink. The <zone> segment is an zone ID or the reserved
// "master" token; the payload is either a bare HA command string
// (ARM_HOME / ARM_AWAY / … / DISARM, plus the SILENCE extension) or the
// JSON {"action":…,"code":…} envelope. Unknown payloads are logged and
// dropped, matching the other command handlers.
func (c *CommandSubscriber) handleAlarmCommand(ctx context.Context, cmd hapublisher.Command) {
	// <base>/alarm/<zone>/set — one wildcard, the zone.
	topic := cmd.Topic
	w, ok := c.routeWildcards(cmd, 1)
	if !ok {
		return
	}
	zone := w[0]
	action, code := parseAlarmAction(cmd.Payload)
	if action == "" {
		c.logger.Warn("mqtt.command.alarm.empty_payload", slog.String("topic", topic))
		return
	}
	if c.alarmSink == nil {
		c.logger.Debug("mqtt.command.alarm.no_sink",
			slog.String("topic", topic),
			slog.String("detail", "AlarmSink not wired; ignoring alarm command"))
		return
	}
	// The alarm command topic carries no <central> segment (zones are
	// daemon-level, see [AlarmSink]) — nothing to label this increment with.
	c.incReceivedCommands("")
	c.dispatchAlarm(ctx, topic, zone, action, code)
}

// alarmCommandTrigger is the HA panic command routed onto the engine's
// loud panic path (notes/concepts/alarm-concept.md §7). It has no master form.
const alarmCommandTrigger = "TRIGGER"

// alarmCommandResetMotion clears the zone's latched motion detectors.
// It is an openccu-loom extension to the HA command vocabulary, carried
// on the same command topic so the plane keeps one subscription.
const alarmCommandResetMotion = "RESET_MOTION"

// dispatchAlarm resolves the HA command string onto the alarm verb and
// calls it. The reserved "master" zone routes to the aggregate verbs;
// SILENCE and TRIGGER have no master form and are dropped for it. The
// parsed code is threaded into the per-zone verbs and validated by the
// sink; the master verbs stay code-free.
func (c *CommandSubscriber) dispatchAlarm(ctx context.Context, topic, zone, action, code string) {
	master := zone == alarmMasterZone
	switch action {
	case alarmpanel.HAAlarmCommandDisarm:
		var err error
		if master {
			err = c.alarmSink.MasterDisarm(ctx)
		} else {
			err = c.alarmSink.Disarm(ctx, zone, code)
		}
		if err != nil {
			c.logger.Warn("mqtt.command.alarm.disarm",
				slog.String("topic", topic), slog.String("err", err.Error()))
		}
	case alarmpanel.HAAlarmCommandSilence:
		if master {
			c.logger.Debug("mqtt.command.alarm.master_silence_unsupported", slog.String("topic", topic))
			return
		}
		if err := c.alarmSink.Silence(ctx, zone, code); err != nil {
			c.logger.Warn("mqtt.command.alarm.silence",
				slog.String("topic", topic), slog.String("err", err.Error()))
		}
	case alarmCommandResetMotion:
		var err error
		if master {
			err = c.alarmSink.MasterResetMotion(ctx)
		} else {
			err = c.alarmSink.ResetMotion(ctx, zone)
		}
		if err != nil {
			c.logger.Warn("mqtt.command.alarm.reset_motion",
				slog.String("topic", topic), slog.String("err", err.Error()))
		}
	case alarmCommandTrigger:
		if master {
			c.logger.Debug("mqtt.command.alarm.master_trigger_unsupported", slog.String("topic", topic))
			return
		}
		if err := c.alarmSink.Panic(ctx, zone); err != nil {
			c.logger.Warn("mqtt.command.alarm.trigger",
				slog.String("topic", topic), slog.String("err", err.Error()))
		}
	default:
		mode, ok := alarmpanel.ArmModeForCommand(action)
		if !ok {
			c.logger.Warn("mqtt.command.alarm.unknown_action",
				slog.String("topic", topic), slog.String("action", action))
			return
		}
		var err error
		if master {
			err = c.alarmSink.MasterArm(ctx, mode)
		} else {
			err = c.alarmSink.Arm(ctx, zone, mode, code)
		}
		if err != nil {
			c.logger.Warn("mqtt.command.alarm.arm",
				slog.String("topic", topic), slog.String("mode", string(mode)), slog.String("err", err.Error()))
		}
	}
}

// parseAlarmAction extracts the upper-cased HA command and the optional
// code from an alarm command payload, accepting a bare string (no code)
// or the JSON envelope. Returns an empty action for an empty or
// unparseable payload.
func parseAlarmAction(body []byte) (action, code string) {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "", ""
	}
	if strings.HasPrefix(s, "{") {
		var pay alarmCommandPayload
		if err := json.Unmarshal(body, &pay); err != nil {
			return "", ""
		}
		return strings.ToUpper(strings.TrimSpace(pay.Action)), pay.Code
	}
	return strings.ToUpper(s), ""
}

// handleAddonUpdateCommand triggers the add-on self-update install
// sequence from `<base>/system/addon_update/set`. HA's `update`
// entity publishes its configured `payload_install` ("INSTALL", see
// [DefaultDiscoveryBuilder.BuildAddonUpdateDiscovery]) here; any
// non-empty payload is accepted so a hand-built tool need not match
// the exact token.
func (c *CommandSubscriber) handleAddonUpdateCommand(ctx context.Context, cmd hapublisher.Command) {
	topic := cmd.Topic
	if strings.TrimSpace(string(cmd.Payload)) == "" {
		c.logger.Warn("mqtt.command.addon_update.empty_payload", slog.String("topic", topic))
		return
	}
	if c.addonSink == nil {
		c.logger.Debug("mqtt.command.addon_update.no_sink",
			slog.String("topic", topic),
			slog.String("detail", "AddonUpdateSink not wired; ignoring install command"))
		return
	}
	// The add-on-update command topic carries no <central> segment
	// (daemon-level self-updater, see [AddonUpdateSink]) — nothing to
	// label this increment with.
	c.incReceivedCommands("")
	if err := c.addonSink.TriggerInstall(ctx); err != nil {
		c.logger.Warn("mqtt.command.addon_update.install",
			slog.String("topic", topic),
			slog.String("err", err.Error()))
	}
}

// handleCDPInvoke dispatches a Custom-DP operation from
// `<base>/<central>/devices/<deviceAddr>/cdps/<name>/<operation>/invoke`.
// The `devices`, `cdps` and `invoke` literals are the route's, so the four
// wildcards are the central, the device address, the custom-DP name and the
// operation.
func (c *CommandSubscriber) handleCDPInvoke(ctx context.Context, cmd hapublisher.Command) {
	topic := cmd.Topic
	w, ok := c.routeWildcards(cmd, 4)
	if !ok {
		return
	}
	centralName, ok := c.resolveCentral(topic, w[0])
	if !ok {
		return
	}
	deviceAddr, name, operation := w[1], w[2], w[3]

	if c.cdpSink == nil {
		c.logger.Warn("mqtt.command.cdp.no_sink",
			slog.String("topic", topic),
			slog.String("detail", "CDPInvocationSink not wired; ignoring invoke"))
		return
	}

	var body CDPInvokePayload
	if len(cmd.Payload) > 0 {
		if err := json.Unmarshal(cmd.Payload, &body); err != nil {
			c.logger.Warn("mqtt.command.cdp.bad_payload",
				slog.String("topic", topic),
				slog.String("err", err.Error()))
			return
		}
	}

	priority := parseMQTTPriority(body.Priority)
	if err := c.cdpSink.InvokeCustomDP(ctx, centralName, deviceAddr, name, operation, body.Params, priority); err != nil {
		c.logger.Warn("mqtt.command.cdp.invoke",
			slog.String("topic", topic),
			slog.String("err", err.Error()))
	}
}

// parseMQTTPriority converts the optional priority string from the
// CDPInvokePayload to a [hmenum.CommandPriority]. Unknown strings and
// empty values default to High — the safest midpoint for command topics.
func parseMQTTPriority(s string) hmenum.CommandPriority {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return hmenum.CommandPriorityCritical
	case "low":
		return hmenum.CommandPriorityLow
	default: // "high", "", or anything else
		return hmenum.CommandPriorityHigh
	}
}

// parseCommandPayload normalises an MQTT payload into a native Go
// value: booleans (`true`/`false`), integers, floats, strings, and
// JSON literals all round-trip.
func parseCommandPayload(body []byte) any {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return nil
	}
	switch strings.ToLower(s) {
	case "true", "on":
		return true
	// "PRESS" is the payload_press token every HA `button` discovery
	// payload declares (per-parameter buttons, virtual-remote press
	// buttons). The target parameters are write-only ACTIONs whose
	// wire type is boolean — map the token to `true` so the press
	// actually triggers instead of failing bool coercion downstream.
	case "press":
		return true
	case "false", "off":
		return false
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	var v any
	if err := json.Unmarshal(body, &v); err == nil {
		return v
	}
	return s
}

// scalarPayloadToParams wraps a scalar MQTT payload for a service
// method into the JSON-decoded params map that
// `payload.Source.Invoke` expects. JSON-object payloads pass through
// as-is.
//
// scalarKeyResolver maps a method name to its canonical scalar-argument
// key. Pass [payload.GlobalScalarArgKey] in production — the key is
// populated at startup by [payload.ServiceRegistry.RegisterServiceWithArg]
// calls in each model package. Tests may inject a custom resolver.
//
// Empty payload → nil map (zero-arg methods like `lock`, `unlock`,
// `open`, `disable_boost`, `disable_away`).
func scalarPayloadToParams(method string, raw []byte, scalarKeyResolver func(string) string) (map[string]any, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, nil
	}
	// JSON object — forward verbatim.
	if strings.HasPrefix(trimmed, "{") {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, err
		}
		return obj, nil
	}
	// JSON-decode the scalar so booleans, numbers, and strings
	// end up as their canonical Go types.
	var scalar any
	if err := json.Unmarshal([]byte(quoteIfBareString(trimmed)), &scalar); err != nil {
		// Last-resort fallback: keep the raw string verbatim.
		scalar = trimmed
	}
	key := scalarKeyResolver(method)
	if key == "" {
		key = "value"
	}
	return map[string]any{key: scalar}, nil
}

// quoteIfBareString turns a bare identifier into a JSON string so
// json.Unmarshal accepts it. Already-valid JSON tokens (true, false,
// null, numbers, quoted strings) pass through unchanged.
func quoteIfBareString(s string) string {
	if s == "" {
		return `""`
	}
	switch s {
	case "true", "false", "null":
		return s
	}
	if c := s[0]; c == '"' || c == '-' || c == '+' || (c >= '0' && c <= '9') {
		return s
	}
	// Bare identifier — wrap in quotes.
	escaped := strings.ReplaceAll(s, `"`, `\"`)
	return `"` + escaped + `"`
}
