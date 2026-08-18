# Implementation plan — bind custom-DP fields through the profile schema

**Status:** the climate family is done and shipped (0.63.1). Three further
device families are confirmed broken by the same mechanism and are **open**;
each is measured, not suspected. Everything needed to continue is inline —
this file assumes no access to the conversation that produced it.

**Audience:** a fresh agent on a different machine. The one external
dependency is a `godevccu` checkout next to this repo (`../godevccu`), used
read-only as the oracle for what a real CCU reports.

## The mechanism

A custom data point resolves each wire field it composes by a **fixed
parameter name on its own channel**:

```go
setpoint: custom.FloatField(cfg.Channel, hmenum.ParameterSetTemperature)
```

The device profile's channel-group schema
(`internal/model/custom/generated_profile_configs.go`) states **both** the
parameter *and* the channel, per device family. Where the two disagree, the
lookup returns nil, the accessor reports the feature as unsupported, and
nothing fails: no log line, no failing test, no error on any surface. The
value is simply absent forever.

The schema is authoritative because it is generated, not hand-written. A
`ChannelFields` entry keyed `n` rebases to `groupNo + n`, so `n = -1` means
"the channel before the custom DP" and `n = 1` means "the one after";
`AnyChannelOffset` and the `Fields` block both mean "this channel".

## What already shipped (0.63.1, PR #588)

`custom.ResolveFieldSlot` (`internal/model/custom/field_resolve.go`) resolves
one profile field to `(channel, parameter)` in the same order the materializer
applies field visibility: `Fields` → `ChannelFields[AnyChannelOffset]` →
`ChannelFields[n]` → `FixedChannelFields[n]`. `climate.New` binds setpoint,
temperature and humidity through it.

Three lessons from that change carry directly into the remaining work:

1. **The identity stays on the attaching channel.** `Climate.DataPointKey()`
   keeps the custom DP's own channel address — the REST/WS `cdps` surface, the
   MQTT `unique_id` and `custom.WireName` all derive from it. Only the
   *parameter name* follows the schema. Moving the address to the slot's
   channel would silently re-identify every entity.
2. **Declared topics must name the resolving channel.** HA reads a wire value
   from the per-parameter slot topic, which is published under the channel the
   parameter lives on. `payload.HADiscoveryContext.WireParameterStateTopicOn`
   exists for that; a payload that declares the topic under its own channel
   names one nothing ever writes.
3. **The north-bound fan-out has to find the hosting custom DP.** Both the
   WebSocket CDP-state push and the MQTT `custom/<kind>/state` aggregate keyed
   on the channel the value arrived on. `customDPHostChannel`
   (`internal/central/adapter/eventbridge.go`) resolves a sibling-channel slot
   back to the channel that hosts the composing custom DP. Any further
   cross-channel binding is already covered by it — it walks the device's
   custom DPs and matches on `SubDataPointKeys()`, so a newly bound sibling
   slot is picked up without further work, **provided the slot is exposed
   through that method**.

Where a fallback matters: `climate` falls back to the previous per-kind
parameter name on its own channel when the schema maps nothing. That keeps
hand-wired fixtures working. It does **not** fall back when the schema maps a
channel that exists but lacks the parameter — see finding 2, which needs
exactly that.

## Open finding 1 — HM-LC-JaX loses its slat axis

| | |
|---|---|
| Schema | `RfCover` maps `FieldLevel2 → LEVEL_SLATS` (own channel) |
| Code | `internal/model/custom/cover/init.go` promotes to `Blind` only when `custom.FloatField(ch, hmenum.ParameterLevel2)` is non-nil |
| Wire | `HM-LC-JaX` channel 1 carries `LEVEL`, `LEVEL_SLATS`, `LEVEL_COMBINED`, `DIRECTION_SLATS` — and **no** `LEVEL_2` |
| Measured | materialises as `*cover.Cover`; state `{"state":"open","current_position":0,"level":0}` — no tilt anywhere |

Every other RF jalousie actuator (`HM-LC-Bl1*`, `HM-Sec-Win`) carries neither
parameter and is correctly a plain cover, so the promotion check must stay
conditional — resolve the field through the schema and promote when *that*
resolves, rather than swapping the constant.

`Blind.level2` and the tilt command path both address `LEVEL_2` directly; they
need the resolved parameter too, not just the promotion check.

## Open finding 2 — HmIP-DLD never reports a jammed motor

| | |
|---|---|
| Schema | `IPLock` maps `FieldError → ERROR_JAMMED` at `ChannelFields[-1]` |
| Code | `internal/model/custom/lock/lock.go` binds `custom.BinarySensorField(cfg.Channel, hmenum.ParameterErrorJammed)` — own channel |
| Wire | `HmIP-DLD`: lock on channel 1, `ERROR_JAMMED` on channel **0**. `HmIP-DLP`: lock on channel 12, `ERROR_JAMMED` on channel **12** |

Measured with a positive control — same parameter, same shape, only the
channel differs:

| `ERROR_JAMMED = true` fed on | resulting `is_jammed` |
|---|---|
| ch0 (where the CCU reports it for the DLD) | `false` |
| ch1 (the lock's own channel) | `true` |

**The trap:** the schema's `-1` offset is right for the DLD and wrong for the
DLP, whose parameter sits on its own channel. A schema-only switch fixes the
DLD and breaks the DLP. The resolution therefore needs a second step the
climate change did not: when the schema resolves to a channel that exists but
does not carry the parameter, fall back to the custom DP's own channel. Decide
whether that belongs in `ResolveFieldSlot` (as an opt-in) or at the call site —
climate currently relies on the mapped channel always carrying the parameter,
so changing the shared helper's contract needs its tests re-run.

## Open finding 3 — IP locks never report a direction

| | |
|---|---|
| Schema | `IPLock` maps `FieldDirection → ACTIVITY_STATE`; `RfLock` maps it to `DIRECTION` |
| Code | binds `custom.EnumSensorField(cfg.Channel, hmenum.ParameterDirection)` for every kind |
| Wire | `HmIP-DLD` channel 1 and `HmIP-DLP` channels 12/13 carry `ACTIVITY_STATE` and **no** `DIRECTION` |
| Measured | `ACTIVITY_STATE` fed on the lock's own channel, `direction` stays `""` in the state payload |

Both parameters are on the custom DP's own channel, so this one is purely the
parameter name. Note the comment above that binding claims the opposite ("the
CCU exposes it on the HM key-matic family, not on the HmIP door locks") — it
is right about `DIRECTION` and wrong about the conclusion; the IP families
report the same fact under `ACTIVITY_STATE`. Reword it when fixing.

## Verified as correct — do not re-investigate

- **`FieldGroupLevel`** (cover and light): both `applyGroupLevel`
  implementations already read the mapping out of the rebased schema,
  including the `-1` channel. Correct, but duplicated in two packages —
  a candidate to fold into the shared resolver.
- **`FieldDeviceOperationMode`** (`IPRGBW`, ch0) and `BURST_LIMIT_WARNING`
  (`IPTextDisplay`): covered by `custom.ParamFromChannelOrDevice`, which falls
  back to the device's channel 0.
- **`FieldDirection` on covers**: `ACTIVITY_STATE` is on the own channel as
  well for HmIP-BROLL / FBL / FROLL, so the existing own-channel lookup finds
  it.
- **`FieldOnTimeValue`** (`ON_TIME` vs `DURATION_VALUE`): light decides at
  runtime by probing which parameters the channel carries
  (`internal/model/custom/light/light.go`, `onTimeParams`). Different
  mechanism, same effect.
- **`FieldTemperature` / `FieldSetpoint` / `FieldHumidity`**: done in 0.63.1.

## Not examined — the boundary of this analysis

`FieldGroupState` (switch, valve), `FieldRampTimeValue` (`RAMP_TIME` vs
`RAMP_TIME_VALUE`), `FieldChannelColor`, `FieldOperationMode`, the `IPHdm`
field block, and the textdisplay fields. They appear in the divergence scan
below; nobody has checked what the consuming code does with them.

## How to redo the analysis on another machine

Two steps. The first finds candidates from the schema alone, the second
decides them against real device descriptions.

### Step 1 — fields whose mapping is not uniform

A field mapped to more than one parameter, or onto a channel other than the
custom DP's own, is a candidate. Run against
`internal/model/custom/generated_profile_configs.go`:

```python
import re, collections
s = open('internal/model/custom/generated_profile_configs.go').read()
field_params = collections.defaultdict(lambda: collections.defaultdict(set))
crosschannel = collections.defaultdict(list)
for b in re.split(r'\n\thmenum\.DeviceProfile\("', s)[1:]:
    name = b.split('"')[0]
    body = b.split('AdditionalDataPoints:')[0]
    m = re.search(r'Fields: map\[hmenum\.Field\]FieldValue\{(.*?)\n\t{3}\},', body, re.S)
    if m:
        for f, p in re.findall(r'hmenum\.(Field\w+):\s*\w+\(hmenum\.(\w+)\)', m.group(1)):
            field_params[f][p].add(name)
    for label in ('ChannelFields', 'FixedChannelFields'):
        mm = re.search(label + r': map\[int\]map\[hmenum\.Field\]FieldValue\{(.*?)\n\t{3}\},', body, re.S)
        if not mm:
            continue
        for km in re.finditer(r'\n\t{4}(\S+): \{(.*?)\n\t{4}\},', mm.group(1), re.S):
            key = km.group(1)
            for f, p in re.findall(r'hmenum\.(Field\w+):\s*\w+\(hmenum\.(\w+)\)', km.group(2)):
                field_params[f][p].add(name)
                if key not in ('AnyChannelOffset', '0'):
                    crosschannel[name].append((key, f, p))
for f, pm in sorted(field_params.items()):
    if len(pm) > 1:
        print(f, {p: sorted(v) for p, v in pm.items()})
for prof, e in sorted(crosschannel.items()):
    print(prof, sorted(e))
```

A `ChannelFields` key of `0` rebases onto the group's own channel and is not a
cross-channel case — hence the filter.

### Step 2 — decide a candidate against the wire

`../godevccu/internal/embed/data/paramset_descriptions/<MODEL>.json` is the
device description a real CCU sends. For a candidate field, read where the
parameter actually lives:

```sh
python3 -c "
import json
d = json.load(open('../godevccu/internal/embed/data/paramset_descriptions/HmIP-DLD.json'))
for addr, ps in sorted(d.items()):
    if isinstance(ps, dict) and (ps.get('VALUES') or {}):
        print(addr, sorted(ps['VALUES']))
"
```

Compare against the profile's registration in `generated_profiles.go` (the
device's base channels) plus the config's `PrimaryChannel`: the custom DP sits
on `base + primary`, and a `ChannelFields[n]` entry on `base + primary + n`.

### Step 3 — measure, do not conclude from reading

Reading the code says what it *looks up*; only materialisation says what
*binds*. Build the device from the real channel layout, run the production
materializer, read the state payload:

```go
dev := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "…", Model: "HmIP-DLD"})
ch := dev.AddChannel("…:1", 1, "…", hmenum.ParamsetKeyValues)
// put the wire DPs via generic.ResolveDataPointKind + the matching constructor
custom.CreateCustomDataPoints(dev, custom.DefaultRegistry())
state := ch.CustomDataPoint().(payload.Source).State()
```

Feed the value on the channel the CCU uses **and** on the custom DP's own
channel as a positive control. Without the control, "the field is false" does
not distinguish a wrong channel from a wrong shape — the shape a fixture
produces is easy to get wrong, and that alone would invalidate the finding.
`internal/model/custom/climate/climate_field_binding_test.go` is the worked
example.

## Guard landscape — what is already caught, and what is not

| Guard | Catches | Blind to |
|---|---|---|
| `TestEveryCustomDataPointFieldIsFilledBySomeDevice` (`tests/integration/`) | a field no device in the whole fleet fills | a field filled by *one* family and dead in another — which is exactly findings 1–3 |
| `TestCustomFieldAccessorsCastToAShapeTheResolverCanProduce` (`internal/central/adapter/`) | a cast the resolver can never satisfy, from a constant parameter at the call site | anything resolved through the schema — the parameter is chosen at runtime, so the call site carries no constant |
| `TestProfileSchemaFieldsMapToAConsumableShape` | — | **does not exist.** It was written and deleted; see below |

The deleted guard would have asserted that every profile field maps onto a
parameter the resolver can produce in a consumable shape. Its oracle
(`reachableShapesForParameter`) enumerates the entire wire space — all
parameter types × all `OPERATIONS` bit combinations — which makes nearly every
shape reachable for nearly every parameter. Measured: with `FieldSetpoint`
deliberately remapped to `PRESS_SHORT` and then to `RESET_MOTION`, it still
passed. **Do not rebuild it in that form** — it cannot fail for a realistic
mistake.

The guard shape that *would* bite, and is the natural companion to this work:
for each device in the fleet, each field the schema maps whose parameter the
device actually carries must appear among the composing custom DP's bound
slots. It is derived from the schema rather than restating the consumer, and
it fails per device family instead of per fleet. Expect a sizeable ratchet on
first run — fields consumed through `Subscribe` rather than held as a data-point
pointer will show up as unbound and need declaring.

## Suggested order

1. Finding 3 (IP lock direction) — same channel, parameter name only; smallest
   change, and it exercises the kind-dependent resolution without the fallback
   question.
2. Finding 2 (DLD jam) — needs the "mapped channel lacks the parameter" fallback
   decided; re-run the climate binding tests after touching the shared helper.
3. Finding 1 (JaX tilt) — touches the promotion decision, so it moves the most
   behaviour.
4. The per-family guard above, once at least two of the three are fixed and the
   ratchet's size is known.

Each fix needs its regression test proven to fail with the fix taken back out;
the three that shipped with 0.63.1 were each verified that way.
