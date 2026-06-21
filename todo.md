# TODO — offene Daemon-Implementierungen (north-bound Vertragslücken)

**Stand:** 2026-06-21
**Quelle:** Verifikation während der Integration des Python-Wire-Clients
(`py-openccu-loom-client`, der `aiohomematic` in `homematicip_local` ablöst).
Gegengeprüft am Daemon-Quellcode dieses Repos.

> **Status 2026-06-21:** **D1–D5 alle umgesetzt in 0.9.0** (APIVersion 1.19.0,
> siehe `CHANGELOG.md`). Die endgültige Fassung jedes Punkts gehört
> perspektivisch als neuer Ask nach `docs/external-clients/asks.md`. Die
> Einzelbefunde bleiben unten als Provenienz stehen, jeweils mit
> Umsetzungsnotiz.

## Verhältnis zu `docs/external-clients/asks.md`

`asks.md` ist der **kanonische** External-Client-Backlog (5 Wellen + 3 ADRs,
Stand 2026-05-24) und gilt dort als substanziell abgeschlossen — nur **F2**
(Cold-Start nach Cutover) ist bewusst offen. Insbesondere **H1 — Streaming
`/snapshot` (NDJSON)** ist als _Umgesetzt_ markiert und im Code vorhanden
(`internal/north/rest/handlers/snapshot.go`: `?include=channels,data_points`
+ `Accept: application/x-ndjson`).

Dieses Dokument sammelt **nur die danach neu entdeckten** Vertragslücken, die
asks.md (älter) noch nicht führt. Wenn ein Punkt hier landet, gehört seine
endgültige Fassung perspektivisch als neuer Ask in asks.md / einen ADR —
todo.md ist der Arbeitszettel davor.

---

## D1 — `hub.system_update` WebSocket-Broadcast fehlt (P1) ✅ UMGESETZT (0.9.0)

> **Umsetzung:** `hub.<central>.system_update`-Broadcast ergänzt, verdrahtet
> über `Update.OnUpdate` in `internal/north/rest/ws/hub_events.go`
> (`SystemUpdateTopic` + `HubSystemUpdateChangedPayload`). Client kann die
> 300-s-Reconcile-Schleife entfernen.

**Befund:** Von den sechs Hub-Singletons werden fünf als WS-Broadcast emittiert
(`alarm_messages`, `service_messages`, `inbox`, `metrics`, `connectivity` —
`internal/north/rest/ws/hub_events.go`). **`system_update` hat keinen Push** und
ist ausschließlich REST-abrufbar:

- `GET /system/update` → `handlers.GetSystemUpdate` (`internal/north/rest/handlers/system_hub.go:38`, geroutet `router.go:726`).
- Kein Topic in `internal/north/rest/ws/` (grep leer).

**Wirkung:** Externe Clients müssen den Firmware-/System-Update-Status pollen.
Der Python-Client backstoppt das aktuell mit einer bewusst groben 300-s-
Reconcile-Schleife — funktioniert, ist aber Polling statt Push.

**Vorschlag:** Einen `hub.<central>.system_update`-Broadcast ergänzen, analog zu
den fünf bestehenden Hub-Topics, ausgelöst beim Update-Status-Wechsel. Dann kann
der Client die Polling-Schleife entfernen.

**Priorität:** Höchste der offenen Punkte (einziger echter Push-Gap).

## D2 — `value_translations` (übersetzte ENUM-Anzeigewerte) nicht implementiert (P2) ✅ UMGESETZT (0.9.0)

> **Umsetzung:** Optionales `value_translations map[string]string` auf
> `DataPointSummary`, gespeist aus der OCCU-`parameter_values_<locale>`-Tabelle
> (`ChannelTypedValueLabel` auf dem Label-Adapter +
> `resolvedValueTranslations` in `devices.go`). Nur tatsächlich übersetzte
> Werte werden aufgenommen.

**Befund:** `DataPointSummary` liefert die rohe `value_list` aus dem CCU-
Descriptor (`internal/north/rest/handlers/devices.go:207-210`), aber **keine**
übersetzten Anzeige-Strings je Enum-Wert. Übersetzt wird bisher nur der
Parameter-_Name_ (`translated_name`/`parameter_label`), nicht die einzelnen
Werte.

**Wirkung:** Clients können ENUM-Datenpunkte nur mit den technischen
VALUE_LIST-Labels anzeigen; eine lokalisierte Wert-Darstellung (z. B. für HA-
Selects) ist nicht möglich.

**Vorschlag:** Ein optionales Feld (z. B. `value_translations: map[string]string`
oder paralleler `value_labels []string`) auf `DataPointSummary`, gespeist aus dem
i18n-Katalog (heute nur Web-UI-intern, `internal/i18n/`).

**Priorität:** Mittel — Komfort/Genauigkeit, kein Funktionsblocker.

## D3 — `ChannelSummary` ohne `functions`/`function`-Feld (P2) ✅ UMGESETZT (0.9.0)

> **Umsetzung:** `ChannelSummary.Functions []string` ergänzt, befüllt aus
> `ch.Functions` in `toChannelSummary()` (`devices.go`).

**Befund:** `DeviceSummary` serialisiert sowohl `rooms[]` als auch `functions[]`
(`devices.go:65-66`). `ChannelSummary` trägt zwar `room` (singular, `:139`),
aber **kein** `function(s)`-Feld — obwohl die Information intern existiert
(`internal/model/device/channel.go` `Channel.Functions`).

**Wirkung:** Eine Gewerke-Zuordnung auf Kanal-Ebene ist über den Wire nicht
abrufbar; Clients können Funktionen nur grob auf Geräteebene mappen.

**Vorschlag:** `ChannelSummary` um `function`/`functions` ergänzen, parallel zur
bestehenden `room`-Auflösung in `toChannelSummary()`.

**Priorität:** Mittel.

## D4 — OpenAPI: `SchemaField.default` nicht dokumentiert (P3, Doku-Fix) ✅ UMGESETZT (0.9.0)

> **Umsetzung:** `default` als optionales `nullable`-Property in `SchemaField`
> (`assets/openapi.yaml`) ergänzt.

**Befund:** Der Handler `GetConfigSchema` (`handlers/admin_config.go`) gibt pro
Feld auch `default any` aus, aber die OpenAPI-Definition `SchemaField`
(`assets/openapi.yaml:5634`, `required: [path, class, go_type, restart_required]`)
führt **kein** `default`-Property.

**Wirkung:** Ein strikter OpenAPI-Validator würde echte Handler-Responses
ablehnen; generierte Typen (z. B. `openccu-loom-types`) kennen das Feld nicht.
Der Top-Level-Vertrag `{sections, fields}` selbst stimmt — nur dieses eine Feld
fehlt in der Spec.

**Vorschlag:** `default` als optionales Property in `SchemaField` ergänzen
(Typ frei/`nullable`), dann Typen regenerieren.

**Priorität:** Niedrig — reiner Spec-/Doku-Abgleich, kein Laufzeitproblem.

## D5 — `set_color`: Saturation-Einheit am Wire klären (P3, VERIFY) ✅ UMGESETZT (0.9.0)

> **Umsetzung (über VERIFY hinaus — war ein echter Bug):** Audit gegen
> aiohomematic ergab, dass `ColorLight`/`EffectLight`/`RGBW` die rohe
> Wire-`0..1`-Saturation ins HA-kanonische `color.s`-Feld schrieben, während
> `FixedColorLight` und der `combined`-HS-DP bereits `0..100` lieferten — eine
> interne Inkonsistenz. Das gesamte `custom/light`-Paket spricht jetzt
> HA-kanonisch `0..100` (Read `Color`, Write `SetColor`, `FixedColorToHS`/
> `HSToFixedColor`, Matter-Saturation-Konversionen, `set_color`-Default `100`);
> der Wire-SATURATION-DP bleibt `0..1`. **Breaking:** externe Clients müssen
> `set_color`-Saturation als `0..100` senden. Siehe `CHANGELOG.md` 0.9.0.

**Befund:** Intern führt der Daemon HS-Saturation in **Prozent (0..100)**
(`internal/model/combined/hscolor.go:23`). Der Client liest die nested
`color:{h,s}`-Read-Form dagegen als **Bruch (0..1)** und skaliert ×100; sein
Write-Pfad sendet HA-`[0,100]` roh. Mindestens eine Seite hat die falsche
Annahme über die Wire-Einheit.

**Vorschlag:** Die tatsächlich serialisierte/erwartete Saturation-Einheit von
`set_color` (Read **und** Write) festschreiben und dokumentieren; ggf. den
Read-Pfad des Clients oder die Daemon-Serialisierung angleichen, damit ein
Round-Trip konsistent ist.

**Priorität:** Niedrig, aber Korrektheit — auf echtem Farb-Gerät verifizieren.

---

## Bewusst KEINE Daemon-Lücken (zur Abgrenzung)

Diese kamen bei der Client-Verifikation auf, sind aber **kein** Daemon-Defizit:

- **`sysvar_changed` bei client-initiiertem Write** wird sehr wohl gesendet
  (`PatchSysvar → UpdateSysvar → SysvarChangedEvent`, mit Same-Value-Dedup,
  `internal/central/coordinators/hub.go`). Ein xfail im Client ist ein
  Simulator-/Dedup-Artefakt.
- **`program_executed` nur CCU-originär** (`NotifyProgramExecuted`) — ein
  client-initiiertes `execute` bekommt REST 202, keinen Push. Bewusstes Design.
- **rooms / functions (Geräteebene) / `translated_name` / sysvar `description`**
  sind bereits am Wire — fehlende Werte im Client sind dort ein
  Konsumptions-, kein Daemon-Thema.
