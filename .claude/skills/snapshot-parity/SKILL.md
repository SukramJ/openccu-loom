---
name: snapshot-parity
description: Run the cross-stack model-snapshot regression — compares OpenCCU-Loom's domain model against the aiohomematic reference on identical wire data. Use after changing DataPoint creation, visibility marks, custom-DP composition, or channel methods.
---

# Cross-stack model-snapshot verification

An end-to-end regression check, not a parity mandate: it catches unintended
model regressions when both stacks load the same wire data. The common-schema
definition is `notes/parity/model_snapshot_schema.md`.

Run the four scripts **in this order**:

```sh
# 1. Wire-data identity (399 devices x 12 attributes per parameter,
#    pydevccu vs godevccu). Must be 0 drift.
python3 script/datasource_diff.py

# 2. Dump OpenCCU-Loom's model against godevccu (~80k DPs, 60+ MB JSON).
go test -tags=integration -timeout=300s \
    -run TestModelSnapshotDumpAgainstGodevccu ./tests/integration/...

# 3. Dump aiohomematic's model against pydevccu (~8k DPs, ~8 MB JSON).
#    The script re-execs in the aiohomematic venv when openccu_data is not on
#    sys.path — without that it silently emits empty parameter labels and
#    masks real drift.
python3 script/aiohomematic_snapshot.py

# 4. Per-field diff (tolerated: `profile`, `wrapped_dps`; paramsets-channel
#    field excluded). Exit 0 means full intersection parity.
python3 script/model_snapshot_diff.py
```

`OPENCCU_LOOM_SNAPSHOT_DEVICES=A,B,C` scopes both sides to a smoke subset for
fast iteration; the default loads the whole embedded fleet. The two snapshot
JSONs under `tests/integration/testdata/` (~70 MB) are gitignored — produced
on demand, never committed.

## Reading the result

The baseline is ~270 architecturally-accepted drifts. Growth beyond that in
your area, without a matching entry in `notes/parity/by_design.md`, is a
regression — not something to re-baseline.
