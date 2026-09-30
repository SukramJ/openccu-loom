// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// IdentifyDevicesMissingParamsets reports only channels whose own description
// declares MASTER or VALUES while the paramset registry holds neither: a
// channel that declares none of them can never be healed by a fetch.
func TestIdentifyDevicesMissingParamsetsHonoursDeclaredParamsets(t *testing.T) {
	dc, _, _, descs, psets := newDCFull(t)
	iface := wireKey(hmenum.InterfaceHmIPRF)

	descList := []hmproto.DeviceDescription{
		// Root addresses are never reported, declared or not.
		{Address: "DEV1", Type: "HmIP-PS", Paramsets: []string{"MASTER"}},
		// Declares VALUES, registry empty: missing.
		{Address: "DEV1:1", Parent: "DEV1", Paramsets: []string{"LINK", "MASTER", "VALUES"}},
		// Declares MASTER only, registry empty: missing.
		{Address: "DEV1:2", Parent: "DEV1", Paramsets: []string{"MASTER"}},
		// Declares VALUES, registry holds it: present.
		{Address: "DEV1:3", Parent: "DEV1", Paramsets: []string{"MASTER", "VALUES"}},
		// Declares nothing: never missing.
		{Address: "DEV1:4", Parent: "DEV1"},
		// Declares only LINK: never missing.
		{Address: "DEV1:5", Parent: "DEV1", Paramsets: []string{"LINK"}},
	}
	for i := range descList {
		descs.Put(iface, descList[i])
	}
	psets.Put(iface, "DEV1:3", hmenum.ParamsetKeyValues, hmproto.Paramset{})

	got := dc.IdentifyDevicesMissingParamsets(iface)
	slices.Sort(got)
	want := []string{"DEV1:1", "DEV1:2"}
	if !slices.Equal(got, want) {
		t.Fatalf("IdentifyDevicesMissingParamsets = %v, want %v", got, want)
	}
}
