// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"context"
	"errors"
	"strconv"

	fabricendpoint "github.com/SukramJ/go-fabric/endpoint"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	northprojection "github.com/SukramJ/openccu-loom/internal/north/projection"
	"github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Identity keys of a published Matter entry.
const (
	// ProjectionIdentitySourceKey is the persisted endpoint identity.
	ProjectionIdentitySourceKey = "source_key"
	// ProjectionIdentityEndpointID is the endpoint number, present once
	// the bridge has assigned one.
	ProjectionIdentityEndpointID = "endpoint_id"
	// ProjectionIdentityDeviceType is the Matter device type's name,
	// present with the endpoint number.
	ProjectionIdentityDeviceType = "device_type"
)

// EndpointLookup reads the endpoint record the bridge keeps for a
// source. The matter endpoint store satisfies it.
type EndpointLookup interface {
	GetEndpoint(ctx context.Context, key fabricendpoint.SourceKey) (fabricendpoint.Record, error)
}

// ProjectionPlane is the Matter side of a device's projection. It answers
// from [Assembler.DecideDevice], the pass the bridge's endpoints are
// described by.
type ProjectionPlane struct {
	assembler *Assembler
	endpoints EndpointLookup
}

// NewProjectionPlane returns the plane over the bridge's assembler and
// its endpoint records. A nil assembler is a bridge that is switched off:
// the plane then answers inactive.
func NewProjectionPlane(a *Assembler, endpoints EndpointLookup) *ProjectionPlane {
	return &ProjectionPlane{assembler: a, endpoints: endpoints}
}

// Plane implements [northprojection.Source].
func (*ProjectionPlane) Plane() string { return northprojection.PlaneMatter }

// Project implements [northprojection.Source].
func (p *ProjectionPlane) Project(ctx context.Context, central string, fleet []*device.Device, dev *device.Device) (northprojection.PlaneView, error) {
	view := northprojection.PlaneView{Plane: northprojection.PlaneMatter, State: northprojection.StateActive, Entries: []northprojection.Entry{}}
	if p == nil || p.assembler == nil {
		view.State = northprojection.StateInactive
		view.Reason = northprojection.ReasonPlaneDisabled
		return view, nil
	}
	decisions, err := p.assembler.DecideDevice(ctx, central, fleet, dev)
	if err != nil {
		return northprojection.PlaneView{}, err
	}
	for _, d := range decisions {
		e := northprojection.Entry{Channel: d.Key.ChannelNo, Kind: string(d.Key.DPKind), Key: d.Key.DPKey}
		switch d.Outcome {
		case OutcomePublished:
			e.Verdict = northprojection.VerdictPublished
			e.Identity, err = p.identity(ctx, d.Key)
			if err != nil {
				return northprojection.PlaneView{}, err
			}
		case OutcomeFolded:
			e.Verdict = northprojection.VerdictFolded
			e.FoldedInto = d.Into
		default:
			e.Verdict = northprojection.VerdictWithheld
			e.Reason = string(d.Reason)
		}
		view.Entries = append(view.Entries, e)
	}
	return view, nil
}

// identity is what a published source is called on the bridge. The
// endpoint number is missing until the bridge has taken the source in.
func (p *ProjectionPlane) identity(ctx context.Context, key matterendpoint.SourceKey) (map[string]string, error) {
	id := map[string]string{ProjectionIdentitySourceKey: key.String()}
	if p.endpoints == nil {
		return id, nil
	}
	rec, err := p.endpoints.GetEndpoint(ctx, key)
	if errors.Is(err, fabricendpoint.ErrNotFound) {
		return id, nil
	}
	if err != nil {
		return nil, err
	}
	id[ProjectionIdentityEndpointID] = strconv.FormatUint(uint64(rec.EndpointID), 10)
	id[ProjectionIdentityDeviceType] = interfaces.MatterDeviceTypeName(rec.DeviceType)
	return id, nil
}
