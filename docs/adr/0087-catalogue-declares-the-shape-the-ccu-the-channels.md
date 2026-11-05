# ADR 0087 — The catalogue declares the shape, the CCU confirms the channels

- Status: accepted
- Date: 2026-11-05

## Context

A device profile names channel numbers: base channels in `profiles.go`, relative
offsets in `profile_configs.go`. The materializer turns them into channel groups
and custom data points without comparing them to what the CCU reports for the
device. A model that matches a longer family's prefix, or a schema shared by two
families, therefore inherits channels that are wrong for it, and nothing notices.

Comparing the catalogue with the eQ-3 device documentation (`script/hmipdoc`,
crRFD 1.60.1) produced this class of defect repeatedly:

| Model | Defect | Fixed in |
| --- | --- | --- |
| `HmIP-BDT-I` | The prefix bound the light to the state channel | #1038 |
| `HmIPW-DRS4` | The prefix declared groups 18 to 30 and put the week-profile channel 17 into group 18 | #1040 |
| `HmIP-LSC` | The shared schema built a light on the week-profile channel 2 and declared channels 3 and 4 | #1041 |
| `HmIP-WGT`, `HmIP-WGTC` and `-A` | The backlight's schema built dimmers on the switch channels 3 and 4 and took channel 4 out of the switch's group | this ADR |

Each fix is one more per-model entry. The check runs only when someone runs the
tool against a PDF that is not part of the repository, and it covers HmIP only.

Measured over 1,384 device descriptions (the PDF's generations, the firmware's
`rftypes` and `hs485types`, and the 400 `godevccu` fixtures), of which 613 match a
profile: nearly every (profile, role) pair resolves to the channel types of one
family. The exceptions were the defects above and channels absent from a description.

## Decision

**The catalogue keeps naming candidate channels, and it also declares which channel
types each role accepts. The materializer binds a channel only if the device has it
and its type is accepted.**

1. `ChannelGroupConfig` gains the accepted channel types per role: primary and
   secondary, and state. A role with no declared type is a catalogue error, enforced
   by `tests/contract/device_profile_catalogue_test.go`.
2. `addChannelGroupsToDevice` and `relevantChannels` work from the device's channels.
   They are the only place that decides membership, so groups, custom data points and
   the week-program targets cannot disagree. A candidate that does not fit is skipped
   and logged once per device at debug level, because a model's firmware generation
   can legitimately lack a channel.
3. `TestCatalogueFitsEveryFixture` walks the simulated fleet through the production
   ingest and fails on a skipped primary channel. Exceptions are a named ratchet with
   the wire fact behind each entry.
4. Entries that exist only to narrow a prefix (`hmipw-drs4`, the single-channel
   `IPRGBW` copy for `HmIP-LSC`) are removed. Their bindings stay the same.
5. `ScheduleRelevantChannelGroups` applies the same check, and `script/hmipdoc` reports
   the candidates the materializer skips.

The catalogue stays hand-maintained; ADR 0063 is not reopened.

### Rejected: deriving the channels from the types alone

A profile could drop channel numbers and bind every channel of a type. The type does
not identify the role. `DIMMER_VIRTUAL_RECEIVER` serves `IPDimmer`,
`IPFixedColorLight` and `IPSoundPlayerLed`, and the `script/hmipdoc` report lists
seven models with receiver channels outside every group, among them `HmIP-BWTH`,
`HmIP-FWI`, `HmIP-MP3P` and `HmIP-SMO230`. Binding by type alone would give those
channels new entities that no decision asked for.

### Rejected: a fixture guard on its own

A guard reports a wrong entry after it is written and only for models with a fixture.
Every variant still needs its own entry, and a device without a fixture stays
unprotected.

## Is this the best architecture?

- **Better than the comparison targets?** Yes. aiohomematic registers the same offsets
  and still has the `HmIPW-DRS4` and `HmIP-LSC` entries that caused the defects. The
  CCU's own WebUI already selects channels by `CHANNEL_TYPE`, for example in its
  weekly-program editor (see `weekprofile.TargetBitOrder`).
- **Better than before?** Yes. The class of defect is excluded at the single point that
  decides membership, for models with and without a fixture, instead of being repaired
  per model after a PDF comparison.
- **The right cut?** Yes. What a role is, is domain knowledge and stays in the
  catalogue. Which channels a device has is a fact of the connected CCU, in line with
  the non-goal of a static device database (`SPECIFICATION.md` §2.2).

## Consequences

- Compared over the 613 descriptions before and after, 31 change, in 13 models and
  three patterns: the four `HmIP-WGT` models lose the dimmers on channels 3 and 4;
  five `HM-LC-Dim1*` descriptions lose the absent channels from their group; and four
  descriptions lose a group whose primary channel they lack. Nothing else moves.
- The `HmIP-WGT` family has one multi-channel group left, so `HasSubDevices` turns
  false, and with MQTT sub-devices on its entities move back to the parent device.
- The accepted types are a judgement read from the documentation and the device
  descriptions. Deriving them from the data the guard checks would make the guard
  circular, so each role with several types was reviewed by hand. That review left out
  the `HmIP-WGT` secondaries and `KEY_TRANSCEIVER` as a state channel.
- A model reporting a type the catalogue does not accept loses that binding. The guard
  reports this for a model with a fixture. A model without one, such as a homebrew
  device on a shared prefix, shows it only in the debug log and through the missing
  entity. This is the real price of the decision.
- Tests of the catalogue need real channel types, and an empty list stays available for
  ad-hoc schemas in tests. `TestEveryChannelGroupRoleDeclaresItsChannelTypes` keeps it
  out of the catalogue.
- This diverges from the reference implementation and is recorded in
  [`notes/parity/by_design.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/parity/by_design.md).

## Not decided here

Parameters differ by firmware as well: the `HmIP-WSM` timer moved from `ON_TIME` to
`DURATION_VALUE` and `DURATION_UNIT` (#1041). Lights, valves and switches each
resolve their timer in their own way today. Whether one timer slot should replace them
is a separate decision; which other switch families are affected is not measured.
