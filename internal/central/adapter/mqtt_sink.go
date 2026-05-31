// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/combined"
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
		return fmt.Errorf("mqtt_sink: CDP dispatcher not wired")
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
// kind is wired today — other combined-DP kinds return an error so the
// caller can log the mismatch.
func (s *MQTTCommandSink) SetCombinedTimerSeconds(
	ctx context.Context,
	centralName, interfaceID, deviceAddress string, channel int,
	kind string, seconds float64,
	priority hmenum.CommandPriority,
) error {
	_ = interfaceID
	if kind != "duration" {
		return fmt.Errorf("mqtt_sink: combined-DP kind %q not supported", kind)
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

// errNoWriter is re-exported as ErrNoWriter via devices.go.
var _ = errors.New
