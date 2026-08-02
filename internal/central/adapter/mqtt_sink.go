// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// MQTTCommandSink is the domain facade the MQTT [CommandSubscriber]
// dispatches into. It implements the [mqtt.CommandSink] and
// [mqtt.CDPInvocationSink] interfaces by routing to the owning
// central's registry and client layer.
//
// CDP invocations (InvokeCustomDP) are routed through an embedded
// [CustomDPDispatcher] so that the exact same category-dispatch
// logic used by the REST layer is re-used over MQTT.
type MQTTCommandSink struct {
	registry    *central.Registry
	writer      ValueWriter
	cdpDispatch *CustomDPDispatcher
}

// NewMQTTCommandSink constructs the adapter.
func NewMQTTCommandSink(r *central.Registry, w ValueWriter) *MQTTCommandSink {
	return &MQTTCommandSink{
		registry:    r,
		writer:      w,
		cdpDispatch: NewCustomDPDispatcher(r),
	}
}

// SetValue routes the DP command to the configured writer.
func (s *MQTTCommandSink) SetValue(
	ctx context.Context, centralName, interfaceID, channelAddress string,
	parameter hmenum.Parameter, value any, priority hmenum.CommandPriority,
) error {
	if s.writer == nil {
		return ErrNoWriter
	}
	return s.writer.SetValue(ctx, centralName, interfaceID, channelAddress, parameter, value, priority)
}

// SetMasterValue implements [mqtt.CommandSink]. It resolves the channel
// in the central's model registry and writes a single MASTER-paramset
// parameter via [device.Channel.Set] with [hmenum.ParamsetKeyMaster].
//
// This is the canonical single-MASTER-param write path — the same
// Channel.Set route that PutParamset uses for batch MASTER writes —
// so MQTT and REST share identical wire behaviour.
//
// channelAddress is the full "<device>:<channel>" form as extracted
// from the MQTT topic (e.g. "0001ABCD:1"). When interfaceID is
// non-empty the resolved device must belong to that interface; if more
// than one device with the same address exists across interfaces (a
// degenerate multi-CCU edge case is not possible within one central,
// but the guard is cheap) the call returns a clear error instead of
// silently picking the first. Returns a descriptive error when the
// central, device, channel, or parameter cannot be resolved.
func (s *MQTTCommandSink) SetMasterValue(
	ctx context.Context, centralName, interfaceID, channelAddress string,
	parameter hmenum.Parameter, value any, priority hmenum.CommandPriority,
) error {
	c, ok := s.registry.Get(centralName)
	if !ok {
		return fmt.Errorf("mqtt_sink: unknown central %q", centralName)
	}
	deviceAddress := channelAddress
	if i := strings.LastIndexByte(channelAddress, ':'); i > 0 {
		deviceAddress = channelAddress[:i]
	}
	dev, ok := c.ModelRegistry.Get(deviceAddress)
	if !ok || dev == nil {
		return fmt.Errorf("mqtt_sink: unknown device %q on %s", deviceAddress, centralName)
	}
	// When the caller supplies an interface hint, verify the resolved
	// device actually belongs to that interface. Within a single central
	// the model registry is keyed on device address so there can only
	// ever be one entry — the check still guards against an operator
	// misconfiguration where the same address is used on two interfaces.
	if interfaceID != "" && dev.InterfaceID != "" && dev.InterfaceID != interfaceID {
		return fmt.Errorf("mqtt_sink: device %q belongs to interface %q, not %q",
			deviceAddress, dev.InterfaceID, interfaceID)
	}
	ch := dev.Channel(channelAddress)
	if ch == nil {
		return fmt.Errorf("mqtt_sink: unknown channel %s on %s", channelAddress, centralName)
	}
	if ch.MasterParameter(parameter) == nil {
		return fmt.Errorf("mqtt_sink: parameter %q not in MASTER paramset of %s", parameter, channelAddress)
	}
	pv, err := hmtypes.NewParamValue(value)
	if err != nil {
		return fmt.Errorf("mqtt_sink: value normalisation for %q: %w", parameter, err)
	}
	return ch.Set(ctx, hmenum.ParamsetKeyMaster, parameter, pv, device.SetOptions{
		Optimistic: true,
		Priority:   priority,
	})
}

// SetSysvar looks up the named sysvar on the target central and
// dispatches via its writer.
func (s *MQTTCommandSink) SetSysvar(ctx context.Context, centralName, name string, value any) error {
	c, ok := s.registry.Get(centralName)
	if !ok {
		return fmt.Errorf("mqtt_sink: unknown central %q", centralName)
	}
	sv, ok := c.HubModel.Sysvar(name)
	if !ok {
		return fmt.Errorf("mqtt_sink: unknown sysvar %q on %s", name, centralName)
	}
	pv, err := hmtypes.NewParamValue(value)
	if err != nil {
		return fmt.Errorf("mqtt_sink: value normalisation: %w", err)
	}
	return sv.Set(ctx, pv)
}

// TriggerProgram executes the named program on the target central.
func (s *MQTTCommandSink) TriggerProgram(ctx context.Context, centralName, id string) error {
	c, ok := s.registry.Get(centralName)
	if !ok {
		return fmt.Errorf("mqtt_sink: unknown central %q", centralName)
	}
	p, ok := c.HubModel.Program(id)
	if !ok {
		return fmt.Errorf("mqtt_sink: unknown program %q on %s", id, centralName)
	}
	return p.Execute(ctx)
}

// SetProgramEnabled toggles a program's CCU-side active flag. A
// deactivated program ignores its triggers, so this is the control that
// decides whether [MQTTCommandSink.TriggerProgram] can do anything.
func (s *MQTTCommandSink) SetProgramEnabled(ctx context.Context, centralName, id string, enabled bool) error {
	c, ok := s.registry.Get(centralName)
	if !ok {
		return fmt.Errorf("mqtt_sink: unknown central %q", centralName)
	}
	p, ok := c.HubModel.Program(id)
	if !ok {
		return fmt.Errorf("mqtt_sink: unknown program %q on %s", id, centralName)
	}
	return p.SetEnabled(ctx, enabled)
}

// InvokeCustomDP implements [mqtt.CDPInvocationSink]. It delegates to
// the embedded [CustomDPDispatcher] using the source tag
// "mqtt:custom-dp:invoke" for the audit log.
//
// The `central` argument is accepted for signature consistency with the
// MQTT topic structure but is not forwarded to the dispatcher: the
// dispatcher walks all centrals in the registry to find the device,
// matching the same resolution strategy used by the REST handler.
func (s *MQTTCommandSink) InvokeCustomDP(
	ctx context.Context,
	_ string, // central — resolved by dispatcher via registry walk
	deviceAddress, name, operation string,
	params map[string]any,
	priority hmenum.CommandPriority,
) error {
	if s.cdpDispatch == nil {
		return errors.New("mqtt_sink: CDP dispatcher not wired")
	}
	return s.cdpDispatch.InvokeCustomDP(ctx, deviceAddress, name, operation, params, priority, "mqtt:custom-dp:invoke")
}

// InvokeChannelService implements [mqtt.CDPInvocationSink]. It looks
// up the channel's custom DP on the target central and calls
// `Source.Invoke(ctx, method, params, priority)` directly. ADR 0009.
func (s *MQTTCommandSink) InvokeChannelService(
	ctx context.Context,
	centralName, interfaceID, deviceAddress string, channel int,
	method string, params map[string]any,
	priority hmenum.CommandPriority,
) error {
	_ = interfaceID // resolution is via model registry, not (iface, addr)
	c, ok := s.registry.Get(centralName)
	if !ok {
		return fmt.Errorf("mqtt_sink: unknown central %q", centralName)
	}
	dev, ok := c.ModelRegistry.Get(deviceAddress)
	if !ok || dev == nil {
		return fmt.Errorf("mqtt_sink: unknown device %q on %s", deviceAddress, centralName)
	}
	chAddr := fmt.Sprintf("%s:%d", deviceAddress, channel)
	ch := dev.Channel(chAddr)
	if ch == nil {
		return fmt.Errorf("mqtt_sink: unknown channel %s on %s", chAddr, centralName)
	}
	cdp := ch.CustomDataPoint()
	if cdp == nil {
		return fmt.Errorf("mqtt_sink: no custom DP attached to channel %s", chAddr)
	}
	type invoker interface {
		Invoke(ctx context.Context, name string, params map[string]any, priority hmenum.CommandPriority) error
	}
	src, ok := cdp.(invoker)
	if !ok {
		return fmt.Errorf("mqtt_sink: custom DP on %s does not expose Invoke", chAddr)
	}
	return src.Invoke(ctx, method, params, priority)
}

// SetScheduleSwitch implements [mqtt.ScheduleSwitchSink]. Resolves the
// non-climate ProfileDataPoint on the (central, iface, addr, channel)
// tuple and calls SetScheduleEnabled, which writes COMBINED_PARAMETER
// to the CCU.
func (s *MQTTCommandSink) SetScheduleSwitch(
	ctx context.Context,
	centralName, interfaceID, deviceAddress string, channel int,
	key string, enabled bool,
	priority hmenum.CommandPriority,
) error {
	_ = interfaceID
	c, ok := s.registry.Get(centralName)
	if !ok {
		return fmt.Errorf("mqtt_sink: unknown central %q", centralName)
	}
	dev, ok := c.ModelRegistry.Get(deviceAddress)
	if !ok || dev == nil {
		return fmt.Errorf("mqtt_sink: unknown device %q on %s", deviceAddress, centralName)
	}
	chAddr := fmt.Sprintf("%s:%d", deviceAddress, channel)
	ch := dev.Channel(chAddr)
	if ch == nil {
		return fmt.Errorf("mqtt_sink: unknown channel %s on %s", chAddr, centralName)
	}
	wp := ch.WeekProfile()
	if wp == nil {
		return fmt.Errorf("mqtt_sink: no WeekProfile on channel %s", chAddr)
	}
	return wp.SetScheduleEnabled(ctx, key, enabled, priority)
}

// SetCombinedTimerSeconds implements [mqtt.CombinedDPSink]. It resolves
// the channel's attached combined Timer DP on the target central and
// calls Timer.SetDuration(seconds * time.Second). Only the "duration"
// kind is wired today — other combined-DP kinds (e.g. HSColor,
// LevelCombined) are silently dropped with a debug log so that
// unsupported MQTT commands from HA produce no error-log noise.
func (s *MQTTCommandSink) SetCombinedTimerSeconds(
	ctx context.Context,
	centralName, interfaceID, deviceAddress string, channel int,
	kind string, seconds float64,
	priority hmenum.CommandPriority,
) error {
	_ = interfaceID
	if kind != "duration" {
		slog.Default().DebugContext(
			ctx, "mqtt_sink: combined-DP kind not yet implemented, skipping",
			slog.String("kind", kind),
			slog.String("central", centralName),
			slog.String("device", deviceAddress),
			slog.Int("channel", channel),
		)
		return nil
	}
	c, ok := s.registry.Get(centralName)
	if !ok {
		return fmt.Errorf("mqtt_sink: unknown central %q", centralName)
	}
	dev, ok := c.ModelRegistry.Get(deviceAddress)
	if !ok || dev == nil {
		return fmt.Errorf("mqtt_sink: unknown device %q on %s", deviceAddress, centralName)
	}
	chAddr := fmt.Sprintf("%s:%d", deviceAddress, channel)
	ch := dev.Channel(chAddr)
	if ch == nil {
		return fmt.Errorf("mqtt_sink: unknown channel %s on %s", chAddr, centralName)
	}
	for _, cdp := range ch.CombinedDataPoints() {
		timer, ok := cdp.(*combined.Timer)
		if !ok {
			continue
		}
		dur := time.Duration(seconds * float64(time.Second))
		return timer.SetDuration(ctx, dur, priority)
	}
	return fmt.Errorf("mqtt_sink: no combined Timer on channel %s", chAddr)
}

// ActivateInstallMode implements [mqtt.InstallModeSink]. It activates
// pairing/install mode on the named interface's install-mode data point.
// seconds <= 0 selects the model's default pairing window (60s). Mirrors
// the REST `POST /install-mode/interfaces` activation path so both
// surfaces share the same backend behaviour.
func (s *MQTTCommandSink) ActivateInstallMode(ctx context.Context, centralName, interfaceID string, seconds int) error {
	c, ok := s.registry.Get(centralName)
	if !ok {
		return fmt.Errorf("mqtt_sink: unknown central %q", centralName)
	}
	for _, dp := range c.HubModel.InstallModeDPs() {
		if dp == nil || dp.InterfaceID != interfaceID {
			continue
		}
		if seconds <= 0 {
			return dp.Press(ctx)
		}
		return dp.Enable(ctx, time.Duration(seconds)*time.Second)
	}
	return fmt.Errorf("mqtt_sink: no install-mode interface %q on %s", interfaceID, centralName)
}

// errNoWriter is re-exported as ErrNoWriter via devices.go.
var _ = errors.New
