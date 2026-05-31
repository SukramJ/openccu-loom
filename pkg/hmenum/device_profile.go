// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

// DeviceProfile is the name of a custom device profile
// ("IPSwitch", "RfCover", …). Concrete constants land alongside the
// auto-generated registry entries in internal/model/custom.
type DeviceProfile string

// String returns the wire representation.
func (p DeviceProfile) String() string { return string(p) }

// DeviceProfile constants mirror the Python DeviceProfile StrEnum
// Values are the exact
// string forms used as registry keys. Only the profiles needed by the
// registered constructors are declared here; the full enum is added
// incrementally alongside each sub-package's init block.
const (
	// Climate profiles.
	DeviceProfileIPThermostat       DeviceProfile = "IPThermostat"
	DeviceProfileIPThermostatGroup  DeviceProfile = "IPThermostatGroup"
	DeviceProfileRfThermostat       DeviceProfile = "RfThermostat"
	DeviceProfileRfThermostatGroup  DeviceProfile = "RfThermostatGroup"
	DeviceProfileSimpleRfThermostat DeviceProfile = "SimpleRfThermostat"

	// Lock profiles.
	DeviceProfileIPLock       DeviceProfile = "IPLock"
	DeviceProfileIPButtonLock DeviceProfile = "IPButtonLock"
	DeviceProfileRFButtonLock DeviceProfile = "RFButtonLock"
	DeviceProfileRfLock       DeviceProfile = "RfLock"

	// Siren profiles.
	DeviceProfileIPSiren          DeviceProfile = "IPSiren"
	DeviceProfileIPSirenSmoke     DeviceProfile = "IPSirenSmoke"
	DeviceProfileIPSoundPlayer    DeviceProfile = "IPSoundPlayer"
	DeviceProfileIPSoundPlayerLed DeviceProfile = "IPSoundPlayerLed"

	// Switch profiles.
	DeviceProfileIPSwitch DeviceProfile = "IPSwitch"
	DeviceProfileRfSwitch DeviceProfile = "RfSwitch"

	// Valve profiles.
	DeviceProfileIPIrrigationValve DeviceProfile = "IPIrrigationValve"

	// Text Display profiles.
	DeviceProfileIPTextDisplay DeviceProfile = "IPTextDisplay"
)
