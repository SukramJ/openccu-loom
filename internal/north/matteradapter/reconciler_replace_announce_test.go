// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"sync"
	"testing"
	"time"

	bdbidef "thingfabric.dev/go-fabric/cluster/spec/bridgeddevicebasicinformation"
	onoffdef "thingfabric.dev/go-fabric/cluster/spec/onoff"
	"thingfabric.dev/go-fabric/fabrictest"
	"thingfabric.dev/go-fabric/im"

	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// clusterVersion reads endpoint ep and returns the DataVersion it serves
// for cluster, filtered by filters; ok is false when the read returned no
// attribute of the cluster (a filter that matched).
func (s *pairedSite) clusterVersion(t *testing.T, ep uint16, cluster uint32, filters ...im.DataVersionFilter) (version uint32, ok bool) {
	t.Helper()
	r, err := fabrictest.ReadWith(s.ctx, s.pair, fabrictest.ReadRequest{
		Attributes:         []im.ConcreteAttributePath{{Endpoint: ep, HasEndpoint: true, Cluster: cluster, HasCluster: true}},
		DataVersionFilters: filters,
	})
	if err != nil {
		t.Fatalf("read cluster 0x%04X: %v", cluster, err)
	}
	for i := range r.Attributes {
		if r.Attributes[i].Path.Cluster == cluster {
			return r.Attributes[i].DataVersion, true
		}
	}
	return 0, false
}

// TestARebuiltDeviceIsAnnouncedToItsController pins what a controller
// learns when a firmware update makes the source build its device anew and
// the reconciler replaces it under the same key: a running subscription is
// told the new SoftwareVersionString, and a read filtered by the
// DataVersion the controller cached before the update returns the device's
// clusters again, because the replaced endpoint's clusters carry new
// versions. A sync that changed nothing keeps the versions, so the same
// filter still hides the cluster.
func TestARebuiltDeviceIsAnnouncedToItsController(t *testing.T) {
	t.Parallel()
	s := newPairedSite(t)
	const addr = "SW0000301"
	dev := switchDevice(addr, "Lamp")
	dev.Model = "HmIP-PS"
	dev.Firmware().Set(device.FirmwareInfo{Current: "1.0.0"})
	s.sync(t, dev)
	ep := s.endpointOf(t, addr)

	var (
		mu       sync.Mutex
		versions []string
	)
	sub, err := fabrictest.Subscribe(s.ctx, s.pair, fabrictest.SubscribeRequest{
		Attributes: []im.ConcreteAttributePath{{
			Endpoint: ep, HasEndpoint: true,
			Cluster: bdbidef.ClusterID, HasCluster: true,
			Attribute: bdbidef.AttrSoftwareVersionString, HasAttribute: true,
		}},
		Updated: func(r *fabrictest.Report) error {
			mu.Lock()
			defer mu.Unlock()
			for i := range r.Attributes {
				versions = append(versions, r.Attributes[i].Value.El.String)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(sub.Close)
	reported := func() []string {
		if err := s.site.Step(s.ctx, 5*time.Second); err != nil {
			t.Fatalf("step: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), versions...)
	}

	cached, ok := s.clusterVersion(t, ep, onoffdef.ClusterID)
	if !ok {
		t.Fatal("the switch serves no OnOff cluster")
	}
	filter := im.DataVersionFilter{Endpoint: ep, Cluster: onoffdef.ClusterID, DataVersion: cached}

	s.sync(t, dev)
	if _, ok := s.clusterVersion(t, ep, onoffdef.ClusterID, filter); ok {
		t.Fatal("an unchanged sync moved the OnOff DataVersion; the filter check below would measure nothing")
	}
	primed := reported()

	dev.Firmware().Set(device.FirmwareInfo{Current: "1.1.0"})
	s.sync(t, dev)
	if got := s.endpointOf(t, addr); got != ep {
		t.Fatalf("endpoint after the rebuild = %d, want %d", got, ep)
	}
	if _, ok := s.clusterVersion(t, ep, onoffdef.ClusterID, filter); !ok {
		t.Error("a read filtered by the pre-update DataVersion hides the rebuilt device's OnOff cluster")
	}
	got := reported()[len(primed):]
	if len(got) == 0 || got[len(got)-1] != "1.1.0" {
		t.Errorf("SoftwareVersionString reports after the rebuild = %q, want the new version 1.1.0 reported", got)
	}
}
