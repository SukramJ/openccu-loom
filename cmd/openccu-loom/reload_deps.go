// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"sync/atomic"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// reloadDeps is the late-bound reference bag the config-watcher's
// reload handler consults to mutate live subsystems. The
// composition root (daemonServe) constructs the relevant subsystems
// well after the watcher itself; reloadDeps bridges the gap
// without resorting to globals or a re-ordering of the daemon
// startup sequence.
//
// Every field is atomically settable so the watcher can fire
// concurrently with daemon boot — until the corresponding setter
// is called the reload handler treats that subsystem as not yet
// ready and logs the diff as deferred.
type reloadDeps struct {
	mqttSup atomic.Pointer[mqttSupervisor]
	curCfg  atomic.Pointer[config.Config]
}

// newReloadDeps returns an empty bag.
func newReloadDeps() *reloadDeps { return &reloadDeps{} }

// SetMQTTSupervisor installs the MQTT supervisor. May be called
// exactly once during daemon boot. A nil supervisor clears the
// slot (used by tests).
func (d *reloadDeps) SetMQTTSupervisor(s *mqttSupervisor) {
	if d == nil {
		return
	}
	d.mqttSup.Store(s)
}

// MQTTSupervisor returns the currently-installed MQTT supervisor,
// or nil when none has been bound yet.
func (d *reloadDeps) MQTTSupervisor() *mqttSupervisor {
	if d == nil {
		return nil
	}
	return d.mqttSup.Load()
}

// SetCurrentConfig records the freshest [*config.Config] snapshot. The
// daemon writes the boot config here and the config-watcher updates
// it after every successful hot-reload, so REST consumers reading
// CurrentConfig always see the same view the running daemon sees.
func (d *reloadDeps) SetCurrentConfig(c *config.Config) {
	if d == nil {
		return
	}
	d.curCfg.Store(c)
}

// CurrentConfig returns the freshest [*config.Config] snapshot, or
// nil when none has been recorded yet (i.e. pre-boot or nil deps).
func (d *reloadDeps) CurrentConfig() *config.Config {
	if d == nil {
		return nil
	}
	return d.curCfg.Load()
}
