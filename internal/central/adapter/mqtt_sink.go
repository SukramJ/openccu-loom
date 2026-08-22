// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	paramcoerce "github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/internal/payload"
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
	// labels resolves a localised selection back to its wire token. Nil
	// leaves every command untouched, which is the pre-localisation
	// behaviour and always correct.
	labels mqtt.ValueListLabeler
}

// WithSelectionLabeler installs the labeler used to turn a localised
// choice back into a wire token. Returns the receiver for fluent wiring.
func (s *MQTTCommandSink) WithSelectionLabeler(labeler mqtt.ValueListLabeler) *MQTTCommandSink {
	s.labels = labeler
	return s
}

// NewMQTTCommandSink constructs the adapter.
func NewMQTTCommandSink(r *central.Registry, w ValueWriter) *MQTTCommandSink {
	return &MQTTCommandSink{
		registry:    r,
		writer:      w,
		cdpDispatch: NewCustomDPDispatcher(r),
	}
}

// canonicalChannelAddress maps a topic-derived channel address onto the
// model's canonical spelling. The naming layer upper-cases every address in
// the MQTT topic path, but XML-RPC addresses are case-sensitive — the
// virtual remote ("HmIP-RCV-1") is the one mixed-case address in a CCU's
// inventory, and writing with the upper-cased form faults with
// "Invalid device". Every entry point that receives a channel address
// parsed out of an MQTT topic routes through this helper; addresses the
// model does not know pass through unchanged.
func (s *MQTTCommandSink) canonicalChannelAddress(centralName, channelAddress string) string {
	if s.registry == nil {
		return channelAddress
	}
	deviceAddr := channelAddress
	suffix := ""
	if i := strings.LastIndexByte(channelAddress, ':'); i > 0 {
		deviceAddr, suffix = channelAddress[:i], channelAddress[i:]
	}
	units := s.registry.List()
	if centralName != "" {
		if c, ok := s.registry.Get(centralName); ok && c != nil {
			units = []*central.Unit{c}
		}
	}
	for _, c := range units {
		if c == nil || c.ModelRegistry == nil {
			continue
		}
		// Fast path: the address is already canonical.
		if _, ok := c.ModelRegistry.Get(deviceAddr); ok {
			return channelAddress
		}
		for _, d := range c.ModelRegistry.List() {
			if d != nil && strings.EqualFold(d.Address, deviceAddr) {
				return d.Address + suffix
			}
		}
	}
	return channelAddress
}

// SetValue routes the DP command to the addressed channel's writer.
//
// The channel is resolved in the central's model first, so the command
// travels through [device.Channel.Writer] — the writer that enforces the
// operator channel lock. Writing straight through the raw value writer let
// an MQTT VALUES command switch a channel the operator had locked, while
// REST, WS and the SPA all refused it.
//
// A channel the model does not know (an address that never materialised as a
// device, an unregistered central) falls back to the raw writer, which is the
// behaviour every such command had before: no model entry means no lock to
// enforce, and the write is the CCU's to accept or reject.
func (s *MQTTCommandSink) SetValue(
	ctx context.Context, centralName, interfaceID, channelAddress string,
	parameter hmenum.Parameter, value any, priority hmenum.CommandPriority,
) error {
	if s.writer == nil {
		return ErrNoWriter
	}
	channelAddress = s.canonicalChannelAddress(centralName, channelAddress)
	if ch, err := s.resolveChannel(centralName, interfaceID, channelAddress); err == nil && ch != nil {
		// Coerce the descriptor-blind topic payload against the resolved
		// parameter's descriptor before it reaches the wire, mirroring the
		// REST PUT /value path (PutDataPointValue in
		// internal/north/rest/handlers/devices.go). Without it an ENUM select
		// write forwards the option label (e.g. "DIGITAL_OUTPUT") verbatim
		// while the CCU expects the integer index, and a whole-number FLOAT
		// lands as an int with no MIN/MAX check. A rejection here (out of
		// range, not in VALUE_LIST, wrong type) never reaches the wire.
		if dp := ch.Parameter(parameter); dp != nil {
			pv, cerr := paramcoerce.Coerce(dp.ParameterData(), value)
			if cerr != nil {
				return fmt.Errorf("mqtt_sink: coerce %q on %s: %w", parameter, channelAddress, cerr)
			}
			value = pv.Unwrap()
		}
		if w := ch.Writer(); w != nil {
			return w.SetValue(ctx, channelAddress, parameter, value, priority)
		}
		// The channel exists but carries no writer (hydration has not
		// reached it yet). Its lock still governs the command.
		if ch.IsLocked() {
			return device.ErrChannelOperationLocked
		}
	}
	return s.writer.SetValue(ctx, centralName, interfaceID, channelAddress, parameter, value, priority)
}

// resolveChannel maps a topic-derived (central, interface, channel address)
// tuple onto the model channel that owns it. The channel address must already
// be canonical — call [MQTTCommandSink.canonicalChannelAddress] first.
//
// Returns a descriptive error for every stage that cannot be resolved so
// callers can surface which half of the tuple was wrong.
func (s *MQTTCommandSink) resolveChannel(
	centralName, interfaceID, channelAddress string,
) (*device.Channel, error) {
	if s.registry == nil {
		return nil, errors.New("mqtt_sink: no central registry wired")
	}
	c, ok := s.registry.Get(centralName)
	if !ok {
		return nil, fmt.Errorf("mqtt_sink: unknown central %q", centralName)
	}
	deviceAddress := channelAddress
	if i := strings.LastIndexByte(channelAddress, ':'); i > 0 {
		deviceAddress = channelAddress[:i]
	}
	dev, ok := c.ModelRegistry.Get(deviceAddress)
	if !ok || dev == nil {
		return nil, fmt.Errorf("mqtt_sink: unknown device %q on %s", deviceAddress, centralName)
	}
	// When the caller supplies an interface hint, verify the resolved
	// device actually belongs to that interface. Within a single central
	// the model registry is keyed on device address so there can only
	// ever be one entry — the check still guards against an operator
	// misconfiguration where the same address is used on two interfaces.
	if interfaceID != "" && dev.InterfaceID != "" && dev.InterfaceID != interfaceID {
		return nil, fmt.Errorf("mqtt_sink: device %q belongs to interface %q, not %q",
			deviceAddress, dev.InterfaceID, interfaceID)
	}
	ch := dev.Channel(channelAddress)
	if ch == nil {
		return nil, fmt.Errorf("mqtt_sink: unknown channel %s on %s", channelAddress, centralName)
	}
	return ch, nil
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
	channelAddress = s.canonicalChannelAddress(centralName, channelAddress)
	ch, err := s.resolveChannel(centralName, interfaceID, channelAddress)
	if err != nil {
		return err
	}
	mp := ch.MasterParameter(parameter)
	if mp == nil {
		return fmt.Errorf("mqtt_sink: parameter %q not in MASTER paramset of %s", parameter, channelAddress)
	}
	// Coerce against the MASTER descriptor and validate — the same
	// descriptor-aware path the REST PUT /value handler takes. The previous
	// hmtypes.NewParamValue guess was descriptor-blind (a whole-number "21"
	// for a FLOAT became int64(21)) and Channel.Set was called without
	// Validate, so no MIN/MAX/enum check ran on the configuration write.
	pv, err := paramcoerce.Coerce(mp.ParameterData(), value)
	if err != nil {
		return fmt.Errorf("mqtt_sink: coerce %q on %s: %w", parameter, channelAddress, err)
	}
	return ch.Set(ctx, hmenum.ParamsetKeyMaster, parameter, pv, device.SetOptions{
		Validate:   true,
		Optimistic: true,
		Priority:   priority,
		Source:     "mqtt:command",
	})
}

// SetSysvar looks up the named sysvar on the target central and
// dispatches via its writer.
//
// `name` arrives as the MQTT topic segment, which is TopicSafe-escaped
// — a sysvar named `Außen Temperatur` reaches us as
// `Außen_Temperatur`. Resolution therefore goes through
// [hub.Hub.SysvarByTopicSegment], which tries the exact name first and
// falls back to the unique sysvar whose escaped name matches, so the
// CCU-side write still carries the real name.
func (s *MQTTCommandSink) SetSysvar(ctx context.Context, centralName, name string, value any) error {
	c, ok := s.registry.Get(centralName)
	if !ok {
		return fmt.Errorf("mqtt_sink: unknown central %q", centralName)
	}
	sv, ok := c.HubModel.SysvarByTopicSegment(name)
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
	deviceAddress = s.canonicalChannelAddress("", deviceAddress)
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
	deviceAddress = s.canonicalChannelAddress(centralName, deviceAddress)
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
	params = s.resolveSelectionLabels(ch, cdp, params)
	return src.Invoke(ctx, method, params, priority)
}

// resolveSelectionLabels turns a localised choice back into the wire
// token the device speaks.
//
// The discovery payload shows an operator labels for the lists a custom
// data point declares localisable, so Home Assistant hands that label
// back on the command topic while the domain resolves selections by
// exact VALUE_LIST match. Without this the write silently does nothing —
// a translated tone selector that never changes the tone.
//
// A token that is already on the list passes through untouched, so the
// raw form stays valid: an automation written against FREQUENCY_RISING
// keeps working, and one written against the label works too.
func (s *MQTTCommandSink) resolveSelectionLabels(
	ch *device.Channel, cdp any, params map[string]any,
) map[string]any {
	decl, ok := cdp.(payload.LocalisableSelections)
	if !ok || len(params) == 0 || ch == nil {
		return params
	}
	vl := s.labels
	if vl == nil {
		return params
	}
	// params is decoded fresh per command, so mutating it in place is
	// safe and avoids copying on every siren write.
	out := params
	for _, sel := range decl.LocalisableSelections() {
		given, isString := out[sel.ArgKey].(string)
		if !isString || given == "" {
			continue
		}
		dp := ch.Parameter(hmenum.Parameter(sel.Parameter))
		if dp == nil {
			continue
		}
		values := dp.ParameterData().ValueList
		if slices.Contains(values, given) {
			continue // already a wire token
		}
		labels := vl.ValueListLabels(ch.Type, sel.Parameter, values)
		idx := slices.Index(labels, given)
		if idx < 0 || idx >= len(values) {
			continue
		}
		out[sel.ArgKey] = values[idx]
	}
	return out
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
	deviceAddress = s.canonicalChannelAddress(centralName, deviceAddress)
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

// SetCombinedValue implements [mqtt.CombinedDPSink]. It resolves the
// combined data point whose projection claims `kind` on the target
// channel and hands it the raw payload to parse.
//
// Dispatch runs through [payload.CombinedProjection] /
// [payload.CombinedWritable] rather than a per-type branch, so a new
// writable combined DP is reachable from MQTT the moment it declares its
// projection. A kind that resolves to a read-only projection is an
// error, not a silent drop: its command topic is never advertised, so a
// write arriving there means something published to a topic nobody
// offered.
func (s *MQTTCommandSink) SetCombinedValue(
	ctx context.Context,
	centralName, interfaceID, deviceAddress string, channel int,
	kind, raw string,
	priority hmenum.CommandPriority,
) error {
	_ = interfaceID
	if kind == "" {
		return errors.New("mqtt_sink: combined write without a kind")
	}
	c, ok := s.registry.Get(centralName)
	if !ok {
		return fmt.Errorf("mqtt_sink: unknown central %q", centralName)
	}
	deviceAddress = s.canonicalChannelAddress(centralName, deviceAddress)
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
		proj, ok := cdp.(payload.CombinedProjection)
		if !ok || proj.CombinedKind() != kind {
			continue
		}
		writable, ok := proj.(payload.CombinedWritable)
		if !ok {
			return fmt.Errorf("mqtt_sink: combined %q on channel %s is read-only", kind, chAddr)
		}
		return writable.WriteCombined(ctx, raw, priority)
	}
	return fmt.Errorf("mqtt_sink: no combined %q on channel %s", kind, chAddr)
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
