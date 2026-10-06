// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"log/slog"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// mqttMaintenanceHooks are the daemon-side halves of the MQTT maintenance
// topics (mqtt-smarthome 2.0 §7, ADR 0083). The bridge's shared instance
// publisher parses the commands; what a command does is the daemon's.
type mqttMaintenanceHooks struct {
	// setLogLevel applies `maintenance/set/loglevel`: the root level of the
	// log-level registry, per-path overrides untouched, not persisted.
	setLogLevel func(slog.Level)
	// supervised is the predicate the REST restart endpoint is mounted
	// behind. A restart without a supervisor is a stop, so it is refused.
	supervised func() bool
	// restart is the REST restart's own path: the 30 s latch, the audit
	// entry and the SIGTERM.
	restart func()
}

// newMQTTMaintenanceHooks binds the hooks to the daemon's log-level registry,
// its supervisor detection and its audit recorder.
//
// It deliberately does not use the deployment kind: ADR 0081 says the kind
// names where the daemon runs, while `OPENCCU_LOOM_SUPERVISOR` and the other
// signals [detectSupervisedRestart] reads say whether something restarts it.
func newMQTTMaintenanceHooks(levels *hmlog.LevelRegistry, rec audit.Recorder) mqttMaintenanceHooks {
	h := mqttMaintenanceHooks{
		supervised: detectSupervisedRestart,
		restart: func() {
			handlers.RequestRestart(rec, mqttMaintenanceRestartUser)
		},
	}
	if levels != nil {
		h.setLogLevel = levels.SetDefault
	}
	return h
}

// mqttMaintenanceRestartUser attributes an MQTT-requested restart in the
// audit trail. The broker carries no identity, so the surface is the actor.
const mqttMaintenanceRestartUser = "mqtt:maintenance/set/restart"

// config projects the hooks and the operator's settings onto the bridge's
// [mqtt.MaintenanceConfig].
func (h mqttMaintenanceHooks) config(m config.NorthMQTTMaintenance) mqtt.MaintenanceConfig {
	return mqtt.MaintenanceConfig{
		Disabled:      !m.IsEnabled(),
		StatsInterval: hapublisher.StatsInterval(m.EffectiveStatsIntervalSeconds()),
		SetLogLevel:   h.setLogLevel,
		Supervised:    h.supervised,
		Shutdown:      h.restart,
	}
}

// warnNonConformantTopicBase logs, once at start, that a topic base spanning
// several levels (`home/loom`) runs outside mqtt-smarthome 2.0 §3, which
// requires `<name>` to be one level: the daemon keeps the configured base
// verbatim (ADR 0083), and a tool scanning `+/info` cannot see it.
func warnNonConformantTopicBase(cfg *config.Config, logger *slog.Logger) {
	if cfg == nil || !cfg.North.MQTT.Enabled || mqtt.TopicBaseConformant(cfg.North.MQTT.TopicBase) {
		return
	}
	logger.Warn("mqtt.topic_base.non_conformant",
		slog.String("topic_base", cfg.North.MQTT.TopicBase),
		slog.String("effect", "the base spans several topic levels, which mqtt-smarthome 2.0 does not allow; "+
			"the daemon keeps it, but a tool scanning +/info for instances cannot see this one"))
}
