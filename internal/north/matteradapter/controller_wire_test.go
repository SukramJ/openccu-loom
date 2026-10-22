// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/client/interaction"
	onoffdef "github.com/SukramJ/go-fabric/cluster/spec/onoff"
	temperaturedef "github.com/SukramJ/go-fabric/cluster/spec/temperaturemeasurement"
	"github.com/SukramJ/go-fabric/endpoint"
	"github.com/SukramJ/go-fabric/fabrictest"
	"github.com/SukramJ/go-fabric/im"

	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// TestGoldenFleetAnswersAControllerOverCASE commissions an in-process node
// that serves the assembler's golden topology as its host Snapshotter and
// reads both endpoint shapes over CASE with go-fabric's own controller: a
// profile server declared as its cluster's HostServer (the plug's OnOff)
// and a sensor built from endpoint.Spec.Measurement (the climate sensor's
// MeasuredValue). Reading OnOff on the sensor endpoint must fail with
// UnsupportedCluster, so a read that answers anything for any path would
// not pass.
func TestGoldenFleetAnswersAControllerOverCASE(t *testing.T) {
	t.Parallel()
	asm, err := matteradapter.New(newFakeStore(), goldenConfig(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fleet := goldenFleet(t)
	var (
		mu   sync.Mutex
		last *endpoint.Topology
	)
	snapshot := func(ctx context.Context) (*endpoint.Topology, error) {
		top, err := asm.AssembleDevices(ctx, []matteradapter.DeviceSnapshot{{
			CentralName:   goldenCentralName,
			Devices:       fleet,
			ModelComplete: true,
		}})
		mu.Lock()
		last = top
		mu.Unlock()
		return top, err
	}
	site := fabrictest.NewSite(t, fabrictest.Options{})
	pair := site.CommissionedPair(fabrictest.PairOptions{
		Device: fabrictest.Topology{Snapshotter: snapshot},
	})
	mu.Lock()
	top := last
	mu.Unlock()
	plug := findByDeviceType(t, top, 0x010A).ID   // OnOffPlugInUnit
	sensor := findByDeviceType(t, top, 0x0302).ID // TemperatureSensor

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
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
	err = read(sensor, onoffdef.ClusterID, onoffdef.AttrOnOff)
	if !errors.As(err, &status) || status.Status != im.StatusUnsupportedCluster {
		t.Fatalf("read OnOff on the temperature sensor = %v, want UnsupportedCluster", err)
	}
}
