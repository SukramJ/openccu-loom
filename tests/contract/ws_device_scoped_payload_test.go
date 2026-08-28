// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// payloadsWithoutDeviceScope are broadcast payloads that carry a
// `device_address` field and deliberately implement NEITHER interface.
//
// Each entry states why. There is currently one shape: a payload where
// the address is neither the subject nor an association a subscriber
// would attach an entity to. Add an entry only with a reason that would
// survive a reader asking "so what happens to a filtering client?".
var payloadsWithoutDeviceScope = map[string]string{}

// TestEveryDeviceScopedPayloadIsFilterable is the rule that carries
// itself.
//
// The onboarding filter used to match on a type switch, and the switch
// listed five of the ten broadcast payloads that name a device. The five
// it missed included the device-trigger frame: a client that turns those
// into automations would have fired them for a device it had explicitly
// asked not to see. A hand-maintained list is a list the next payload
// slips past; this guard makes the compiler and this test ask instead.
//
// Two interfaces, because there are two relationships:
//
//   - [ws.DeviceScopedPayload] — the payload is ABOUT the device, so a
//     filtering subscriber must not receive it at all;
//   - [ws.DeviceAssociatedPayload] — a hub entity (sysvar, program) that
//     merely names a device it relates to. It exists independently of
//     that device's onboarding, so withholding it would take away
//     something the operator has; the association is dropped instead.
func TestEveryDeviceScopedPayloadIsFilterable(t *testing.T) {
	t.Parallel()

	scoped := reflect.TypeOf((*ws.DeviceScopedPayload)(nil)).Elem()
	associated := reflect.TypeOf((*ws.DeviceAssociatedPayload)(nil)).Elem()

	var missing []string
	checked := 0
	for name, sample := range wsPayloadStructs {
		rt := reflect.TypeOf(sample)
		if rt == nil || rt.Kind() != reflect.Struct {
			continue
		}
		if !hasDeviceAddressField(rt) {
			continue
		}
		checked++
		if reason, exempt := payloadsWithoutDeviceScope[name]; exempt {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%s is exempted with an empty reason", name)
			}
			continue
		}
		if rt.Implements(scoped) || rt.Implements(associated) {
			continue
		}
		missing = append(missing, name)
	}

	if checked == 0 {
		t.Fatal("no payload with a device_address field was examined — the guard is measuring nothing")
	}
	if len(missing) > 0 {
		t.Errorf("%d broadcast payload(s) name a device but implement neither "+
			"ws.DeviceScopedPayload nor ws.DeviceAssociatedPayload:\n  %s\n\n"+
			"A released_only subscriber receives these for a device it asked not to see. "+
			"Implement DeviceAddr() when the payload is ABOUT the device, or "+
			"AssociatedDeviceAddr()/WithoutDeviceAssociation() when it is a hub entity "+
			"that merely names one.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// hasDeviceAddressField reports whether a payload struct carries a
// `device_address` JSON field, at any nesting depth an encoder would
// flatten.
func hasDeviceAddressField(rt reflect.Type) bool {
	for i := range rt.NumField() {
		f := rt.Field(i)
		tag := f.Tag.Get("json")
		if name, _, _ := strings.Cut(tag, ","); name == "device_address" {
			return true
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct && hasDeviceAddressField(f.Type) {
			return true
		}
	}
	return false
}
