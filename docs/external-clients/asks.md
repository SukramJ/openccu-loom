# External-Client Integration — Backlog & Vertragslücken

**Status:** Substantiell umgesetzt (5 Wellen + 3 ADRs). Wellen **J** (`unique_id`-Ownership) und **K** (CCU-Domänen-Ableitung, „dümmerer Client") **vollständig umgesetzt** (In-Tree **0.10.0**). Die J1-Nacharbeit ist erledigt: `unique_id` ist jetzt **garantiert nicht-leer** (Serial-Readiness-Gate, `hub_wiring.go` `resolveCCUSerial` — ein Central liefert keine Entity aus, bevor sein CCU-Serial aufgelöst ist) und im Schema `required` (REST + WS) — der Client kann `canonical.py` streichen. J2 hat jetzt einen echten Go↔Python-Parity-Check (`script/routing_key_parity.py`, `make routing-key-parity`) zusätzlich zum Go-Golden-Test. Bewusste Non-Goals: K1-Feld-Parameter-Komposition (`docs/parity/by_design.md`). Offen: nur noch F2 (bewusst).
**Letzte Aktualisierung:** 2026-06-22

## Closure Index

Jeder Ask trägt im Body sein eigenes Detail; die folgende Tabelle ist
die schnelle Übersicht, was wann landete:

| Ask | Status | Referenz |
|---|---|---|
| A1 — WS-Broadcast-Events in wsapi.json | Umgesetzt | ADR-0020 |
| A2 — Owner-Entscheidung Push-Payload-Schemas | Umgesetzt | ADR-0020 |
| A3 — Topic-Konvention verankert | Umgesetzt | docs/external-clients/topic-hierarchy.md |
| A4 — Problem-Type-URIs enumeriert | Umgesetzt | ADR-0020 |
| B1 — WS-Resume-Cursor (seq/since/replay) | Umgesetzt | ADR-0022 |
| B2 — Envelope-`kind` Diskriminator | Umgesetzt | ADR-0022 |
| B3 — Capability-Handshake auf /info | Umgesetzt | ADR-0020 |
| C1 — JSON-Schema-Export pkg/hmenum + hmtypes | Umgesetzt | `make export-schemas` |
| C2 — WS-Envelope-Schema offiziell | Umgesetzt | ADR-0020 |
| C3 — `openccu-loom-types` PyPI-Sister-Repo | Umgesetzt (Scaffolding + enums.py) | `../openccu-loom-types-py` |
| C4 — Drei-Wege-Diff Skript | Skeleton — voller Diff wartet auf py-openccu-loom-client | `script/openccu_loom_client_snapshot.py` |
| D1 — Bulk-Value-Read REST | Umgesetzt | `POST /devices/values:batch` |
| D2 — paramset.put_atomic | Umgesetzt (Doku der CCU-Atomizität auf bestehendem `paramset.put`) | wsapi.json |
| E1 — mDNS-Auto-Discovery | Umgesetzt | ADR-0021 |
| E2 — POST /auth/tokens | Umgesetzt | OpenAPI |
| E3 — Auth-Scope pro Endpoint | Umgesetzt | ADR-0020 |
| F1 — GET /system/ccu | Umgesetzt | OpenAPI |
| F2 — Cold-Start nach Cutover | Bewusst nicht umgesetzt (Greenfield-Policy) | siehe Body |
| G1 — Sysvar/Program-Broadcasts | Umgesetzt | ADR-0020 |
| G2 — Optional-Settings in /config | Umgesetzt (Policies-Block) | OpenAPI |
| H1 — Streaming /snapshot (NDJSON) | Umgesetzt | OpenAPI |
| H2 — Strukturiertes /diagnostics | Umgesetzt (Doku des bestehenden JSON-Surface, Prometheus-Trennung) | OpenAPI |
| Sektion I — Rate-Limiting / Timezone / Heartbeat / Token-Rotation / Multi-Central / Idempotency | Umgesetzt (alle dokumentiert/verdrahtet) | siehe Body |
| J1 — `unique_id` auf REST-Summaries + Snapshot (garantiert auf WS) | Umgesetzt — Feld auf allen Summaries + Snapshot + WS, `omitempty` entfernt + `required` (REST + WS); **garantiert nicht-leer** über das Serial-Readiness-Gate (`hub_wiring.go` `resolveCCUSerial`: ein Central liefert keine Entity aus, bevor sein CCU-Serial aufgelöst ist). Client kann `canonical.py` streichen. | Sektion J |
| J2 — Daemon als alleinige `unique_id`-Quelle + Drift-Guard | Umgesetzt — Go-Golden-Test (`routing_key_contract_test.go`) **plus** automatischer Go↔Python-Parity-Check (`script/routing_key_parity.py`, `make routing-key-parity`), der die Fixtures gegen aiohomematics aktuellen `generate_unique_id`-Output prüft (schließt die manuelle-Sync-Lücke) | Sektion J |
| J3 — Hub-/Schedule-/Calculated-`unique_id`s mitliefern | Umgesetzt (calculated + event_groups; sysvar/program via J1) — week_profile-Aggregat siehe Body | Sektion J |
| J4 — Bootstrap-Rest-N×M: Channel-Metadaten in den Snapshot | Umgesetzt (war bereits vorhanden: nested Snapshot bettet `ChannelSummary` ein) | Sektion J |
| K1 — Geräteprofil-Komposition daemon-seitig (löst `DeviceProfileRegistry` ab) | Umgesetzt (Primärkanal-Marker + `ClimateMode`/`ClimateProfile`-Enum); Feld-Param-Komposition bewusst Non-Goal (`docs/parity/by_design.md`) | Sektion K |
| K2 — Normalisierter Custom-DP-Gerätezustand | Umgesetzt (war bereits vorhanden: typisierte `StatePayload`) | Sektion K |
| K3 — Firmware-Update-Status als abgeleitetes Feld | Umgesetzt (0.10.0) | Sektion K |
| K4 — CCU-Domänen-Konstanten/Enums aus den generierten Typen | Umgesetzt (0.10.0) | Sektion K |

Stand 0.10.0-in-tree (verifiziert gegen den Code):

**Welle K ist umgesetzt** (verifiziert): K1 mit Primärkanal-Marker
`is_custom_dp_primary` (`ChannelSummary`, `devices.go:158`) + `ClimateMode`/
`ClimateProfile`-Enums (`pkg/hmenum/climate.go`, nach `enums.json` exportiert);
K2 typisierter `StatePayload` (`internal/payload/state.go` — benannte Felder,
kein Paramset-Dict); K3 `update_status` (`devices.go:70`); K4 Pseudo-Adressen +
Dispatch-Enums in `enums.json`. Die feinere **Feld-Parameter-Komposition** je
Custom-DP ist ein **bewusstes Non-Goal** (`docs/parity/by_design.md` →
`BD-North-CustomDPCompositionMap`) — client-seitig bestätigt: **keine**
Consumer-Stelle braucht sie (Schreib-Fan-out läuft server-seitig über
`invoke(operation=…)`, Member-DP-Unterdrückung über den daemon-seitigen
`usage`-Verdikt). _Kleiner Rest:_ `ClimateMode` ist nur im `enums.json`-Export,
**nicht** als benanntes OpenAPI-Schema (reitet dort auf `config.hvac_modes` als
freie Strings) — Symmetrie-Lücke, kein Blocker.

**Welle J ist vollständig umgesetzt** — die zuvor offene Nacharbeit ist erledigt:

- **J1 (erledigt):** Das `unique_id`-Feld liegt auf allen Summaries, im nested
  Snapshot und auf den WS-Payloads, ist **nicht mehr `omitempty`** und im Schema
  `required` (REST-Summaries + die beiden Kern-WS-Payloads). Die
  **Nicht-Leer-Garantie** liefert das Serial-Readiness-Gate:
  `WireHub`/`resolveCCUSerial` (`hub_wiring.go`) löst den CCU-Serial — den
  Central-ID-Slot jedes Hub/INT/Virtual-Remote-Keys — mit bounded Retry **vor**
  dem Geräte-Load auf; schlägt das fehl, scheitert die Bring-up und das Gate
  re-wartet, sodass ein Central **nie** Entities mit unaufgelöstem Serial
  ausliefert (gleiche Philosophie wie `ccu_readiness.go`: lieber eine Central
  zurückhalten als identitäts-kaputte Entities publizieren). Damit kann der
  Client `canonical.py` aus dem Kern streichen.
- **J2 (erledigt):** Zusätzlich zum Go-Golden-Test gibt es jetzt einen echten
  Cross-Repo-Parity-Check (`script/routing_key_parity.py`, `make
  routing-key-parity`): er führt aiohomematics aktuellen `generate_unique_id`/
  `generate_channel_unique_id` über dieselben Fixture-Inputs aus und vergleicht
  gegen die `expected`-Werte. Go-Test pinnt Go==Fixtures, der Parity-Check pinnt
  Python==Fixtures ⇒ automatische Go↔Python-Parität (schließt die zuvor manuelle
  Sync-Lücke). Der Test-Kommentar verweist jetzt auf den Parity-Check statt auf
  „copy upstream".
- **J3 (Rest by-design):** calculated DPs + event_groups tragen `unique_id`,
  sysvar/program via J1. Offen-by-design: `WeekProfileResponse` ist ein
  per-Kanal-Aggregat ohne 1:1-Entity, die `schedule_channel_switch`-Keys bleiben
  daher **client-synthetisiert**, bis es eine eigene REST-Entity-Fläche gibt.
- **J4 ist voll umgesetzt** (verifiziert): der nested Snapshot bettet die
  komplette `ChannelSummary` ein (`snapshot.go:53`) — das N+1 ist real weg.

**Netto:** J löst `canonical.py`/`generate_unique_id` (Serial-Garantie steht),
K4 löst den `aiohomematic.const`-Rest. Die größte verbleibende
aiohomematic-Laufzeitkopplung (`DeviceProfileRegistry`) ist client-seitig auf
**eine** kosmetische Stelle reduziert (`naming.py:161`, `ch`/`vch`-Marker) —
ablösbar über den daemon-seitigen `usage`-Verdikt, **keine** Daemon-Nacharbeit
nötig. Alle übrigen 22 Asks aus A–I sind entweder als Runtime-Feature gelandet
oder als Vertrags-Erweiterung verankert; **außer F2 (bewusst) bleibt nichts
mehr als Daemon-Nacharbeit offen.**

## Zweck

Dieses Dokument sammelt die Lücken im publizierten OpenCCU-Loom-Vertrag,
die externe Wire-Clients (Python, TypeScript, Rust, Java) am
verlustfreien Bauen hindern. Jeder Punkt verweist auf eine reale
Datei/Zeile, damit die Asks direkt umgesetzt werden können.

Konkreter erster Use-Case: ein Python-Client (`py-openccu-loom-client`),
der `aiohomematic` in `homematicip_local` ablöst. Die Asks gelten aber
generisch für jede Wire-Sprache; das Dokument ist nicht
Python-spezifisch.

## Scope

**In scope:** alles, was über die north-bound REST- und
WebSocket-Surface des Daemons läuft (`assets/openapi.yaml`,
`assets/wsapi.json`, Push-Events, Auth-Provisionierung,
Discovery).

**Out of scope:**

- **`pkg/hmapi/`** — die Go-Embedding-Facade
  (`HomematicAPI.ReadValue`/`WriteValue`/`SubscribeToUpdates`) richtet
  sich an Go-Prozesse, die OpenCCU-Loom als Library einbinden. Andere
  Konsumenten-Klasse, anderer Lebenszyklus. Wire-Clients sind keine
  Konsumenten von `pkg/hmapi`.
- **MQTT-Bridge** — hat einen eigenen, separat versionierten Vertrag
  in [`docs/mqtt-topic-schema.md`](../mqtt-topic-schema.md) und in
  [ADR-0011](../adr/0011-mqtt-topic-and-payload-architecture.md).
- **`pkg/hmtypes` / `pkg/hmenum` als Go-Import** — Go-Konsumenten
  ziehen die Typen direkt; nur die Repräsentation für andere Sprachen
  ist Gegenstand der Asks (siehe C1).

---

## A. Vertrags-Lücken (höchste Priorität)

### A1. WS-Broadcast-Events fehlen vollständig in `wsapi.json`

**Befund:** `assets/wsapi.json` führt 85 Commands, aber `"kind":
"broadcast"` taucht nur sechsmal auf — und ausschließlich für Matter
(Z. 410-444). Die zentralen Push-Events, von denen jeder Client lebt,
sind nicht im publizierten Vertrag:

| Event (Go-Konstante) | Topic-Schema | im Vertrag? |
|---|---|---|
| `hmevent.EventTypeDataPointValueChanged` | `device.{addr}.channels.{no}.data_points.{param}` | nein |
| `hmevent.EventTypeCustomDataPointStateChanged` | `device.{addr}.cdps.{name}` | nein |
| `hmevent.EventTypeCentralStateChanged` | `central.{name}.state` | nein |
| `hmevent.EventTypeDeviceCreated` / `…Removed` | nicht in `payloads.go` typisiert | nein |
| `hmevent.EventTypeSystemStatusChanged` | nicht dokumentiert | nein |
| `hmevent.EventTypeOptimisticRollback` | nicht dokumentiert | nein |

**Quelle:** `internal/north/rest/ws/payloads.go:66-148`,
`pkg/hmevent/catalogue.go`.

**Ask:** `wsapi.json` um eine vollständige Broadcast-Sektion erweitern
(die existierende `"kind": "broadcast"`-Konvention konsequent auf
*alle* Push-Events anwenden) inklusive Topic-Pattern und
Payload-Schema-Referenz. Solange das fehlt, muss jeder externe Client
den Go-Quellcode lesen, was Drift garantiert.

### A2. Owner-Entscheidung: Push-Payload-Schemas wohin?

**Befund:** `payloads.go` definiert `DataPointValueChangedPayload`,
`CustomDataPointStateChangedPayload`, `CentralStateChangedPayload` als
Go-Strukturen — aber `assets/openapi.yaml` führt sie nicht unter
`components.schemas`. Im `/events`-Pfad (Z. 555-579) steht nur ein
Markdown-Snippet.

Die Wurzel ist eine ungelöste Owner-Frage: aktuell tragen `wsapi.json`
(Commands) und `openapi.yaml` (REST-DTOs) parallele Schemas. Push-Event-Payloads fallen
genau in den Spalt zwischen beiden.

**Ask:** Owner-Entscheidung treffen und konsolidieren:

- **Empfehlung:** `wsapi.json` wird Owner für alle WS-Frames (Commands
  *und* Broadcasts) inklusive Envelope (`{topic, type, ts, payload}`).
  Payload-Strukturen leben in `openapi.yaml` unter
  `components.schemas` und werden aus `wsapi.json` via `$ref` zitiert
  — eine Quelle pro Typ.
- Alternative ("WS-Frames komplett in einem dedizierten
  `wsapi-events.json`") nur dann, wenn das Splitting durch andere
  Tooling-Zwänge erzwungen wird; sonst Doppelpflege.

### A3. Topic-Konvention ist nur im Code dokumentiert

**Befund:** Die Funktionen `DataPointTopic`, `CustomDataPointTopic`,
`CentralStateTopic` in `internal/north/rest/ws/payloads.go:132-148`
definieren das Format implizit. Ein Client, der "alle Werte-Änderungen
von Gerät X" abonnieren will, muss raten, dass `device.0001ABCDE.*`
das richtige Pattern ist. Der einzige Hinweis ist der Inline-Kommentar
"Topic follows the spec convention" — die Spec dokumentiert die
Konvention nirgends.

**Ask:** In `SPECIFICATION.md` oder einem neuen ADR die vollständige
Topic-Hierarchie verankern: Wildcards, Beispiele, Mapping
Event-Typ → Topic. Inhaltlich kann das aus `payloads.go` extrahiert
werden; der dauerhafte Wohnort fehlt.

### A4. Problem-Type-URIs sind nicht enumeriert

**Befund:** OpenAPI nennt RFC 9457 `application/problem+json` und die
zugehörigen `responses/BadRequest`, `Forbidden`, etc. Aber: welche
`type`-URIs existieren? Ein Client braucht eine endliche Liste, um
Fehler typsicher auf Exceptions zu mappen (z. B. `AuthFailure` vs.
`BadGateway` vs. `InterfaceUnreachable`).

**Ask:** `components.schemas.Problem` um ein `enum` aller stabilen
`type`-Werte ergänzen, oder eine separate Tabelle in
`SPECIFICATION.md` (analog zur HTTP-Status-Code-Tabelle).

---

## B. Resilienz-Semantik (für stabile HA-Integration kritisch)

### B1. Keine Resume-/Replay-Semantik nach WS-Reconnect

**Befund:** Die WS-API bietet `subscribe`, `unsubscribe` und `pong`,
aber kein `last_event_id`, keinen `since`-Cursor und keine
`seq`-Nummer. Bei jedem Reconnect (Daemon-Restart, Netzwerk-Glitch,
NAT-Timeout) verliert der Client Events; der einzige Recovery-Pfad ist
`GET /snapshot`, der aber nicht zwischen "unverändert" und "Update
verpasst" unterscheidet.

Das ist nicht nur ein HA-Recovery-Thema, sondern blockiert auch jedes
headless Replay-Szenario (Tests, Datendump, Migrations-Drylauf).

**Ask:** Eine von beiden Optionen umsetzen:

1. **Monotonic Sequence** auf jedem Envelope (`seq: <uint64>`). Client
   sendet bei Reconnect `subscribe` mit `since: <last_seq>`. Daemon
   spielt gepufferte Events ab oder antwortet `replay_lost` → Client
   triggert dann gezielten Resync.
2. **Per-Datapoint Generation-Counter** im REST-Modell, sodass
   `DataPointSummary` ein `generation`-Feld mitführt. Bei Reconnect
   lädt der Client den Snapshot, vergleicht Generationen und übernimmt
   nur die geänderten DPs.

Beide Varianten verhindern den heutigen "Optimistic-Sync mit
Hoffnung". `aiohomematic` löst das mit Periodic-Refresh-Pässen — wenn
der Daemon diese Verantwortung übernimmt, muss er auch die
Sync-Garantien dafür bieten.

### B2. `previous` ist `omitempty` — Frame-Klassifikation fehlt

**Befund:** `DataPointValueChangedPayload.Previous` ist mit
`omitempty` markiert (`payloads.go:41`). Ein Client kann nicht
zuverlässig State-Machines darauf bauen ("hat sich der Wert wirklich
geändert oder ist das ein Initial-Push?").

**Ask:** Primärempfehlung — ein zusätzliches Feld
`kind: "initial" | "change" | "refresh"` auf dem Envelope (oder
Payload). Klassifiziert die Semantik explizit, ohne von der Präsenz
eines optionalen Felds abzuleiten.

Alternative (`Previous` als `*Value` ohne `omitempty`, nil = "noch nie
gesehen"): funktioniert, bricht aber Go's idiomatisches JSON-Mapping
(jeder `nil`-Pointer würde explizit als `"previous": null` serialisiert)
und überträgt die Semantik weiterhin durch Abwesenheit/Anwesenheit
eines Werts — fehleranfälliger als das explizite `kind`-Feld.

### B3. Keine Capability-/Version-Negotiation

**Befund:** Es gibt `GET /info` für die Daemon-Version, aber kein
dediziertes Capability-Handshake. Wenn Loom 0.2 ein Event-Format
ändert, lernt der Python-Client das erst beim ersten kaputten Parse —
mit schwer zu diagnostizierenden Folgefehlern.

**Ask:** `GET /info` um `api_version` (semver, eigene Achse vom
Daemon-Release) und `capabilities: [...]` erweitern. Der Client kann
beim Start die unterstützten Features prüfen und mit klarer
Fehlermeldung abbrechen statt mysteriös zu deserialisieren.

---

## C. Pakete und Tooling für externe Clients

### C1. JSON-Schema-Export der Domain-Typen

**Befund:** `pkg/hmtypes/value.go`, `pkg/hmtypes/datapoint_key.go`,
`pkg/hmenum/*.go` (~20 Go-Dateien) sind die Wahrheit für externes
Modeling — aber nur als Go-Code. Externe Sprachen brauchen ein
maschinenlesbares Artefakt.

**Ask:** Im `Makefile` ein Target `make export-schemas` ergänzen, das
aus `pkg/hmenum/*.go` ein `assets/schemas/enums.json` (Wert→Name-Mapping)
und aus `pkg/hmtypes/*.go` ein `assets/schemas/types.json` (JSON-Schema
Draft-7) generiert. Beide Artefakte als Release-Asset publizieren —
danach kann jeder Code-Generator in jeder Sprache sie verwenden. Heute
muss man Go-AST parsen, um zu wissen, dass `ParamsetKey.VALUES ==
"VALUES"`.

**Breaking-Change-Achse `DataPointCategory` / `DataPointType`:** Diese
beiden Enums liegen auf dem Wire — `DataPointSummary.category`
+ `data_point_type`, optional auf dem WS-Value-Changed-Payload, und
die genesteten Snapshot-Einträge. `homematicip_local` filtert direkt
auf `DataPointCategory`. **Jede Änderung an einem Wert dieser beiden
Enums (Hinzufügen, Entfernen, Umbenennen) ist ein Breaking Wire
Change** und muss mit einem `api_version`-Bump einhergehen. Ein
Drift-Detektor-Contract-Test (`tests/contract/`) hält `enums.json`
und die Go-Konstanten in Lockstep — `assets/schemas/enums.json` ist
generiert (`go run ./script/export_schemas.go`) und darf nicht von
Hand editiert werden.

### C2. WS-Envelope-Schema offiziell publizieren

**Befund:** Das Envelope-Schema `{topic, type, ts, payload}` ist nur
ein Kommentar in `payloads.go:15`.

**Ask:** Folgt direkt aus A2: wenn `wsapi.json` Owner aller WS-Frames
wird, gehört das Envelope-Schema dort ins Wurzeldokument (nicht in
einen Inline-Kommentar). Kein neuer File-Wildwuchs nötig.

### C3. Offizielles Python-Types-Paket aus dem Daemon-Repo

**Befund:** Es gibt `script/aiohomematic_snapshot.py` und andere
Python-Helfer, aber kein offizielles Python-SDK. Ein externer
`py-openccu-loom-client` muss sich selbst aktuell halten — Drift
garantiert.

**Ask:** Aus `assets/openapi.yaml` + `assets/wsapi.json` ein dünnes
Python-Paket `openccu-loom-types` generieren (nur Pydantic-Models,
keine Logik), das die CI bei jedem Loom-Release neu auf PyPI
veröffentlicht. Höherwertige Clients bauen darauf auf, ohne Drift zu
riskieren.

**Hosting:** Als **Sister-Repo unter gleicher Org** (`SukramJ/openccu-loom-types-py`),
nicht als Sub-Directory innerhalb des Daemon-Repos. Das Daemon-Repo
ist Go-only — Python-CI darin würde `make lint` / `make test` /
`goreleaser` kompliziert machen und Multi-Sprach-Toolchain-Drift
einschleppen. Versions-Bumps koppeln via CI-Trigger, nicht via
Monorepo.

### C4. Parity-Test-Skript für den Python-Client bereitstellen

**Migration-blocker für `aiohomematic` → `py-openccu-loom-client`.**
Ohne automatisierten Drei-Wege-Diff hat die HA-Migration kein
CI-Sicherheitsnetz; jede Loom-Release-Welle wäre eine
manuelle Sichtprüfung pro Geräteklasse. Niedrigere Priorität als
A1-A3/B1/C1-C2 in der TL;DR, weil ohne die Vertrags-Asks der
Python-Client gar nicht erst existiert — aber sobald er existiert,
ist C4 das Gate, durch das jeder Release muss.

**Befund:** `script/aiohomematic_snapshot.py` und
`script/model_snapshot_diff.py` existieren bereits — sie vergleichen
Go-Modell mit aiohomematic-Modell.

**Ask:** Ein drittes Snapshot-Skript
`script/openccu_loom_client_snapshot.py` ergänzen, das via
`py-openccu-loom-client` gegen den lokal laufenden Daemon einen
Snapshot zieht. Dann kann `model_snapshot_diff.py` zum Drei-Wege-Diff
(aiohomematic ↔ loom-go ↔ loom-py-client) ausgebaut werden. CI-Gate
für jede HA-relevante Release-Welle.

---

## D. Batch- und Transaktions-Operationen

### D1. Bulk-Value-Read fehlt im REST-Interface

**Befund:** `aiohomematic.api.HomematicAPI.read_value` liest pro Call
einen Wert. Bei 80k DPs wäre Initial-Sync mit REST-Roundtrips
katastrophal. Loom löst das mit `GET /snapshot`, aber Snapshot ist ein
One-Shot, nicht filterbar.

**Ask:** `POST /devices/values:batch` (oder
`GET /data-points?addr=…&param=…&addr=…&param=…`) für Punkt-Lookups
in einem Roundtrip. Im WS-Pendant entspricht das mehrfache
`paramset.get` — bereits OK, aber für REST-only-Clients fehlt es.

### D2. Atomares Multi-Parameter-Set ist undurchsichtig

**Befund:** `homematicip_local` nutzt `CallParameterCollector`
(`control_unit.py:57`, `cover.py:10` in der HA-Component), um z. B.
Helligkeit, Farbe und Übergang einer Lampe in einem CCU-Call zu setzen
(`setValue`-Batch, kein beliebiges Paramset). REST hat dafür
`PUT /devices/{addr}/paramsets/{key}` — funktioniert, aber:

- Es ist nicht klar, ob der Daemon das auch atomar an die CCU
  weitergibt oder serialisiert.
- Eine `priority`-Semantik wie bei `SetValueRequest` fehlt.
- Keine WS-Variante mit Ack pro Parameter.

**Ask:** OpenAPI-Beschreibung von `PUT /devices/{addr}/paramsets/{key}`
um Atomaritäts- und Priority-Garantien ergänzen. Idealerweise
WS-Befehl `paramset.put_atomic` mit Ack/Nack pro Parameter, damit der
Client weiß, welcher Teil-Write fehlgeschlagen ist.

---

## E. Discovery und Auth-UX (für HA-Config-Flow relevant)

### E1. mDNS / Zeroconf-Service-Advertisement

**Status:** Umgesetzt — siehe [ADR 0021](../adr/0021-mdns-self-advertisement.md).
Daemon publiziert `_openccu-loom._tcp.local.` mit Port, `path=/api/v1`,
`api_version=…`, `tls=0` standardmäßig (Opt-out via
`North.Discovery.MDNS.Enabled: false`). HA-`zeroconf:`-Manifest-Eintrag
bleibt SDK-seitige Arbeit.

**Befund (historisch):** `homematicip_local`'s `config_flow.py` fragt
heute Host und Port manuell ab. Wenn der Daemon im LAN per mDNS
advertised, kann HA ihn auto-entdecken (wie es für HomeKit, Sonos etc.
funktioniert).

**Ask (historisch):** Daemon publiziert `_openccu-loom._tcp.local.`
mit Port und TLS-Flag. HA's `zeroconf:`-Manifest-Eintrag ergänzt das im
config_flow.

### E2. API-Token-Provisionierung im UI/CLI dokumentieren

**Befund:** `openapi.yaml` Z. 641-655 hat `GET /auth/tokens` (List),
aber kein sichtbares `POST /auth/tokens` (Create). Wie kommt ein
HA-Setup an seinen Bearer-Token?

**Ask:** Klar dokumentierter Provisioning-Pfad: entweder ein neuer
REST-Endpunkt `POST /auth/tokens` (admin-only), oder eindeutige
CLI-Anleitung (`openccu-loom token create --role operator --name
homeassistant`). Im config_flow von `homematicip_local` brauchen wir
dann nur Host + Token statt Host + User + Passwort.

### E3. Auth-Scope pro Endpoint annotieren

**Befund:** Roles laut `Identity`-Schema (`openapi.yaml:2350`):
`admin | operator | viewer`. Klar definiert — aber **welcher Endpoint
braucht welche Rolle?** Nicht dokumentiert.

**Ask:** Den OpenAPI-Standard-Mechanismus benutzen: pro Operation
einen `security:`-Block mit Scopes, deren Vokabular sich am
`Identity`-Schema orientiert (`viewer`, `operator`, `admin`). Dann ist
das maschinenlesbar für jeden OpenAPI-Codegen und erzeugt keinen
proprietären `x-required-role:`-Extension-Friedhof. Damit kann HA
entscheiden, welcher Token-Scope nötig ist.

---

## F. Konfigurations-Migration aus der aiohomematic-Welt

### F1. CCU-Verbindungs-Config sollte vom Daemon kommen, nicht aus HA

**Befund:** `homematicip_local`'s config_flow erfragt heute
Interface-Liste, Callback-Host, Callback-Port-XML-RPC, JSON-Port —
alles Daemon-Sache nach dem Cutover. Aber: was wenn der Daemon eine
andere CCU verwaltet als HA erwartet?

**Ask:** `GET /interfaces` existiert bereits. Zusätzlich
`GET /system/ccu` mit allem, was HA für Repair-Flows wissen muss
(Serial, Firmware-Version, configured-interfaces, central-id). Damit
kann HA Migration- und Repair-Issues anzeigen, ohne selbst XML-RPC zu
sprechen.

### F2. Cold-Start nach Cutover ist gewollt — Warm-Start liegt HA-seitig

**Befund:** `homematicip_local/__init__.py:23` ruft `cleanup_files`
aus `aiohomematic.store.persistent`. Bestehende Installationen haben
einen vollen aiohomematic-Cache (mehrere MB pro Instanz) auf der
Platte. Naheliegender Wunsch: diesen Cache in den Daemon importieren,
damit der erste Start nach Cutover nicht alle 80k DPs frisch von der
CCU zieht.

**Entscheidung — kein daemon-seitiger Import.** CLAUDE.md formuliert
explizit: *"No backwards-compatibility shims for aiohomematic data /
caches; this is greenfield."* Ein
`script/migrate_aiohomematic_storage.py` würde Format-Drift zwischen
zwei Cache-Schemas zementieren und gegen genau diese Greenfield-Policy
verstoßen.

**Ask (HA-seitig):** Der Cold-Start ist der gewollte Preis. Wo
HA-seitig optimiert werden kann: nach Cutover einmal
`GET /snapshot` ziehen, daraus die HA-Entity-Registry vorbefüllen,
ab dann nur noch WS-Push. Der Daemon füllt seinen eigenen
SQLite-Cache nebenher. Nach 1-2 Minuten ist beide Seiten warm,
ohne dass ein Migrations-Pfad gepflegt werden muss.

**Abhängigkeit:** Diese Strategie steht und fällt mit
[H1](#h1-snapshot-ist-heute-ein-self-dos-bei-80k-dps). Solange
`GET /snapshot` ein nicht-streamender 60-MB-Blob ist, ist der
HA-Warm-Start beim Cutover-Start derselbe Self-DoS, vor dem H1 warnt.
H1 ist Voraussetzung, nicht parallel umsetzbar.

---

## G. Konsistenz mit dem aiohomematic-Vokabular

### G1. Sysvar- und Program-Events auch über WS pushen

**Befund:** `wsapi.json` hat `sysvars.list` / `sysvars.set` /
`programs.list` / `programs.execute` als Commands, aber **keine**
Broadcasts für Sysvar-Value-Changes oder
Program-Execute-Notifications. Die Events existieren bereits intern
(`pkg/hmevent/catalogue.go:57-58`: `EventTypeProgramExecuted`,
`EventTypeSysvarChanged`) — sie sind nur nicht nach außen geführt.

`homematicip_local` mappt Sysvars auf HA-Sensoren — die müssen
pushable sein, sonst muss der Client periodisch pollen und entwertet
damit das ganze WS-Konstrukt.

**Ask:** Broadcast-Events `sysvar.value_changed` (Topic
`hub.sysvars.{name}`) und `program.executed` (Topic
`hub.programs.{id}`) in `wsapi.json` annoncieren und im WS-Hub
publizieren.

### G2. Optional-Settings als typed config in `/config` exponieren

**Befund:** `homematicip_local` reicht `DEFAULT_OPTIONAL_SETTINGS`,
`DEFAULT_PROGRAM_MARKERS`, `DEFAULT_SYSVAR_MARKERS` an `CentralConfig`
(`control_unit.py:1150-1165`). Im Daemon-Modell sind das
Server-Side-Konfig — gut, aber nicht klar in `openapi.yaml`
reflektiert.

**Ask:** `GET /config` (existiert bereits, `openapi.yaml:65-74`,
"Sanitized effective configuration") explizit um diese
aiohomematic-Konzepte erweitern, damit HA prüfen kann: "Läuft der
Daemon mit den Settings, die der Nutzer in HA gewählt hatte?"

---

## H. Operative Verträge

### H1. `/snapshot` ist heute ein Self-DoS bei 80k DPs

**Befund:** `GET /snapshot` liefert die volle Modell-Sicht in einem
JSON-Blob. Bei 80k DPs sind das schnell 60+ MB unkomprimiert. Jeder
Client muss diesen Blob komplett in den Speicher ziehen, parsen und
indexieren — und der Daemon hält ihn vorher komplett im Speicher,
während er ihn serialisiert.

**Ask:** Eine streaming-/paginierungsfähige Variante: entweder
NDJSON-Stream (`Accept: application/x-ndjson`, eine Zeile pro Device
oder DP) oder Cursor-Paginierung (`?cursor=…&limit=…`). Bestehender
JSON-Blob-Endpoint kann als Convenience erhalten bleiben, aber für
HA-Initial-Sync und CI-Snapshot-Skripte ist Streaming Pflicht.

### H2. Diagnostics-Surface für Client-Repair-Flows

**Befund:** HA's Repair-Flows leben von strukturierter
Diagnose-Information ("Welche Interfaces sind unreachable? Wann war
der letzte CCU-Callback? Wie groß ist der Event-Buffer?"). Aktuell
gibt es Prometheus-Metriken (siehe `internal/metrics/`), aber kein
JSON-Endpoint, den HA direkt abfragen und einem Nutzer anzeigen
könnte.

**Ask:** `GET /diagnostics` als strukturierte JSON-Sicht einführen
(Interface-Status, Coordinator-Ticks, Event-Buffer-Tiefe,
Cache-Hit-Raten, letzte CCU-Callbacks pro Interface). Schema in
`openapi.yaml` als versionierter Vertrag, identisches Schema für alle
Clients. Prometheus-Surface bleibt für Scraping-Workflows (Grafana,
Alertmanager) bestehen — die zwei Konsumenten-Klassen sind disjunkt:
Repair-Flows brauchen eine Momentaufnahme, Monitoring braucht
Time-Series. Ein Endpoint pro Klasse.

---

## J. Loom-native Identität — `unique_id`-Ownership (höchste neue Priorität)

**Kontext.** Der Daemon besitzt den Routing-Key-Algorithmus bereits in Go
(`internal/routingkey/uniqueid.go:89 GenerateUniqueID`,
`:107 GenerateChannelUniqueID`, `canonical.go:49 CanonicalUniqueID`) und
emittiert `unique_id` auf den WS-Payloads
(`internal/north/rest/ws/payloads.go:45`). **Trotzdem rechnet der
Python-Client den Key heute ein zweites Mal nach** — in `canonical.py`, das
an `aiohomematic.model.support.generate_unique_id` delegiert. Das ist die
**einzige verbleibende harte aiohomematic-Laufzeitkopplung** im Loom-Pfad von
`homematicip_local`. Welle J schließt sie: liefert der Daemon den Key überall
mit, **konsumiert** der Client ihn nur noch und `aiohomematic` fällt aus dem
gesamten Loom-Pfad (nicht nur dem Compat-Shim, sondern auch dem Client-Kern).

Hintergrund + Architektur: das Konzept
`openccu-loom-client/docs/homematicip-loom-konzept.md` (§6.3, §10) und die
Migrations-Spezifikation
[`ha-unique-id-migration.md`](./ha-unique-id-migration.md).

> **Status Welle J (0.10.0-in-tree, noch nicht getaggt): größtenteils umgesetzt —
> J1-Garantie + J2-Labeling offen (Daemon-Nacharbeit).**
> - **J1** ⚠️ **Feld da, aber nicht garantiert.** `unique_id` (das
>   `CanonicalUniqueID`-Ergebnis) liegt auf `DataPointSummary`, `CustomDPSummary`,
>   `ProgramSummary`, `SysvarSummary`, `CalculatedDPSummary`, `EventGroupSummary`
>   und den nested Snapshot-Datenpunkten; auf den WS-Value-Changed-Payloads wurde
>   `omitempty` entfernt. **Aber:** `toDataPointSummary` setzt es nur
>   `if serialSuffix != ""` (`devices.go:825`), REST-Feld ist `omitempty`, und
>   OpenAPI führt es **nicht** als `required` (`openapi.yaml:4569` „omitted when
>   the central serial is not yet known"; WS „Optional", `:5265`). **Nacharbeit:**
>   Serial vor Auslieferung garantiert auflösen + `unique_id` als
>   `required`/immer-nicht-leer verankern — sonst behält der Client `canonical.py`
>   und aiohomematic fällt **nicht** aus dem Kern. (Serial-Suffix kommt über die um
>   `SerialSuffix(central)` erweiterten `DeviceIndex`/`HubIndex`-Facades.)
> - **J2** ⚠️ **Golden-Test, keine aiohomematic-Parität.**
>   `tests/contract/routing_key_contract_test.go` hält `GenerateUniqueID`/
>   `GenerateChannelUniqueID` gegen ein **byte-fixiertes Golden-Korpus**, das
>   **manuell** aus aiohomematics Output nachgezogen wird (Test-Kommentar:
>   „Re-pin by copying the upstream golden files") — **kein** automatischer
>   Cross-Sprach-Check wie der `enums.json`-Lockstep (C1). Genügt für den
>   Endzustand, deckt die Übergangsphase (Client nutzt beides parallel) aber
>   nicht. **Nacharbeit:** als „Golden, manuell synced" labeln oder echten
>   Cross-Repo-Paritätscheck ergänzen. Neu ergänzt: `event.Group.CanonicalUniqueID`
>   (Event-Gruppen-Keys an einer Stelle, Go).
> - **J3** ✅ calculated DPs + event_groups tragen `unique_id`; sysvar/program
>   via J1. **Offen-by-design:** die `WeekProfileResponse` ist ein
>   per-Kanal-Aggregat (kein Entity-1:1), trägt daher keinen einzelnen
>   `unique_id`; die `schedule_channel_switch`-Entities bräuchten erst eine
>   eigene REST-Entity-Fläche.
> - **J4** ✅ war bereits vorhanden: der nested Snapshot bettet `ChannelSummary`
>   ein (`SnapshotChannelEntry`), trägt also `group_no`/`room`/`functions`/
>   `is_group_master`/`sub_device_name` in **einem** Call — das N+1-Problem
>   existiert nicht mehr.

> _Die folgenden Unterabschnitte J1–J4 sind der **ursprüngliche Ask** (Stand vor
> Umsetzung, im Präsens formuliert). Der aktuelle Stand — inkl. der offenen
> J1-/J2-Nacharbeit — steht im **Status-Blockquote oben**._

### J1. `unique_id` auf die REST-Surfaces (Summaries + Snapshot)

**Befund:** `unique_id` liegt heute **nur** auf den WS-Payloads
(`payloads.go:45`) — und dort als `omitempty`, der Daemon darf ihn also
weglassen. Die REST-Summaries tragen ihn **gar nicht**:

| DTO (Go-Handler) | `unique_id` heute |
|---|---|
| `DataPointSummary` (`internal/north/rest/handlers/devices.go`) | nein |
| `CustomDataPointSummary` (`handlers/custom_data_points.go`) | nein |
| Sysvar-/Program-Summary (`handlers/hub.go`) | nein |
| nested Snapshot `device_channels[].channels[].data_points` (`handlers/snapshot*.go`) | nein |
| WS-Payloads (`ws/payloads.go:45`) | ja, aber `omitempty` |

Konsequenz: Für die **Entity-Anlage** (Bootstrap aus dem Snapshot, nicht aus
einem Push) muss der Client den Key selbst rechnen → `canonical.py` →
`aiohomematic`.

**Ask:** `unique_id` (das `CanonicalUniqueID`-Ergebnis) auf
`DataPointSummary`, `CustomDataPointSummary`, die Sysvar-/Program-Summaries
**und** die genesteten Snapshot-Datenpunkte legen, sowie auf den WS-Payloads
`omitempty` entfernen (immer befüllen). Der Algorithmus existiert bereits
(`routingkey/`); es ist ein zusätzliches Feld, **kein** Verhaltensbruch.
Danach ist `event.payload.unique_id` der Normalfall statt des
Fallback-Rebuilds, und der Client braucht den Algorithmus nicht mehr.

### J2. Daemon als alleinige `unique_id`-Quelle + Drift-Guard

**Befund:** Solange der Client den Key nachrechnet, existieren **zwei**
bit-identische Implementierungen — `internal/routingkey/` (Go) und
`aiohomematic.generate_unique_id` (Python). Driften sie auseinander (z. B.
geänderte Slug-Regel, anderer Serial-Suffix), bricht das **still** die
HA-Entity-Registry: Entities verwaisen, Historie/Automationen gehen verloren.

**Ask:** Einen Contract-Test (`tests/contract/`) etablieren, der
`routingkey.CanonicalUniqueID` gegen ein eingefrorenes Referenz-Korpus
(generic / custom / sysvar / program / hub-singleton / calculated /
week_profile / schedule_channel_switch) bit-identisch hält — analog zum
`enums.json`-Lockstep aus C1. Sobald J1 steht und der Client nur noch
konsumiert, **ist der Daemon-Key der Vertrag**; der Drift-Guard sichert ihn
über Releases. Das Migrations-Doc `ha-unique-id-migration.md` wird damit zur
verbindlichen, getesteten Spezifikation.

### J3. Hub-/Schedule-/Calculated-`unique_id`s mitliefern

**Befund:** Nicht nur die generischen DPs tragen Keys. Der Client
synthetisiert heute auch die Keys für Install-Mode, `week_profile`,
`schedule_channel_switch`, `calculated` und die kombinierte Dauer-Number —
über eine **Prefix-Konvention** (`loom_calculated_…`, `loom_week_profile_…`,
`loom_schedule_channel_switch_…`). Diese Prefix-Logik lebt damit doppelt
(Go + Python).

**Ask:** Auch diese Keys auf den jeweiligen REST-Surfaces mitliefern —
`handlers/calculated_data_points.go`, `handlers/week_profile.go`,
`handlers/event_groups.go`, `handlers/hub.go` (Sysvar/Program + Singletons).
Dann lebt die **gesamte** Key-Konvention an einer Stelle (Go), und J2s
Drift-Guard deckt sie vollständig ab.

### J4. Bootstrap-Rest-N×M: Channel-Metadaten in den Snapshot

**Befund:** Der nested Snapshot (`device_channels`, Welle „nested snapshot")
hat das per-**Channel**-Datenpunkt-Fetch (`M`) bereits eliminiert — der
Client liest die DPs direkt aus `device_channels[].channels[].data_points`.
Übrig bleibt das per-**Device** `GET /devices/{addr}` für die
Channel-**Metadaten** (`group_no`, `room`, `functions`, `is_group_master`,
…). Bei großen CCUs sind das wieder `N` Roundtrips beim Bootstrap.

**Ask:** Die Channel-Metadaten in die genesteten Snapshot-Channel-Einträge
aufnehmen (dieselben Felder, die `GET /devices/{addr}` heute liefert), sodass
der Bootstrap mit **einem** Snapshot-Call auskommt statt `N+1`. Der bestehende
Detail-Endpoint bleibt für gezielte Einzelabfragen/Repair-Flows erhalten.

---

## K. „Dümmerer Client" — CCU-Domänen-Ableitung in den Daemon verlagern

**Prinzip.** Je mehr CCU-Domänen-Wissen der Daemon besitzt und fertig
ausliefert, desto dünner der Client — und desto weniger
Mehrfach-Implementierung über die Wire-Sprachen (Python, TS, Rust, Java)
hinweg. Welle J adressiert die `unique_id`; Welle K adressiert die **restliche
Ableitung**, die der `py-openccu-loom-client` heute noch selbst macht und dafür
`aiohomematic` zur Laufzeit zieht. Nach J+K ist der Loom-Pfad
**vollständig aiohomematic-frei**.

**Wichtige Abgrenzung (gilt für alle K-Asks):** verlagert wird nur
**generische CCU-Domänen-Ableitung** — Wissen, das _jeder_ Wire-Client sonst
neu baut (Geräteprofile, Kanal-Komposition, normalisierter Gerätezustand,
Firmware-Status-Klassifikation). **HA-spezifisches** bleibt im Client:
Kategorie→HA-Plattform-Mapping, HA-Einheiten-Skalierung (Helligkeit 0–255,
HS 0–100), HA-Entity-Namenskonventionen. Der Daemon liefert die
CCU-Wahrheit, der Client die HA-Übersetzung.

**Konkrete Restkopplung (Quelle: `compat/aiohomematic/_upstream.py` — der eine
Seam, über den der Client noch `aiohomematic` zieht):**

> **Status Welle K (0.10.0): bis auf den K1-Rest umgesetzt.**
> - **K1** ✅ umgesetzt: der **Primärkanal-Marker** liegt als
>   `is_custom_dp_primary` auf `ChannelSummary`
>   (`device.Channel.IsCustomDPPrimaryChannel`); die **Kanal-Komposition** eines
>   Custom-DP über `CustomDPSummary.channels`; das **Climate-/Preset-Vokabular**
>   als geschlossenes `ClimateMode`/`ClimateProfile`-Enum
>   (`pkg/hmenum/climate.go`, nach `enums.json` exportiert — `climate.Mode`/
>   `Profile` sind jetzt Aliase darauf, Single-Source) plus per Gerät über
>   `CustomDPSummary.config` (`hvac_modes`/`preset_modes`). **Bewusstes
>   Non-Goal:** die feinere **Feld-→Parameter-Komposition** je Custom-DP —
>   `docs/parity/by_design.md` → `BD-North-CustomDPCompositionMap`
>   (konterkariert die K2-Normalisierung, leakt die interne Profil-Struktur,
>   schaltet keinen Client frei: nach Primärkanal-Marker + Climate-Enum sind
>   alle Registry-*Outputs* gedeckt).
> - **K2** ✅ war bereits vorhanden: `CustomDPSummary.state` ist die typisierte
>   `payload.StatePayload` (`is_locked`, `hvac_mode`, `brightness`,
>   `current_position`, …), kein roher Paramset-Dict. Die HA-Einheiten-Skalierung
>   bleibt — wie in der K-Abgrenzung gewollt — Client-seitig.
> - **K3** ✅ neues abgeleitetes `update_status`-Feld
>   (`up_to_date`|`update_available`|`installing`) auf `DeviceSummary`, gespeist
>   aus `hmenum.DeriveDeviceUpdateStatus` (kollabiert die rohen
>   `DeviceFirmwareState`-Phasen). Das `DeviceUpdateStatus`-Enum wird über
>   `enums.json` mitexportiert.
> - **K4** ✅ die vier Pseudo-Adressen sind jetzt benannte Konstanten in
>   `internal/routingkey` (`HubAddress`/`InstallModeAddress`/`ProgramAddress`/
>   `SysvarAddress`) und werden als `pseudo_addresses`-Block nach
>   `assets/schemas/enums.json` exportiert; `DataPointKey` lag bereits in
>   `types.json`, die fünf Dispatch-Enums in `enums.json`. Der
>   `aiohomematic.const`-Import des Clients entfällt damit.

> _Die folgenden Unterabschnitte K1–K4 sind der **ursprüngliche Ask** (Stand vor
> Umsetzung, im Präsens). Der aktuelle Stand steht im **Status-Blockquote oben**._

### K1. Geräteprofil-Komposition daemon-seitig (löst `DeviceProfileRegistry` ab)

**Befund:** Nach `canonical.py` ist `aiohomematic.model.custom.DeviceProfileRegistry`
die **größte verbleibende aiohomematic-Laufzeitkopplung**. Der Client nutzt sie
in `model/device.py`, `compat/.../model/custom/__init__.py` und
`compat/.../model/naming.py`, um **CCU-Geräteprofil-Wissen** abzuleiten:
welcher Kanal der **Primärkanal** eines Geräts ist, welche Feld-Kanäle einen
Custom-DP (Cover/Climate/Light) **komponieren**, und das Climate-Mode-/
Profile-Vokabular (`ClimateMode`, `ClimateProfile`). Das ist reines
CCU-Domänen-Wissen — der Daemon spricht ohnehin mit der CCU und kennt die
Profile.

**Ask:** Auf den Geräte-/Custom-DP-Surfaces mitliefern:
- ein **Primärkanal-Marker** pro Gerät/Custom-DP
  (`handlers/devices.go`, `handlers/custom_data_points.go`),
- die **Kanal-Komposition** eines Custom-DP (welche Feld-Kanäle/-Parameter ihn
  bilden) — heute leitet der Client das aus dem Profil ab,
- das **Climate-Mode-/Profile-Vokabular** als Enum in den generierten Typen
  (analog `enums.json`, C1).

Danach kann der Client `DeviceProfileRegistry` streichen.

### K2. Normalisierter Custom-DP-Gerätezustand

**Befund:** `CustomDataPointSummary.state` liefert heute einen Zustands-Dict,
aber **halb-roh** — der Client interpretiert/skaliert ihn noch selbst
(`custom/__init__.py`: `hs_color` aus `color:{h,s}`, `current_position`,
`hvac_mode`, `target_temperature`, `brightness`, `is_locked` …, ~1100 LOC).
Die **CCU-seitige Normalisierung** (welcher Parameter ist Soll/Ist, welcher
Zustand ist „verriegelt", die Cover-Richtungslogik) ist generisch.

**Ask:** Den Custom-DP-State **CCU-normalisiert** ausliefern (Cover-Level
[0..1], Climate Soll/Ist/Mode, Light an/Level/Farbe, Lock-Zustand) — als
stabile, benannte Felder statt eines paramset-nahen Dicts. Der Client macht
dann nur noch die **HA-Einheiten-Skalierung**, nicht mehr die
Paramset-Interpretation. (Vorbild: die **berechneten Datenpunkte**, die der
Daemon bereits aus Kanal-Generics ableitet — dasselbe Muster für Custom-DPs.)

### K3. Firmware-Update-Status als abgeleitetes Feld

**Befund:** Der Client klassifiziert den rohen Geräte-Firmware-Zustand über
die Konstanten-Mengen `HMIP_FIRMWARE_UPDATE_IN_PROGRESS_STATES` /
`HMIP_FIRMWARE_UPDATE_READY_STATES` (aus `aiohomematic.const`, via `_upstream.py`),
um daraus den HA-Update-Entity-Zustand zu bauen (`compat/.../model/update.py`).
Die State-Mengen sind CCU-Domäne.

**Ask:** Ein abgeleitetes `update_status`-Feld auf dem Geräte-/Update-Surface
(`up_to_date | update_available | installing | …`) statt der rohen Zustände,
sodass der Client die Klassifikations-Mengen nicht mehr trägt.

### K4. CCU-Domänen-Konstanten aus den generierten Typen, nicht aus aiohomematic

**Befund:** Der Client importiert aus `aiohomematic.const` noch die
Pseudo-Adressen (`HUB_ADDRESS`, `INSTALL_MODE_ADDRESS`, `PROGRAM_ADDRESS`,
`SYSVAR_ADDRESS` — die den Central-Slot der `unique_id` füllen) sowie die
Dispatch-relevanten Enums `DataPointCategory`, `DataPointKey`, `ParamsetKey`,
`DeviceTriggerEventType`, `CCUType`, `CentralState`. Diese sind CCU-Domäne und
sollten aus dem **Daemon-generierten** `openccu-loom-types` (bzw.
`enums.json`, C1) kommen, nicht aus `aiohomematic`.

**Ask:** Sicherstellen, dass alle dispatch-/identitätsrelevanten Enums und
Pseudo-Adress-Konstanten im generierten Typenpaket vollständig vorhanden sind
(C1 deckt `DataPointCategory` bereits als Breaking-Change-Achse ab — die
Lücke sind die übrigen Enums + die vier Pseudo-Adressen). Dann fällt der
`aiohomematic.const`-Import im Client weg.

**Reihenfolge / Abhängigkeit:** K baut auf J auf. Sinnvolle Folge:
J1 (Keys auf REST) → J2 (Drift-Guard) → K4 (Konstanten/Enums) → K1
(Geräteprofile, der große Hebel) → K2/K3 (State-/Status-Normalisierung).
Nach J+K1+K4 ist der **Compat-Shim** auf reine HA-Adaption reduziert und der
**Client-Kern** aiohomematic-frei.

---

## I. Geprüft, bewusst zurückgestellt

Die folgenden Themen wurden gegen typische blinde Flecken externer
Wire-Clients geprüft und als nicht-blockierend klassifiziert. Sie
stehen hier, damit sie nicht später als „vergessen" wiederauftauchen
— bei konkreter Nachfrage eines externen Clients (oder beim
Übergang zu Multi-HA-Instances, Mobilfunk-Deployments, Token-Rotation
auf Geräten) können sie als eigene Asks in die A-H-Sektionen
promoviert werden.

| Thema | Status / Begründung der Zurückstellung |
|---|---|
| Rate-Limiting / Throttling-Verhalten (HTTP 429? `Retry-After`?) | Interne Diagnostik existiert (`wsapi.json:51-52`, `ccu.throttle_stats`); Client-sichtbares Verhalten ist undokumentiert, aber Detail-Ask, kein Wire-Client-Blocker. |
| Timezone-Semantik (`last_seen_at`, `last_changed_at` — UTC garantiert?) | OpenAPI ist hier inkonsistent: `modified_at` ist sogar nur `type: string` ohne `format: date-time` (`openapi.yaml:2082`), `last_seen_at`/`last_changed_at` analog. Kosmetischer Klarstellungspunkt — UTC-Garantie + RFC3339Nano sollten überall annonciert sein. |
| WS-Heartbeat-Intervall / Idle-Timeout | Nirgends exponiert. Mobilfunkproxies / VPN können Idle-WS killen — lässt sich client-seitig mit `ping`-Probing lösen. Ops-Detail. |
| Token-Rotation auf langlebigen WS-Verbindungen | Nicht spezifiziert. Eher Loom-Auth-Reife als Wire-Client-Thema; relevant sobald langlebige Geräte-Tokens im Spiel sind. |
| Multi-Central-Semantik in `wsapi.json` | Payloads tragen `central:`-Feld (gut). HA-Use-Case ist heute single-central pro Config-Entry — kein HA-Regression-Risiko. Wird erst akut, wenn HA Multi-Central in einem Entry erlauben will. |
| Idempotency-Keys auf `PUT …/value` | Nicht vorhanden — `aiohomematic` hat es aber auch nicht, also keine Regression durch den Cutover. |
| `GET /data-points/{unique_id}` (Lookup-by-unique_id) | Geprüft, **geringer Mehrwert**: der Store des `py-openccu-loom-client` IST bereits der `unique_id`→DP-Index (O(1) lokal). Ein REST-Lookup hilft nur einem **stateless, snapshot-losen** Client; die HA-Integration hält immer den Store. Promovierbar, sobald dünne REST-only-Clients ein realer Use-Case werden. |

---

## TL;DR — die fünf wertvollsten Asks

Für die konkrete Migration `aiohomematic` → `py-openccu-loom-client`
in `homematicip_local`:

1. **A1 + A2 + A3** — Push-Events vollständig in `wsapi.json` (Owner)
   inkl. Topic-Konvention, Payload-Schemas via `$ref` in
   `openapi.yaml`. Ohne das kann kein Wire-Client ohne
   Go-Code-Lektüre gebaut werden.
2. **B1** — Sequence-Nummern oder Generation-Counter. Sonst ist jede
   Form von Recovery nach Reconnect eine Race-Condition (HA *und*
   Tests *und* CI-Snapshot).
3. **B3** — Capability-/Version-Handshake. Voraussetzung für
   Forward-Compat über Loom-Releases hinweg.
4. **C1 + C2** — JSON-Schema-Export aller Enums plus offizielles
   WS-Envelope-Schema. Codegen-Basis für alle Sprachen.
5. **G1** — Sysvar- und Program-Broadcasts (intern bereits da, nur
   nicht annonciert). Sonst muss der Client doch wieder pollen.

Wenn 1-5 umgesetzt sind, schrumpft `py-openccu-loom-client` selbst
auf etwa 1500-2000 LOC plus Compat-Layer und bleibt über Releases
hinweg automatisch stabil.
