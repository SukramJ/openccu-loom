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
