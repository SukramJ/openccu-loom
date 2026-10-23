// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/client/interaction"
	onoffdef "github.com/SukramJ/go-fabric/cluster/spec/onoff"
	temperaturedef "github.com/SukramJ/go-fabric/cluster/spec/temperaturemeasurement"
	"github.com/SukramJ/go-fabric/fabrictest"
	"github.com/SukramJ/go-fabric/im"
	"github.com/SukramJ/go-fabric/node"

	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// TestGoldenFleetAnswersAControllerOverCASE reads the golden fleet over
// CASE as a controller does, the way the daemon serves it: the walk
// describes the fleet, the reconciler syncs it into the node's
// Aggregator under the central's partition, and the node assembles. One
// endpoint of each shape is read: a profile server declared with the
// device kit (the plug's OnOff) and a sensor declared with
// device.Measurement (the climate sensor's MeasuredValue). A cluster the
// sensor does not serve answers UnsupportedCluster, so the reads are
// not vacuous.
func TestGoldenFleetAnswersAControllerOverCASE(t *testing.T) {
	t.Parallel()
	asm := matteradapter.New(goldenConfig(t), slog.New(slog.DiscardHandler))
	site := fabrictest.NewSite(t, fabrictest.Options{})
	pair := site.CommissionedPair(fabrictest.PairOptions{})
	n := pair.Device.Node()
	reconciler := matteradapter.NewReconciler(n.Aggregator(), asm, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := reconciler.Sync(ctx, []matteradapter.DeviceSnapshot{{
		CentralName:   goldenCentralName,
		Devices:       goldenFleet(t),
		ModelComplete: true,
	}}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	topo := n.Topology()
	plug := endpointOfType(t, topo, 0x010A)   // OnOffPlugInUnit
	sensor := endpointOfType(t, topo, 0x0302) // TemperatureSensor

	read := func(ep uint16, cluster, attr uint32) error {
		_, err := fabrictest.Read(ctx, pair, im.ConcreteAttributePath{
			Endpoint: ep, Cluster: cluster, Attribute: attr,
			HasEndpoint: true, HasCluster: true, HasAttribute: true,
		})
		return err
	}
	if err := read(plug, onoffdef.ClusterID, onoffdef.AttrOnOff); err != nil {
		t.Fatalf("read OnOff on the plug (endpoint %d): %v", plug, err)
	}
	if err := read(sensor, temperaturedef.ClusterID, temperaturedef.AttrMeasuredValue); err != nil {
		t.Fatalf("read MeasuredValue on the temperature sensor (endpoint %d): %v", sensor, err)
	}
	var status *interaction.StatusError
	err := read(sensor, onoffdef.ClusterID, onoffdef.AttrOnOff)
	if !errors.As(err, &status) || status.Status != im.StatusUnsupportedCluster {
		t.Fatalf("read OnOff on the temperature sensor = %v, want UnsupportedCluster", err)
	}
}

// endpointOfType returns the endpoint number of the first bridged device
// of deviceType in the node's served topology.
func endpointOfType(t *testing.T, topo node.BridgeTopology, deviceType uint16) uint16 {
	t.Helper()
	for _, d := range topo.Devices {
		if d.DeviceType == deviceType {
			return d.Endpoint
		}
	}
	t.Fatalf("no bridged device of type 0x%04X among %d", deviceType, len(topo.Devices))
	return 0
}
