// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
)

func identityDevice(model, firmware string) *device.Device {
	return device.New(device.Config{
		Address:  "IDT0001",
		Model:    model,
		Firmware: device.FirmwareInfo{Current: firmware},
	})
}

// TestDeviceIdentityLeavesAnEmptyFirmwareUnstated pins that a device whose
// firmware the CCU has not reported states no SoftwareVersionString: the
// attribute's minimum length is 1, so an empty string would refuse the
// whole device.
func TestDeviceIdentityLeavesAnEmptyFirmwareUnstated(t *testing.T) {
	t.Parallel()
	id := deviceIdentity(identityDevice("HmIP-PSM", ""))
	if id.SoftwareVersionString != "" {
		t.Fatalf("SoftwareVersionString=%q, want unstated for an empty firmware", id.SoftwareVersionString)
	}
	if id.ProductName != "HmIP-PSM" {
		t.Fatalf("ProductName=%q, want the model stated", id.ProductName)
	}
}

// TestDeviceIdentityLeavesAnEmptyModelUnstated pins that a device without a
// model states neither name: ProductName and ProductLabel have no minimum
// length, so only the emptiness check keeps an empty string off the wire.
func TestDeviceIdentityLeavesAnEmptyModelUnstated(t *testing.T) {
	t.Parallel()
	id := deviceIdentity(identityDevice("", "1.0.0"))
	if id.ProductName != "" || id.ProductLabel != "" {
		t.Fatalf("ProductName=%q ProductLabel=%q, want both unstated for an empty model", id.ProductName, id.ProductLabel)
	}
}

// TestDeviceIdentityDropsAValueThatBreaksItsConstraint pins that a model
// longer than ProductName's 32 bytes leaves ProductName unstated while
// ProductLabel (64 bytes) still carries it.
func TestDeviceIdentityDropsAValueThatBreaksItsConstraint(t *testing.T) {
	t.Parallel()
	model := strings.Repeat("M", 33)
	id := deviceIdentity(identityDevice(model, "1.0.0"))
	if id.ProductName != "" {
		t.Errorf("ProductName=%q, want unstated for a 33-byte model", id.ProductName)
	}
	if id.ProductLabel != model {
		t.Errorf("ProductLabel=%q, want the 33-byte model stated (limit 64)", id.ProductLabel)
	}
}

// TestStatedIdentityValueChangedLeavesOnlySurfaceDriftToTheNode pins which
// change of a stated string the reconciler raises the ConfigurationVersion
// for. A serial number or software version that appears or disappears
// changes the AttributeList, which the node raises for itself; a product name
// or label stays listed through the address fallback, so any change of it is
// invisible to the node and the reconciler must raise.
func TestStatedIdentityValueChangedLeavesOnlySurfaceDriftToTheNode(t *testing.T) {
	t.Parallel()
	full := statedIdentity{serialNumber: "0001ABCD", productName: "HmIP-PSM", productLabel: "HmIP-PSM", softwareVersion: "1.0.0"}
	with := func(edit func(*statedIdentity)) statedIdentity {
		s := full
		edit(&s)
		return s
	}
	cases := []struct {
		name       string
		prev, next statedIdentity
		want       bool
	}{
		{"unchanged", full, full, false},
		{"software version moves", full, with(func(s *statedIdentity) { s.softwareVersion = "1.2.0" }), true},
		{"software version appears", with(func(s *statedIdentity) { s.softwareVersion = "" }), full, false},
		{"software version disappears", full, with(func(s *statedIdentity) { s.softwareVersion = "" }), false},
		{"serial disappears", full, with(func(s *statedIdentity) { s.serialNumber = "" }), false},
		{"product name moves", full, with(func(s *statedIdentity) { s.productName = "HmIP-PSM-2" }), true},
		{"product name stated over the address fallback", with(func(s *statedIdentity) { s.productName = "" }), full, true},
		{"product label falls back to the address", full, with(func(s *statedIdentity) { s.productLabel = "" }), true},
	}
	for _, c := range cases {
		if got := c.prev.valueChanged(c.next); got != c.want {
			t.Errorf("%s: valueChanged = %v, want %v", c.name, got, c.want)
		}
	}
}
