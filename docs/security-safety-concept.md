# Security & Safety — Konzept für eine eigene Melde- und Diagnose-Domäne

**Status:** Entscheidungen getroffen (§10), bereit zum Schnitt von Slice 1 · **Ziel-Release:** 0.53.0 · **API:** 3.14.0 → 3.15.0
**Betrifft:** `internal/alarm/**`, neue Pakete `internal/security/**`, `internal/model/security/**`, Nordflächen MQTT/REST/WS/Webhook, SPA

---

## 1. Bestandsaufnahme

### 1.1 Präzisierung der Prämisse: eigenes Gerät ja — aber ein leeres

Vom Anwender bestätigt: in HA erscheint eine Gerätekarte **„OpenCCU-Loom Alarm"**. Die Panels hängen also technisch *nicht* am Hub-Gerät der Zentrale — die Alarmdomäne ist bereits daemon-level und vom Hub getrennt:

| Aspekt | Beleg | Befund |
|---|---|---|
| Modell | `internal/model/alarmpanel/panel.go:19-56` | `Panel` ist eine eigenständige Projektionsstruktur — **kein** `datapoint.BaseDataPointFields`, **kein** `payload.Source`, **nicht** in `internal/central/registry/model_registry.go` |
| MQTT-Topics | `internal/north/mqtt/alarm_discovery.go:36-42`, ADR 0052 | `<base>/alarm/<zone>/{state,availability,event,set}` — **ohne** `<central>`-Segment, im Gegensatz zu allen Hub-Topics |
| HA-Gerät | `internal/north/mqtt/alarm_discovery.go:50-56` | eigene synthetische Karte `identifiers: ["openccu-loom_alarm"]`, **nicht** `openccu-loom_central_<name>` |
| Identität | `internal/model/alarmpanel/panel.go:56` | `openccu-loom_alarm_<zoneID>` — ohne CCU-Seriennummer, im Gegensatz zu Hub-IDs `loom_<serial10>_*` |
| REST/WS | `internal/north/rest/handlers/alarm.go:605-633`, `internal/north/rest/ws/alarm_panel.go:33-70` | eigene Routen `/api/v1/alarm/*` und eigene WS-Kategorie `alarm_panel`, **nicht** die Hub-Endpunkte |

**Das eigentliche Problem ist nicht die Zugehörigkeit, sondern die Substanz.** `alarmDeviceBlock()` (`internal/north/mqtt/alarm_discovery.go:50-56`) ist ein hartkodierter Drei-Felder-Block — `identifiers`, `name`, `manufacturer`. Kein `model`, keine `sw_version`, keine `configuration_url`, kein `via_device`, keine Topologie. Dahinter steht **kein Modellobjekt**: `alarmpanel.Panel` ist eine reine Projektionsstruktur ohne `datapoint.BaseDataPointFields`, ohne `payload.Source`, ohne Registrierung in `internal/central/registry/model_registry.go`. Die Karte existiert ausschließlich als Nebenprodukt der MQTT-Discovery. Deshalb liest sie sich wie ein Sammelbecken statt wie ein Gerät — und deshalb kann sie außer den Panels nichts tragen.

**Zusätzlich verstärkt** — zwei echte Hub-Flächen mit „Alarm" im Namen:

1. **CCU-Alarmmeldungen** (`internal/model/hub/messages.go:53`) sind ein Hub-Aggregat: ein Sensor mit `json_attributes_topic` auf der Zentralen-Gerätekarte (`internal/north/mqtt/hub_discovery.go:617-646`), REST unter der Hub-Route (`internal/north/rest/router.go:1128-1130`: `/alarm-messages`, `/alarm-messages/{id}/ack`). Das sind **CCU-Servicemeldungen**, nicht die Alarmanlage.
2. **Der Sysvar-Mirror** (`internal/alarm/sysvar.go:21-41`) schreibt den Zonenzustand in eine CCU-Systemvariable — die dann als *Hub*-Entität auf der Zentralen-Karte auftaucht (`internal/north/mqtt/hub_discovery.go:252-265`).

Die eigentliche Lücke ist also **nicht** „Loslösung vom Hub", sondern: es gibt für die Alarmanlage genau **eine** HA-Entitätsklasse (`alarm_control_panel`) und **keine** Melde- oder Diagnosedatenpunkte.

### 1.2 Was Home Assistant heute sieht

* Pro Zone ein `alarm_control_panel`, plus ein `master`-Aggregat ab 2 Zonen (`internal/north/mqtt/alarm_publisher.go:386-405`).
* **Sonst nichts.** Kein `sensor`, kein `binary_sensor`, kein `event`, kein `json_attributes_topic` auf dem Panel (`internal/north/mqtt/alarm_discovery.go:117-137`).
* Das reichhaltige JSON auf `<base>/alarm/<zone>/event` (`type`, `open_sensors[]`, `mode`, `changed_by`) hat **keine Discovery-Konfiguration** — es ist für HA unsichtbar und nur per handgeschriebenem MQTT-Trigger konsumierbar.
* Der Gesundheitsverdikt der Anlage (`internal/alarm/service.go:129-137`) senkt nur die *Availability* der Panels — in HA nicht von einem Broker-Ausfall unterscheidbar.

### 1.3 Was fehlt (belegt)

| Lücke | Beleg |
|---|---|
| Kein Auslöser-**Adressbezug** irgendwo im Audit-Trail | `incidentCause{Kind,SensorID,SensorName,Central}` — `internal/alarm/engine/engine.go:126-131`; Journal-Details `{"sensor_id":…}` — `engine.go:785,797` |
| Nur **ein** Auslöser pro Incident; Folgeauslösungen gehen verloren | `internal/alarm/engine/engine.go:1495-1505` |
| `AlarmNotificationEvent` — genau das Event, das ein Operator als „Benachrichtigungsausgang" einträgt — trägt **keine** Sensoridentität | `pkg/hmevent/alarm.go:34-51`; Webhook: `internal/north/webhook/outbound.go:330-349` |
| **Keine** Message/Subject/Severity in irgendeiner Schicht, in keiner Sprache | `pkg/hmevent/alarm.go` (12 Typen, nur Maschinentoken); Webhook-Payload: `internal/north/webhook/outbound.go:588-610` |
| Kein Live-**Offen**-Sensor-Bestand; `openAtArm` ist eine Scharfschalt-Baseline | `internal/alarm/engine/zone.go:63,259-303` |
| Readiness dedupliziert den **Grund** weg | `internal/alarm/engine/readiness.go:44-65` |
| Incident-Ledger ist schreib-only (0 Produktionsaufrufer) | `internal/store/sqlite/alarm_incidents.go:100,196` |
| `open_sensors[]` mischt zwei Identifikatorräume (Namen bei TRIGGER, IDs bei FAILED_TO_ARM) | `internal/north/mqtt/alarm_publisher.go:191-231` |
| **Einzelentitäten sind besser abgedeckt als gedacht** — die Lücke ist die Aggregation, nicht die Klassifikation | Gegen den godevccu-Discovery-Snapshot verifiziert: HmIP-SWD → `MOISTURE_DETECTED`/`WATERLEVEL_DETECTED` = `device_class: moisture`, `ALARMSTATE` = `safety`; HmIP-SWSD → `SMOKE_ALARM` = `smoke`; HM-Sec-SD-2 → `STATE` = `smoke`. Diese Regeln existieren und greifen. |
| Echte Restlücken auf Parameterebene: HM-Sec-WDS `STATE` (ENUM DRY/WET/WATER) landet als `sensor` mit `device_class: enum` statt als Feuchtigkeits-Binärsensor; roher `SMOKE_DETECTOR_ALARM_STATUS` ebenfalls `enum`; keine `hmenum.Parameter`-Konstanten für die Wasserfamilie | Snapshot `tests/integration/testdata/discovery_snapshot_openccu-loom.json`; `internal/north/mqtt/entity_description_rules_sensors.go:314`; `pkg/hmenum/parameter.go` (keine Konstanten) |
| Gas/CO: **null** Abdeckung (`QuantityGas` ist Zählersemantik m³) | `pkg/hmenum/quantity.go:19-51` |
| Tote Nachschlageschlüssel täuschen Abdeckung vor | `pkg/hmui/quantity.go:137` (`WATER_DETECT`), `assets/ui/src/lib/sensor-actor/primary.ts:26-30` (`WATER_DETECTOR`/`RAIN_DETECTOR`), `internal/north/mqtt/entity_description_rules_binary_sensors.go:64-68` (`{HmIP-SWD, STATE}→window`, inert) |
| Sicherheitsrelevante Gerätefehler haben **keinen** Datenpunkt | `internal/store/visibility/rules.go:288-328` unterdrückt `ERROR*`/`SENSOR_ERROR*` außerhalb einer 4-Modell-Allowlist |
| `paramValueActive` reduziert ENUMs auf `index != 0` | `internal/alarm/inputs.go:238-249` — die SPA füllt für Rauchmelder genau den rohen ENUM vor (`assets/ui/src/lib/alarm/sensorCandidates.ts:46-47`) |
| Kein Retain-Reaper für die daemon-level Bäume | `internal/north/mqtt/retain_cleanup.go` enthält **kein** einziges `alarm`-Token (verifiziert: 0 Treffer) |

---

## 2. Empfehlung

### 2.1 Gewinner: der „HA-Automations-first"-Entwurf (Angle C), mit fünf harten Korrekturen

Die drei Entwürfe unterscheiden sich nicht im *Inhalt* der Datenpunkte — der ist in allen dreien im Kern identisch und richtig —, sondern in der **Trägerstruktur**:

| Entwurf | Trägerstruktur | Urteil |
|---|---|---|
| A | synthetisches `*device.Device` `LOOM-SECURITY`, Kanal 0/N | **verworfen** — der Hauptnutzen (Matter „gratis") existiert nicht: `endpoint.Snapshot` verlangt `CentralName != ""` (`internal/north/matter/endpoint/assembler.go`), und die Matter-Endpoint-Identität hängt an der Kanalnummer, die der Entwurf selbst als wegwerfbar deklariert. Preis: ~55 Geräte-Unterrouten brauchen Sonderbehandlung, ein neuer `ParamsetKey`, eine Kanal-Zuteilungstabelle — für einen Eintrag in `GET /devices`. |
| B | dritte Domänen-Ebene mit `payload.Source`-Zeremonie | **verworfen** — die Fehlerachse zieht aus `internal/model/generic/quantity.go:139-186` (DUTY_CYCLE, LOW_BAT, POWER_MAINS_FAILURE …) über die **ganze Flotte**: in jeder realen Installation dauerhaft ON, also nie eine Flanke. Zusätzlich: `QuantitySafety` enthält `ACOUSTIC_ALARM_ACTIVE`/`OPTICAL_ALARM_ACTIVE` — die Ebene würde ihre eigene Sirene als Ursache melden. |
| **C** | **eigene daemon-level Meldeebene mit eigener HA-Gerätekarte, keine Fake-Geräte** | **empfohlen** |

### 2.2 Die fünf Korrekturen an C

1. **Keine `payload.Source`/`MQTTAddressable`-Implementierung.** Es gibt keinen generischen Konsumenten: jeder `MQTTAddressable`-Aufrufer ist eine handgeschriebene, typisierte Methode (`internal/north/mqtt/bridge.go:1034-1168`). Wir bauen die Ebene wie `internal/model/alarmpanel` — schlichte Projektionsstrukturen + dedizierte Publisher/Handler. Das ist die im Repo bewährte und einzige tatsächlich funktionierende Form.
2. **Zonen-IDs sind UUIDs** (`internal/north/rest/handlers/alarm_config.go:73,415` — verifiziert). Entity-IDs mit UUID sind für YAML unbrauchbar. → Jede Zone bekommt einen **Slug** (Migration 034), der ausschließlich in den *neuen* Security-Entitäten verwendet wird. Die bestehenden Panel-IDs bleiben UUID-basiert und eingefroren.
3. **Klassifikator läuft über einen vorgebauten Index, nicht pro Event.** `hmevent.DataPointValueChangedEvent` trägt nur den `DataPointKey` und den Wert (`pkg/hmevent/catalogue.go:272-277`) — kein Modell, kein Kanaltyp, keine `VALUE_LIST`. Der Index wird bei Attach/Modelländerung einmal gebaut (Vorbild: `rebuildIndexes` in `internal/alarm/inputs.go:23-40`) und löst `dpKey → {Modell, Kanaltyp, Klasse, ActiveValues}` per Map-Lookup auf.
4. **Sicherheitsrelevanz-Gate statt Flottenweite** (behebt Bs Fataldefekt): `tamper`, `battery` und `technical` werden **nur** für sicherheitsrelevante Geräte aggregiert (Definition in §3.4). Flottenweite Gesundheit bleibt bei `internal/health` und der Fleet-Ansicht.
5. **Kein neues Config-Feld für die Basis-URL** — `north.rest.public_url` existiert bereits als validiertes `cfg:"basic"`-Feld (`internal/config/config.go:975`, verifiziert). Wird wiederverwendet.

### 2.3 Aus den Verlierern übernommen

* **Aus A:** vollständiger Datenpunkt-Inhalt (ausgelöste Sensoren, Störquellen, Bereitschaft mit Grund, Incident-Ledger, Walk-Test), `retain_cleanup`-Matcher, `configuration_url` auf der Gerätekarte, gelabelte Metriken, die Absage an `hazard`-Split zugunsten einer orthogonalen Klassenachse.
* **Aus B:** die **Drei-Achsen-Aggregation System / Zone / Klasse**, der `SensorRef` mit zentralen-qualifiziertem `ref`-Schlüssel, die Wiederverwendung der bestehenden Quantity-Tabelle als *eine* Klassifikatorquelle (mit Ausschlussliste), die Notiz auf `BooleanStateConfiguration 0x0080` als künftiges Matter-Zuhause, und die vollständige Absage-Liste (kein Fake-Device, keine nie-zugewiesenen `DataPointCategory`-Werte, keine Aufweichung von `hmtypes.DataPointKey`).

### 2.4 Name

**Domäne / HA-Gerät: „Security & Safety" (EN) · „Sicherheit & Gefahrenmelder" (DE)**
Technisches Token durchgängig: `security`.

Begründung der Wahl:

* **„Alarm" scheidet aus** — der Begriff ist im Produkt dreifach belegt: die Zonen/ACPs (`internal/model/alarmpanel`), die CCU-Alarmmeldungen (`hub.AlarmMessages`) und die Sysvar-Spiegelung. Ein viertes „Alarm" wäre für Operatoren nicht auflösbar.
* **„Sicherheit" allein scheidet aus** — im Repo semantisch von `internal/auth` und `internal/secret` besetzt (Authentifizierung, Secret-at-Rest). Der Paket-Doc-Kommentar von `internal/security` muss die Abgrenzung explizit machen.
* **„Gefahrenmelder"** ist der deutsche Fachbegriff der Branche (Gefahrenmeldeanlage: EMA Einbruch + BMA Brand + technische Meldungen) und deckt exakt den geforderten Umfang ab.
* **„Security & Safety"** trennt im Englischen sauber die beiden Halbachsen (Security = Einbruch/Sabotage, Safety = Brand/Wasser/Gas) und ist genau die Formulierung der Anforderung.

Alternativen, falls gewünscht: „Gefahrenmeldung", „Melder & Störungen", „Hazards". (→ offene Entscheidung 1)

---

## 3. Modell

### 3.1 Identität

Die Domäne ist **daemon-level** (zentralenunabhängig, wie ADR 0052 für Zonen festlegt) und **singulär** — ein Prozess, eine Instanz.

```
kind   := system | class | zone
scope  := ""            (system)
        | <class>       (smoke|water|gas|co|tamper|battery|technical|intrusion|panic)
        | <zone-slug>   (stabiler Slug, NICHT die UUID)

unique_id := "loom_security" ["_" scope] "_" key      (object_id == unique_id)
topic     := <base>/security/<kind>[/<scope>]/<key>
discovery := <disco>/<component>/security/<unique_id>/config      (node_id: "security")
```

Beispiele: `binary_sensor.loom_security_smoke`, `event.loom_security_event`, `sensor.loom_security_zone_eg`, `sensor.loom_security_last_alarm`.

**Neue Ableitungsfunktion:** `routingkey.DaemonUniqueID(kind string, parts ...string) string` in `internal/routingkey/uniqueid.go` (die Datei hat heute nur `GenerateUniqueID`/`GenerateChannelUniqueID` — verifiziert). Sie bekommt **eingefrorene Legacy-Zweige**, die `openccu-loom_alarm_<zoneID>` (`internal/model/alarmpanel/panel.go:56`) und `loom_addon_update` (`internal/north/mqtt/addon_update_discovery.go:33`) byte-identisch reproduzieren. Danach rufen `alarmpanel.PanelUniqueID`, `internal/north/mqtt/alarm_discovery.go:116` und die REST-Projektion in `internal/north/rest/handlers/alarm.go:610-633` alle dieselbe Funktion auf — die heutige Drei-Wege-Duplikation verschwindet, abgesichert durch einen Golden-Fixture-Contract-Test.

**HA-Gerätekarte:** eine **neue**, eigene Karte `identifiers: ["openccu-loom_security"]`, Name „Security & Safety" / „Sicherheit & Gefahrenmelder", `model`, `sw_version` (aus `internal/build/version.go`), `manufacturer: "OpenCCU-Loom"`, `configuration_url` = `north.rest.public_url` + `/app/#/security`.

> Ausdrücklich **nicht** die bestehende `openccu-loom_alarm`-Karte mitbenutzen. Grund: `alarmDeviceBlock()` (`internal/north/mqtt/alarm_discovery.go:50-56`) wird bei **jeder** Panel-Discovery mitgeschrieben und auf jedem Broker-Reconnect erneut (`RepublishDiscovery`). Zwei Publisher mit abweichenden Blöcken unter einem Identifier-Set lassen den Kartennamen flattern und würden die Friendly-Names aller bestehenden ACP-Entitäten ändern. Zwei Karten = null Risiko — und es ist genau das „eigene Gerät", das die Anforderung verlangt.

### 3.2 Aufbau: System → Zone → Klasse

```
SecuritySystem                        (daemon-level, 1 Instanz)
├── severity: ok|info|warning|alarm|critical      (Präzedenz, §4)
├── classes[9]      ClassState        (fachliche Achse, zentralenübergreifend)
│     └── sources[] SourceRef
├── zones[n]        ZoneState         (nur wenn Alarm-Engine aktiv)
│     ├── sources[] SourceRef  (by_class-Facette statt Zone×Klasse-Matrix)
│     └── readiness by_mode + blockers mit Grund
└── faults[]        Fault             (persistiert, ack-bar)
```

Die **„pro Typ"-Achse ist eine Facette der Zonenentität** (`by_class`), keine eigene Entitätsmatrix — sonst entstünden bei 3 Zonen × 9 Klassen 27 Entitäten für dieselbe Information.

### 3.3 Multi-CCU-Semantik

* Wurzel, Klassen und Zonen sind **zentralenunabhängig**.
* Die Zentrale wandert eine Ebene tiefer: jeder `SourceRef` trägt `central`, `interface_id`, `channel_address`, `parameter` **verpflichtend**.
* Dedup-/Referenzschlüssel `ref := <central>|<interface_id>|<channel_address>|<parameter>` — identisch zum `dpKey` des Engine-Index (`internal/alarm/inputs.go:33`), damit zwei CCUs mit identischer Kanaladresse nicht kollidieren.
* Die Klassenaggregate tragen eine `centrals[]`-Facette. Es gibt **keine** Entität pro Zentrale (kombinatorische Explosion).
* **Teardown:** Der Service hängt sich wie `alarm.Service.AttachCentral/DetachCentral` (`internal/alarm/service.go:475-500`) an die Central-Hooks. Beim Detach werden alle Beiträge dieser Zentrale aus den Aggregaten entfernt — sonst bliebe `smoke` mit einer Geisterquelle dauerhaft ON.
* Fällt eine von N Zentralen aus, wird ein `technical`-Fault mit `reason: central_lost` gesetzt; die Klassenzustände der übrigen Zentralen bleiben unberührt und tragen `stale: true` für die betroffenen Quellen.

### 3.4 Sicherheitsrelevanz-Gate

Ein Datenpunkt gehört zur Domäne, wenn **mindestens eines** gilt:

1. Er ist als Alarmsensor enrolled (`internal/store/sqlite/alarm_sensors.go`) oder liegt auf einem Gerät, das einen Alarmsensor/-ausgang trägt.
2. Der Klassifikator ordnet ihn einer **Gefahrenklasse** zu (`smoke`, `water`, `gas`, `co`).
3. Der Operator hat ihn explizit aufgenommen (`security_sources.included = 1`).

`tamper`, `battery` und `technical` werden **ausschließlich** für so qualifizierte Geräte aggregiert. Damit ist `binary_sensor.loom_security_problem` in einer 400-Geräte-Installation ein echtes Signal und nicht dauerhaft ON.

### 3.5 Klassifikator

Schlüssel ist **(Modell, Kanaltyp, Parameter)** — nicht der Parameter allein. Grund: `ALARMSTATE` bedeutet auf `WATER_DETECTION_TRANSMITTER` (HmIP-SWD) Wasser, auf einer Sirene aber Aktorrückmeldung.

Quellen in dieser Reihenfolge:

1. **Operator-Override** (`security_sources.class`)
2. **Alarm-Enrollment** (`alarm_sensors.sensor_type`)
3. **Explizite Tabelle** in `internal/model/safety/classify.go` (NEU) für die Gefahrenklassen
4. **Bestehende Quantity-Tabelle** `internal/model/generic/quantity.go:140-186` für `tamper`/`battery` — Wiederverwendung, keine fünfte Parallelität

**Harte Ausschlussliste** (Rückkopplungsschutz, contract-getestet): `ACOUSTIC_ALARM_ACTIVE`, `OPTICAL_ALARM_ACTIVE`, `EMERGENCY_OPERATION`, `SMOKE_DETECTOR_COMMAND`, der berechnete `INTRUSION_ALARM` und jeder Parameter, den `internal/alarm/outputs/manager.go` selbst schreibt. Ohne diese Liste meldet die Ebene ihre eigene Sirene als Ursache.

### 3.6 Go-Pakete und Dateien

**Neu:**

| Pfad | Inhalt |
|---|---|
| `pkg/hmenum/security.go` (NEU) | `SecurityClass`, `SecuritySeverity`, `SecurityFaultReason`, `SecurityVerb` — drahtsichtbar |
| `pkg/hmevent/security.go` (NEU) | `SecurityStateChanged`, `SecurityClassChanged`, `SecurityZoneChanged`, `SecurityFaultChanged`, `SecurityNotification`, `SecuritySourceRef` |
| `pkg/hmapi/security.go` (NEU) | REST/WS-DTOs |
| `internal/model/safety/classify.go` (NEU) | Klassifikationstabellen + `Classify(model, channelType, parameter) (Class, ActiveValues, ok)` |
| `internal/model/security/{snapshot,source,fault,notification}.go` (NEU) | Projektionsstrukturen, Formvorbild `internal/model/alarmpanel/panel.go` |
| `internal/security/{service,index,aggregator,faults,render,store}.go` (NEU) | Daemon-level Dienst, Geschwister von `internal/alarm/service.go` |
| `internal/store/sqlite/{alarm_incident_sources,security_faults,security_sources}.go` (NEU) | Persistenz |
| `internal/store/sqlite/migrations/033_alarm_incident_sources.sql` (NEU) | nächste freie Nummer nach `032_room_areas.sql` (verifiziert) |
| `internal/store/sqlite/migrations/034_security_domain.sql` (NEU) | `alarm_zones.slug`, `security_sources`, `security_faults` |
| `internal/north/mqtt/{security_discovery,security_publisher}.go` (NEU) | |
| `internal/north/rest/handlers/security.go` (NEU) | |
| `internal/north/rest/ws/security_events.go` (NEU) | |
| `internal/metrics/security_collector.go` (NEU) | |
| `cmd/openccu-loom/security_wiring.go` (NEU) | Composition-Root |
| `assets/ui/src/routes/security/{SecurityOverview,SecuritySources,SecurityFaults}.svelte` (NEU) | |

**Bestehende Dateien mit Änderungen:** `pkg/hmevent/alarm.go`, `pkg/hmenum/parameter.go`, `pkg/hmapi/alarm.go`, `internal/alarm/engine/{engine,readiness,zone,config}.go`, `internal/alarm/inputs.go`, `internal/store/sqlite/{alarm_incidents,alarm_zones}.go`, `internal/store/visibility/rules.go`, `internal/routingkey/uniqueid.go`, `internal/i18n/i18n.go`, `internal/i18n/catalogs/{en,de}.json`, `internal/north/mqtt/{retain_cleanup,alarm_publisher,alarm_discovery,entity_description_rules_binary_sensors}.go`, `internal/north/rest/{router.go,handlers/alarm_config.go,handlers/info.go}`, `internal/north/webhook/outbound.go`, `internal/model/generic/matter.go`, `pkg/hmui/quantity.go`, `assets/ui/src/lib/{i18n.ts,nav.ts}`, `assets/ui/src/lib/sensor-actor/primary.ts`, `assets/ui/src/lib/alarm/sensorCandidates.ts`, `assets/openapi.yaml`, `assets/wsapi.json`.

### 3.7 „DataPointKey-Schema" — zwei getrennte Schlüsselräume

`hmtypes.DataPointKey` (`pkg/hmtypes/datapoint_key.go:23-60`) verlangt vier **nicht-leere** Komponenten (Interface, Kanal, Paramset, Parameter) und ist damit strukturell CCU-gebunden. Wir weichen ihn **nicht** auf. Stattdessen:

**(a) Quellschlüssel — referenziert echte CCU-Datenpunkte**

```
ref := <central> "|" <interface_id> "|" <channel_address> "|" <parameter>
```

Verlustfrei in einen `hmtypes.DataPointKey` konvertierbar (`ParamsetKey` = `VALUES` bzw. `CALCULATED`), damit Deep-Links in die Gerätedetailansicht, History und Un-Ignore funktionieren. Identisch zum Engine-Index-Schlüssel — kein zweites Format.

**(b) Entitätsschlüssel — die daemon-level Aggregate**

```
SecurityKey := <kind> "/" <scope> "/" <key>
```

Kein `DataPointKey`, keine Zentrale, kein Kanal. Genau wie `alarmpanel.Panel` heute. Ein Contract-Test pinnt, dass die reservierten System-Keys (`state`, `alarm`, `problem`, `health`, `test_mode`, `last_alarm`, `last_fault`, `event`, `fault_event`) mit keiner Klassen-ID kollidieren.

---

## 4. Datenpunkt-Katalog

Legende: `retained` = MQTT retained; State-Topic dient gleichzeitig als `json_attributes_topic` (bewährtes Muster, `internal/north/mqtt/hub_discovery.go:617-646`). **Attribut-Payloads sind immer Objekte**, nie nackte Listen — HA verwirft letztere mit „JSON result was not a dictionary".

### 4.1 System-Ebene (9 Entitäten)

| Key | Kategorie / HA-Entity | Wertform | Zweck | Attribute |
|---|---|---|---|---|
| `security/system/event` | `event` (`DataPointCategoryEvent`) · **nicht retained, QoS0** | JSON pro Auslösung; `event_type ∈ {triggered, pre_alarm, silenced, cleared, failed_to_arm, test}` | **Der** Automations-Primitive für Alarm-/Gefahrenfälle. Feuert auch bei zwei identischen Vorfällen (State = Feuerzeitstempel) | `class, severity, verb, subject, message, i18n_key, args, zone{id,slug,name}, mode, incident_id, sources[], source_names[], centrals[], count, truncated, total, link, ts` |
| `security/system/fault` | `event` · **nicht retained, QoS0** · diagnostic | `event_type ∈ {raised, cleared}` | Der Störfall-Zwilling — eigene Entität, damit Störungen ohne Bedingung an ein anderes Ziel geroutet werden können | `class, reason, severity, subject, message, i18n_key, args, source{}, since, central, link, ts` |
| `security/system/state` | `sensor`, `device_class: enum` · retained | `ok \| info \| warning \| alarm \| critical` | Ein-Blick-Zustand der Domäne. Präzedenz: `critical` (smoke/gas/co) > `alarm` (intrusion/panic/water) > `warning` (tamper, Engine ungesund) > `info` (technical/battery) > `ok` | `classes{<class>:{active,count}}, zones{<slug>:{state,mode,triggered}}, open_faults, armed_zones, last_alarm_ts, last_fault_ts` |
| `security/system/alarm` | `binary_sensor`, `device_class: safety` · retained | bool | „Es liegt jetzt eine Gefahr an" = OR(smoke, water, gas, co, intrusion, panic-laut). Der eine HA-Trigger | `count, sources[], source_names[], by_class{}, by_zone{}, since, truncated, total` — **Achse „ausgelöste Sensoren pro System"** |
| `security/system/problem` | `binary_sensor`, `device_class: problem` · retained · diagnostic | bool | „Der Sicherheitsbereich ist gestört" = OR(technical, battery, tamper), **nur sicherheitsrelevante Geräte** | `count, faults[]{id,class,reason,severity,subject,central,device,address,parameter,since,acknowledged}, truncated, total` |
| `security/system/health` | `binary_sensor`, `device_class: problem` · retained · diagnostic | bool (ON = ungesund) | Macht `AlarmHealthChangedEvent` zur eigenen Entität, statt nur die Panel-Availability zu senken (heute in HA nicht von Broker-Ausfall unterscheidbar) | `note` (stabiler engl. Maschinenstring aus `internal/alarm/outputs/watchdog.go:121,129`), `since` |
| `security/system/test_mode` | `binary_sensor` · retained · diagnostic, default off | bool | ON solange irgendeine Zone im Gehtest ist — verhindert, dass Automationen während des Tests Meldungen versenden | `zones[], seen, total, started_at` |
| `security/system/last_alarm` | `sensor`, `device_class: timestamp` · retained | RFC3339 | Neustartfeste Dashboard-Hälfte des Ereignisses. HA-`event`-Entitäten sind nach Neustart `unknown` | vollständige letzte Alarm-Nutzlast (siehe `system/event`) — **Träger von Subject & Message** |
| `security/system/last_fault` | `sensor`, `device_class: timestamp` · retained · diagnostic | RFC3339 | Symmetrisch; ein Störungssturm überschreibt so nie den Nachweis des Brandes | vollständige letzte Störungs-Nutzlast |

### 4.2 Klassen-Ebene (bis 9 Entitäten, bedingt publiziert)

Discovery wird **nur** ausgeliefert, wenn der Klassifikator-Index mindestens eine Quelle dieser Klasse kennt. Keine dauerhaft OFF stehenden Geisterentitäten.

| Key | Kategorie / HA-Entity | Wertform | Zweck | Attribute |
|---|---|---|---|---|
| `security/class/smoke` | `binary_sensor`, `device_class: smoke` · retained | bool | Rauch/Feuer systemweit, **unabhängig vom ACP** | `count, sources[], source_names[], by_zone{}, centrals[], since` |
| `security/class/water` | `binary_sensor`, `device_class: moisture` · retained | bool | Wasser/Leckage. Quellen: HmIP-SWD (`ALARMSTATE`, `MOISTURE_DETECTED`, `WATERLEVEL_DETECTED` — die ODER-Verknüpfung leistet das Aggregat selbst) und HM-Sec-WDS (`STATE ∈ {WET, WATER}` via ActiveValues) | wie oben, zusätzlich `severity_order` (moisture < waterlevel) |
| `security/class/gas` | `binary_sensor`, `device_class: gas` · retained | bool | Brenngas. Heute ohne Producer im Homematic-Bestand → Entität wird nicht publiziert, bis eine Quelle klassifiziert ist | wie oben |
| `security/class/co` | `binary_sensor`, `device_class: carbon_monoxide` · retained | bool | CO — getrennt von Gas, weil die Eskalation eine andere ist | wie oben |
| `security/class/tamper` | `binary_sensor`, `device_class: tamper` · retained · diagnostic | bool | Sabotage: `SABOTAGE*`-Familie, `BLOCKED_TEMPORARY/PERMANENT`, tamper-typisierte Sensoren (heute nur eine Journalzeile `tamper_while_disarmed`) | wie oben, `reason` pro Quelle |
| `security/class/battery` | `binary_sensor`, `device_class: battery` · retained · diagnostic | bool | Schwache Batterie **sicherheitsrelevanter** Geräte | wie oben |
| `security/class/technical` | `binary_sensor`, `device_class: problem` · retained · diagnostic | bool | Technische Störung: unerreichbar, Keypad geblockt, Gerätefehler (`ERROR_SMOKE_CHAMBER`, `ERROR_ALARM_TEST`, `ERROR_CODE`, `ERROR_NON_FLAT_POSITIONING`), Zentralenverlust | wie oben, `reason ∈ {unreachable, blocked, device_error, central_lost, duty_cycle}` |
| `security/class/intrusion` | `binary_sensor`, `device_class: safety` · retained | bool | Einbruch — die Klassenprojektion der Engine, damit ein Konsument über Klassen iterieren kann | wie oben, `zones[]{id,slug,name,state,mode,incident_id}` |
| `security/class/panic` | `binary_sensor`, `device_class: safety` · retained | bool | Panik/Überfall (laut). Stille Auslösungen sind ausgenommen (§7.6) | wie oben |

### 4.3 Zonen-Ebene (2 Entitäten pro Zone, nur bei aktiver Engine)

| Key | Kategorie / HA-Entity | Wertform | Zweck | Attribute |
|---|---|---|---|---|
| `security/zone/<slug>` | `sensor`, `state_class: measurement` · retained | int = Anzahl gerade aktiver Quellen der Zone | **Achsen „pro Zone" und „pro Typ" in einer Entität** | `by_class{smoke:[names], water:[…], intrusion:[…], tamper:[…]}, sources[], source_names[], open_sensors[], bypassed[], zone_state, mode, incident_id, subject, message, since` |
| `security/zone/<slug>/arm_blocked` | `binary_sensor`, `device_class: problem` · retained | bool | „Warum kann ich nicht scharfschalten" | `by_mode{full:{ready,blockers[]{sensor_id,name,ref,reason},warnings[]}, …}, blocker_names[], reasons[]` — die heute wegdeduplizierte Begründung (`internal/alarm/engine/readiness.go:44-65`) |

### 4.4 Nicht-Entitäten (REST/WS/Persistenz)

| Key | Form | Zweck |
|---|---|---|
| `alarm_incident_sources` | Tabelle (Migration 033) | **Jede** Auslösung während eines Incidents, nicht nur die erste — heute gehen Folgeauslösungen verloren. Felder: `incident_id, ref, central, interface_id, channel_address, device_address, parameter, name, sensor_type, class, at_ms` |
| `security_faults` | Tabelle (Migration 034) | Persistenter offener Störungsbestand; `since` überlebt Neustarts, Quittierung möglich. Felder: `id, ref, class, reason, severity, central, device_address, channel_address, parameter, since_ms, cleared_at_ms, acknowledged_at_ms, acknowledged_by` |
| `security_sources` | Tabelle (Migration 034) | Operator-Klassifikation und Ein-/Ausschluss **auch für nicht-enrollte** Datenpunkte. PK `(central, interface_id, channel_address, parameter)`, Spalten `class TEXT NULL`, `included INTEGER NOT NULL DEFAULT 1` |
| `alarm_zones.slug` | Spalte (Migration 034) | Stabiler, lesbarer Zonenbezeichner für Topics/Entity-IDs |

**Kappungen (HA-Recorder-Limits):** max. 30 Einträge pro `sources[]`/`faults[]`-Attribut, dann `truncated: true` + `total: n` + `link` auf die REST-Route. Hintergrund: Recorder verwirft State-Attribute > 16 KiB, und ein Entity-State > 255 Zeichen wird zu `unknown`. Deshalb ist **kein** Freitext jemals ein Entity-State.

---

## 5. Message & Subject

### 5.1 Semantik

| Frage | Antwort |
|---|---|
| **Wer rendert?** | `internal/security/render.go` (NEU), serverseitig, **einmal pro Ereignis**. Es gibt heute im gesamten Alarm-Stack keinen Renderer — der einzige i18n-Zugriff ist der Master-Panel-Anzeigename (`cmd/openccu-loom/alarm_wiring.go:81`). |
| **Woher die Texte?** | Neuer Namensraum `security.subject.<class>.<verb>` und `security.message.<class>.<verb>` in `internal/i18n/catalogs/{en,de}.json` — **beide** Locales verpflichtend. |
| **Platzhalter?** | `internal/i18n/i18n.go` kennt heute nur `T(locale, key)` ohne Interpolation (verifiziert). → Neue Methode `TF(locale, key string, args map[string]string) string` mit benannten `{platzhalter}`. Platzhalter: `{sensor} {sensors} {count} {zone} {mode} {central} {time} {reason}`. |
| **In welcher Sprache?** | **Beides — kein Split-Brain.** Persistiert und mitgesendet werden `i18n_key` **und** `args{}`. MQTT/Webhook liefern zusätzlich den in `cfg.Locale` (`internal/config/config.go:123`, Default `en`) gerenderten Text — dieselbe Locale, die schon heute den Master-Panel-Namen und die MQTT-Discovery-Namen bestimmt. REST/WS/SPA rendern aus `i18n_key`+`args` **neu in der Request-Locale** (`internal/reqctx`), sodass die Oberfläche immer in der Sprache des Benutzers ist. |
| **Trigger?** | Alarm-Ereignis: Incident geöffnet, Pre-Alarm, Gefahrenklasse wird aktiv (auch ohne Zone), Stummschaltung, Incident geschlossen, Scharfschaltung verweigert, Testmeldung. Störungs-Ereignis: Fault geht auf/zu (mit Entprellung). |
| **Subject** | ≤ 120 Zeichen, eine Zeile, ohne Satzzeichen am Ende. `„Rauchalarm — Erdgeschoss"` / `„Smoke alarm — Ground floor"`. Bildet direkt den `title` einer HA-Notify-Aktion ab (`async_send_message(message, title)`). |
| **Message** | Unbegrenzt (praktisch ≤ 1024), vollständiger Satz mit Ursache, Ort und Zeit: `„Rauchmelder Flur EG hat um 03:14 Uhr ausgelöst (Zone Erdgeschoss, Modus Vollschutz)."` Bei mehreren Quellen: `„3 Melder haben ausgelöst: Flur EG, Küche, Keller."` |
| **Struktur** | **Beides parallel.** Fertigtext (`subject`, `message`) für den 3-Zeilen-Fall; Maschinenfacetten (`class`, `severity`, `verb`, `sources[]`, `source_names[]`, `zone`, `mode`, `incident_id`, `i18n_key`, `args`) für eigene Formulierungen. Nie nur das eine. |

### 5.2 HA-Automation — der Regelfall (9 Zeilen)

```yaml
automation:
  - alias: Sicherheitsmeldung an Messenger
    triggers:
      - trigger: state
        entity_id: event.loom_security_event
    actions:
      - action: notify.telegram_familie
        data:
          title: "{{ trigger.to_state.attributes.subject }}"
          message: "{{ trigger.to_state.attributes.message }}"
```

### 5.3 Gezielt — nach Klasse und mit Nennung der Melder

```yaml
automation:
  - alias: Brandmeldung, alle wecken
    triggers:
      - trigger: state
        entity_id: event.loom_security_event
    conditions:
      - condition: template
        value_template: "{{ trigger.to_state.attributes.class in ['smoke','gas','co'] }}"
    actions:
      - action: notify.alle_handys
        data:
          title: "{{ trigger.to_state.attributes.subject }}"
          message: >-
            {{ trigger.to_state.attributes.message }}
            Ausgelöst: {{ trigger.to_state.attributes.source_names | join(', ') }}
            {{ trigger.to_state.attributes.link }}
```

### 5.4 Dashboard / Rückschau (ohne Event-Entität)

```yaml
{{ state_attr('sensor.loom_security_last_alarm','subject') }}
{{ state_attr('sensor.loom_security_last_alarm','message') }}
{{ state_attr('sensor.loom_security_zone_eg','by_class').smoke | join(', ') }}
```

### 5.5 Warum `event` und nicht `sensor` als Primitiv

Ein Zähl-Sensor löst nur bei **State**-Wechsel aus: zwei aufeinanderfolgende Vorfälle mit derselben Melderanzahl erzeugen **keinen** Trigger. Die `event`-Entität hat als State den Feuerzeitstempel und feuert immer. Zwingende Randbedingungen (im Publisher fest verdrahtet und contract-getestet):

* **nicht retained** — HA ignoriert retained Payloads auf Event-Topics vollständig,
* **kein `value_template`** — ein Skalar zerstört das JSON-Parsing,
* **kein `device_class`** — HA-`event` kennt nur `doorbell`/`button`/`motion`,
* jeder gesendete `event_type` **muss** in der angekündigten `event_types`-Liste stehen (gemeinsame Go-Konstante für Discovery und Emitter),
* auf `OnBrokerConnect` werden **nur** die retained Aggregate neu geschrieben, **niemals** Events erneut gefeuert. (Der bestehende Alarm-Publisher säht seine gesamte Ebene bei jedem Reconnect neu — `internal/north/mqtt/alarm_publisher.go:288-290`; das ist eine reale, keine theoretische Falle.)

---

## 6. Safety-Sensoren

### 6.1 Erfassung

Der Klassifikator-Index wird aus dem Gerätemodell aufgebaut (nicht pro Wertänderung, §2.2 Korrektur 3) und liefert für jeden qualifizierten Datenpunkt `{Klasse, Severity, ActiveValues, sicherheitsrelevant}`.

**Neue `hmenum.Parameter`-Konstanten** (heute nur rohe Strings, `pkg/hmenum/parameter.go`): `ALARMSTATE`, `MOISTURE_DETECTED`, `WATERLEVEL_DETECTED`, `ERROR_CODE`, `ERROR_SMOKE_CHAMBER`, `ERROR_ALARM_TEST`, `ERROR_NON_FLAT_POSITIONING`.

Die folgende Tabelle ist gegen `tests/integration/testdata/model_snapshot_openccu-loom.json`
verifiziert (Kanaltyp, Datentyp und vollständige `VALUE_LIST` je Parameter):

| Gerät | Kanaltyp | Parameter | Typ / Werteliste | Klasse | Aktiv-Semantik |
|---|---|---|---|---|---|
| HmIP-SWSD | `SMOKE_DETECTOR` | berechneter `SMOKE_ALARM` | BOOL | `smoke` | bool — **nicht** der rohe `SMOKE_DETECTOR_ALARM_STATUS` |
| HmIP-SWSD | `SMOKE_DETECTOR` | `SMOKE_DETECTOR_ALARM_STATUS` | ENUM `[IDLE_OFF, PRIMARY_ALARM, INTRUSION_ALARM, SECONDARY_ALARM]` | `smoke` | `ActiveValues = {PRIMARY_ALARM, SECONDARY_ALARM}` — **`INTRUSION_ALARM` ist ausgeschlossen** (siehe unten) |
| HM-Sec-SD-2 | `SMOKE_DETECTOR` | `STATE` | BOOL | `smoke` | bool |
| HmIP-SWD | `WATER_DETECTION_TRANSMITTER` | `ALARMSTATE`, `MOISTURE_DETECTED`, `WATERLEVEL_DETECTED` | 3 × BOOL | `water` | drei Quellen, ODER im Aggregat. Die HA-`device_class`-Regeln existieren bereits (`safety`/`moisture`/`moisture`) — an den Einzelentitäten ändert sich **nichts**, sie werden nur zusätzlich aggregiert |
| HM-Sec-WDS | `WATERDETECTIONSENSOR` | `STATE` | ENUM `[DRY, WET, WATER]` | `water` | `ActiveValues = {WET, WATER}` |
| beliebig | — | `SABOTAGE*` | BOOL | `tamper` | bool |
| beliebig | — | `LOW_BAT`, `LOWBAT`, `LOWBAT_SENSOR` | BOOL | `battery` | bool |
| beliebig | — | `UNREACH`, `STICKY_UNREACH`, `BLOCKED_*`, `ERROR_*` | BOOL | `technical` | bool + `reason` |

**Warum `INTRUSION_ALARM` ausgeschlossen ist — der konkrete Rückkopplungsfall.**
`INTRUSION_ALARM` steht auf **Index 2** der Werteliste. Unter der heutigen Regel
`paramValueActive` (`internal/alarm/inputs.go:238-249`, „aktiv ⇔ `index != 0`") gilt der
Wert also als Auslösung. Er bedeutet aber das Gegenteil einer Rauchentwicklung: die Anlage
hat den Rauchmelder als **Sirene** für einen Einbruchalarm angesteuert. Ohne Ausschluss
meldet die Rauchklasse den eigenen Sirenenbefehl als Brand — und ein Automationsziel weckt
das Haus wegen Feuer, während tatsächlich ein Fensterkontakt ausgelöst hat.

Dieselbe Regel begründet die Rain-Abgrenzung: `RAINDETECTOR`/`RAIN_DETECTION_TRANSMITTER`
mit `STATE` BOOL `[DRY, RAIN]` sind **Wetter**, nicht `water` — Regen ist kein Leckagefall
und wird nicht klassifiziert.

**Gas/CO:** Klassen sind modelliert und vollständig verdrahtet, haben aber im Homematic-Bestand keinen Producer (`QuantityGas` ist Zählersemantik in m³). Die Entitäten werden erst publiziert, wenn eine Quelle klassifiziert ist — per CUxD-Anbindung oder Operator-Zuordnung über `security_sources`. Das ist ehrlicher als ein dauerhaft OFF stehender „Gasalarm".

### 6.2 Tote Schlüssel, die Abdeckung vortäuschen — werden entfernt

* `pkg/hmui/quantity.go:137` — `WATER_DETECT` passt auf keinen realen Parameter.
* `assets/ui/src/lib/sensor-actor/primary.ts:26-30` — `WATER_DETECTOR`/`RAIN_DETECTOR` passen auf keinen realen Kanaltyp (real: `WATER_DETECTION_TRANSMITTER`, `WATERDETECTIONSENSOR`, `RAINDETECTOR`).
* `internal/north/mqtt/entity_description_rules_binary_sensors.go:64-68` — `{HmIP-SWD, STATE} → window`: HmIP-SWD hat gar keinen `STATE`-Parameter, und die Regel würde den Wassermelder als Fensterkontakt etikettieren.
* `internal/model/custom/siren/smoke.go:26` — `SmokeStatusIdleOn = "IDLE_ON"`, ein Label, das kein HmIP-SWSD-Paramset je emittiert.

### 6.3 Verhältnis zum Alarm-Engine-Trigger — zwei getrennte Rollen

| Rolle | Wirkung | Konfiguration |
|---|---|---|
| **Beobachtet (informativ)** | Speist Klassenaggregate, Meldungen, Störungsbestand. **Kein** Scharfschaltbezug, **keine** Sirene, **kein** Incident. Funktioniert auch bei `alarm.enabled: false`. | automatisch über den Klassifikator |
| **Ausgelöst (24h / always-on)** | Öffnet einen Incident, fährt die Zone auf `triggered`, schaltet `HazardOutputs`, unterbricht den Zustandsautomaten und kehrt danach zurück (`internal/alarm/engine/engine.go:1403-1445`) | ausschließlich über `alarm_sensors` mit `sensor_type = hazard` + `config.always_on` |

Beide Rollen sind unabhängig: ein Rauchmelder kann beobachtet sein, ohne 24h-Auslöser zu sein — genau der Fall „Wassersensoren und Rauchmelder, die bisher nicht abgedeckt sind".

### 6.4 Drei Sicherheitsfixes im Enrollment-Pfad

1. **`GET /api/v1/security/sources`** ist zugleich der fehlende Sensor-Kandidatenendpunkt. Heute gibt es `OutputCandidates` und `RemoteKeyCandidates` (`internal/alarm/candidates.go:130-260`), aber **keinen** für Sensoren — Enrollment ist unvalidierter Freitext. Die Liste empfiehlt für Rauchmelder den berechneten `SMOKE_ALARM` und stuft den rohen ENUM ab.
2. **`SensorConfig.ActiveValues []string`** macht `paramValueActive` (`internal/alarm/inputs.go:238-249`) ENUM-fähig. **Leer = exakt heutiges Verhalten** (`index != 0`), also keine stille Bedeutungsänderung bestehender Enrollments. Ein einmaliger Startup-Log meldet jedes Enrollment, bei dem der Klassifikator dem eingetragenen Parameter widerspricht — sichtbar gemacht, **nicht** automatisch umgeschrieben.
3. **Serverseitige Kopplung `sensor_type = hazard ⇒ always_on`** in `internal/north/rest/handlers/alarm_config.go:246-275` (heute wird nur der Enum-Wert und das Parsen der Config geprüft). Ohne die Kopplung fällt ein Gefahrenmelder mit leerer Modusliste in den Zustandsautomaten und feuert nie.

---

## 7. Nordbound

### 7.1 MQTT

Neuer daemon-level Baum (dritter nach `bridge/*` und `alarm/*`; braucht eine kurze ADR als Erweiterung von ADR 0052):

```
<base>/security/state                  retained  cfg.QoS.State
<base>/security/alarm                  retained
<base>/security/problem                retained
<base>/security/health                 retained
<base>/security/test_mode              retained
<base>/security/last_alarm             retained
<base>/security/last_fault             retained
<base>/security/event                  NICHT retained, QoS0
<base>/security/fault                  NICHT retained, QoS0
<base>/security/class/<class>          retained
<base>/security/zone/<slug>            retained
<base>/security/zone/<slug>/readiness  retained
<base>/security/availability           retained  QoS1
```

* Discovery `node_id: "security"`, `object_id == unique_id`.
* Zweistufige Availability (Bridge-LWT + `<base>/security/availability`, `availability_mode: all`) — dasselbe Muster wie die Alarmebene.
* `internal/north/mqtt/retain_cleanup.go` bekommt Matcher für **beide** daemon-level Bäume (`security/` **und** `alarm/`) — die Datei enthält heute kein einziges `alarm`-Token (verifiziert), verwaiste retained Alarm-Topics sind also aktuell unerreichbar.
* Pro Zone ein `retractZone`-Pfad analog zu `retractPanel` (`internal/north/mqtt/alarm_publisher.go:411-417`), ausgelöst über `AlarmPanelChangedEvent.Removed`.
* **Keine** MQTT-Device-Trigger (`device_automation`) in v1 — im Repo nirgends implementiert, und ohne Dashboard-Fläche kein Mehrwert gegenüber der `event`-Entität. Kandidat für v2.

### 7.2 REST (`internal/north/rest/router.go`, spec-first in `assets/openapi.yaml`)

| Route | Schutz | Zweck |
|---|---|---|
| `GET /api/v1/security` | viewer | Systemschnappschuss (Severity, Klassen, Zonen, Zähler) |
| `GET /api/v1/security/classes/{class}` | viewer | Klassendetail mit vollständiger Quellenliste |
| `GET /api/v1/security/zones/{zoneID}` | viewer | Zonendetail inkl. `by_class`, offen, gebypasst, Readiness mit Grund |
| `GET /api/v1/security/sources` | viewer | klassifiziertes Inventar, Filter `class/zone/central/state/enrolled` — **zugleich Sensor-Kandidaten** |
| `PUT /api/v1/security/sources/{ref}` | operator | Klassen-Override / Ein-/Ausschluss |
| `GET /api/v1/security/faults` | viewer | offener Störungsbestand |
| `POST /api/v1/security/faults/{id}/acknowledge` | operator | Quittierung |
| `GET /api/v1/security/notifications?limit=` | viewer | Meldungsverlauf |
| `POST /api/v1/security/notifications/test` | operator | Testmeldung durch alle Ebenen — rein daemon-intern, **kein** CCU-Write |
| `GET /api/v1/alarm/incidents`, `GET /api/v1/alarm/incidents/{id}` | viewer | verdrahtet endlich `AlarmIncidentStore.ListByZone` (0 Produktionsaufrufer) inkl. `sources[]` |
| `GET /api/v1/alarm/sensor-candidates` | viewer | Alias auf `/security/sources?enrollable=true` |

`APIVersion` in `internal/north/rest/handlers/info.go`: **`3.14.0` → `3.15.0`** (rein additiv, verifiziert).

### 7.3 WebSocket (`assets/wsapi.json`)

* Broadcasts auf dem flachen Topic `security`: `security.state_changed`, `security.class_changed`, `security.zone_changed`, `security.fault_changed`, `security.notification`.
* Commands (Kategorie `security`): `security.state`, `security.sources`, `security.faults`, `security.acknowledge_fault`, `security.notifications`.
* **Einheitliches Vokabular:** hmevent-Tag **und** WS-Topic heißen beide `security.*`. Die bestehende Alarm-Divergenz (`alarm_panel.*` im hmevent vs. `alarm.*` auf WS) wird **nicht** angefasst — eine Vereinheitlichung würde jede konfigurierte `north.webhook.events`-Allow-List still entschärfen.
* `tests/contract/alarm_ws_payload_parity_test.go` wird auf die Security-Payloads erweitert **und** um das heute ungeschützte `AlarmNotificationPayload` ergänzt.

### 7.4 Webhook (`internal/north/webhook/outbound.go`)

Neue Tags `security.notification`, `security.state_changed`, `security.fault_changed` unter einem verschachtelten `security`-Objekt im bestehenden versionierten Umschlag — erstmals mit `subject`, `message`, `severity` und `sources[]`. Der bestehende `alarm`-Block bleibt unverändert, erhält aber additiv `sources[]` (siehe §8).

### 7.5 Metriken (`internal/metrics/security_collector.go`, NEU)

`security_notifications_total{class,verb,severity}`, `security_faults_open{class,reason}`, `security_class_active{class}`, `security_sources_total{class}`. Die vier bestehenden ungelabelten Alarm-Zähler (`internal/metrics/alarm_collector.go:42-48`) bleiben unangetastet — ein Relabeling bräche jede vorhandene Grafana-Abfrage.

### 7.6 Duress und stille Panik

`AlarmDuressEvent` wird heute bewusst nicht auf WebSocket gesendet (`pkg/hmevent/alarm.go:56-62`), und der Journaleintrag wird `Hidden` geschrieben (`internal/alarm/journal/journal.go:52-53`) — dokumentiertes Bedrohungsmodell: ein Bildschirmbeobachter darf nicht erfahren, dass verdeckt ausgelöst wurde.

**Entschieden (§10.1):** Die Reichweite wird über `alarm.duress.visibility` konfigurierbar (`hidden` / **`notify_only`** / `full`, Default `notify_only`). Begründung und vollständige Matrix in §10.1.

**Unverändert gilt:** Der Service abonniert eine **explizite Allow-Liste** von Ereignistypen, nie ein Pauschalabonnement — die Stufe entscheidet über die Fläche, nicht ein Zufall der Verdrahtung. Ein Contract-Test pro Stufe pinnt die Matrix; unterhalb von `full` trägt kein Security-Payload je ein Duress- oder Silent-Panic-Verb auf einer retained oder WS-Fläche.

### 7.7 Matter — nichts in v1, begründet

* matter.js HEAD hat **keinen** Intrusion-/Panel-Cluster und kein Panel-Device-Type; die einzigen Alarm-Abstraktionen sind das appliance-scoped `AlarmBase`-Muster und Kamera-`ZoneManagement`.
* Die Topologie wird ausschließlich aus `[]*device.Device` gebaut (`internal/north/matter/endpoint/types.go:19-34`), ein synthetisches Aggregat ist strukturell nicht einfügbar.
* `BooleanStateServer` meldet `FeatureMap = 0` ohne `StateChange`-Event (`internal/north/matter/cluster/measurement/measurement.go:675,699-702`) — das blockiert die konforme Übernahme von WaterLeakDetector 0x0043 / WaterFreezeDetector 0x0041 / RainSensor 0x0044 (rev 2, StateChange = Konformität M), unabhängig von der bewussten Alexa-Entscheidung BD-Matter-LeakAsContactSensor.

**Was trotzdem als Folge-Slice möglich wird:** Der Klassifikator liefert erstmals eine Leck-Klassifikation, sodass `internal/model/generic/matter.go:90-107` (Kommentar: „Leak/moisture parameters are not classified yet") sie an `MatterMeasurementLeak` weiterreichen kann — heute toter Code. HmIP-SWD würde dann als ContactSensor/BooleanState gebridged, innerhalb der bestehenden By-Design-Entscheidung. Zusätzlich vorgemerkt (ADR, kein Code): `BooleanStateConfiguration 0x0080` als das richtige Matter-Zuhause für einen quittierbaren Safety-Alarm; und die fehlende PowerSource-0x11-Anforderung am SmokeCoAlarm-Endpoint.

### 7.8 SPA

Neue lazy-geladene Route `assets/ui/src/routes/security/` mit drei Ansichten:

* **SecurityOverview** — Severity-Badge, Klassenkacheln, aktive Auslöser, letzte Meldung (Subject/Message), Störungszähler.
* **SecuritySources** — klassifiziertes Inventar mit Filtern (Klasse / Zone / Zentrale / Zustand / enrolled), Badge „unklassifiziert" mit Ein-Klick-Setzer, Aktion „als 24h-Melder in Zone aufnehmen".
* **SecurityFaults** — offene Störungen mit Grund und Quittierung über `confirmStore.ask({destructive:true})`.

Pflicht nach Hausregeln: Nav-Eintrag in `assets/ui/src/lib/nav.ts`; jeder sichtbare String über `t(...)` mit **DE und EN** in `assets/ui/src/lib/i18n.ts` (inkl. Buttons, Toasts, Confirm, Empty/Error, Badges, `aria-label`, Dokumenttitel); alle vier Kombinationen `data-skin` (loom|ha) × light/dark; geteilte `LoadingState`/`EmptyState`/`ErrorState`; Ergebnisse über `toastStore`; Playwright-Baselines light+dark unter `assets/ui/tests/e2e/` (ausschließlich über das CI-Playwright-Docker-Image erzeugen). Querverweis aus `assets/ui/src/routes/alarm/AlarmOverview.svelte`.

Zusätzlich: die drei toten Nachschlageschlüssel aus §6.2 entfernen, und `assets/ui/src/lib/alarm/sensorCandidates.ts:46-47` auf den empfohlenen berechneten Parameter umstellen.

---

## 8. Breaking Changes & Migration

### 8.1 HA-`unique_id`-Stabilität — nichts verwaist

| Garantie | Umsetzung |
|---|---|
| Jede bestehende `alarm_control_panel`-Entität behält `unique_id` **und** `object_id` `openccu-loom_alarm_<zoneID>` | `routingkey.DaemonUniqueID` bekommt einen eingefrorenen Legacy-Zweig; Golden-Fixture-Contract-Test mit beiden Schreibweisen. Muss dieser Test je aktualisiert werden, ist das das Signal, dass Entitäten zu verwaisen drohen. |
| Die Gerätekarte `openccu-loom_alarm` bleibt **unverändert** (Identifier, Name, Manufacturer) | Die neue Domäne bekommt eine **eigene** Karte `openccu-loom_security`. Kein Flattern, keine Friendly-Name-Änderung an Bestandsentitäten. |
| `<base>/alarm/**` bleibt unverändert | Die neue Ebene lebt vollständig unter `<base>/security/**`, node_id `security`, Präfix `loom_security_*` — kollisionsfrei zu `loom_addon_update`, `loom_<serial10>_*` und `openccu-loom_alarm_*`. |
| `<base>/alarm/<zone>/event` bekommt **kein** nachträgliches Discovery-Entity | Zwei Discovery-Quellen für dieselbe Semantik wären schlimmer als eine. ADR 0052 hat die Topic-Form dieser Ebene festgeschrieben. |

### 8.2 Additive Draht-Änderungen (kein Break)

* `hmevent.AlarmTriggeredEvent` und `hmevent.AlarmNotificationEvent` erhalten `Sources []SecuritySourceRef`; `SensorID`/`SensorName` bleiben befüllt.
* `hmevent.AlarmModeReadiness` erhält `BlockerDetails []{SensorID,Name,Ref,Reason}`; `Blockers []string` bleibt, dokumentiert als deprecated (Entfernung frühestens 4.0.0).
* `ZoneSnapshot` erhält `OpenSensors`, `Faults`, `TriggeredSources`.
* `incidentCause` erhält `sensor_type`, `interface_id`, `channel_address`, `parameter` — additives JSON in der bestehenden `cause_json`-Spalte; Altzeilen dekodieren mit leeren Feldern, **keine** Datenmigration.
* `pkg/hmapi.AlarmZoneStatus` erhält `open_sensors[]`, `faults[]`, `blocker_details`.
* `PUT /api/v1/alarm/zones/{id}/sensors` erhält optionale Felder `active_values[]`, `security_class`.
* MQTT-`open_sensors[]` liefert künftig **einheitlich** aufgelöste Referenzen statt gemischter Identifikatorräume (heute Anzeigenamen bei TRIGGER, IDs bei FAILED_TO_ARM) — die Feldform bleibt ein String-Array, der Inhalt wird konsistent. Als Klarstellung im CHANGELOG.

### 8.3 Bewusste Bereinigungen

* Das tote Feld `delay_s` verschwindet aus dem MQTT-Alarm-Event-Payload. Es wird von keinem Producer je gesetzt und ist auf dem Draht immer abwesend; `docs/mqtt-topic-schema.md:169-181` dokumentiert es fälschlich als vorhanden. Wire-Impact: null. Ebenso wird das dort versprochene, nie existierende `INVALID_CODE`/`DURESS`-Vokabular richtiggestellt.
* `SmokeStatusIdleOn` (Phantomlabel) wird entfernt.

### 8.4 Sichtbare Nebenwirkungen mit Gate

* **Un-Ignore der Safety-`ERROR_*`-Familien** (`internal/store/visibility/rules.go`) erzeugt neue HA-Entitäten auf bestehenden Geräten. → `entity_category: diagnostic` **und** `enabled_by_default: false`. Der Störungsplane konsumiert sie intern unabhängig vom HA-Enable-Status, damit `security/class/technical` sofort funktioniert. (→ offene Entscheidung 5)
* **Zonen-Slug** (Migration 034): Backfill aus dem Zonennamen via `routingkey.HubSlug` mit Kollisionssuffix. Danach **eingefroren** — eine Umbenennung der Zone ändert den Slug nicht. (→ offene Entscheidung 2)

### 8.5 Kompatibilität mit `alarm.enabled: false`

Der Security-Dienst startet unabhängig vom Alarmdienst. Ohne Engine publizieren `state`, `alarm`, `problem`, `health`, alle Gefahren-/Störungsklassen, `last_alarm`, `last_fault` und beide Event-Entitäten weiterhin; `intrusion`/`panic` melden leer, Zonenentitäten existieren nicht. Das ist die zentrale Anforderung („Weiterverwendung in HA, unabhängig vom ACP") und wird contract-getestet.

### 8.6 Version & Begleitartefakte

`internal/build/version.go` 0.52.12 → **0.53.0**; `APIVersion` 3.14.0 → **3.15.0**; Root-`CHANGELOG.md`; **beide** HA-Add-on-Changelogs und **beide** `config.yaml` (`packaging/ha-addon/openccu-loom/`, `packaging/ha-addon/openccu-loom-remote/`); Node-RED-Contrib-API-Pin (spec/-Snapshots + api-surface-Test) im selben Zug synchronisieren.

---

## 9. Umsetzungsplan

Sieben eigenständig mergebare Slices. Jede Slice ist für sich grün, jede hat eine eigene Testpflicht. Reihenfolge ist bindend: 1 → 2 → 3 → (4 ∥ 5) → 6 → 7.

### Slice 1 — Taxonomie & Klassifikator (≈ 4 Tage, M)

`pkg/hmenum/security.go`, neue `hmenum.Parameter`-Konstanten, `internal/model/safety/classify.go`, Ausschlussliste, Entfernen der vier toten Schlüssel (§6.2), Korrektur der inerten `{HmIP-SWD, STATE}`-Regel.
**Tests:** Contract-Test der Klassifikationstabelle gegen die vollständige godevccu-Flotte (jeder Treffer mit Modell + Kanaltyp belegt, jeder Nicht-Treffer begründet); Contract-Test „kein Aktor-Rückkopplungsparameter ist klassifiziert"; Unit-Tests für `ActiveValues`-Auflösung bei ENUMs.

### Slice 2 — Engine-Quellenledger & Readiness-Gründe (≈ 4 Tage, M)

`SecuritySourceRef`; additive Felder auf `AlarmTriggeredEvent` **und** `AlarmNotificationEvent`; `incidentCause`-Erweiterung; Migration `033_alarm_incident_sources.sql`; `BlockerDetails`; `ZoneSnapshot` + offene/gestörte Sensoren; `GET /alarm/incidents`; Verdrahtung von `PurgeClosedBefore` an die bestehende Journal-Retention; Korrektur des `open_sensors[]`-Identifikatorbruchs.
**Korrigiert bei der Umsetzung:** Der Schreibpfad ist **synchron**, nicht asynchron. Die Annahme „Vorbild Journal" traf nicht zu — `internal/alarm/journal/journal.go` schreibt selbst synchron auf der Engine-Goroutine, und `trigger()` persistiert den Incident laut eigenem Kommentar bewusst **vor** dem Auslösen der Ausgänge, damit ein Absturz nur über- statt unterzählen kann. Ein asynchroner Ledger-Schreibpfad hätte die Platte also nicht vom Auslösepfad genommen — Journal und Incident liegen weiterhin darauf — sondern nur den Nachweis lückenhafter gemacht als die Zähler, die er erklärt. Der Ledger-Insert gehört in dieselbe Safety-First-Phase.
**Tests:** Test-first — der fehlschlagende Reproduzierer für den Rauch-Rückkopplungsfall zuerst; Unit-Tests der Engine für Folgeauslösungen im laufenden Incident; Contract-Test, dass `AlarmNotificationEvent` Sensoridentität trägt; Golden-Update für Journal/Incident-Serialisierung.

### Slice 3 — ENUM-Aktivwerte & Sensor-Kandidaten (≈ 3 Tage, M)

`SensorConfig.ActiveValues` (leer = heutiges Verhalten), ENUM-fähiges `paramValueActive` über den vorgebauten Index, `GET /alarm/sensor-candidates`, serverseitige `hazard ⇒ always_on`-Kopplung, Startup-Log für widersprüchliche Enrollments, SPA-Vorbelegung auf `SMOKE_ALARM`.
**Höchste Sicherheitsdichte pro Zeile — klein halten, separat reviewen.**
**Tests:** Unit-Tabellentest über alle ENUM-Formen inkl. Default-Erhaltung; Contract-Test „ein enrolltes rohes `SMOKE_DETECTOR_ALARM_STATUS` mit `ActiveValues={PRIMARY_ALARM,SECONDARY_ALARM}` reagiert nicht auf den eigenen `INTRUSION_ALARM`-Befehl"; Integrationstest des Walk-Test-Pfads.

### Slice 4 — Security-Kern (≈ 6 Tage, L)

`internal/security/**`, `internal/model/security/**`, Migration `034_security_domain.sql` (Zonen-Slug, `security_sources`, `security_faults`), Index-Aufbau + Attach/Detach-Hooks, Aggregator (eine Mutex, kohärenter Snapshot), Störungsentprellung, `render.go`, `i18n.Catalogs.TF`, Katalog-Namensraum `security.*` in EN **und** DE, REST + WS + `pkg/hmapi/security.go`, `routingkey.DaemonUniqueID`.
**Tests:** Unit-Tests des Aggregators inkl. Multi-CCU-Detach-Teardown; Contract-Test der i18n-Vollständigkeit (jede Klasse × Verb × Locale); Contract-Test „kein Duress-/Silent-Panic-Verb auf einer Security-Fläche"; WS-Payload-Parity-Guard erweitert (inkl. `AlarmNotificationPayload`); Wiring-Pin, dass Service **und** Observer im Daemon tatsächlich angehängt sind (genau diese Fehlerklasse war der Hub-Notifier-Bug in 0.52.12).

### Slice 5 — MQTT, Webhook, Metriken (≈ 4 Tage, M)

`security_discovery.go`, `security_publisher.go`, Topic-Builder, bedingte Klassen-Discovery, `retractZone`, Retain-Cleanup-Matcher für **beide** daemon-level Bäume, Webhook-Verschachtelung, Collector, ADR (Erweiterung von ADR 0052), `docs/mqtt-topic-schema.md`.
**Tests:** Golden-Fixtures für jede Discovery-Nutzlast; Contract-Test „Event-Topics nie retained, kein `value_template`, kein `device_class`, jeder emittierte `event_type` ist angekündigt"; Integrationstest mit Mosquitto: Broker-Bounce während eines offenen Incidents darf **kein** Event replayen, muss aber alle retained Aggregate neu setzen; Contract-Test, dass `alarm_discovery.go` und der Security-Publisher denselben Ableitungshelfer verwenden.

### Slice 6 — SPA (≈ 4 Tage, M)

Drei Ansichten, Nav-Eintrag, vollständige DE/EN-Katalogeinträge, alle vier Skin×Modus-Kombinationen, Querverweis aus der Alarmübersicht, Entfernen der toten SPA-Schlüssel.
**Tests:** vitest-Komponententests; Playwright-E2E für Navigation/Titel/Skip-Link, Loading/Empty/Error, Toast nach Quittierung, Confirm-Dialog; Visual-Baselines light + dark je Ansicht (ausschließlich über das CI-Playwright-Docker-Image).

### Slice 7 — Optional / Folge (≈ 2–3 Tage, S–M)

Un-Ignore der Safety-`ERROR_*`-Familien; Matter-Leak-Klassifikation in `internal/model/generic/matter.go`; optionaler berechneter `WATER_ALARM`-Datenpunkt pro Gerät für HA; ADR-Notiz zu `BooleanStateConfiguration 0x0080`.
**Tests:** Sichtbarkeits-Contract-Test pro un-ignoriertem Modell; Matter-Parity-Test für die Leck-Klassifikation.

**Gesamtaufwand:** ≈ 25–30 Entwicklertage inkl. Tests. Die Testarbeit (~35–40 %) ist der natürliche Delegationskandidat für Sonnet-Sub-Agenten (Tabellenfälle, Golden-Fixtures, Playwright-Baselines); nicht delegierbar sind die Duress-Ausschlusslogik, der Retained-vs-Nicht-Retained-Split im Publisher und die `ActiveValues`-Defaults.

---

## 10. Getroffene Entscheidungen

Alle fünf offenen Punkte sind entschieden. Vier folgen der Empfehlung, einer weicht begründet ab.

| # | Entscheidung | Wirkung |
|---|---|---|
| 1 | **Name: „Security & Safety" (EN) · „Sicherheit & Gefahrenmelder" (DE)**, technisches Token durchgängig `security` | wie §2.4. „Alarm" bleibt für Zonen/ACP reserviert. |
| 2 | **Zonen-Slug bei Anlage fixiert** — eine Umbenennung der Zone ändert ihn nicht | Migration 034 backfillt einmalig aus dem Zonennamen (`routingkey.HubSlug` + Kollisionssuffix), danach eingefroren. Eine explizite Operator-Aktion „technische ID ändern" mit Warndialog bleibt als Folge-Slice möglich. |
| 3 | **Störungsachse nur für sicherheitsrelevante Geräte** (Gate in §3.4) | Flottenweite Gesundheit bleibt bei `internal/health` und der Fleet-Ansicht. |
| 4 | **Duress/stille Panik: konfigurierbar über `alarm.duress.visibility`** — *abweichend von der Empfehlung „nur Webhook"* | siehe §10.1 |
| 5 | **Safety-`ERROR_*` jetzt un-ignorieren** (Slice 7), als `entity_category: diagnostic` **und** `enabled_by_default: false` | Der Störungsplane konsumiert sie intern unabhängig vom HA-Enable-Status. Für Bestandsinstallationen unsichtbar. |

### 10.1 `alarm.duress.visibility` — Begründung und Verhalten

**Warum die Abweichung richtig ist:** Die ursprüngliche Empfehlung „nur Webhook" hat einen blinden Fleck — in einer Installation ohne Webhook, die ausschließlich über Home Assistant benachrichtigt, bedeutet sie *gar keine* Duress-Benachrichtigung. Eine Sicherheitsfunktion, die stumm ins Leere läuft, ist schlechter als eine sichtbare. Das Bedrohungsmodell (ein Beobachter am Bildschirm darf die verdeckte Auslösung nicht bemerken) hängt an der Installation — Wandtablet im Flur gegenüber reiner Handy-Nutzung — und ist damit eine Betreiber-, keine Produktentscheidung.

**Neues Config-Feld** `alarm.duress.visibility`, `cfg:"basic"`, Default **`notify_only`**:

| Stufe | Webhook + `<base>/alarm/<zone>/event` | HA-`event`-Entität | retained `last_alarm` | SPA / WebSocket |
|---|---|---|---|---|
| `hidden` | ✅ | — | — | — |
| **`notify_only`** (Default) | ✅ | ✅ | **—** | — |
| `full` | ✅ | ✅ | ✅ | ✅ |

`hidden` reproduziert das heutige Verhalten exakt. Der Kniff bei `notify_only` ist das Auslassen des **retained** `last_alarm`: die Meldung erreicht das Handy, bleibt aber flüchtig und steht nicht dauerhaft in einem Dashboard-Attribut. Nur `full` hebt zusätzlich die bestehende WebSocket-Unterdrückung (`pkg/hmevent/alarm.go:56-62`) und den `Hidden`-Journaleintrag (`internal/alarm/journal/journal.go:52-53`) auf.

Gilt gleichermaßen für den Duress-Code und für `SensorConfig.PanicSilent`.

**Pflichten:**
* Label + Hilfetext in **beiden** Locales (`config.field.alarm.duress.visibility`, `config.help.…`) — sonst schlägt `TestConfigFieldsHaveLabelsAndHelp` fehl.
* Ein Contract-Test **pro Stufe**, der die Matrix oben pinnt — insbesondere, dass `notify_only` niemals ein retained Topic beschreibt und `hidden`/`notify_only` niemals auf WS senden.
* Der Hilfetext benennt die Grenze ausdrücklich: **ob Home Assistant die Benachrichtigung als Banner auf dem Sperrbildschirm anzeigt, kann Loom nicht steuern.** Wer das absichern will, richtet in HA einen eigenen Notify-Kanal ohne Vorschau ein.
* ADR-Notiz zur Erweiterung des dokumentierten Duress-Bedrohungsmodells (die bisherige Zusage „geht nie auf WebSocket" wird unter `full` aufgehoben) + CHANGELOG-Hinweis.
* Aufwand: **+1 Tag** in Slice 4 (Kern + Config + Tests), zusätzlich zu den ≈ 25–30 Tagen.