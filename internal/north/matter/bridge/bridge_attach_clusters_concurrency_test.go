// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

// White-box test for the concurrency contract of AttachRootClusters /
// AttachAggregatorClusters: the daemon attaches the root and aggregator
// cluster sets while the bridge is already listening, so a commissioning
// read can land on the live topology at the same instant.

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/matterport"
)

// TestAttachClustersConcurrentWithLiveRead exercises the real bring-up
// ordering: the bridge is started (so the topology and its dispatcher are
// live and reachable over the wire) and the daemon then attaches the root
// and aggregator cluster servers. A commissioner reading endpoint 0 / 1 at
// that moment resolves the very endpoints the attach mutates.
//
// Run under -race this fails whenever the attach writes the endpoint's
// cluster-server slice without publishing it atomically.
func TestAttachClustersConcurrentWithLiveRead(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)

	b.mu.RLock()
	dispatcher := b.dispatcher
	b.mu.RUnlock()
	if dispatcher == nil {
		t.Fatal("started bridge has no dispatcher")
	}

	rootPath := im.ConcreteAttributePath{Endpoint: 0, HasEndpoint: true}
	aggPath := im.ConcreteAttributePath{Endpoint: 1, HasEndpoint: true}

	const rounds = 300
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := range rounds {
			b.AttachRootClusters([]matterport.ClusterServer{&noopCluster{id: 0x0028 + uint32(i%2)}})
			b.AttachAggregatorClusters([]matterport.ClusterServer{&noopCluster{id: 0x001D}})
		}
	}()

	go func() {
		defer wg.Done()
		ctx := context.Background()
		for range rounds {
			dispatcher.Read(ctx, rootPath)
			dispatcher.Read(ctx, aggPath)
		}
	}()

	wg.Wait()

	// The publication has to stay VISIBLE to the dispatcher: a fix that
	// parks the set where no reader loads it would silence the race and
	// leave endpoint 0 / 1 answering UnsupportedCluster for every read.
	b.AttachRootClusters([]matterport.ClusterServer{&noopCluster{id: 0x0028}})
	b.AttachAggregatorClusters([]matterport.ClusterServer{&noopCluster{id: 0x001D}})
	for _, tc := range []struct {
		name    string
		path    im.ConcreteAttributePath
		cluster uint32
	}{
		{"root", rootPath, 0x0028},
		{"aggregator", aggPath, 0x001D},
	} {
		path := tc.path
		path.Cluster, path.HasCluster = tc.cluster, true
		results := dispatcher.Read(context.Background(), path)
		if len(results) == 0 {
			t.Errorf("%s: read of the attached cluster 0x%04X returned no results", tc.name, tc.cluster)
			continue
		}
		for _, res := range results {
			if res.Status == im.StatusUnsupportedCluster {
				t.Errorf("%s: attached cluster 0x%04X reads as UnsupportedCluster", tc.name, tc.cluster)
				break
			}
		}
	}
}
