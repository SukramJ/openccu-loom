// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake

// Deviation names one wire-contract fact the fake can be told to break.
// A contract test pins a fact by driving the production client against
// the fake twice: once as the contract says, once with the matching
// deviation on, asserting that the client notices (it errors, resyncs or
// classifies differently). Without the deviation run such a test only
// proves that the fake agrees with itself.
type Deviation string

// Deviations. Each breaks exactly the fact its comment names and leaves
// every other behaviour of the fake alone.
const (
	// DeviateInitFaultText answers init with a fault text other than the
	// exact refusal string.
	DeviateInitFaultText Deviation = "init-fault-text"
	// DeviateTierFaultText phrases the tier refusal differently from
	// "not permitted: <method> needs rpc:<tier>".
	DeviateTierFaultText Deviation = "tier-fault-text"
	// DeviateDownErrorCode answers a down interface process with a 503
	// whose JSON error code is not "down".
	DeviateDownErrorCode Deviation = "down-error-code"
	// DeviateHelloWithoutID sends the hello frame without an id.
	DeviateHelloWithoutID Deviation = "hello-without-id"
	// DeviateEventBeforeHello sends an event frame before the hello.
	DeviateEventBeforeHello Deviation = "event-before-hello"
	// DeviateReplayIncludesSince replays the message at the resume
	// position itself, not only those after it.
	DeviateReplayIncludesSince Deviation = "replay-includes-since"
	// DeviateResyncWithID gives the resync frame an id.
	DeviateResyncWithID Deviation = "resync-with-id"
	// DeviateNoHeartbeat sends no ": ping" heartbeat on the lite-rpc
	// stream.
	DeviateNoHeartbeat Deviation = "no-heartbeat"
	// DeviateStreamLimitCode answers the stream limit with a 429 whose
	// error code is not "too-many-streams".
	DeviateStreamLimitCode Deviation = "stream-limit-code"
	// DeviateMetaDropEvent drops every change event of one revision from
	// the metadata stream (the revision is skipped on the wire).
	DeviateMetaDropEvent Deviation = "meta-drop-event"
	// DeviateMeta304WithoutETag answers an unchanged metadata write with
	// 304 but no ETag.
	DeviateMeta304WithoutETag Deviation = "meta-304-without-etag"
	// DeviateLengthRequiredAlways answers 411 to every /api/ request
	// with a body method, whatever its headers.
	DeviateLengthRequiredAlways Deviation = "length-required-always"
	// DeviateVersionMajor announces rpc major 2 in /api/meta/v1/version.
	DeviateVersionMajor Deviation = "version-major"
	// DeviateVersionHTML answers /api/meta/v1/version with the HTML
	// shell, as a box whose API is not there would.
	DeviateVersionHTML Deviation = "version-html"
)

// Deviate switches a deviation on or off. Concurrency-safe; it applies
// to requests (and stream frames) handled afterwards.
func (f *Fake) Deviate(d Deviation, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deviations == nil {
		f.deviations = map[Deviation]bool{}
	}
	if on {
		f.deviations[d] = true
		return
	}
	delete(f.deviations, d)
}

// deviates reports whether a deviation is on.
func (f *Fake) deviates(d Deviation) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deviations[d]
}
