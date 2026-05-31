#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 openccu-loom authors.
#
# Clears retained MQTT topics published by openccu-loom in two
# scopes:
#
#   1. Home Assistant MQTT Discovery configs under
#      `<ha_prefix>/<component>/<node_id>/<object_id>/config`. The
#      `origin.name` payload field is used to scope the delete to
#      openccu-loom only — other integrations on the same broker stay
#      untouched.
#
#   2. openccu-loom state / availability / config / aggregate topics
#      under `<topic_base>/#` (default `openccu-loom/#`). No origin
#      filter is needed here: the topic prefix already scopes the
#      delete. Avoids stale-state mix after a schema change or after
#      a long daemon outage where retained values went obsolete.
#
# Useful after a discovery-payload or state-payload schema change:
# the broker keeps the old retained messages, HA replays them on
# connect and rejects every entry whose schema no longer validates;
# downstream subscribers see leftover values that nothing produces
# any more.
#
# Strategy: subscribe to every retained topic under each scope, then
# publish an empty retained payload to each matching topic — the
# broker drops a topic whose retained payload is empty.
#
# Requires: mosquitto-clients (`mosquitto_sub`, `mosquitto_pub`).
#
# Usage:
#   ./script/clean-mqtt-discovery.sh [options]
#
# Options:
#   -c PATH           load broker creds from a openccu-loom config.yaml
#                     (north.mqtt section: broker_url, username,
#                     password, topic_base). CLI flags win on
#                     conflict.
#   -h HOST           broker host           (default: localhost)
#   -p PORT           broker port           (default: 1883)
#   -u USER           broker username       (default: unset)
#   -P PASS           broker password       (default: unset)
#   -d PREFIX         HA discovery prefix   (default: homeassistant)
#   -b BASE           openccu-loom topic base (default: openccu-loom)
#   -t TOPIC_PATTERN  override the HA-Discovery subscribe pattern.
#                     Default `<prefix>/+/+/+/config`.
#   -o ORIGIN_NAME    payload `origin.name` to match (default:
#                     openccu-loom). Pass empty with `--all` to skip
#                     payload filtering.
#       --all         skip payload origin filter on the HA-Discovery
#                     phase; clears every retained config that
#                     matches the topic pattern. Use with care.
#       --ha-only     only clear HA-Discovery configs; skip every
#                     other phase.
#       --state-only  only clear openccu-loom state topics; skip every
#                     other phase.
#       --legacy-only only clear the retired-topology topics (legacy
#                     channel-aggregate `<addr>/<ch>/state`,
#                     bucket-less `<addr>/<ch>/<PARAM>`, and the
#                     `<addr>/channels/...` subtree) without
#                     touching the new canonical topics or HA
#                     Discovery configs.
#   -w SECS           sub collection window (default: 3)
#   -y                execute deletes (default is dry-run)
#       --help        show this help

set -euo pipefail

BROKER_HOST="localhost"
BROKER_PORT="1883"
BROKER_USER=""
BROKER_PASS=""
HA_PREFIX="homeassistant"
TOPIC_BASE="openccu-loom"
TOPIC_PATTERN=""
ORIGIN_NAME="openccu-loom"
ALL_PAYLOADS="0"
HA_ONLY="0"
STATE_ONLY="0"
LEGACY_ONLY="0"
COLLECT_SECS="3"
EXECUTE="0"
CONFIG_FILE=""

# Track which knobs the user set explicitly on the CLI so a later
# `-c` load only fills in the gaps.
HOST_SET="0"; PORT_SET="0"; USER_SET="0"; PASS_SET="0"; BASE_SET="0"

usage() {
  cat <<'EOF'
Clears retained MQTT topics published by openccu-loom — both HA
Discovery configs and the state-topic tree.

Usage:
  clean-mqtt-discovery.sh [options]

Options:
  -c PATH           load broker creds + topic_base from openccu-loom
                    config.yaml (north.mqtt section). CLI flags win.
  -h HOST           broker host           (default: localhost)
  -p PORT           broker port           (default: 1883)
  -u USER           broker username       (default: unset)
  -P PASS           broker password       (default: unset)
  -d PREFIX         HA discovery prefix   (default: homeassistant)
  -b BASE           openccu-loom topic base (default: openccu-loom)
  -t TOPIC_PATTERN  override HA-Discovery subscribe pattern; default
                    <prefix>/+/+/+/config
  -o ORIGIN_NAME    match payload origin.name (default: openccu-loom)
      --all         skip origin filter on HA-Discovery phase
      --ha-only     only clear HA-Discovery configs
      --state-only  only clear openccu-loom state topics
      --legacy-only only clear retired-topology topics (legacy
                    channel-aggregate `<addr>/<ch>/state`,
                    bucket-less `<addr>/<ch>/<PARAM>`,
                    `<addr>/channels/...` subtree)
  -w SECS           sub collection window (default: 3)
  -y                execute deletes (default is dry-run)
      --help        show this help
EOF
  exit "${1:-0}"
}

# Extracts flat key:value pairs from the `north.mqtt:` block of a
# openccu-loom YAML config. Stays inside that block, strips inline
# comments + surrounding quotes. Keeps things minimal — anchors,
# multi-line scalars or inline-flow YAML in this section would need
# a real parser, but the schema is flat by contract.
parse_mqtt_block() {
  awk '
    function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }
    function unquote(s) {
      if (s ~ /^".*"$/) return substr(s, 2, length(s) - 2)
      if (s ~ /^'\''.*'\''$/) return substr(s, 2, length(s) - 2)
      return s
    }
    /^[^[:space:]#]/                          { in_north = 0; in_mqtt = 0 }
    /^north:[[:space:]]*(#.*)?$/              { in_north = 1; next }
    in_north && /^  [^[:space:]#]/            { in_mqtt = 0 }
    in_north && /^  mqtt:[[:space:]]*(#.*)?$/ { in_mqtt = 1; next }
    in_mqtt && /^    [a-zA-Z_][a-zA-Z0-9_]*:/ {
      idx = index($0, ":")
      key = trim(substr($0, 1, idx - 1))
      val = substr($0, idx + 1)
      sub(/[[:space:]]+#.*$/, "", val)
      val = unquote(trim(val))
      print key "=" val
    }
  ' "$1"
}

# Parses `scheme://host:port` URLs into HOST and PORT globals. Falls
# back to the protocol-default port (1883 / 8883) when the URL omits
# the port. Anything else is rejected loudly so a typo in config.yaml
# doesn't silently target the wrong broker.
parse_broker_url() {
  local url="$1" rest scheme hostport host port
  if [[ "$url" != *"://"* ]]; then
    echo "config: broker_url has no scheme: $url" >&2
    return 1
  fi
  scheme="${url%%://*}"
  rest="${url#*://}"
  hostport="${rest%%/*}"
  if [[ "$hostport" == *":"* ]]; then
    host="${hostport%:*}"
    port="${hostport##*:}"
  else
    host="$hostport"
    case "$scheme" in
      tcp|mqtt|ws)         port="1883" ;;
      tls|mqtts|wss|ssl)   port="8883" ;;
      *)                   port="1883" ;;
    esac
  fi
  CFG_HOST="$host"
  CFG_PORT="$port"
}

load_config() {
  local file="$1"
  if [[ ! -f "$file" ]]; then
    echo "config not found: $file" >&2
    exit 2
  fi

  local broker_url="" cfg_user="" cfg_pass="" cfg_base=""
  while IFS='=' read -r k v; do
    case "$k" in
      broker_url) broker_url="$v" ;;
      username)   cfg_user="$v" ;;
      password)   cfg_pass="$v" ;;
      topic_base) cfg_base="$v" ;;
    esac
  done < <(parse_mqtt_block "$file")

  if [[ -n "$broker_url" ]]; then
    parse_broker_url "$broker_url"
    [[ "$HOST_SET" == "0" ]] && BROKER_HOST="$CFG_HOST"
    [[ "$PORT_SET" == "0" ]] && BROKER_PORT="$CFG_PORT"
  fi
  [[ "$USER_SET" == "0" && -n "$cfg_user" ]] && BROKER_USER="$cfg_user"
  [[ "$PASS_SET" == "0" && -n "$cfg_pass" ]] && BROKER_PASS="$cfg_pass"
  [[ "$BASE_SET" == "0" && -n "$cfg_base" ]] && TOPIC_BASE="$cfg_base"

  echo "config:   loaded ${file}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -c) CONFIG_FILE="$2"; shift 2 ;;
    -h) BROKER_HOST="$2"; HOST_SET="1"; shift 2 ;;
    -p) BROKER_PORT="$2"; PORT_SET="1"; shift 2 ;;
    -u) BROKER_USER="$2"; USER_SET="1"; shift 2 ;;
    -P) BROKER_PASS="$2"; PASS_SET="1"; shift 2 ;;
    -d) HA_PREFIX="$2"; shift 2 ;;
    -b) TOPIC_BASE="$2"; BASE_SET="1"; shift 2 ;;
    -t) TOPIC_PATTERN="$2"; shift 2 ;;
    -o) ORIGIN_NAME="$2"; shift 2 ;;
    --all) ALL_PAYLOADS="1"; shift ;;
    --ha-only) HA_ONLY="1"; shift ;;
    --state-only) STATE_ONLY="1"; shift ;;
    --legacy-only) LEGACY_ONLY="1"; shift ;;
    -w) COLLECT_SECS="$2"; shift 2 ;;
    -y) EXECUTE="1"; shift ;;
    --help) usage 0 ;;
    *) echo "unknown arg: $1" >&2; usage 2 ;;
  esac
done

only_count=0
[[ "$HA_ONLY" == "1" ]] && only_count=$((only_count + 1))
[[ "$STATE_ONLY" == "1" ]] && only_count=$((only_count + 1))
[[ "$LEGACY_ONLY" == "1" ]] && only_count=$((only_count + 1))
if (( only_count > 1 )); then
  echo "--ha-only, --state-only and --legacy-only are mutually exclusive" >&2
  exit 2
fi

if [[ -n "$CONFIG_FILE" ]]; then
  load_config "$CONFIG_FILE"
fi

auth_args=()
if [[ -n "$BROKER_USER" ]]; then
  auth_args+=(-u "$BROKER_USER")
fi
if [[ -n "$BROKER_PASS" ]]; then
  auth_args+=(-P "$BROKER_PASS")
fi

if [[ -z "$TOPIC_PATTERN" ]]; then
  # Default: every device-level HA Discovery config. The shape is
  # `<prefix>/<component>/<node_id>/<object_id>/config` — five levels
  # total. `+` matches a single level so we don't accidentally pick up
  # `<prefix>/status` or other broker-level chatter.
  TOPIC_PATTERN="${HA_PREFIX}/+/+/+/config"
fi

# State-topic pattern under the openccu-loom topic_base. `#` is a
# multi-level wildcard — every retained topic the daemon publishes
# (state, availability, config, aggregate, hub, programs, sysvars,
# install_mode, alarm_messages, …) is captured.
STATE_PATTERN="${TOPIC_BASE}/#"

run_ha="1"
run_state="1"
run_legacy="1"
if [[ "$HA_ONLY" == "1" ]]; then
  run_state="0"
  run_legacy="0"
fi
if [[ "$STATE_ONLY" == "1" ]]; then
  run_ha="0"
  run_legacy="0"
fi
if [[ "$LEGACY_ONLY" == "1" ]]; then
  run_ha="0"
  run_state="0"
fi

echo "broker:        ${BROKER_HOST}:${BROKER_PORT}"
if [[ "$run_ha" == "1" ]]; then
  echo "ha-discovery:  ${TOPIC_PATTERN}"
  if [[ "$ALL_PAYLOADS" == "1" ]]; then
    echo "ha-filter:     none (--all: every matching retained config)"
  else
    echo "ha-filter:     origin.name == \"${ORIGIN_NAME}\""
  fi
fi
if [[ "$run_state" == "1" ]]; then
  echo "state-topics:  ${STATE_PATTERN}"
  echo "state-filter:  none (prefix-scoped — every retained openccu-loom topic)"
fi
if [[ "$run_legacy" == "1" ]]; then
  echo "legacy-shapes: ${STATE_PATTERN} (filtered to retired topology)"
  echo "  - <addr>/<ch>/state                  (channel-aggregate)"
  echo "  - <addr>/<ch>/<PARAM>                (bucket-less DataPointState)"
  echo "  - <addr>/channels/<ch>/...           (channels-infix subtree)"
fi
echo "window:        ${COLLECT_SECS}s"
if [[ "$EXECUTE" == "1" ]]; then
  echo "mode:          EXECUTE (will clear retained topics)"
else
  echo "mode:          DRY-RUN (no changes; pass -y to execute)"
fi
echo

for bin in mosquitto_sub mosquitto_pub; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "missing required tool: $bin (install mosquitto-clients)" >&2
    exit 2
  fi
done

# collect_retained subscribes to the given topic pattern with
# `--retained-only`, dumps every record as `<topic>\t<payload>` lines
# into the named output file, and returns. Treats the `-W` timeout
# (rc=27) as a normal exit — that's how we end the subscription
# after a quiet window.
collect_retained() {
  local pattern="$1" outfile="$2" rc
  set +e
  mosquitto_sub \
    -h "$BROKER_HOST" -p "$BROKER_PORT" \
    "${auth_args[@]}" \
    -t "$pattern" \
    -F '%t\t%p' \
    --retained-only \
    -W "$COLLECT_SECS" \
    >"$outfile" 2>/dev/null
  rc=$?
  set -e
  if [[ $rc -ne 0 && $rc -ne 27 ]]; then
    echo "mosquitto_sub failed for ${pattern} (rc=$rc) — check broker reachability/credentials" >&2
    return 1
  fi
  return 0
}

# delete_topics_from prints the topic list (dry-run preview) or
# publishes empty retained payloads for each topic in the file.
# `-r -n` on mosquitto_pub publishes an empty retained payload,
# which the broker interprets as "delete the retained message for
# this topic".
delete_topics_from() {
  local list="$1" label="$2" cleared=0
  if [[ "$EXECUTE" != "1" ]]; then
    local n
    n="$(wc -l <"$list" | tr -d ' ')"
    head -n 10 "$list" | sed 's/^/  /'
    if (( n > 10 )); then
      echo "  ... ($((n - 10)) more)"
    fi
    echo
    echo "[${label}] re-run with -y to clear ${n} topic(s)."
    return 0
  fi
  while IFS= read -r topic; do
    [[ -z "$topic" ]] && continue
    mosquitto_pub \
      -h "$BROKER_HOST" -p "$BROKER_PORT" \
      "${auth_args[@]}" \
      -t "$topic" \
      -r -n
    cleared=$((cleared + 1))
  done <"$list"
  echo "[${label}] cleared ${cleared} retained topic(s)."
}

# Phase 1 — HA Discovery configs (origin.name-filtered).
phase_ha_discovery() {
  echo "=== HA Discovery (${TOPIC_PATTERN}) ==="
  local records topics total matched
  records="$(mktemp)"
  topics="$(mktemp)"
  trap 'rm -f "$records" "$topics"' RETURN

  if ! collect_retained "$TOPIC_PATTERN" "$records"; then
    return 1
  fi

  total="$(wc -l <"$records" | tr -d ' ')"
  if [[ "$total" == "0" ]]; then
    echo "no retained configs found under ${TOPIC_PATTERN}"
    return 0
  fi

  if [[ "$ALL_PAYLOADS" == "1" ]]; then
    cut -f1 "$records" >"$topics"
  else
    # Only consider the substring after `"origin"` so a device.name
    # that happens to equal `openccu-loom` doesn't false-positive.
    awk -F'\t' -v want="$ORIGIN_NAME" '
      {
        topic = $1
        payload = $0
        sub(/^[^\t]*\t/, "", payload)
        i = index(payload, "\"origin\"")
        if (i == 0) next
        tail = substr(payload, i)
        pat = "\"name\"[[:space:]]*:[[:space:]]*\"" want "\""
        if (tail ~ pat) print topic
      }
    ' "$records" >"$topics"
  fi

  sort -u -o "$topics" "$topics"
  matched="$(wc -l <"$topics" | tr -d ' ')"
  echo "scanned ${total}; ${matched} matched the filter"

  if [[ "$matched" == "0" ]]; then
    if [[ "$ALL_PAYLOADS" == "0" ]]; then
      echo "tip: re-run with --all to inspect every retained config under ${TOPIC_PATTERN}"
    fi
    return 0
  fi

  delete_topics_from "$topics" "ha-discovery"
}

# Phase 2 — openccu-loom state topics (prefix-scoped, no origin
# filter). Captures every retained topic under <topic_base>/#:
# DataPointState, AggregatedState, DeviceAvailability, DataPointConfig
# (json_attributes_topic), ChannelEvent, Hub topics, Sysvars, Programs,
# AlarmMessages, ServiceMessages, InstallMode, Connectivity,
# bridge/status. The daemon republishes everything on next start.
phase_state_topics() {
  echo "=== openccu-loom state topics (${STATE_PATTERN}) ==="
  local records topics total
  records="$(mktemp)"
  topics="$(mktemp)"
  trap 'rm -f "$records" "$topics"' RETURN

  if ! collect_retained "$STATE_PATTERN" "$records"; then
    return 1
  fi

  total="$(wc -l <"$records" | tr -d ' ')"
  if [[ "$total" == "0" ]]; then
    echo "no retained state topics found under ${STATE_PATTERN}"
    return 0
  fi

  cut -f1 "$records" | sort -u >"$topics"
  echo "found ${total} retained topic(s) (deduped)"

  delete_topics_from "$topics" "state-topics"
}

# is_legacy_shape reports whether a retained topic under the daemon's
# topic_base falls into one of the three retired-topology buckets the
# daemon's RunRetainCleanupOnce evicts on boot. Mirrors the matchers
# in `internal/north/mqtt/retain_cleanup.go`:
#
#   - LegacyAggregateStateMatcher: 5-segment `<central>/<iface>/
#     <addr>/<channel>/state`
#   - LegacyDataPointStateMatcher: 5-segment `<central>/<iface>/
#     <addr>/<channel>/<UPPER_PARAM>` (bucket-less per-DP, retired
#     because MASTER and VALUES collided on the same topic)
#   - LegacySlotStateMatcher: anything under `<central>/<iface>/
#     <addr>/channels/<channel>/...` (the verbose 8-segment SlotState
#     plus its config / set / custom-DP companions)
#
# Returns 0 (match) / 1 (no match). $1 is the relative path AFTER the
# topic_base prefix has been stripped.
is_legacy_shape() {
  local tail="$1"
  local parts
  IFS='/' read -ra parts <<<"$tail"
  local n="${#parts[@]}"

  # Need at least <central>/<iface>/<addr>/<segment4>/...
  (( n < 4 )) && return 1

  # `<addr>/channels/<ch>/...` subtree (any depth ≥ 5).
  if [[ "${parts[3]}" == "channels" ]]; then
    (( n < 5 )) && return 1
    [[ "${parts[4]}" =~ ^[0-9]+$ ]] || return 1
    return 0
  fi

  # The next two shapes both have exactly 5 segments.
  (( n != 5 )) && return 1
  [[ "${parts[3]}" =~ ^[0-9]+$ ]] || return 1

  # Channel-aggregate `<central>/<iface>/<addr>/<ch>/state`.
  if [[ "${parts[4]}" == "state" ]]; then
    return 0
  fi

  # Bucket-less DataPointState `<central>/<iface>/<addr>/<ch>/<PARAM>`.
  # Reserved sub-tree nodes that aren't legacy:
  case "${parts[4]}" in
    state|event|set|config|availability|info|diagnostics|update|week_profile|svc|values|master|calculated|custom)
      return 1
      ;;
  esac
  # Legacy wire-parameter names are upper-case by convention; the
  # new shape uses lower-case bucket labels at this depth.
  if [[ "${parts[4]}" =~ ^[a-z] ]]; then
    return 1
  fi
  return 0
}

# Phase 3 — retired-topology cleanup. Mirrors the daemon-side
# RunRetainCleanupOnce so operators can run the same eviction
# manually (e.g. when the daemon's boot-time cleanup window was too
# short on a busy broker).
phase_legacy_topology() {
  echo "=== Legacy topology (${STATE_PATTERN}) ==="
  local records topics total matched base_prefix
  records="$(mktemp)"
  topics="$(mktemp)"
  trap 'rm -f "$records" "$topics"' RETURN

  if ! collect_retained "$STATE_PATTERN" "$records"; then
    return 1
  fi

  total="$(wc -l <"$records" | tr -d ' ')"
  if [[ "$total" == "0" ]]; then
    echo "no retained topics found under ${STATE_PATTERN}"
    return 0
  fi

  base_prefix="${TOPIC_BASE}/"
  while IFS=$'\t' read -r topic _; do
    [[ -z "$topic" ]] && continue
    [[ "$topic" != "$base_prefix"* ]] && continue
    local tail="${topic#"$base_prefix"}"
    if is_legacy_shape "$tail"; then
      echo "$topic"
    fi
  done <"$records" | sort -u >"$topics"

  matched="$(wc -l <"$topics" | tr -d ' ')"
  echo "scanned ${total}; ${matched} matched the legacy shape filter"

  if [[ "$matched" == "0" ]]; then
    return 0
  fi

  delete_topics_from "$topics" "legacy-topology"
}

if [[ "$run_ha" == "1" ]]; then
  phase_ha_discovery
  echo
fi
if [[ "$run_state" == "1" ]]; then
  phase_state_topics
  echo
fi
if [[ "$run_legacy" == "1" ]]; then
  phase_legacy_topology
  echo
fi

if [[ "$EXECUTE" == "1" ]]; then
  echo "tip: restart openccu-loom (or trigger HA-Birth-Sync) so it republishes."
fi
