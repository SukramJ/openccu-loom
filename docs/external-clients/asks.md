# External-Client Integration — Backlog & Vertragslücken

**Status:** Substantiell umgesetzt (5 Wellen + 3 ADRs) — verbleibende offene Punkte unten markiert
**Letzte Aktualisierung:** 2026-05-24

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

Was bleibt offen: nur F2 (bewusste Entscheidung). Alle übrigen 22
Asks sind entweder als Runtime-Feature gelandet oder als
Vertrags-Erweiterung in OpenAPI / wsapi.json / docs/ verankert.

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
