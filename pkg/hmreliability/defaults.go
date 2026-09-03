// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package hmreliability is the single source of truth for the timing
// and threshold defaults used by the southbound reliability layer
// (`internal/client/reliability`). The constants mirror the Python
// reference implementation's `client/_send_request.py` defaults so
// the two stacks stay behaviourally aligned.
//
// Why a public package: the per-component defaults were historically
// scattered across `circuit.go`, `retry.go`, `throttle.go`, and
// `pingpong.go`. That made silent drift against the Python reference
// implementation possible and invisible to CI. Centralising them here
// lets the [TestRecordedReliabilityDefaults] snapshot lock the
// values; any drift triggers a CI failure that demands an explicit
// by-design entry (notes/parity/by_design.md) before the change lands.
//
// Drift policy: when the Python reference implementation advances and
// a default genuinely changes upstream, update the constant *and* the
// snapshot in the same commit, with a reference in the commit
// message.
package hmreliability

import "time"

// Circuit-breaker defaults — mirror the Python reference
// implementation's `Connection` constructor (`client/connection.py`).
const (
	// CircuitFailureThreshold is the number of consecutive errors after which
	// the breaker trips OPEN.
	CircuitFailureThreshold = 5
	// CircuitResetTimeout is the dwell time in OPEN before the breaker advances
	// to HALF-OPEN.
	CircuitResetTimeout = 30 * time.Second
	// CircuitHalfOpenSuccess is the number of consecutive successes in HALF-OPEN
	// required to close the breaker.
	CircuitHalfOpenSuccess = 2
)

// Retrier defaults — mirror
// (`client/_send_request.py` + `command_retry.py`).
const (
	// RetryInitialBackoff is the first sleep before the first retry.
	// The 2 s value is production-hardened on real CCUs; smaller
	// initial backoff produces measurably more retry storms under
	// duty-cycle saturation.
	RetryInitialBackoff = 2 * time.Second
	// RetryMaxBackoff bounds exponential growth.
	RetryMaxBackoff = 30 * time.Second
	// RetryDutyCycleDelay is the wait inserted on XML-RPC fault -8.
	//
	// The fault is far narrower than "the CCU is duty-cycle limited": -8 is
	// raised at exactly one place in the CCU source tree, inside
	// RFDevice::UpdateFirmware (OpenCCU-Base src/rfd/RFDevice.cpp:1492), when
	// DutyCycleForUpdate reports that the image does not fit in the remaining
	// airtime allowance (src/rfd/BidcosInterfaceConcentrator.cpp:1045 — an
	// instantaneous byte-budget comparison, no time dimension). No value write
	// produces it, and the HmIP legacy surface has no -8 at all — its fault
	// mapping (HMIPServer
	// de.eq3.cbcs.legacy.bidcos.rpc.internal.NotificationUtil#getRpcRemoteException)
	// produces -1, -2, -5 and -10 only. So on a real
	// CCU this delay is reachable only from a firmware update.
	//
	// The 40 s is carried over from the Python reference implementation
	// (`const.py`, command_retry_duty_cycle_delay) and is therefore a witness
	// value, not a measurement: rfd holds no duty-cycle recovery model — it
	// reads the percentage out of the transceiver's HELLO frame
	// (src/rfd/BidcosRemoteInterface.cpp:505) and re-notifies only when a
	// threshold band is crossed — so how long the window takes to drain is
	// unverified, and no readable source can settle it. Kept as-is for that
	// reason: there is no measured number to replace it with.
	RetryDutyCycleDelay = 40 * time.Second
	// RetryTransmissionPendingDelay is the wait inserted on XML-RPC fault -10.
	//
	// -10 ("Transmission is pending.") exists only on the HmIP side; rfd and
	// hs485d never emit it. It is raised behind a reachability test rather
	// than a timer — DeviceSubcommandHandler#handleSetConfigCommand persists
	// the configuration as pending and answers TRANSMISSION_PENDING when
	// `device.isReachable()` is false — so on putParamset MASTER the command
	// is already queued at the CCU and waiting cannot change the answer;
	// #handleSetValueCommand does not produce it at all. Where it does mean
	// something transient is deleteDevice and the link operations, whose
	// mapping collapses AP_BUSY / STACK_BUSY / RESPONSE_NAK / TIMEOUT into the
	// same -10 (NotificationUtil#createDeleteDeviceRpcRemoteException); a
	// short retry is defensible there, which is why the delay stays.
	//
	// The 5 s is likewise a witness value from the Python reference
	// implementation (`const.py`, command_retry_transmission_pending_delay).
	// The pending window is bounded by the target device's own wake-up cycle,
	// which no readable CCU source states, so the duration is unverified.
	RetryTransmissionPendingDelay = 5 * time.Second
	// RetryRecoveryWait caps how long the retrier blocks for a
	// connection-recovery event before giving up. 120 s mirrors the
	// reference Python stack's recovery-wait ceiling.
	RetryRecoveryWait = 120 * time.Second
)

// Throttle defaults — mirror
// (`command_throttle.py`).
//
// Only [ThrottleInterCommandDelay] is in force today. The southbound
// pools are built per traffic class with an explicit in-flight capacity
// and queue depth, and they leave the throttle's burst window
// unconfigured — the throttle enables that path only when both burst
// values are set, so the two constants below describe the reference
// pacing rather than what a running daemon does. Enabling it is a
// change to RF transmit behaviour and needs measurement against a real
// CCU, not a constant flipped here.
const (
	// ThrottleInterCommandDelay is the minimum gap between two consecutive
	// non-critical commands on the same interface. Default 0: writes are
	// already serialised at one in-flight command per interface. It is not
	// backstopped by [RetryDutyCycleDelay] — that delay keys on fault -8,
	// which a value write never receives — so nothing in this stack paces
	// writes against the transceiver's duty cycle today. Whether a real CCU
	// needs such pacing is unverified: it would have to be measured against
	// the DUTY_CYCLE reading of a live interface.
	ThrottleInterCommandDelay = 0 * time.Millisecond
	// ThrottleBurstThreshold is the soft cap on non-critical commands per
	// [ThrottleBurstWindow].
	ThrottleBurstThreshold = 5
	// ThrottleBurstWindow is the rolling window across which the burst threshold
	// is measured.
	ThrottleBurstWindow = 500 * time.Millisecond
	// ThrottleMaxQueueDepthFactor scales a pool's in-flight capacity into
	// its recommended queue depth ("4× in-flight"), centralised here so
	// the multiplier stays one value across callers.
	ThrottleMaxQueueDepthFactor = 4
)

// Ping/pong defaults — mirror
// (`client/connection.py`).
const (
	// PingPongTTL is how long a sent ping is kept in the pending map before it
	// is considered orphaned.
	PingPongTTL = 300 * time.Second
)
