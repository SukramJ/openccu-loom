// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// wireConfigPendingHook installs the CONFIG_PENDING True→False handler on
// the central's EventCoordinator. Only HmIP (RF + Wired) devices emit
// CONFIG_PENDING reliably; classic HM devices use the MasterPoller path
// instead.
//
// The discrimination is the device-level one from
// [hmenum.PushesConfigPendingFor] — the same verdict the REST device
// surface advertises to the SPA as `master_pushes_config_pending` —
// unioned with the interface-level [hmenum.Interface.PushesConfigPending].
// The product-group half is what lets an HmIP-flavoured device hosted on
// the VirtualDevices interface (an HmIP-HEATING group, say) reach the
// settle leg at all; the interface half keeps every device that is
// gated in today from losing it when its model name resolves to a
// classic product group.
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
	if unit == nil || unit.Events == nil || unit.ModelRegistry == nil {
		return
	}
	unit.Events.SetOnConfigSettled(func(interfaceID, deviceAddress string) {
		// interfaceID arrives in the canonical wire form
		// (`<central>-<iface>`, see [CallbackHandlers.Event]); the rest of
		// this closure keeps it that way because the backend registry is
		// keyed by it. Only the product-family discrimination needs the bare
		// interface, so derive it rather than casting the wire id — the cast
		// never matched on a named central and silenced the whole handler.
		//
		// The device is fetched before the discrimination because the
		// product group only exists on the device record. The interface
		// taken is the delivering one, not device.Interface: the backend
		// that just reported the settle is the one whose CONFIG_PENDING
		// semantics apply.
		dev, ok := unit.ModelRegistry.Get(deviceAddress)
		if !ok || dev == nil {
			return
		}
		iface := BareInterfaceFromWireID(unit.Name(), interfaceID)
		if !hmenum.PushesConfigPendingFor(iface, dev.ProductGroup) && !iface.PushesConfigPending() {
			return
		}
		SafeGo("auto_refresh.config_pending."+dev.Address, func() {
			ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()

			reloadDeviceWeekProfiles(ctx, dev, logger)

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

// reloadDeviceWeekProfiles reloads every distinct week profile of dev after a
// CONFIG_PENDING settle. Both climate and simple (non-climate) profiles are
// reloaded: a non-climate schedule write (switch / cover / light / lock) also
// settles through CONFIG_PENDING, and without the simple reload the retained
// MQTT schedule_data would stay at its boot snapshot until the daemon restarts.
// Each profile is reloaded once (dedup) and only when it has a schedule.
func reloadDeviceWeekProfiles(ctx context.Context, dev *device.Device, logger *slog.Logger) {
	seenClimate := make(map[*weekprofile.ClimateProfile]struct{})
	seenSimple := make(map[*weekprofile.DefaultProfile]struct{})
	logLoadErr := func(err error) {
		if err != nil && logger != nil {
			logger.Debug("auto_refresh.config_pending.week_profile.load",
				slog.String("device", dev.Address),
				slog.String("err", err.Error()))
		}
	}
	for _, ch := range dev.Channels() {
		wp := ch.WeekProfile()
		if wp == nil {
			continue
		}
		if cp := wp.Climate(); cp != nil {
			if _, dup := seenClimate[cp]; dup {
				continue
			}
			seenClimate[cp] = struct{}{}
			if _, err := cp.Current(); err != nil {
				continue
			}
			_, err := cp.Load(ctx)
			logLoadErr(err)
			continue
		}
		if sp := wp.Simple(); sp != nil {
			if _, dup := seenSimple[sp]; dup {
				continue
			}
			seenSimple[sp] = struct{}{}
			if _, err := sp.Current(); err != nil {
				continue
			}
			_, err := sp.Load(ctx)
			logLoadErr(err)
		}
	}
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
// [device.Channel.Refresh]. Returns nil for an interface that pushes
// CONFIG_PENDING ([hmenum.Interface.PushesConfigPending]) — those use
// the CONFIG_PENDING settle path instead.
//
// The decision is per interface because the poller is constructed once
// per interface and has no device in scope. The settle gate in
// [wireConfigPendingHook] is per device, so an HmIP-flavoured device on
// a non-pushing interface (an HmIP-HEATING group on VirtualDevices) is
// served by both paths: it keeps this interface's post-write poll and
// additionally gets the settle-driven targeted MASTER read. The cost is
// one extra cached getParamset(MASTER) per write on that device class;
// narrowing it would mean making the refresh hook per device, which is a
// device-pipeline API change rather than a classification fix.
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
	if iface.PushesConfigPending() {
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
