// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"net/url"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/projection"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Reasons the data-point list leaves a parameter out.
const (
	// ListingChannelHidden: the operator hid the channel, so its
	// operation list is empty.
	ListingChannelHidden = "channel_hidden"
	// ListingHiddenParameter: the parameter visibility rules leave the
	// parameter out.
	ListingHiddenParameter = "hidden_parameter"
)

// ListingIdentityPath is the identity key of a listed data point: its
// REST path.
const ListingIdentityPath = "path"

// dataPointListing is the rule of GET …/channels/{no}/data-points for one
// parameter of ch: the reason the default list leaves it out, or "". An
// operator-hidden channel (G12) lists nothing; otherwise the visibility
// set decides, asked with the channel number so MASTER's per-channel
// whitelist applies. A nil set lists everything. `?include=all` bypasses
// the rule and is not described by it.
func dataPointListing(ch *device.Channel, vis filter.VisibilitySet, param hmenum.Parameter) string {
	if ch.IsHidden() {
		return ListingChannelHidden
	}
	model := ""
	if d := ch.Device(); d != nil {
		model = d.Model
	}
	if vis != nil && !vis.VisibleForChannel(model, ch.Type, ch.Number, ch.ParamsetIn, param) {
		return ListingHiddenParameter
	}
	return ""
}

// DataPointListingPlane is the REST side of a device's projection: which
// data points the operation list of each channel carries, and the rule
// behind each one it leaves out. It asks the list handler's own rule.
type DataPointListingPlane struct {
	vis filter.VisibilitySet
}

// NewDataPointListingPlane returns the plane over the visibility set the
// list handler is mounted with.
func NewDataPointListingPlane(vis filter.VisibilitySet) *DataPointListingPlane {
	return &DataPointListingPlane{vis: vis}
}

// Plane implements [projection.Source].
func (*DataPointListingPlane) Plane() string { return projection.PlaneREST }

// Project implements [projection.Source]. The REST surface is the daemon
// itself, so the plane is always active.
func (p *DataPointListingPlane) Project(_ context.Context, _ string, _ []*device.Device, dev *device.Device) (projection.PlaneView, error) {
	view := projection.PlaneView{Plane: projection.PlaneREST, State: projection.StateActive, Entries: []projection.Entry{}}
	for _, ch := range dev.Channels() {
		if ch == nil || ch.Number == device.ChannelNumberDevice {
			continue
		}
		for _, dp := range ch.DataPoints() {
			param := dp.Parameter()
			e := projection.Entry{Channel: ch.Number, Key: string(param), Verdict: projection.VerdictPublished}
			if reason := dataPointListing(ch, p.vis, param); reason != "" {
				e.Verdict, e.Reason = projection.VerdictWithheld, reason
			} else {
				e.Identity = map[string]string{ListingIdentityPath: "/api/v1/devices/" + url.PathEscape(dev.Address) +
					"/channels/" + strconv.Itoa(ch.Number) + "/data-points/" + url.PathEscape(string(param))}
			}
			view.Entries = append(view.Entries, e)
		}
	}
	return view, nil
}
