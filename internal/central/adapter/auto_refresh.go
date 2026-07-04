// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// configPendingSettleDelay is the carenz that elapses between
// CONFIG_PENDING True→False and the targeted MASTER read. The CCU's
// interface daemon needs a moment to update its on-disk file cache
// with the just-resynced device state; reading earlier would race
// with that update and force a fresh radio validation. 10s is large
// enough that the file cache is stable, small enough that the
// daemon's MQTT/UI surface sees the new values inside a human
// reaction time.
const configPendingSettleDelay = 10 * time.Second

// isHmIPInterface reports whether iface belongs to the HmIP product
// family. The HmIP-RF interface serves both HmIP-RF and HmIP-Wired
// devices on the CCU and signals a completed MASTER write through the
// CONFIG_PENDING True→False transition; it does not need a
// MasterPoller. Classic HM interfaces (BidCos-RF, BidCos-Wired,
// VirtualDevices, CUxD) do not emit CONFIG_PENDING reliably and use a
// post-write poll instead.
func isHmIPInterface(iface hmenum.Interface) bool {
	return iface == hmenum.InterfaceHmIPRF
}

// wireConfigPendingHook installs the CONFIG_PENDING True→False handler on
// the central's EventCoordinator. Only HmIP (RF + Wired) channels emit
// CONFIG_PENDING reliably; classic HM devices use the MasterPoller path
// instead. The hook discriminates by interface.
//
// What runs after a CONFIG_PENDING settle:
//
//   - week-profile reload (1× per unique attached climate profile,
//     gated on the profile already holding a schedule); naive
//     per-channel reload would trigger redundant set/get-Schedule
//     round-trips on climate groups that span multiple channels.
//   - after [configPendingSettleDelay] (10 s carenz so the CCU's file
//     cache has stabilised) a targeted getParamset(MASTER) per channel
//     of the affected device, with the result persisted into the
//     [sqlite.MasterValuesStore]. The carenz is what makes the read
//     duty-cycle-neutral: by then the interface daemon's sync state
//     is "ok" and the read is served from its on-disk file cache
//     without forcing a fresh radio validation. Without the cache
//     write, subsequent cold-boots would re-issue this read for every
//     channel of every device at hydration time and burn the
//     duty-cycle budget; with it, cold boots skip the RPC entirely.
//   - visibility re-apply for CHANNEL_OPERATION_MODE.
//
// `getter`, `masterValues` or `centralName` may be nil/empty — the
// targeted MASTER refresh then becomes a no-op (week-profile reload
// still runs).
//
// `ctx` is the central's teardown-bounded bring-up context: the
// per-settle refresh goroutine derives its own timeout from it, so a
// central teardown / re-init cancels an in-flight refresh instead of
// letting it linger. The goroutine body runs under [SafeGo] so a panic
// in the week-profile reload / MASTER refresh is contained (logged with
// a stack trace) rather than crashing the whole daemon.
//
// Must be called once per central, after the EventCoordinator exists.
// It is idempotent (installs a new closure each call; callers must not
// call it more than once per central).
func wireConfigPendingHook(
	ctx context.Context,
	unit *central.Unit,
	masterValues *sqlite.MasterValuesStore,
	centralName string,
	getterFor func(interfaceID string) backends.MasterGetter,
	logger *slog.Logger,
) {
	if unit == nil || unit.Events == nil {
		return
	}
	unit.Events.SetOnConfigSettled(func(interfaceID, deviceAddress string) {
		iface := hmenum.Interface(interfaceID)
		if !isHmIPInterface(iface) {
			return
		}
		dev, ok := unit.ModelRegistry.Get(deviceAddress)
		if !ok || dev == nil {
			return
		}
		SafeGo("auto_refresh.config_pending."+dev.Address, func() {
			ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()

			// Week-profile reload (unique per device, has_schedule-gated).
			seen := make(map[*weekprofile.ClimateProfile]struct{})
			for _, ch := range dev.Channels() {
				wp := ch.WeekProfile()
				if wp == nil {
					continue
				}
				cp := wp.Climate()
				if cp == nil {
					continue
				}
				if _, dup := seen[cp]; dup {
					continue
				}
				seen[cp] = struct{}{}
				if _, err := cp.Current(); err != nil {
					continue
				}
				if _, err := cp.Load(ctx); err != nil && logger != nil {
					logger.Debug("auto_refresh.config_pending.week_profile.load",
						slog.String("device", dev.Address),
						slog.String("err", err.Error()))
				}
			}

			visibility.ApplyChannelOperationModeGatingDevice(dev)

			// Targeted MASTER refresh after the carenz, into SQLite.
			if masterValues == nil || centralName == "" || getterFor == nil {
				return
			}
			getter := getterFor(interfaceID)
			if getter == nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(configPendingSettleDelay):
			}
			refreshDeviceMasterCache(ctx, dev, interfaceID, centralName, getter, masterValues, logger)
		})
	})
}

// refreshDeviceMasterCache reads MASTER for every channel of dev and
// upserts the values into the cache. Per-channel errors are logged
// and skipped; one failing channel must not abort the rest.
func refreshDeviceMasterCache(
	ctx context.Context,
	dev *device.Device,
	interfaceID, centralName string,
	getter backends.MasterGetter,
	store *sqlite.MasterValuesStore,
	logger *slog.Logger,
) {
	channels := dev.Channels()
	applied := 0
	for _, ch := range channels {
		if ch == nil {
			continue
		}
		values, err := getter.GetParamset(ctx, ch.Address, hmenum.ParamsetKeyMaster)
		if err != nil {
			if logger != nil {
				logger.Debug("auto_refresh.config_pending.master.get_err",
					slog.String("channel", ch.Address),
					slog.String("err", err.Error()))
			}
			continue
		}
		applyMasterValuesToChannel(ch, values)
		if err := store.SaveChannel(ctx, centralName, interfaceID, ch.Address, values); err != nil && logger != nil {
			logger.Debug("auto_refresh.config_pending.master.cache_save_err",
				slog.String("channel", ch.Address),
				slog.String("err", err.Error()))
		}
		applied++
	}
	if logger != nil && applied > 0 {
		logger.Debug("auto_refresh.config_pending.master.refreshed",
			slog.String("device", dev.Address),
			slog.String("interface", interfaceID),
			slog.Int("channels_refreshed", applied))
	}
}

// applyMasterValuesToChannel writes MASTER paramset values onto the
// channel's data points via OnWireValue. Same shape as
// DevicePipeline.applyMasterValues but lives here so the
// auto_refresh path can reuse it without depending on the pipeline.
func applyMasterValuesToChannel(ch *device.Channel, values map[string]any) {
	for name, v := range values {
		dp := ch.MasterParameter(hmenum.Parameter(name))
		if dp == nil {
			continue
		}
		if setter, ok := dp.(interface{ OnWireValue(any) bool }); ok {
			setter.OnWireValue(v)
		}
	}
}

// newMasterPollerForInterface constructs a [backends.MasterPoller] for a
// classic HM interface and wires its OnRefresh callback to push fresh
// MASTER values back through the channel model via
// [device.Channel.Refresh]. Returns nil when iface is an HmIP interface
// — those use CONFIG_PENDING instead.
//
// `masterValues` / `interfaceID` / `centralName`: when all three are
// usable, refreshed MASTER values land in the persistent cache so a
// subsequent cold boot can rehydrate without an RPC.
//
// The caller is responsible for calling poller.Close() on shutdown.
func newMasterPollerForInterface(
	iface hmenum.Interface,
	unit *central.Unit,
	getter backends.MasterGetter,
	masterValues *sqlite.MasterValuesStore,
	interfaceID, centralName string,
	logger *slog.Logger,
) *backends.MasterPoller {
	if isHmIPInterface(iface) {
		return nil
	}
	poller := backends.NewMasterPoller(getter)
	poller.OnRefresh = func(addr string, key hmenum.ParamsetKey, values map[string]any) {
		deviceAddr := deviceAddressOf(addr)
		dev, ok := unit.ModelRegistry.Get(deviceAddr)
		if !ok || dev == nil {
			return
		}
		ch := dev.Channel(addr)
		if ch == nil {
			return
		}
		applyMasterValuesToChannel(ch, values)
		// Persist post-refresh values so a subsequent cold boot
		// rehydrates from disk instead of issuing getParamset(MASTER)
		// again. Only MASTER values are cached; VALUES come back from
		// the CCU through the fetch_all_device_data ReGa script.
		if key == hmenum.ParamsetKeyMaster && masterValues != nil && centralName != "" && interfaceID != "" && len(values) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := masterValues.SaveChannel(ctx, centralName, interfaceID, addr, values); err != nil && logger != nil {
				logger.Debug("master.poll.cache_save_err",
					slog.String("channel", addr),
					slog.String("err", err.Error()))
			}
		}
		// Re-evaluate operation-mode gating after the refresh in case
		// CHANNEL_OPERATION_MODE flipped between modes.
		visibility.ApplyChannelOperationModeGating(ch)
	}
	poller.OnError = func(addr string, key hmenum.ParamsetKey, err error) {
		if logger != nil {
			logger.Debug("master.poll.error",
				slog.String("addr", addr),
				slog.String("key", string(key)),
				slog.String("err", err.Error()))
		}
	}
	return poller
}
