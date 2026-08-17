// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// daemonServeWithReload is [daemonServe] augmented with an optional
// [config.Watcher] that polls the YAML config for edits and applies
// the **hot-reloadable subset** to a running daemon. Fields outside
// that subset are flagged with a `daemon.reload.restart_required`
// log record so operators see when a YAML edit will only take effect
// after a process restart.
//
// Hot-reloadable matrix:
//
//   - north.mqtt.*          → tears down + rebuilds the MQTT stack atomically
//     (broker URL, client_id, credentials, topic base, discovery toggles,
//     payload format, enabled flag — see [mqttDiffersStructurally])
//
// The logging block is NOT in that matrix. It used to be counted as applied
// and logged as swapped, but the logger stack is built once and installed as
// the process default, and nothing here reaches the level registry that could
// change it — so the record said "applied" and the daemon kept logging at the
// level it booted with. It is restart-required like everything else the rule
// table names; the runtime control for log levels is the diagnostics endpoint.
//
// Restart-required: everything [config.RestartRequiredDiff] reports —
// the daemon's single source of truth for "changing this needs a restart",
// shared with the REST save response and the restart-pending banner. Each
// reported path logs a `daemon.reload.restart_required` warning and is
// otherwise ignored. Keeping a second, hand-written list here is what let a
// YAML edit report success for a setting nothing applied, so the punch-list
// is derived, never enumerated.
//
// Empty configPath disables the watcher entirely (test mode + the
// `--config` un-supplied path).
func daemonServeWithReload(ctx context.Context, cfg *config.Config, configPath string, stdout, stderr io.Writer) error {
	if configPath == "" {
		return daemonServe(ctx, cfg, stdout, stderr)
	}

	// Subordinate ctx so a panic in the watcher cannot orphan the
	// daemon. The watcher itself catches its own errors via slog;
	// we only need to ensure cancellation propagation works.
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()

	logger := slog.Default()
	deps := newReloadDeps()
	w, _, err := config.NewWatcher(
		configPath,
		config.WithLogger(logger),
		config.WithHandler(hotReloadHandler(logger, deps)), //nolint:contextcheck // hotReloadHandler returns a callback with no ctx param; it creates its own timeout internally
	)
	if err != nil {
		return err
	}
	go func() {
		if rErr := w.Run(watchCtx); rErr != nil && !errors.Is(rErr, context.Canceled) {
			logger.Warn("daemon.reload.watcher_exited", slog.String("err", rErr.Error()))
		}
	}()

	return daemonServeWithDeps(ctx, cfg, stdout, stderr, deps)
}

// hotReloadHandler returns the [config.ReloadHandler] that classifies
// each detected diff into "applied" vs "restart-required" and acts on
// the hot-reloadable subset.
//
// The handler returns a non-nil error only when an applied hot-reload
// (currently: MQTT swap) fails — in that case [config.Watcher] rolls
// back to the previous config snapshot, leaving the running stack
// untouched. Restart-required diffs are logged as informational
// warnings and never abort the reload.
func hotReloadHandler(logger *slog.Logger, deps *reloadDeps) config.ReloadHandler {
	return func(prev, next *config.Config) error {
		if prev == nil || next == nil {
			return nil
		}
		// Always publish the latest snapshot so the REST trigger
		// handler can replay the current config on demand. We do
		// this before the swap so an unsuccessful swap still
		// updates downstream views — a REST reload retry should
		// race against the just-read YAML, not the prior boot.
		deps.SetCurrentConfig(next)
		applied := 0
		restart := 0

		// Hot-reloadable: MQTT. The supervisor owns the rebuild
		// sequence (new stack starts before the old one tears down,
		// rollback on connect failure). A nil supervisor means the
		// daemon boot has not yet reached the MQTT wiring step — we
		// log the diff as deferred and let the next config edit
		// re-trigger the swap.
		if mqttDiffersStructurally(prev.North.MQTT, next.North.MQTT) {
			sup := deps.MQTTSupervisor()
			if sup == nil {
				logger.Warn("daemon.reload.mqtt_deferred",
					slog.String("reason", "supervisor not yet bound"))
			} else {
				swapCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				// Swap the EFFECTIVE config, not the config file's view of it.
				// The watcher loads the YAML+env tier on its own, while
				// `north.mqtt` is a DB-tier section: every value the operator
				// saved in the SPA lives in the database and is overlaid on top
				// at boot. Rebuilding from `next` alone would silently revert
				// the running bridge to the file's broker, credentials and
				// topic base — and the next restart, which re-applies the
				// overlay, would flip it back. The REST-triggered reload
				// re-assembles for the same reason.
				swapCfg, fresh := deps.AssembleConfig(swapCtx)
				if swapCfg == nil {
					swapCfg = next
				}
				if !fresh {
					logger.Warn("daemon.reload.mqtt_config_not_reassembled",
						slog.String("effect", "swapping from the config file tier; database section edits may not be applied"))
				}
				err := sup.Swap(swapCtx, swapCfg)
				cancel()
				if err != nil {
					logger.Warn("daemon.reload.mqtt_swap_failed",
						slog.String("err", err.Error()),
						slog.String("hint", "previous MQTT stack remains active"))
					return fmt.Errorf("mqtt swap: %w", err)
				}
				applied++
				logger.Info("daemon.reload.mqtt_swapped",
					slog.String("broker", redactBrokerURL(swapCfg.North.MQTT.BrokerURL)),
					slog.Bool("enabled", swapCfg.North.MQTT.Enabled))

				// Re-seed the snapshot. Swap rebuilt the bridge from
				// scratch, so its Discovery cache and per-DP slot state
				// are empty — the boot path seeds them via
				// PublishInitialSnapshot, the runtime path must too.
				// Without this, enabling HA discovery (or any MQTT
				// edit) at runtime leaves the new bridge publishing
				// nothing until a full daemon restart. Only meaningful
				// when MQTT stays enabled; a disable-swap has no bridge.
				if swapCfg.North.MQTT.Enabled {
					if reseed := deps.MQTTReseed(); reseed != nil {
						seedCtx, seedCancel := context.WithTimeout(context.Background(), 60*time.Second)
						reseed(seedCtx)
						seedCancel()
						logger.Info("daemon.reload.mqtt_reseeded")
					}
				}
			}
		}

		// Restart-required diffs — log each so the operator has a
		// concrete punch-list of fields whose edit was *seen* but not
		// *applied*. The punch-list comes from [config.RestartRequiredDiff],
		// the same table the REST save response and the restart-pending
		// banner read. It used to be a hand-written block of seven
		// comparisons against a rule table many times that size: a
		// hand-edited config.yaml that switched the Matter bridge on or the
		// Basic-auth gate off logged "reloaded, 0 restart-required fields"
		// while neither change existed anywhere but in the file.
		pending := config.RestartRequiredDiff(prev, next)
		restart = len(pending)
		for _, field := range pending {
			logger.Warn("daemon.reload.restart_required", slog.String("field", field))
		}

		// The summary carries the field list so a single record answers
		// "what did the daemon do with my edit"; at Warn when anything is
		// pending, because an Info line reading "applied" is what an
		// operator takes as confirmation.
		summary := logger.Info
		if restart > 0 {
			summary = logger.Warn
		}
		summary("daemon.reload.applied",
			slog.Int("hot_reloaded_fields", applied),
			slog.Int("restart_required_fields", restart),
			slog.String("restart_required", strings.Join(pending, ",")))
		return nil
	}
}

// mqttDiffersStructurally reports whether prev and next differ in any
// field that requires tearing down + rebuilding the MQTT stack. Every
// settable [config.NorthMQTT] field is considered structural in the
// current design — the Bridge bakes [TopicBase] and the discovery
// toggles at construction time, and the TCPClient cannot mutate its
// broker URL / credentials live. Operators who want to flip just one
// flag still pay the cost of one full reconnect; that is acceptable
// because the typical use case (rotate broker credentials, point at a
// new broker, toggle HA discovery) is interactive and the operator
// expects the brief reconnect.
func mqttDiffersStructurally(prev, next config.NorthMQTT) bool {
	return prev != next
}
