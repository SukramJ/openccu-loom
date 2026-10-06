// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
	hagomqtt "github.com/SukramJ/go-hamqtt/publisher/gomqtt"

	"github.com/SukramJ/openccu-loom/internal/build"
)

// InstanceName is `info.name`: the Go project, not an npm package a
// management tool would offer updates for (ADR 0083 §`info`).
const InstanceName = "openccu-loom"

// MaintenanceConfig configures mqtt-smarthome 2.0 §7's maintenance topics,
// served by the shared [hapublisher.Instance]:
//
//   - `<base>/maintenance/set/loglevel` — error / warn / info / debug onto
//     SetLogLevel, not persisted;
//   - `<base>/maintenance/set/restart` — Shutdown, only while Supervised
//     answers true, refused and logged at warn otherwise. It is the one
//     `set`-shaped topic that fires on an empty payload, because the
//     management tool the convention exists for publishes it empty;
//   - `<base>/maintenance/stats` — retained process statistics every
//     StatsInterval.
//
// Anyone who may publish on the broker can use these; the broker's ACLs are
// the only gate (ADR 0083 §Security).
type MaintenanceConfig struct {
	// Disabled switches the maintenance topics off: nothing is routed,
	// nothing is published, and `info.maintenance` says false. The zero
	// value is enabled, as the spec recommends.
	Disabled bool
	// StatsInterval is the period of `maintenance/stats`. Zero means the
	// shared default (60 s); [hapublisher.StatsOff] switches it off — see
	// [hapublisher.StatsInterval] for turning an operator's seconds into it.
	StatsInterval time.Duration
	// SetLogLevel applies `maintenance/set/loglevel`. Nil refuses it.
	SetLogLevel func(slog.Level)
	// Supervised answers whether a supervisor restarts the process after a
	// clean exit. Nil, or false, refuses `maintenance/set/restart`.
	Supervised func() bool
	// Shutdown starts the graceful shutdown a restart is.
	Shutdown func()
}

// newInstance builds the shared instance publisher of this bridge:
// `<base>/info` and the maintenance topics, on the bridge's own topic base.
func newInstance(b *Bridge, logger *slog.Logger) *hapublisher.Instance {
	extra := map[string]any{
		"commit":     build.Commit,
		"build_date": build.BuildDate,
		// Resolved per publish, never captured: the document is republished
		// on every broker reconnect, and a CCU adopted at runtime must
		// appear in it without a restart.
		"centrals": infoCentrals{b: b},
	}
	m := b.cfg.Maintenance
	return hapublisher.NewInstance(
		hagomqtt.Split(b.client, lateSubscriber{b: b}),
		hapublisher.InstanceConfig{
			Layout:              bridgeStatusLayout{base: b.topics.Base},
			Name:                InstanceName,
			Version:             build.Version,
			Started:             b.cfg.StartedAt,
			Extra:               extra,
			MaintenanceDisabled: m.Disabled,
			SetLogLevel:         m.SetLogLevel,
			Supervised:          m.Supervised,
			Shutdown:            m.Shutdown,
			StatsInterval:       m.StatsInterval,
			Logger:              logger,
		},
	)
}

// infoCentrals is the `centrals` field of `<base>/info`: the centrals the
// daemon serves, resolved when the document is rendered rather than when the
// instance is built. The shared [hapublisher.InstanceConfig.Extra] is fixed
// at construction, so the value is a marshaller that asks the bridge.
type infoCentrals struct{ b *Bridge }

// MarshalJSON implements [json.Marshaler].
func (c infoCentrals) MarshalJSON() ([]byte, error) {
	names := c.b.cleanupCentralNames()
	if names == nil {
		names = []string{}
	}
	return json.Marshal(names)
}

// Instance exposes the bridge's instance publisher for the composition
// root, which registers its maintenance routes on the command plane and runs
// its statistics loop.
func (b *Bridge) Instance() *hapublisher.Instance { return b.instance }

// RunMaintenanceStats publishes `<base>/maintenance/stats` until ctx ends.
// It returns at once, with nil, when the stats are off — maintenance
// disabled or the interval switched off.
func (b *Bridge) RunMaintenanceStats(ctx context.Context) error {
	if b == nil || b.instance == nil {
		return nil
	}
	err := b.instance.RunStats(ctx)
	if errors.Is(err, hapublisher.ErrStatsOff) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
