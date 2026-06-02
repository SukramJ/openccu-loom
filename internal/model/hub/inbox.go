// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"sort"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// InboxDevice is one pending pairing candidate reported by the CCU.
// The primary key for deduplication is Address.
// DeviceID, Name, and Interface mirror the Python InboxDeviceData fields
// (const.py:2100-2108). Model, Serial, and Manufacturer are Go-side
// extensions that carry additional CCU-reported metadata.
type InboxDevice struct {
	// DeviceID is the CCU-internal device identifier (device_id).
	DeviceID string
	// Address is the RF/wired address of the pending device.
	Address string
	// Name is the operator-assigned or default device name.
	Name string
	// Model holds the CCU device type string (maps to device_type).
	Model string
	// Interface is the CCU interface through which the device was detected.
	Interface string
	// Serial is the device serial number (Go extension, not in Python model).
	Serial string
	// Manufacturer is the declared manufacturer string (Go extension).
	Manufacturer string
	// FirstSeen is a Unix-second timestamp set by the coordinator on
	// first detection (Go extension, not in Python model).
	FirstSeen int64
}

// Inbox aggregates the "pending device" view exposed by the CCU's
// system variables. The hub coordinator populates it; the UI reads
// `Count()` / `List()` to surface pairing candidates.
//
// Inbox embeds [datapoint.BaseDataPointFields] mirroring
// (hub/inbox.py:31). The promoted lifecycle / timestamp methods are
// thereby activated. Multi-CCU callers MUST use
// [NewInboxWithCentral].
type Inbox struct {
	datapoint.BaseDataPointFields

	// ServiceRegistry implements the write-half of [payload.Source].
	// Inbox is read-only from the Source perspective; accept-device
	// operations go through Hub.AcceptInboxDeviceRemote.
	payload.ServiceRegistry

	mu        sync.RWMutex
	devices   map[string]InboxDevice
	observed  bool
	callbacks []func([]InboxDevice)
}

// NewInbox constructs an empty Inbox with no central scoping.
// Multi-CCU callers MUST use [NewInboxWithCentral].
func NewInbox() *Inbox { return NewInboxWithCentral("") }

// NewInboxWithCentral is the multi-CCU-safe constructor. The embedded
// [datapoint.BaseDataPointFields] is initialised with the `central`
// scope so the resulting [UniqueID] is `<central>::inbox`.
// ADR 0002 requires production callers to set `central`.
func NewInboxWithCentral(centralName string) *Inbox {
	return &Inbox{
		BaseDataPointFields: datapoint.NewBaseDataPointFields(centralName, "", "inbox"),
		devices:             make(map[string]InboxDevice),
	}
}

// Count is the number of pending devices.
func (i *Inbox) Count() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.devices)
}

// Observed reports whether the coordinator has ever delivered a set.
func (i *Inbox) Observed() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.observed
}

// List returns the pending devices sorted by address.
func (i *Inbox) List() []InboxDevice {
	i.mu.RLock()
	defer i.mu.RUnlock()
	out := make([]InboxDevice, 0, len(i.devices))
	for _, d := range i.devices {
		out = append(out, d)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Address < out[b].Address })
	return out
}

// Replace swaps the whole pending-devices set. Fires subscribers when
// the set actually changed.
func (i *Inbox) Replace(devices []InboxDevice) {
	i.mu.Lock()
	next := make(map[string]InboxDevice, len(devices))
	for _, d := range devices {
		next[d.Address] = d
	}
	changed := !i.observed || !sameInbox(i.devices, next)
	i.devices = next
	i.observed = true
	cbs := make([]func([]InboxDevice), len(i.callbacks))
	copy(cbs, i.callbacks)
	i.mu.Unlock()
	if !changed {
		return
	}
	snap := i.List()
	for _, cb := range cbs {
		if cb != nil {
			cb(snap)
		}
	}
}

// OnUpdate registers a change handler. Returns an idempotent
// unsubscribe closure.
func (i *Inbox) OnUpdate(fn func([]InboxDevice)) func() {
	i.mu.Lock()
	i.callbacks = append(i.callbacks, fn)
	idx := len(i.callbacks) - 1
	i.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			i.mu.Lock()
			defer i.mu.Unlock()
			if idx < len(i.callbacks) {
				i.callbacks[idx] = nil
			}
		})
	}
}

// MQTTTopics implements [payload.MQTTAddressable] — the canonical inbox
// aggregate is published to `<base>/<central>/hub/inbox`. Read-only;
// no Set topic.
func (i *Inbox) MQTTTopics(base, centralName string) payload.MQTTTopicSet {
	return payload.MQTTTopicSet{
		State: naming.MQTTHubInbox(base, centralName),
	}
}

// LegacyName returns the original pre-slug name stored on the CCU.
// Inbox is a structural aggregate without a CCU-side variable name,
// so this always returns "".
func (*Inbox) LegacyName() string { return "" }

// Description returns the optional human-readable description. Inbox has
// no CCU-side description field, so this always returns "".
func (*Inbox) Description() string { return "" }

// TranslationKey returns the HA translation key used to localise the inbox
// sensor entity.
func (i *Inbox) TranslationKey() string { return "inbox" }

// DataType returns the CCU data type for the inbox aggregate.
func (i *Inbox) DataType() string { return "INTEGER" }

func sameInbox(a, b map[string]InboxDevice) bool {
	if len(a) != len(b) {
		return false
	}
	for addr, left := range a {
		right, ok := b[addr]
		if !ok || left != right {
			return false
		}
	}
	return true
}
