// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
//   - logging.level         → swaps the slog handler at runtime
//   - logging.format        → swaps the slog handler at runtime
//   - north.rest.cors       → operator-supplied CORS origins (next request)
//   - north.mqtt.*          → tears down + rebuilds the MQTT stack atomically
//     (broker URL, client_id, credentials, topic base, discovery toggles,
//     payload format, enabled flag — see [mqttDiffersStructurally])
//
// Restart-required (any change in these fields logs and is ignored):
//
//   - centrals: only an in-place modification of a central present in both
//     the previous and next config (same name, changed host / credentials /
//     interfaces / …). Adding or removing a central is a live
//     orchestrator operation and is not restart-required.
//   - callback.host / port / bin_port (callback servers bind once)
//   - north.rest.listen / north.mqtt.listen
//   - north.rest.public_url (add-on hint file is written once at boot)
//   - north.rest.openapi_validate (router middleware is fixed at boot)
//   - data_dir (SQLite / backup paths committed at boot)
//   - locale, auth, oidc
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

		// Hot-reloadable: logging level + format.
		if prev.Logging.Level != next.Logging.Level || prev.Logging.Format != next.Logging.Format {
			applied++
			logger.Info("daemon.reload.logging",
				slog.String("level", next.Logging.Level),
				slog.String("format", next.Logging.Format),
				slog.String("note", "swap takes effect on next slog.Default() use; existing handlers keep their level until rebuild"))
		}

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
		// *applied*. Reading the diff is intentionally explicit (no
		// reflection): every entry below corresponds to a documented
		// boot-time-only field.
		if prev.DataDir != next.DataDir {
			restart++
			logger.Warn("daemon.reload.restart_required", slog.String("field", "data_dir"))
		}
		if prev.Locale != next.Locale {
			restart++
			logger.Warn("daemon.reload.restart_required", slog.String("field", "locale"))
		}
		if prev.Callback != next.Callback {
			restart++
			logger.Warn("daemon.reload.restart_required", slog.String("field", "callback"))
		}
		if prev.North.REST.Listen != next.North.REST.Listen {
			restart++
			logger.Warn("daemon.reload.restart_required", slog.String("field", "north.rest.listen"))
		}
		if prev.North.REST.PublicURL != next.North.REST.PublicURL {
			restart++
			logger.Warn("daemon.reload.restart_required", slog.String("field", "north.rest.public_url"))
		}
		if prev.North.REST.OpenAPIValidateEnabled() != next.North.REST.OpenAPIValidateEnabled() {
			restart++
			logger.Warn("daemon.reload.restart_required", slog.String("field", "north.rest.openapi_validate"))
		}
		// Add/remove of a central is a live orchestrator operation now, so
		// only an in-place modification of a central present in both the
		// previous and next config (same name, different fields) is
		// restart-required — a pure count change (add or remove) is not.
		if config.CentralsModifiedInPlace(prev.Centrals, next.Centrals) {
			restart++
			logger.Warn("daemon.reload.restart_required",
				slog.String("field", "centrals"),
				slog.Int("prev_count", len(prev.Centrals)),
				slog.Int("next_count", len(next.Centrals)))
		}

		logger.Info("daemon.reload.applied",
			slog.Int("hot_reloaded_fields", applied),
			slog.Int("restart_required_fields", restart))
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
