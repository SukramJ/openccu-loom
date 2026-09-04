// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package schedule

// This file carries the wire facts about a schedule slot's duration that more
// than one package states. The (DURATION_BASE, DURATION_FACTOR) encoding
// itself lives with the raw converter in internal/model/weekprofile; what is
// declared here is the part the domain model also has to know, so that one
// firmware fact has one spelling.

// PermanentDurationBase and PermanentDurationFactor are the
// (DURATION_BASE, DURATION_FACTOR) pair the CCU writes for "Dauerhaft" — a
// switch point that does not expire. The lock domain uses it for "until
// further notice": an auto-relock end, an unlock, a standing user permission.
//
// The name is the CCU's own. Its weekly-program editor offers a two-option
// select per switch point, and choosing the first writes exactly this pair
// (www/config/easymodes/js/HmIPWeeklyProgram.js: `if (parseInt(value) == 0) {
// factorElm.val(31); baseElm.val(7); }`). Base 7 is the one-hour time base, so
// the pair reads as 31 h — one step above the 30 x 3600 s that the time
// parameters declare as their logical maximum, which is what leaves the value
// free to mean something else.
//
// This is a property of the current encoding, not a permanent one: a device
// declaring DURATION_FACTOR MAX above 31 would move it. It is declared here,
// once, so that such a correction reaches the lock action table and the wire
// converter together — they used to spell it separately.
const (
	PermanentDurationBase   = 7 // HOUR_1
	PermanentDurationFactor = 31
)

// PermanentDuration is the reserved word for the pair above, and ZeroDuration
// the one for (0, 0).
//
// Both exist because "" already means "leave the device's duration alone" —
// the raw converter writes a sparse paramset — so without a spelling of their
// own, a permanent slot and a zero-duration slot both read back as "" and were
// indistinguishable from a slot carrying no duration at all.
//
// They are duration strings for every purpose except arithmetic: they travel
// on [SimpleEntry.Duration] and [SimpleEntry.RampTime], and
// [SimpleEntry.Validate] accepts them there.
const (
	PermanentDuration = "permanent"
	ZeroDuration      = "0ms"
)
