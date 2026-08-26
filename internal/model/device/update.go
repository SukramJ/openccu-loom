// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// FirmwareUpdater is the outbound contract Update uses to push
// firmware to the CCU. Implementations live in the client package.
type FirmwareUpdater interface {
	UpdateFirmware(ctx context.Context, deviceAddress string) error
}

// FirmwareRefresher re-reads firmware data from the CCU after an
// update so the [Firmware] snapshot re-settles.
type FirmwareRefresher interface {
	RefreshFirmwareData(ctx context.Context, deviceAddress string) error
}

// Compile-time guarantee that *Update satisfies the universal Source
// contract and the HA-Discovery payload builder contract (ADR 0010).
var (
	_ payload.Source                    = (*Update)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*Update)(nil)
)

// Update is the per-device firmware-update surface — the MVP port of
// It is created automatically
// when [Config.Updatable] is true.
type Update struct {
	device    *Device
	updater   FirmwareUpdater
	refresher FirmwareRefresher

	// svcReg provides ServiceMethodNames / Invoke for the Source contract.
	svcReg payload.ServiceRegistry

	// mu guards isRegistered.
	mu sync.RWMutex
	// isRegistered tracks whether this update entity has been registered with a
	// north-bound adapter (e.g. MQTT Discovery). Set by Register Unregister.
	isRegistered bool
}

// NewUpdate constructs an Update bound to d. Either updater or
// refresher may be nil — callers that only want the observable
// surface wire them in later.
func NewUpdate(d *Device, updater FirmwareUpdater, refresher FirmwareRefresher) *Update {
	if d == nil {
		return nil
	}
	u := &Update{device: d, updater: updater, refresher: refresher}
	u.svcReg.RegisterService("install", func(ctx context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		_, err := u.Start(ctx, nil)
		return err
	})
	return u
}

// Device returns the parent device.
func (u *Update) Device() *Device { return u.device }

// Available mirrors [Device.Availability].IsReachable().
func (u *Update) Available() bool { return u.device.availability.IsReachable() }

// Firmware returns the currently installed firmware version.
func (u *Update) Firmware() string { return u.device.firmware.Info().Current }

// FirmwareUpdateState returns the CCU-reported phase.
func (u *Update) FirmwareUpdateState() hmenum.DeviceFirmwareState {
	return u.device.firmware.Info().UpdateState
}

// InProgress reports whether an update is actively running. Only
// HmIP-RF exposes a meaningful in-progress phase; other interfaces
// always return false.
func (u *Update) InProgress() bool {
	if u.device.Interface != hmenum.InterfaceHmIPRF {
		return false
	}
	return u.FirmwareUpdateState().IsFirmwareUpdateInProgress()
}

// LatestFirmware is the version an install would target — the update
// lifecycle gating (image delivered to the device for HmIP-RF, available
// directly for BidCos) lives in [GatedLatestFirmware].
func (u *Update) LatestFirmware() string {
	return GatedLatestFirmware(u.device.Interface, u.device.firmware.Info())
}

// --- payload.Source implementation ---

// Info returns identity-level fields for the update entity.
func (u *Update) Info() payload.InfoPayload {
	if u == nil || u.device == nil {
		return nil
	}
	return map[string]any{
		"address":  u.device.Address,
		"model":    u.device.Model,
		"category": "update",
	}
}

// Config returns static configuration metadata. The update
// entity has no operator-tunable configuration, so this is minimal.
func (u *Update) Config() payload.ConfigPayload {
	if u == nil {
		return nil
	}
	return map[string]any{
		"device_class":    "firmware",
		"entity_category": "config",
	}
}

// State returns the four live firmware-state fields mirroring
//
//	{
//	 "firmware": "<installed>",
//	 "latest_firmware": "<target>",
//	 "in_progress": <bool>,
//	 "firmware_update_state": "<state-string>"
//	}
func (u *Update) State() payload.StatePayload {
	if u == nil {
		return nil
	}
	return map[string]any{
		"firmware":              u.Firmware(),
		"latest_firmware":       u.LatestFirmware(),
		"in_progress":           u.InProgress(),
		"firmware_update_state": string(u.FirmwareUpdateState()),
	}
}

// ServiceMethodNames returns the registered method names. The only
// service method is "install" which dispatches [Update.Start].
func (u *Update) ServiceMethodNames() []string {
	return u.svcReg.ServiceMethodNames()
}

// Invoke dispatches a named service method. "install" calls Start with
// no refresh delays.
func (u *Update) Invoke(ctx context.Context, name string, params map[string]any, priority hmenum.CommandPriority) error {
	return u.svcReg.Invoke(ctx, name, params, priority)
}

// HADiscoveryPayload returns the HA Update-platform discovery payload.
//
// Deliberately read-only: no `command_topic` / `payload_install`. HA's
// `update` entity would publish "INSTALL" straight to the broker on a
// button press, and nothing in the daemon has ever subscribed to that
// topic — the daemon-level add-on self-updater and the CCU's own
// firmware update ([DefaultDiscoveryBuilder.BuildHubUpdateDiscovery])
// both gate a firmware install behind an operator-confirmed REST
// action for the same reason: an unconfirmed MQTT payload triggering a
// device flash is unsafe, and a broker replaying a stale retained
// command on reconnect would be worse. The install control for a
// device's firmware lives at `POST /devices/{addr}/firmware/update`;
// HA still shows the available-version state via `state_topic` +
// `latest_version_topic`.
func (u *Update) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if u == nil || ctx == nil {
		return "", nil
	}
	stateTopic := ctx.CustomDPStateTopic()
	model := ""
	if u.device != nil {
		model = u.device.Model
	}
	body = map[string]any{
		"device_class":            "firmware",
		"entity_category":         "config",
		"state_topic":             stateTopic,
		"value_template":          "{{ value_json.firmware }}",
		"latest_version_topic":    stateTopic,
		"latest_version_template": "{{ value_json.latest_firmware }}",
		"title":                   model + " Firmware",
		"display_precision":       0,
		// json_attributes_topic mirrors the state so operators can
		// inspect all four fields (firmware, latest_firmware,
		// in_progress, firmware_update_state) in HA entity attributes.
		"json_attributes_topic":    stateTopic,
		"json_attributes_template": "{{ value_json | tojson }}",
	}
	return "update", body
}

// TranslationKey returns the i18n lookup key for this update entity.
//
// return "device_update"
//
// North-bound adapters (HA UpdatePlatform, WS push) use this string as the
// entity's display-name translation key.
func (u *Update) TranslationKey() string { return "device_update" }

// ─── Name ─────────────────────────────────────────────────────────────

// Name returns the entity name for this update data point.
//
// def name(self) -> str: return "Update"
func (u *Update) Name() string { return "Update" }

// ─── FullName ─────────────────────────────────────────────────────────

// FullName returns the device-prefixed full name for this update entity.
//
// def full_name(self) -> str: return f"{self._device.name} Update"
func (u *Update) FullName() string {
	if u == nil || u.device == nil {
		return "Update"
	}
	name := u.device.Name()
	if name == "" {
		return "Update"
	}
	return name + " Update"
}

// ─── Register / Unregister ───────────────────────────────────────────

// Register marks this update entity as registered with the north-bound
// adapter (e.g. MQTT Discovery).
//
// def register(self) -> None: self._is_registered = True
func (u *Update) Register() {
	if u == nil {
		return
	}
	u.mu.Lock()
	u.isRegistered = true
	u.mu.Unlock()
}

// Unregister marks this update entity as no longer registered.
//
// def unregister(self) -> None: self._is_registered = False
func (u *Update) Unregister() {
	if u == nil {
		return
	}
	u.mu.Lock()
	u.isRegistered = false
	u.mu.Unlock()
}

// IsRegistered reports whether Register has been called and Unregister
// has not been called since.
func (u *Update) IsRegistered() bool {
	if u == nil {
		return false
	}
	u.mu.RLock()
	r := u.isRegistered
	u.mu.RUnlock()
	return r
}

// Start dispatches the firmware update. When refreshDelays is
// non-empty and a [FirmwareRefresher] is configured, the caller
// receives a channel that closes once every scheduled refresh has
// fired — this lets the domain layer retry data reads at the intervals
// the CCU typically settles at (mirrors
// `update_firmware(refresh_after_update_intervals=...)`).
func (u *Update) Start(ctx context.Context, refreshDelays []time.Duration) (<-chan struct{}, error) {
	if u.updater == nil {
		return nil, errors.New("device/update: no firmware updater configured")
	}
	if err := u.updater.UpdateFirmware(ctx, u.device.Address); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	if u.refresher == nil || len(refreshDelays) == 0 {
		close(done)
		return done, nil
	}
	go func() {
		defer close(done)
		for _, d := range refreshDelays {
			select {
			case <-ctx.Done():
				return
			case <-time.After(d):
			}
			if err := u.refresher.RefreshFirmwareData(ctx, u.device.Address); err != nil {
				// Best-effort refresh: the update itself already succeeded
				// (UpdateFirmware returned nil above), so a failed re-read
				// only delays the Firmware snapshot settling — it must not
				// fail the update. Still, a silently discarded error hides
				// a CCU-side problem from anyone debugging a stale
				// firmware state, so it is logged instead of dropped.
				slog.Warn("device/update: refresh firmware data failed",
					"address", u.device.Address,
					"err", err)
			}
		}
	}()
	return done, nil
}
