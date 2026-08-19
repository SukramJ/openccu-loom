# CLAUDE.md — Device-profile catalogue

Loaded when you touch `internal/model/custom/`. Repo-wide rules: root
[`CLAUDE.md`](../../../CLAUDE.md).

## Add a new device type

The device-profile catalogue is **hand-maintained** — there is no
generator (ADR 0063). Adding a device is four edits and one measurement:

1. Read the CCU's own device description for the model (the embedded
   `godevccu` fixtures under
   `../godevccu/internal/embed/data/paramset_descriptions/` carry it for
   the simulated fleet). Note which channel holds which parameter — that
   is the fact the rest of the work rests on, and guessing it produces a
   profile that binds nothing.
2. Add the registration to `internal/model/custom/profiles.go`
   (device type, category, base channels, the schema pointer).
3. Add or reuse a channel-group schema in
   `internal/model/custom/profile_configs.go`. Reuse only when the
   parameter *and* the channel offsets match — a schema shared by two
   devices that place a field differently cannot serve both, and that is
   what the per-registration `Config` pointer is for.
4. A new `DeviceProfile` value needs a hand-written Go wrapper and a
   registered constructor under `internal/model/custom/<cat>/`;
   `TestEveryRegisteredProfileHasConstructor` fails without it.
5. Where the catalogue deviates from the reference implementation,
   record it in `notes/parity/by_design.md` with the wire fact behind
   it.

