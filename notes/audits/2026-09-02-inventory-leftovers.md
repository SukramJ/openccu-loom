# The five inventory leftovers, settled

The [firmware follow-up](./2026-09-01-firmware-followup-against-openccu-base.md)
located five rules ranked "a wrong value here reaches a device" and did not
reach them. This pass does.

Four came back **confirmed** — which is worth as much as a contradiction,
because each had rested on the port. One turned out to be **wrong in its
comment while right in its behaviour**, and one is **narrowed rather than
settled**.

## 1. BIN-RPC double packing — confirmed, and a rounding aligned

The formula was asserted in a comment with no source. It is the firmware's:
`OpenCCU-Base/src/libXmlRpc/src/XmlRpcValue.cpp` decodes with
`ldexp(double(mantissa)/double(1<<30), exponent)` and encodes with
`mantissa = int(frexp(value, &exponent) * double(1<<30))`.

The comparison found one divergence. C's `int()` truncates toward zero; we
used `math.Floor`. They agree on every positive value and on negatives that
land on a mantissa boundary, and differ by one unit in the last place
otherwise:

| value | firmware | ours (before) |
|---|---|---|
| -21.5 | -721420288 | -721420288 |
| **-0.1** | **-858993459** | **-858993460** |
| **-3.7** | **-993211187** | **-993211188** |

About 1.2e-10 on -0.1 — invisible in any reading, and still a divergence from
the origin for no reason. Changed to `math.Trunc`.

## 2. RF-lock inverted `STATE` — confirmed

The descriptor cannot settle it: `STATE` is a bare BOOL with no `VALUE_LIST`
on every HM-Sec-Key variant. The WebUI can, in both directions
(`src/webui/www_source/ise/js/iseButtonsKeyMatic.js`):

```js
if (opts.stState == 1) { ControlBtn.on(this.divOpen); }   // true  -> open
else                   { ControlBtn.on(this.divClosed); } // false -> closed
onClickClose: setDpState(this.opts.idState, 0);
onClickOpen:  setDpState(this.opts.idState, 1);
```

`false = locked, true = unlocked`, read and write. Our comment cited
`CustomDpRfLock`; it now cites the origin.

## 3. `TARGET_CHANNELS` bit order — narrowed, not settled

The firmware does not compute a bit position. It **reads** one, from the label
beside each checkbox (`HmIPWeeklyProgram.js`: `var bit =
parseInt(jQuery(this).prev().text())`), and that label is the device's virtual
channel number. The offset is confirmed by example in the same file:
`isBitSet(val, 1) || isBitSet(val, 2)` is commented "the virtual channels 2
and 3 of the HmIP-FWI", so **bit index = virtual channel number - 1**.

Our table computes what the CCU looks up. That is correct only while a device
lays its virtual channels out actor-major in groups of three — consistent with
the editor's own `maxVirtCounter = 3`, not proven by it. A device numbering
them otherwise is mis-addressed silently, and nothing here would notice.

Settling it still needs a device: one bit set, and an observation of which
channel stops following its program. Recorded in the comment rather than
resolved.

## 4. HM-Sec-Win `-0.005` — the behaviour stands, the comment did not

The descriptor settles it outright: `LEVEL` is `FLOAT MIN 0.0 / MAX 1.0` with
`SPECIAL {LOCKED: -0.005}`.

So `-0.005` is the device's declared **LOCKED** state, and our comment called
it "fully closed". `0.0` is the ordinary minimum, and the comment called it
"slightly open". Both descriptions were of the *domain remap*, written as if
they described the wire.

The remap itself is unchanged and defensible — it presents locked as fully
closed and lifts the ordinary minimum to 0.01 so the two stay distinguishable
in one position number. What changed is that the comment now says which half
is the device's and which is ours.

## 5. Two-entry ENUM `index 0 -> payload_off` — confirmed in scope, false one step wider

As a general rule about two-entry enums it is wrong. Of 81 distinct two-entry
`VALUE_LIST`s in the corpus only 29 put an inactive-looking label first, and
one is literally `{ON, OFF}`.

Restricted to what can reach `binarySensorPayloads` — two entries, in
`VALUES`, not writable — **all 14 do**:

    NORMAL/UNKNOWN · STABLE/NOT_STABLE · NO_ERROR/… (nine variants)
    CLOSED/OPEN · DRY/RAIN

The counterexamples are writable config and action selects, which never take
that path. So the rule is sound where it is applied and would be wrong one
step wider, and the comment now says so — a reader who generalises it is the
failure mode worth preventing.

The standing caveat is unchanged and now stated at the site: that a
`VALUE_LIST` delivered over XML-RPC is ordered by ordinal is itself
firmware-silent, and it sits under this decision.

## A correction to the previous note

It claimed the four inventories were "in the scratchpad". They were not: the
agents returned their tables inline and nothing was written out, so the
sentence pointed at a file that never existed. Corrected there.

It is the same shape as the claims this series keeps finding in code comments,
and it was written by the same hand that had just spent a day cataloguing
them — which is the argument for the sweep being mechanical wherever it can
be, rather than a matter of care.
