// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package outputs

import "log/slog"

// Shared-channel arbitration: the same device channel may be enrolled
// as an output in more than one zone. Every successful sustained fire
// records a demand (keyed by the enrolled row id, so a re-fire of the
// same row stays one demand); every stop attempt for a row releases
// its demand first and then only writes the device OFF when no OTHER
// zone still demands the channel. The device therefore keeps sounding
// until the last demanding zone stops — silencing one zone must never
// kill another zone's active alarm on a shared siren.
//
// The bookkeeping is in-memory by design: after a daemon restart all
// demands are gone and every stop proceeds, which is the safe
// direction (device off).

// demandRec records one enrolled row's active claim on its channel.
type demandRec struct {
	channelKey string
	zoneID     string
}

// demandChannelKey scopes a demand to the physical target: the same
// channel address on two centrals is two different devices.
func demandChannelKey(centralName, channelAddress string) string {
	return centralName + "|" + channelAddress
}

// noteDemand records that the instance's row holds its channel. Called
// after a successful sustained fire write. Idempotent per row id.
func (m *Manager) noteDemand(inst *instance) {
	m.mu.Lock()
	m.demands[inst.row.ID] = demandRec{
		channelKey: demandChannelKey(inst.row.CentralName, inst.row.ChannelAddress),
		zoneID:     inst.row.ZoneID,
	}
	m.mu.Unlock()
}

// releaseDemandForeignRemains drops the row's own demand and reports
// whether another zone still demands the same channel. Callers skip
// the device OFF write (and its verify chain — read-back would keep
// seeing the intentionally-still-active device and escalate a fault)
// when it returns true.
func (m *Manager) releaseDemandForeignRemains(inst *instance) bool {
	key := demandChannelKey(inst.row.CentralName, inst.row.ChannelAddress)
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.demands, inst.row.ID)
	for _, rec := range m.demands {
		if rec.channelKey == key && rec.zoneID != inst.row.ZoneID {
			return true
		}
	}
	return false
}

// pruneDemands drops demands whose enrolled row no longer exists after
// a Reload (output or zone deleted mid-alarm) so a stale claim can
// never block another zone's stop forever.
func (m *Manager) pruneDemands(rowIDs map[string]struct{}) {
	m.mu.Lock()
	for id := range m.demands {
		if _, ok := rowIDs[id]; !ok {
			delete(m.demands, id)
		}
	}
	m.mu.Unlock()
}

// logSharedStopDeferred emits the visibility breadcrumb for a skipped
// OFF write on a shared channel.
func (m *Manager) logSharedStopDeferred(inst *instance) {
	m.log.Info("alarm.output_stop_deferred_shared",
		slog.String("output", inst.row.ID),
		slog.String("zone", inst.row.ZoneID),
		slog.String("channel", inst.row.ChannelAddress))
}
