// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package interfaces

import "github.com/SukramJ/openccu-loom/pkg/mattercontract"

// The Matter port contracts live in
// [github.com/SukramJ/openccu-loom/pkg/mattercontract], which depends on
// nothing else in this repository at all. They are re-exported here as
// aliases so the call sites that reach for them through this package
// keep compiling; new code should name the mattercontract symbol directly.

// MatterEndpointSource is a compatibility alias for [mattercontract.EndpointSource].
type MatterEndpointSource = mattercontract.EndpointSource

// MatterClusterServer is a compatibility alias for [mattercontract.ClusterServer].
type MatterClusterServer = mattercontract.ClusterServer

// FabricScopedReader is a compatibility alias for [mattercontract.FabricScopedReader].
type FabricScopedReader = mattercontract.FabricScopedReader

// MatterClusterAttributeLister is a compatibility alias for [mattercontract.ClusterAttributeLister].
type MatterClusterAttributeLister = mattercontract.ClusterAttributeLister

// MatterClusterCommandLister is a compatibility alias for [mattercontract.ClusterCommandLister].
type MatterClusterCommandLister = mattercontract.ClusterCommandLister

// MatterClusterDataVersion is a compatibility alias for [mattercontract.ClusterDataVersion].
type MatterClusterDataVersion = mattercontract.ClusterDataVersion

// MatterClusterEventLister is a compatibility alias for [mattercontract.ClusterEventLister].
type MatterClusterEventLister = mattercontract.ClusterEventLister

// MatterClusterAttributeReadPrivilege is a compatibility alias for [mattercontract.ClusterAttributeReadPrivilege].
type MatterClusterAttributeReadPrivilege = mattercontract.ClusterAttributeReadPrivilege

// MatterClusterAttributeWritePrivilege is a compatibility alias for [mattercontract.ClusterAttributeWritePrivilege].
type MatterClusterAttributeWritePrivilege = mattercontract.ClusterAttributeWritePrivilege

// MatterClusterCommandInvokePrivilege is a compatibility alias for [mattercontract.ClusterCommandInvokePrivilege].
type MatterClusterCommandInvokePrivilege = mattercontract.ClusterCommandInvokePrivilege

// MatterMeasurementClass is a compatibility alias for [mattercontract.MeasurementClass].
type MatterMeasurementClass = mattercontract.MeasurementClass

// MatterMeasurementClass values, aliasing the mattercontract constants.
const (
	// MatterMeasurementNone is a compatibility alias for [mattercontract.MeasurementNone].
	MatterMeasurementNone = mattercontract.MeasurementNone

	// MatterMeasurementTemperature is a compatibility alias for [mattercontract.MeasurementTemperature].
	MatterMeasurementTemperature = mattercontract.MeasurementTemperature

	// MatterMeasurementHumidity is a compatibility alias for [mattercontract.MeasurementHumidity].
	MatterMeasurementHumidity = mattercontract.MeasurementHumidity

	// MatterMeasurementIlluminance is a compatibility alias for [mattercontract.MeasurementIlluminance].
	MatterMeasurementIlluminance = mattercontract.MeasurementIlluminance

	// MatterMeasurementPressure is a compatibility alias for [mattercontract.MeasurementPressure].
	MatterMeasurementPressure = mattercontract.MeasurementPressure

	// MatterMeasurementCO2 is a compatibility alias for [mattercontract.MeasurementCO2].
	MatterMeasurementCO2 = mattercontract.MeasurementCO2

	// MatterMeasurementPM25 is a compatibility alias for [mattercontract.MeasurementPM25].
	MatterMeasurementPM25 = mattercontract.MeasurementPM25

	// MatterMeasurementPM10 is a compatibility alias for [mattercontract.MeasurementPM10].
	MatterMeasurementPM10 = mattercontract.MeasurementPM10

	// MatterMeasurementOccupancy is a compatibility alias for [mattercontract.MeasurementOccupancy].
	MatterMeasurementOccupancy = mattercontract.MeasurementOccupancy

	// MatterMeasurementContact is a compatibility alias for [mattercontract.MeasurementContact].
	MatterMeasurementContact = mattercontract.MeasurementContact

	// MatterMeasurementLeak is a compatibility alias for [mattercontract.MeasurementLeak].
	MatterMeasurementLeak = mattercontract.MeasurementLeak

	// MatterMeasurementBattery is a compatibility alias for [mattercontract.MeasurementBattery].
	MatterMeasurementBattery = mattercontract.MeasurementBattery

	// MatterMeasurementPower is a compatibility alias for [mattercontract.MeasurementPower].
	MatterMeasurementPower = mattercontract.MeasurementPower

	// MatterMeasurementEnergy is a compatibility alias for [mattercontract.MeasurementEnergy].
	MatterMeasurementEnergy = mattercontract.MeasurementEnergy

	// MatterMeasurementMomentarySwitch is a compatibility alias for [mattercontract.MeasurementMomentarySwitch].
	MatterMeasurementMomentarySwitch = mattercontract.MeasurementMomentarySwitch

	// MatterMeasurementElectrical is a compatibility alias for [mattercontract.MeasurementElectrical].
	MatterMeasurementElectrical = mattercontract.MeasurementElectrical
)

// MatterElectricalReadings is a compatibility alias for [mattercontract.ElectricalReadings].
type MatterElectricalReadings = mattercontract.ElectricalReadings

// MatterMeasurementSource is a compatibility alias for [mattercontract.MeasurementSource].
type MatterMeasurementSource = mattercontract.MeasurementSource

// MatterFloatMeasurementSource is a compatibility alias for [mattercontract.FloatMeasurementSource].
type MatterFloatMeasurementSource = mattercontract.FloatMeasurementSource

// MatterBoolMeasurementSource is a compatibility alias for [mattercontract.BoolMeasurementSource].
type MatterBoolMeasurementSource = mattercontract.BoolMeasurementSource

// MatterChangeNotifier is a compatibility alias for [mattercontract.ChangeNotifier].
type MatterChangeNotifier = mattercontract.ChangeNotifier

// MatterEventPriority is a compatibility alias for [mattercontract.EventPriority].
type MatterEventPriority = mattercontract.EventPriority

// MatterEventPriority values, aliasing the mattercontract constants.
const (
	// MatterEventPriorityDebug is a compatibility alias for [mattercontract.EventPriorityDebug].
	MatterEventPriorityDebug = mattercontract.EventPriorityDebug

	// MatterEventPriorityInfo is a compatibility alias for [mattercontract.EventPriorityInfo].
	MatterEventPriorityInfo = mattercontract.EventPriorityInfo

	// MatterEventPriorityCritical is a compatibility alias for [mattercontract.EventPriorityCritical].
	MatterEventPriorityCritical = mattercontract.EventPriorityCritical
)

// MatterEventEmitter is a compatibility alias for [mattercontract.EventEmitter].
type MatterEventEmitter = mattercontract.EventEmitter

// MatterEventReceiver is a compatibility alias for [mattercontract.EventReceiver].
type MatterEventReceiver = mattercontract.EventReceiver

// MatterEligibilityState is a compatibility alias for [mattercontract.EligibilityState].
type MatterEligibilityState = mattercontract.EligibilityState

// MatterEligibilityState values, aliasing the mattercontract constants.
const (
	// MatterEligibilityUnmappable is a compatibility alias for [mattercontract.EligibilityUnmappable].
	MatterEligibilityUnmappable = mattercontract.EligibilityUnmappable

	// MatterEligibilityMappable is a compatibility alias for [mattercontract.EligibilityMappable].
	MatterEligibilityMappable = mattercontract.EligibilityMappable

	// MatterEligibilityPartial is a compatibility alias for [mattercontract.EligibilityPartial].
	MatterEligibilityPartial = mattercontract.EligibilityPartial
)

// MatterEligibilityVerdict is a compatibility alias for [mattercontract.EligibilityVerdict].
type MatterEligibilityVerdict = mattercontract.EligibilityVerdict

// MatterEligibilitySource is a compatibility alias for [mattercontract.EligibilitySource].
type MatterEligibilitySource = mattercontract.EligibilitySource

// MatterMeasurementClassDeviceType returns the standalone Matter
// Device Type (uint16) that best wraps the given measurement class
// when the source is materialised as its own sensor endpoint. Zero
// for `MatterMeasurementNone` and any value with no standalone
// device-type counterpart (Battery / Power / Energy roll up to a
// host endpoint instead).
//
// Compatibility wrapper; [mattercontract.MeasurementClassDeviceType] is
// the single source of truth for the mapping and carries the full
// rationale for each entry.
func MatterMeasurementClassDeviceType(class MatterMeasurementClass) uint16 {
	return mattercontract.MeasurementClassDeviceType(class)
}

// MatterDeviceTypeName returns the operator-facing name for a Matter
// Device Type ID. Returns the empty string for `0` (no device type)
// and a hex fallback like "0x0123" for IDs the model does not project
// to — the UI then still has something stable to render and to filter
// on.
//
// Compatibility wrapper; [mattercontract.DeviceTypeName] is the single
// source of truth for the device-type → human label mapping and
// carries the rule that every advertised device type needs a case
// there.
func MatterDeviceTypeName(id uint16) string {
	return mattercontract.DeviceTypeName(id)
}

// MatterMeasurementClassClusterID returns the cluster ID the given
// measurement class projects to. Counterpart to
// [MatterMeasurementClassDeviceType] for the cluster slot.
//
// Compatibility wrapper for [mattercontract.MeasurementClassClusterID].
func MatterMeasurementClassClusterID(class MatterMeasurementClass) uint32 {
	return mattercontract.MeasurementClassClusterID(class)
}
