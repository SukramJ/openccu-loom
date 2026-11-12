// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package measurements

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/projection"
)

// Reasons of an inactive plane.
const (
	// ReasonCentralExcluded is the plane's reason when forwarding runs
	// but not for the device's central.
	ReasonCentralExcluded = "central_excluded"
)

// Identity keys of a forwarded data point: the measurement and the tags
// the exporter writes its samples under.
const (
	ProjectionIdentityMeasurement = "measurement"
	ProjectionIdentityCentral     = "central"
	ProjectionIdentityInterfaceID = "interface_id"
	ProjectionIdentityChannel     = "channel"
	ProjectionIdentityParameter   = "parameter"
)

// measurementNamer is the exporter side of the identity. The InfluxDB
// exporter satisfies it.
type measurementNamer interface {
	MeasurementName() string
}

// ProjectionPlane is the measurement side of a device's projection. It
// answers with the forwarder's own rules: the recording policy and the
// provenance guard its event handler applies.
type ProjectionPlane struct {
	f *Forwarder
}

// NewProjectionPlane returns the plane over f. A nil forwarder, or one
// without an exporter, is forwarding switched off: the plane then answers
// inactive.
func NewProjectionPlane(f *Forwarder) *ProjectionPlane { return &ProjectionPlane{f: f} }

// Plane implements [projection.Source].
func (*ProjectionPlane) Plane() string { return projection.PlaneMeasurements }

// Project implements [projection.Source]. It lists the data points of the
// device's VALUES paramsets; nothing else is ever forwarded.
func (p *ProjectionPlane) Project(_ context.Context, central string, _ []*device.Device, dev *device.Device) (projection.PlaneView, error) {
	view := projection.PlaneView{Plane: projection.PlaneMeasurements, State: projection.StateActive, Entries: []projection.Entry{}}
	if p == nil || p.f == nil || p.f.exporter == nil {
		view.State, view.Reason = projection.StateInactive, projection.ReasonPlaneDisabled
		return view, nil
	}
	if !p.f.enabledFor(central) {
		view.State, view.Reason = projection.StateInactive, ReasonCentralExcluded
		return view, nil
	}
	for _, ch := range dev.Channels() {
		if ch == nil {
			continue
		}
		for _, dp := range ch.DataPoints() {
			key := dp.DataPointKey()
			e := projection.Entry{Channel: ch.Number, Key: key.Parameter, Verdict: projection.VerdictWithheld}
			if reason := p.f.decide(central, dp); reason != "" {
				e.Reason = string(reason)
			} else {
				e.Verdict = projection.VerdictPublished
				e.Identity = p.identity(central, key.InterfaceID, key.ChannelAddress, key.Parameter)
			}
			view.Entries = append(view.Entries, e)
		}
	}
	return view, nil
}

func (p *ProjectionPlane) identity(central, iface, channel, parameter string) map[string]string {
	id := map[string]string{
		ProjectionIdentityCentral:     central,
		ProjectionIdentityInterfaceID: iface,
		ProjectionIdentityChannel:     channel,
		ProjectionIdentityParameter:   parameter,
	}
	if n, ok := p.f.exporter.(measurementNamer); ok {
		id[ProjectionIdentityMeasurement] = n.MeasurementName()
	}
	return id
}

// decide is the forwarder's standing answer for one data point: the
// reason its next value would not be forwarded, or "". It applies the
// rules of the event handler to the value the model holds: a value that
// is not a number, the recording policy, the provenance guard.
func (f *Forwarder) decide(central string, dp device.ParameterDataPoint) WithheldReason {
	key := dp.DataPointKey()
	raw, observed := dp.RawValue()
	if observed && !isNumeric(raw) {
		return WithheldNotNumeric
	}
	if reason := f.policy(central, key.InterfaceID, key.ChannelAddress, key.Parameter); reason != "" {
		return reason
	}
	if !observed {
		return WithheldNoLiveValue
	}
	return provenance(dp)
}

// isNumeric reports whether a data point's raw value is a number. v is
// any because the model stores each parameter in its own Go type.
func isNumeric(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return true
	default:
		return false
	}
}
