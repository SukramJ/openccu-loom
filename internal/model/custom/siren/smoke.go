// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package siren

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// SmokeAlarmStatus enumerates the possible SMOKE_DETECTOR_ALARM_STATUS values
// reported by HmIP-SWSD-class devices.
type SmokeAlarmStatus string

// SmokeAlarmStatus values.
const (
	SmokeStatusIdleOff        SmokeAlarmStatus = "IDLE_OFF"
	SmokeStatusPrimaryAlarm   SmokeAlarmStatus = "PRIMARY_ALARM"
	SmokeStatusSecondaryAlarm SmokeAlarmStatus = "SECONDARY_ALARM"
	SmokeStatusIntrusion      SmokeAlarmStatus = "INTRUSION_ALARM"
)

// SmokeSiren is the HmIP-SWSD smoke detector with built-in siren.
type SmokeSiren struct {
	custom.BaseDP

	Address string

	// ServiceRegistry implements the write-half of [payload.Source].
	// Service methods are registered in [NewSmokeSiren].
	payload.ServiceRegistry

	// dataVersion tracks the per-cluster monotonic counter (Matter
	// §10.6.5). SmokeCOAlarm has no client-writable attributes / commands
	// in 0.1.0; the field is reserved for when SelfTestRequest is wired.
	dataVersion hmtypes.DataVersionTracker

	key    hmtypes.DataPointKey
	writer custom.Writer
	// status carries SMOKE_DETECTOR_ALARM_STATUS, a read-only ENUM the
	// resolver projects onto a raw-index Sensor[int32]; the string label is
	// resolved on read via [custom.EnumLabelValue].
	status  *generic.Sensor[int32]
	command *generic.Sensor[string]
}

// DataPointKey returns the composite identifier for this custom data
// point. Satisfies [device.AttachableDataPoint] so the materializer
// can attach the SmokeSiren to a channel.
func (s *SmokeSiren) DataPointKey() hmtypes.DataPointKey { return s.key }

// Category reports the HA data-point category — clients spawn the
// entity off this value (siren platform).
func (s *SmokeSiren) Category() hmenum.DataPointCategory { return hmenum.DataPointCategorySiren }

// Subscribe satisfies [device.SubscribingDataPoint]. SmokeSiren has
// no hot-path aggregate cache to hydrate — Status() / IsActive() /
// IsPrimaryAlarm() etc. read directly from the embedded wire DPs,
// and the EventBridge's publishCustomDPState path fires the
// aggregate snapshot on every wire-side change. Returns a no-op
// closure so the SubscribingDataPoint contract is satisfied and the
// channel records an OnAnyUpdate hook for the bridge to re-fire on
// wire updates.
func (s *SmokeSiren) Subscribe(ch *device.Channel) func() {
	if ch == nil {
		return func() {}
	}
	var unsubs []func()
	if s.status != nil {
		unsubs = append(unsubs, s.status.OnAnyUpdate(func(_, _ any) {}))
	}
	if s.command != nil {
		unsubs = append(unsubs, s.command.OnAnyUpdate(func(_, _ any) {}))
	}
	return func() {
		for _, u := range unsubs {
			if u != nil {
				u()
			}
		}
	}
}

// SmokeSirenConfig is the constructor record.
type SmokeSirenConfig struct {
	Channel *device.Channel
	Writer  custom.Writer
}

// NewSmokeSiren constructs a SmokeSiren.
func NewSmokeSiren(cfg SmokeSirenConfig) *SmokeSiren {
	addr := ""
	var key hmtypes.DataPointKey
	if cfg.Channel != nil {
		addr = cfg.Channel.Address
		if dev := cfg.Channel.Device(); dev != nil {
			key = hmtypes.DataPointKey{
				InterfaceID:    dev.InterfaceID,
				ChannelAddress: addr,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(hmenum.ParameterSmokeDetectorAlarmStatus),
			}
		}
	}
	s := &SmokeSiren{
		Address: addr,
		key:     key,
		writer:  cfg.Writer,
		status:  custom.EnumSensorField(cfg.Channel, hmenum.ParameterSmokeDetectorAlarmStatus),
		command: custom.StringSensorField(cfg.Channel, hmenum.ParameterSmokeDetectorCommand),
	}
	s.registerSmokeSirenServices()
	// Matter §10.6.5: DataVersion advances on every CCU-confirmed attribute change.
	if s.status != nil {
		_ = s.status.OnConfirmedUpdate(func(_, _ int32) { s.dataVersion.Bump() })
	}
	if s.command != nil {
		_ = s.command.OnConfirmedUpdate(func(_, _ string) { s.dataVersion.Bump() })
	}
	return s
}

// Status returns the last observed alarm status. SMOKE_DETECTOR_ALARM_STATUS
// is a read-only ENUM projected onto a raw-index sensor, so the index is
// resolved to its VALUE_LIST label before being returned as a SmokeAlarmStatus.
func (s *SmokeSiren) Status() (SmokeAlarmStatus, bool) {
	label, ok := custom.EnumLabelValue(s.status)
	if !ok {
		return "", false
	}
	return SmokeAlarmStatus(label), true
}

// IsActive reports whether the siren is currently firing — i.e. the status is
// anything other than IDLE_OFF.
func (s *SmokeSiren) IsActive() (active, observed bool) {
	st, ok := s.Status()
	if !ok {
		return false, false
	}
	return st != SmokeStatusIdleOff && st != "", true
}

// IsPrimaryAlarm reports whether the device itself is alarming
// (smoke detected). PRIMARY_ALARM is set by the source detector.
func (s *SmokeSiren) IsPrimaryAlarm() bool {
	st, _ := s.Status()
	return st == SmokeStatusPrimaryAlarm
}

// IsSecondaryAlarm reports whether the device is in slave-alarm
// mode (a peer is alarming and broadcast it).
func (s *SmokeSiren) IsSecondaryAlarm() bool {
	st, _ := s.Status()
	return st == SmokeStatusSecondaryAlarm
}

// IsIntrusion reports whether the device is in INTRUSION_ALARM mode.
func (s *SmokeSiren) IsIntrusion() bool {
	st, _ := s.Status()
	return st == SmokeStatusIntrusion
}

// IsStateChange reports whether triggering or silencing the siren constitutes
// a state change relative to the last observed status. Returns true when the
// siren has not yet been observed (first command always goes through).
func (s *SmokeSiren) IsStateChange(turnOn bool) bool {
	active, observed := s.IsActive()
	if !observed {
		return true
	}
	return active != turnOn
}

// AvailableLights returns nil — the SmokeSiren (HmIP-SWSD) has no
// configurable optical alarm selection.
func (s *SmokeSiren) AvailableLights() []string { return nil }

// AvailableTones returns nil — the SmokeSiren (HmIP-SWSD) has no configurable
// acoustic tone selection; it fires its built-in alarm unconditionally.
func (s *SmokeSiren) AvailableTones() []string { return nil }

// TurnOn sets SMOKE_DETECTOR_COMMAND = INTRUSION_ALARM, triggering the
// secondary alarm pattern across all peer devices.
func (s *SmokeSiren) TurnOn(ctx context.Context, priority hmenum.CommandPriority) error {
	if s.writer == nil {
		return errors.New("smokesiren: writer required")
	}
	if err := s.writer.SetValue(custom.EnsureContext(ctx), s.Address, hmenum.ParameterSmokeDetectorCommand, "INTRUSION_ALARM", priority); err != nil {
		return fmt.Errorf("smokesiren: COMMAND=INTRUSION: %w", err)
	}
	return nil
}

// TurnOff sets SMOKE_DETECTOR_COMMAND = INTRUSION_ALARM_OFF, clearing the
// secondary alarm pattern.
func (s *SmokeSiren) TurnOff(ctx context.Context, priority hmenum.CommandPriority) error {
	if s.writer == nil {
		return errors.New("smokesiren: writer required")
	}
	if err := s.writer.SetValue(custom.EnsureContext(ctx), s.Address, hmenum.ParameterSmokeDetectorCommand, "INTRUSION_ALARM_OFF", priority); err != nil {
		return fmt.Errorf("smokesiren: COMMAND=OFF: %w", err)
	}
	return nil
}
