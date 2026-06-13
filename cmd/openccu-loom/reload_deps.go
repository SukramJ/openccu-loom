// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
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
	reseed  atomic.Pointer[mqttReseedHook]
}

// mqttReseedHook re-publishes the full MQTT snapshot (Discovery
// configs + availability + per-DP slot state) for every known
// device. It wraps [adapter.EventBridge.PublishInitialSnapshot] so
// the reload handler can re-seed a freshly-swapped bridge without
// importing the adapter package. Boxed in a struct because
// [atomic.Pointer] needs a pointer element type.
type mqttReseedHook struct {
	fn func(context.Context)
}

// newReloadDeps returns an empty bag.
func newReloadDeps() *reloadDeps { return &reloadDeps{} }

// SetMQTTReseed installs the snapshot re-seed hook. Called once at
// daemon boot with the EventBridge's PublishInitialSnapshot. A
// supervisor swap rebuilds the MQTT bridge from scratch, which
// resets the bridge's Discovery cache and slot-state to empty; the
// reload handler invokes this hook after a successful enable-swap so
// the new bridge re-emits the snapshot the boot path emits — without
// it, enabling HA discovery (or any MQTT edit) at runtime publishes
// nothing until a full daemon restart. A nil fn clears the slot.
func (d *reloadDeps) SetMQTTReseed(fn func(context.Context)) {
	if d == nil {
		return
	}
	if fn == nil {
		d.reseed.Store(nil)
		return
	}
	d.reseed.Store(&mqttReseedHook{fn: fn})
}

// MQTTReseed returns the installed re-seed function, or nil when none
// has been bound yet (pre-boot, MQTT-less builds, or nil deps).
func (d *reloadDeps) MQTTReseed() func(context.Context) {
	if d == nil {
		return nil
	}
	if h := d.reseed.Load(); h != nil {
		return h.fn
	}
	return nil
}

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
