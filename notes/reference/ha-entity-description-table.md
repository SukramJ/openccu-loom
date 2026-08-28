# The HA entity-description table

Working reference for `internal/north/mqtt/entity_descriptions_table.go` —
the 147 rules the MQTT discovery plane uses to stamp `device_class`,
`state_class`, `entity_category`, `icon`, `translation_key`,
`unit_of_measurement`, `suggested_display_precision`, `enabled_by_default`,
`options` and the per-number `multiplier` onto a discovery payload.

It lives here, not in the Go file, for two reasons. The table is not named
`ha_*`, so `TestDocPurity` scans it in full and its Go comments may not carry
the upstream project names this page needs. And a decision about *scope*
belongs in a document that can be revised, not in a header that a reader
mistakes for machine output.

The decision this table sits under is [ADR
0067](../../docs/adr/0067-north-surface-is-a-model-api.md): the MQTT plane
keeps its Home Assistant entity projection, and the REST/WS surface does not
grow one. This table is that projection's description half.

## The table is maintained here

Until 2026-08-28 the file carried a `DO NOT EDIT` header naming
`script/generate_ha_descriptions.py`, and its stamp read
`2026-05-02T15:22:37Z`.

Three measurements settled what that header was worth:

| Claim | Measurement |
|---|---|
| The generator runs in CI or a make target | `grep generate_ha_descriptions Makefile .github/workflows/` → **0 hits** |
| The file is generator output | Re-running the generator emits `hmipLocalRules` / `HmipLocalLookup` / `HmipLocalDescription`; the committed file has used `haRegistryDescriptionRules` / `HARegistryDescriptionLookup` / `HARegistryDescription` for months. `go build ./internal/north/mqtt/` on the fresh output fails with 8 undefined symbols. |
| The table is reference data, "not yet wired into the discovery builder" (its own header) | `entity_descriptions_apply.go:44` and `:110` call the lookup; `discovery.go:620`, `discovery_aggregate.go:234` and `discovery_press_button.go:96` call those. It has been wired the whole time. |

So the header described a file that had not existed for some time: the
symbols were renamed by hand, the project names were stripped by hand to get
past `TestDocPurity`, and the visible effect was two sentences in the header
breaking off mid-clause where a name had been cut out.

Following ADR 0063 — the device-profile catalogue is maintained here, not
generated — the header came off and the generator was deleted. The table is
ordinary hand-maintained source now, and says so.

## The drift the generator was hiding

Run once before deletion, against `homematicip_local` HEAD in its own venv
(`python3 script/generate_ha_descriptions.py`, 2026-08-28):

- upstream: **159** rules
- committed: **147** rules
- upstream-only: **12**, listed below
- committed-only: **0**

All 12 are deliberately out of scope for this plane. None is a missing
device rule.

| Rule key | Category | Match criterion | Why out of scope |
|---|---|---|---|
| `SECURITY_BATTERY`, `SECURITY_CO`, `SECURITY_GAS`, `SECURITY_INTRUSION`, `SECURITY_PANIC`, `SECURITY_SMOKE`, `SECURITY_TAMPER`, `SECURITY_TECHNICAL`, `SECURITY_WATER` | `hub_binary_sensor` | `VarNameContains: security_*` | The daemon owns the alarm and security domain itself (`internal/alarm`, `internal/north/mqtt/security_entities.go`) and publishes it from its own model. These nine describe the consumer's own hub sensors over CCU system variables — a second, parallel security surface. |
| `DAEMON_CONNECTION` | `hub_binary_sensor` | `VarNameContains: daemon_connection` | Describes a Python client's connection to *its* daemon. An MQTT consumer's connection to this daemon is the broker's LWT, which the bridge already publishes. |
| `DAEMON_LATENCY` | `hub_sensor` | `VarNameContains: daemon_latency` | Same: a client-side measurement. The daemon's own equivalent is `HubMetricsEntry.connection_latency_ms`, served over REST. |
| `event_doorbell` | `event_group` | `Devices: HM-Sen-DB-PCB, HmIP-DBB, HmIP-DSD-PCB` | Already implemented, from a better source. `EventDeviceClassForModel` (`entity_descriptions.go:262`) resolves the doorbell class from `ccudata.DoorbellModels()` — the full CCU model set, not three hard-coded entries — and the aggregate builder stamps it directly at `discovery_aggregate.go:129`. |

## Rules that cannot fire yet

19 of the 147 rules carry a non-empty `VarNameContains`, and every
production call site passes `varName == ""`:

```
discovery.go:620              applyEntityDescription(body, hmipCat, ev.Parameter, ev.Model, ev.descUnit(), "")
discovery_press_button.go:96  applyEntityDescription(body, "button", ev.Parameter, ev.Model, "", "")
discovery_aggregate.go:234    applyEntityDescriptionStrict(body, comp, "", ev.Model, ev.descUnit(), postfix)
```

`varNameMatches(needle, haystack)` returns `strings.Contains(haystack, needle)`
for a non-empty needle, so with an empty haystack none of the 19 can match.
They are not dead code — the lookup is exported and takes the parameter — but
they describe hub entities keyed by system-variable name, and no builder
supplies one today:

`CONNECTIVITY_SENSOR`, `INSTALL_MODE_BIDCOS`, `INSTALL_MODE_BIDCOS_BUTTON`,
`INSTALL_MODE_HMIP`, `INSTALL_MODE_HMIP_BUTTON`, `ALARM_MESSAGES`,
`SERVICE_MESSAGES`, `INBOX`, `CONNECTION_LATENCY`, `LAST_EVENT_AGE`,
`SYSTEM_HEALTH`, `ENERGY_COUNTER`, `ENERGY_COUNTER_FEED_IN`, `RAIN_COUNTER`,
`RAIN_COUNTER_TODAY`, `RAIN_COUNTER_YESTERDAY`, `SUNSHINE_COUNTER`,
`SUNSHINE_COUNTER_TODAY`, `SUNSHINE_COUNTER_YESTERDAY`.

Wiring a varName through the hub-discovery builder would activate all 19 at
once. That is a change with a visible effect on every hub entity's discovery
payload, so it wants its own slice and its own round-trip test — not a
side-effect of a rule edit.

## The guard

`TestHARegistryDescriptionRulesCountUnchanged` used to pin `len(rules) == 147`.
A count catches a truncated slice and nothing else: a rule whose
`device_class` silently changes keeps the count exact.

It is replaced by `TestHARegistryDescriptionRulesMatchTheGolden`, which
compares every field of every rule against
`tests/contract/testdata/ha_registry_description_rules.json`. The golden file
is the named source the coherence test needs now that no generator supplies
one. Refresh it deliberately, in the same commit as the rule change:

```sh
GOMAXPROCS=2 go test -p 2 -run TestHARegistryDescriptionRulesMatchTheGolden \
  ./tests/contract/ -update-ha-description-rules
```

Reviewing that diff is the point of the guard. A rule edit shows up as a
field-level diff in the golden file, which is what a reviewer can actually
check against the device.
