// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core

import (
	"context"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/mattercontract"
)

// TimeSynchronization implements the minimum required surface of the
// Matter TimeSynchronization cluster (0x0038) per Matter Core
// Specification 1.5.1 §11.16. The bridge exposes UTCTime + Granularity
// only — feature flags (TZ, NTPC, NTPS, TSC) all stay off, so the
// optional attributes (TimeSource, TrustedTimeSource, DefaultNTP,
// TimeZone, DSTOffset, etc.) intentionally surface as
// UnsupportedAttribute.
//
// chip-tool reads UTCTime + Granularity during ReadCommissioningInfo;
// returning sensible values keeps the commissioning flow clean
// instead of producing 0xC3 UnsupportedCluster errors.
type TimeSynchronization struct{}

const (
	timeSyncClusterID       uint32 = 0x0038
	timeSyncClusterRevision uint16 = 2 // Matter 1.5.1 §11.16

	timeSyncAttrUTCTime     uint32 = 0x0000
	timeSyncAttrGranularity uint32 = 0x0001
)

// GranularityEnum values per Matter §11.16.5.1.
const (
	GranularityNoTime          uint8 = 0
	GranularityMinutesGran     uint8 = 1
	GranularitySecondsGran     uint8 = 2
	GranularityMillisecGran    uint8 = 3
	GranularityMicrosecondGran uint8 = 4
)

// NewTimeSynchronization returns the cluster server. Stateless —
// every read is computed at call time from `time.Now`.
func NewTimeSynchronization() *TimeSynchronization { return &TimeSynchronization{} }

var (
	_ mattercontract.ClusterServer          = (*TimeSynchronization)(nil)
	_ mattercontract.ClusterAttributeLister = (*TimeSynchronization)(nil)
)

// MatterClusterID implements [mattercontract.ClusterServer].
func (t *TimeSynchronization) MatterClusterID() uint32 { return timeSyncClusterID }

// MatterRead implements [mattercontract.ClusterServer]. UTCTime is
// reported as Matter's epoch_us (microseconds since 2000-01-01 UTC,
// per §A.2). Granularity is fixed at MILLISECONDS_GRANULARITY since
// the bridge syncs from the host clock (typically NTP-disciplined).
func (t *TimeSynchronization) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case timeSyncAttrUTCTime:
		// Matter §A.2: epoch is 2000-01-01 00:00:00 UTC. Difference
		// from Unix epoch is 30 years + 7 leap days = 946684800 seconds.
		const matterEpochOffsetSec int64 = 946684800
		nowMicros := time.Now().UnixMicro() - matterEpochOffsetSec*1_000_000
		if nowMicros < 0 {
			return nil, true // null per spec when host clock is pre-Matter-epoch
		}
		return uint64(nowMicros), true //nolint:gosec // wall-clock micros fit uint64; see #20
	case timeSyncAttrGranularity:
		return GranularityMillisecGran, true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true // no optional features advertised
	case cluster.AttrGlobalClusterRevision:
		return timeSyncClusterRevision, true
	}
	return nil, false
}

// MatterWrite implements [mattercontract.ClusterServer]. Every
// attribute is read-only on the bridge — clients that try to set
// TimeSource etc. get UnsupportedWrite.
func (t *TimeSynchronization) MatterWrite(_ context.Context, attrID uint32, _ any) error {
	return fmt.Errorf("matter: TimeSynchronization attribute 0x%04X is read-only", attrID)
}

// timeSyncCmdSetUTCTime is the SetUTCTime command ID (Matter §11.16.9.1).
// Mirrors matter.js packages/model/src/standard/elements/time-synchronization.element.ts
// command id 0x00.
const timeSyncCmdSetUTCTime uint32 = 0x00

// MatterInvoke implements [mattercontract.ClusterServer].
// SetUTCTime (0x00) is a mandatory command per Matter §11.16.9.1 when the
// UTC feature bit is advertised. The bridge does not adjust the host clock,
// so the command is accepted and returns Success without acting —
// controllers that send SetUTCTime receive a well-formed response instead
// of UnsupportedCommand, which some implementations treat as a fatal
// commissioning error.
// All other commands require feature flags the bridge does not advertise;
// the IM dispatcher rejects them at the path level.
func (t *TimeSynchronization) MatterInvoke(_ context.Context, cmdID uint32, _ any) (any, error) {
	if cmdID == timeSyncCmdSetUTCTime {
		// Accept the command and return Success. The bridge's clock is
		// managed by the host OS; no adjustment is applied here.
		// Mirrors matter.js TimeSynchronizationServer.ts::setUtcTime which
		// stores the value — we omit the store because the bridge is not a
		// time-coordinator.
		return nil, nil
	}
	return nil, im.UnsupportedCommandf("matter: TimeSynchronization command 0x%02X not supported", cmdID)
}

// MatterReportable lists subscribe-able attributes.
func (t *TimeSynchronization) MatterReportable() []uint32 {
	return []uint32{timeSyncAttrUTCTime, timeSyncAttrGranularity}
}

// MatterAttributes lists every TimeSynchronization (0x0038) attribute
// the server implements via MatterRead. Apple Home's HAP service
// rebuild reads the full attribute set; without this the dispatcher
// falls back to MatterReportable's two-attribute surface.
func (t *TimeSynchronization) MatterAttributes() []uint32 {
	return []uint32{timeSyncAttrUTCTime, timeSyncAttrGranularity}
}
