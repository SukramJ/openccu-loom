# Firmware follow-up against OpenCCU-Base

A second pass, complementing
[`2026-08-31-firmware-check-of-fold-winners.md`](./2026-08-31-firmware-check-of-fold-winners.md).

That pass checked six decisions the fold series had settled on the port's
authority. This one asks the same question of the rules the fold series never
touched: a rounding, a scale, a bit position, a cap, a tie-break — the classes
where `aiohomematic` is a port and the firmware is the origin.

## Method, and what it cost

Four read-only inventories over our own code, one per class (numeric
conversion, bit/packed encoding, acceptance set, ordering/tie-break). Each was
told to report **claims whose authority is a port**, not defects, and to rank
by "a wrong value here reaches a device". They returned 42 candidates.

The verdicts below are mine, measured against OpenCCU-Base and the device
descriptor corpus. The inventories located; they did not decide.

## The result: one encoding explains a cluster of unrelated-looking constants

`src/libhsscomm/HSSTypeConversionFloatConfigtime.cpp` defines the CCU's
`float_configtime` conversion in full:

```cpp
static const double DEFAULT_FACTORS[]={0.1, 1.0, 5.0, 10.0, 60.0, 300.0, 600.0, 3600.0};
value_size = 5;                       // default, overridable per parameter

// LogicalToPhysical
max_value = (1<<value_size)-1;        // 31
for (i=0; i<factors.size(); i++) if (f_time <= max_value*factors[i]) break;
f_time = f_time/factors[i] + 0.5;     // rounds half-up; does NOT truncate
i_time = (int)f_time;
if (i_time>max_value) i_time=max_value;
i_time |= i<<value_size;              // unit index in the high bits
```

Three things follow that were separately unexplained in our code.

### 1. `111600` is the top of that encoding, not an arbitrary marker

`31 x 3600 = 111600`. The time parameters declare a logical `MAX` of
`30 x 3600 = 108000`. So the value is the one the encoding can represent that
the logical range excludes — which is what leaves it free to carry a meaning
instead of a duration. Constructed, not chosen. WHAT it means differs per
surface; see section 3.

The wired family repeats the construction with a wider value field:
`(2^14 - 1) x 1000 = 16383000`, which is exactly the `SPECIAL.NOT_USED` those
six `HMW-*` types declare.

This refines what PR #674 recorded ("deliberately out of band") and it partly
rehabilitates the finding the previous pass withdrew: `111600` **is** a range
maximum — of the encoding, which is a surface neither the earlier finding nor
its retraction had looked at.

### 2. Our `timeBaseTable` is the firmware's table, not the port's

`DURATION_BASE`'s `VALUE_LIST` is
`{BASE_100_MS, BASE_1_S, BASE_5_S, BASE_10_S, BASE_1_M, BASE_5_M, BASE_10_M, BASE_1_H}`
on every device in the corpus that carries it — element for element the
`DEFAULT_FACTORS` above. `(DURATION_BASE, DURATION_FACTOR)` is the same
conversion split across two parameters instead of packed into one integer.

`internal/model/weekprofile/rawconvert.go` cited `_TIME_BASE_IN_100MS` in
`schedule_models.py` for this. It now cites the origin.

### 3. `permanentBase=7, permanentFactor=31` is the CCU's own "Dauerhaft"

Base 7 is the 3600 s factor; factor 31 is the 5-bit maximum. The pair is
`111600` again — and the CCU names it. Its weekly-program editor offers two
options per switch point, and choosing the first writes exactly this pair
(`www/config/easymodes/js/HmIPWeeklyProgram.js`):

```js
result += "<option value='0'>" + translateKey('optionPermanent')  + "</option>";
result += "<option value='1'>" + translateKey('optionEnterValue') + "</option>";
...
if (parseInt(value) == 0) { factorElm.val(31); baseElm.val(7); }
```

`"optionPermanently" : "Dauerhaft"` in `translate.lang.option.js`, and a second
site writes the same pair straight onto `_WP_DURATION_BASE` /
`_WP_DURATION_FACTOR`.

So `permanentBase` / `permanentFactor` was named right all along, and better
grounded than this note's first draft, which called the pair a "no-duration"
marker throughout. **The correction matters beyond wording**, and it is the
same mistake this project keeps catching — one encoding ceiling, two surfaces,
two names:

| Surface | Parameters | The CCU's own label for 111600 |
|---|---|---|
| Weekly program | `DURATION_BASE` / `DURATION_FACTOR` | **Dauerhaft** — the editor's own select |
| LINK paramset | `SHORT_/LONG_ON_TIME`, delays, ramps | **`SPECIAL.NOT_USED`** — the descriptor's own field |

"The timer never expires" and "the timer is not applied" coincide physically,
which is exactly why folding them is tempting and wrong: each surface states
one of them, and neither states the other. This one was caught in review, not
by me — I had carried the descriptor's label across to a surface that uses a
different one, which is the same shape as the finding this series withdrew.

### What the operator side says about the same number

Searched `homematic-forum.de` for `111600` after the derivation, as a check
against a source that does not share the firmware's assumptions. It corroborates
the effect and does **not** explain it — which is the right way round, and worth
recording so the next reader does not go looking for an explanation there.

Three independent appearances:

- The WebUI shows it as the **range end**. An HM-LC-Sw1-DR expert-parameter
  listing gives `0.0 s (0.0-111600.0)` for `SHORT_ONDELAY_TIME`,
  `SHORT_OFFDELAY_TIME`, `LONG_ONDELAY_TIME` and `LONG_OFFDELAY_TIME`
  (<https://homematic-forum.de/forum/viewtopic.php?t=81358>).
- It appears as a **parked value on a real device**: a paramset dump shows
  `LONG_OFF_TIME 111600.000000` beside `LONG_ON_TIME 900.000000`
  (<https://homematic-forum.de/forum/viewtopic.php?f=26&t=26669>). One time is
  a real 15 minutes; the other is the marker.
- It manifests as **behaviour**: "HmIP-BSL - Einschaltdauer begrenzt OFF, nach
  31 Stunden aus"
  (<https://homematic-forum.de/forum/viewtopic.php?f=60&t=67525&start=10>).
  31 h is 111600 s exactly.

That last thread carries the one thing a forum can add that a source tree
cannot, and also the one caveat. eQ-3 replied in it:

> "Dieses Verhalten ist uns bereits bekannt. An einer Lösung wird zurzeit
> seitens der Entwicklung gearbeitet und in einem zukünftigen Update
> veröffentlicht."

So the manufacturer treats the 31-hour cutoff as a **defect to be fixed**, not
as a documented ceiling. Nobody in the thread connects it to 111600 or to an
encoding — the explanation is only in
`HSSTypeConversionFloatConfigtime.cpp`.

**The caveat that follows**: if that fix widens the encoding, the ceiling
moves, and with it the derivation that `111600` is the one representable value
above the logical range. The reasoning here is sound about the current
encoding and is not a permanent property. What would show it had moved: a
device declaring `DURATION_FACTOR` `MAX` above 31, or a time parameter whose
`SPECIAL.NOT_USED` is no longer `31 x top_factor`.

Source weight, stated because it is easy to lose: the forum posts are user
reports and one manufacturer statement. They establish that the limit is real
and user-visible. They establish nothing about *why*, and none of the
derivation above rests on them.

## Contradicted

**`MaxTimeBaseFactor = 30` is not "the largest the CCU firmware accepts."**
Every device in the corpus declares `DURATION_FACTOR` as `MIN 0 / MAX 31`. 31
is inside the declared range and is spoken for: with base 7 it is what the
editor writes for "Dauerhaft". So 30 caps a timed *duration*, not the field. The claim came from the port
(`_MAX_DURATION_FACTOR`); the behaviour is right and the justification was not.

**The firmware does not hard-code that bound at all.** `hmipWeeklyProgramDevice.tcl:29-30`
reads `$param_descr(MIN)` / `$param_descr(MAX)` from the paramset description
at runtime. We hold a constant where the CCU looks it up per device. Harmless
while every device declares 31; a fact worth knowing before the first one
does not.

## Confirmed, and no longer resting on the port

- **`timeUnitThreshold = 16343`** is `DURATION_VALUE`'s own declared `MAX`
  (INTEGER, `MIN 0 / MAX 16343`, 95 devices). Promoting only once the value
  would exceed the declared maximum is the field's rule.
- **Index 0 of a device-error enum is the no-fault state.** 70 of 70 `ERROR*`
  state enums: 69x `NO_ERROR`, 1x `BUS_OK`. The two apparent exceptions —
  `CLEAR_ERROR` on HmIP-FWI and HmIP-WKP, whose index 0 is `SABOTAGE_STICKY` /
  `SABOTAGE` — are *command* enums, and `errorPrefixes` (`ERROR`,
  `SENSOR_ERROR`) does not match them: `CLEAR_ERROR` starts with neither. The
  prefix rule excludes them correctly, which is worth stating because the last
  prefix rule this project checked did not.

## Still resting on the port, and now for a stated reason

The seconds -> `(DURATION_VALUE, DURATION_UNIT)` **rounding** rule. The
previous pass called it firmware-silent, and that verdict survives this one:
`float_configtime` governs the LINK-paramset time parameters — 39 parameter
ids, `SHORT_/LONG_ON_TIME`, the delay and ramp times — and neither
`DURATION_VALUE`, `DURATION_UNIT`, `ON_TIME` nor `RAMP_TIME` is among them.
`DURATION_UNIT` is a `{S, M, H}` / `{S, M, H, 10MS}` enum, not the eight-step
factor table. Two encodings, and the firmware's rounding belongs to the other
one.

Recording this precisely is the point: the temptation was to carry the `+0.5`
across, which is exactly the move that produced the withdrawn finding last
time — a correct citation answering the wrong surface.

## Noted, not acted on

`DURATION_UNIT` declares a fourth member `10MS` on 72 of 95 devices.
`hmenum.TimerUnit` has three. Index alignment is unaffected (`S`, `M`, `H` are
0, 1, 2 either way), so nothing is misread; sub-second durations are simply
not expressible through this path.

## Candidates this pass did not settle

The four inventories are in the scratchpad; 42 ranked candidates, of which the
highest-ranked unsettled ones are worth a later pass:

- BIN-RPC double packing `mantissa * 2^exp / 2^30` — asserted in a comment
  with no source; the codec exists in the OCCU tree.
- The RF-lock inverted `STATE` boolean, and the HM-Sec-Win `-0.005` /`0.0`
  level pair.
- `TARGET_CHANNELS` bit order `3*(actor-1)+(sub-1)`, which has the same
  "which key addresses which channel" gap as `channelKeyBitmask`.
- The two-entry ENUM `index 0 -> payload_off` rule in MQTT discovery.

Each carries the standing caveat the previous pass named: whether a
`VALUE_LIST` delivered over XML-RPC is ordered by ordinal is itself
firmware-silent, and it sits under every enum-index decision here.
