// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

// DeviceFirmwareState enumerates the states a CCU reports for a device's
// firmware lifecycle.
type DeviceFirmwareState string

// DeviceFirmwareState values.
const (
	DeviceFirmwareStateUnknown                      DeviceFirmwareState = "UNKNOWN"
	DeviceFirmwareStateUpToDate                     DeviceFirmwareState = "UP_TO_DATE"
	DeviceFirmwareStateLiveUpToDate                 DeviceFirmwareState = "LIVE_UP_TO_DATE"
	DeviceFirmwareStateNewFirmwareAvailable         DeviceFirmwareState = "NEW_FIRMWARE_AVAILABLE"
	DeviceFirmwareStateLiveNewFirmwareAvailable     DeviceFirmwareState = "LIVE_NEW_FIRMWARE_AVAILABLE"
	DeviceFirmwareStateDeliverFirmwareImage         DeviceFirmwareState = "DELIVER_FIRMWARE_IMAGE"
	DeviceFirmwareStateLiveDeliverFirmwareImage     DeviceFirmwareState = "LIVE_DELIVER_FIRMWARE_IMAGE"
	DeviceFirmwareStateReadyForUpdate               DeviceFirmwareState = "READY_FOR_UPDATE"
	DeviceFirmwareStateDoUpdatePending              DeviceFirmwareState = "DO_UPDATE_PENDING"
	DeviceFirmwareStatePerformingUpdate             DeviceFirmwareState = "PERFORMING_UPDATE"
	DeviceFirmwareStateBackgroundUpdateNotSupported DeviceFirmwareState = "BACKGROUND_UPDATE_NOT_SUPPORTED"
)

// String returns the wire representation.
func (s DeviceFirmwareState) String() string { return string(s) }

// IsFirmwareUpdateInProgress reports whether the state represents an
// HmIP-RF firmware update that is actively running.
func (s DeviceFirmwareState) IsFirmwareUpdateInProgress() bool {
	return s == DeviceFirmwareStateDoUpdatePending || s == DeviceFirmwareStatePerformingUpdate
}

// IsFirmwareUpdateReady reports whether an update can be started now.
//
// This is the CCU's own install precondition:
//
//	installable = updateState == READY_FOR_UPDATE
//	           || liveServerUpdateState == NEW_FIRMWARE_AVAILABLE
//	           || liveServerUpdateState == DELIVER_FIRMWARE_IMAGE
//
// The live-server states reach the legacy XML-RPC wire carrying a "LIVE_"
// prefix, and the CCU's device-firmware overview renders its Update button for
// LIVE_NEW_FIRMWARE_AVAILABLE. Both were missing here, so an access point with
// an installable update reported its current version as the latest one and the
// update stayed hidden.
//
// DO_UPDATE_PENDING and PERFORMING_UPDATE were in this set and are not
// installable — an install is already running. They answer
// [DeviceFirmwareState.IsFirmwareUpdateInProgress] instead, which every
// consumer that needs to know about an in-flight install already asks.
func (s DeviceFirmwareState) IsFirmwareUpdateReady() bool {
	return s == DeviceFirmwareStateReadyForUpdate ||
		s == DeviceFirmwareStateLiveNewFirmwareAvailable ||
		s == DeviceFirmwareStateLiveDeliverFirmwareImage
}

// DeviceUpdateStatus is the daemon-derived, client-facing firmware-update
// verdict. It collapses the raw [DeviceFirmwareState] phases (and the
// update-available signal) into the three states an update entity needs, so a
// wire client renders the entity without carrying the CCU phase-classification
// sets itself.
type DeviceUpdateStatus string

// DeviceUpdateStatus values.
const (
	// DeviceUpdateStatusUpToDate means no installable update is pending.
	DeviceUpdateStatusUpToDate DeviceUpdateStatus = "up_to_date"
	// DeviceUpdateStatusUpdateAvailable means a newer firmware is ready to
	// install but no install is running.
	DeviceUpdateStatusUpdateAvailable DeviceUpdateStatus = "update_available"
	// DeviceUpdateStatusInstalling means a firmware install is in flight.
	DeviceUpdateStatusInstalling DeviceUpdateStatus = "installing"
)

// String returns the wire representation.
func (s DeviceUpdateStatus) String() string { return string(s) }

// DeriveDeviceUpdateStatus collapses the raw firmware phase + update-available
// signal into the client-facing [DeviceUpdateStatus]. An in-flight install
// wins over availability; otherwise an available/ready update reports
// update_available; everything else is up_to_date.
func DeriveDeviceUpdateStatus(state DeviceFirmwareState, updateAvailable bool) DeviceUpdateStatus {
	switch {
	case state.IsFirmwareUpdateInProgress():
		return DeviceUpdateStatusInstalling
	case updateAvailable || state.IsFirmwareUpdateReady():
		return DeviceUpdateStatusUpdateAvailable
	default:
		return DeviceUpdateStatusUpToDate
	}
}

// ForcedDeviceAvailability overrides a device's auto-detected availability
// for testing or operator intervention.
type ForcedDeviceAvailability string

// ForcedDeviceAvailability values.
const (
	ForcedDeviceAvailabilityForceFalse ForcedDeviceAvailability = "forced_not_available"
	ForcedDeviceAvailabilityForceTrue  ForcedDeviceAvailability = "forced_available"
	ForcedDeviceAvailabilityNotSet     ForcedDeviceAvailability = "not_set"
)

// String returns the wire representation.
func (a ForcedDeviceAvailability) String() string { return string(a) }

// CommandRxMode names the rx modes commands may specify.
type CommandRxMode string

// CommandRxMode values.
const (
	// CommandRxModeUnset is the zero value: no rx_mode argument is appended
	// to the wire call. Backends must omit the argument when this is passed.
	CommandRxModeUnset      CommandRxMode = ""
	CommandRxModeBurst      CommandRxMode = "BURST"
	CommandRxModeWakeup     CommandRxMode = "WAKEUP"
	CommandRxModeLazyConfig CommandRxMode = "LAZY_CONFIG"
)

// String returns the wire representation.
func (m CommandRxMode) String() string { return string(m) }

// DescriptionMarker tags device descriptions that originate from a
// specific integration or filtering system.
type DescriptionMarker string

// DescriptionMarker values.
const (
	DescriptionMarkerHAHM     DescriptionMarker = "HAHM"
	DescriptionMarkerHX       DescriptionMarker = "HX"
	DescriptionMarkerInternal DescriptionMarker = "INTERNAL"
	DescriptionMarkerMQTT     DescriptionMarker = "MQTT"
)

// String returns the wire representation.
func (m DescriptionMarker) String() string { return string(m) }

// ProfileKey names fields within a device profile definition.
type ProfileKey string

// ProfileKey values.
const (
	ProfileKeyAdditionalDPs            ProfileKey = "additional_dps"
	ProfileKeyAllowUndefinedGenericDPs ProfileKey = "allow_undefined_generic_dps"
	ProfileKeyDefaultDPs               ProfileKey = "default_dps"
	ProfileKeyDeviceDefinitions        ProfileKey = "device_definitions"
	ProfileKeyDeviceGroup              ProfileKey = "device_group"
	ProfileKeyFields                   ProfileKey = "fields"
	ProfileKeyIncludeDefaultDPs        ProfileKey = "include_default_dps"
	ProfileKeyPrimaryChannel           ProfileKey = "primary_channel"
	ProfileKeySecondaryChannels        ProfileKey = "secondary_channels"
	ProfileKeyStateChannel             ProfileKey = "state_channel"
)

// String returns the wire representation.
func (k ProfileKey) String() string { return string(k) }

// ChannelOffset names semantic channel offsets used by profile
// definitions.
type ChannelOffset int

// ChannelOffset values.
const (
	ChannelOffsetState  ChannelOffset = -1
	ChannelOffsetSensor ChannelOffset = -2
	ChannelOffsetConfig ChannelOffset = -5
)

// SourceOfDeviceCreation says where a device record in the registry came
// from.
type SourceOfDeviceCreation string

// SourceOfDeviceCreation values.
const (
	SourceOfDeviceCreationCache   SourceOfDeviceCreation = "CACHE"
	SourceOfDeviceCreationInit    SourceOfDeviceCreation = "INIT"
	SourceOfDeviceCreationManual  SourceOfDeviceCreation = "MANUAL"
	SourceOfDeviceCreationNew     SourceOfDeviceCreation = "NEW"
	SourceOfDeviceCreationRefresh SourceOfDeviceCreation = "REFRESH"
)

// String returns the wire representation.
func (s SourceOfDeviceCreation) String() string { return string(s) }

// DeviceLifecycleSubtype discriminates the sub-types carried by a
// DeviceLifecycleEvent. Closes
type DeviceLifecycleSubtype string

// DeviceLifecycleSubtype values.
const (
	// DeviceLifecycleSubtypeCreated fires when a new device is first
	// discovered. Equivalent to DeviceCreatedEvent.
	DeviceLifecycleSubtypeCreated DeviceLifecycleSubtype = "CREATED"

	// DeviceLifecycleSubtypeDelayed fires when device creation is
	// deferred because the device description has not yet been received
	// from the CCU. No standalone Go equivalent previously existed.
	DeviceLifecycleSubtypeDelayed DeviceLifecycleSubtype = "DELAYED"

	// DeviceLifecycleSubtypeUpdated fires when an existing device's
	// metadata (firmware, name, …) is updated.
	DeviceLifecycleSubtypeUpdated DeviceLifecycleSubtype = "UPDATED"

	// DeviceLifecycleSubtypeRemoved fires when the CCU removes a device.
	// Equivalent to DeviceRemovedEvent.
	DeviceLifecycleSubtypeRemoved DeviceLifecycleSubtype = "REMOVED"

	// DeviceLifecycleSubtypeAvailabilityChanged fires when a device's
	// reachability flips. No standalone Go equivalent previously existed.
	DeviceLifecycleSubtypeAvailabilityChanged DeviceLifecycleSubtype = "AVAILABILITY_CHANGED"
)

// String returns the wire representation.
func (s DeviceLifecycleSubtype) String() string { return string(s) }
