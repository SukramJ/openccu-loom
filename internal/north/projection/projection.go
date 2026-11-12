// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// Package projection answers, for one device, how it stands on each
// north-bound plane: what the plane publishes under which identifiers,
// what it carries inside another entry, and what it leaves out and why.
//
// A plane answers from the same decision its publisher acts on, so the
// answer and the published set are one set of rules. This package owns
// only the walk over the planes and the common shape; no rule lives here.
package projection

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// Plane names.
const (
	PlaneMatter       = "matter"
	PlaneMeasurements = "measurements"
	PlaneHADiscovery  = "ha_discovery"
	PlaneMQTTRaw      = "mqtt_raw"
	PlaneREST         = "rest"
)

// Plane states.
const (
	// StateActive: the plane runs for the device's central.
	StateActive = "active"
	// StateInactive: it does not, for [PlaneView.Reason].
	StateInactive = "inactive"
)

// ReasonPlaneDisabled is the reason of a plane that is switched off.
const ReasonPlaneDisabled = "plane_disabled"

// Verdicts of an entry.
const (
	VerdictPublished = "published"
	VerdictFolded    = "folded"
	VerdictWithheld  = "withheld"
)

// Device is the projection of one device.
type Device struct {
	Address string      `json:"address"`
	Central string      `json:"central"`
	Planes  []PlaneView `json:"planes"`
}

// PlaneView is one plane's answer for the device.
type PlaneView struct {
	Plane string `json:"plane"`
	State string `json:"state"`
	// Reason is set when State is [StateInactive].
	Reason  string  `json:"reason,omitempty"`
	Entries []Entry `json:"entries"`
}

// Entry is one subject the plane could carry: a whole channel (Kind
// empty) or one source on it.
type Entry struct {
	Channel int `json:"channel"`
	// Kind and Key name the subject in the plane's own vocabulary.
	Kind    string `json:"kind,omitempty"`
	Key     string `json:"key,omitempty"`
	Verdict string `json:"verdict"`
	// Reason is set when Verdict is [VerdictWithheld]: one value of the
	// plane's closed set.
	Reason string `json:"reason,omitempty"`
	// FoldedInto is set when Verdict is [VerdictFolded]: the key of the
	// entry that carries this subject.
	FoldedInto string `json:"folded_into,omitempty"`
	// Identity is what the subject is called on the plane. Set for a
	// published entry; the keys are the plane's.
	Identity map[string]string `json:"identity,omitempty"`
}

// Source is one plane's side of the projection. Implementations answer
// from the decision their publisher acts on.
type Source interface {
	// Plane is the plane's name.
	Plane() string
	// Project answers for dev on central. fleet is the central's device
	// list, for planes whose naming depends on the device's neighbours.
	Project(ctx context.Context, central string, fleet []*device.Device, dev *device.Device) (PlaneView, error)
}

// Service asks every plane about one device.
type Service struct {
	sources []Source
}

// New returns a service over sources, in the order given. A nil source
// is skipped.
func New(sources ...Source) *Service {
	s := &Service{}
	for _, src := range sources {
		if src != nil {
			s.sources = append(s.sources, src)
		}
	}
	return s
}

// Project is the projection of dev. plane narrows it to one plane; ""
// asks all of them. An unknown plane name yields no plane view.
func (s *Service) Project(ctx context.Context, central string, fleet []*device.Device, dev *device.Device, plane string) (Device, error) {
	out := Device{Address: dev.Address, Central: central, Planes: []PlaneView{}}
	for _, src := range s.sources {
		if plane != "" && src.Plane() != plane {
			continue
		}
		v, err := src.Project(ctx, central, fleet, dev)
		if err != nil {
			return Device{}, fmt.Errorf("projection: plane %s: %w", src.Plane(), err)
		}
		if v.Entries == nil {
			v.Entries = []Entry{}
		}
		out.Planes = append(out.Planes, v)
	}
	return out, nil
}

// Planes lists the names of the planes the service asks, in order.
func (s *Service) Planes() []string {
	out := make([]string, 0, len(s.sources))
	for _, src := range s.sources {
		out = append(out, src.Plane())
	}
	return out
}
