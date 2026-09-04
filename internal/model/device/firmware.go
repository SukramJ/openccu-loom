// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// FirmwareInfo is the observable snapshot of a device's firmware
// state — version strings plus the CCU-reported update lifecycle
// phase.
type FirmwareInfo struct {
	Current     string
	Available   string
	Updatable   bool
	UpdateState hmenum.DeviceFirmwareState
}

// GatedLatestFirmware returns the firmware version an install would target,
// gated on the CCU update lifecycle so a newer version is only surfaced once
// it is actually installable: for HmIP-RF the image must have been delivered
// to the device (READY_FOR_UPDATE family); BidCos reports availability
// directly. Every other interface (or an empty available version) yields the
// current version, i.e. "no update". Mirrors the reference stack's
// update.latest_firmware gating — a newer firmware merely existing on the CCU
// (NEW_FIRMWARE_AVAILABLE) is NOT yet an installable update.
func GatedLatestFirmware(iface hmenum.Interface, info FirmwareInfo) string {
	if info.Available == "" || isZeroVersion(info.Available) {
		return info.Current
	}
	switch iface {
	case hmenum.InterfaceHmIPRF:
		// Installable, or already installing: both report the version the
		// device is heading for. IsFirmwareUpdateReady carries the CCU's
		// install precondition, which deliberately excludes the in-flight
		// states — they are asked separately here so a running install keeps
		// showing its target rather than falling back to the current version.
		if info.UpdateState.IsFirmwareUpdateReady() || info.UpdateState.IsFirmwareUpdateInProgress() {
			return info.Available
		}
		return info.Current
	case hmenum.InterfaceBidCosRF, hmenum.InterfaceBidCosWired:
		return info.Available
	default:
		return info.Current
	}
}

// isZeroVersion reports whether v consists only of zero segments
// ("0.0.0", "0.0", "0"). The CCU reports this all-zero placeholder as
// the available firmware of devices it has no OTA image for — e.g. the
// gateway RF module, which is updated through the CCU firmware itself —
// so it must never count as an installable version.
func isZeroVersion(v string) bool {
	sawZero := false
	for _, r := range v {
		switch r {
		case '0':
			sawZero = true
		case '.':
		default:
			return false
		}
	}
	return sawZero
}

// Firmware owns the mutable firmware record and fires change
// callbacks whenever any field transitions.
type Firmware struct {
	mu       sync.RWMutex
	info     FirmwareInfo
	handlers []func(FirmwareInfo)
}

// newFirmware constructs a tracker seeded with initial.
func newFirmware(initial FirmwareInfo) *Firmware {
	if initial.UpdateState == "" {
		initial.UpdateState = hmenum.DeviceFirmwareStateUnknown
	}
	return &Firmware{info: initial}
}

// Info returns the current snapshot.
func (f *Firmware) Info() FirmwareInfo {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.info
}

// Set replaces the firmware info. Returns true and invokes every
// registered handler when any field changed.
func (f *Firmware) Set(next FirmwareInfo) bool {
	if next.UpdateState == "" {
		next.UpdateState = hmenum.DeviceFirmwareStateUnknown
	}
	f.mu.Lock()
	changed := f.info != next
	if changed {
		f.info = next
	}
	handlers := make([]func(FirmwareInfo), len(f.handlers))
	copy(handlers, f.handlers)
	f.mu.Unlock()
	if changed {
		for _, h := range handlers {
			if h != nil {
				h(next)
			}
		}
	}
	return changed
}

// OnChange registers a handler fired on every effective state
// transition. Returns an idempotent unsubscribe closure.
func (f *Firmware) OnChange(fn func(FirmwareInfo)) func() {
	f.mu.Lock()
	f.handlers = append(f.handlers, fn)
	idx := len(f.handlers) - 1
	f.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			if idx < len(f.handlers) {
				f.handlers[idx] = nil
			}
		})
	}
}
