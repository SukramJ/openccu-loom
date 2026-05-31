// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"strings"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// WireClimateLinkPeerRefresh subscribes to [hmevent.RecoveryCompletedEvent]
// and [hmevent.LinkPeerChangedEvent] so that [climate.Climate] custom DPs
// stay wired to their heating-activity peer channels after a reconnect or
// after a link-topology change.
//
// After a successful recovery the CCU re-delivers current state; the Climate
// can only derive its "activity" field (heating/idle) from LEVEL or STATE
// pushes from linked valve/switch channels. If those subscriptions are lost
// (or were never wired because the link peers were added after initial
// startup) the activity view goes stale. This wiring closes that gap.
//
// Trigger points:
//
// - [hmevent.RecoveryCompletedEvent] with Result == Success or Partial:
// re-subscribe every Climate whose host device lives on the recovered
// interface. - [hmevent.LinkPeerChangedEvent]: re-subscribe the Climate on
// the channel named by the event's Address field (if it carries one).
//
// For each trigger the function: 1. Locates the [device.Channel] that hosts a
// [*climate.Climate] custom DP. 2. Resolves the peer channel addresses to
// [*device.Channel] objects via the model registry. 3. Calls
// [climate.Climate.RefreshLinkPeerActivitySources] with the peer set and
// stores the returned closer so the previous subscriptions are torn down
// atomically.
//
// Returns a closer that drops both bus subscriptions. Safe to call on daemon
// shutdown — it does not touch the Climate's own peer closers.
func WireClimateLinkPeerRefresh(unit *central.CentralUnit) func() {
	if unit == nil || unit.EventBus == nil || unit.ModelRegistry == nil {
		return func() {}
	}
	bus := unit.EventBus

	// peerClosers maps channel address → most-recent closer returned by
	// RefreshLinkPeerActivitySources. Guarded implicitly: event handlers run
	// on the event-bus goroutine which is single-threaded per-subscriber.
	peerClosers := make(map[string]func())

	// resolveChannel locates *device.Channel by channel address.
	// Channel addresses have the form "<device>:<no>"; the model registry
	// is keyed by device address so we strip the suffix.
	resolveChannel := func(channelAddr string) *device.Channel {
		devAddr := channelAddr
		if idx := strings.LastIndex(channelAddr, ":"); idx > 0 {
			devAddr = channelAddr[:idx]
		}
		d, ok := unit.ModelRegistry.Get(devAddr)
		if !ok {
			return nil
		}
		return d.Channel(channelAddr)
	}

	// refreshForChannel re-subscribes the Climate on ch to the given
	// peer address list. If ch carries no Climate custom DP it is a no-op.
	refreshForChannel := func(ch *device.Channel, peerAddresses []string) {
		if ch == nil {
			return
		}
		cdp := ch.CustomDataPoint()
		if cdp == nil {
			return
		}
		clim, ok := cdp.(*climate.Climate)
		if !ok {
			return
		}
		// Resolve peer addresses to *device.Channel objects.
		var peers []*device.Channel
		for _, pAddr := range peerAddresses {
			if pCh := resolveChannel(pAddr); pCh != nil {
				peers = append(peers, pCh)
			}
		}
		// Tear down previous peer subscriptions before installing new ones.
		if prev, exists := peerClosers[ch.Address]; exists && prev != nil {
			prev()
		}
		peerClosers[ch.Address] = clim.RefreshLinkPeerActivitySources(peers)
	}

	unsub1 := events.Subscribe(bus, func(e hmevent.RecoveryCompletedEvent) {
		if e.Result != hmenum.RecoveryResultSuccess && e.Result != hmenum.RecoveryResultPartial {
			return
		}
		for _, d := range unit.ModelRegistry.List() {
			if d.InterfaceID != e.InterfaceID {
				continue
			}
			for _, ch := range d.Channels() {
				if ch == nil {
					continue
				}
				// Use cached peer addresses so the Climate can immediately
				// re-wire activity subscriptions without waiting for the CCU
				// to re-deliver a topology push (P3 optimisation). When the
				// cache is empty (first boot or peers not yet observed) we
				// pass nil, which causes Climate to drop any stale
				// subscriptions and wait for incoming value pushes — safe
				// because the CCU always delivers current state on reconnect.
				refreshForChannel(ch, ch.LinkPeers())
			}
		}
	})

	unsub2 := events.Subscribe(bus, func(e hmevent.LinkPeerChangedEvent) {
		ch := resolveChannel(e.Address)
		// Update the per-channel cache so the recovery path can use it
		// on the next reconnect without waiting for another topology push.
		if ch != nil {
			ch.SetLinkPeers(e.Peers)
		}
		refreshForChannel(ch, e.Peers)
	})

	return func() {
		unsub1()
		unsub2()
	}
}
