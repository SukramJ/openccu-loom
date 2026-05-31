# Structural Parity-Approach — Replacement for Audit-Sweep-Loop

**Stand:** 2026-05-29
**Status:** Plan
**Kontext:** Nach v13–v16-Re-Audit-Schleifen reproduziert sich ein systemisches Pattern: ~20 % der "geschlossenen" Closures sind Dead-Code (Methode/Feld/Builder existiert, kein Production-Caller). Die Schleife konvergiert, aber wir produzieren das Pattern aktiv mit, weil Sub-Agents Surface ohne Caller-Verifikation patchen. Dieses Dokument ersetzt die Schleife durch ein strukturelles Sicherheitsnetz.

---

## 1. Grundprinzip

Die Re-Audit-Schleife behandelt das **Symptom** (Drift sichtbar machen). Das strukturelle Netz behandelt die **Ursachen**:

1. **Dead-Code-Produktion verhindern** — kein neuer Code ohne Production-Caller-Anker
2. **Bestehenden Dead-Code mechanisch finden** — kein 600-Zeilen-Audit für ein Reachability-Problem
3. **Wire-Encoding-Drifts verhindern** — Snapshot-Byte-Vergleich gegen aiohomematic pro Custom-DP
4. **Integration testen, nicht Surface** — Daemon-Boot + Feature-Smoke-Tests gegen godevccu
5. **Kein Code verlieren** — Dead-Code wird produktiv gewired ODER als by-design dokumentiert, niemals stillschweigend entfernt

---

## 2. Vier Säulen

### Säule 1: Static-Reachability-Analyzer (verhindert Dead-Code-Akkumulation)

**Was:** Go-Tool unter `script/reachability/main.go`, das aus `cmd/openccu-loom/main.go` den Call-Graph traversiert und alle exported Identifiers listet, die nicht erreicht werden (außer Tests + by-design-markiert).

**Implementation:**
- Nutzt `golang.org/x/tools/go/callgraph` (cha/rta-Algorithmus)
- Ausgabe: `docs/parity/dead-code-inventory.json` mit Pfaden + Identifiern
- Whitelist via `// loom:reachable:reason=...`-Kommentar (z.B. `// loom:reachable:test-only`)
- Läuft als Contract-Test `TestReachability` in `tests/contract/` — bricht Build wenn neuer ungewirter Identifier hinzugefügt wird

**Vorteil:** Kein Audit-Sweep mehr nötig, um die ~20 % Dead-Code-Items zu finden. Tool listet sie deterministisch. CI verhindert neuen Dead-Code.

**Was passiert mit existierendem Dead-Code:**
- Inventory-Run produziert eine vollständige Liste
- Liste wird Item-für-Item bearbeitet (siehe Säule 2)
- Niemals einfach löschen — entweder wiren oder by-design markieren

### Säule 2: Production-Caller-Pin-Tests (verhindert Re-Drift)

**Was:** Pro produktiv-gewireter Methode/Builder/Subscriber ein Test der den Caller-Pfad **ankert**, nicht das Verhalten.

**Pattern:**
```go
// Vor dem Fix: Methode existiert, kein Caller → Test fehlt
func (h *HubCoordinator) ExecuteProgram(ctx context.Context, id string) error { ... }

// Nach dem Fix: Caller in daemon.go gewired → Test pinnt Wiring
func TestProductionCaller_HubCoordinator_ExecuteProgram(t *testing.T) {
    // Pin: hub_wiring.go muss SetProgramExecutor aufrufen
    ref := mustFindIdentifierUsage(t, "internal/central/adapter/hub_wiring.go", "SetProgramExecutor")
    require.NotEmpty(t, ref, "HubCoordinator.SetProgramExecutor must be called in hub_wiring.go")
}
```

**Implementation:**
- Helper `mustFindIdentifierUsage` in `tests/contract/wiring_helpers.go`
- AST-basiert (kein grep) für robuste Refactoring-Stabilität
- Default-Verzeichnis: `tests/contract/wiring_pins/<area>_test.go`
- Konvention: ein Pin-Test pro nicht-trivialer produktiver Methode

**Vorteil:** Refactoring oder versehentliches Wiring-Removal bricht den Test sofort. Dead-Code kann nicht still wiedereinkehren.

### Säule 3: E2E-Boot-Smoke-Tests (verhindert Feature-Drift)

**Was:** Daemon startet gegen `godevccu`-Simulator. Smoke-Tests verifizieren, dass kritische End-to-End-Pfade emitten.

**Implementation:**
- Verzeichnis `tests/e2e/` (heute leer) füllen
- Test-Harness in `tests/e2e/harness/` (boot daemon, attach MQTT-test-client, attach REST/WS-clients)
- Run-Tag `-tags=e2e` (nicht in normalem `go test ./...`)
- Mindest-Inventar (10 Smoke-Tests):
  1. **Boot:** Daemon startet, REST/MQTT/Matter Listener aktiv, `/health` returnt 200
  2. **MQTT-Discovery emitted:** Alle 15 HA-Component-Topics werden publiziert (inkl. Hub-Update, Inbox)
  3. **MQTT-Subscriber wirklich aktiv:** `SetValue`-Befehl auf Command-Topic erreicht godevccu
  4. **Hub-Aggregate publish:** AlarmMessages/ServiceMessages/Inbox/SystemHealth/ConnectionLatency haben aktuelle Werte
  5. **Reconnect:** godevccu trennt → Reconnect → `OnConnect` republished Discovery + AnnounceOnline
  6. **Hot-Plug:** godevccu meldet new device → `HandleNewDevices` triggert → Discovery emitted
  7. **EvaluateCentralState DEGRADED:** ein Interface offline → SystemStatusChangedEvent.State=DEGRADED
  8. **MqttCollector inkrementiert:** alle 6 Counter sind > 0 nach Smoke-Run
  9. **Matter Boot:** Endpoint 0 Cluster ServerList = mounted set, BasicInformation.Reachable=true
  10. **Custom-DP Wire-Round-Trip:** Setzt Switch ON → CCU bestätigt → Subscriber sieht Event

**Vorteil:** Findet Dead-Code-Pattern zur Laufzeit (z.B. MqttCollector nie incrementiert weil nie instanziiert). Findet Wire-Drifts (CCU rejects → Smoke-Test bricht).

### Säule 4: Wire-Encoding-Snapshot-Tests (verhindert Wire-Drifts)

**Was:** Pro Custom-DP-Setter ein Test, der den Wire-Payload als Snapshot-Bytes pinnt — gegen einen aus aiohomematic erzeugten Referenz-Snapshot.

**Implementation:**
- Python-Helper `script/aiohomematic_wire_snapshots.py` (analog `aiohomematic_snapshot.py`) ruft jeden Custom-DP-Setter mit Standard-Input und dumpt die XML-RPC/JSON-RPC-Bytes
- Output: `tests/contract/wire_snapshots/<dp_type>_<setter>.json` (Input → erwartete Wire-Bytes)
- Go-Test `TestWireSnapshots` ruft jeden Setter mit gleichem Input gegen Fake-Transport, vergleicht Wire-Bytes
- Diff-Output zeigt sofort Byte-Drift (LEVEL_COMBINED hex-CSV vs. int32, DpSelect String vs. Integer, etc.)

**Vorteil:** Die 8 wire-blockierenden v16-Drifts (LEVEL_COMBINED, FixedColorLight COLOR, RGBW EFFECT, …) wären sofort gefangen worden. Künftige Wire-Drifts sind compile-time-äquivalent unmöglich.

---

## 3. Migrations-Plan (Reihenfolge der Umsetzung)

### Phase A — Reachability (verhindert weiteren Dead-Code)
1. `script/reachability/main.go` schreiben (~200 LOC, `golang.org/x/tools/go/callgraph`-basiert)
2. Initialen Run: `docs/parity/dead-code-inventory.json` produzieren
3. Inventory in 3 Klassen aufteilen:
   - **A: produktiv-wirebar** → in Säule 2 nachziehen (Pin-Test + Caller)
   - **B: by-design** → `// loom:reachable:reason=...`-Kommentar + Eintrag in `docs/parity/by_design.md`
   - **C: echte Altlast** → einzeln mit User-Approval entfernen (default: nicht löschen)
4. Contract-Test `TestReachability` aktivieren — `make test` blockiert neue Dead-Code-Items

### Phase B — Pin-Tests (verhindert Re-Drift)
1. `tests/contract/wiring_helpers.go` mit AST-basiertem Identifier-Usage-Finder
2. Pin-Tests für alle in Phase A wieder-gewireten Items
3. Pin-Tests für die ~75 v14- + ~65 v15-Closures, die laut v15/v16-Audit produktiv sind (rückwirkend absichern)
4. Konvention: jede neue Implementations-PR muss einen Pin-Test pro produktivem Item enthalten (CONTRIBUTING.md)

### Phase C — Wire-Snapshots (verhindert Wire-Drifts)
1. Python-Snapshot-Generator `aiohomematic_wire_snapshots.py`
2. Snapshots erzeugen für alle 22 Custom-DP-Variants × Setters (~150-200 Snapshots)
3. `TestWireSnapshots` als Contract-Test
4. Snapshot-Regen-Workflow in `make generate-wire-snapshots`

### Phase D — E2E-Boot-Smoke (verhindert Feature-Drift)
1. Test-Harness in `tests/e2e/harness/`
2. 10 Smoke-Tests (siehe Säule 3)
3. CI-Job `make e2e` der weekly läuft (oder als `needs-e2e`-Label-Trigger)

### Phase E — Audit-Schleife einstellen
1. `parity_request.md` als historisches Dokument markieren
2. Re-Audits nur noch bei matter.js / aiohomematic HEAD-Bumps oder bei wirklich tiefgreifenden Refactorings
3. Default-Workflow: Reachability-Bruch oder Pin-Test-Bruch ist das einzige Drift-Signal

---

## 4. Kein-Code-verlieren-Garantie

Konkrete Schutzmaßnahmen:

1. **Reachability-Tool entfernt nichts.** Es listet nur.
2. **Inventory-Liste wird Item-für-Item geprüft.** Default ist "wiren" (Säule 2). Nur explizit als Müll markierte Items werden entfernt — und das nur nach User-Approval.
3. **by-design-Pfad** ist erste Option für architekturell-divergente Surface, kein Lösch-Pfad.
4. **Pin-Tests verhindern unbeabsichtigtes Entfernen** durch zukünftige Refactorings.
5. **Wire-Snapshots können verifiziert werden** — bevor sie übernommen werden, kann der Diff manuell geprüft werden.

Die einzige Stelle, wo Code wirklich verschwindet, ist die Phase-A-Klasse-C-Entscheidung (echte Altlast). Diese hat ein explizites User-Approval-Gate.

---

## 5. Was passiert mit laufender v16-P0-Welle und offenen Audits

1. **v16 P0-Welle 1 (A2 Wire-Encoding)** läuft noch — sie fixt 8 wire-blockierende Drifts und ist unabhängig vom strukturellen Ansatz wertvoll. Abwarten, committen.
2. **v16 P1/P2-Wellen werden NICHT mehr gestartet** — stattdessen läuft Phase A des strukturellen Ansatzes los.
3. **Detail-Reports `/tmp/parity_v16_*.md`** bleiben als Input für die Phase-A-Inventory (sie listen ja bereits viele Dead-Code-Kandidaten).
4. **`docs/parity/by_design.md`** wird weiter geführt — die ~200 Einträge sind bereits gut. Neue Einträge kommen aus Phase A Klasse B.

---

## 6. Erfolgs-Kriterien

Die Migration ist erfolgreich, wenn:

- `make test` schlägt fehl, sobald ein Identifier ohne Production-Caller hinzugefügt wird (Phase A)
- Pin-Test bricht, sobald jemand ein Wiring versehentlich entfernt (Phase B)
- Wire-Snapshot bricht, sobald ein Wire-Encoding driftet (Phase C)
- E2E-Smoke bricht, sobald ein Feature nicht mehr End-to-End funktioniert (Phase D)
- v17-Audit findet **null** Dead-Code-Items (Pattern strukturell tot)
- v17-Audit findet **null** Wire-Drifts (Pattern strukturell tot)

---

## 7. Entschiedene Optionen (User-Wizard 2026-05-29)

1. **Reachability-Tool:** **Selbst bauen** unter `script/reachability/main.go` (~200 LOC, `golang.org/x/tools/go/callgraph`-basiert). Whitelist via `// loom:reachable:reason=...`-Kommentar.
2. **Wire-Snapshots:** **Vollständig ~150-200 Setter** (alle 22 Custom-DP-Variants × alle Setter). Maximaler Schutz, ~1 Tag Generator-Arbeit.
3. **E2E-Simulator:** **Hybrid godevccu + embedded Mosquitto** (`eclipse/mosquitto-go-embed` oder Äquivalent). godevccu für CCU-Seite, Embedded-MQTT für Discovery/Subscriber-Tests.
4. **Reihenfolge:** **A → B → C → D sequentiell.** A baselined alles, B+C arbeiten dann Item-für-Item ab, D als oberste Defense zum Schluss.

---

## 8. Erweiterte Konkretisierung (nach Wizard)

### Phase A — Reachability (~1 Tag)

**Tool-Spec:**
- Entry-Points: `cmd/openccu-loom/main.go`, `cmd/hmcli/main.go`, alle `_test.go`-Dateien
- Algorithmus: `golang.org/x/tools/go/callgraph/rta` (Rapid Type Analysis — präziser als CHA für Interface-Calls)
- Output-Format:
  ```json
  {
    "generated": "2026-05-29T...",
    "head": "a1f281f",
    "entry_points": ["main.go"],
    "unreachable": [
      {
        "package": "internal/health",
        "identifier": "ConnectionTracker",
        "file": "internal/health/connection.go",
        "line": 23,
        "kind": "type"
      }
    ],
    "whitelisted": [...]
  }
  ```
- Whitelist-Syntax:
  ```go
  // loom:reachable:reason="HA-Discovery-Builder, runtime-mounted via embedded Sw"
  func BuildSwitchDiscovery(...) DiscoveryItem { ... }
  ```
- Contract-Test `TestReachability` in `tests/contract/reachability_test.go`:
  - Lädt `docs/parity/dead-code-inventory.json`
  - Vergleicht aktuellen Run mit Baseline
  - Bricht wenn neue ungewirter Identifier hinzugefügt → fordert Whitelist-Kommentar oder Production-Caller

### Phase B — Pin-Tests (~3-5 Tage)

**Helper-Spec:**
```go
// tests/contract/wiring_helpers.go
func MustFindCaller(t *testing.T, callerFile, calleePackage, calleeIdent string)
func MustFindFieldUsage(t *testing.T, ownerFile, fieldName string)
func MustFindInterfaceImpl(t *testing.T, ifacePackage, ifaceName string, expectedImpls ...string)
```

**Pin-Test-Inventur (rückwirkend für v14/v15/v16):**
- ~140 produktiv-gewirte Items werden mit Pin-Tests abgesichert
- Konvention: `tests/contract/wiring_pins/<area>_test.go` (z.B. `a5_central_test.go`)
- CONTRIBUTING.md erweitert: jede neue Implementations-PR braucht Pin-Test pro produktivem Item

### Phase C — Wire-Snapshots (~3-4 Tage)

**Generator-Spec:**
- `script/aiohomematic_wire_snapshots.py` (analog `aiohomematic_snapshot.py`)
- Iteriert alle Custom-DP-Klassen × alle Setter-Methoden
- Pro Setter: feste Input-Matrix (z.B. Cover.SetLevel: [0, 0.25, 0.5, 0.75, 1.0])
- Fake-Transport captureed XML-RPC/JSON-RPC-Bytes
- Output: `tests/contract/wire_snapshots/<dp_type>_<setter>.json`
- Erwartete Snapshot-Größe: ~150-200 Setter × 5-10 Inputs = 750-2000 Snapshot-Entries

**Go-Test:**
```go
// tests/contract/wire_snapshot_test.go
func TestWireSnapshots(t *testing.T) {
    for _, snap := range loadWireSnapshots() {
        gotBytes := runGoSetter(t, snap.DPType, snap.Setter, snap.Input)
        if !bytes.Equal(gotBytes, snap.ExpectedWireBytes) {
            t.Errorf("wire drift %s.%s(%v):\nexpected: %x\ngot: %x",
                snap.DPType, snap.Setter, snap.Input,
                snap.ExpectedWireBytes, gotBytes)
        }
    }
}
```

**Regen-Workflow:** `make generate-wire-snapshots` ruft Python-Generator, schreibt JSON-Files. Git-Diff zeigt sofort jede Wire-Änderung.

### Phase D — E2E-Boot-Smoke (~2 Tage)

**Test-Harness:**
- `tests/e2e/harness/daemon.go`: bootet OpenCCU-Loom mit Test-Config + Test-Ports
- `tests/e2e/harness/godevccu.go`: bootet godevccu in goroutine, registriert Devices
- `tests/e2e/harness/mqtt.go`: bootet embedded Mosquitto, attached Test-Client
- `tests/e2e/harness/rest.go`: HTTP-Client für REST/WS-Assertions

**10 Smoke-Tests** (siehe Säule 3 in Abschnitt 2). Tagged mit `// +build e2e`.

**CI:** `make e2e` läuft weekly im scheduled job. PR-Trigger via `needs-e2e`-Label.

---

## 9. Abdeckung der offenen v16-Drifts durch die Säulen

Nach v16 P0-Welle (18 Items adressiert) verbleiben ~71 offene Items aus v16-P1/P2. Diese werden NICHT in separaten Audit-Wellen abgearbeitet, sondern durch die Säulen automatisch entdeckt:

| Drift-Klasse | Anzahl | Säule | Mechanismus |
|---|---:|---|---|
| Dead-Code (Methode/Builder ohne Caller) | ~30 | Phase A | Tool listet automatisch |
| Wire-Encoding-Drifts (Names, Datentypen) | ~15 | Phase C | Snapshot-Byte-Diff |
| Feature-Drifts (Funktion existiert, nicht aktiv) | ~10 | Phase D | Daemon-Boot + Smoke-Check |
| Matter Konstanten/Schema | 3 | Phase F | manuell |
| chip Security/Spec-Conformance P1/P2 | 5 | Phase F | manuell |
| Cosmetic/Hygiene | ~8 | manuell oder ignorieren | — |

**Workflow:**
1. Phase A Inventory listet ~30 Dead-Code-Items, davon ~20 aus v16-P1/P2-Liste — Klasse-A-Wiring erledigt diese als Side-Effect
2. Phase C Wire-Snapshots zeigen alle Wire-Drifts aus v16-A2/A4-P1/P2 als Byte-Diff
3. Phase D E2E-Smoke macht Feature-Drifts zur Laufzeit sichtbar
4. Phase F arbeitet die ~12 strukturell-nicht-erfassbaren Items manuell ab

**Konsequenz:** Kein v16-Drift bleibt unbehandelt. Das System (Säulen) ersetzt die manuelle Audit-Liste als Drift-Detector.

---

*Geschrieben als Strategie-Dokument nach v16-Audit-Welle. Optionen via User-Wizard 2026-05-29 entschieden.*

---

## 10. Stand 2026-05-29 — Säulen aktiv

Phase A (Reachability): committed `8b4b309`, `f5e1443`. Initial-Inventory
406 → 3 echte Dead-Code-Items übrig (alle externe API; siehe
docs/parity/dead-code-classification.md).

Phase B (Pin-Tests): committed `f5e1443`. 23 Pin-Tests + 1 CSRF-Pin = 24
Wiring-Anker für v14/v15/v16-Closures.

Phase C (Wire-Snapshots): committed `45546cd`. 24 Snapshots / 34 Entries
für Switch/Cover/Blind/ClimateIP/Valve/ModulatingValve/Lock/Siren.

Phase D (E2E-Smoke): committed `45546cd`. 10 Tests, 6 PASS / 4 SKIP.
SKIP-Items dokumentiert in jeweiligem Test-File für späteren Fix.

Phase E (Audit-Schleife einstellen): dieser Commit. parity_request.md
als historisch markiert, CONTRIBUTING.md erweitert.

Phase F (Residual-Drifts): folgt — Matter D-16-01/02/03 + chip P1/P2.

**Echter v16-Befund während Phase A:** CSRFMiddleware war implementiert
aber nicht in REST-Pipeline gemounted. Fix in `45546cd`. Production-
Caller-Pin verhindert Regression.
