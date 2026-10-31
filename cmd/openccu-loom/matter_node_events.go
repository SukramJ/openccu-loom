// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"fmt"
	"time"

	"github.com/SukramJ/go-fabric-ui/fabricui"
	"github.com/SukramJ/go-fabric/node"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// The WebSocket topics of the node page family's events: go-fabric-ui's
// event types (fabricui.Event*) under the `node.` prefix, with the
// payloads the SSE stream carries (assets/wsapi.json, category "node").
// The SPA reads the pages' events from the hub rather than from the
// handler's own stream, so one authenticated connection carries every
// server push.
const (
	// nodeTopicPrefix is what every node topic starts with; the event
	// type follows it unchanged.
	nodeTopicPrefix = "node."

	nodeTopicFabricAdded      = "node.fabric_added"
	nodeTopicFabricRemoved    = "node.fabric_removed"
	nodeTopicWindowOpened     = "node.window_opened"
	nodeTopicWindowClosed     = "node.window_closed"
	nodeTopicEndpointsChanged = "node.endpoints_changed"
	nodeTopicExposureChanged  = "node.exposure_changed"
	nodeTopicSurfaceChanged   = "node.surface_changed"
)

// nodeTopics maps go-fabric-ui's event types onto the hub topics. An
// event type the table does not name is not a broadcast of this daemon
// (assets/wsapi.json declares exactly these seven) and is dropped rather
// than published under a topic no client subscribed to.
var nodeTopics = map[string]string{
	fabricui.EventFabricAdded:      nodeTopicFabricAdded,
	fabricui.EventFabricRemoved:    nodeTopicFabricRemoved,
	fabricui.EventWindowOpened:     nodeTopicWindowOpened,
	fabricui.EventWindowClosed:     nodeTopicWindowClosed,
	fabricui.EventEndpointsChanged: nodeTopicEndpointsChanged,
	fabricui.EventExposureChanged:  nodeTopicExposureChanged,
	fabricui.EventSurfaceChanged:   nodeTopicSurfaceChanged,
}

// nodeAuditActor is the audit principal of a change the node reports
// without a request behind it: a controller removed through the node
// routes (whose principal the handler does not hand on) or over the wire
// (RemoveFabric by the controller itself).
const nodeAuditActor = "node"

// matterNodeObserved is what the bridge observes the node through;
// *node.Node is one.
type matterNodeObserved interface {
	Observe(o node.Observers) (unsubscribe func())
	Window() node.WindowSnapshot
}

// wireMatterNodeEvents bridges the node page family's events onto the
// WebSocket hub, one topic per event type, and records a removed fabric
// in the audit ledger. It returns the unsubscribe function.
//
// The events arrive on two paths. The endpoint and exposure changes are
// published into go-fabric-ui's event source (src) by the daemon and the
// exposure route. The fabric, window and surface transitions are not: go-fabric-ui
// observes the node for each open SSE stream alone and puts nothing into
// src, so the bridge observes the node itself (n) through go-fabric-ui's
// own conversion, fabricui.NodeObservers — publishing them into src
// instead would hand every SSE stream each transition twice.
//
// A fabric removal is the one node write whose principal the ledger
// cannot name: go-fabric-ui's routes (DELETE /node/fabrics/{index}, the
// factory reset) and a controller's own RemoveFabric both reach here as
// the same observer event, so the entry carries [nodeAuditActor].
func wireMatterNodeEvents(src *fabricui.Events, n matterNodeObserved, hub *ws.Hub, rec audit.Recorder) func() {
	sink := matterNodeEventSink(hub, rec)
	var unsubs []func()
	if src != nil {
		unsubs = append(unsubs, src.Subscribe(sink))
	}
	if n != nil {
		unsubs = append(unsubs, n.Observe(fabricui.NodeObservers(sink, n.Window().Open)))
	}
	return func() {
		for _, u := range unsubs {
			u()
		}
	}
}

// matterNodeEventSink publishes one node event under its hub topic and
// records a removed fabric in the audit ledger.
func matterNodeEventSink(hub *ws.Hub, rec audit.Recorder) func(fabricui.Event) {
	return func(ev fabricui.Event) {
		topic, known := nodeTopics[ev.Type]
		if hub != nil && known {
			hub.Publish(ws.Event{
				Topic:   topic,
				Type:    topic,
				When:    time.Now().UTC(),
				Payload: ev.Data,
			})
		}
		if rec != nil && ev.Type == fabricui.EventFabricRemoved {
			note := "fabric removed"
			if f, ok := ev.Data.(fabricui.FabricEvent); ok {
				note = fmt.Sprintf("fabric_index=%d", f.FabricIndex)
			}
			rec.Record(audit.Entry{User: nodeAuditActor, Action: audit.ActionMatterFabricRevoke, Note: note})
		}
	}
}
