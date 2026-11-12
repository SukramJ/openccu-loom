// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package projection_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/projection"
)

type fakeSource struct {
	name string
	view projection.PlaneView
	err  error
}

func (f fakeSource) Plane() string { return f.name }

func (f fakeSource) Project(context.Context, string, []*device.Device, *device.Device) (projection.PlaneView, error) {
	return f.view, f.err
}

func TestServiceAsksEveryPlaneInOrder(t *testing.T) {
	svc := projection.New(
		fakeSource{name: "a", view: projection.PlaneView{Plane: "a", State: projection.StateActive}},
		nil,
		fakeSource{name: "b", view: projection.PlaneView{Plane: "b", State: projection.StateInactive, Reason: projection.ReasonPlaneDisabled}},
	)
	if got := svc.Planes(); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("Planes = %v", got)
	}
	out, err := svc.Project(context.Background(), "ccu", nil, &device.Device{Address: "X"}, "")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if out.Address != "X" || out.Central != "ccu" || len(out.Planes) != 2 || out.Planes[0].Plane != "a" || out.Planes[1].Plane != "b" {
		t.Fatalf("out = %+v", out)
	}
	for _, p := range out.Planes {
		if p.Entries == nil {
			t.Errorf("plane %s has nil entries; the wire form would be null", p.Plane)
		}
	}
}

func TestServiceNarrowsToOnePlane(t *testing.T) {
	svc := projection.New(fakeSource{name: "a", view: projection.PlaneView{Plane: "a"}}, fakeSource{name: "b", view: projection.PlaneView{Plane: "b"}})
	out, err := svc.Project(context.Background(), "ccu", nil, &device.Device{Address: "X"}, "b")
	if err != nil || len(out.Planes) != 1 || out.Planes[0].Plane != "b" {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
	out, err = svc.Project(context.Background(), "ccu", nil, &device.Device{Address: "X"}, "nope")
	if err != nil || out.Planes == nil || len(out.Planes) != 0 {
		t.Fatalf("unknown plane: out = %+v, err = %v", out, err)
	}
}

func TestServiceReportsAPlaneThatFails(t *testing.T) {
	boom := errors.New("boom")
	_, err := projection.New(fakeSource{name: "a", err: boom}).Project(context.Background(), "ccu", nil, &device.Device{Address: "X"}, "")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v; want it to wrap the plane's error", err)
	}
}
