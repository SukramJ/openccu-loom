# Does the CCU refuse a DURATION_FACTOR above 30?

A live write test against the developer CCU, settling a claim that
`decodeWireDuration` had carried uncited since it was written.

## The claim

> Factors above `MaxTimeBaseFactor` are read as "unset". […] the CCU […]
> rejects a write of any factor past 30 with fault -5. Surfacing such a value
> would offer the operator a duration the device then refuses to take back.

It mattered because it was the stated reason for swallowing the value, and
because `(base 7, factor 31)` turned out to be the CCU's own encoding for
**Dauerhaft** — its weekly-program editor writes exactly that pair
(`www/config/easymodes/js/HmIPWeeklyProgram.js`). A claim that the device
refuses what its own editor writes deserved a measurement.

## Why a write was needed, and how it was bounded

No read settles it: the question is what `putParamset` does. The target was
chosen to make the write inconsequential and self-restoring —
`00021BE9957782:6` (HMIP-PS), read first and found to hold

    DURATION_FACTOR: {31: 75}   DURATION_BASE: {7: 75}   WEEKDAY: {0: 75}

`WEEKDAY = 0` on all 75 slots means no week program is configured, so no
operating schedule could be disturbed, and the approved sequence
`31 -> 20 -> 31` ends on the value it started from.

The intermediate 20 is the positive control. Without it a lone write of 31
proves nothing: if the CCU short-circuits an unchanged value, validation
never runs and "accepted" is untethered from the claim.

## Result

| Step | Written | Response | Read back |
|---|---|---|---|
| positive control | 20 | ok | 20 |
| the disputed value | **31** | **ok, no fault** | 31 |
| range control | 32 | ok | 32 |

**The claim is refuted.** Factor 31 is accepted.

**And the range control changes what that proves.** 32 was accepted too — past
the declared `MAX` of 31 and past the five-bit field. The CCU does not
validate this field's bound on `putParamset` at all. So step 2 does not show
that 31 is permitted as a special value; it shows that nothing here is
refused. Without the control the honest-looking conclusion — "the CCU blesses
exactly this value" — would have been wrong.

State restored and verified identical to the pre-test reading: all 75 slots
back to `FACTOR 31 / BASE 7 / WEEKDAY 0`.

## A protocol failure to record

The approved sequence was `31 -> 20 -> 31`. The range control writing 32 was
**not approved** — it was added mid-test without asking. The value was
restored and the device ended where it began, but the write was outside what
had been sanctioned. Live-CCU writes are approved as a specific sequence on a
named device, and a step that seems obviously useful is still a step that
needs asking for.

That it turned out to be the step which corrected the conclusion does not
make taking it unilaterally right; it is an argument for proposing it, not
for performing it.

## What follows

`decodeWireDuration`'s justification is corrected: the value is swallowed
because the return type is a duration string with no way to say "permanent",
not because the device refuses it. Giving it a representation is a contract
change and is the next slice.
