// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package interfaces

import "github.com/SukramJ/openccu-loom/pkg/matterport"

// The Matter port contracts live in
// [github.com/SukramJ/openccu-loom/pkg/matterport], which depends on
// nothing in this repository but pkg/hmenum. They are re-exported here
// as aliases so the call sites that reach for them through this package
// keep compiling; new code should name the matterport symbol directly.

// MatterEndpointSource is a compatibility alias for [matterport.EndpointSource].
type MatterEndpointSource = matterport.EndpointSource

// MatterClusterServer is a compatibility alias for [matterport.ClusterServer].
type MatterClusterServer = matterport.ClusterServer

// FabricScopedReader is a compatibility alias for [matterport.FabricScopedReader].
type FabricScopedReader = matterport.FabricScopedReader

// MatterClusterAttributeLister is a compatibility alias for [matterport.ClusterAttributeLister].
type MatterClusterAttributeLister = matterport.ClusterAttributeLister

// MatterClusterCommandLister is a compatibility alias for [matterport.ClusterCommandLister].
type MatterClusterCommandLister = matterport.ClusterCommandLister

// MatterClusterDataVersion is a compatibility alias for [matterport.ClusterDataVersion].
type MatterClusterDataVersion = matterport.ClusterDataVersion

// MatterClusterEventLister is a compatibility alias for [matterport.ClusterEventLister].
type MatterClusterEventLister = matterport.ClusterEventLister

// MatterClusterAttributeReadPrivilege is a compatibility alias for [matterport.ClusterAttributeReadPrivilege].
type MatterClusterAttributeReadPrivilege = matterport.ClusterAttributeReadPrivilege

// MatterClusterAttributeWritePrivilege is a compatibility alias for [matterport.ClusterAttributeWritePrivilege].
type MatterClusterAttributeWritePrivilege = matterport.ClusterAttributeWritePrivilege

// MatterClusterCommandInvokePrivilege is a compatibility alias for [matterport.ClusterCommandInvokePrivilege].
type MatterClusterCommandInvokePrivilege = matterport.ClusterCommandInvokePrivilege

// MatterMeasurementClass is a compatibility alias for [matterport.MeasurementClass].
type MatterMeasurementClass = matterport.MeasurementClass

// MatterMeasurementClass values, aliasing the matterport constants.
const (
	// MatterMeasurementNone is a compatibility alias for [matterport.MeasurementNone].
	MatterMeasurementNone = matterport.MeasurementNone

	// MatterMeasurementTemperature is a compatibility alias for [matterport.MeasurementTemperature].
	MatterMeasurementTemperature = matterport.MeasurementTemperature

	// MatterMeasurementHumidity is a compatibility alias for [matterport.MeasurementHumidity].
	MatterMeasurementHumidity = matterport.MeasurementHumidity

	// MatterMeasurementIlluminance is a compatibility alias for [matterport.MeasurementIlluminance].
	MatterMeasurementIlluminance = matterport.MeasurementIlluminance

	// MatterMeasurementPressure is a compatibility alias for [matterport.MeasurementPressure].
	MatterMeasurementPressure = matterport.MeasurementPressure

	// MatterMeasurementCO2 is a compatibility alias for [matterport.MeasurementCO2].
	MatterMeasurementCO2 = matterport.MeasurementCO2

	// MatterMeasurementPM25 is a compatibility alias for [matterport.MeasurementPM25].
	MatterMeasurementPM25 = matterport.MeasurementPM25

	// MatterMeasurementPM10 is a compatibility alias for [matterport.MeasurementPM10].
	MatterMeasurementPM10 = matterport.MeasurementPM10

	// MatterMeasurementOccupancy is a compatibility alias for [matterport.MeasurementOccupancy].
	MatterMeasurementOccupancy = matterport.MeasurementOccupancy

	// MatterMeasurementContact is a compatibility alias for [matterport.MeasurementContact].
	MatterMeasurementContact = matterport.MeasurementContact

	// MatterMeasurementLeak is a compatibility alias for [matterport.MeasurementLeak].
	MatterMeasurementLeak = matterport.MeasurementLeak

	// MatterMeasurementBattery is a compatibility alias for [matterport.MeasurementBattery].
	MatterMeasurementBattery = matterport.MeasurementBattery

	// MatterMeasurementPower is a compatibility alias for [matterport.MeasurementPower].
	MatterMeasurementPower = matterport.MeasurementPower

	// MatterMeasurementEnergy is a compatibility alias for [matterport.MeasurementEnergy].
	MatterMeasurementEnergy = matterport.MeasurementEnergy

	// MatterMeasurementMomentarySwitch is a compatibility alias for [matterport.MeasurementMomentarySwitch].
	MatterMeasurementMomentarySwitch = matterport.MeasurementMomentarySwitch

	// MatterMeasurementElectrical is a compatibility alias for [matterport.MeasurementElectrical].
	MatterMeasurementElectrical = matterport.MeasurementElectrical
)

// MatterElectricalReadings is a compatibility alias for [matterport.ElectricalReadings].
type MatterElectricalReadings = matterport.ElectricalReadings

// MatterMeasurementSource is a compatibility alias for [matterport.MeasurementSource].
type MatterMeasurementSource = matterport.MeasurementSource

// MatterFloatMeasurementSource is a compatibility alias for [matterport.FloatMeasurementSource].
type MatterFloatMeasurementSource = matterport.FloatMeasurementSource

// MatterBoolMeasurementSource is a compatibility alias for [matterport.BoolMeasurementSource].
type MatterBoolMeasurementSource = matterport.BoolMeasurementSource

// MatterChangeNotifier is a compatibility alias for [matterport.ChangeNotifier].
type MatterChangeNotifier = matterport.ChangeNotifier

// MatterEventPriority is a compatibility alias for [matterport.EventPriority].
type MatterEventPriority = matterport.EventPriority

// MatterEventPriority values, aliasing the matterport constants.
const (
	// MatterEventPriorityDebug is a compatibility alias for [matterport.EventPriorityDebug].
	MatterEventPriorityDebug = matterport.EventPriorityDebug

	// MatterEventPriorityInfo is a compatibility alias for [matterport.EventPriorityInfo].
	MatterEventPriorityInfo = matterport.EventPriorityInfo

	// MatterEventPriorityCritical is a compatibility alias for [matterport.EventPriorityCritical].
	MatterEventPriorityCritical = matterport.EventPriorityCritical
)

// MatterEventEmitter is a compatibility alias for [matterport.EventEmitter].
type MatterEventEmitter = matterport.EventEmitter

// MatterEventReceiver is a compatibility alias for [matterport.EventReceiver].
type MatterEventReceiver = matterport.EventReceiver

// MatterEligibilityState is a compatibility alias for [matterport.EligibilityState].
type MatterEligibilityState = matterport.EligibilityState

// MatterEligibilityState values, aliasing the matterport constants.
const (
	// MatterEligibilityUnmappable is a compatibility alias for [matterport.EligibilityUnmappable].
	MatterEligibilityUnmappable = matterport.EligibilityUnmappable

	// MatterEligibilityMappable is a compatibility alias for [matterport.EligibilityMappable].
	MatterEligibilityMappable = matterport.EligibilityMappable

	// MatterEligibilityPartial is a compatibility alias for [matterport.EligibilityPartial].
	MatterEligibilityPartial = matterport.EligibilityPartial
)

// MatterEligibilityVerdict is a compatibility alias for [matterport.EligibilityVerdict].
type MatterEligibilityVerdict = matterport.EligibilityVerdict

// MatterEligibilitySource is a compatibility alias for [matterport.EligibilitySource].
type MatterEligibilitySource = matterport.EligibilitySource

// MatterMeasurementClassDeviceType returns the standalone Matter
// Device Type (uint16) that best wraps the given measurement class
// when the source is materialised as its own sensor endpoint. Zero
// for `MatterMeasurementNone` and any value with no standalone
// device-type counterpart (Battery / Power / Energy roll up to a
// host endpoint instead).
//
// Compatibility wrapper; [matterport.MeasurementClassDeviceType] is
// the single source of truth for the mapping and carries the full
// rationale for each entry.
func MatterMeasurementClassDeviceType(class MatterMeasurementClass) uint16 {
	return matterport.MeasurementClassDeviceType(class)
}

// MatterDeviceTypeName returns the operator-facing name for a Matter
// Device Type ID. Returns the empty string for `0` (no device type)
// and a hex fallback like "0x0123" for IDs the model does not project
// to — the UI then still has something stable to render and to filter
// on.
//
// Compatibility wrapper; [matterport.DeviceTypeName] is the single
// source of truth for the device-type → human label mapping and
// carries the rule that every advertised device type needs a case
// there.
func MatterDeviceTypeName(id uint16) string {
	return matterport.DeviceTypeName(id)
}

// MatterMeasurementClassClusterID returns the cluster ID the given
// measurement class projects to. Counterpart to
// [MatterMeasurementClassDeviceType] for the cluster slot.
//
// Compatibility wrapper for [matterport.MeasurementClassClusterID].
func MatterMeasurementClassClusterID(class MatterMeasurementClass) uint32 {
	return matterport.MeasurementClassClusterID(class)
}
