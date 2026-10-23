// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

import (
	"fmt"
	"testing"

	fabricdevice "github.com/SukramJ/go-fabric/device"

	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// matterProjectionOf returns the device type a custom data point
// advertises and the cluster servers its endpoint carries: the servers of
// the device a declaring source builds (interfaces.MatterDeviceSource),
// or the ones a serving source returns itself. ok is false for a data
// point that projects nothing.
func matterProjectionOf(t *testing.T, cdp any, witness string) (deviceType uint16, servers []interfaces.MatterClusterServer, ok bool) {
	t.Helper()
	switch src := cdp.(type) {
	case interfaces.MatterDeviceSource:
		d, err := src.MatterDevice(fabricdevice.Options{StableKey: fabricdevice.StringKey(witness), Name: witness})
		if err != nil {
			t.Errorf("%s (%T): the device kit refused the source's device: %v", witness, cdp, err)
			return src.MatterDeviceType(), nil, true
		}
		if d == nil {
			return 0, nil, false
		}
		return src.MatterDeviceType(), d.Servers(), true
	case interfaces.MatterEndpointSource:
		return src.MatterDeviceType(), src.MatterClusterServers(), true
	default:
		return 0, nil, false
	}
}

// projectionWitness names a channel's custom data point in a finding.
func projectionWitness(model string, channel int, cdp any) string {
	return fmt.Sprintf("%s ch%d %T", model, channel, cdp)
}
