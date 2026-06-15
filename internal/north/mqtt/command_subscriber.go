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

	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// CommandSink is the domain-facing write contract. The composition
// root wires this to the central's ValueWriter; tests can stub it.
type CommandSink interface {
	SetValue(ctx context.Context, centralName, interfaceID, channelAddress string,
		parameter hmenum.Parameter, value any, priority hmenum.CommandPriority) error
	SetSysvar(ctx context.Context, centralName, name string, payload any) error
	TriggerProgram(ctx context.Context, centralName, id string) error
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
	logger    *slog.Logger
	// lifecycleCtx is the daemon-lifetime context wired via
	// WithLifecycleContext; command handlers derive each per-command
	// context from it so an in-flight CCU write is cancelled when the
	// daemon shuts down instead of running on a detached background
	// context. Defaults to context.Background() until wired.
	lifecycleCtx context.Context
}

// NewCommandSubscriber constructs the subscriber.
func NewCommandSubscriber(sub Subscriber, topics *TopicBuilder, sink CommandSink, logger *slog.Logger) *CommandSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &CommandSubscriber{sub: sub, topics: topics, sink: sink, logger: logger, lifecycleCtx: context.Background()}
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
	if err := c.sub.Subscribe(ctx, base+"/+/+/+/+/+/+/set", QoS1, c.handleDataPoint); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe datapoint bucket-aware: %w", err)
	}
	// Legacy 7-segment shape (no bucket infix) — still emitted by
	// some hand-built tools and by the legacy alias mirror on the
	// raw plane. Keep it active so existing automations don't break.
	if err := c.sub.Subscribe(ctx, base+"/+/+/+/+/+/set", QoS1, c.handleDataPoint); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe datapoint legacy: %w", err)
	}
	// Canonical (ADR 0011): {base}/{central}/hub/sysvars/{name}/set.
	if err := c.sub.Subscribe(ctx, base+"/+/hub/sysvars/+/set", QoS1, c.handleSysvar); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe hub_sysvar: %w", err)
	}
	// Canonical (ADR 0011): {base}/{central}/hub/programs/{id}/trigger.
	if err := c.sub.Subscribe(ctx, base+"/+/hub/programs/+/trigger", QoS1, c.handleProgram); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe hub_program: %w", err)
	}
	// Per-interface install-mode activation button:
	// {base}/{central}/hub/install_mode/{iface}/set — HA publishes the
	// press token; the handler activates pairing on the named interface.
	if err := c.sub.Subscribe(ctx, base+"/+/hub/install_mode/+/set", QoS1, c.handleInstallMode); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe hub_install_mode: %w", err)
	}
	// {base}/{central}/devices/{device}/cdps/{name}/{operation}/invoke
	// MQTT wildcards cannot span /; use +/+/+/+/+/+/+/invoke to catch all.
	if err := c.sub.Subscribe(ctx, base+"/+/devices/+/cdps/+/+/invoke", QoS1, c.handleCDPInvoke); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe cdp_invoke: %w", err)
	}
	// Canonical ADR-0011 per-service-method form:
	// {base}/{central}/{interface}/{address}/{channel}/custom/{kind}/set/{method}
	if err := c.sub.Subscribe(ctx, base+"/+/+/+/+/custom/+/set/+", QoS1, c.handleServiceMethod); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe service_method: %w", err)
	}
	// {base}/{central}/{interface}/{address}/{channel}/week_profile/set
	// — the active-profile selector for climate channels (paired with
	// the discovery built by [DefaultDiscoveryBuilder.BuildWeekProfileDiscovery]).
	if err := c.sub.Subscribe(ctx, base+"/+/+/+/+/week_profile/set", QoS1, c.handleWeekProfile); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe week_profile: %w", err)
	}
	// {base}/{central}/{interface}/{address}/{channel}/combined/{kind}/set
	// — combined-DP writes (Timer SetDuration etc.). Paired with the
	// discovery built by [DefaultDiscoveryBuilder.BuildCombinedTimerDiscovery].
	if err := c.sub.Subscribe(ctx, base+"/+/+/+/+/combined/+/set", QoS1, c.handleCombinedDP); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe combined_dp: %w", err)
	}
	// {base}/{central}/{interface}/{address}/{channel}/schedule/{key}/set
	// — schedule-channel-switch writes (ScheduleChannelSwitch TurnOn/Off).
	// Paired with discovery from [DefaultDiscoveryBuilder.BuildScheduleSwitchDiscovery].
	if err := c.sub.Subscribe(ctx, base+"/+/+/+/+/schedule/+/set", QoS1, c.handleScheduleSwitch); err != nil {
		c.incSubscribeFailures()
		return fmt.Errorf("subscribe schedule_switch: %w", err)
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
	centralName, iface, deviceAddr, channelStr, key := parts[1], parts[2], parts[3], parts[4], parts[6]
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
	ctx, cancel := context.WithCancel(c.lifecycleCtx)
	defer cancel()
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
	centralName, iface, deviceAddr, channelStr := parts[1], parts[2], parts[3], parts[4]
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
	ctx, cancel := context.WithCancel(c.lifecycleCtx)
	defer cancel()
	if err := c.wpSink.SetActiveProfile(ctx, centralName, iface, deviceAddr, channel, profile, hmenum.CommandPriorityHigh); err != nil {
		c.logger.Warn("mqtt.command.wp.set_active_profile",
			slog.String("topic", topic),
			slog.String("profile", profile),
			slog.String("err", err.Error()))
	}
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
	centralName, iface, deviceAddr, channelStr, kind := parts[1], parts[2], parts[3], parts[4], parts[6]
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
	ctx, cancel := context.WithCancel(c.lifecycleCtx)
	defer cancel()
	if err := c.cmbSink.SetCombinedTimerSeconds(ctx, centralName, iface, deviceAddr, channel, kind, seconds, hmenum.CommandPriorityHigh); err != nil {
		c.logger.Warn("mqtt.command.combined.set",
			slog.String("topic", topic),
			slog.String("kind", kind),
			slog.Float64("seconds", seconds),
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
	centralName, iface, deviceAddr, channelStr, method := parts[1], parts[2], parts[3], parts[4], parts[8]
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
	ctx, cancel := context.WithCancel(c.lifecycleCtx)
	defer cancel()
	if err := c.cdpSink.InvokeChannelService(ctx, centralName, iface, deviceAddr, channel, method, params, hmenum.CommandPriorityHigh); err != nil {
		c.logger.Warn("mqtt.command.svc.invoke",
			slog.String("topic", topic),
			slog.String("method", method),
			slog.String("err", err.Error()))
	}
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
	//   2. Legacy bucket-less shape (still produced by some
	// Hand-built tools and
	//      <base>/<central>/<iface>/<addr>/<channel>/<param>/set
	//      (7 segments).
	//
	// Both write VALUES paramset — MASTER edits flow through the
	// REST paramset endpoint, not the MQTT command bus. The
	// 8-segment form happens to carry the bucket but the subscriber
	// only honours `values`; other buckets are silently ignored
	// here (the caller's discovery would not emit a master/
	// command_topic in the first place).
	parts := strings.Split(topic, "/")
	if parts[len(parts)-1] != "set" {
		c.logger.Warn("mqtt.command.unknown_topic", slog.String("topic", topic))
		return
	}
	var centralName, iface, device, channelStr, parameter string
	switch len(parts) {
	case 7:
		centralName, iface, device, channelStr, parameter = parts[1], parts[2], parts[3], parts[4], parts[5]
	case 8:
		bucket := parts[5]
		if bucket != "values" {
			// Master / calculated buckets are not write-capable
			// from the MQTT command bus; drop silently with a debug
			// breadcrumb so operators see the topic if they're
			// pointing the wrong tool at it.
			c.logger.Debug("mqtt.command.unsupported_bucket",
				slog.String("topic", topic),
				slog.String("bucket", bucket))
			return
		}
		centralName, iface, device, channelStr, parameter = parts[1], parts[2], parts[3], parts[4], parts[6]
	default:
		c.logger.Warn("mqtt.command.unknown_topic", slog.String("topic", topic))
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
	ctx, cancel := context.WithCancel(c.lifecycleCtx)
	defer cancel()
	if err := c.sink.SetValue(ctx, centralName, iface, channelAddress,
		hmenum.Parameter(parameter), value, hmenum.CommandPriorityHigh); err != nil {
		c.logger.Warn("mqtt.command.setvalue",
			slog.String("topic", topic),
			slog.String("err", err.Error()))
	}
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
	centralName, name := parts[1], parts[4]
	value := parseCommandPayload(body)
	c.incReceivedCommands()
	ctx, cancel := context.WithCancel(c.lifecycleCtx)
	defer cancel()
	if err := c.sink.SetSysvar(ctx, centralName, name, value); err != nil {
		c.logger.Warn("mqtt.command.setsysvar",
			slog.String("topic", topic), slog.String("err", err.Error()))
	}
}

func (c *CommandSubscriber) handleProgram(topic string, _ []byte, retained bool) {
	if retained {
		c.logger.Debug("mqtt.command.program.retained_drop", slog.String("topic", topic))
		return
	}
	// Canonical ADR-0011: <base>/<central>/hub/programs/<id>/trigger
	parts := strings.Split(topic, "/")
	if len(parts) != 6 || parts[2] != "hub" || parts[3] != "programs" || parts[5] != "trigger" {
		return
	}
	centralName, id := parts[1], parts[4]
	c.incReceivedCommands()
	ctx, cancel := context.WithCancel(c.lifecycleCtx)
	defer cancel()
	if err := c.sink.TriggerProgram(ctx, centralName, id); err != nil {
		c.logger.Warn("mqtt.command.program",
			slog.String("topic", topic), slog.String("err", err.Error()))
	}
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
	centralName, iface := parts[1], parts[4]
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
	ctx, cancel := context.WithCancel(c.lifecycleCtx)
	defer cancel()
	if err := c.imSink.ActivateInstallMode(ctx, centralName, iface, seconds); err != nil {
		c.logger.Warn("mqtt.command.install_mode.activate",
			slog.String("topic", topic),
			slog.String("interface", iface),
			slog.String("err", err.Error()))
	}
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
	centralName, deviceAddr, name, operation := parts[1], parts[3], parts[5], parts[6]

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
	ctx, cancel := context.WithCancel(c.lifecycleCtx)
	defer cancel()
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
