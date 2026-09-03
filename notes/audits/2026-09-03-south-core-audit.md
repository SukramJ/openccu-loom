# South and domain-core audit — duplication, layering, authority

**Run** 2026-09-03, against `main` at 0.72.2 · **Scope** `internal/client`,
`internal/central`, `internal/model`, `internal/store`, `internal/alarm`,
`internal/security`, `internal/parameter`, `internal/routingkey`,
`internal/config`, `internal/history`, `pkg` — 595 non-test Go files,
176,922 lines.

**No code was changed.** This document is the report that was asked for
instead. The full verdict sets are in
[`2026-09-03-south-core-code-findings.json`](./2026-09-03-south-core-code-findings.json)
and
[`2026-09-03-south-core-firmware-findings.json`](./2026-09-03-south-core-firmware-findings.json).

## What this pass is

It repeats [the 2026-08-30 domain-core audit](./2026-08-30-domain-core-audit.md)
whose 36 findings were folded in #664 and after, and it widens it twice.

**`internal/client` is in scope for the first time.** The August pass read the
core from `central` inward; the south-bound client — 61 files, 18,249 lines,
the three transports, the backends, ReGa, the reliability stack — had never
been read in this frame at all.

**A fourth question was added: whose authority does a rule rest on?** The
firmware series of 31.08 / 01.09 / 02.09 grounded six, then forty-two, then
five individual rules against the CCU sources. This pass asks it of the whole
core at once, and it can now ask it of HmIP as well: the CCU's HmIP server was
previously unreadable, so for every HmIP rule the Python port had been the
only witness. It is readable now, which is why a third of this report is about
HmIP.

| | Question | Judged | Stands |
|---|---|---|---|
| Q1 | Is the same domain rule defined twice inside the core? | 171 | **113** |
| Q2 | Does knowledge sit in the wrong layer? | 100 | 65 |
| Q3 | Has target-system knowledge leaked into the core? | 61 | 44 |
| Q1× | Cross-package duplicates (mechanical match, then read) | 71 | 58 |
| Q4 | Whose authority does the rule rest on? | 234 | **57 contradicted** |

403 code candidates judged: 73 refuted by the first lens, **49 killed by the
second, adversarial lens**, 280 stand (13 high, 97 medium, 170 low). 234 rules
grounded against the firmware: 36 confirmed, 109 narrowed, **57 contradicted**,
27 Tier-1-silent, 5 not performable. 204 of the 234 groundings were themselves
challenged by a second agent whose brief was to break them.

## Coverage

**595 of 595 non-test files read end to end (100%), 0 scanned, 0 unopened.**
Every reader returned a per-file receipt; the receipts were checked
mechanically against the partition lists — no file appears twice, none is
missing. "No findings" in a subtree therefore means *clean*, not *not looked
at*.

## The measurement that frames everything else

The 29 readers inventoried **2675 domain rules** — decisions about CCU or
device semantics that could in principle live somewhere else — and recorded,
for each, what the code itself rests on:

| The rule's authority, as the code states it | Rules |
|---|---|
| a comment asserting it, with no source | **1129** |
| nothing at all — a bare literal | **987** |
| the Python port (`aiohomematic` et al.) | 458 |
| the device descriptor, read at runtime | 57 |
| **a CCU firmware source** | **23** |
| a test, and only a test | 21 |

That is the shape of the problem this pass set out to measure. 79 % of the
core's domain rules rest on an assertion or on nothing. It is not evidence
that they are wrong — 36 of the 234 checked came back confirmed exactly, and
109 more were right within their scope. It is evidence that being right was
not, until now, something the code could demonstrate.

## The findings that should not wait

### 1. Four of eight fixed colours are decoded to the wrong colour

`internal/model/custom/light/color.go:451` numbers the colour slots
`BLACK, RED, GREEN, YELLOW, BLUE, MAGENTA, CYAN, WHITE`. The CCU numbers them
by the RGB bit pattern (bit 0 = blue, bit 1 = green, bit 2 = red):

| index | 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 |
|---|---|---|---|---|---|---|---|---|
| CCU | BLACK | **BLUE** | GREEN | **TURQUOISE** | **RED** | PURPLE | **YELLOW** | WHITE |
| ours | Black | **Red** | Green | **Yellow** | **Blue** | Magenta | **Cyan** | White |

Three independent Tier-1 sources agree on the CCU's order: the HmIP server's
own colour enum (`HMIPServer
de.eq3.cbcs.devicedescription.channelspecification.SimpleRGBColor`, from which
the `COLOR` state parameter's `VALUE_LIST` is built via
`…stateparameter.GeneralStateParameterFactory#createColorSelectionParameter`),
the WebUI control
`src/webui/rega/www/esp/controls/opticalsignalreceiver.fn:64`
(`"black\tblue\tgreen\tturquoise\tred\tpurple\tyellow\twhite"`, emitted as
`<option value=#loop#>` and written back with `setDpState(oColor.ID(),
this.value)`), and the easy-mode editor
`src/webui/www/config/easymodes/DIMMER_VIRTUAL_RECEIVER/getColorElement.tcl:10-17`.

That the *index* rather than the label reaches us is settled separately:
`HMIPServer de.eq3.cbcs.legacy.bidcos.rpc.internal.DeviceUtil#convertParameterValue`
substitutes an ENUM's ordinal for its label when the flag is set, and the
shipped configuration sets it — `etc/config_templates/crRFD.conf:47`,
`Legacy.Parameter.ReplaceEnumValueWithOrdinal=true`.

Affected: HmIP-BSL, HmIP-MP3P channels 2-5, HmIPW-WGC. `Color()`,
`ColorName()` and everything downstream mislabel indices 1, 3, 4 and 6;
`SetColor`'s optimistic `l.color.OnEvent(int32(c))` (`color.go:606`) seeds the
store with an ordinal the CCU will not echo. The write path is correct — it
writes the label, and its comment at `color.go:610` states the right order
while `Color()` contradicts it. `ChannelHsColor` is label-driven and therefore
correct, which makes it the sibling that will visibly disagree.

Provenance: `aiohomematic/model/custom/light.py:63` declares `FixedColor` as a
`StrEnum` of labels and carries no ordinal at all. The numbering is a Go-side
invention with no upstream source — a convention substituted for an input
nobody had.

### 2. `reportValueUsage` is classified as a read, and it is a write

`internal/client/reliability/classifier.go:63` lists `reportvalueusage` in
`readMethods`, whose own header says the table holds methods that return CCU
state "without mutating it".

`src/rfd/XmlRpcMethods.cpp:700-723` documents the method as telling the
interface process how often a value is used **so that it can create or delete
the link to the component**. It persists the per-channel usage record and it
performs radio configuration: the direct link peer between the channel and the
central is added or removed. Its BidCos return value is a transmission
verdict, not a query result — `false` means the device was unreachable, the
change is queued, and `CONFIG_PENDING` is now set on its MAINTENANCE channel.

`internal/central/adapter/central_links.go:224` calls it per channel with
`refCounter` 1 to create and 0 to tear down, the teardown also zeroing
`PRESS_LONG`.

This one reaches beyond the code: the project's own rule for the developer's
live CCU is that **reads are free and writes need approval**. A method
classified as a read, on a table whose contract says reads do not mutate, in
fact reconfigures device links. Any procedure that relied on "this is only a
read" was relying on a false premise here.

### 3. `ON_TIME_LIST`: four labels we send do not exist, five the device offers are missing

`internal/model/custom/light/sound_led.go:19` carries the table
`[100MS, 200MS, 300MS, 400MS, 500MS, 600MS, 700MS, 800MS, 900MS, 1S, 2S, 3S, 4S, 5S]`
plus "`> 5000 ms` means `PERMANENTLY_ON`". The device's list, built by
`HMIPServer …stateparameter.GeneralStateParameterFactory#createOnTimeListParameter`,
is 16 entries:

    100MS 200MS 300MS 400MS 500MS 700MS 1S 2S 3S 5S 7S 10S 20S 40S 60S PERMANENTLY_ON

`600MS`, `800MS`, `900MS` and `4S` do not exist on the device;
`ConvertFlashTimeToOnTimeList` returns them for inputs in 551-650, 751-850,
851-950 and 3501-4500 ms and the string goes straight into the atomic turn-on
`put_paramset` (`sound_led.go:341`). And because the device does express 7 s
through 60 s, the `> 5000 ms` rule silently converts an ordinary 10-second
flash request into a permanently-on LED.

### 4. The optical alarm pattern is chosen by list position

`internal/alarm/outputs/manager.go:475` and `outputs/testfire.go:80` both pick
`lights[len(lights)-1]` — the last entry of the device's
`OPTICAL_ALARM_SELECTION` value list — as the default alarm pattern.

The list is fixed and stable across HmIP-ASIR, -ASIR-2 and -ASIR-O
(`HMIPServer …channelspecification.SignalOptical`): `DISABLE_OPTICAL_SIGNAL`,
then the four `*_REPEATING` alarm patterns at indices 1-4, then three
`CONFIRMATION_SIGNAL_*` one-shots at 5-7. The positional rule therefore
resolves deterministically to `CONFIRMATION_SIGNAL_2` — "long short short", a
confirmation blink, the entry furthest from what an optical alarm needs. An
optical-only activation or test fire writes it, `OPTICAL_ALARM_ACTIVE` goes
true, and the watchdog is satisfied.

This is the same defect class as finding 1, and it is the one the project's
own rules name explicitly: a position in a list stood in for a property the
list does not carry.

### 5. The astro offset accepts ±720 minutes; every device declares ±128

`pkg/hmapi/rest_contract.go:398` states ±720 and
`internal/model/schedule/simple.go:197` enforces it. All 47 models in the
descriptor corpus declare `01_WP_ASTRO_OFFSET` as `INTEGER MIN -128 MAX 127`.
The CCU's own weekly-program editor holds no constant at all — it reads
`ASTRO_OFFSET_MIN`/`MAX` out of the paramset description
(`src/webui/www/config/easymodes/js/HmIPWeeklyProgram.js:1128`, filled by
`hmipWeeklyProgramDevice.tcl:39-46`) and clamps the input to them. So the
firmware's answer is "whatever the channel declares", and what every channel
declares is a fifth of what we accept. `rawconvert.go:832` passes anything
within ±720 to the wire.

The same finding carries a second contradiction: the "up to 24 slots per
channel" comment at `rest_contract.go:361`. The editor's own `_getMaxEntries`
(`HmIPWeeklyProgram.js:3413`) returns 75 for most profiles and 69 for
HmIP-MP3P, HmIPW-WRC6 and HmIP-BSL.

### 6. `MAX > 1.0` tests the wrong field for "last known level"

`internal/central/adapter/uischema_link.go:175` decides that a level parameter
supports last-known-level when its `MAX` exceeds 1.0, and the SPA gates its
control on the same test. The firmware declares `max="1.0"` and carries the
sentinel as a *separate* member of the SPECIAL set:
`src/devicetypes/rftypes/rf_d.xml:608-615` —
`<special_value id="OLD_LEVEL" value="1.005"/>`. The paramset description
exports MAX and SPECIAL as different fields. A write of 1.005 is accepted
because the firmware widens an internal `effective_max` while leaving `max`
at 1.0 — precisely the mechanism our rule mistakes for a raised MAX. Our
comment is right about the number and its meaning, wrong about where it lives,
and the code tests the field that does not carry it. The firmware's own name
for it is `OLD_LEVEL`.

### 7. A patch widens a CCU-declared range on both ends

`internal/store/patches/patches.go:243` rewrites HM-CC-VG-1 channel 1
`SET_TEMPERATURE` bounds to 4.5 / 30.5, with the reason "CCU returns invalid
MIN/MAX bounds for virtual heating groups". The firmware declares them:
`opt/HMServer/groups/groupdefinitions.xml:504` gives
`min="5.0" max="30.0" default="20.0" unit="°C"`. 4.5 / 30.5 are HM-CC-RT-DN's
own bounds (`rf_cc_rt_dn.xml:2395`) — the member device's range substituted
for the group's, which is deliberately narrower. Half the patch's reason is
corroborated (the CCU does report these as strings), half is not: the bounds
exist and we overwrite them with wider ones, so a setpoint outside the group's
declared range now passes validation and the SPA slider offers it.

### 8. Two XML-RPC formatting rules rest on a fault code that means something else

`internal/client/transport/xmlrpc/value.go:100` and `:141` avoid `<i4>` and
avoid `<double>1</double>`, both attributed to CCU rejection with fault -5.
Neither holds. `src/libXmlRpc/src/XmlRpcValue.cpp:260` dispatches `I4_TAG` and
`INT_TAG` to the same parser — and `:515` shows libXmlRpc *emits* `<i4>`
itself, so a client that rejected it could not talk to the CCU at all.
`<double>1</double>` is parsed with `strtod` (`:577`) and on the HmIP side with
`Double.parseDouble`; both discard the literal text before any type check, so
no downstream validator can distinguish "1" from "1.0". Fault -5 in rfd is
"Unknown parameter". The behaviour is harmless; the stated reason is false,
which is what makes it dangerous — it will be cited the next time someone
needs to know what the CCU accepts.

### 9. The duty-cycle retry cannot fire on the path its own comment describes

`pkg/hmreliability/defaults.go:50` waits 40 s on fault -8 ("duty cycle") and
5 s on fault -10 ("transmission pending"). Fault -8 is raised at exactly one
place in the CCU source tree — inside `RFDevice::UpdateFirmware()`
(`src/rfd/RFDevice.cpp:1490`), gated by an instantaneous airtime-budget test.
No `setValue`/`putParamset` path emits it, and the HmIP surface has no -8 at
all. Fault -10 is real but HmIP-only, and it does not mean "busy for a
moment": it means the target device is unreachable and the command has been
persisted as pending configuration until the device next talks to the access
point — cleared by that event, not by waiting 5 s.

### 10. Two `duration` vocabularies on one siren operation

`internal/central/adapter/custom_dp_dispatcher.go:675` reads a bare number as
**milliseconds** (`anyToDuration`, `:1089`); the siren's own service handler
`internal/model/custom/siren/payload.go:97` reads the same key as **seconds**.
`{"duration": 30}` therefore writes `DURATION_VALUE=0` through REST/WS/MQTT
cdp-invoke and `DURATION_VALUE=30` through the per-service MQTT topic — same
key, same device, wire values 0 and 30. The invoke plane's own canonical timed
key is `seconds` (`custom_dp_dispatcher.go:986`), used by every other timed
operation; the siren branch accepts neither it nor the shared helper. No
in-repo caller emits the divergent input, which is why the second lens lowered
this from high to medium.

### 11. LINK-profile constraint matching exists twice and has already drifted

`internal/central/adapter/link_profile.go:161` compares floats with `num != v`;
`internal/store/linkprofile/store.go:439` compares them with `floatsEqual`
(ε = 1e-6 relative). One decoder answers "does this profile match" one way and
the other answers it differently for any value that survived a float
round-trip.

## Recurring shapes

**A position stood in for a property.** Findings 1 and 4, plus the
`WEEK_PROGRAM_CHANNEL_LOCKS` bit table (below) and the sysvar kind guess. Each
time, the code needed a value the source did not carry, and each time a
plausible ordering was used instead. This is the failure mode the project's
own rules name, and it produced three separate wrong values here.

**A comment that is right about the fact and wrong about the mechanism.**
Findings 6 and 8; also `mixins.go:90` ("some CCU firmware variants reject a raw
setValue" — the asymmetry is real, unconditional and channel-type-driven, not
variant-driven). The behaviour survives; the reason does not, and the reason is
what the next reader will act on.

**A constant where the CCU looks something up.** The astro offset, the slot
count, `CODE_ID` MAX, the install-mode clamp (`internal/model/hub/install_mode.go:214`
says 60-3600 s; BidCos-RF has no minimum and a maximum of 600 s). The firmware
reads these from the paramset description at runtime; we hold a number.

**A rule true in our scope and false one step wider.** 109 of 234 groundings.
The report records the boundary for each rather than the rule.

## `WEEK_PROGRAM_CHANNEL_LOCKS`, narrowed rather than settled

#690 replaced the formula with a device-derived bit. The firmware does define
the mapping, and it is still not the uniform 8×3 grid the key table assumes:
`src/webui/www_source/ise/js/iseHmIPWeeklyProgram.js:517-555` derives the bit
from the channel's index in `relevantChn` — the device's channel list filtered
to `*_VIRTUAL_RECEIVER` plus access-control types — **with explicit per-family
overrides** (HmIP-DLP `:12`→256, `:13`→512, `:4..:11`→1..128; HmIP-FWI
`ACCESS_TRANSCEIVER :1..:8`→bits 3-10; HmIP-DRG-DALI computed). Two firmware
facts do back the current table: the inverted semantics (set bit = manual =
schedule off, `:239`) and the stride of 3 (`:361`, `:614`). It is correct for
ordinary multi-actor virtual-receiver devices and wrong wherever the firmware
overrides the list.

## What could not be decided

**5 not performable.** Each names the input no source carries: the
target-channel key ordering; the RF tunable-white `COLOR_LEVEL`↔Kelvin mapping
(needs the colour temperature of the installed strips — a property of the
installation, not of any source); CUxD's connection lifetime and nil encoding
(CUxD is a third-party addon, absent from both trees); the garage `SECTION`
motion codes; the 3-second post-write hold window.

**27 Tier-1 silent.** The CCU sources do not decide them — among them the
BIN-RPC callback method set, the hub-scan floor, the recovery backoff bounds,
the two saturation-means-white cutoffs. For each, the report states what the
rule now rests on instead of promoting a witness to an authority.

## What the refutations say

122 of 403 code candidates died — 73 in the first lens, **49 in the second**.
The second lens killing 49 findings that a first careful pass had confirmed is
the strongest argument for keeping it: among them "default-central selection
is defined three ways", the device-eviction policy triple, the `LOWBAT`
spelling set, and the HTTP-status classification quadruple. The usual reasons
recur: two readings of one datum each right for its caller, repetition the type
system forces, and stores that legitimately persist what the model defines.

On the firmware side the ratio runs the other way: only 36 of 234 rules came
back confirmed exactly, but 109 were narrowed rather than refuted — right where
they are applied, wrong if generalised. Recording the boundary is the result.

## Method

29 readers, one per partition, each reading its files end to end and returning
a per-file receipt plus a rule inventory. Cross-partition duplicates were found
mechanically (name and constant collisions across package boundaries) and then
read by an agent — 405 constant collisions sifted to 47 real candidates. Every
candidate was verified against the code by one agent and attacked by a second
whose brief was to refute it. Every firmware grounding was challenged by a
second agent whose brief was to break the citation, with "does this source
answer the surface our code is on?" as the first line of attack.

Each verdict states its negative control — what the check would have shown had
the claim been false. Verdicts that could not produce a different answer in
that case are recorded as not performable rather than confirmed.
