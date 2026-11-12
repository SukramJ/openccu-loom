// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"log/slog"
	"testing"

	fabricendpoint "github.com/SukramJ/go-fabric/endpoint"

	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/internal/north/projection"
)

// oneEndpoint is an endpoint store that holds a record for one key.
type oneEndpoint struct {
	key string
	rec fabricendpoint.Record
}

func (o oneEndpoint) GetEndpoint(_ context.Context, key fabricendpoint.SourceKey) (fabricendpoint.Record, error) {
	if key.String() == o.key {
		return o.rec, nil
	}
	return fabricendpoint.Record{}, fabricendpoint.ErrNotFound
}

// A bridge that is switched off answers that the plane is inactive, with
// no entries: nothing is published, and nothing is individually withheld.
func TestProjectionPlaneOfADisabledBridgeIsInactive(t *testing.T) {
	fleet := goldenFleet(t)
	view, err := matteradapter.NewProjectionPlane(nil, nil).Project(context.Background(), goldenCentralName, fleet, fleet[0])
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if view.State != projection.StateInactive || view.Reason != projection.ReasonPlaneDisabled || len(view.Entries) != 0 {
		t.Fatalf("view = %+v; want inactive, %s, no entries", view, projection.ReasonPlaneDisabled)
	}
}

// The plane's published entries carry the source key, and the endpoint
// number and device type only where the bridge holds a record.
func TestProjectionPlaneNamesWhatTheBridgeAssigned(t *testing.T) {
	ctx := context.Background()
	fleet := goldenFleet(t)
	a := matteradapter.New(goldenConfig(t), slog.New(slog.DiscardHandler))

	bare, err := matteradapter.NewProjectionPlane(a, nil).Project(ctx, goldenCentralName, fleet, fleet[0])
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	var assigned string
	published := 0
	for _, e := range bare.Entries {
		if e.Verdict != projection.VerdictPublished {
			if e.Identity != nil {
				t.Errorf("%d/%s/%s is %s and carries an identity", e.Channel, e.Kind, e.Key, e.Verdict)
			}
			continue
		}
		published++
		key := e.Identity[matteradapter.ProjectionIdentitySourceKey]
		if key == "" {
			t.Fatalf("published entry %d/%s/%s has no source key", e.Channel, e.Kind, e.Key)
		}
		if _, ok := e.Identity[matteradapter.ProjectionIdentityEndpointID]; ok {
			t.Errorf("%s names an endpoint id without an endpoint store", key)
		}
		if assigned == "" {
			assigned = key
		}
	}
	if published < 2 {
		t.Fatalf("the device published %d entries; the test needs two to tell assigned from unassigned", published)
	}

	store := oneEndpoint{key: assigned, rec: fabricendpoint.Record{EndpointID: 7, DeviceType: 0x010A}}
	withStore, err := matteradapter.NewProjectionPlane(a, store).Project(ctx, goldenCentralName, fleet, fleet[0])
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	for _, e := range withStore.Entries {
		if e.Verdict != projection.VerdictPublished {
			continue
		}
		id, has := e.Identity[matteradapter.ProjectionIdentityEndpointID]
		if e.Identity[matteradapter.ProjectionIdentitySourceKey] == assigned {
			if id != "7" || e.Identity[matteradapter.ProjectionIdentityDeviceType] == "" {
				t.Errorf("%s: identity %v; want endpoint 7 and a device type", assigned, e.Identity)
			}
			continue
		}
		if has {
			t.Errorf("an entry the store has no record for names endpoint %s", id)
		}
	}
}
