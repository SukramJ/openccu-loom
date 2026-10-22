// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package interfaces

import (
	"strings"

	contract "github.com/SukramJ/go-fabric/contract"
	"github.com/SukramJ/go-fabric/schema"
)

// The Matter port contracts live in
// [github.com/SukramJ/go-fabric/contract], a separate module that depends on
// nothing in this repository. They are re-exported here as aliases so the
// call sites that reach for them through this package keep compiling; new
// code should name the contract symbol directly.

// MatterEndpointSource is a compatibility alias for [contract.EndpointSource].
type MatterEndpointSource = contract.EndpointSource

// MatterClusterServer is a compatibility alias for [contract.ClusterServer].
type MatterClusterServer = contract.ClusterServer

// FabricScopedReader is a compatibility alias for [contract.FabricScopedReader].
type FabricScopedReader = contract.FabricScopedReader

// MatterClusterAttributeLister is a compatibility alias for [contract.ClusterAttributeLister].
type MatterClusterAttributeLister = contract.ClusterAttributeLister

// MatterClusterCommandLister is a compatibility alias for [contract.ClusterCommandLister].
type MatterClusterCommandLister = contract.ClusterCommandLister

// MatterClusterDataVersion is a compatibility alias for [contract.ClusterDataVersion].
type MatterClusterDataVersion = contract.ClusterDataVersion

// MatterClusterEventLister is a compatibility alias for [contract.ClusterEventLister].
type MatterClusterEventLister = contract.ClusterEventLister

// MatterClusterAttributeReadPrivilege is a compatibility alias for [contract.ClusterAttributeReadPrivilege].
type MatterClusterAttributeReadPrivilege = contract.ClusterAttributeReadPrivilege

// MatterClusterAttributeWritePrivilege is a compatibility alias for [contract.ClusterAttributeWritePrivilege].
type MatterClusterAttributeWritePrivilege = contract.ClusterAttributeWritePrivilege

// MatterClusterCommandInvokePrivilege is a compatibility alias for [contract.ClusterCommandInvokePrivilege].
type MatterClusterCommandInvokePrivilege = contract.ClusterCommandInvokePrivilege

// MatterMeasurementClass is a compatibility alias for [contract.MeasurementClass].
type MatterMeasurementClass = contract.MeasurementClass

// MatterMeasurementClass values, aliasing the contract constants.
const (
	// MatterMeasurementNone is a compatibility alias for [contract.MeasurementNone].
	MatterMeasurementNone = contract.MeasurementNone

	// MatterMeasurementTemperature is a compatibility alias for [contract.MeasurementTemperature].
	MatterMeasurementTemperature = contract.MeasurementTemperature

	// MatterMeasurementHumidity is a compatibility alias for [contract.MeasurementHumidity].
	MatterMeasurementHumidity = contract.MeasurementHumidity

	// MatterMeasurementIlluminance is a compatibility alias for [contract.MeasurementIlluminance].
	MatterMeasurementIlluminance = contract.MeasurementIlluminance

	// MatterMeasurementPressure is a compatibility alias for [contract.MeasurementPressure].
	MatterMeasurementPressure = contract.MeasurementPressure

	// MatterMeasurementCO2 is a compatibility alias for [contract.MeasurementCO2].
	MatterMeasurementCO2 = contract.MeasurementCO2

	// MatterMeasurementPM25 is a compatibility alias for [contract.MeasurementPM25].
	MatterMeasurementPM25 = contract.MeasurementPM25

	// MatterMeasurementPM10 is a compatibility alias for [contract.MeasurementPM10].
	MatterMeasurementPM10 = contract.MeasurementPM10

	// MatterMeasurementOccupancy is a compatibility alias for [contract.MeasurementOccupancy].
	MatterMeasurementOccupancy = contract.MeasurementOccupancy

	// MatterMeasurementContact is a compatibility alias for [contract.MeasurementContact].
	MatterMeasurementContact = contract.MeasurementContact

	// MatterMeasurementLeak is a compatibility alias for [contract.MeasurementLeak].
	MatterMeasurementLeak = contract.MeasurementLeak

	// MatterMeasurementBattery is a compatibility alias for [contract.MeasurementBattery].
	MatterMeasurementBattery = contract.MeasurementBattery

	// MatterMeasurementPower is a compatibility alias for [contract.MeasurementPower].
	MatterMeasurementPower = contract.MeasurementPower

	// MatterMeasurementEnergy is a compatibility alias for [contract.MeasurementEnergy].
	MatterMeasurementEnergy = contract.MeasurementEnergy

	// MatterMeasurementMomentarySwitch is a compatibility alias for [contract.MeasurementMomentarySwitch].
	MatterMeasurementMomentarySwitch = contract.MeasurementMomentarySwitch

	// MatterMeasurementElectrical is a compatibility alias for [contract.MeasurementElectrical].
	MatterMeasurementElectrical = contract.MeasurementElectrical

	// MatterMeasurementFlow is a compatibility alias for [contract.MeasurementFlow].
	MatterMeasurementFlow = contract.MeasurementFlow
	// MatterMeasurementCO is a compatibility alias for [contract.MeasurementCO].
	MatterMeasurementCO = contract.MeasurementCO
	// MatterMeasurementNO2 is a compatibility alias for [contract.MeasurementNO2].
	MatterMeasurementNO2 = contract.MeasurementNO2
	// MatterMeasurementOzone is a compatibility alias for [contract.MeasurementOzone].
	MatterMeasurementOzone = contract.MeasurementOzone
	// MatterMeasurementFormaldehyde is a compatibility alias for [contract.MeasurementFormaldehyde].
	MatterMeasurementFormaldehyde = contract.MeasurementFormaldehyde
	// MatterMeasurementPM1 is a compatibility alias for [contract.MeasurementPM1].
	MatterMeasurementPM1 = contract.MeasurementPM1
	// MatterMeasurementTVOC is a compatibility alias for [contract.MeasurementTVOC].
	MatterMeasurementTVOC = contract.MeasurementTVOC
	// MatterMeasurementRadon is a compatibility alias for [contract.MeasurementRadon].
	MatterMeasurementRadon = contract.MeasurementRadon
	// MatterMeasurementSoilMoisture is a compatibility alias for [contract.MeasurementSoilMoisture].
	MatterMeasurementSoilMoisture = contract.MeasurementSoilMoisture
)

// MatterElectricalReadings is a compatibility alias for [contract.ElectricalReadings].
type MatterElectricalReadings = contract.ElectricalReadings

// MatterMeasurementSource is a compatibility alias for [contract.MeasurementSource].
type MatterMeasurementSource = contract.MeasurementSource

// MatterFloatMeasurementSource is a compatibility alias for [contract.FloatMeasurementSource].
type MatterFloatMeasurementSource = contract.FloatMeasurementSource

// MatterBoolMeasurementSource is a compatibility alias for [contract.BoolMeasurementSource].
type MatterBoolMeasurementSource = contract.BoolMeasurementSource

// MatterChangeNotifier is a compatibility alias for [contract.ChangeNotifier].
type MatterChangeNotifier = contract.ChangeNotifier

// MatterEventPriority is a compatibility alias for [contract.EventPriority].
type MatterEventPriority = contract.EventPriority

// MatterEventPriority values, aliasing the contract constants.
const (
	// MatterEventPriorityDebug is a compatibility alias for [contract.EventPriorityDebug].
	MatterEventPriorityDebug = contract.EventPriorityDebug

	// MatterEventPriorityInfo is a compatibility alias for [contract.EventPriorityInfo].
	MatterEventPriorityInfo = contract.EventPriorityInfo

	// MatterEventPriorityCritical is a compatibility alias for [contract.EventPriorityCritical].
	MatterEventPriorityCritical = contract.EventPriorityCritical
)

// MatterEventEmitter is a compatibility alias for [contract.EventEmitter].
type MatterEventEmitter = contract.EventEmitter

// MatterEventReceiver is a compatibility alias for [contract.EventReceiver].
type MatterEventReceiver = contract.EventReceiver

// MatterEligibilityState is a compatibility alias for [contract.EligibilityState].
type MatterEligibilityState = contract.EligibilityState

// MatterEligibilityState values, aliasing the contract constants.
const (
	// MatterEligibilityUnmappable is a compatibility alias for [contract.EligibilityUnmappable].
	MatterEligibilityUnmappable = contract.EligibilityUnmappable

	// MatterEligibilityMappable is a compatibility alias for [contract.EligibilityMappable].
	MatterEligibilityMappable = contract.EligibilityMappable

	// MatterEligibilityPartial is a compatibility alias for [contract.EligibilityPartial].
	MatterEligibilityPartial = contract.EligibilityPartial
)

// MatterEligibilityVerdict is a compatibility alias for [contract.EligibilityVerdict].
type MatterEligibilityVerdict = contract.EligibilityVerdict

// MatterEligibilitySource is a compatibility alias for [contract.EligibilitySource].
type MatterEligibilitySource = contract.EligibilitySource

// MatterDeviceTypeName returns the operator-facing name for a Matter
// Device Type ID. Returns the empty string for `0` (no device type)
// and a hex fallback like "0x0123" for IDs the model does not project
// to — the UI then still has something stable to render and to filter
// on.
//
// [contract.DeviceTypeName] answers first; a device type its label
// table lacks is named from the generated schema
// ([schema.DeviceTypeName]) before the hex fallback, so every type a
// measurement class advertises has a label here
// (TestW2PkgMatterDeviceTypeNameCoversEveryAdvertisedType).
func MatterDeviceTypeName(id uint16) string {
	if id == 0 {
		return ""
	}
	if name := contract.DeviceTypeName(id); !strings.HasPrefix(name, "0x") {
		return name
	}
	// contract's label table can lag the generated schema (it once
	// named no SoilSensor, 0x0045, which the soil-moisture measurement
	// class advertises; go-fabric v0.4.0 generates it); the schema
	// carries every device type matter.js models, so it answers before
	// the hex fallback.
	if name, ok := schema.DeviceTypeName(schema.DeviceTypeID(id)); ok {
		return name
	}
	return contract.DeviceTypeName(id)
}
