// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

// DeviceScopedPayload is a broadcast payload whose subject is one device.
//
// It exists so the per-connection onboarding filter (`released_only`) can
// ask a payload which device it is about instead of matching on its type.
// The type-switch this replaces listed five of the ten device-scoped
// payloads, and the five it missed included the device-trigger frame — a
// client that turns those into automations would have fired them for a
// device it had explicitly asked not to see. A list that has to be
// extended by hand every time a payload is added is a list that will be
// wrong again; a method the compiler asks for is not.
//
// Implement it on a payload whose subject IS a device. A hub entity that
// merely NAMES a device it relates to implements
// [DeviceAssociatedPayload] instead — see there for why the two are
// treated differently.
//
// Pinned by TestEveryDeviceScopedPayloadIsFilterable: every broadcast
// payload with a `device_address` field must implement this or be listed
// there with a reason.
type DeviceScopedPayload interface {
	// DeviceAddr returns the address of the device the payload is about.
	// An empty string means "not attributable", and the filter then lets
	// the frame through rather than guessing.
	DeviceAddr() string
}

// DeviceAddr implements [DeviceScopedPayload].
func (p DataPointValueChangedPayload) DeviceAddr() string { return p.DeviceAddress }

// DeviceAddr implements [DeviceScopedPayload].
func (p CustomDataPointStateChangedPayload) DeviceAddr() string { return p.DeviceAddress }

// DeviceAddr implements [DeviceScopedPayload].
func (p DeviceCreatedPayload) DeviceAddr() string { return p.DeviceAddress }

// DeviceAddr implements [DeviceScopedPayload].
func (p DeviceReleasedPayload) DeviceAddr() string { return p.DeviceAddress }

// DeviceAddr implements [DeviceScopedPayload].
func (p DeviceRemovedPayload) DeviceAddr() string { return p.DeviceAddress }

// DeviceAddr implements [DeviceScopedPayload].
func (p DeviceAvailabilityChangedPayload) DeviceAddr() string { return p.DeviceAddress }

// DeviceAddr implements [DeviceScopedPayload].
func (p DeviceTriggerPayload) DeviceAddr() string { return p.DeviceAddress }

// DeviceAddr implements [DeviceScopedPayload].
func (p OptimisticRollbackPayload) DeviceAddr() string { return p.DeviceAddress }

// DeviceMetadataChangedPayload is the `device.metadata_changed` broadcast
// payload. Address is always the DEVICE address even when a channel was
// renamed, because a client materialises a device's name and area as one
// unit and re-reads the whole device.
//
// It lives here rather than in the adapter that publishes it so both this
// filter and the payload-field parity guard can see it; as an unexported
// adapter struct it was invisible to each.
type DeviceMetadataChangedPayload struct {
	Central       string `json:"central"`
	InterfaceID   string `json:"interface_id"`
	DeviceAddress string `json:"device_address"`
}

// DeviceAddr implements [DeviceScopedPayload].
func (p DeviceMetadataChangedPayload) DeviceAddr() string { return p.DeviceAddress }

// ScheduleChangedPayload is the `schedules.changed` broadcast payload. The
// profile body is deliberately not inlined — a week profile is large and
// most subscribers only need to invalidate and re-read.
//
// Here rather than in the adapter for the same reason as
// [DeviceMetadataChangedPayload].
type ScheduleChangedPayload struct {
	Central       string `json:"central"`
	InterfaceID   string `json:"interface_id"`
	DeviceAddress string `json:"device_address"`
	Channel       int    `json:"channel"`
}

// DeviceAddr implements [DeviceScopedPayload].
func (p ScheduleChangedPayload) DeviceAddr() string { return p.DeviceAddress }

// DeviceAssociatedPayload is a hub entity — a system variable, a program —
// that names a device it relates to without being about that device.
//
// The distinction matters for the onboarding filter and it is not
// cosmetic. Such an entity exists on the CCU independently of whether the
// device has been released here, so withholding the frame would take away
// something the operator has: the sysvar vanishes until an unrelated
// device finishes onboarding. Passing it through unchanged is no better —
// a filtering client attaches the entity to a device it does not have, and
// either drops it or invents a phantom device.
//
// So the association is stripped instead. The payload contract already
// defines what an absent one means ("clients then attach the entity to the
// central hub device"), so the frame degrades into a shape every client
// already handles rather than into a special case.
type DeviceAssociatedPayload interface {
	// AssociatedDeviceAddr returns the device this hub entity relates to,
	// or "" when it relates to none.
	AssociatedDeviceAddr() string
	// WithoutDeviceAssociation returns a copy with the device association
	// cleared, for a subscriber that cannot see the device.
	WithoutDeviceAssociation() any
}

// AssociatedDeviceAddr implements [DeviceAssociatedPayload].
func (p SysvarChangedPayload) AssociatedDeviceAddr() string { return p.DeviceAddress }

// WithoutDeviceAssociation implements [DeviceAssociatedPayload].
func (p SysvarChangedPayload) WithoutDeviceAssociation() any {
	p.DeviceAddress = ""
	p.Channel = ""
	return p
}

// AssociatedDeviceAddr implements [DeviceAssociatedPayload].
func (p ProgramExecutedPayload) AssociatedDeviceAddr() string { return p.DeviceAddress }

// WithoutDeviceAssociation implements [DeviceAssociatedPayload].
func (p ProgramExecutedPayload) WithoutDeviceAssociation() any {
	p.DeviceAddress = ""
	p.Channel = ""
	return p
}

// AssociatedDeviceAddr implements [DeviceAssociatedPayload].
func (p ProgramChangedPayload) AssociatedDeviceAddr() string { return p.DeviceAddress }

// WithoutDeviceAssociation implements [DeviceAssociatedPayload].
func (p ProgramChangedPayload) WithoutDeviceAssociation() any {
	p.DeviceAddress = ""
	p.Channel = ""
	return p
}
