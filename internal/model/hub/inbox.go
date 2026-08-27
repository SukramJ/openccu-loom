// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"sort"
	"sync"
	"time"

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
	// AwaitingRelease marks an entry that is already accepted and fully
	// materialised — it has its CCU ise_id, its channels and its data
	// points, and it can be renamed and assigned rooms right now — but is
	// still withheld from the ecosystems (MQTT / Home Assistant, Matter,
	// outbound webhooks) until the operator finishes onboarding it.
	//
	// It is the wizard's middle state, and it is a different ask than
	// PendingCreation: that one means "decide whether this exists", this
	// one means "you can configure it now, and publishing it is the last
	// step". A client that shows them as one list tells the operator to
	// accept a device that is already accepted.
	AwaitingRelease bool
	// PendingCreation marks an entry the daemon itself is holding back:
	// with `delay_new_device_creation` enabled the announced device
	// descriptions are parked until an operator accepts them, so the
	// device exists on the CCU but has no data points here yet. Entries
	// reported by the CCU's own inbox carry false.
	PendingCreation bool
}

// Inbox aggregates the "pending device" view an operator has to act on.
// It has two sources: the CCU's own inbox (devices paired but not yet
// configured, delivered by the hub coordinator through [Inbox.Replace])
// and the daemon's deferred-creation queue (devices announced over
// newDevices while `delay_new_device_creation` is enabled, delivered
// through [Inbox.SetPendingCreation]). Both mean the same thing to an
// operator — "this device is waiting for you" — so they share one
// aggregate and therefore one REST/WS/MQTT surface. The UI reads
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

	mu sync.RWMutex
	// devices holds the CCU-reported inbox; pending holds the daemon's
	// own deferred-creation queue. They are kept apart so a CCU sweep
	// never drops a deferred entry the CCU does not know about (and
	// vice versa).
	devices map[string]InboxDevice
	pending map[string]InboxDevice
	// awaiting holds the wizard's middle state, kept apart from the other
	// two for the same reason they are kept apart from each other: a CCU
	// sweep must not drop it, and it must not be presented as something
	// to accept.
	awaiting  map[string]InboxDevice
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
		pending:             make(map[string]InboxDevice),
		awaiting:            make(map[string]InboxDevice),
	}
}

// Count is the number of pending devices across both sources.
func (i *Inbox) Count() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.merged())
}

// Observed reports whether the hub coordinator has ever delivered a
// CCU-side set. It stays false while only the daemon's deferred queue
// carries entries: it gates the MQTT publish of the aggregate, which
// must not start before the CCU sweep has established a baseline.
func (i *Inbox) Observed() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.observed
}

// List returns the pending devices of both sources, sorted by address.
func (i *Inbox) List() []InboxDevice {
	i.mu.RLock()
	defer i.mu.RUnlock()
	merged := i.merged()
	out := make([]InboxDevice, 0, len(merged))
	for addr := range merged {
		out = append(out, merged[addr])
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Address < out[b].Address })
	return out
}

// merged joins the CCU-reported entries with the daemon's deferred ones.
// The CCU entry wins on a collision because it carries the richer
// metadata (serial, manufacturer, first-seen), but it inherits the
// PendingCreation marker so the operator sees that accepting the device
// also has to materialise it here. Callers hold i.mu.
func (i *Inbox) merged() map[string]InboxDevice {
	out := make(map[string]InboxDevice, len(i.devices)+len(i.pending)+len(i.awaiting))
	for addr := range i.awaiting {
		out[addr] = i.awaiting[addr]
	}
	for addr := range i.pending {
		out[addr] = i.pending[addr]
	}
	for addr := range i.devices {
		d := i.devices[addr]
		if _, deferred := i.pending[addr]; deferred {
			d.PendingCreation = true
		}
		out[addr] = d
	}
	return out
}

// SetAwaitingRelease swaps the set of devices that are materialised but
// still withheld from the ecosystems. Fires subscribers when the merged
// set actually changed.
//
// Kept apart from the CCU-reported set and the deferred-creation queue so
// a CCU sweep never drops an entry it does not know about, exactly as
// [Inbox.SetPendingCreation] is.
func (i *Inbox) SetAwaitingRelease(devices []InboxDevice) {
	next := make(map[string]InboxDevice, len(devices))
	for j := range devices {
		d := devices[j]
		d.AwaitingRelease = true
		next[d.Address] = d
	}
	i.swap(func() bool {
		now := time.Now().Unix()
		for addr := range next {
			d := next[addr]
			if prev, existed := i.awaiting[addr]; existed && prev.FirstSeen != 0 {
				d.FirstSeen = prev.FirstSeen
			} else if d.FirstSeen == 0 {
				d.FirstSeen = now
			}
			next[addr] = d
		}
		changed := !sameInbox(i.awaiting, next)
		i.awaiting = next
		return changed
	})
}

// Replace swaps the CCU-reported pending-devices set. Fires subscribers
// when the merged set actually changed.
//
// The caller never carries a FirstSeen value (the coordinator has no
// first-detection timestamp of its own — the CCU's inbox query does not
// report one), so this is the one place that stamps it: an address seen
// in the previous set keeps its stamp, a genuinely new address is
// stamped with the current time. Without the carry-over every periodic
// hub scan would reset FirstSeen to "now", since Inbox.Replace rebuilds
// the whole list from scratch on every call.
func (i *Inbox) Replace(devices []InboxDevice) {
	next := make(map[string]InboxDevice, len(devices))
	for j := range devices {
		next[devices[j].Address] = devices[j]
	}
	i.swap(func() bool {
		now := time.Now().Unix()
		for addr := range next {
			d := next[addr]
			if prev, existed := i.devices[addr]; existed && prev.FirstSeen != 0 {
				d.FirstSeen = prev.FirstSeen
			} else if d.FirstSeen == 0 {
				d.FirstSeen = now
			}
			next[addr] = d
		}
		changed := !i.observed || !sameInbox(i.devices, next)
		i.devices = next
		i.observed = true
		return changed
	})
}

// SetPendingCreation swaps the daemon-side deferred-creation queue: the
// devices whose descriptions arrived over newDevices while
// `delay_new_device_creation` is enabled and that no operator has
// accepted yet. Fires subscribers when the merged set actually changed,
// which is what drives the `hub.<central>.inbox` broadcast so an open
// SPA sees a newly paired device without polling.
//
// Carries FirstSeen over the same way [Inbox.Replace] does — the caller
// (PublishPendingDevices) rebuilds the whole queue from scratch on every
// call, so without this an address already pending would have its
// first-detection stamp reset on every subsequent announcement / accept
// in the queue.
func (i *Inbox) SetPendingCreation(devices []InboxDevice) {
	next := make(map[string]InboxDevice, len(devices))
	for j := range devices {
		d := devices[j]
		d.PendingCreation = true
		next[d.Address] = d
	}
	i.swap(func() bool {
		now := time.Now().Unix()
		for addr := range next {
			d := next[addr]
			if prev, existed := i.pending[addr]; existed && prev.FirstSeen != 0 {
				d.FirstSeen = prev.FirstSeen
			} else if d.FirstSeen == 0 {
				d.FirstSeen = now
			}
			next[addr] = d
		}
		changed := !sameInbox(i.pending, next)
		i.pending = next
		return changed
	})
}

// swap applies a mutation to one of the two source sets under the lock
// and notifies subscribers with the merged snapshot when it reported a
// change. Handlers run outside the lock so a subscriber may read back.
func (i *Inbox) swap(mutate func() bool) {
	i.mu.Lock()
	changed := mutate()
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

// Remove drops a single pending device by address from both sources,
// firing subscribers only
// when the entry was actually present. It reconciles a stale entry the CCU no
// longer knows (e.g. an accept that reported the device gone) immediately,
// without waiting for the next full inbox sweep.
func (i *Inbox) Remove(address string) {
	i.swap(func() bool {
		_, known := i.devices[address]
		_, deferred := i.pending[address]
		if !known && !deferred {
			return false
		}
		delete(i.devices, address)
		delete(i.pending, address)
		return true
	})
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
	for addr := range a {
		right, ok := b[addr]
		if !ok || a[addr] != right {
			return false
		}
	}
	return true
}
