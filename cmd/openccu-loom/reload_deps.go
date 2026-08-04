// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"sync/atomic"

	"github.com/SukramJ/openccu-loom/internal/config"
	northbridge "github.com/SukramJ/openccu-loom/internal/north/bridge"
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

	// assemble re-derives the effective config the way boot does: the YAML
	// base with the DB-tier sections overlaid on top. curCfg alone is not
	// enough for a REST-triggered reload — it only advances on boot and on a
	// YAML file change, so a section the operator saved through the SPA (which
	// writes the DB, not the file) would be invisible and the reload would
	// rebuild the subsystem from the stale snapshot.
	assemble atomic.Pointer[configAssembler]

	// mdnsTXTRefresh re-announces the daemon-discovery mDNS TXT bundle
	// (ADR 0058: the ccus serial list and the centrals count resolve
	// and change at runtime). Stored by the REST mount once the
	// advertiser is running; invoked from the hub-ready pipeline, which
	// fires on serial resolution and live adopt. Atomic because the
	// southbound wiring subscribes before the advertiser exists.
	mdnsTXTRefresh atomic.Pointer[func()]

	// onNorthBridges is a test-only observation hook invoked once during
	// boot, after every north-bound surface has been registered on the
	// registry and before the late StartAll. It lets a wiring test assert
	// the registration-completeness + reverse-stop order (ADR 0047 §7)
	// without exposing the registry through the production return path. Nil
	// in production (set only by the guard test). Read once on the boot
	// goroutine, so it needs no atomic.
	onNorthBridges func(*northbridge.Registry)
}

// configAssembler boxes the effective-config assembly function so it can live
// in an [atomic.Pointer], which needs a pointer element type.
type configAssembler struct {
	fn func(context.Context) (*config.Config, error)
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

// SetConfigAssembler records the function that re-derives the effective
// config (YAML base + DB-tier sections). The daemon wires it once the config
// store exists; until then reload consumers fall back to [CurrentConfig].
func (d *reloadDeps) SetConfigAssembler(fn func(context.Context) (*config.Config, error)) {
	if d == nil || fn == nil {
		return
	}
	d.assemble.Store(&configAssembler{fn: fn})
}

// AssembleConfig re-derives the effective config so a reload sees section
// edits the SPA persisted to the database since boot. It falls back to the
// last recorded snapshot when no assembler is wired (direct daemonServe
// callers and tests) or when the assembly fails — a reload from a slightly
// stale config beats refusing to reload at all. The bool reports whether the
// returned config was freshly assembled, which the caller logs so a silent
// fallback is never mistaken for a fresh read.
func (d *reloadDeps) AssembleConfig(ctx context.Context) (*config.Config, bool) {
	if d == nil {
		return nil, false
	}
	if a := d.assemble.Load(); a != nil && a.fn != nil {
		if cfg, err := a.fn(ctx); err == nil && cfg != nil {
			return cfg, true
		}
	}
	return d.curCfg.Load(), false
}
