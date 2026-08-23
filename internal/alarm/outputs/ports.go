// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package outputs

import (
	"context"
	"time"

	sirencdp "github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// SirenDevice is the driver's view of a native siren (satisfied by
// *siren.Siren): one atomic activation write, one atomic stop write,
// event-fed active feedback.
type SirenDevice interface {
	TurnOn(ctx context.Context, cfg sirencdp.OnConfig, priority hmenum.CommandPriority) error
	TurnOff(ctx context.Context, priority hmenum.CommandPriority) error
	AcousticState() (active bool, selection string, observed bool)
	OpticalState() (active bool, selection string, observed bool)
	AvailableTones() []string
	AvailableLights() []string
}

// SmokeSounderDevice is the driver's view of a smoke detector used as
// intrusion sounder (satisfied by *siren.SmokeSiren). It has no
// duration parameter — the engine-side watchdog is its only bound.
type SmokeSounderDevice interface {
	TurnOn(ctx context.Context, priority hmenum.CommandPriority) error
	TurnOff(ctx context.Context, priority hmenum.CommandPriority) error
	IsIntrusion() bool
	IsActive() (active, observed bool)
}

// ActuatorDevice abstracts switch and dimmer actuators for the
// switched-siren and alarm-light classes. TurnOnBounded must write
// the device-side auto-off (ON_TIME) together with the switch-on so
// the device self-terminates even if the daemon dies (S1); adapters
// wrap *switchdev.Switch and *light.Light.
type ActuatorDevice interface {
	TurnOnBounded(ctx context.Context, d time.Duration, level *float64, priority hmenum.CommandPriority) error
	TurnOnSteady(ctx context.Context, level *float64, priority hmenum.CommandPriority) error
	TurnOff(ctx context.Context, priority hmenum.CommandPriority) error
	IsOn() (on, observed bool)
}

// SoundDevice is the driver's view of an MP3 chirp emitter (satisfied
// by an adapter over *siren.SoundPlayer).
type SoundDevice interface {
	PlayChirp(ctx context.Context, soundfileIndex int, volume float64, priority hmenum.CommandPriority) error
	Stop(ctx context.Context, priority hmenum.CommandPriority) error
}

// DeviceResolver maps an enrolled output row to its live device
// driver. Implementations resolve through the central registry; a
// missing central, channel, or mismatched device class returns an
// error the manager reports as a fault (S7).
type DeviceResolver interface {
	Siren(centralName, channelAddress string) (SirenDevice, error)
	SmokeSounder(centralName, channelAddress string) (SmokeSounderDevice, error)
	Actuator(centralName, channelAddress string) (ActuatorDevice, error)
	Sound(centralName, channelAddress string) (SoundDevice, error)
}

// IncidentLedger is the manager's slice of the incident store: the
// cumulative acoustic accounting (written before each activation —
// over-counting on crash is safe) and the fresh budget read.
type IncidentLedger interface {
	AddAcousticMS(ctx context.Context, id, deltaMS int64) error
	Get(ctx context.Context, id int64) (sqlitestore.AlarmIncident, bool, error)
}

// OutputRowSource loads the enrolled output rows. Satisfied by
// *sqlitestore.AlarmOutputStore.
type OutputRowSource interface {
	GetAll(ctx context.Context) ([]sqlitestore.AlarmOutputRow, error)
}

// HealthFunc receives driver-level health transitions: every failed
// output command — activation, stop, test — reports a degradation
// naming the output, and a verified stop reports recovery. Optional;
// nil drops the signal, and the journal still records every failure.
//
// A refusal by design is not a failure: a test fire the manager
// declines for a class without a safe test path reports nothing, so the
// signal keeps meaning "something is wrong" (S7).
type HealthFunc func(healthy bool, note string)

// ZoneHealthFunc receives a zone-scoped output health transition: an
// output enrolled in exactly this zone failed or recovered. Optional;
// nil drops the signal. Unlike HealthFunc — fleet-wide, feeds the
// daemon health tracker and the alarm bus for external planes — this
// drives only the alarm-control-panel projection's per-zone
// availability, so a siren stuck in one zone does not remove Home
// Assistant's disarm control from every other zone during an active
// alarm.
type ZoneHealthFunc func(zoneID string, healthy bool)
