// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package outputs implements the alarm output-driver layer
// (docs/alarm-concept.md §7/§8): it turns the engine's abstract
// FireCycle/StopAll/Chirp port calls into bounded device commands on
// the enrolled sirens, switch actuators, smoke-detector sounders,
// alarm lights, and chirp emitters.
//
// The safety split with the engine: the engine accounts cycles and
// re-fires before calling in; this package clamps every acoustic
// duration to a finite bound (S1), writes the incident's cumulative
// acoustic ledger before each activation, schedules the verified stop
// watchdog for every activation (S2), issues stops at critical
// priority (S5), and reconciles already-sounding sirens adopt-before-
// stop (S4). No output class is ever activated without a stop path.
package outputs
