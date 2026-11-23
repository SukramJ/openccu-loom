// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"strings"
	"testing"

	"thingfabric.dev/go-fabric/endpoint"

	"github.com/SukramJ/openccu-loom/internal/model/device"
)

func identityDevice(model, firmware string) *device.Device {
	return device.New(device.Config{
		Address:  "IDT0001",
		Model:    model,
		Firmware: device.FirmwareInfo{Current: firmware},
	})
}

// TestStateIdentityLeavesAnEmptyFirmwareUnstated pins that a device whose
// firmware the CCU has not reported states no SoftwareVersionString: the
// attribute's minimum length is 1, so an empty string would refuse the
// whole endpoint.
func TestStateIdentityLeavesAnEmptyFirmwareUnstated(t *testing.T) {
	t.Parallel()
	var spec endpoint.Spec
	stateIdentity(&spec, identityDevice("HmIP-PSM", ""))
	if got := spec.BridgedBasicInformation.SoftwareVersionString; got != nil {
		t.Fatalf("SoftwareVersionString=%q, want unstated for an empty firmware", *got)
	}
	if got := spec.BridgedBasicInformation.ProductName; got == nil || *got != "HmIP-PSM" {
		t.Fatalf("ProductName=%v, want the model stated", got)
	}
}

// TestStateIdentityLeavesAnEmptyModelUnstated pins that a device without a
// model states neither name: ProductName and ProductLabel have no minimum
// length, so only the emptiness check keeps an empty string off the wire.
func TestStateIdentityLeavesAnEmptyModelUnstated(t *testing.T) {
	t.Parallel()
	var spec endpoint.Spec
	stateIdentity(&spec, identityDevice("", "1.0.0"))
	st := spec.BridgedBasicInformation
	if st.ProductName != nil || st.ProductLabel != nil {
		t.Fatalf("ProductName=%v ProductLabel=%v, want both unstated for an empty model", st.ProductName, st.ProductLabel)
	}
}

// TestStateIdentityDropsAValueThatBreaksItsConstraint pins that a model
// longer than ProductName's 32 bytes leaves ProductName unstated while
// ProductLabel (64 bytes) still carries it.
func TestStateIdentityDropsAValueThatBreaksItsConstraint(t *testing.T) {
	t.Parallel()
	model := strings.Repeat("M", 33)
	var spec endpoint.Spec
	stateIdentity(&spec, identityDevice(model, "1.0.0"))
	st := spec.BridgedBasicInformation
	if st.ProductName != nil {
		t.Errorf("ProductName=%q, want unstated for a 33-byte model", *st.ProductName)
	}
	if st.ProductLabel == nil || *st.ProductLabel != model {
		t.Errorf("ProductLabel=%v, want the 33-byte model stated (limit 64)", st.ProductLabel)
	}
}

// TestStateIdentityKeepsAnAlreadyStatedValue pins that a value the spec
// already states is not overwritten by the device model.
func TestStateIdentityKeepsAnAlreadyStatedValue(t *testing.T) {
	t.Parallel()
	own := "Custom name"
	var spec endpoint.Spec
	spec.BridgedBasicInformation.ProductName = &own
	stateIdentity(&spec, identityDevice("HmIP-PSM", "1.0.0"))
	if got := spec.BridgedBasicInformation.ProductName; got == nil || *got != own {
		t.Fatalf("ProductName=%v, want the pre-stated %q kept", got, own)
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
