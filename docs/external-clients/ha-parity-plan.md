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
