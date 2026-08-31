# Checking the fold winners against the CCU firmware

**Run** 2026-08-31, against `main` after the domain-core fold series (#664–#672)
· **Sources** `../OpenCCU-Base` (doc root `www/`), `../occu` (doc root
`WebUI/www/`) · **No code was changed.**

## Why this pass exists

The fold series reduced duplicated domain rules to one definition each. Where
two copies disagreed, a winner had to be chosen, and the authority used was
`aiohomematic` — which CLAUDE.md names as the CCU-side gold standard, and which
is right for enumerations, interface classification and paramset handling.

But for a rounding, a tie-break, an acceptance set, a bit position or a limit,
`aiohomematic` is a **port**. The firmware is the origin. Three times during the
series the firmware settled a question the port left open or answered wrongly —
the LINK provenance of the profiles archive, the `putParamset $address $peer`
shape, and the naming of the profile write. That was reason enough to go back
and check the winners themselves.

Six decisions were checked. Every reader was required to state a negative
control, and "the firmware does not carry this" was an explicitly allowed
verdict that must not be dressed up as agreement.

## Result

The heaviest reported contradiction was withdrawn after being measured against
the live CCU, and one more rested on a phrase the code does not use. What
survives is smaller than the first pass suggested, and the pass is still worth
its cost: it produced two hardware-confirmed facts about a wire parameter that
had only ever been reasoned about.

| Decision | Verdict |
|---|---|
| Climate endtime grammar | **contradicted in one half** |
| 13-slot climate limit | confirmed; a secondary assumption contradicted |
| `LEVEL_COMBINED` rounding | confirmed |
| `WEEK_PROGRAM_CHANNEL_LOCKS` | reported contradicted, **withdrawn after measurement** |
| `DURATION_VALUE` / timer sentinel | **contradicted** |
| Smoke labels · sysvar exclusion | mixed — see the correction below |

Nine sub-questions came back **firmware-silent**, and that is a result in
itself: those decisions still rest on the port, which is worth knowing and is
not the same as the firmware agreeing.

## The corrections I had to make to the readers' own verdicts

A report is a claim, and two of these did not survive checking.

**The sysvar IDs 40/41 verdict was overstated.** The reader found
`bin/hm_autoconf:233-243` calling `sv.Internal(0)` on both — verified, it is
there verbatim — and concluded our exclusion contradicts the firmware. It does
not. Our rule (`internal/model/hub/sysvar.go:158-166`) gives a different reason
than the reader assumed: those two are excluded because the daemon *already*
surfaces them through dedicated hub singletons (`alarm_messages.*`,
`service_messages.*`), not because they are internal. The firmware saying "these
are visible variables" and the daemon saying "we publish them once, elsewhere"
are compatible statements. The phrase the reader was reacting to — "CCU-internal
scratch values" — is one I used in conversation, not one the code makes.

**The weaker half of that same rule is the real finding.** `excludedSysvarMarkers`
carries `"OldVal"` and `"pcCCUID"`. In the firmware, `OldVal` appears only as an
ad-hoc scratch-variable naming habit and never as a classification rule; and
`pcCCUID` has **zero hits, case-insensitive, in both trees**. That half of the
rule rests entirely on the port's `_EXCLUDED` list.

## Confirmed, and worth keeping

- **One-digit hour in a climate endtime.** `tom_isTime` is
  `/^[0-2]?[0-9]:[0-5][0,5]$/` (`www/webui/webui.js:49002`) — the `?` makes
  `"1:30"` legal — and `:49051` zero-pads rather than refusing.
- **13 slots per weekday, hard.** `for {set i 1} {$i <= 13} {incr i}`
  (`www/config/easymodes/etc/hmipChannelConfigDialogs.tcl:3003`), and the group
  catalogue defines ordinals 1..13 and nothing above, for both the HmIP `P<n>_`
  names and the classic BidCos names.
- **`LEVEL_COMBINED` encoding.** The CCU does no scaling of its own — the
  parameter is a `hexstring_bytearray` string, so the client's arithmetic *is*
  the encoding, and the firmware's client emits integer-percent × 2 as a
  `"0Xnn,0Xnn"` pair.
- **The inverted read of the channel-lock bitmask.** A set bit renders MANU, a
  clear bit renders AUTO, corroborated on both the JS and the ReGa side.
- **`DURATION_UNIT` ordinals S=0, M=1, H=2**, and the CCU never selects `10MS`
  for a duration itself — the `value="3"` option is commented out in five
  duration selects.
- **`SMOKE_DETECTOR_ALARM_STATUS` semantics.** Closed four-member enum, and
  `INTRUSION_ALARM` is an operator actuation rather than a detection. The
  carve-out this project makes is the firmware's own reading.

## Contradicted — the findings

Ranked by what would misbehave.

### 1. WITHDRAWN — the `WEEK_PROGRAM_CHANNEL_LOCKS` finding does not survive measurement

This was reported as the heaviest contradiction. All three of its parts were
checked afterwards, two against the live CCU, and none holds. It is recorded in
full because a finding that collapses deserves the same care as one that
stands — and because the way it collapsed is instructive.

**The bit width is 24, measured.** Writing `WPTCLS=2147483647` (bits 0..30) to
`00536409A5E82D:19`, an HmIP-WRC6-230, and reading back gives `0x00FFFFFF` —
bits 0..23 exactly. The device masks to a 24-bit field, which is the width of
this project's own table (8 actors x 3 sub-channels). The claim that drove the
finding, that the highest bit the CCU ever emits is 9, is refused by the
hardware: bit 23 is accepted and held. The original value 0 was restored.

**The mode values are ours, measured twice.** On `0008DA499C8ADA:7` (HmIP-BDT),
`WPTCLS=1,WPTCL=0` sets bit 0 and `WPTCL=2` clears it. So `WPTCL=0` is MANU and
`WPTCL=2` is AUTO, exactly the enum order this project implements. A community
report giving "HmIP-BDT: Auto = 0, Manual = 1" is wrong for that device.
Confirmed again on the WRC6-230. Both devices restored to 0.

**The write shape is ours.** `COMBINED_PARAMETER` carrying `WPTCLS=...,WPTCL=...`
is accepted and changes the value; the claim that this string lives only inside
a `getConfigString()` helper is not what the hardware does.

**Why the firmware evidence looked otherwise.** The `Math.pow(2, index)` flat
ordinal really is in the firmware, at
`src/webui/www_source/ise/js/iseHmIPWeeklyProgram_AccessReceiver.js` — not under
`www/`, which is why a first search missed it. But
`www/rega/esp/controls/hmip_weekly_program.fn:75-85` selects that renderer by
device label, for `HmIP-DLD`, `-DLD-A`, `-DLD-S`, `-DLP`, `-DLP-A`, `-DLP-AS`
and `-DLP-WS` only. Every other device gets the generic
`iseHmIPWeeklyProgram.js`, which contains no `pow`, no `bit` and no `LOCK` at
all. The flat ordinal is the DOOR-LOCK rule; for the dimmer, switch and optical
families the firmware renders no channel locks and says nothing about the
mapping.

**What remains genuinely open.** The 24-bit width fits the 8x3 grid and does not
fit a scheme needing ten bits for this device, but it does not by itself prove
which key addresses which channel. Deciding that would need a week program
configured on the device, one bit set, and observation of which channel stops
following it — an intervention in a working installation over time, not a read.
It was not done.

**The lesson, which is the same one this session kept relearning.** The reader's
citations were real; the question they answered was not the one being asked. It
read a renderer without establishing which devices reach it. Four of my own
measurements in the same session failed the same way — a device built without
channels, groups counted per profile instead of per device, a bite on a caching
helper rather than the line the behaviour rests on, and a read-back that echoes
whatever was written. Each source was sound. Each question was slightly beside
the point.

### 1b. What the original finding claimed, kept for the record

`internal/model/weekprofile/channel_keys.go` maps `<actor>_<sub>` keys onto bits
0..23. The firmware's own weekly-program UI treats the bit as the **ordinal of
the channel inside that device type's relevant-channel list**, and the highest
bit it emits is 9. For an HmIP-DLD, bit 0 is the door-lock transmitter and bits
1..8 are the access receivers.

Two further divergences sit beside it. The mode value space is
`["MANU_MODE", "AUTO_MODE_WITH_RESET", "AUTO_MODE_WITHOUT_RESET"]` — mode **1
is real**, merely commented out of the UI select, while we emit only `{0, 2}`.
And the firmware's live write is a `putParamset` on VALUES carrying
`WEEK_PROGRAM_TARGET_CHANNEL_LOCK` (the mode *name*, a string) together with
`WEEK_PROGRAM_TARGET_CHANNEL_LOCKS` (an int); the `WPTCLS=…,WPTCL=…` combined
string we write appears in the firmware only inside a `getConfigString()`
helper.

This is the one to look at first: a wrong bit means a schedule toggle silently
addresses a different channel than the operator asked for.

### 2. The climate endtime grammar rejects what the CCU clamps

Verified first-hand at `www/webui/webui.js:49052`:

    if (hour == 24) {hour = "23"; min = "55";}

It runs **before** validation, and the clamped string is written back into the
field. So `"24:00"`, `"24:01"` and `"24:30"` all become 1435 on every
modern-generation climate device. This project refuses them instead — a value
the CCU's own editor accepts.

The range check that does exist (`endtime > 0 && endtime <= 1440`) only bites at
hour ≥ 25. And the old-generation path (HM-CC-TC) skips the clamp entirely, so
there a typed `24:00` is a genuine 1440. The firmware does not speak with one
voice here, which is itself the point: a single rule cannot be parity-true for
both generations.

Bound on this evidence, stated rather than glossed: it measures the WebUI
editor. A server-side re-validation of posted `ENDTIME_*` values was looked for
and not located, so this is the CCU's UX contract, not proof of a device-level
refusal.

### 3. `111600.0` is a range maximum labelled "unlimited", not a "not used" marker

The firmware shows `111600` as the top of the float `ON_TIME` / `OFF_TIME` /
`LENGTH_OF_OFF` range, labelled `${unlimited}` — and as `${inactive}` for
`DOOR_LOCK_TIME` only. No file in either tree contains both `111600` and
`DURATION`. Our naming ("timer disabled sentinel") reads the value as a marker
where the firmware treats it as a bound with a per-parameter label.

### 4. Slot 13 is not a terminator

The firmware terminates a weekday by **value** at any ordinal —
`if {$timeout == 1440} then { break; }` — and slot 13's catalogue entry is
schema-identical to slots 5..12. A day whose slot 3 already ends at 1440 is
complete; nothing makes the 13th special.

### 5. The `(value*200)+1` encoder has no firmware basis

This is the fifth `LEVEL_COMBINED` encoder found during the fold series and
deliberately left untouched, because no source said whether its convention was
legitimate. Now one does: every `float_integer_scale factor="200"` in
`firmware/rftypes` was enumerated — 267 of them, one with `offset="0.0"`, none
with an offset that could produce `+1` — and the WebUI encoder adds nothing.
Bytes 201/202 are reachable but are reserved sentinels.

## Firmware-silent, so still resting on the port

Recorded so the next reader does not repeat the search.

- Rounding vs truncation for a fractional level onto the 0..200 byte. The CCU's
  own client feeds an integer percent, so the firmware never faces the case.
- The seconds → `(DURATION_VALUE, DURATION_UNIT)` conversion, and therefore any
  rounding rule for it. Every firmware duration writer takes number and unit as
  two independent controls.
- A negative `WEEK_PROGRAM_CHANNEL_LOCKS` value. The reader said outright that
  its search could not have produced a "negative is legal" answer, so it
  measures nothing — the honest classification.
- Whether an enum index outside the declared value list is inactive.
- That the `VALUE_LIST` delivered over XML-RPC is ordered by ordinal. This
  caveat sits under **every** enum-index decision, including the smoke one.
- The authoritative per-device `DURATION_UNIT` value list and its order — not
  performable from these repos; it lives in each device's
  `getParamsetDescription`, which the CCU queries at runtime.
- Server-side re-validation of posted `ENDTIME_*`.
- `ProofAndSetValue`, the validator bound to the `DURATION_VALUE` input, has no
  definition anywhere in `OpenCCU-Base`.
- `OldVal` as a classification rule, and `pcCCUID` at all.

## What this pass says about method

The firmware is a strong authority for wire shapes, bit assignments, value
spaces and UI acceptance rules — the questions where a port has to make a choice
and may make it silently. It is a weak authority for anything the CCU's own
client never exercises: it does not round fractional levels because it never
sends one, and it does not convert seconds to a unit because it asks the user
for both.

That asymmetry is the useful takeaway. Reaching for the firmware pays where a
decision is about *what the wire carries*; it does not pay where the decision is
about *what a client should do with a value the CCU never produces*.
