// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

// Feature names one capability a central may or may not offer right now,
// depending on the system behind it and — on a token-authenticated system —
// the scopes its credential carries. The values are public wire strings:
// `/system/ccu` reports them, and a refused request names the one it hit.
type Feature string

// Feature values, grouped by the surface they gate.
const (
	FeatureHubSysvars              Feature = "hub.sysvars"
	FeatureHubPrograms             Feature = "hub.programs"
	FeatureHubAlarmMessages        Feature = "hub.alarm_messages"
	FeatureHubInbox                Feature = "hub.inbox"
	FeatureHubServiceMessages      Feature = "hub.service_messages"
	FeatureHubServiceMessagesAck   Feature = "hub.service_messages.ack"
	FeatureHubServiceMessagesMute  Feature = "hub.service_messages.suppress"
	FeatureHubSystemUpdate         Feature = "hub.system_update"
	FeatureHubSystemUpdateInstall  Feature = "hub.system_update.install"
	FeatureSystemBackupCreate      Feature = "system.backup.create"
	FeatureSystemBackupRestore     Feature = "system.backup.restore"
	FeatureSystemReboot            Feature = "system.reboot"
	FeatureSystemPowerOff          Feature = "system.poweroff"
	FeatureSystemRecoveryMode      Feature = "system.recovery_mode"
	FeatureSystemSafeMode          Feature = "system.safe_mode"
	FeatureSystemPosition          Feature = "system.position"
	FeatureSystemAuthDelegation    Feature = "system.auth_delegation"
	FeatureDeviceControl           Feature = "device.control"
	FeatureDeviceConfigure         Feature = "device.configure"
	FeatureDeviceAdmin             Feature = "device.admin"
	FeatureDeviceFirmwareUpdate    Feature = "device.firmware_update"
	FeatureDeviceCommunicationTest Feature = "device.communication_test"
	FeatureDeviceRename            Feature = "device.rename"
	FeatureTaxonomyRead            Feature = "taxonomy.read"
	FeatureTaxonomyAssign          Feature = "taxonomy.assign"
	FeatureTaxonomyEdit            Feature = "taxonomy.edit"
	FeatureTaxonomyTree            Feature = "taxonomy.tree"
	FeatureHeatingGroupsRead       Feature = "heating_groups.read"
	FeatureHeatingGroupsWrite      Feature = "heating_groups.write"
	FeatureInstallMode             Feature = "install_mode"
	FeatureInstallModeLocal        Feature = "install_mode.local"
	FeatureRadioDutyCycle          Feature = "radio.duty_cycle"
	FeatureConnectivity            Feature = "connectivity"
)

// AllFeatures lists every [Feature] in a stable order. A profile's feature
// table must state each one, so an added key cannot silently default.
func AllFeatures() []Feature {
	return []Feature{
		FeatureHubSysvars, FeatureHubPrograms, FeatureHubAlarmMessages, FeatureHubInbox,
		FeatureHubServiceMessages, FeatureHubServiceMessagesAck, FeatureHubServiceMessagesMute,
		FeatureHubSystemUpdate, FeatureHubSystemUpdateInstall,
		FeatureSystemBackupCreate, FeatureSystemBackupRestore,
		FeatureSystemReboot, FeatureSystemPowerOff, FeatureSystemRecoveryMode,
		FeatureSystemSafeMode, FeatureSystemPosition, FeatureSystemAuthDelegation,
		FeatureDeviceControl, FeatureDeviceConfigure, FeatureDeviceAdmin,
		FeatureDeviceFirmwareUpdate, FeatureDeviceCommunicationTest, FeatureDeviceRename,
		FeatureTaxonomyRead, FeatureTaxonomyAssign, FeatureTaxonomyEdit, FeatureTaxonomyTree,
		FeatureHeatingGroupsRead, FeatureHeatingGroupsWrite,
		FeatureInstallMode, FeatureInstallModeLocal,
		FeatureRadioDutyCycle, FeatureConnectivity,
	}
}

// String returns the wire representation.
func (f Feature) String() string { return string(f) }

// FeatureReason says why a [Feature] is not available.
type FeatureReason string

// FeatureReason values.
const (
	// FeatureReasonNotSupported: the system behind the central has no such
	// capability at all.
	FeatureReasonNotSupported FeatureReason = "not_supported_by_system"
	// FeatureReasonMissingScope: the system has it, but the credential the
	// daemon holds lacks the scope that grants it.
	FeatureReasonMissingScope FeatureReason = "missing_scope"
	// FeatureReasonNotReady: the central has not finished its first
	// bring-up, so nothing is known yet.
	FeatureReasonNotReady FeatureReason = "not_ready"
)

// String returns the wire representation.
func (r FeatureReason) String() string { return string(r) }
