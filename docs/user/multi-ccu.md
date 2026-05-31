# Multi-CCU Operations Handbook

OpenCCU-Loom is multi-CCU-capable: a single daemon process can serve
any number of Homematic CCUs concurrently. This handbook covers the
operational workflow — adding a second CCU to a running daemon,
MQTT topic scoping, callback routing, and per-`central_name`
diagnostics.

Design rationale and non-operational aspects are in
[ADR 0002](../adr/0002-multi-ccu-first-class.md). The mechanics
described here all ship in v1.0 and are pinned by contract tests.

---

## Concepts

- **`central_name`** — Unique identifier for one CCU within the daemon
  process. Attached to every event, every metric, every MQTT topic
  and every REST path. Free-form, but stable: changing it breaks
  continuity in the topic schema, backups, and persistence.
- **CentralRegistry** — In-process map `central_name → CentralUnit`.
  Holds every active CCU; every north-bound adapter (MQTT, REST, WS,
  UI, Matter) iterates or looks up by `central_name`.
- **Single-Central Convenience** — When the daemon runs with exactly
  one CCU, many paths are addressable without a `central_name` (e.g.
  `/api/v1/devices/...`); the UI collapses the central selector.

---

## 1. Adding a second CCU to a running daemon

### 1.1 Update the configuration

`centrals:` in the config YAML is a list. Example: existing `ccu-haus`,
adding `ccu-garage`.

```yaml
centrals:
  - name: ccu-haus           # existing
    host: 192.168.1.10
    interfaces: [HmIP-RF, BidCos-RF]

  - name: ccu-garage         # new
    host: 192.168.1.11
    interfaces: [HmIP-RF]
    # Optional: per-central reliability override
    # reliability:
    #   command_throttle_inter_command_delay: 50ms
```

Required fields per central: `name` (unique), `host`, `interfaces`.
Every other field falls back to the daemon default or is optional.

### 1.2 Reload strategy

OpenCCU-Loom has no hot-reload for `centrals:`. After editing the
config:

```sh
systemctl restart openccu-loom     # systemd deployment
docker compose restart openccu-loom # compose deployment
```

During the restart:

- Existing MQTT discovery configs under the topic base remain
  retained — Home Assistant does not lose any devices.
- Existing sessions (edit locks, OIDC sessions) stay persisted in
  SQLite.
- The XML-RPC callback subscription on the CCU survives for ~2 min;
  events arriving in this window buffer and replay on reconnect.

### 1.3 Verification

After the restart both CCUs should appear in the health aggregation:

```sh
curl -s http://localhost:8080/api/v1/health | jq '.components[] | select(.name | contains("central"))'
```

Expected output (status `healthy`):

```json
{ "name": "central:ccu-haus",   "status": "healthy", ... }
{ "name": "central:ccu-garage", "status": "healthy", ... }
```

In the config UI the central selector at the top-left now lists both
entries.

---

## 2. MQTT topic scoping

Every MQTT topic carries `central_name` as the second path segment
under the configured `topic_base`:

```
<topic_base>/<central_name>/<interface>/<device>/<channel>/<parameter>
```

Examples:

```
openccu-loom/ccu-haus/HmIP-RF/000A0000000001/4/STATE
openccu-loom/ccu-garage/HmIP-RF/000A0000000099/1/LEVEL
```

### 2.1 Home Assistant Discovery

HA discovery object IDs include `central_name` as well:

```
homeassistant/binary_sensor/openccu-loom_ccu-haus_000A0000000001_4_STATE/config
homeassistant/sensor/openccu-loom_ccu-garage_000A0000000099_1_LEVEL/config
```

That eliminates topic collisions when both CCUs host devices with the
same address (which happens because HmIP-RF addresses are unique per
CCU but not across CCUs).

### 2.2 Subscriptions

The MQTT bridge uses a **single** broker connection across all CCUs.
Adding a second CCU does not start a new MQTT client; publishes are
disambiguated by the topic path.

Subscriptions on set topics (`<topic_base>/<central>/.../set`) are
registered per central. A set action targeting the wrong central
path is rejected as an unknown address — there is no cross-central
routing.

### 2.3 Migrating between CCUs

When a device moves from one CCU to another (re-paired), it surfaces
under the new `central_name`. The old topic + discovery config
remain retained — operational cleanup pattern:

```sh
# Example: device 000A0000000001 moved from ccu-haus → ccu-garage.
mosquitto_pub -h <broker> -t 'openccu-loom/ccu-haus/HmIP-RF/000A0000000001/#' \
              -r -n -l < /dev/null

mosquitto_pub -h <broker> \
  -t 'homeassistant/binary_sensor/openccu-loom_ccu-haus_000A0000000001_4_STATE/config' \
  -r -n -l < /dev/null
```

An automatic retain-cleanup migrator at first boot is part of the
ADR-0011 wave; until that lands the manual pattern above is the
recommended workflow.

---

## 3. Callback routing

Both callback listeners (XML-RPC and BIN-RPC) are **shared** across
all CCUs in the daemon.

### 3.1 XML-RPC (HmIP-RF, BidCos-*, VirtualDevices)

One HTTP listener on `callback.port` (default `:8120`). The daemon
registers one URL path per central during `init()`:

```
http://<daemon-host>:8120/RPC2/<central_name>
```

Inbound XML-RPC calls dispatch to the correct CentralUnit by path.
Inspection via daemon log:

```sh
journalctl -u openccu-loom -f | grep "callback.xmlrpc"
```

Expected log fields: `central=<name>`, `interface=<HmIP-RF|...>`,
`method=event|newDevices|...`.

### 3.2 BIN-RPC (CUxD)

One TCP listener on `callback.bin_port` (default `:8129`). CUxD
addresses inbound datagrams via the `interface_id` in the envelope;
the BIN-RPC router resolves `interface_id → central_name` through
the registry. Each central owns a separate interface-id namespace.

### 3.3 Dynamic ports

When `callback.port: 0` or `callback.port_range: "30000-30099"` is
set, the OS or the range allocator hands out fresh ports on every
daemon restart. The **effective** port is announced to the CCU at
`init()` time — not the configured `0`. Multiple CCUs share the
same listener; each CCU learns the new port at its first reconnect.

---

## 4. Diagnostics per `central_name`

### 4.1 Health endpoint

```sh
curl -s http://localhost:8080/api/v1/health | jq .
```

Each central exposes a `central:<name>` component with sub-components
`central:<name>:hub`, `central:<name>:<interface>`, etc. The aggregate
daemon status flips to `unhealthy` as soon as a single central is
unhealthy — HA discovery frontends and CI healthchecks therefore
detect a bad CCU immediately.

### 4.2 Structured logs

Every log record carries `central=<name>` as a structured field.
Filter via `jq`:

```sh
journalctl -u openccu-loom -o json | \
  jq 'select(.central == "ccu-garage")'
```

With `logging.format: text` (dev mode) `central=...` shows up as an
inline key=value at the end of the line.

### 4.3 Prometheus metrics

CCU-scoped metrics carry the `central` label:

```
openccu_loom_client_state{central="ccu-haus",   interface="HmIP-RF", state="CONNECTED"} 1
openccu_loom_client_state{central="ccu-garage", interface="HmIP-RF", state="CONNECTED"} 1
openccu_loom_command_count_total{central="ccu-haus",interface="HmIP-RF",method="setValue"} 4173
```

Adapter-scoped metrics (MQTT publish rate, REST request duration) do
**not** carry the label — they measure the north-bound side, which is
process-global.

PromQL example: per-central round-trip-time:

```
histogram_quantile(0.95,
  sum by (central, le) (rate(openccu_loom_command_duration_seconds_bucket[5m])))
```

### 4.4 REST inspection

Per-central device list:

```sh
curl -s http://localhost:8080/api/v1/centrals/ccu-haus/devices | jq '.devices | length'
```

Per-central audit log:

```sh
curl -s 'http://localhost:8080/api/v1/audit?central=ccu-haus&since=1h' | jq .
```

In single-central mode the unscoped path `/api/v1/devices` is enough —
the router redirects automatically.

### 4.5 WebSocket events

WebSocket subscription `wss://<host>:8080/api/v1/ws` carries events
from every central. Filter client-side on `event.central_name`:

```js
ws.onmessage = (e) => {
  const ev = JSON.parse(e.data);
  if (ev.central_name !== "ccu-garage") return;
  // ...
};
```

---

## 5. Backup + state layout

State per daemon lives under `data_dir`:

```
<data_dir>/
├── openccu-loom.db          # shared: users, tokens, sessions, sysvar cache
└── centrals/
    ├── ccu-haus/
    │   ├── devices.json
    │   ├── paramsets/
    │   └── incidents/
    └── ccu-garage/
        ├── devices.json
        ├── paramsets/
        └── incidents/
```

Backups are per-central:

```sh
curl -s -u admin:secret http://localhost:8080/api/v1/centrals/ccu-haus/backups
```

Backup restore is also per-central — restoring a single CCU does not
affect any other.

---

## 6. Common failure modes

| Symptom | Cause | Remedy |
|---------|-------|--------|
| Both CCUs deliver events, one suddenly goes silent | XML-RPC callback path mismatch — typically when `central_name` was changed in config without a fresh `init()` round-trip | Daemon restart or `POST /api/v1/centrals/<name>/reconnect` |
| HA Discovery shows duplicated devices | Migration between CCUs without retain cleanup | Manual MQTT retain wipe (see §2.3) |
| `openccu_loom_client_state` metric missing for a central | CCU never connected (host / firewall / credentials) | Check `/health` component; filter daemon log on `central=<name>` |
| Audit log mixes CCUs | Expected behaviour — the audit log is daemon-global with `central_name` as a column | Use `?central=...` query or the UI filter |
| OIDC login fails on one central | OIDC provider is daemon-global, not per-central — login fails for *every* CCU the same way | Inspect the provider config under `auth.oidc:` |

---

## 7. Related documents

- [ADR 0002 — Multi-CCU as a first-class feature](../adr/0002-multi-ccu-first-class.md)
- [ADR 0011 — MQTT topic + payload architecture](../adr/0011-mqtt-topic-and-payload-architecture.md)
- [`docs/mqtt-topic-schema.md`](../mqtt-topic-schema.md)
- [`docs/user-guide.md`](../user-guide.md) — daemon-global configuration
- [`config.example.yaml`](../../config.example.yaml) — annotated config reference
