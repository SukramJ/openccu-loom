# SPA-E2E gegen godevccu

End-to-End-Validierung der Svelte-SPA-Bedienoberflächen ohne
physische CCU. Adressiert die Sichtbarkeitslücke bei Gerätetypen,
die im Entwickler-Inventar fehlen (Cover, Lock, RGBW, …).

## Motivation

Drei Schichten müssen pro Bedienelement zusammenspielen:

```
Svelte-Tile ─── HTTP/WS ─── Daemon-CDP-Dispatcher ─── ChannelWriter ─── CCU
```

Bei einer realen CCU testet der Entwickler nur die Geräte, die im
eigenen Bestand stehen. Cover-Tiles auf HmIP-FBL, Lock auf
HmIP-DLD, RGBW auf HmIP-RGBW — wenn die im Hausstand fehlen, geht
jede Regression dort unentdeckt durch alle Stages bis zum
Anwender.

aiohomematic hat dieses Problem auf dem Backend gelöst: jeder
Custom-DP hat Unit-Tests, die gegen pydevccu zeigen, dass ein
Call gegen den DP die erwarteten Wire-Calls produziert. Auf der
Backend-Seite besteht diese Abdeckung in OpenCCU-Loom
strukturäquivalent (per-Custom-DP-Tests + Cross-Stack-Snapshot
gegen aiohomematic).

Diese Doku beschreibt die Erweiterung auf die SPA-Schicht: ein
Test-Setup, das die Svelte-Tile-REST-Calls gegen den Daemon-mit-
godevccu fährt und überprüft, dass die wire-seitige Antwort der
Erwartung entspricht.

## Architektur

```
┌──────────────────────────────────────────────────────────────────┐
│ Go-Test-Prozess (tests/spa_e2e/, build-tag `e2e`)                │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │ godevccu (in-process VirtualCCU)                           │  │
│  │   • XML-RPC + JSON-RPC auf ephemeral ports                 │  │
│  │   • komplette OCCU-Gerätevielfalt (~399 Modelle)           │  │
│  │   • devicelogic schreibt SetValue / PutParamset durch      │  │
│  │     und emittiert event() Push-Callbacks                   │  │
│  └────────────────────────────────────────────────────────────┘  │
│                  ▲ XML-RPC                                       │
│                  │                                               │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │ openccu-loom-Daemon (in-process, config.testdata.yaml)      │  │
│  │   • central.CentralUnit zeigt auf godevccu                 │  │
│  │   • REST + WS auf ephemeral port                           │  │
│  │   • Auth = Single-User-Test-Token, im Test eingerichtet    │  │
│  │   • Pfad: cmd/openccu-loom Hauptcode, kein Test-Surrogat    │  │
│  └────────────────────────────────────────────────────────────┘  │
│                  ▲ HTTP/WS                                       │
│                  │                                               │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │ E2E-Driver                                                 │  │
│  │   • baut authentifizierten http.Client                     │  │
│  │   • iteriert über CDP-Inventar (GET /cdps für jedes Gerät) │  │
│  │   • führt pro CDP einen Bedien-Plan aus                    │  │
│  │   • verifiziert Roundtrip (REST-Response + Wire-Side-Effect│  │
│  │     + WS-Event)                                            │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

Die SPA selbst wird nicht gestartet — der Test fährt die REST-/WS-
Calls direkt. Die Tests modellieren die *Bedien-Pläne* der Tiles
1:1 (was der User mit einem Klick auslöst), nicht das Rendering.
Rendering-Bugs deckt `svelte-check` + ein optionaler Visual-Test
ab (außer Scope dieser Doku).

## Bedien-Plan pro CDP-Kind

Jeder CDP-Kind bekommt einen Tabellen-Eintrag mit:

| Feld | Bedeutung |
|---|---|
| `setup` | Geräte-Modell + welcher Kanal — `("HmIP-BWTH", 1)` |
| `pre-state` | Welche Wire-Werte gelten als Ausgangslage; ggf. via `godevccu.setValue` direkt gesetzt |
| `actions` | Liste von `(operation, params)` — was der Tile bei Klick X sendet |
| `expect_wire` | Welche Wire-Calls (`setValue` / `putParamset`) godevccu hätte sehen müssen |
| `expect_state` | Welche Custom-DP-StatePayload-Felder nach dem Call den erwarteten Wert tragen |
| `expect_ws` | Welche WS-Events (`data_point` / `custom_data_point`) durchgelaufen sein müssen |

Beispiel — Climate HmIP-BWTH:

```yaml
- name: climate_hmip_set_mode_auto
  setup: { model: HmIP-BWTH, channel: 1 }
  actions:
    - { op: set_mode, params: { mode: auto } }
  expect_wire:
    - { method: setValue, addr: "<addr>:1", param: CONTROL_MODE, value: 0 }
  expect_state:
    hvac_mode: auto
  expect_ws:
    - { type: custom_data_point, name: SET_POINT_TEMPERATURE, kind: climate_hmip }
```

Pläne werden YAML-spezifiziert (oder Go-strukturiert) — bewusst
deklarativ, damit jeder Tile-Refactor einen mechanischen Test-Update
auslöst.

## Erste Slice (ADR 0016 Follow-up)

Slice 1 deckt die Kind-Familien ab, die in der Übersicht-View
heute rendern:

| Tile | godevccu-Modell | erste actions |
|---|---|---|
| LightTile / `light_fixed_color` | HmIP-BSL | turn_on, set_color {color: RED}, turn_off |
| LightTile / `light_rgbw` | HmIP-RGBW | turn_on, set_color {hue: 120, saturation: 1}, set_color_temperature {kelvin: 4000}, turn_off |
| ClimateTile / `climate_hmip` | HmIP-BWTH | set_temperature {temperature: 21.5}, set_mode {mode: auto}, set_mode {mode: heat}, enable_boost / disable_boost, set_away_for_duration |
| ClimateTile / `climate_rf` | HM-CC-RT-DN | set_temperature, set_mode (Auto / Boost / Manuell) |
| CoverTile / `cover_blind` | HmIP-FBL | open, set_position {position: 0.5}, set_tilt {tilt: 0.3}, close, stop |
| CoverTile / `cover_garage` | HmIP-MOD-HO | open, close, stop, ventilate |
| LockTile | HmIP-DLD | lock, unlock, open |
| SwitchTile | HmIP-PS / HmIP-BSM | turn_on, turn_off, turn_on_for {seconds: 5} |
| SirenTile | HmIP-ASIR | turn_on, turn_off |

Slice 1 reicht aus, wenn jeder kanonische Tile-Klick einmal grün
durchgelaufen ist. Erweiterungen (Boundary-Werte, Fehlerpfade,
unerlaubte Operationen) folgen in Slice 2.

## Test-Layout

```
tests/spa_e2e/
├── README.md
├── harness.go            (build-tag e2e) — Daemon + godevccu start, Auth-Setup, http.Client-Factory
├── plans.go              Bedien-Pläne als Go-strukturen (`type Plan struct { ... }`)
├── runner.go             ExecutePlan(t, h, plan) — orchestriert action / expect-blöcke
├── plan_climate_test.go  per Kind ein Datei → Liste an Plänen
├── plan_cover_test.go
├── plan_light_test.go
├── …
└── testdata/
    └── config.yaml       Daemon-Config-Vorlage (godevccu-Backend, ephemere Ports)
```

Build-Tag `e2e` hält die Tests aus dem Normal-Build heraus
(Daemon-Boot dauert >1 s). CI fährt `make e2e-spa` als separate
Stage (Branch-Protection-Gate, kein Pre-Merge-Block).

## Wire-Side-Verification

Zwei Optionen die godevccu-Wire-Calls zu beobachten:

1. **godevccu Inspector**: aktuell hat godevccu keinen
   öffentlichen "RecordedCalls()"-Hook. Erste Slice baut den Hook
   in godevccu auf (~30 LOC: ein Slice `[]Call` an `Server.rpc`
   anhängen, mit `Server.Calls() []Call` exportieren).

2. **Sniff-Adapter**: alternativ ein dünner XML-RPC-Proxy
   zwischen Daemon und godevccu, der jede SetValue / PutParamset
   protokolliert. Mehr Aufwand, brauchen wir nicht — Option 1
   reicht.

Option 1 ist die Vorwahl.

## Migration vs. Erweiterung

Die existierenden Integration-Tests unter `tests/integration/`
(godevccu) testen das **Daemon-Modell**: dass der Daemon aus
godevccu-Events das richtige Custom-DP-Modell baut. Diese Tests
bleiben. Die neuen SPA-E2E-Tests sitzen *über* dem Daemon, gehen
durch REST, und decken speziell die SPA-Bedien-Pfade ab.

Die Trennung ist sauber: Daemon-Modell-Tests sehen kein REST,
SPA-Tests sehen kein Custom-DP-internal.

## Erste konkrete Anwendung

Der heutige User-Bericht — "Auto schalten geht nicht, gibt 502
zurück" — ist der archetypische Anwendungsfall: ein Tile-Click,
der lokal beim Entwickler einen Fehler produziert, den er per
Daemon-Log nur schwer eingrenzen kann. Im E2E-Test gegen
godevccu zeigt das Setup sofort:

- Wenn der Test gegen godevccu erfolgreich ist → der Bug liegt
  in der echten CCU-Antwort (CCU-Firmware-Stand / Gerät verträgt
  den Wire-Shape nicht).
- Wenn der Test gegen godevccu auch failt → der Bug ist im
  Daemon-Code; godevccu hat die Last-of-Truth, der Daemon
  schreibt etwas, was kein CCU akzeptieren würde.

Die Implementation startet daher mit `climate_hmip.set_mode auto`
als ersten Plan-Eintrag — die regression, die der User gerade
gemeldet hat.

## Offene Fragen (für die Implementierung)

- **Auth-Setup im Test**: einfachster Weg ist ein Default-Admin-
  Token, der per Migration ins SQLite gestempelt wird. Alternative:
  Setup-Wizard durch eine REST-Sequence durchlaufen.
- **Parallel-Execution**: jeder Plan braucht einen eigenen
  Daemon-+-godevccu-Stack oder eine `t.Parallel`-Sperre. Erstes
  reicht (godevccu-Start ist günstig).
- **WS-Event-Capture**: ein simpler `gorilla/websocket`-Client
  reicht; envelopes parse + assert.
- **Test-Daemon-Konfig-Vorlage**: braucht eine eigene
  config-Subset-Datei, weil die produktive `config.yaml` viel
  enthält, das im Test nicht greift (MQTT, Matter, OIDC). Wir
  ziehen den Subset-Schnitt entlang der `Deps`-Struktur in
  `cmd/openccu-loom/daemon.go`.

## Akzeptanzkriterium

Slice 1 ist erfolgreich wenn:

- `make e2e-spa` läuft grün durch
- jeder Kind aus der Übersicht-View hat mindestens einen Plan
- ein bewusst eingebauter Regression-Bug (z.B. set_mode mit
  falschem CONTROL_MODE-Wert) wird vom Test-Setup zuverlässig
  gefangen
