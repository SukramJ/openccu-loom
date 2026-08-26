// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package addonupdate

import (
	"errors"
	"time"
)

// State is the updater's lifecycle state. Mirrors the OpenAPI
// AddonUpdateStatus.state enum verbatim — do not rename these
// values without a matching assets/openapi.yaml change.
type State string

// State values. Installing is terminal from a caller's perspective —
// the daemon restarts on a successful install, so there is no
// observable "installed" state to transition into.
const (
	StateIdle        State = "idle"
	StateChecking    State = "checking"
	StateDownloading State = "downloading"
	StateInstalling  State = "installing"
	StateFailed      State = "failed"
)

// Status is the full snapshot the state machine hands to every
// observer (REST GET, WS broadcast, MQTT state topic). Field shapes
// mirror the OpenAPI AddonUpdateStatus schema; callers that serialise
// to JSON own their own DTO with the exact wire tags — this type is
// the domain-side source of truth they convert from.
type Status struct {
	// Supported reports whether the platform capability check passed
	// (add-on build + executable firmware installer). false zeroes
	// every other field.
	Supported bool
	// CurrentVersion is the running daemon's build version.
	CurrentVersion string
	// LatestVersion is the newest published release tag (version
	// part, no leading "v"); empty before the first successful check.
	LatestVersion string
	// UpdateAvailable is true when LatestVersion is newer than
	// CurrentVersion.
	UpdateAvailable bool
	// ReleaseURL is the release-notes page for LatestVersion.
	ReleaseURL string
	// LastCheck is the time of the last successful check; the zero
	// value means "never".
	LastCheck time.Time
	// State is the current lifecycle state.
	State State
	// Error carries the failure detail while State is StateFailed.
	Error string
}

// Sentinel errors the updater's Check/Install verbs return. REST
// handlers map these to their documented HTTP status.
var (
	// ErrUnsupported is returned when the platform capability check
	// failed — no firmware installer, or not an add-on build.
	ErrUnsupported = errors.New("addonupdate: platform does not support self-update")
	// ErrBusy is returned when Check or Install is called while the
	// state machine is already checking, downloading, or installing.
	ErrBusy = errors.New("addonupdate: an update operation is already running")
	// ErrNoUpdateAvailable is returned by Install when the last known
	// check found no newer release.
	ErrNoUpdateAvailable = errors.New("addonupdate: no update available")
)
