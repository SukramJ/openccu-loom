# Wire Snapshots

## Ground-truth workflow

This directory contains two independent snapshot layers:

| Layer | Location | Ground truth | Purpose |
|---|---|---|---|
| Self-capture (regression lock) | `snapshots/` | Current Go implementation | Detect unintended changes to the wire encoding |
| aiohomematic reference | `aiohomematic_reference/` | aiohomematic Python | Detect drift between Go and the reference implementation |

**The reference layer is authoritative.** A green self-capture test
combined with a red reference test means the Go implementation has
a wire drift against aiohomematic that must be fixed in the production
code — not by updating the reference snapshot.

## Self-capture snapshots (`snapshots/`)

Every file under `snapshots/` is a golden JSON record of the exact
`SetValue` / `PutParamset` calls a Custom-DP setter emits when it
runs against a fake backend. The pin test (`snapshot_pin_test.go`)
re-runs each setter and compares the live output against the stored
record — a mismatch means the wire encoding has changed.

This catches encoding regressions like:

- A parameter value type changing (e.g. string vs. integer for an
  ENUM selector).
- An extra or missing parameter in a `put_paramset` batch.
- A `SetValue` being silently replaced by a no-op due to a broken
  state-change gate.

### Regenerate self-capture snapshots

After an intentional production-code change that alters the wire
encoding, regenerate all baselines in one shot:

```sh
make wire-snapshots
# or directly:
go test -tags=snapshot_gen ./tests/contract/wire_snapshots/
```

The generator overwrites every file under `snapshots/`. Commit the
updated snapshot files alongside the production change so the pin
test stays green.

## aiohomematic reference snapshots (`aiohomematic_reference/`)

Files under `aiohomematic_reference/` are ground-truth records produced
directly from the aiohomematic Python library. They capture what
aiohomematic actually sends on the wire for each setter, making them
the authoritative comparator for Go parity work.

### Regenerate reference snapshots

```sh
make wire-reference
# or directly:
python3 script/aiohomematic_wire_snapshots.py
```

Requires aiohomematic to be importable (the script auto-discovers the
sibling-repo venv at `../aiohomematic/.venv/`).

### Compare Go against aiohomematic reference

```sh
make wire-compare
# or directly:
go test -tags=wire_reference ./tests/contract/wire_snapshots/ -run TestReferenceCompare -v
```

This test runs under the `wire_reference` build tag (excluded from
`make test`) because it deliberately fails for every known drift until
the production code is corrected.

**Do not skip or xfail failing cases in `reference_compare_test.go`.**
The failing tests ARE the drift list. Fix the production code, then
re-run.

### Current drift status

As of the initial reference snapshot generation, 11 of 17 covered
setters have wire drifts against aiohomematic:

| Setter | Drift class |
|---|---|
| DRGDaliLight/SetEffect | EFFECT sent as INT index; aiohomematic sends STRING |
| RGBWLight/SetEffect | EFFECT sent as INT index; aiohomematic sends STRING |
| RGBWLight/SetColor | 2 SetValues instead of 1 PutParamset (atomicity) |
| ColorLight/SetColor | 2 SetValues instead of 1 PutParamset (atomicity) |
| ClimateRF/SetProfile | 3 SetValues instead of 1 PutParamset (atomicity) |
| Siren/TurnOff | Empty string instead of DP-default for alarm selection |
| Siren/TurnOn | Missing DURATION in paramset |
| SoundPlayer/PlaySound | Extra DURATION_UNIT/DURATION_VALUE in paramset |
| Blind/SetCombined | Unconditional STOP; aiohomematic omits on first call |
| Blind/SetTilt | Same STOP drift as SetCombined |
| TextDisplay/WriteRows | 10 SetValues instead of per-row PutParamsets |

## How do I review a wire drift?

1. Run the pin test:

   ```sh
   go test ./tests/contract/wire_snapshots/
   ```

2. A failure prints the differing call(s) in a structured format:

   ```
   [label] call[0] mismatch:
     want: {"method":"SetValue","address":"VCU0001:3","parameter":"STATE","value":true}
     got:  {"method":"SetValue","address":"VCU0001:3","parameter":"STATE","value":1}
   ```

3. Decide: is the change intentional (update snapshot + document)
   or a regression (fix production code)?

4. For reference drift (aiohomematic mismatch), always fix the
   production code — do not change the reference file.

## Known limitations

- The fake backend implements `generic.Writer` + the optional
  `generic.ParamsetWriter` extension. Setters that require a
  more complex backend surface (e.g. direct CCU JSON-RPC calls
  outside the Writer interface) are not covered here.

- The Climate fixture covers the IP kind only. RF and SimpleRF
  thermostats have a different wire shape for `SetMode` /
  `SetProfile` and can be added as separate snapshot entries if
  needed.

- Setter invocations that are suppressed by a state-change gate
  (e.g. `Switch.TurnOn` when the switch is already on) produce an
  empty `calls` array in the snapshot. This is intentional: the
  snapshot documents the suppression.
