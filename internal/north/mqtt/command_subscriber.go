// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"

	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/reqctx"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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
// The interface deliberately mirrors the REST `ScheduleService.SetActiveProfile`
// shape so a single implementation backs both surfaces. `central`,
// `iface`, and the addr+channel split are extracted from the MQTT
// topic; `profileKey` is one of "P1".."PN" — the implementation
// validates the key against the channel's [AvailableProfiles].
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
// writes (Timer SetDuration, HSColor SetHSColor, …). The composition
// root wires this to the central registry; when nil, the subscriber
// drops combined-DP commands with a debug breadcrumb.
//
// `kind` matches the topic segment in the discovery payload's
// command_topic (e.g. "duration"). The implementation resolves the
// combined DP on the (central, iface, deviceAddr, channel) tuple and
// dispatches the typed write.
type CombinedDPSink interface {
	SetCombinedTimerSeconds(ctx context.Context,
		centralName, interfaceID, deviceAddress string, channel int,
		kind string, seconds float64,
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

// commandDispatchWorkers bounds how many command jobs (SetValue /
// SetMasterValue / SetSysvar / TriggerProgram / … — every one of them
// potentially a CCU write behind the circuit breaker/retry stack) can run
// concurrently. Kept modest: high enough that one stalled interface does
// not stall unrelated devices, low enough to bound goroutine + CCU-request
// fan-out from a single command burst.
const commandDispatchWorkers = 8

// commandDispatchQueueDepth bounds the per-worker backlog before Enqueue
// starts blocking (with a logged warning) the go-mqtt read loop that
// delivered the message. Sized for a burst of commands across many
// data points landing on the same worker slot.
const commandDispatchQueueDepth = 32

// CommandSubscriber wires the bridge's inbound /set and /invoke topics
// back into the domain. It subscribes to four wildcards and dispatches
// based on the topic shape (raw-plane schema; see ADR 0011):
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
	// WithLifecycleContext; command handlers derive each per-command
	// context from it so an in-flight CCU write is cancelled when the
	// daemon shuts down instead of running on a detached background
	// context. Defaults to context.Background() until wired.
	lifecycleCtx context.Context

	// dispatcher runs every sink call (the downstream I/O: SetValue,
	// SetMasterValue, InvokeCustomDP, …) off the go-mqtt client's
	// synchronous read loop, so a CCU write that blocks for seconds behind
	// the circuit breaker/retry stack never stalls PUBACK/PINGRESP
	// processing on that same goroutine. Jobs are keyed by the inbound
	// topic string, so repeated writes to the same data point never
	// reorder while unrelated data points dispatch concurrently. See
	// [boundedDispatcher].
	dispatcher *boundedDispatcher
}

// NewCommandSubscriber constructs the subscriber. Call
// [CommandSubscriber.Close] on teardown to drain the dispatcher's worker
// goroutines cleanly.
func NewCommandSubscriber(sub Subscriber, topics *TopicBuilder, sink CommandSink, logger *slog.Logger) *CommandSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &CommandSubscriber{
		sub: sub, topics: topics, sink: sink, qos: QoS1, logger: logger, lifecycleCtx: context.Background(),
		dispatcher: newBoundedDispatcher(commandDispatchWorkers, commandDispatchQueueDepth, "command", logger),
	}
}

// Close stops accepting new commands and blocks until every in-flight or
// already-queued command has finished running. Safe to call on a
// zero-value or nil *CommandSubscriber.
func (c *CommandSubscriber) Close() {
	if c == nil {
		return
	}
	c.dispatcher.Close()
}

// WaitIdle blocks until every command enqueued before this call has been
// dispatched to the sink. It is a deterministic test barrier for callers
// that assert on a fake sink right after delivering a message (commands
// now run off the caller's goroutine — see [CommandSubscriber.dispatcher])
// — production code does not need it, since commands are fire-and-forget
// by design. Safe to call on a nil *CommandSubscriber (no-op).
func (c *CommandSubscriber) WaitIdle() {
	if c == nil {
		return
	}
	c.dispatcher.flush()
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

// reservedLegacyParamSegments lists the segments a seven-level `.../set`
// topic can carry that belong to another subscription rather than to a CCU
// parameter. The legacy bucket-less data-point filter registered in
// [CommandSubscriber.Start] is a seven-level wildcard, so it matches every
// seven-level command topic the plane defines — and a broker fans a message
// out to all matching subscriptions, not just the most specific one.
//
// Every literal segment used in a seven-level command filter MUST be listed
// here, or that topic is dispatched a second time as a data-point write to a
// parameter that does not exist. The eight-level branch of
// [CommandSubscriber.handleDataPoint] gets the same protection for free from
// its bucket allow-list.
var reservedLegacyParamSegments = map[string]struct{}{
	"week_profile": {},
}

// Start attaches the four subscriptions.
func (c *CommandSubscriber) Start(ctx context.Context) error {
	if c.sub == nil {
		return errors.New("mqtt/command: no subscriber")
	}
	base := c.topics.Base
	// Bucket-aware data-point command topology (the canonical shape
	// the discovery builder advertises since the Option-B migration):
	//   <base>/<central>/<interface>/<addr>/<channel>/<bucket>/<param>/set
	// where `<bucket>` is `values` / `master` / `calculated`. The
	// switch / lock / select / number entities all publish here, so
	// the subscriber MUST register the 8-segment shape — without it
	// HA's `payload_on=true` to a Custom-DP switch arrives at the
	// broker but never reaches the daemon.
	if _, err := c.sub.Subscribe(ctx, base+"/+/+/+/+/+/+/set", c.qos, LegacyHandler(c.handleDataPoint)); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe datapoint bucket-aware: %w", err)
	}
	// Legacy 7-segment shape (no bucket infix) — still emitted by
	// some hand-built tools and by the legacy alias mirror on the
	// raw plane. Keep it active so existing automations don't break.
	if _, err := c.sub.Subscribe(ctx, base+"/+/+/+/+/+/set", c.qos, LegacyHandler(c.handleDataPoint)); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe datapoint legacy: %w", err)
	}
	// Canonical (ADR 0011): {base}/{central}/hub/sysvars/{name}/set.
	if _, err := c.sub.Subscribe(ctx, base+"/+/hub/sysvars/+/set", c.qos, LegacyHandler(c.handleSysvar)); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe hub_sysvar: %w", err)
	}
	// Canonical (ADR 0011): {base}/{central}/hub/programs/{id}/trigger.
	// Activation is a separate control from execution — a deactivated
	// program refuses to run — so it has its own topic (see
	// hub.Program.MQTTRoles).
	if _, err := c.sub.Subscribe(ctx, base+"/+/hub/programs/+/set", c.qos, LegacyHandler(c.handleProgramEnable)); err != nil {
		return err
	}
	if _, err := c.sub.Subscribe(ctx, base+"/+/hub/programs/+/trigger", c.qos, LegacyHandler(c.handleProgram)); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe hub_program: %w", err)
	}
	// Per-interface install-mode activation button:
	// {base}/{central}/hub/install_mode/{iface}/set — HA publishes the
	// press token; the handler activates pairing on the named interface.
	if _, err := c.sub.Subscribe(ctx, base+"/+/hub/install_mode/+/set", c.qos, LegacyHandler(c.handleInstallMode)); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe hub_install_mode: %w", err)
	}
	// {base}/{central}/devices/{device}/cdps/{name}/{operation}/invoke
	// MQTT wildcards cannot span /; use +/+/+/+/+/+/+/invoke to catch all.
	if _, err := c.sub.Subscribe(ctx, base+"/+/devices/+/cdps/+/+/invoke", c.qos, LegacyHandler(c.handleCDPInvoke)); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe cdp_invoke: %w", err)
	}
	// Canonical ADR-0011 per-service-method form:
	// {base}/{central}/{interface}/{address}/{channel}/custom/{kind}/set/{method}
	if _, err := c.sub.Subscribe(ctx, base+"/+/+/+/+/custom/+/set/+", c.qos, LegacyHandler(c.handleServiceMethod)); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe service_method: %w", err)
	}
	// {base}/{central}/{interface}/{address}/{channel}/week_profile/set
	// — the active-profile selector for climate channels (paired with
	// the discovery built by [DefaultDiscoveryBuilder.BuildWeekProfileDiscovery]).
	if _, err := c.sub.Subscribe(ctx, base+"/+/+/+/+/week_profile/set", c.qos, LegacyHandler(c.handleWeekProfile)); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe week_profile: %w", err)
	}
	// {base}/{central}/{interface}/{address}/{channel}/combined/{kind}/set
	// — combined-DP writes (Timer SetDuration etc.). Paired with the
	// discovery built by [DefaultDiscoveryBuilder.BuildCombinedTimerDiscovery].
	if _, err := c.sub.Subscribe(ctx, base+"/+/+/+/+/combined/+/set", c.qos, LegacyHandler(c.handleCombinedDP)); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe combined_dp: %w", err)
	}
	// {base}/{central}/{interface}/{address}/{channel}/schedule/{key}/set
	// — schedule-channel-switch writes (ScheduleChannelSwitch TurnOn/Off).
	// Paired with discovery from [DefaultDiscoveryBuilder.BuildScheduleSwitchDiscovery].
	if _, err := c.sub.Subscribe(ctx, base+"/+/+/+/+/schedule/+/set", c.qos, LegacyHandler(c.handleScheduleSwitch)); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe schedule_switch: %w", err)
	}
	// {base}/alarm/{zone}/set — the daemon-level alarm arm/disarm/silence
	// plane. Zones are daemon-level, so the topic carries no <central>
	// segment; the reserved <zone> "master" routes to the aggregate verbs.
	if _, err := c.sub.Subscribe(ctx, base+"/alarm/+/set", c.qos, LegacyHandler(c.handleAlarmCommand)); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe alarm_command: %w", err)
	}
	// {base}/system/addon_update/set — the daemon-level CCU add-on
	// self-update INSTALL command (ADR 0057). Daemon-level like the
	// alarm plane above, so the topic carries no <central> segment.
	if _, err := c.sub.Subscribe(ctx, base+"/system/addon_update/set", c.qos, LegacyHandler(c.handleAddonUpdateCommand)); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe addon_update_command: %w", err)
	}
	return nil
}

// incReceivedCommands increments the received_commands counter when a collector is wired.
func (c *CommandSubscriber) incReceivedCommands() {
	if c.collector != nil {
		c.collector.ReceivedCommands.Inc()
	}
}

// incSubscribeFailures increments the subscribe_failures counter when a collector is wired.
func (c *CommandSubscriber) incSubscribeFailures() {
	if c.collector != nil {
		c.collector.SubscribeFailures.Inc()
	}
}

// handleScheduleSwitch dispatches a payload from
// `<base>/<central>/<iface>/<addr>/<chan>/schedule/<key>/set` into the
// schedule-switch sink. Payload is "true" or "false" (HA's standard
// switch payload).
func (c *CommandSubscriber) handleScheduleSwitch(topic string, body []byte, retained bool) {
	if retained {
		c.logger.Debug("mqtt.command.schedule.retained_drop", slog.String("topic", topic))
		return
	}
	parts := strings.Split(topic, "/")
	if len(parts) != 8 || parts[5] != "schedule" || parts[7] != "set" {
		c.logger.Warn("mqtt.command.schedule.unknown_topic", slog.String("topic", topic))
		return
	}
	centralSeg, iface, deviceAddr, channelStr, key := parts[1], parts[2], parts[3], parts[4], parts[6]
	centralName, ok := c.resolveCentral(topic, centralSeg)
	if !ok {
		return
	}
	channel, err := strconv.Atoi(channelStr)
	if err != nil {
		c.logger.Warn("mqtt.command.schedule.bad_channel", slog.String("topic", topic))
		return
	}
	raw := strings.ToLower(strings.TrimSpace(string(body)))
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
	c.dispatcher.Enqueue(topic, func() {
		ctx, cancel := context.WithCancel(c.lifecycleCtx)
		defer cancel()
		if err := c.schedSink.SetScheduleSwitch(ctx, centralName, iface, deviceAddr, channel, key, enabled, hmenum.CommandPriorityHigh); err != nil {
			c.logger.Warn("mqtt.command.schedule.set",
				slog.String("topic", topic),
				slog.String("key", key),
				slog.Bool("enabled", enabled),
				slog.String("err", err.Error()))
		}
	})
}

// handleWeekProfile dispatches a payload from
// `<base>/<central>/<iface>/<addr>/<chan>/week_profile/set` to the
// week-profile sink. The payload is the profile key ("P1".."PN") as
// a plain string (no quotes / no JSON envelope) — the same shape HA
// publishes for `select` entities.
func (c *CommandSubscriber) handleWeekProfile(topic string, body []byte, retained bool) {
	if retained {
		c.logger.Debug("mqtt.command.wp.retained_drop", slog.String("topic", topic))
		return
	}
	parts := strings.Split(topic, "/")
	if len(parts) != 7 || parts[5] != "week_profile" || parts[6] != "set" {
		c.logger.Warn("mqtt.command.wp.unknown_topic", slog.String("topic", topic))
		return
	}
	centralSeg, iface, deviceAddr, channelStr := parts[1], parts[2], parts[3], parts[4]
	centralName, ok := c.resolveCentral(topic, centralSeg)
	if !ok {
		return
	}
	channel, err := strconv.Atoi(channelStr)
	if err != nil {
		c.logger.Warn("mqtt.command.wp.bad_channel", slog.String("topic", topic))
		return
	}
	profile := strings.TrimSpace(string(body))
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
	c.dispatcher.Enqueue(topic, func() {
		ctx, cancel := context.WithCancel(c.lifecycleCtx)
		defer cancel()
		if err := c.wpSink.SetActiveProfile(ctx, centralName, iface, deviceAddr, channel, profile, hmenum.CommandPriorityHigh); err != nil {
			c.logger.Warn("mqtt.command.wp.set_active_profile",
				slog.String("topic", topic),
				slog.String("profile", profile),
				slog.String("err", err.Error()))
		}
	})
}

// handleCombinedDP dispatches a payload from
// `<base>/<central>/<iface>/<addr>/<chan>/combined/<kind>/set` into the
// combined-DP sink. Only the Timer kind ("duration") is wired today;
// HSColor / LevelCombined remain attachable scaffolding.
//
// Payload is the seconds value as a plain decimal string (HA's MQTT
// number entity publishes "30" for 30 seconds, not a JSON envelope).
func (c *CommandSubscriber) handleCombinedDP(topic string, body []byte, retained bool) {
	if retained {
		c.logger.Debug("mqtt.command.combined.retained_drop", slog.String("topic", topic))
		return
	}
	parts := strings.Split(topic, "/")
	if len(parts) != 8 || parts[5] != "combined" || parts[7] != "set" {
		c.logger.Warn("mqtt.command.combined.unknown_topic", slog.String("topic", topic))
		return
	}
	centralSeg, iface, deviceAddr, channelStr, kind := parts[1], parts[2], parts[3], parts[4], parts[6]
	centralName, ok := c.resolveCentral(topic, centralSeg)
	if !ok {
		return
	}
	channel, err := strconv.Atoi(channelStr)
	if err != nil {
		c.logger.Warn("mqtt.command.combined.bad_channel", slog.String("topic", topic))
		return
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(body)), 64)
	if err != nil {
		c.logger.Warn("mqtt.command.combined.bad_payload",
			slog.String("topic", topic),
			slog.String("err", err.Error()))
		return
	}
	if c.cmbSink == nil {
		c.logger.Debug("mqtt.command.combined.no_sink",
			slog.String("topic", topic),
			slog.String("detail", "CombinedDPSink not wired; ignoring combined-DP write"))
		return
	}
	c.dispatcher.Enqueue(topic, func() {
		ctx, cancel := context.WithCancel(c.lifecycleCtx)
		defer cancel()
		if err := c.cmbSink.SetCombinedTimerSeconds(ctx, centralName, iface, deviceAddr, channel, kind, seconds, hmenum.CommandPriorityHigh); err != nil {
			c.logger.Warn("mqtt.command.combined.set",
				slog.String("topic", topic),
				slog.String("kind", kind),
				slog.Float64("seconds", seconds),
				slog.String("err", err.Error()))
		}
	})
}

// handleServiceMethod dispatches a payload from the canonical
// ADR-0011 per-service-method topic
// `<base>/<central>/<iface>/<addr>/<chan>/custom/<kind>/set/<method>`
// into `Source.Invoke`.
//
// The MQTT payload may be a JSON object (forwarded verbatim as
// `params`) or a scalar (wrapped under the canonical argument name
// for `method` — see `service_method_routing.go`).
func (c *CommandSubscriber) handleServiceMethod(topic string, raw []byte, retained bool) {
	if retained {
		c.logger.Debug("mqtt.command.svc.retained_drop", slog.String("topic", topic))
		return
	}
	parts := strings.Split(topic, "/")
	if len(parts) != 9 || parts[5] != "custom" || parts[7] != "set" {
		c.logger.Warn("mqtt.command.svc.unknown_topic", slog.String("topic", topic))
		return
	}
	centralSeg, iface, deviceAddr, channelStr, method := parts[1], parts[2], parts[3], parts[4], parts[8]
	centralName, ok := c.resolveCentral(topic, centralSeg)
	if !ok {
		return
	}
	channel, err := strconv.Atoi(channelStr)
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
	params, err := scalarPayloadToParams(method, raw, payload.GlobalScalarArgKey)
	if err != nil {
		c.logger.Warn("mqtt.command.svc.bad_payload",
			slog.String("topic", topic),
			slog.String("err", err.Error()))
		return
	}
	c.dispatcher.Enqueue(topic, func() {
		ctx, cancel := context.WithCancel(c.lifecycleCtx)
		defer cancel()
		if err := c.cdpSink.InvokeChannelService(ctx, centralName, iface, deviceAddr, channel, method, params, hmenum.CommandPriorityHigh); err != nil {
			c.logger.Warn("mqtt.command.svc.invoke",
				slog.String("topic", topic),
				slog.String("method", method),
				slog.String("err", err.Error()))
		}
	})
}

func (c *CommandSubscriber) handleDataPoint(topic string, body []byte, retained bool) {
	if retained {
		// Retained `*/set` replays at subscribe time would re-issue
		// the last write to the CCU on every daemon restart. HA never
		// publishes set-topics retained; an external publisher with
		// retain=true is almost always a configuration mistake.
		c.logger.Debug("mqtt.command.dp.retained_drop", slog.String("topic", topic))
		return
	}
	// Two accepted shapes — both end with `/set`:
	//
	//   1. Bucket-aware (canonical, emitted by the discovery builder):
	//      <base>/<central>/<iface>/<addr>/<channel>/<bucket>/<param>/set
	//      (8 segments; `<bucket>` is `values`/`master`/`calculated`).
	//
	//   2. Legacy bucket-less shape (still produced by some hand-built
	//      tools and the legacy alias mirror on the raw plane):
	//      <base>/<central>/<iface>/<addr>/<channel>/<param>/set
	//      (7 segments).
	//
	// The 7-segment (legacy) form always routes to VALUES. In the 8-segment
	// form `values` routes to SetValue, `master` routes to SetMasterValue,
	// and `calculated` is read-only and is dropped with a debug log.
	parts := strings.Split(topic, "/")
	if parts[len(parts)-1] != "set" {
		c.logger.Warn("mqtt.command.unknown_topic", slog.String("topic", topic))
		return
	}
	var centralSeg, iface, device, channelStr, parameter string
	isMaster := false
	switch len(parts) {
	case 7:
		// A broker delivers a message to EVERY matching subscription, and
		// the legacy filter is the same length as the filters that own a
		// literal segment here. Without this guard picking a heating profile
		// wrote the profile AND issued a CCU write for a parameter named
		// `week_profile`, which no channel has.
		if _, reserved := reservedLegacyParamSegments[parts[5]]; reserved {
			c.logger.Debug("mqtt.command.reserved_segment",
				slog.String("topic", topic),
				slog.String("segment", parts[5]))
			return
		}
		centralSeg, iface, device, channelStr, parameter = parts[1], parts[2], parts[3], parts[4], parts[5]
	case 8:
		bucket := parts[5]
		switch bucket {
		case "values":
			// Default VALUES write — no special flag needed.
		case "master":
			isMaster = true
		default:
			// `calculated` and any unknown bucket are read-only; drop
			// with a debug breadcrumb so operators can diagnose
			// mis-directed writes.
			c.logger.Debug("mqtt.command.unsupported_bucket",
				slog.String("topic", topic),
				slog.String("bucket", bucket))
			return
		}
		centralSeg, iface, device, channelStr, parameter = parts[1], parts[2], parts[3], parts[4], parts[6]
	default:
		c.logger.Warn("mqtt.command.unknown_topic", slog.String("topic", topic))
		return
	}
	centralName, ok := c.resolveCentral(topic, centralSeg)
	if !ok {
		return
	}
	channel, err := strconv.Atoi(channelStr)
	if err != nil {
		c.logger.Warn("mqtt.command.bad_channel", slog.String("topic", topic))
		return
	}
	value := parseCommandPayload(body)
	channelAddress := fmt.Sprintf("%s:%d", device, channel)
	c.incReceivedCommands()
	c.dispatcher.Enqueue(topic, func() {
		ctx, cancel := context.WithCancel(c.lifecycleCtx)
		defer cancel()
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
	})
}

func (c *CommandSubscriber) handleSysvar(topic string, body []byte, retained bool) {
	if retained {
		c.logger.Debug("mqtt.command.sysvar.retained_drop", slog.String("topic", topic))
		return
	}
	// Canonical ADR-0011: <base>/<central>/hub/sysvars/<name>/set
	parts := strings.Split(topic, "/")
	if len(parts) != 6 || parts[2] != "hub" || parts[3] != "sysvars" || parts[5] != "set" {
		return
	}
	centralSeg, name := parts[1], parts[4]
	centralName, ok := c.resolveCentral(topic, centralSeg)
	if !ok {
		return
	}
	value := parseCommandPayload(body)
	c.incReceivedCommands()
	c.dispatcher.Enqueue(topic, func() {
		ctx, cancel := context.WithCancel(c.lifecycleCtx)
		defer cancel()
		if err := c.sink.SetSysvar(ctx, centralName, name, value); err != nil {
			c.logger.Warn("mqtt.command.setsysvar",
				slog.String("topic", topic), slog.String("err", err.Error()))
		}
	})
}

func (c *CommandSubscriber) handleProgram(topic string, body []byte, retained bool) {
	if retained {
		c.logger.Debug("mqtt.command.program.retained_drop", slog.String("topic", topic))
		return
	}
	// An empty payload is not a command. It is what the retain-cleanup
	// pass publishes to evict a parked retained message from the trigger
	// topic — the broker forwards that eviction to this very
	// subscription as a live (non-retained) message, and executing a CCU
	// program because a topic was cleaned would repeat the state-mirror
	// defect this guard exists to keep out.
	if strings.TrimSpace(string(body)) == "" {
		c.logger.Debug("mqtt.command.program.empty_drop", slog.String("topic", topic))
		return
	}
	// Canonical ADR-0011: <base>/<central>/hub/programs/<id>/trigger
	parts := strings.Split(topic, "/")
	if len(parts) != 6 || parts[2] != "hub" || parts[3] != "programs" || parts[5] != "trigger" {
		return
	}
	centralSeg, id := parts[1], parts[4]
	centralName, ok := c.resolveCentral(topic, centralSeg)
	if !ok {
		return
	}
	c.incReceivedCommands()
	c.dispatcher.Enqueue(topic, func() {
		ctx, cancel := context.WithCancel(c.lifecycleCtx)
		defer cancel()
		// Stamp the surface so the program-execute audit/log subscriber
		// can attribute the run to the MQTT command plane.
		ctx = reqctx.WithOperation(ctx, "mqtt:program-trigger")
		if err := c.sink.TriggerProgram(ctx, centralName, id); err != nil {
			c.logger.Warn("mqtt.command.program",
				slog.String("topic", topic), slog.String("err", err.Error()))
		}
	})
}

// handleProgramEnable toggles a program's CCU-side activity flag from
// `<base>/<central>/hub/programs/<id>/set`. While the flag is off the CCU
// ignores the program's triggers and refuses a manual run, so this is the
// control that decides whether the paired execute button does anything.
func (c *CommandSubscriber) handleProgramEnable(topic string, body []byte, retained bool) {
	if retained {
		c.logger.Debug("mqtt.command.program_enable.retained_drop", slog.String("topic", topic))
		return
	}
	parts := strings.Split(topic, "/")
	if len(parts) != 6 || parts[2] != "hub" || parts[3] != "programs" || parts[5] != "set" {
		return
	}
	centralSeg, id := parts[1], parts[4]
	centralName, ok := c.resolveCentral(topic, centralSeg)
	if !ok {
		return
	}
	enabled, ok := parseBoolPayload(body)
	if !ok {
		c.logger.Warn("mqtt.command.program_enable.bad_payload",
			slog.String("topic", topic), slog.String("payload", string(body)))
		return
	}
	c.incReceivedCommands()
	c.dispatcher.Enqueue(topic, func() {
		ctx, cancel := context.WithCancel(c.lifecycleCtx)
		defer cancel()
		if err := c.sink.SetProgramEnabled(ctx, centralName, id, enabled); err != nil {
			c.logger.Warn("mqtt.command.program_enable",
				slog.String("topic", topic), slog.String("err", err.Error()))
		}
	})
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
func (c *CommandSubscriber) handleInstallMode(topic string, body []byte, retained bool) {
	if retained {
		c.logger.Debug("mqtt.command.install_mode.retained_drop", slog.String("topic", topic))
		return
	}
	// <base>/<central>/hub/install_mode/<iface>/set
	parts := strings.Split(topic, "/")
	if len(parts) != 6 || parts[2] != "hub" || parts[3] != "install_mode" || parts[5] != "set" {
		c.logger.Warn("mqtt.command.install_mode.unknown_topic", slog.String("topic", topic))
		return
	}
	centralSeg, iface := parts[1], parts[4]
	centralName, ok := c.resolveCentral(topic, centralSeg)
	if !ok {
		return
	}
	if c.imSink == nil {
		c.logger.Debug("mqtt.command.install_mode.no_sink",
			slog.String("topic", topic),
			slog.String("detail", "InstallModeSink not wired; ignoring install-mode press"))
		return
	}
	// "PRESS" (HA button) and empty payloads request the default
	// duration (seconds=0 → sink default); a bare integer overrides it.
	seconds := 0
	if raw := strings.TrimSpace(string(body)); raw != "" && !strings.EqualFold(raw, "PRESS") {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			seconds = n
		}
	}
	c.incReceivedCommands()
	c.dispatcher.Enqueue(topic, func() {
		ctx, cancel := context.WithCancel(c.lifecycleCtx)
		defer cancel()
		if err := c.imSink.ActivateInstallMode(ctx, centralName, iface, seconds); err != nil {
			c.logger.Warn("mqtt.command.install_mode.activate",
				slog.String("topic", topic),
				slog.String("interface", iface),
				slog.String("err", err.Error()))
		}
	})
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
func (c *CommandSubscriber) handleAlarmCommand(topic string, body []byte, retained bool) {
	if retained {
		c.logger.Debug("mqtt.command.alarm.retained_drop", slog.String("topic", topic))
		return
	}
	// <base>/alarm/<zone>/set
	parts := strings.Split(topic, "/")
	if len(parts) != 4 || parts[1] != "alarm" || parts[3] != "set" {
		c.logger.Warn("mqtt.command.alarm.unknown_topic", slog.String("topic", topic))
		return
	}
	zone := parts[2]
	action, code := parseAlarmAction(body)
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
	c.incReceivedCommands()
	c.dispatchAlarm(topic, zone, action, code)
}

// alarmCommandTrigger is the HA panic command routed onto the engine's
// loud panic path (notes/concepts/alarm-concept.md §7). It has no master form.
const alarmCommandTrigger = "TRIGGER"

// alarmCommandResetMotion clears the zone's latched motion detectors.
// It is an openccu-loom extension to the HA command vocabulary, carried
// on the same command topic so the plane keeps one subscription.
const alarmCommandResetMotion = "RESET_MOTION"

// dispatchAlarm resolves the HA command string onto the alarm verb and
// enqueues it. The reserved "master" zone routes to the aggregate verbs;
// SILENCE and TRIGGER have no master form and are dropped for it. The
// parsed code is threaded into the per-zone verbs and validated by the
// sink; the master verbs stay code-free.
func (c *CommandSubscriber) dispatchAlarm(topic, zone, action, code string) {
	master := zone == alarmMasterZone
	switch action {
	case alarmpanel.HAAlarmCommandDisarm:
		c.dispatcher.Enqueue(topic, func() {
			ctx, cancel := context.WithCancel(c.lifecycleCtx)
			defer cancel()
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
		})
	case alarmpanel.HAAlarmCommandSilence:
		if master {
			c.logger.Debug("mqtt.command.alarm.master_silence_unsupported", slog.String("topic", topic))
			return
		}
		c.dispatcher.Enqueue(topic, func() {
			ctx, cancel := context.WithCancel(c.lifecycleCtx)
			defer cancel()
			if err := c.alarmSink.Silence(ctx, zone, code); err != nil {
				c.logger.Warn("mqtt.command.alarm.silence",
					slog.String("topic", topic), slog.String("err", err.Error()))
			}
		})
	case alarmCommandResetMotion:
		c.dispatcher.Enqueue(topic, func() {
			ctx, cancel := context.WithCancel(c.lifecycleCtx)
			defer cancel()
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
		})
	case alarmCommandTrigger:
		if master {
			c.logger.Debug("mqtt.command.alarm.master_trigger_unsupported", slog.String("topic", topic))
			return
		}
		c.dispatcher.Enqueue(topic, func() {
			ctx, cancel := context.WithCancel(c.lifecycleCtx)
			defer cancel()
			if err := c.alarmSink.Panic(ctx, zone); err != nil {
				c.logger.Warn("mqtt.command.alarm.trigger",
					slog.String("topic", topic), slog.String("err", err.Error()))
			}
		})
	default:
		mode, ok := alarmpanel.ArmModeForCommand(action)
		if !ok {
			c.logger.Warn("mqtt.command.alarm.unknown_action",
				slog.String("topic", topic), slog.String("action", action))
			return
		}
		c.dispatcher.Enqueue(topic, func() {
			ctx, cancel := context.WithCancel(c.lifecycleCtx)
			defer cancel()
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
		})
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
func (c *CommandSubscriber) handleAddonUpdateCommand(topic string, body []byte, retained bool) {
	if retained {
		c.logger.Debug("mqtt.command.addon_update.retained_drop", slog.String("topic", topic))
		return
	}
	if strings.TrimSpace(string(body)) == "" {
		c.logger.Warn("mqtt.command.addon_update.empty_payload", slog.String("topic", topic))
		return
	}
	if c.addonSink == nil {
		c.logger.Debug("mqtt.command.addon_update.no_sink",
			slog.String("topic", topic),
			slog.String("detail", "AddonUpdateSink not wired; ignoring install command"))
		return
	}
	c.incReceivedCommands()
	c.dispatcher.Enqueue(topic, func() {
		ctx, cancel := context.WithCancel(c.lifecycleCtx)
		defer cancel()
		if err := c.addonSink.TriggerInstall(ctx); err != nil {
			c.logger.Warn("mqtt.command.addon_update.install",
				slog.String("topic", topic),
				slog.String("err", err.Error()))
		}
	})
}

func (c *CommandSubscriber) handleCDPInvoke(topic string, raw []byte, retained bool) {
	if retained {
		c.logger.Debug("mqtt.command.cdp.retained_drop", slog.String("topic", topic))
		return
	}
	// <base>/<central>/devices/<deviceAddr>/cdps/<name>/<operation>/invoke
	parts := strings.Split(topic, "/")
	if len(parts) != 8 || parts[2] != "devices" || parts[4] != "cdps" || parts[7] != "invoke" {
		c.logger.Warn("mqtt.command.cdp.unknown_topic", slog.String("topic", topic))
		return
	}
	centralSeg, deviceAddr, name, operation := parts[1], parts[3], parts[5], parts[6]
	centralName, ok := c.resolveCentral(topic, centralSeg)
	if !ok {
		return
	}

	if c.cdpSink == nil {
		c.logger.Warn("mqtt.command.cdp.no_sink",
			slog.String("topic", topic),
			slog.String("detail", "CDPInvocationSink not wired; ignoring invoke"))
		return
	}

	var body CDPInvokePayload
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			c.logger.Warn("mqtt.command.cdp.bad_payload",
				slog.String("topic", topic),
				slog.String("err", err.Error()))
			return
		}
	}

	priority := parseMQTTPriority(body.Priority)
	c.dispatcher.Enqueue(topic, func() {
		ctx, cancel := context.WithCancel(c.lifecycleCtx)
		defer cancel()
		if err := c.cdpSink.InvokeCustomDP(ctx, centralName, deviceAddr, name, operation, body.Params, priority); err != nil {
			c.logger.Warn("mqtt.command.cdp.invoke",
				slog.String("topic", topic),
				slog.String("err", err.Error()))
		}
	})
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
