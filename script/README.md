# script/

Operational helper scripts. Every entry here is **out-of-tree** for
the Go toolchain — they are invoked by operators or CI, not by the
daemon.

## CCU metadata extraction

The Config UI depends on two gzipped JSON archives extracted from a
local OpenCCU (OCCU) checkout:

| Artefact                     | Produced by (aiohomematic)                   | Consumed by                                           |
|------------------------------|---------------------------------------------|-------------------------------------------------------|
| `translation_extract.json.gz`| `script/extract_ccu_translations.py`        | `internal/ccudata.LoadTranslations`                  |
| `easymode_extract.json.gz`   | `script/extract_ccu_easymodes.py`           | `internal/ccudata.LoadEasymode`                      |

### Why aiohomematic's scripts, not our own?

The Python scripts in `../aiohomematic/script/extract_ccu_*.py` are
the canonical extractors used by the whole Homematic Python
ecosystem (`aiohomematic-config`, `homematicip_local`,
`homematicip-local-frontend`). Forking them into Go would duplicate
a large TCL/JS parser without benefit. Re-using the JSON output is
both lighter and keeps us bug-compatible with the reference.

### Running the extractor

```sh
# From a local OCCU checkout
OCCU_PATH=/path/to/occu \
OUTPUT_DIR=/path/to/openccu-loom/var/ccu_data \
    python3 ../aiohomematic/script/extract_ccu_translations.py

OCCU_PATH=/path/to/occu \
OUTPUT_DIR=/path/to/openccu-loom/var/ccu_data \
    python3 ../aiohomematic/script/extract_ccu_easymodes.py
```

Point the daemon at the resulting files via the YAML config:

```yaml
ccu_data:
  translations_path: ./var/ccu_data/translation_extract.json.gz
  easymode_path:     ./var/ccu_data/easymode_extract.json.gz
```

### Why the output files are not committed

The archives contain strings derived from OCCU/OpenCCU
firmware. We respect their redistribution terms by not vendoring
the artefacts into this repository. Operators either run the
extractor themselves or copy the `.json.gz` from an existing
aiohomematic install.

## Other scripts

- `gen_propkinds.go` — pre-computes `payload:"info|config|state"` descriptors per struct so the runtime payload extractor (`internal/payload`) can short-cut its reflection path. Skeleton only; the runtime wiring follows once we know which packages are hot enough to warrant the optimisation.
- `clean-mqtt-discovery.sh` — clears retained Home Assistant Discovery configs that were published by OpenCCU-Loom. Run after any change to the discovery payload schema: HA replays retained configs on connect and rejects every entry whose schema no longer validates, which leaves stale `extra keys not allowed` errors in the HA log until the broker is purged. Requires `mosquitto-clients`. Defaults to a dry run; pass `-y` to actually clear.

  Subscribes under `homeassistant/+/+/+/config` (every device-level discovery config across components) and inspects each payload's `origin.name` field; only records whose origin matches `openccu-loom` are touched, so other integrations on the same broker stay untouched. Pre-`dd96c3b` retained configs (with the old `<base>/<address>_<channel>_<param>` topic shape) are picked up too because the origin marker is schema-stable.

  ```sh
  # Use broker + credentials from an openccu-loom config.yaml.
  ./script/clean-mqtt-discovery.sh -c config.yaml
  ./script/clean-mqtt-discovery.sh -c config.yaml -y

  # Or pass everything explicitly (CLI flags win over -c).
  ./script/clean-mqtt-discovery.sh -h mosquitto.lan -u ha -P "$PASS" -y

  # Clear every retained discovery config on the broker, regardless
  # of origin (use only when the broker is dedicated to OpenCCU-Loom).
  ./script/clean-mqtt-discovery.sh -c config.yaml --all -y
  ```

  After clearing, restart OpenCCU-Loom (or trigger HA-Birth-Sync) so the bridge re-publishes the current schema via `RepublishDiscovery()`.

  ```sh
  go run ./script/gen_propkinds.go ./internal/model/device ./internal/model/hub
  # writes propkinds_gen.go into each package
  ```

  Re-running on unchanged sources is a no-op (deterministic output, file mtime unchanged). Re-run after editing any struct that carries `payload:"..."` tags. Generated files MUST end in `_gen.go` so `golangci-lint` skips them.

## Download statistics

`release_download_snapshot.py` turns two public counters into rows of
newline-delimited JSON: the per-asset download totals of every GitHub release
and the opt-in install counts per version from the Home Assistant analytics
export (`https://analytics.home-assistant.io/addons.json`). Neither source
keeps history, so the `download-stats` workflow runs the script daily and
appends to `downloads.ndjson` on the orphan branch `stats` — the only place
the series exists. `test_release_download_snapshot.py` is its unit test; the
workflow runs it before every sample, and
`tests/contract/download_stats_workflow_test.go` pins the workflow to every
add-on declared under `packaging/ha-addon/`.

```sh
# Read the series without touching the working tree
git fetch origin stats && git show origin/stats:downloads.ndjson \
  | python3 -c 'import json,sys
for l in sys.stdin:
    r=json.loads(l)
    if r["kind"]=="github_release" and r["tag"]=="v0.90.0": print(r["ts"], r["total"])'
```

Two caveats travel with the numbers: GitHub counts requests, not users, and
the maintainer's own installs are in there; the HA export lists only
installations that opted into analytics.

## Issue-form check

`check_issue_template.py` closes an issue that was not filed through the
bug-report form. GitHub enforces the forms' required checklist and fields only
in the browser, so an issue created through the API (`gh issue create`, an MCP
server, an agent) can carry any body. The check reads the required checkbox
labels from `.github/ISSUE_TEMPLATE/*.yml` at run time, matches the body
against the closer of the two forms, and on a violation labels the issue
`invalid-template`, posts one comment in the form's language, and closes it as
not planned. Editing the issue later does not reopen it; the comment asks for a
new issue through the form.

The `validate-issue-template` workflow runs it on every opened or edited issue,
after `test_check_issue_template.py`. Maintainers (`OWNER`/`MEMBER`/
`COLLABORATOR`) and issues labelled `skip-template-check` are exempt. Setting
the repository variable `AUTO_CLOSE_INVALID_TEMPLATE` to `false` keeps
labelling and commenting but stops the close.
