// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package siren implements the siren custom data point. The
// per-channel state DPs (ACOUSTIC_ALARM_ACTIVE / SELECTION,
// OPTICAL_ALARM_ACTIVE / SELECTION) are typed references to the
// channel's existing generic data points — the [Siren] type does not
// duplicate them. Siren layers the start/stop command semantics plus
// the optional DURATION pair on top.
package siren

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ErrNotSupported is returned when a capability-gated method is invoked
// on a siren whose capability profile does not advertise it.
var ErrNotSupported = errors.New("siren: operation not supported")

// Writer is an alias for [generic.Writer].
type Writer = generic.Writer

// Siren bundles the optical/acoustic channels plus commands that emit
// start/stop on both simultaneously.
type Siren struct {
	custom.BaseDP

	Address      string
	Capabilities custom.SirenCapabilities

	// ServiceRegistry implements the write-half of [payload.Source].
	// Service methods are registered in [New].
	payload.ServiceRegistry

	// dataVersion tracks the per-cluster monotonic counter (Matter
	// §10.6.5). Bumped on every successful MatterWrite / MatterInvoke
	// so DataVersionFilter evaluation correctly detects cluster changes.
	dataVersion hmtypes.DataVersionTracker

	key    hmtypes.DataPointKey
	writer Writer

	acousticActive *generic.BinarySensor
	// acousticIdx / opticalIdx are the alarm-selection parameters. They are
	// write-only ENUMs on the wire (OPERATIONS=2), so the resolver builds an
	// ActionSelect for them and the device never reports a value: what they
	// carry is the selection this daemon last sent, plus the VALUE_LIST and
	// DEFAULT the CCU declares. TurnOff needs that DEFAULT to name the value
	// that silences the device.
	acousticIdx   *generic.ActionSelect
	opticalActive *generic.BinarySensor
	opticalIdx    *generic.ActionSelect

	// duration is the combined DURATION_VALUE + DURATION_UNIT pair
	// exposed as a single seconds-as-float DP. Created when the
	// channel carries both wire parameters; nil otherwise.
	duration *combined.Timer

	// availableTones / availableLights are the labels exposed by the underlying
	// SELECTION DPs' VALUE_LIST. Captured at construction time and read by
	// [AvailableTones] / [AvailableLights].
	availableTones  []string
	availableLights []string
}

// DataPointKey returns the composite identifier for this custom data
// point. Satisfies [device.AttachableDataPoint] so the materializer
// can attach the Siren to a channel.
func (s *Siren) DataPointKey() hmtypes.DataPointKey { return s.key }

// Category reports the HA data-point category — clients spawn the
// entity off this value (siren platform).
func (s *Siren) Category() hmenum.DataPointCategory { return hmenum.DataPointCategorySiren }

// Config is the constructor record. Channel must already carry the
// per-channel ACOUSTIC_ALARM_* and OPTICAL_ALARM_* DPs; absent fields
// degrade to "(zero, false)" on the corresponding accessors and are
// silently skipped by [TurnOn] / [TurnOff].
type Config struct {
	Channel      *device.Channel
	Writer       Writer
	Capabilities custom.SirenCapabilities
	// Group is the rebased channel-group schema of the profile that
	// materialised this data point. Every composed field resolves through
	// it — see [custom.ResolveSlotOr]. The zero value is valid: each
	// binding falls back to the parameter named at the call site.
	Group custom.RebasedChannelGroupConfig
}

// New constructs a Siren.
func New(cfg Config) *Siren {
	address := ""
	var key hmtypes.DataPointKey
	if cfg.Channel != nil {
		address = cfg.Channel.Address
		if dev := cfg.Channel.Device(); dev != nil {
			key = hmtypes.DataPointKey{
				InterfaceID:    dev.InterfaceID,
				ChannelAddress: address,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(hmenum.ParameterAcousticAlarmActive),
			}
		}
	}
	s := &Siren{
		Address:        address,
		Capabilities:   cfg.Capabilities,
		key:            key,
		writer:         cfg.Writer,
		acousticActive: custom.BinarySensorField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldAcousticAlarmActive, hmenum.ParameterAcousticAlarmActive)),
		acousticIdx:    custom.ActionSelectField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldAcousticAlarmSelection, hmenum.ParameterAcousticAlarmSelection)),
		opticalActive:  custom.BinarySensorField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldOpticalAlarmActive, hmenum.ParameterOpticalAlarmActive)),
		opticalIdx:     custom.ActionSelectField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldOpticalAlarmSelection, hmenum.ParameterOpticalAlarmSelection)),
	}
	if cfg.Channel != nil {
		if dp := cfg.Channel.Parameter(hmenum.ParameterAcousticAlarmSelection); dp != nil {
			s.availableTones = append([]string(nil), dp.ParameterData().ValueList...)
		}
		if dp := cfg.Channel.Parameter(hmenum.ParameterOpticalAlarmSelection); dp != nil {
			s.availableLights = append([]string(nil), dp.ParameterData().ValueList...)
		}
		// Combined DURATION = DURATION_VALUE (INTEGER) + DURATION_UNIT (ENUM
		// 0=s, 1=m, 2=h). Exposed as a single "seconds" DP. Attach via
		// Channel.AttachCalculatedDataPoint so the channel's
		// SubscribingDataPoint hook wires the OnAnyUpdate listeners onto
		// the two wire DPs.
		if cfg.Channel.Parameter(hmenum.ParameterDurationValue) != nil &&
			cfg.Channel.Parameter(hmenum.ParameterDurationUnit) != nil &&
			cfg.Writer != nil {
			s.duration = combined.NewTimer(
				address, cfg.Writer,
				hmenum.ParameterDurationValue,
				hmenum.ParameterDurationUnit,
			)
			if dev := cfg.Channel.Device(); dev != nil {
				s.duration.InterfaceID = dev.InterfaceID
			}
			cfg.Channel.AttachCalculatedDataPoint(s.duration)
		}
	}
	s.registerSirenServices()
	// Matter §10.6.5: DataVersion advances on every CCU-confirmed attribute change.
	if s.acousticActive != nil {
		_ = s.acousticActive.OnConfirmedUpdate(func(_, _ bool) { s.dataVersion.Bump() })
	}
	if s.acousticIdx != nil {
		_ = s.acousticIdx.OnConfirmedUpdate(func(_, _ int32) { s.dataVersion.Bump() })
	}
	if s.opticalActive != nil {
		_ = s.opticalActive.OnConfirmedUpdate(func(_, _ bool) { s.dataVersion.Bump() })
	}
	if s.opticalIdx != nil {
		_ = s.opticalIdx.OnConfirmedUpdate(func(_, _ int32) { s.dataVersion.Bump() })
	}
	return s
}

// DisableAcousticLabel names the acoustic selection that silences this
// siren, and whether the device declares one at all.
//
// It is the value any write that must not start a tone has to carry:
// an ASIR takes tone, pattern and duration in one atomic VALUES
// paramset and ignores partial writes, so leaving the acoustic half
// out of an optical-only write does not keep it quiet — it re-sends
// whatever the device selected last.
//
// The resolution order is the one [sirenSelectionDefaultString]
// documents: the declared DEFAULT first, the VALUE_LIST head only as
// its fallback. Callers outside this package have no view of the
// descriptor — the flattened [Siren.AvailableTones] projection loses
// the DEFAULT — so the rule has to be answered here rather than
// re-derived from a list position.
//
// The second fallback covers a profile whose alarm-selection field
// slot resolves onto a different channel than the one carrying the
// VALUE_LIST: the selection data point is then nil while the channel
// still offers tones, and the head is the only disable candidate left.
func (s *Siren) DisableAcousticLabel() (string, bool) {
	if label := sirenSelectionDefaultString(s.acousticIdx); label != "" {
		return label, true
	}
	if len(s.availableTones) > 0 {
		return s.availableTones[0], true
	}
	return "", false
}

// AvailableTones returns the labels of acoustic-alarm selections this
// siren accepts (e.g. "DISABLE_ACOUSTIC_SIGNAL", "FREQUENCY_RISING",
// "FREQUENCY_FALLING").
// The returned slice is a copy — callers may mutate freely.
func (s *Siren) AvailableTones() []string {
	if s.availableTones == nil {
		return nil
	}
	return append([]string(nil), s.availableTones...)
}

// AvailableLights returns the labels of optical-alarm selections this
// siren accepts (e.g. "DISABLE_OPTICAL_SIGNAL", "BLINKING_RED").
func (s *Siren) AvailableLights() []string {
	if s.availableLights == nil {
		return nil
	}
	return append([]string(nil), s.availableLights...)
}

// --- accessors ---

// AcousticState reports the current acoustic alarm state and selection label.
// The state comes from ACOUSTIC_ALARM_ACTIVE, which the device reports. The
// selection is the label this daemon last sent (e.g. "FREQUENCY_RISING"):
// ACOUSTIC_ALARM_SELECTION is write-only, so the CCU never reports one back
// and it is empty until the first TurnOn / TurnOff.
func (s *Siren) AcousticState() (active bool, selection string, observed bool) {
	a, aOK := readBool(s.acousticActive)
	i, iOK := readSelection(s.acousticIdx)
	return a, i, aOK || iOK
}

// OpticalState reports the current optical alarm state and selection label.
// See [AcousticState] for where each half comes from.
func (s *Siren) OpticalState() (active bool, selection string, observed bool) {
	a, aOK := readBool(s.opticalActive)
	i, iOK := readSelection(s.opticalIdx)
	return a, i, aOK || iOK
}

// IsRefreshed reports whether at least one of the siren's wire slots
// (acoustic active/selection, optical active/selection) has been observed.
//
// Each concrete pointer is tested on its own, for the same reason
// [Siren.Subscribe] does it that way: a nil *generic.ActionSelect handed
// to an `interface{ IsRefreshed() bool }` parameter is a non-nil
// interface holding a nil pointer, so the nil check inside the helper
// never fires and the call dereferences nothing.
func (s *Siren) IsRefreshed() bool {
	if s.acousticActive != nil && s.acousticActive.IsRefreshed() {
		return true
	}
	if s.acousticIdx != nil && s.acousticIdx.IsRefreshed() {
		return true
	}
	if s.opticalActive != nil && s.opticalActive.IsRefreshed() {
		return true
	}
	return s.opticalIdx != nil && s.opticalIdx.IsRefreshed()
}

// IsActive reports whether any alarm channel is currently firing.
func (s *Siren) IsActive() (active, observed bool) {
	acoustic, aOK := readBool(s.acousticActive)
	optical, oOK := readBool(s.opticalActive)
	return acoustic || optical, aOK || oOK
}

// IsStateChange reports whether turning the siren on or off constitutes a
// state change relative to the last observed state. Returns true when the
// siren has not yet been observed (first command always goes through).
// Mirrors the CustomDataPoint.is_state_change pattern where state_uncertain
// always returns true.
func (s *Siren) IsStateChange(turnOn bool) bool {
	active, observed := s.IsActive()
	if !observed {
		return true
	}
	return active != turnOn
}

// ErrInvalidTone is returned by [ValidateTone] when the requested
// acoustic-alarm tone is not in the device's available-tones list.
var ErrInvalidTone = errors.New("siren: tone not in available tones list")

// ValidateTone reports whether tone is in the siren's available-tones
// list. Returns nil when the list is empty (no capability restriction)
// or when tone is found. Returns [ErrInvalidTone] otherwise.
//
// Called by TurnOn before dispatching the wire write so an invalid tone
// label is rejected locally rather than triggering a CCU fault.
func (s *Siren) ValidateTone(tone string) error {
	if len(s.availableTones) == 0 {
		return nil
	}
	if slices.Contains(s.availableTones, tone) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidTone, tone)
}

// --- commands ---

// TurnOn fires both channels according to cfg. When AcousticSelection or
// OpticalSelection is nil, the current device value or declared default for
// the corresponding alarm-selection DP is used as a fallback — matching the
// reference pattern where every TurnOn sends a concrete selection string. The
// DURATION pair is included when cfg.Duration > 0; when 0 and the device
// has a declared duration default, that default is sent instead.
//
// Acoustic + optical + duration parameters are batched through one PutParamset
// when the underlying writer supports it.
//
// A [generic.CallParameterCollector] is attached to ctx for
// forward-compatible batching.
func (s *Siren) TurnOn(ctx context.Context, cfg OnConfig, priority hmenum.CommandPriority) (err error) {
	ctx = custom.EnsureContext(ctx)
	if s.writer != nil {
		coll := generic.NewCollector(generic.WriterAsBackend(s.writer), generic.WithPriority(priority))
		ctx = generic.ContextWithCollector(ctx, coll)
		// Anything staged on the collector only reaches the wire in the
		// flush, so its error is part of this command's result.
		defer func() { err = generic.FlushCollector(ctx, coll, err) }()
	}
	params := make(map[hmenum.Parameter]any, 5)
	if s.Capabilities.SupportsDuration {
		if cfg.Duration > 0 {
			value, unit := custom.EncodeTimerDuration(cfg.Duration)
			params[hmenum.ParameterDurationUnit] = unit
			params[hmenum.ParameterDurationValue] = value
		} else if s.duration != nil {
			// Send duration default so the device uses its declared on-time rather
			// than inheriting whatever was set by the previous command.
			dv, du := custom.EncodeTimerDuration(s.duration.DefaultDuration())
			params[hmenum.ParameterDurationUnit] = du
			params[hmenum.ParameterDurationValue] = dv
		}
	}
	if s.Capabilities.SupportsAcoustic {
		var sel string
		if cfg.AcousticSelection != nil {
			sel = *cfg.AcousticSelection
			// Optional validation against the available tone list.
			if cfg.AcousticTone != "" {
				if err := s.ValidateTone(cfg.AcousticTone); err != nil {
					return err
				}
			}
		} else {
			// Fall back to the last observed value, then to the declared default.
			sel = sirenSelectionCurrentOrDefault(s.acousticIdx)
		}
		if sel != "" {
			params[hmenum.ParameterAcousticAlarmSelection] = sel
		}
	}
	if s.Capabilities.SupportsOptical {
		var sel string
		if cfg.OpticalSelection != nil {
			sel = *cfg.OpticalSelection
		} else {
			sel = sirenSelectionCurrentOrDefault(s.opticalIdx)
		}
		if sel != "" {
			params[hmenum.ParameterOpticalAlarmSelection] = sel
		}
	}
	if len(params) == 0 {
		return nil
	}
	if err := custom.PutOrSet(ctx, s.writer, s.Address, hmenum.ParamsetKeyValues, params, priority); err != nil {
		return fmt.Errorf("siren: TurnOn: %w", err)
	}
	if s.Capabilities.SupportsAcoustic {
		if sel, ok := params[hmenum.ParameterAcousticAlarmSelection].(string); ok {
			writeBool(s.acousticActive, true)
			recordSelection(s.acousticIdx, sel)
		}
	}
	if s.Capabilities.SupportsOptical {
		if sel, ok := params[hmenum.ParameterOpticalAlarmSelection].(string); ok {
			writeBool(s.opticalActive, true)
			recordSelection(s.opticalIdx, sel)
		}
	}
	return nil
}

// TurnOff silences both channels. Sends the declared default for each
// alarm-selection parameter and, when duration is supported, also sends the
// default DURATION pair — matching the reference pattern that always flushes
// defaults to clear any previously set timer. Atomic where possible.
//
// A [generic.CallParameterCollector] is attached to ctx for
// forward-compatible batching.
func (s *Siren) TurnOff(ctx context.Context, priority hmenum.CommandPriority) (err error) {
	ctx = custom.EnsureContext(ctx)
	if s.writer != nil {
		coll := generic.NewCollector(generic.WriterAsBackend(s.writer), generic.WithPriority(priority))
		ctx = generic.ContextWithCollector(ctx, coll)
		// Anything staged on the collector only reaches the wire in the
		// flush, so its error is part of this command's result.
		defer func() { err = generic.FlushCollector(ctx, coll, err) }()
	}
	params := make(map[hmenum.Parameter]any, 4)
	if s.Capabilities.SupportsAcoustic {
		v := sirenSelectionDefaultString(s.acousticIdx)
		params[hmenum.ParameterAcousticAlarmSelection] = v
	}
	if s.Capabilities.SupportsOptical {
		v := sirenSelectionDefaultString(s.opticalIdx)
		params[hmenum.ParameterOpticalAlarmSelection] = v
	}
	if s.Capabilities.SupportsDuration && s.duration != nil {
		dv, du := custom.EncodeTimerDuration(s.duration.DefaultDuration())
		params[hmenum.ParameterDurationUnit] = du
		params[hmenum.ParameterDurationValue] = dv
	}
	if len(params) == 0 {
		return nil
	}
	if err := custom.PutOrSet(ctx, s.writer, s.Address, hmenum.ParamsetKeyValues, params, priority); err != nil {
		return fmt.Errorf("siren: TurnOff: %w", err)
	}
	if s.Capabilities.SupportsAcoustic {
		writeBool(s.acousticActive, false)
		recordSelection(s.acousticIdx, sirenSelectionDefaultString(s.acousticIdx))
	}
	if s.Capabilities.SupportsOptical {
		writeBool(s.opticalActive, false)
		recordSelection(s.opticalIdx, sirenSelectionDefaultString(s.opticalIdx))
	}
	return nil
}

// OnConfig bundles optional fields TurnOn understands.
type OnConfig struct {
	Duration time.Duration
	// AcousticSelection is the string label of the desired tone
	// (e.g. "FREQUENCY_RISING", "FREQUENCY_FALLING"). The label is sent
	// directly to the CCU as a DpActionSelect string value.
	// Use [Siren.AvailableTones] to enumerate valid labels.
	AcousticSelection *string
	// AcousticTone is kept for API compatibility with callers that prefer
	// a secondary validation path. When both AcousticSelection and
	// AcousticTone are set, AcousticSelection is sent on the wire and
	// AcousticTone validates against [AvailableTones].
	// Empty means no secondary validation.
	AcousticTone string
	// OpticalSelection is the string label of the desired optical signal
	// (e.g. "BLINKING_RED"). See AcousticSelection.
	OpticalSelection *string
}

func readBool(dp *generic.BinarySensor) (value, observed bool) {
	if dp == nil {
		return false, false
	}
	return dp.Value()
}

func writeBool(dp *generic.BinarySensor, v bool) {
	if dp == nil {
		return
	}
	dp.OnEvent(v)
}

func readSelection(dp *generic.ActionSelect) (string, bool) {
	if dp == nil {
		return "", false
	}
	return dp.Label()
}

func recordSelection(dp *generic.ActionSelect, label string) {
	if dp == nil {
		return
	}
	dp.RecordLabel(label)
}

// sirenSelectionDefaultString returns the effective disable-label for the
// given alarm-selection data point. Resolution order:
//  1. Declared DEFAULT from the CCU paramset description.
//  2. First entry in the VALUE_LIST — on real HmIP-ASIR hardware this is
//     conventionally the "disable" label (e.g. "DISABLE_ACOUSTIC_SIGNAL").
//  3. Empty string as last resort (e.g. no VALUE_LIST, no default).
func sirenSelectionDefaultString(dp *generic.ActionSelect) string {
	if dp == nil {
		return ""
	}
	label, ok := dp.DefaultLabel()
	if !ok {
		return ""
	}
	return label
}

// sirenSelectionCurrentOrDefault returns the last observed value for the given
// alarm-selection DP, or falls back to the declared default when no value has
// been observed yet.
func sirenSelectionCurrentOrDefault(dp *generic.ActionSelect) string {
	if dp == nil {
		return ""
	}
	if v, ok := dp.Label(); ok && v != "" {
		return v
	}
	return sirenSelectionDefaultString(dp)
}

// Subscribe wires the channel's siren state parameters into the Siren
// so that CCU pushes feed through the EventBridge's
// publishCustomDPState path. Siren has no hot-path aggregate cache —
// each accessor reads directly from the embedded wire DP — so the
// OnAnyUpdate hooks have no-op bodies. They only need to exist so
// the channel records an OnAnyUpdate registration the bridge can
// re-fire on every wire-side change.
//
// IMPORTANT: each concrete pointer field is checked individually (not
// via an interface wrapper) to avoid the typed-nil-via-interface
// pitfall — a nil *generic.BinarySensor wrapped in a non-nil
// interface would pass an `interface{} != nil` test and panic on
// dispatch.
//
// Implements [device.SubscribingDataPoint].
func (s *Siren) Subscribe(ch *device.Channel) func() {
	if ch == nil {
		return func() {}
	}
	var unsubs []func()
	if s.acousticActive != nil {
		unsubs = append(unsubs, s.acousticActive.OnAnyUpdate(func(_, _ any) {}))
	}
	if s.acousticIdx != nil {
		unsubs = append(unsubs, s.acousticIdx.OnAnyUpdate(func(_, _ any) {}))
	}
	if s.opticalActive != nil {
		unsubs = append(unsubs, s.opticalActive.OnAnyUpdate(func(_, _ any) {}))
	}
	if s.opticalIdx != nil {
		unsubs = append(unsubs, s.opticalIdx.OnAnyUpdate(func(_, _ any) {}))
	}
	return func() {
		for _, u := range unsubs {
			if u != nil {
				u()
			}
		}
	}
}
