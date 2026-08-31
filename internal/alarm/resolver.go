// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"context"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/alarm/outputs"
	"github.com/SukramJ/openccu-loom/internal/central"
	lightcdp "github.com/SukramJ/openccu-loom/internal/model/custom/light"
	sirencdp "github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	switchcdp "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/safety"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// deviceResolver maps enrolled output rows onto their live custom
// data points through the central registry (multi-CCU: every lookup
// is central-scoped).
type deviceResolver struct {
	reg *central.Registry
}

// channel resolves the raw channel of an output target.
func (r *deviceResolver) channel(centralName, channelAddress string) (*device.Channel, error) {
	u, ok := r.reg.Get(centralName)
	if !ok {
		return nil, fmt.Errorf("alarm: unknown central %q", centralName)
	}
	ch := u.GetChannel(channelAddress)
	if ch == nil {
		return nil, fmt.Errorf("alarm: unknown channel %q on %q", channelAddress, centralName)
	}
	return ch, nil
}

// Siren implements outputs.DeviceResolver.
func (r *deviceResolver) Siren(centralName, channelAddress string) (outputs.SirenDevice, error) {
	ch, err := r.channel(centralName, channelAddress)
	if err != nil {
		return nil, err
	}
	if s, ok := ch.CustomDataPoint().(*sirencdp.Siren); ok {
		return s, nil
	}
	return nil, fmt.Errorf("alarm: channel %q is not a siren", channelAddress)
}

// SmokeSounder implements outputs.DeviceResolver.
func (r *deviceResolver) SmokeSounder(centralName, channelAddress string) (outputs.SmokeSounderDevice, error) {
	ch, err := r.channel(centralName, channelAddress)
	if err != nil {
		return nil, err
	}
	if s, ok := ch.CustomDataPoint().(*sirencdp.SmokeSiren); ok {
		return s, nil
	}
	return nil, fmt.Errorf("alarm: channel %q is not a smoke-detector sounder", channelAddress)
}

// Actuator implements outputs.DeviceResolver: switch and dimmer
// channels both back the switched-siren and alarm-light classes.
func (r *deviceResolver) Actuator(centralName, channelAddress string) (outputs.ActuatorDevice, error) {
	ch, err := r.channel(centralName, channelAddress)
	if err != nil {
		return nil, err
	}
	switch dev := ch.CustomDataPoint().(type) {
	case *switchcdp.Switch:
		return switchActuator{dev}, nil
	case *lightcdp.Light:
		return lightActuator{dev}, nil
	default:
		return nil, fmt.Errorf("alarm: channel %q is not a switch or dimmer", channelAddress)
	}
}

// Sound implements outputs.DeviceResolver.
func (r *deviceResolver) Sound(centralName, channelAddress string) (outputs.SoundDevice, error) {
	ch, err := r.channel(centralName, channelAddress)
	if err != nil {
		return nil, err
	}
	if sp, ok := ch.CustomDataPoint().(*sirencdp.SoundPlayer); ok {
		return soundAdapter{sp}, nil
	}
	return nil, fmt.Errorf("alarm: channel %q is not a sound player", channelAddress)
}

// switchActuator adapts the switch CDP onto the actuator port.
type switchActuator struct {
	dev *switchcdp.Switch
}

func (a switchActuator) TurnOnBounded(ctx context.Context, d time.Duration, _ *float64, priority hmenum.CommandPriority) error {
	return a.dev.TurnOnFor(ctx, d, priority)
}

func (a switchActuator) TurnOnSteady(ctx context.Context, _ *float64, priority hmenum.CommandPriority) error {
	return a.dev.TurnOn(ctx, priority)
}

func (a switchActuator) TurnOff(ctx context.Context, priority hmenum.CommandPriority) error {
	return a.dev.TurnOff(ctx, priority)
}

func (a switchActuator) IsOn() (on, observed bool) { return a.dev.IsOn() }

// lightActuator adapts the dimmer/light CDP onto the actuator port:
// the auto-off travels in the same atomic bundle as the level.
type lightActuator struct {
	dev *lightcdp.Light
}

func (a lightActuator) TurnOnBounded(ctx context.Context, d time.Duration, level *float64, priority hmenum.CommandPriority) error {
	return a.dev.TurnOnWith(ctx, lightcdp.OnConfig{Brightness: level, OnTime: &d}, priority)
}

func (a lightActuator) TurnOnSteady(ctx context.Context, level *float64, priority hmenum.CommandPriority) error {
	return a.dev.TurnOnWith(ctx, lightcdp.OnConfig{Brightness: level}, priority)
}

func (a lightActuator) TurnOff(ctx context.Context, priority hmenum.CommandPriority) error {
	return a.dev.TurnOff(ctx, priority)
}

func (a lightActuator) IsOn() (on, observed bool) { return a.dev.IsOn() }

// soundAdapter adapts the MP3 player CDP onto the chirp port.
type soundAdapter struct {
	dev *sirencdp.SoundPlayer
}

func (a soundAdapter) PlayChirp(ctx context.Context, soundfileIndex int, volume float64, priority hmenum.CommandPriority) error {
	return a.dev.PlaySound(ctx, sirencdp.PlayConfig{
		SoundfileIndex: soundfileIndex,
		Volume:         volume,
		Duration:       2 * time.Second,
	}, priority)
}

func (a soundAdapter) Stop(ctx context.Context, priority hmenum.CommandPriority) error {
	return a.dev.StopSound(ctx, priority)
}

// sensorReader implements engine.SensorReader: restore-time fresh
// value reads through the cached channel model.
type sensorReader struct {
	reg *central.Registry
}

// CurrentActive implements engine.SensorReader.
func (r *sensorReader) CurrentActive(_ context.Context, row sqlitestore.AlarmSensorRow) (active, known bool) {
	u, ok := r.reg.Get(row.CentralName)
	if !ok {
		return false, false
	}
	ch := u.GetChannel(row.ChannelAddress)
	if ch == nil {
		return false, false
	}
	p := ch.Parameter(hmenum.Parameter(row.Parameter))
	if p == nil {
		return false, false
	}
	raw, ok := p.RawValue()
	if !ok {
		return false, false
	}
	// Restore must reach the same verdict as the live event path, or a
	// sensor reads active while running and inactive after a restart.
	// Both resolve through [safety.ActiveFromRaw]; the row carries the
	// config, so no service state is needed here.
	cfg, err := engine.ParseSensorConfig(row.ConfigJSON)
	if err != nil {
		// An unparsable config falls back to the default rule, the same
		// way the routing index does.
		active, known, _ = safety.ActiveFromRaw(nil, raw, nil)
		return active, known
	}
	active, known, _ = safety.ActiveFromRaw(cfg.ActiveValues, raw, p.ParameterData().ValueList)
	return active, known
}
