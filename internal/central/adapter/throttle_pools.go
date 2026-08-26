// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
)

// perClassThrottlePools builds the three per-RPC-class throttle pools wired
// into every InterfaceClient (read / write / control). Splitting the classes
// into independent pools keeps a backing-off write — e.g. a CCU DUTY_CYCLE
// stall that parks a setValue for tens of seconds — from blocking cheap reads
// or liveness pings that would otherwise queue behind the same shared permit.
//
// Each pool bounds its pending-waiter queue at 4× its in-flight capacity
// (ThrottleConfig.MaxQueueDepth): a stalled CCU then fails non-critical work
// fast (ErrThrottleQueueFull → the retrier's backoff) instead of growing the
// heap until the daemon OOMs. CRITICAL-priority commands bypass the depth gate.
//
// interCommandDelay (reliability.command_throttle_inter_command_delay) paces
// the WRITE pool only: RF duty cycle is a transmit-side concern, so reads and
// control traffic are not gated by it. Read and write keep an in-flight
// capacity of 1 (matching the historic single shared pool) but no longer share
// a permit; control is sized near-unbounded so a reconnect storm (init / ping /
// session floods) does not stall device traffic.
func perClassThrottlePools(interCommandDelay time.Duration) (read, write, control *reliability.CommandThrottle) {
	const (
		readWriteCapacity = 1
		controlCapacity   = 8
		queueDepthRatio   = 4
	)
	read = reliability.NewThrottle(reliability.ThrottleConfig{
		MaxInFlight:   readWriteCapacity,
		MaxQueueDepth: readWriteCapacity * queueDepthRatio,
	})
	write = reliability.NewThrottle(reliability.ThrottleConfig{
		MaxInFlight:       readWriteCapacity,
		MaxQueueDepth:     readWriteCapacity * queueDepthRatio,
		InterCommandDelay: interCommandDelay,
	})
	control = reliability.NewThrottle(reliability.ThrottleConfig{
		MaxInFlight:   controlCapacity,
		MaxQueueDepth: controlCapacity * queueDepthRatio,
	})
	return read, write, control
}
