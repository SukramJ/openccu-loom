# HA-Paritätsplan: Otto-Rem (loom) ≙ OttoMac (aiohomematic)

Stand: 2026-06-10. Grundlage ist ein vollständiger Live-Vergleich der
HA-Entity-Registry und der Live-States zweier Instanzen derselben CCU
(125 Geräte): `OttoMac` (aiohomematic, Referenz) vs. `Otto-Rem`
(openccu-loom-client). Rohzahlen: 1971 vs. 2277 Entitäten, 1065 über
normalisierte unique_ids gematcht, 107 von 483 vergleichbaren
Live-Werten abweichend.

## Phase 0 — behoben (Branches vom 2026-06-10)

| Repo | Branch | Fix |
| --- | --- | --- |
| openccu-loom-client | `fix/ha-parity-live-state` | WS-Subscriptions um `datapoint.*` + `custom_data_point.*` ergänzt (Werte froren auf Bootstrap-Stand ein — Ursache der "0-Werte" und der eingefrorenen CDP-Defaults) |
| openccu-loom-client | ebd. | CDP-Invoke POSTet immer `{}` (Daemon wies leeren Body mit 400 ab — Schalten/Sperren war komplett funktionslos) |
| openccu-loom-client | ebd. | binary_sensor: ENUM→bool nach aiohomematic-TRUE-Tabelle (`CLOSED/OPEN`→`OPEN`, …) — 22 Tür-/Fensterkontakte waren invertiert |
| openccu-loom-client | ebd. | `observed=false` liest `None` (unknown) statt Wire-Default 0 |
| openccu-loom-client | ebd. | Climate/Siren lesen statische Daten aus dem CDP-`config`-Block (min/max_temp, temp_step, hvac_modes, preset_modes, available_tones/lights); Capability-Alias `profiles`→`profile`; hvac_modes-Fallback `("heat",)`; `data_point_name_postfix="BUTTON_LOCK"`; `translation_key` auf generischen DPs; Warnung bei leerer CCU-Serial; CDP-State-Seeding beim Bootstrap |
| openccu-loom | `fix/cdp-rest-state` | REST/WS lösen CDP-State über `payload.Source` auf (Assertion auf `State() map[string]any` matchte nie → nur Adress-Stub); `state` zusätzlich im `GET …/cdps`-Listen-Payload; openapi.yaml dokumentiert `config`/`state` |
| openccu-loom-types-py | `feat/cdp-config-state` | `CustomDPSummary.config`/`.state` regeneriert (0.1.10) — wurden vorher von pydantic verworfen |

Erwarteter Effekt nach Deployment: alle 107 Live-Wert-Abweichungen
(invertierte Kontakte/Bediensperren, Climate `auto`→`off`, 0-Werte)
beseitigt; Climate-capabilities (hvac_modes, min/max, PRESET_MODE)
korrekt.

## Fortschritt 2026-06-11 (Branch `feat/ha-parity-phase1` + Client `fix/climate-enum-members`)

- Punkt 1 ✅ Serial: `WireHub` stampt model/version/hostname/serial via
  `get_backend_info.fn`/`get_serial.fn`; `GET /system/ccu` liefert sie.
- Punkt 2 ✅ (Identität): Die Materialisierung pro Kanal existierte
  bereits — die CDPs eines Kanal-Verbunds teilten sich aber den Wire-
  Namen (`LEVEL`), wodurch die per-Name-REST/WS-Fläche den Verbund
  kollabierte (Client behielt nur den letzten Kanal; Invoke traf immer
  den ersten). Kollisionen heißen jetzt `PARAM@<channel>`
  (`custom.WireName`/`FindByWireName`, REST + WS + Dispatcher); der
  Client keyed sie getrennt und benennt Verbund-Mitglieder
  aiohomematic-konform über den CCU-Kanalnamen (primary → Gerätename,
  `vch5`/`vch6`).
- Punkt 4 ✅ Bediensperren: Lock löst `GLOBAL_BUTTON_LOCK` (MASTER) auf,
  Semantik invertiert (true=LOCKED), Schreibpfad via put_paramset.
- Punkt 7 ✅ als Nicht-Bug geschlossen (RF_SIREN auch in aiohomematic tot).
- Punkt 14 ✅ Dimmer-`onoff`: Capability-Aliase im Client
  (`brightness`→`dimmable`, `tones`→`acoustic`, `profiles`→`profile`).
- Client-Folgefix: Climate `modes`/`profiles` liefern echte
  aiohomematic-Enums (HA liest `.value`; Strings crashten das Setup).

Live-Verifikation nach Merge/Deploy ausstehend; erwartet: 23 → ~0
Wert-Abweichungen. Offen: Punkte 3, 5, 6, 8–13.

### Nachtrag 2026-06-11 (Blöcke 8 + 9, Client-Branch `fix/climate-enum-members`)

- Punkt 8 ✅ Hub-Layer: Sysvars + Programme spawnen in HA (Otto-Rem live:
  193 Hub-Entitäten — 80 Sensoren, 52 binary, 61 Programm-Buttons).
  Fixes: Hub-DPs in der Bootstrap-Ankündigung, voller Katalog via
  `GET /sysvars`/`/programs` (der Snapshot-Hub-Block trägt nur den
  Index der ERSTEN Central — Daemon-Folge-Item: Multi-Central-Snapshot),
  Central-Filter + `${…}`-Internals-Filter, ALARM/LOGIC→binary_sensor
  (aiohomematic-Default-Mapping), `/system/ccu`-Eintrag per Namens-Match.
  Offen für exakte Parität: Marker-Filterung (ccu: 69 von 132 Sysvars,
  6 von 59 Programmen) — braucht das description-Feld an
  SysvarSummary/ProgramSummary im Daemon (→ Punkt 5/6).
- Punkt 9 ✅ Update-Entitäten: ein DpUpdate je updatable Device
  (uid `loom_<address>_update`), HmIP-Ready/In-Progress-Gating wie
  aiohomematic, Install via `POST /devices/{addr}/firmware/update`.
- Punkt 10 ✅ Events: Event-Groups werden mit dem Bootstrap-Batch
  angekündigt (Cache auf der Query-Facade), tragen den `loom_`-Namespace
  (`loom_event_group_<typ>_<channel_uid>`), und die Refresh-Bridge
  zeichnet jeden Device-Trigger am passenden Group-Objekt auf
  (`last_triggered_event`) und pingt dessen keyed
  DataPointStateChangedEvent — die HA-event-Entität feuert.
- Punkt 11 ✅ Calculated DPs: Bootstrap zieht
  `GET …/channels/{no}/calc-dps` je Kanal; die DPs subclassen die
  generischen Dp*-Zwillinge (uid-Präfix `calculated`, parity zu
  aiohomematics `calculated_<addr>_<ch>_<param>`), liegen im normalen
  (address, channel, parameter)-Store und empfangen WS-Wertänderungen
  ohne Sonderfälle.
- Valve/Siren-Residual ✅ (2026-06-11): Ursache war Kategorie-/Kind-
  Mapping, nicht fehlende Materialisierung. Nur Light/Cover
  implementierten `Category()`; das Valve promotete das `switch` seines
  anonym eingebetteten `*generic.Switch`, Smoke-Sirenen (HmIP-SWSD) und
  SoundPlayer (HmIP-MP3P) hatten keinen kind-Token. Fix: explizite
  `Category()` auf allen Custom-Typen + `siren_smoke`/`siren_sound`
  Kinds; Client mappt die neuen Kinds. **Daemon-Redeploy nötig.**
- Punkt 5/6 ✅ (Hub-Teil): `resolve_hub_inclusion` spiegelt
  `_resolve_sysvar_enabled_default` (Marker-Präfix-Match auf der
  description, die der Daemon bereits shipped); ohne Marker spawnt
  alles Nicht-Interne **default-disabled** (ccu-Referenz: alle 69
  Sysvar- und 12 Programm-Entitäten disabled_by=integration);
  CCU-interne Programme (`prgEnergyCounter-…`, `is_internal`) spawnen
  nie; je Programm Button+Switch; Marker laufen Integration →
  CentralConfig → HubCoordinator. Geräte-Parameter-Visibility
  (SECTION/ACTIVITY_STATE/Climate-Interna, ~600 Entitäten) bleibt
  offen → braucht ein service/hidden-Flag je DP vom Daemon.
- Geräte-Parameter-Visibility ✅ (Rest von 5/6, 2026-06-11): Die
  Pipeline berechnete das volle Visibility-Modell schon immer
  (forced sensors, un-ignore, HIDDEN_PARAMETERS, CDP-Absorption →
  forced no_create) und MQTT-Discovery nutzte es — nur der REST-Wire
  trug das Verdict nicht. `DataPointSummary.usage` shipped es jetzt;
  der Client überspringt `no_create`/`ignored` in Announcement und
  `get_data_points(exclude_no_create=True)`. Erwartet: ~600
  Überhang-Entitäten weniger. **Daemon-Redeploy + Types 0.1.12.**
- Offen: Punkt 3 (VALUES-Seeding), 12 (Schedule), 13
  (Rest-Namensparität), Hub-Singletons (alarm/service messages, inbox,
  HUB_UPDATE, Connectivity, Install-Mode), Orphan-Cleanup für loom,
  Sysvar-`is_internal` am Wire. Hinweis Verifikation: bestehende Registry-Einträge
  (59 interne Programm-Buttons, enabled Hub-Entitäten) bleiben
  registriert — echte Zahlenparität zeigt erst ein frisch angelegter
  Entry; der Orphan-Cleanup ist auf loom noch deaktiviert.

### Messung 2026-06-11 nachmittags (frischer Otto-Rem-Spawn, Registry bereinigt)

Struktur: climate/lock/valve/notify exakt ✓ (Valve 9/9!), siren 8/7,
gematcht 1725 (von 1065), Live-Abweichungen 21 von 692. Disabled-Parität
greift (loom 1352 disabled).

**Neuer präziser Daemon-Bug (Blocker für die letzten 21 Wert-Abweichungen):**
Jedes CCU-Wire-Event wird DOPPELT ingestiert — derselbe DP publiziert
binnen ~1 ms zwei `datapoint.value_changed` (fortlaufende seq), einmal
mit dem echten Wert, einmal mit **0**; die Reihenfolge variiert, der
letzte gewinnt (deshalb pendeln HA-Werte zwischen korrekt und 0).
Beleg: seq=43209 value=17.8 / seq=43211 value=0 (000EE0C9A7062C:1,
ACTUAL_TEMPERATURE), beobachtet auf TEMPERATURE/HUMIDITY/VOLTAGE/
FREQUENCY/ILLUMINATION. Verdacht: doppelt registrierte Event-Quelle
bzw. paralleler Ingestion-Pfad, dessen Wert-Parse auf 0 fällt.
Hinweis Messartefakt: WS-Topic der DP-Events ist `device.<addr>.…`
(Type `datapoint.value_changed`) — eine `datapoint.*`-Topic-Subscription
matcht nichts; Events kommen über `device.*`.

Verbleibende Strukturklassen: 132+45 Schedule-Layer (Punkt 12),
Virtual-Remote-Behandlung (ccu: 50 Event-Groups, loom: 100 Event-Groups
+ 100 Buttons + uid-Wechsel durch Serial-Fix), 212 press_*-Buttons
(usage=event sollte generischen Spawn unterdrücken), 42
operating_voltage + 16 current_illumination (Visibility-Nuance), 26
Sysvar-Überhang (is_internal fehlt am Sysvar-Wire), Hub-Singletons.

### Fix-Runde 2026-06-11 abends (Daemon `fix/parity-round3`, Client `fix/parity-round3`, Types `feat/sysvar-flags` 0.1.13)

Alle Issue-Klassen der Nachmittagsmessung außer Schedule-Layer und
Hub-Singletons behoben:

- **Doppel-Null-Ingestion (21 Live-Abweichungen):** Status-Pair-Fallback
  schrieb den `<X>_STATUS`-Index (0) via `OnWireValue` über die Messwerte;
  jetzt `UpdateStatusFromWire` (Daemon `03a940f`).
- **212 press_*-Buttons:** `applyClickEventMarks` forciert usage=event auf
  Click-Parametern physischer Geräte (Daemon `b5f2213`); Client nimmt
  `event` in `_NON_CREATABLE_USAGES` auf (Client `11bef61`).
- **Sysvar-Überhang:** `is_internal`/`is_extended` am SysvarSummary-Wire
  (Daemon `b5f2213`, Types 0.1.13); Client bevorzugt Wire-Flag und mappt
  extended auf schreibbare Klassen (Switch/Select/Number/Text).
- **42 operating_voltage + 16 current_illumination:** Required-Whitelist
  short-circuitete den ganzen Ignore-Entscheid; Spiegel-Semantik
  wiederhergestellt — Whitelist exemptet nur statische Liste/Hides,
  `ignoreParametersByDevice` greift bedingungslos; ForcedUsage-Preserve
  in `markIfIgnored` entfernt (Daemon `9c29301`).
- **HmIP-PS*-Event-Gruppen (~10):** Event-Suppression-Gate matchte das
  Modell exakt statt per Prefix (HmIP-PSM/HMIP-PS fielen durch);
  `applyClickEventMarks` überschreibt keine bestehende Suppression mehr
  (Daemon `d25ab5c`); Client filtert no_create/ignored aus
  `build_event_groups` (Client `3b22cc7`).

**VR-/Bestandsbilanz geklärt (kein Bug):** Die 50 Event-Groups + 100
Buttons `bidcos_rf` plus KEQ0117547 existieren nur auf loom, weil OttoMac
das BidCos-RF-Interface nicht konfiguriert hat; 000D1A49A3D83F
(HmIP-MOD-OC8 „ModOC8 Test“) fehlt im OttoMac-Bestand. Die
HmIP-RCV-Entities (50 EG + 100 Buttons) matchen beidseitig.

Erwartung nächste Messung (nach Daemon-Redeploy + Client-Install):
Live-Abweichungen 0, „nur loom“ schrumpft um ~212 Buttons, 58 Sensoren,
26 Sysvars, ~10 Event-Gruppen; verbleibend Schedule-Layer (Punkt 12),
Hub-Singletons, BidCos-Bestandsdifferenz.

### Messung 2026-06-11 abends (nach Round-3-Redeploy, frischer Spawn)

Struktur: climate/light/lock/notify/siren/valve exakt ✓, gematcht 1710,
**nur loom: 74** (vorher 609), nur ccu: 256. Live: 692 verglichen,
13 Abweichungen — alle wieder `loom='0'` auf Status-Pair-Messwerten,
diesmal als Push NACH dem Bootstrap (`last_updated` Minuten nach Start),
während der Daemon-Store korrekt war (`ACTUAL_TEMPERATURE=21.3, live`).

**Root-Cause Nr. 2 der Doppel-Null (PR #49, Branch `fix/status-event-topic`):**
`EventCoordinator.HandleRawEventNormalized` strippte das `_STATUS`-Suffix
und publizierte den Status-Index (0) als `value_changed` des
BASIS-Parameters — Cache-Korruption + Null-Pushes an WS/MQTT/Clients;
der Round-3-Callback-Fix deckte nur den praktisch toten dp==nil-Fallback
ab. Fix: kein Suffix-Stripping mehr; Basis-DP-Status wird im Handler
immer via `UpdateStatusFromWire` gepflegt (`c99dc36`).

**Folge-Befunde der Struktur-Deltas (gefixt):**
- 27 fehlende Keypress-Gruppen (HmIP-BSM & Co) + 68 überzählige
  press-Buttons (KEY-Kanäle im aktiven Op-Mode): der ForcedUsage-Preserve
  in `applyClickEventMarks` war zu breit — jetzt überschreibt der Pass
  alles außer der Event-Suppression (Decider-Abfrage, `e593ba4`).
- 36 fehlende Sysvar-Entities: Wire-`is_internal` schloss ALLE internen
  aus; aiohomematic inkludiert sie disabled
  (`DEFAULT_INCLUDE_INTERNAL_SYSVARS=True`), nur `${…}` nie
  (Client PR #12, `32a0568`).

Verbleibend nach PR #49 + #12: Schedule-Layer (132 switch + 45
sensor/week_profile, Punkt 12), Hub-Singletons, BidCos-Bestandsdifferenz
(OttoMac ohne BidCos-RF-Interface), Kleinposten (3 level-Sensor-uid-Drift,
2 calculated duration, zentraler Backup-Button-uid).

### Messung 2026-06-11 spät (nach PR #49/#12-Redeploy, frischer Spawn)

**Live-Abweichungen: 0 (von 692).** Struktur: binary_sensor, button,
climate, event, light, lock, notify, siren, valve **exakt**; gematcht
1773, nur loom 29, nur ccu 193.

Rest-Analyse und Fixes (Daemon PR #50, Types 0.1.15 PR #8, Client
2026.6.6 PR #13):

- **23 sysvar nur-loom**: OldVal/pcCCUID-Scratch-Werte (hub.py
  `_EXCLUDED`) + IDs 40/41 (Alarm-/Servicemeldungen,
  `IGNORE_SYSVARS_BY_ID`). Exklusion jetzt ZENTRAL im Daemon
  (`loadSysvars`), damit REST/MQTT/Matter/Clients dieselbe Semantik
  haben; Client-Filter als Fallback. `SysvarSummary.vid` neu am Wire
  (api 1.3.0).
- **4 Kategorie-Drifts auf gematchten uids** (switch/select auf ccu vs
  binary_sensor/sensor auf loom): `SysVar.getAll` liefert keine
  Descriptions → is_extended feuerte nie. Daemon lädt Descriptions nun
  via vorhandenem ReGa-Script (`sv.DPInfo`), HAHM-Marker greift.
- **3 level_sensor nur-ccu**: Legacy-uids im OttoMac-Registry-Altbestand
  (`<addr>_1_level_sensor`); aktueller aiohomematic-Code erzeugt
  `<addr>_1_level` — identisch mit loom. Kein Handlungsbedarf.
- **2 number:combined duration nur-ccu** (+2 calculated sensor
  nur-loom): aiohomematics combined-DP-Layer (DURATION_VALUE+UNIT →
  schreibbare Number, hue+saturation) fehlt im Loom-Stack — neues
  Plan-Item.
- **1 button:backup nur-loom**: bewusstes Loom-Extra (Daemon-Backup).

Verbleibend: Schedule-Layer (132 switch + 45 sensor + 41 week_profile),
Hub-Singletons (~10: alarm/service messages, connectivity, latency,
inbox, system-health, system-update, install-mode, last-event-age),
combined-DP-Layer, BidCos-Bestandsdifferenz.

### Schlussblöcke 2026-06-12 (Hub-Singletons, Schedule-Layer, Combined-DPs)

Befund der Tiefenanalyse: Der Daemon hat den kompletten Schedule-Layer
(weekprofile.ProfileDataPoint + ChannelSwitch inkl. TurnOn/Off) und alle
Hub-Singleton-Daten bereits intern — die MQTT-Plane konsumiert beides
seit jeher. Es fehlte nur die REST-Exposition für externe Clients:

- **PR #51** (`feat/rest-hub-schedule-parity`, stacked auf #50, api 1.4.0):
  GET /system/update (+ install), GET /system/metrics (JSON-Zwilling der
  drei Hub-Sensoren), GET+POST /install-mode/interfaces (pro Interface),
  PUT …/week_profile/channel-locks/{key} (Schreibhälfte von
  schedule_enabled). Types 0.1.16 (PR #9).
- Lesepfade existierten schon: /alarm-messages, /service-messages,
  /inbox, /interfaces, …/week_profile (schedule_enabled mit den
  channel_keys 1_1…, exakt die ccu-Switch-uids), …/schedule.

Client-Arbeitsplan (2026.6.7): Hub-Singleton-DPs (uid-Slugs
alarm-messages/service-messages/inbox/system-health/connection-latency/
last-event-age/system-update/connectivity-…/install_mode_hmip[-button]),
WeekProfileDp + ScheduleChannelSwitch (uids week_profile_<addr>_… /
schedule_channel_switch_<addr>_schedule_channel_lock_<key>),
CombinedDurationDp (Number, ersetzt calculated-duration-Sensoren),
30s-Singleton-Refresh.

uid-Hinweis: connectivity-/install-mode-uids tragen den Instanznamen
(ottomac vs otto-rem) bzw. das Interface-Set — BidCos-Paare und der
connectivity-Name matchen by design nicht 1:1.

### Finale Messung 2026-06-12 (Client 2026.6.8, Daemon mit PR #52)

**Struktur: gematcht 1940, nur ccu 26, nur loom 7** (von einst 247/609).
11/14 Domains exakt. **Live-Abweichungen: 4** — davon 3 lebende
Hub-Werte verschiedener Backends (servicemeldungen, last-event-age),
1 Zeitplan-Zählerdifferenz. **Attribute: friendly_name-Diffs 277 → 8**
(4 calculated ohne daemon-translated_name, 4 Hub-Singleton-Instanznamen
by design); übrige Attribut-Diffs nur noch 5× hvac_action
(idle vs heating an eTRV — Activity-Ableitung prüfen).

**Climate-Cards: 16/16 mit Ist-/Soll-Temperatur** (PR #52 CDP-State +
Client-Fallback); Wandthermostat-Humidity via Fallback, am Wire nach
Deploy von `eda541a` (INTEGER-HUMIDITY-Fix).

Verbleibende Einzelposten:
- 17+2 Schedule-Switches, 2+1 schedule/profile-Sensoren: Daemon 404t
  auf WATER_SWITCH_WEEK_PROFILE (HmIP-WSM/ELV-SH-WSM); MP3P/WRC6-230
  schedule_enabled-Keys weichen von aiohomematics Actor-Map ab.
- update:system-update + sensor:week_program_channel_locks (je 1):
  HA-Entity fehlt loom-seitig — Spawn-Pfad prüfen (Otto-Rem
  current_firmware am Wire leer).
- 5× hvac_action idle/heating an eTRV: Daemon-Activity()-Ableitung.
- 3 level-Sensor-uids + connectivity-/backup-Namen: Altbestand/by design.
- 10 calculated + 2 combined Namen: brauchen daemon-translated_name
  für calc-/combined-DPs.

### ENDSTAND 2026-06-12 (api 1.5.0, Client 2026.6.10) — Parität erreicht

**14/14 Domains exakt** (update 124=124 inkl. System-Update-Wert),
gematcht 1962, nur ccu 4 / nur loom 5 — sämtlich by design (3
Legacy-uids im Referenz-Altbestand, Connectivity-Instanznamen,
Loom-Backup-Button). Live-Abweichungen 4 (2 lebende Hub-Werte, 2
Schedule-Cache-Staleness). friendly_names: 5, ausschließlich
Instanznamen. **Attribut-/Card-Differenzen: 0.**

Alle aktionablen Posten aus dem Vergleich OttoMac (aiohomematic) vs
Otto-Rem (openccu-loom-client) sind damit abgearbeitet; verbleibende
Differenzen sind Instanz-Identität oder Mess-Timing.

## Phase 1 — Daemon-Lücken (openccu-loom)

Priorisiert nach Nutzerwirkung:

1. **CCU-Serial exponieren** (`/centrals`-Eintrag bzw. System-Info um
   `serial` ergänzen). Der Client konsumiert `ccus[0].serial` bereits;
   ohne Serial bauen Virtual-Remote-/Hub-uids einen leeren
   central-id-Slot (`loom__hmip_rcv_…`) und die HA-seitige
   unique_id-Migration wird übersprungen ("config entry has no
   serial").
2. **CDP-Materialisierung pro Kanal** (größtes Strukturthema, ~240
   fehlende Entitäten): aiohomematic erzeugt pro Profil
   primary + secondary/virtual channels (Dimmer: ch4 + vch5 + vch6;
   Schaltaktor: ch3 + vch4 + vch5). Der Daemon materialisiert nur einen
   Kanal (im Live-Bestand: den letzten). `RebasedChannelGroupConfig`
   muss die volle Kanalgruppe abbilden; Namen der Sekundärkanäle
   ("vch5"/"vch6") mitliefern. Betroffen: 26 lights, ~199 custom
   switches, 9 valves, 5 sirens.
3. **VALUES-Initial-Seeding härten**: `seedValues()` läuft nur mit
   gebundenem ReGa-Runner; neue Geräte ohne Values-Cache starten
   unobserved. Mit Fix aus Phase 0 zeigen sie jetzt korrekt "unknown"
   statt 0 — Ziel ist aber der echte Wert ab Bootstrap
   (fetch_all_device_data konsequent, Cache-Persistenz prüfen).
4. **BUTTON_LOCK-State aus MASTER**: Bediensperren leiten `LockState`
   aus dem MASTER-Paramset (`GLOBAL_BUTTON_LOCK`/`BUTTON_LOCK`) ab;
   `seedMasterValues()` muss den Lock-CDP versorgen, sonst bleibt der
   Default UNLOCKED bis zum ersten Config-Event.
5. **Sichtbarkeits-/Absorptionsregeln** (~800 überzählige generische
   Entitäten): Parameter, die aiohomematic versteckt oder im Custom-DP
   absorbiert, erscheinen auf loom als eigene Entitäten — `SECTION`
   (211), `state` parallel zum CDP (160), `ACTIVITY_STATE` (47),
   Climate-Interna (`SET_POINT_TEMPERATURE`, `SET_POINT_MODE`,
   `ACTIVE_PROFILE`, `BOOST_MODE`, `PARTY_MODE` je 16),
   `OPERATING_VOLTAGE`-Gating, `WEEK_PROGRAM_CHANNEL_LOCKS` (29),
   `press_*` als Buttons (360, default-disabled). Empfehlung: der
   Daemon liefert ein `service`/`hidden`-Flag je DP (er kennt
   CONTROL/Profile), der Client filtert wie aiohomematics
   ParameterVisibility.
6. **Enabled-Default-Parität**: 565 gematchte Entitäten sind auf loom
   default-disabled, auf ccu aktiv (duty_cycle 118, rssi 232, sabotage
   43, actual_temperature 20, …). Regeln an aiohomematic angleichen.
7. ~~RF_SIREN-Profil fehlt~~ — geprüft 2026-06-11: `RF_SIREN` ist auch
   in aiohomematic nur ein Enum-Wert ohne Profil-Config und ohne
   Modell-Registrierung (Sirenen: HmIP-ASIR, HmIP-SWSD, HmIP-MP3P).
   Kein Defizit; faktische Parität.
8. **Hub-Layer**: Sysvars/Programme liegen im Snapshot und Store, aber
   es entstehen keine HA-Entitäten (0 vs. 88: 69 Sysvars, 12
   Programme, 7 Connectivity/Hub, 2 Install-Mode). Compat-`model/hub`
   an die Integration anbinden (SysvarDp*-Markerklassen,
   `get_new_hub_data_points`).
9. **Update-Entitäten** (0 vs. 123): `updatable`/`update_available`
   sind im Device-Summary vorhanden; DpUpdate-Compat-Klasse +
   Integrations-Dispatch fehlen.
10. **Event-Entitäten/Event-Groups** (0 vs. 130): keypress-Events als
    HA-event-Entities; `device.trigger` sendet der Daemon bereits,
    compat/event_group.py ist vorbereitet — Spawn-Pfad fehlt.
11. **Calculated DPs** (0 vs. 174): REST/WS-Endpunkte existieren
    (`calculated_data_points.go`); Abgleich des Berechnungs-Satzes mit
    aiohomematic (dewpoint, apparent_temperature, window_open,
    smoke/intrusion alarm, …) + Client-Spawn.
12. **Schedule-Layer**: `schedule_channel_switch`-Switches +
    `week_profile`-Sensor (aiohomematic) statt rohem
    `week_program_channel_locks`.
13. **Namens-Parität** (130 Diffs): Sekundärkanal-Namen (`vch5`),
    Kanal-Suffixe (`Betriebsmodus ch1`), `Status`-Präfix der
    state-Kanäle.
14. **Dimmer-color_mode**: gematchte Dimmer melden `onoff` statt
    `brightness` — `capabilities.dimmable`/kind-Zuordnung prüfen.

Querschnitt (Reviewregel): Jede Daemon-Änderung gegen alle drei
North-Planes prüfen (REST/WS, MQTT-Discovery, Matter) — Phase-0-Fund:
REST **und** WS hatten dieselbe kaputte State-Assertion, MQTT war
korrekt. Jede Model-Änderung gegen alle DP-Typen prüfen (generic,
custom, calculated, hub).

## Phase 2 — Statische Abdeckung jenseits des Live-Bestands

Der Live-Haushalt enthält keine Cover/Blind/Garage-, DALI/RGBW-,
TextDisplay- oder SoundPlayer-Geräte; deren Pfade sind daher
ungetestet. Statischer Befund: Konstruktoren im Daemon registriert
(IPCover, RfCover, IPGarage, IPDRGDALI, IPRGBW, IPTextDisplay,
IPIrrigationValve, …), Client-Compat-Klassen vorhanden (CustomDpCover,
CustomDpIpBlind, …) — aber **ohne Parität auf Kanal-/Feld-Ebene
verifiziert**. Maßnahme: automatisierter Cross-Repo-Paritätstest, der
für **jedes Modell** der aiohomematic-DeviceProfileRegistry (per
aiohomematic-test-support-Gerätebeschreibungen) asserted:

- identischer CDP-Satz pro Gerät (Kanäle, Kategorien, Kinds),
- identische unique_ids (Contract `canonical_unique_id`),
- identische enabled-default-/Sichtbarkeitsentscheidungen,
- identische Namen.

Damit ist die Abbildung auch für Gerätetypen gesichert, die in keinem
Testhaushalt stehen. Verankerung als CI-Gate im Daemon (analog
`tests/contract/test_loom_wire_enum_drift_contract.py` in
aiohomematic).

## Phase 3 — Verifikation am Live-System

1. Daemon + Client + Typen deployen, Otto-Rem-Entry neu laden.
2. Registry-/State-Vergleich erneut fahren (Skripte unter
   `/tmp/compare_entities*.py` der Analyse-Session; Matching über
   normalisierte unique_ids).
3. Zielbild: 0 Wert-Abweichungen bei gematchten Paaren; strukturelle
   Diffs nur noch bei bewusst offenen Phase-1-Punkten; danach pro
   abgeschlossenem Phase-1-Punkt erneut messen.
