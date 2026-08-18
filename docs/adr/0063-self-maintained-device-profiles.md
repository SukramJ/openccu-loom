# ADR 0063 — Device profiles are maintained here, not generated

- Status: accepted
- Date: 2026-08-18

## Context

The device-profile catalogue — which channel group a device model
materialises a custom data point on, and which wire parameter each
composed field maps to — was generated from the reference
implementation's own registry by `script/generate_profiles.py` into three
files carrying `Code generated … DO NOT EDIT`:

| File | Content |
| --- | --- |
| `generated_profiles.go` | 142 profile registrations (device type → profile, base channels) |
| `generated_profile_configs.go` | 33 channel-group schemas (field → parameter, field → channel offset) |
| `generated_default_data_points.go` | the default data-point offsets |

That arrangement bought free device coverage: a new device in the
reference implementation arrived here with one `make generate`. Its cost
surfaced when a profile turned out to be **unable to express the truth for
two devices sharing it**.

`IPLock` maps `Field.ERROR` to `ERROR_JAMMED` at channel offset `-1`. The
CCU reports that parameter on channel 0 for the HmIP-DLD, whose lock sits
on channel 1 — the offset is right. It reports it on channel 12 for the
HmIP-DLP, whose lock also sits on channel 12 — the offset is wrong. One
offset per profile cannot serve both, and the file that would have to say
so is regenerated on every run.

The three ways out are: work around it at runtime in the binder (a
fallback that papers over the data and hides the next wrong offset), fix
it upstream and regenerate (correct, but the schema shape still allows
only one offset per profile), or own the data.

Two further facts shaped the decision.

**The guards that appeared to protect the generated data mostly do not.**
`TestDeviceProfileRegistryParity`, `TestProfileConfigCatalogParity` and
`TestDefaultDataPointsParity` compare the registry's size against a
constant the generator wrote in the same run — they detect a hand edit,
nothing else. `TestProfileRegistryCountsMatchAiohomematicSource` compares
three counts against numbers a human transcribed from the reference
implementation once. Only `TestProfileFieldMappingsMatchAiohomematicSource`
compares content, and only for four of the 33 configs, against literals
that were also hand-copied. The protection against silent divergence was
thinner than the "generated" label suggested.

**The catalogue is not where most defects were.** Of four binding defects
found in one pass over the custom-data-point layer, three were consumer
side — the schema was right and the Go code looked for a different
parameter on a different channel. Exactly one was a data limitation. The
generated data is good; it is simply not correctable in place.

## Decision

**OpenCCU-Loom maintains its device-profile catalogue itself.**
`script/generate_profiles.py` is removed, the three files lose their
`DO NOT EDIT` headers and are renamed to `profiles.go`,
`profile_configs.go` and `default_data_points.go`, and they become
ordinary hand-maintained source.

The provenance does not disappear with the generator: the catalogue was
derived from the reference implementation (MIT) and
[`docs/attribution.md`](../attribution.md) continues to say so. What
changes is who is responsible for it from here on.

Three obligations come with ownership:

1. **A new device is added here, by hand.** There is no regeneration step
   any more. The workflow in [`CLAUDE.md`](https://github.com/SukramJ/openccu-loom/blob/main/CLAUDE.md)
   §"Add a new device type" is rewritten accordingly: add the
   registration, add the config if the device needs a new channel-group
   shape, add the constructor, add the test.
2. **Every deviation from the fork point is recorded** in
   [`notes/parity/by_design.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/parity/by_design.md),
   naming the device, the field, and the wire fact that justifies it —
   the channel and parameter the CCU's own description reports. A
   deviation without that fact is a guess.
3. **The count and content pins against the reference implementation are
   replaced, not deleted silently.** Their premise ("our catalogue equals
   theirs") is gone; what remains checkable is that our catalogue is
   internally coherent. `tests/contract/device_profile_catalogue_test.go`
   asserts that every registered profile resolves to a config, that no
   config is orphaned, that every field mapping names a non-empty
   parameter, and that device types are normalised — invariants over our
   own data rather than a comparison against someone else's.

## Consequences

- The HmIP-DLP can be given its own channel-group config, and the runtime
  fallback that would otherwise be needed to bind `ERROR_JAMMED` on both
  door locks is not written. A wrong offset stays visible as a wrong
  offset.
- New devices no longer arrive for free. When the reference
  implementation gains a device, adding it here is a manual step, and one
  that nothing detects automatically — this is the real price of the
  decision, and it is accepted deliberately. The mitigation is social,
  not mechanical: both projects are maintained by the same author, so a
  device added there is added here in the same sitting.
- The three files enter linting. `golangci-lint` skips files marked as
  generated; ~2,500 lines of catalogue join the linted surface with the
  header removed, and stay formatted by `gofumpt` like any other source.
- `make generate` no longer needs a Python toolchain. Python remains
  listed as a prerequisite for the cross-stack snapshot scripts, which
  are unaffected.
- The cross-stack model-snapshot pipeline keeps working and keeps its
  meaning: it compares the *resulting model* against the reference
  implementation's, so a profile deviation shows up there as drift and
  needs its `by_design.md` entry — which is the enforcement path for
  obligation 2.
- Reverting is cheap for as long as the catalogue stays close to the fork
  point: the generator is one `git revert` away in the history, and
  re-running it would overwrite the files. Once deliberate deviations
  accumulate, that ceases to be true — which is the point at which this
  decision becomes load-bearing rather than reversible.
