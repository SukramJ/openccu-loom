// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package safety classifies device data points into the Security &
// Safety hazard/fault taxonomy ([hmenum.SecurityClass]).
//
// It answers one question: "given a device model, a channel type and a
// parameter — what does this data point mean for the security of the
// installation, and which of its values count as active?" The answer
// feeds the aggregation plane that surfaces hazards, faults and
// triggering sources to north-bound consumers.
//
// Three properties are deliberate:
//
//   - The lookup key is the triple (model, channelType, parameter), not
//     the parameter alone. ALARMSTATE means water on a water-detection
//     channel and actuator feedback on a siren; a parameter-only table
//     cannot tell those apart.
//   - Classification is static. It is derived from the device model, so
//     the index can be built once at attach time and answered by map
//     lookup on the hot path — a value-change event carries no model or
//     value list.
//   - Actuator feedback is excluded by construction, not by omission.
//     A parameter the alarm engine itself drives must never be readable
//     back as a cause, or the domain reports its own siren as the
//     origin of the fire.
//
// See notes/concepts/security-safety-concept.md §3.5 and §6.1.
package safety
