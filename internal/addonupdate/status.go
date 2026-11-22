// SPDX-License-Identifier: Apache-2.0
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
	// LatestPrerelease reports that LatestVersion is a pre-release,
	// which a check only resolves when the operator opted into them.
	LatestPrerelease bool
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
	// ErrorCode classifies a failure a client can explain without
	// parsing Error; empty for every failure without a code of its own.
	// The only code is [ErrorCodeRateLimited].
	ErrorCode string
	// RetryAt is when a check can succeed again, set with
	// [ErrorCodeRateLimited]; the zero value means unknown.
	RetryAt time.Time
}

// ErrorCodeRateLimited marks a check GitHub refused because the request
// limit of the daemon's address is used up — a limit that clears by
// itself at RetryAt, not a fault.
const ErrorCodeRateLimited = "rate_limited"

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
	// ErrRateLimited is returned by a check GitHub refused for its request
	// limit; the error is a [*RateLimitError] carrying the retry time.
	ErrRateLimited = errors.New("addonupdate: GitHub's request limit is used up")
)

// RateLimitError is a release check refused by GitHub's request limit.
// The unauthenticated limit counts per public address, so daemons behind
// one address share it. RetryAt is zero when GitHub named no time.
type RateLimitError struct {
	RetryAt time.Time
}

func (e *RateLimitError) Error() string {
	if e.RetryAt.IsZero() {
		return ErrRateLimited.Error()
	}
	return ErrRateLimited.Error() + "; the check works again after " + e.RetryAt.UTC().Format(time.RFC3339)
}

// Is makes errors.Is(err, ErrRateLimited) hold for every RateLimitError.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }
