# Un-Ignore UI Konzept

## Hintergrund

Die OpenCCU-Loom-Backend-Implementierung der `un_ignore`-Mechanik ist
**vollständig vorhanden**, eine User-facing Steuerung (Config + REST +
UI) fehlt. Aufgabe dieses Dokuments: Skizze der UI + dünner
Wire-Up-Layer, der die Backend-Bausteine vom HMIP-Frontend her erreichbar
macht.

## Bestandsaufnahme — Backend

| Baustein                                 | Datei                                                       | Status   |
| ---------------------------------------- | ----------------------------------------------------------- | -------- |
| Per-DP Markierung `MarkUnIgnored/IsUnIgnored` | `internal/model/datapoint/base.go:271-287`                  | da       |
| Visibility-Decider                       | `internal/store/visibility/decider.go`                      | da       |
| Parser für `MODEL:CHANNEL:PARAMETER`     | `internal/store/visibility/parser.go`                       | da       |
| Materializer-Berücksichtigung            | `internal/model/custom/materialize.go:496+`                 | da       |
| Pipeline-Anwendung pro Interface         | `internal/central/adapter/device_pipeline.go:443-454`       | da       |
| QueryFacade-Kandidatenliste              | `internal/central/queryfacade.go:329 GetUnIgnoreCandidates` | da       |
| Visibility-Registry-API `LoadUnIgnore`   | `internal/store/visibility/registry.go:47`                  | da       |
| **Config-Knob (YAML)**                   | `internal/config/`                                          | **fehlt** |
| **SQLite-Persistenz**                    | `internal/store/sqlite/`                                    | **fehlt** |
| **REST-Endpunkt**                        | `internal/north/rest/handlers/`                             | **fehlt** |
| **Svelte-Screen**                        | `assets/ui/src/routes/`                                     | **fehlt** |

Die fünf Punkte oberhalb der Trennlinie sind das Backend; die vier
Punkte darunter sind diese Konzept-Lieferung.

## Bestandsaufnahme — Python-Referenz

`homematicip_local` (HA-Integration) führt den User durch den
`Advanced Configuration`-Schritt im Config-Flow:

1. Settings → Devices & Services → Homematic(IP) Local → Configure
2. Tab "Interface" → "Advanced configuration" aktivieren
3. Multi-Select-Dropdown mit allen Kandidaten (aus
   `query_facade.get_un_ignore_candidates`)
4. Auswahl wird als `CONF_UN_IGNORES` im HA-ConfigEntry persistiert
5. `control_unit.py` zieht die Liste beim Reload, leitet sie via
   `config_builder.with_un_ignore_list(...)` → `Registry.LoadUnIgnore`
   weiter
6. Integration wird automatisch reloaded

Format der einzelnen Einträge:

```
DEVICE_TYPE:CHANNEL:PARAMETER
```

mit `*`-Wildcards für DEVICE_TYPE bzw. CHANNEL. Beispiele:

| Pattern                 | Effekt                                       |
| ----------------------- | -------------------------------------------- |
| `HmIP-eTRV-2:0:LOW_BAT` | LOW_BAT auf Kanal 0 sichtbar machen          |
| `*:*:RSSI_PEER`         | RSSI_PEER auf allen Kanälen aller Geräte     |
| `*:0:OPERATING_VOLTAGE` | OPERATING_VOLTAGE auf Kanal 0 aller Geräte   |
| `LOW_BAT`               | Short-Form: alle Geräte, alle Kanäle         |

## UI-Konzept (Svelte 5 SPA)

### Einbettung

Neuer Route-Eintrag `/settings/unignore` als Unterseite der bestehenden
`Settings`-View, NICHT als top-level Sidebar-Punkt — die Funktion ist
ein Power-User-Werkzeug und steht im Settings-Drawer neben Backups,
Audit-Log, Diagnostics.

```
Settings
├── General
├── CCUs
├── MQTT
├── Matter
├── Authentication
└── Un-Ignore Parameters          ← neu
```

### View-Struktur — `UnIgnoreList.svelte`

Vorlage ist `assets/ui/src/routes/matter/MatterExposureList.svelte`
(507 Zeilen) — gleiche Form: Multi-Select über
Backend-Kandidatenliste, Bulk-Enable/Disable, Suche, Persistenz via
REST PUT. Wiederverwertbare Bausteine: `$lib/components/ui/{Button,
Input}`, `$lib/i18n`, `$lib/stores/toast.svelte`.

```
┌────────────────────────────────────────────────────────────────┐
│  Un-Ignore Parameters                                          │
│                                                                │
│  Hidden parameters that should be surfaced as data points.     │
│  Use at your own risk — excessive writes to MASTER paramset    │
│  values can damage devices.                                    │
│                                                                │
│  ☐ Include MASTER parameters (off by default)                  │
│                                                                │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ Search… 🔍                                               │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                │
│  Filter by device model: [HmIP-eTRV-2 (12)] [HmIP-SWDO (8)]    │
│                          [* (wildcards) (4)] [Clear]           │
│                                                                │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ ☑ ID │ Pattern                       │ Description       │  │
│  ├──────┼───────────────────────────────┼───────────────────┤  │
│  │ ☑    │ HmIP-eTRV-2:0:LOW_BAT         │ Low battery (HmIP …│  │
│  │ ☐    │ HmIP-SWDO:1:ERROR             │ Sensor error      │  │
│  │ ☑    │ *:*:RSSI_PEER                 │ Signal strength … │  │
│  │ ☐    │ *:0:OPERATING_VOLTAGE         │ Operating voltage │  │
│  │ …                                                        │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                │
│  [Add custom pattern…]   [Save]   [Discard]                    │
│                                                                │
│  ⚠ 3 changes pending — devices will be re-materialised on Save │
└────────────────────────────────────────────────────────────────┘
```

### Datenfluss

1. **Mount** → `GET /api/v1/visibility/unignore/candidates?include_master=false`
   liefert sortierte Kandidatenliste der unsichtbaren Parameter
   (Backend-Endpunkt nutzt `QueryFacade.GetUnIgnoreCandidates`).
2. **Mount** → `GET /api/v1/visibility/unignore` liefert aktuelle
   aktivierte Liste (aus SQLite-Persistenz).
3. **Search/Filter/Toggle** → lokale Mutation eines `$state<Set<string>>`.
4. **Add custom pattern** → öffnet Modal mit `<input>` für Free-Form-Eingabe;
   client-side Validation gegen `^[A-Za-z0-9\-_*]+:[0-9*]+:[A-Z_]+$`,
   inline-Hinweis falls Pattern keinen Kandidaten matched.
5. **Save** → `PUT /api/v1/visibility/unignore` mit komplettem
   Listenzustand; Server validiert via `ParseUnIgnoreLine`, persistiert
   in SQLite, ruft `Registry.LoadUnIgnore` neu auf und triggert
   Materializer-Re-Run pro Central.
6. Response enthält `applied_count`, `parse_errors[]`,
   `affected_devices`. Toast-Nachricht zeigt das Ergebnis.
7. **Discard** → State auf serverseitigen Wert zurücksetzen.

### Interaktionsdetails

- **Bestätigungsdialog** vor "Save" wenn `include_master=true` oder
  wenn das Diff MASTER-Pattern entfernt/hinzufügt: zeigt Anzahl der
  betroffenen Devices + Re-Materialize-Hinweis. Apple-Home / MQTT /
  REST haben offene Subscriptions, die nach dem Re-Run ein
  Re-Discovery sehen.
- **Bulk-Toggle** über Header-Checkbox: aktiviert/deaktiviert alle
  gefilterten Zeilen.
- **Optimistisches UI** — der Save-Roundtrip ist nicht-trivial
  (Materializer-Lauf), daher Spinner + disabled Save-Button bis
  Response da ist; bei Fehler Rollback auf vorherigen State.
- **Read-Only-Modus** für Viewer-Rolle: zeigt die Liste, "Save"
  ausgegraut.

### i18n-Keys

Neue Schlüssel in `internal/i18n/catalogs/{en,de}.json`:

```
unignore.title                = Un-Ignore Parameters
unignore.subtitle             = Hidden parameters that should be surfaced as data points.
unignore.warning              = Use at your own risk — excessive writes to MASTER paramset values can damage devices.
unignore.include_master       = Include MASTER parameters
unignore.search_placeholder   = Filter by device, channel or parameter…
unignore.add_pattern          = Add custom pattern…
unignore.save                 = Save
unignore.discard              = Discard
unignore.unsaved_changes      = {n} changes pending — devices will be re-materialised on Save
unignore.invalid_pattern      = Invalid pattern (expected MODEL:CHANNEL:PARAMETER)
unignore.no_candidates        = No hidden parameters available — all parameters already visible.
unignore.saved                = Un-ignore list updated. {n} parameters now visible.
```

## REST-Endpunkte

| Methode | Pfad                                          | Body                            | Status |
| ------- | --------------------------------------------- | ------------------------------- | ------ |
| GET     | `/api/v1/visibility/unignore`                 | —                               | neu    |
| PUT     | `/api/v1/visibility/unignore`                 | `{patterns: ["..."]}`           | neu    |
| GET     | `/api/v1/visibility/unignore/candidates`      | Query: `include_master=bool`    | neu    |

DTOs:

```yaml
UnIgnoreListResponse:
  type: object
  required: [patterns]
  properties:
    patterns: { type: array, items: { type: string } }
    pattern_count: { type: integer }
    updated_at: { type: string, format: date-time }

UnIgnoreUpdateRequest:
  type: object
  required: [patterns]
  properties:
    patterns: { type: array, items: { type: string } }

UnIgnoreUpdateResponse:
  type: object
  required: [applied_count, parse_errors, affected_devices]
  properties:
    applied_count: { type: integer }
    parse_errors:
      type: array
      items: { type: string, description: "human-readable parse error per offending line" }
    affected_devices: { type: integer }

UnIgnoreCandidateList:
  type: object
  required: [candidates]
  properties:
    candidates:
      type: array
      items: { type: string }
    include_master: { type: boolean }
```

Authorisierung: `admin` zum Schreiben, `operator`/`admin` zum Lesen,
`viewer` darf die Kandidatenliste sehen, aber nicht die aktive Liste
(Sicherheits-Defense-in-Depth: Viewer soll keine MASTER-Pfade kennen).

## Persistenz

Neue SQLite-Tabelle in einer goose-Migration
`internal/store/sqlite/migrations/00xx_visibility_unignore.sql`:

```sql
-- +goose Up
CREATE TABLE visibility_unignore (
    central_name TEXT NOT NULL,
    pattern      TEXT NOT NULL,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (central_name, pattern)
);

-- +goose Down
DROP TABLE visibility_unignore;
```

Die Tabelle wird pro `central_name` partitioniert (Multi-CCU-First-Class
per ADR 0002). Beim Daemon-Start wird die Liste pro Central gelesen
und via `Registry.LoadUnIgnore(strings.NewReader(strings.Join(patterns, "\n")))`
zurückgespielt.

## Config-Knob (YAML)

Bootstrap-Pfad: ein zentraler Default kann via `config.yaml` gesetzt
werden, dient als Erstbefüllung wenn die SQLite-Tabelle leer ist
(nicht als Override — Runtime-Änderungen via REST gewinnen).

```yaml
centrals:
  - name: OttoGo
    ...
    visibility:
      un_ignore:
        - "HmIP-eTRV-2:0:LOW_BAT"
        - "*:*:RSSI_PEER"
```

Mapped auf `config.CentralConfig.Visibility.UnIgnore []string`.

## Roll-out-Reihenfolge

1. **REST + Persistenz + Config-Knob** (`config.go`, neue Migration,
   `internal/north/rest/handlers/visibility.go`). Backend kann ohne UI
   bereits via `curl` bedient werden.
2. **Daemon-Wiring** im `cmd/openccu-loom/daemon.go`: nach Central-Init
   die SQLite-Liste laden + `Registry.LoadUnIgnore` aufrufen + Pipeline
   triggern.
3. **Svelte-Screen** `UnIgnoreList.svelte` + Settings-Sub-Route +
   i18n-Keys. Kann parallel zum REST-Schritt laufen sobald die
   OpenAPI-Spec steht (frontend-types werden generiert).
4. **Apple-Home / Matter-Bridge-Side-Effects** — wenn ein
   MASTER-Parameter via un-ignore zur Bridge sichtbar wird, muss
   `MatterEligibility` neu evaluiert werden. Die Materializer-Pipeline
   feuert bereits ein `DeviceCreatedEvent` re-emission, die Bridge
   hängt sich daran an. Smoke-Test vor Release.
5. **Doku** `docs/user-guide.md` Abschnitt "Un-Ignore Parameters",
   Verweis auf das Format + die Sicherheitswarnung.

## Test-Plan

- **Unit**: `parser_test.go` deckt das `MODEL:CHANNEL:PARAMETER`-Parsing
  schon ab. Neu: `handlers/visibility_test.go` mit PUT-Roundtrip +
  fehlerhaften Patterns.
- **Integration** (`-tags=integration`): per `godevccu` ein Device
  laden, `LOW_BAT` ist initial unsichtbar, REST-PUT macht es sichtbar,
  `GET /api/v1/devices/.../channels/0` enthält den Datenpunkt.
- **Contract**: neue Datei `tests/contract/unignore_visibility_test.go`
  pinnt die `IGNORE_FOR_UN_IGNORE_PARAMETERS`-Liste (Parameter, die
  niemals un-ignored werden dürfen — z. B. interne Service-Parameter)
  gegen die Python-Konstante.
- **Snapshot-Parity**: nach Implementierung
  `tests/integration/TestModelSnapshotDumpAgainstGodevccu` mit einer
  un-ignore-Liste auf beiden Seiten laufen lassen → Drift bleibt 0.

## Beantwortete Fragen (Stand 2026-05-14)

1. **MASTER-Reload-Hinweis** — entfällt. Operator-Feedback: MASTER-
   Paramset-Änderungen brauchen keinen Geräte-Neustart. UI bleibt
   konsistent zwischen VALUES und MASTER, kein Inline-Badge, kein
   Confirm-Dialog. Die `include_master`-Checkbox bleibt aus reinem
   Listen-Filter-Grund vorhanden (verhindert, dass die Default-
   Kandidatenliste mit MASTER-Parametern überlaufen wird).
2. **Export/Import** — nein. Liste lebt nur in SQLite; Sicherung
   erfolgt indirekt über den bestehenden Backup-Mechanismus
   (`var/backups/`). Spart einen REST-Endpunkt und einen Text-
   Format-Kompat-Pfad.
3. **Audit-Log-Integration** — ja, pro `PUT`-Roundtrip ein Audit-
   Event mit:
   - User (aus `reqctx`)
   - Diff: `added[]`, `removed[]` Patterns
   - `affected_devices` Counter
   - Quelle: `rest` (oder später `config_yaml`, wenn der Bootstrap-
     Knob die Liste setzt — diese Initiallast wird als ein einziges
     System-Event geloggt, nicht pro Pattern)

## Offene Fragen

— alle drei Konzept-Fragen sind beantwortet. Implementierung wartet
auf separates Go-Signal.
