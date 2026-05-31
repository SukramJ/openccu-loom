// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package endpoint

import (
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
)

// ReportablePaths returns the concrete (endpoint, cluster, attribute)
// triples that should be marked dirty on the Subscribe engine whenever
// the underlying measurement source publishes a fresh value. The bridge
// uses this at endpoint-mount time to wire MatterChangeNotifier sources
// into Manager.OnAttributeChanged.
//
// The set is built by walking [ClusterServers] for ep and asking each
// cluster server for its MatterReportable() attribute list (the same
// list the dispatcher already uses to advertise subscribable
// attributes). Returns an empty slice for the root and aggregator
// endpoints — those carry their own change-emission paths (e.g.
// BridgedDeviceBasicInformation.SetReachable fires
// ReachableChanged) and do not flow through the measurement-source
// push path.
func ReportablePaths(ep *Endpoint) []im.ConcreteAttributePath {
	if ep == nil || ep.IsRoot() || ep.IsAggregator() {
		return nil
	}
	servers := ClusterServers(ep)
	if len(servers) == 0 {
		return nil
	}
	var paths []im.ConcreteAttributePath
	for _, srv := range servers {
		if srv == nil {
			continue
		}
		for _, attrID := range srv.MatterReportable() {
			paths = append(paths, im.ConcreteAttributePath{
				Endpoint:     ep.ID,
				Cluster:      srv.MatterClusterID(),
				Attribute:    attrID,
				HasEndpoint:  true,
				HasCluster:   true,
				HasAttribute: true,
			})
		}
	}
	return paths
}
