# CCU-WebUI-Ersatz: Gap-Analyse und offene Punkte

Erhebung: 2026-07-23 (Basis OpenCCU-Loom 0.47.0) · Umsetzungsstand
nachgezogen: 2026-07-31 (OpenCCU-Loom 0.51.0, `main`) · Vergleichsbasis:
occu (CCU3-WebUI, `../occu/WebUI/www/`), OpenCCU
(`../OpenCCU/buildroot-external/`).
`OpenCCU-Base` ist lokal nicht ausgecheckt; die Distributions-Ebene
(Overlays, occu-Patches, Recovery, Add-on-Ökosystem) ist über das
OpenCCU-Repo mit abgedeckt.

Methodik: Multi-Agent-Analyse (30 Agenten) — parallele Funktionsinventare
der klassischen WebUI (webui.js, ReGa-esp-Seiten, Tcl-CGIs, JSON-RPC-API,
OpenCCU-Patches) und von Loom (SPA, openapi.yaml, wsapi.json, internal/),
danach Gap-Prüfung pro Funktionscluster direkt gegen den Loom-Code mit
adversarialer Verifikation jedes missing/partial-Befunds und einem
Vollständigkeits-Kritiker. Alle zitierten Dateipfade wurden am Code
verifiziert.

**Entscheidungs-Konvention:** Jeder offene Punkt in §4 und §6 trägt eine
stabile ID (z. B. `PR02`) und ein Feld **Entscheidung:** `offen`. Ersetze
den Wert durch `umsetzen`, `zurückstellen` oder `ausschließen` (optional
mit Kommentar dahinter, z. B. `` `umsetzen` — Welle 2 ``). Auswertung:
`grep -n 'Entscheidung:' docs/ccu-webui-gap-analysis.md`. `abgelehnt`
wird als Synonym für `ausschließen` gewertet.

**Entscheidungsstand 2026-07-21:** 51 × `umsetzen` · 43 × `offen`
(u. a. der komplette Programmeditor-Block PR01–PR06, Favoriten/Benutzer
O01–O05, Systemsteuerung SY01–SY20) · 3 × `abgelehnt` (E01, E03, E04).
Der Umsetzungsplan in §7 enthält nur die beschlossenen Punkte.
(Das `grep` findet heute nur noch die **unerledigten** Punkte — §4 wurde am
2026-07-31 auf sie eingedampft, die 48 gelieferten Punkte stehen als
Ein-Zeilen-Index in §8.)

**Umsetzungsstand 2026-07-31 (Basis 0.51.0):** Alle sechs beschlossenen
Wellen (§7) sind **geliefert** — Wellen 1a–1h + GR01 bis 0.46.0, Wellen 3,
5, 6 sowie die Welle-4-Kernpunkte G11/G14 mit 0.47.0, G12 mit API 2.51.0
und die komplette **Welle 2 (GR02–GR05) mit 0.48.0** (jpages-Proxy,
live gegen echte Hardware verifiziert — Wire-Wissen in der
[jpages-Gruppenreferenz](./ccu-jpages-groups-reference.md)).

Von den beschlossenen (`umsetzen`) Punkten bleiben genau **drei** offen:

- **G08** und **G16** — bewusst zurückgestellt, beide durch fehlende
  Hardware blockiert (Details am jeweiligen Punkt in §4.4).
- **V06** (eigene LINK-Profilvorlagen) — als `umsetzen` beschlossen, aber
  in **keiner** Welle des §7-Plans eingeplant und nie gebaut. Im Code
  existiert nur der eingebaute Easymode-Katalog
  (`internal/store/linkprofile`, datei-getrieben); ein Benutzer-Store und
  ein `/link-profiles`-CRUD fehlen. Der Punkt braucht eine Wellen-Zuordnung
  oder eine neue Entscheidung.

Dazu kommen die **Restposten aus erledigten Punkten** (§4.11) — kleine
Reste ohne eigene ID, keine eigenen Wellen.

**Aufbau seit 2026-07-31:** §4 enthält nur noch Unerledigtes; §8 ist der
Ein-Zeilen-Index der gelieferten Punkte (die IDs bleiben damit auflösbar,
etwa für die `Ref: docs/ccu-webui-gap-analysis.md <ID>`-Zeilen in älteren
Commits). Was ein gelieferter Punkt konkret gebracht hat, steht im
`CHANGELOG.md` unter der im Index genannten Version.

**Frequenz-Einstufung (2026-07-31):** Jeder unerledigte Punkt trägt jetzt
zusätzlich ein Feld **Frequenz:** `hoch` / `mittel` / `corner` — die
geschätzte Nutzungshäufigkeit aus Bedienersicht, unabhängig von Priorität
und Aufwand (Legende in §4). Verteilung: 6 × `hoch`, 11 × `mittel`,
21 × `corner`. Daraus abgeleitet schlägt §7 zwei Abschlusswellen vor
(**7 Alltagsflächen**, **8 Zentralen-Wartung & Korrektheit**); beide sind
Vorschläge und ersetzen die Einzelentscheidungen nicht. Der
Programmeditor-Block bleibt bewusst unbewertet.

---

## 1. Zusammenfassung

**171 geprüfte Einzelfunktionen** der klassischen CCU-WebUI (inkl.
OpenCCU-Erweiterungen): **38 abgedeckt**, **55 teilweise**, **64 fehlend**,
**14 bewusst nicht anwendbar** (vor Dubletten-Bereinigung; einige Punkte
tauchen in mehreren Clustern auf und sind unten zusammengeführt — §4
enthält die bereinigte, einzeln entscheidbare Liste mit ~90 Punkten).

Die Bedien-Ebene (Geräte, Kanäle, Werte, MASTER-Parameter, Direktverknüpfungen,
Räume/Gewerke, Systemvariablen-Grundfunktionen, Servicemeldungen, Backup/Update
auf OpenCCU, Anlernen/Posteingang) ist weitgehend erreicht — dort dominieren
kleine partial-Lücken. Die fünf großen strukturellen Blöcke, die einem
Vollersatz heute im Weg stehen:

1. **Programmeditor** (Wenn/Dann/Sonst-Regelbaum, Zeitmodule/Astro,
   Aktivitäten, Skript-Aktivitäten, Skript-Testfenster) — größte Einzellücke;
   nur über HM-Script-DOM-Manipulation via ReGa-Runner erreichbar.
2. ~~**Gruppenverwaltung** (HmIP-/BidCos-Heizungsgruppen) — komplette
   CRUD-Orchestrierung fehlt; läuft CCU-seitig über die HMServer-Endpunkte
   `/pages/jpages/group/*`, die Loom heute gar nicht anspricht.~~
   **Geschlossen mit 0.48.0** — Loom proxyt die jpages-Endpunkte mit der
   eigenen JSON-RPC-Session (ADR 0055).
3. **Diagramme & Favoriten-Editor** — Loom-native Features ohne
   CCU-Schnittstellenbedarf (Diagramm-Definitionen, Multi-Serien-Charts,
   benannte Favoritenseiten mit Layout).
4. **Systemsteuerung der Zentrale** — Sicherheitsschlüssel, Firewall, SSH,
   NTP/Position, CCU-Reboot/Recovery, CCU3-Backup-Fallback; teils per
   JSON-RPC/ReGa lösbar, teils OS-gebunden.
5. **Drittanbieter-Kompat-API** (`/api/homematic.cgi`-Nachbau) — nur relevant,
   wenn Loom der einzig exponierte Endpunkt sein soll; ADR-pflichtige
   Grundsatzentscheidung.

**Strukturelle Erkenntnis:** Fast alle noch fehlenden CCU-Funktionen sind über
genau drei bereits vorhandene Loom-Kanäle erreichbar — (a) XML-RPC-Methoden,
die im Client-Layer nur ergänzt werden müssen, (b) JSON-RPC-Methoden der CCU,
(c) HM-Script über den vorhandenen ReGa-Runner (`internal/client/rega/`).
Ein harter Rest (Netzwerkkonfig, Addon-Installation, LED, CCU-Logdateien,
Werksreset) braucht OS-Zugriff auf der Zentrale und sollte per ADR entweder
zum Non-Goal erklärt oder über einen künftigen CCU-seitigen Agenten gelöst
werden (vgl. §6).

---

## 2. Wie ist die klassische CCU-WebUI an das Backend angebunden?

Die klassische WebUI ist **kein einheitlicher API-Client**, sondern ein
Geflecht aus drei parallelen Pfaden, die vor lighttpd zusammenlaufen
(Routing: lighttpd `conf.d/proxy.conf` + `cgi.conf` in
`../occu/arm-gnueabihf-gcc8/packages/lighttpd/etc/lighttpd/`):

- Alles, was **nicht** auf `^/(config/|upnp/|webui/|ise/|api/|tools/|pda|pages/jpages|addons)`
  passt, wird per Reverse-Proxy an `127.0.0.1:8183` weitergereicht — den
  **eingebauten Webserver der ReGaHss** (Logikschicht; der klassische Port
  8181 forwardet ebenfalls dorthin). `/pages/jpages` geht an den
  Java-HMIPServer (Port 9292).
- `.cgi`-Dateien unter `/config/`, `/api/`, `/ise/` werden von lighttpd als
  **Tcl-CGI** (`tclsh`) ausgeführt.

### Die drei Aufrufpfade von `webui.js` (~1,9 MB Bundle)

1. **JSON-RPC** `/api/homematic.cgi` — ~363 Aufrufstellen über den Wrapper
   `homematic()` (dominiert von `Interface.*`, `SysVar.*`, `CCU.*`).
   `homematic.cgi` ist ein Tcl-Dispatcher: `methods.conf` registriert
   173 Methoden in 19 Namespaces mit Privilegstufen; jede Methode ist ein
   Tcl-Skript unter `api/methods/`, das seinerseits entweder
   **HM-Script gegen die ReGa** absetzt (`tclrega.so` →
   `http://127.0.0.1:8183/tclrega.exe`; ~70 Skripte), **XML-RPC gegen die
   Interface-Prozesse** spricht (`tclrpc.so`, Interface-Auflösung über
   `/etc/config/InterfacesList.xml`; ~51 Skripte) oder direkte
   Tcl-/Shell-Systemadministration betreibt (`CCU.*`, `Firewall.*`).
   Die JSON-RPC-Methoden sind also reine Wrapper ohne eigene Logik.
2. **Rohe HM-Script-POSTs an ReGa-esp-Seiten** — ~120 Aufrufstellen posten
   HomeMatic-Script direkt als POST-Body an `/esp/*.htm?sid=…`
   (`system.htm`, `programs.htm`, `channels.htm`, `favorites.htm`, …).
   Die esp-Seiten (`WebUI/www/rega/esp/`) sind ReGa-Templates, die in
   `.fn`-HM-Script-Bibliotheken dispatchen. **Programmeditor, Räume/Gewerke,
   Systemvariablen und Favoriten laufen komplett an der JSON-API vorbei**
   direkt in die ReGaHss.
3. **Tcl-CGIs** — ~40 Referenzen auf `ic_*.cgi`/`cp_*.cgi` unter `/config/`
   (Anlernen, Direktverknüpfungen, Geräte-Firmware, Systemsteuerung); diese
   CGIs sprechen serverseitig wiederum XML-RPC (`addLink`, `putParamset`,
   `updateFirmware`, …).

Session/Auth: Login delegiert an die ReGa-Loginseite; die Session-ID
(`@…@`-Format) lebt **in der ReGa selbst** (`system.GetSessionVarStr`),
Privilegstufen NONE/GUEST/USER/ADMIN werden pro JSON-Methode geprüft.
Ein `tclrega.exe`-Aufruf aus dem Browser findet nicht statt (OpenCCU sperrt
`.exe`-URLs von extern explizit); der Browser-Skriptkanal ist `esp/exec.htm`.

### Werden Standardschnittstellen verwendet?

| Pfad | Status | Nutzung durch die WebUI |
|---|---|---|
| **XML-RPC-API** der Interface-Prozesse (rfd :2001 als BIN-RPC, HMIPServer :2010, VirtualDevices :9292) | dokumentierter eQ-3-Standard | nur **indirekt** serverseitig (aus Tcl); nie aus dem Browser |
| **JSON-RPC-API** `/api/homematic.cgi` | halboffiziell, selbstbeschreibend, De-facto-Standard für Drittsoftware | ~363 Aufrufe, aber intern nur dünne Tcl-Fassade über HM-Script + XML-RPC |
| **esp-Seiten + Tcl-CGIs + HM-Script** | proprietär, undokumentiert, session-gekoppelt | die Kernbereiche Programme, Räume/Gewerke, Sysvars, Favoriten, Anlern-/Verknüpfungslogik |

**Antwort kurz:** Teilweise. Die WebUI nutzt die halboffizielle JSON-RPC-API
intensiv, umgeht sie aber für ihre Kernbereiche mit proprietären
ReGa-/Tcl-Pfaden. Die dokumentierte XML-RPC-Standardschnittstelle (die Loom
spricht) wird vom Browser nie direkt genutzt. Konsequenz für Loom: die
JSON-RPC-Methodenliste (`methods.conf`) ist eine gute Feature-Landkarte,
deckt den WebUI-Funktionsumfang aber nicht ab — der Rest ist nur als
HM-Script-Semantik aus den esp-`.fn`-Quellen rekonstruierbar.

---

## 3. Bewertung: eigenes Modell zur Anbindung der SPA

Vergleich der Architekturen — (A) klassische WebUI (serverseitig auf der
Zentrale verwoben, Zustand = ReGaHss, kein eigenständiges Domänenmodell)
vs. (B) OpenCCU-Loom (typisiertes Go-Domänenmodell `internal/model/`,
gespeist über XML-RPC/BIN-RPC-Callbacks + JSON-RPC, SPA über eigene
REST+WS-API):

| Achse | Klassische WebUI (A) | Loom-Modell (B) |
|---|---|---|
| Push/Latenz | kein Browser-Push; `Event.poll` = dateibasiertes Polling über `/tmp/event/` | Callback-Events → typisierter Event-Bus → WS-Broadcast; Latenz = ein Event-Hop |
| Multi-CCU | strukturell Ein-Zentralen-UI | erstklassig (`CentralRegistry`, `central_name`-Scoping, ADR 0002) |
| Auth/Security | ReGa-Sessions; mit gültiger Session beliebige HM-Script-Ausführung (faktisch Code-Execution als Feature) | eigene Auth-Schicht (Basic/Session/OIDC/Token), TLS, Secret-Maskierung, Audit-Log |
| Typsicherheit | dreifaches String-Templating (Tcl, HM-Script, 1,9-MB-JS) ohne Schema | durchtypisiert, spec-getrieben (OpenAPI/wsapi.json), Contract-Tests |
| Offline/Cache | UI = Zentrale; fällt mit ihr aus | SQLite-Snapshot, Warm-Start ohne Funkkosten, ehrliches „waiting for CCU" |
| Erweiterbarkeit | jede weitere Fläche = neuer Tcl-Flickenteppich | dasselbe Modell speist REST/WS, MQTT und Matter |
| Risiken | — | Semantik-Drift zur CCU (durch Parity-Guards bewacht), Nachbau-Aufwand pro neuer CCU-Firmware-Funktion |

**Vollständigkeitsgrenze — der Kernpunkt:** Über die Standard-XML-RPC-API
sind nur Geräte, Paramsets und Direktverknüpfungen erreichbar. Alles
ReGa-Residente erzwingt HM-Script — genau das kapselt Loom bereits
kontrolliert in 27 versionierten Skripten unter
`internal/client/rega/scripts/` (Sysvars, Räume/Gewerke, Programme lesend +
schaltend, Backup, Firmware, Inbox, Servicemeldungen). Nicht sauber
erreichbar über irgendeine dokumentierte API: Programme-CRUD (nur
esp-`.fn`-Interna), Sicherheitsschlüssel, Netzwerk/Firewall/SSH und
OS-Wartung — in der Original-WebUI reine Tcl-Systemadministration auf der
Zentrale.

**Fazit:** Das eigene Modell ist für den WebUI-Ersatz klar der richtige
Ansatz — Push statt Polling, Multi-CCU, moderne Auth, Offline-Robustheit und
Mehrfachverwertung (MQTT/Matter) sind mit der Tcl/esp-Architektur strukturell
unerreichbar. Bemerkenswert: Auch die Original-UI läuft an ihrer eigenen
JSON-API vorbei — es gibt also keine „Standard-API", die man stattdessen
hätte konsumieren können. Ein 100-%-Ersatz ist als **Hybrid** zu denken:
eigenes Modell als Fundament, der schmale versionierte ReGa-Skript-Kanal als
kontrolliertes Durchgriffs-Ventil (Programme, Systemfunktionen), plus eine
per ADR zu entscheidende Antwort auf die OS-gebundenen Restfunktionen.

---

## 4. Offene Punkte — einzeln entscheidbar

Dieser Abschnitt führt **nur noch die unerledigten Punkte**. Die 48
gelieferten Punkte sind auf je eine Zeile in §8 eingedampft; ihre
Beschreibung steht im `CHANGELOG.md` unter der dort genannten Version.
Bereichsnummerierung und IDs bleiben stabil — die vollständig gelieferten
Bereiche 4.1 (Quick Wins) und 4.3 (Gruppenverwaltung) sind entfallen, 4.11
sammelt jetzt die Restposten aus erledigten Punkten.

Legende: Status `partial`/`missing` · Priorität P1 (Kernfunktion für
WebUI-Ersatz) / P2 (wichtig) / P3 (nice-to-have) · Aufwand S/M/L/XL.
Empfohlene CCU-Schnittstelle jeweils am Ende der Empfehlung. Abgedeckte
Funktionen je Bereich sind in §5 zusammengefasst.

**Feld `Frequenz` (eingetragen 2026-07-31):** geschätzte Nutzungshäufigkeit
aus Bedienersicht, unabhängig von Priorität und Aufwand — die Frage
„gehört das in die SPA, weil man es dauernd braucht?".

- `hoch` — wird regelmäßig bis bei jedem Seitenaufruf benutzt, oder einmal
  gesetzt, wirkt dann aber auf jede Nutzung (z. B. Astro-Koordinaten).
- `mittel` — punktuell, aber vorhersehbar wiederkehrend; oder selten und
  dann im Ernstfall (Backup/Restore).
- `corner` — Einmal-Einstellungen, Debugging-Nischen, einzelne
  Gerätefamilien. Bei den CCU-Systemschaltern gilt zusätzlich: die
  CCU-WebUI ist pro Zentrale ohnehin verlinkt, dort kosten sie null
  Wartung — in Loom kosten sie dauerhaft Pflege bei jedem Firmwarewechsel.

Auswertung: `grep -n 'Frequenz:' docs/ccu-webui-gap-analysis.md`. Der
Programmeditor-Block (§4.2) trägt bewusst **keine** Einstufung, die
abgelehnten Punkte (E01, E03, E04) und die ADR-Fragen in §6 ebenfalls nicht.

### 4.2 Programme, Zentralenverknüpfungen & HM-Script

Der gesamte Block bleibt von der Frequenz-Einstufung ausgenommen: die
Entscheidung über Programm- und Skripteditor steht vor der Frage, wie oft
man sie benutzen würde (ADR A4 ist der Sicherheitsrahmen, ohne den PR01
nicht beginnen darf).

- **PR01 — HM-Script-Konsole (Skript-Testfenster)** (missing, P1 M)
  Kein öffentlicher run-script-Endpunkt; der ReGa-Runner existiert nur
  intern. Technischer Wegbereiter für den Programmeditor (gleicher
  Schreibpfad) und deckt zugleich `ReGa.runScript` für Drittanbieter ab
  (E01). *Empfehlung:* `POST /api/v1/rega/script` auf
  `internal/client/rega/runner.go`, admin-only, Skript-Klartext ins
  Audit-Log, Confirm in der SPA; Route `#/scripts` mit Editor + Ausgabe.
  **Entscheidung:** `offen`
- **PR02 — Programmeditor-Grundgerüst** (missing, P1 L)
  Programm anlegen/umbenennen/löschen + Wenn/Dann/Sonst-Regelbaum
  lesen/schreiben — heute toggelt PATCH nur das active-Flag. Deckt auch
  Zentralenverknüpfungen ab (die WebUI bildet sie als Programme ab).
  *Empfehlung:* HM-Script-DOM-Manipulation (`dom.CreateObject(OT_PROGRAM)`,
  `oProgram.Rule()`; Semantik-Vorbild esp-`.fn` + XML-API-Addon) als neue
  kuratierte `.fn`-Skripte; typisiertes Regelbaum-DTO in `pkg/hmapi`,
  REST/WS-CRUD, SPA-Route `#/programs/{id}/edit`. *ReGa.*
  **Entscheidung:** `offen`
- **PR03 — Bedingungseditor** (missing, P1 L)
  Kanalzustand/Sysvar/Zeit, UND/ODER, Wertebereiche, Sonst-wenn-Zweige,
  Trigger-Modi. *Empfehlung:* SingleCondition-Semantik 1:1 aus
  `rega/esp/sico.fn`; volle ReGa-Operatormenge von Anfang an (≥/≤/Negation —
  OpenCCU 0019/0077 schalten sie nur UI-seitig frei). Setzt PR02 voraus.
  *ReGa.*
  **Entscheidung:** `offen`
- **PR04 — Zeitmodul inkl. Astrofunktion** (missing, P1 M)
  Einmalig/periodisch/…, Zeitspannen, Sonnenauf-/-untergang mit Offset.
  *Empfehlung:* Kalender-Datenpunkte am Programm per ReGa-Skript; die
  Astro-Berechnung macht die CCU selbst aus den Koordinaten — Loom bietet
  nur die Typen im Editor an. Setzt PR02 und SY05 (Position) voraus. *ReGa.*
  **Entscheidung:** `offen`
- **PR05 — Aktivitäten (Dann/Sonst)** (missing, P1 M)
  Kanalaktion, Sysvar setzen, Verzögerung (Sek/Min/Std), Retrigger-Flag.
  *Empfehlung:* SingleDestination-Objekte analog `rega/esp/side.fn`; die
  typgerechten Wert-Widgets existieren in `lib/control/` bereits. Setzt
  PR02 voraus. *ReGa.*
  **Entscheidung:** `offen`
- **PR06 — Skripteditor als Programmaktivität + Syntaxprüfung** (missing, P2 M)
  HM-Skripte als Aktivität anlegen/bearbeiten, serverseitiger SyntaxCheck
  (`system.fn::SyntaxCheck`; OpenCCU 0046 ersetzt den Editor durch
  CodeMirror). *Empfehlung:* Skript-Ziel-Destination via ReGa-Runner;
  SPA-Editor mit lokal gebundeltem Syntax-Highlighting; SyntaxCheck per
  ReGa-Skript. Setzt PR02 voraus. *ReGa.*
  **Entscheidung:** `offen`
- **PR09 — Programm-Sichtbarkeitsflag** (missing, P3 S)
  ReGa-Visible-Flag wird weder gelesen noch geschrieben. Geringer Nutzen,
  da Loom ein eigenes Rollenmodell hat. *Empfehlung:* `visible` in
  Fetch-Skript + DTO + PATCH, Spalte in der Liste. *ReGa.*
  **Entscheidung:** `offen`
- **PR10 — „Einstellungen als neues Programm speichern"** (missing, P3 M)
  Aus dem MASTER-Editor ein Programm erzeugen, das die Parametrierung erneut
  anwendet. *Empfehlung:* erst nach PR02 sinnvoll; Button im ChannelPanel,
  Programm mit Skript-Aktivität generieren. *ReGa.*
  **Entscheidung:** `offen`

### 4.4 Geräteverwaltung & Bedienung

- **G08 — Anlernen per Seriennummer (BidCos `addDevice` + `setTempKey`)** (partial, P3 M)
  Gezieltes Anlernfenster existiert; es fehlen der aktive
  addDevice-Fetch-Pfad und der Geräte-AES-Key-Dialog bei Key-Mismatch.
  *XML-RPC.*
  **Frequenz:** `corner` — nur beim Anlernen alter BidCos-Geräte mit Key-
  Mismatch
  **Entscheidung:** `umsetzen` — **zurückgestellt** (2026-07-23): Recon
  vollständig (BidCos-RF-only: `addDevice`/`setTempKey`/
  `getKeyMismatchDevice`). Blocker: der Key-Mismatch-Fault-Code ist nur
  gegen echte BidCos-RF-Hardware sicher zu mappen, godevccu simuliert
  kein Anlernen. Reaktivieren, sobald ein BidCos-RF-Gerät + Live-Freigabe
  vorliegt.
- **G16 — Gerätespezifische Spezialdialoge** (partial, P3 L)
  E-Paper-/Statusdisplay-Zeileneditor, Jalousie-Hex-Hilfe,
  HmW-IO-Typ-Umschaltung, gemeinsames Editieren von Kanalgruppen (häufigste
  Fälle wie Klima-Party/RGBW/Display-Text sind abgedeckt). *Empfehlung:*
  priorisiert E-Paper-Editor als CDP-Erweiterung; Kanalgruppen im ui-schema
  als Gruppe mit Write-Fan-out.
  **Frequenz:** `corner` — je Dialog eine einzelne Gerätefamilie
  **Entscheidung:** `umsetzen` — **zurückgestellt** (2026-07-23): Der
  E-Paper-Kern (HM-Dis-EP-WM55) hängt am SUBMIT-Hexstring-Byte-Layout,
  das weder in occu noch aiohomematic steht und nur gegen ein echtes
  HM-Dis-EP-WM55 verifizierbar ist (Gerät nicht verfügbar). Die HmIP-
  Display-Familie (HmIP-WRCD/SDV) ist bereits über die `textdisplay`-CDP
  abgedeckt. Reaktivieren nur mit realer HM-Dis-EP-WM55-Validierung.

### 4.5 Direkte Verknüpfungen & Zentralenverknüpfungen

- **V06 — Eigene Profilvorlagen (Benutzer-Easymodes)** (missing, P3 M)
  Bewährte LINK-Parametrierung als wiederverwendbare Vorlage. *Empfehlung:*
  SQLite-Store + REST-CRUD `/link-profiles` + „Als Vorlage speichern" im
  ProfileSelector. Rein Loom-seitig.
  **Frequenz:** `mittel` — lohnt erst bei vielen gleichartigen Verknüpfungen
  **Entscheidung:** `umsetzen` — ⚠️ **offen, ohne Wellen-Zuordnung**
  (bemerkt 2026-07-31): Der Punkt ist beschlossen, taucht aber in keiner
  Welle des §7-Plans auf und ist nie gebaut worden. Vorhanden ist nur der
  eingebaute, datei-getriebene Easymode-Katalog
  (`internal/store/linkprofile`, gelesen über `links.get_profiles` /
  `links.test_profile`) — es fehlen der Benutzer-Store, das
  `/link-profiles`-CRUD und der „Als Vorlage speichern"-Einstieg im
  ProfileSelector.
  Entweder einer künftigen Welle zuordnen oder neu entscheiden.

### 4.6 Organisation: Favoriten, Benutzer

- **O01 — Favoritenseiten bedienbar machen** (partial, P2 M)
  Geräte-Pins sind reine Sprungkarten; Kanäle und Programme sind nicht
  pinbar. *Empfehlung:* Kachel-Technik aus `Overview.svelte` (CDP-/
  Auto-Tiles) wiederverwenden, Pin-Typen um `channel`/`program` erweitern.
  Rein Loom-seitig.
  **Frequenz:** `hoch` — Favoriten sind die Alltagsfläche (Wandtablet,
  Startseite); heute nur Sprungkarten
  **Entscheidung:** `offen`
- **O02 — Favoriten-Editor (Seiten, Reihenfolge, Layout)** (missing, P2 L)
  Mehrere benannte Seiten, Drag-and-Drop-Reihenfolge, Trennzeilen,
  Spaltenzahl. *Empfehlung:* serverseitiges Seitenmodell (SQLite +
  REST-CRUD `/favorite-pages`); CCU-Layout-Artefakte (Ausrichtung,
  Namensposition) entfallen. Rein Loom-seitig.
  **Frequenz:** `corner` — sinnvoll erst nach O01 und nur bei mehreren
  Seiten/Nutzern
  **Entscheidung:** `offen`
- **O03 — Startseite pro Benutzer** (missing, P2 S)
  Konfigurierbare Einstiegsroute nach Login. *Empfehlung:* `start_route`
  über den vorhandenen `/me/preferences`-Store; `App.svelte` wertet sie
  beim Initial-Load aus; Auswahlfeld in Settings.
  **Frequenz:** `hoch` — greift bei jedem Login
  **Entscheidung:** `offen`
- **O04 — Favoriten-Zuordnung: global / an Benutzer / als Startseite** (missing, P3 M)
  Geteilte Seiten, Admin-Zuweisung, Startseiten-Flag. *Empfehlung:*
  owner/visibility-Feld auf dem Seitenmodell (setzt O02 voraus).
  **Frequenz:** `corner` — setzt O02 voraus; Mehr-Personen-Haushalte
  **Entscheidung:** `offen`
- **O05 — Auto-Login (Wandtablet/Kiosk)** (missing, P3 M)
  *Empfehlung:* Opt-in-Config mit Subject + Pflicht-CIDR-Beschränkung als
  zusätzliche Auth-Quelle in `internal/auth/chain.go` (Muster:
  Ingress-Pfad); Sicherheitsabwägung als ADR.
  **Frequenz:** `corner` — enge Nische, eigene Sicherheitsabwägung
  **Entscheidung:** `offen`

### 4.7 Systemvariablen & Diagramme

- **SV04 — Diagramm-Anzeige: Langzeit, freier Zeitraum, Zoom, Vergleich** (partial, P2 M)
  `QueryBuckets` liest nur die Raw-Tabelle (Retention 30 d), obwohl
  Hourly-/Daily-Rollups existieren → Tier-Fallback einbauen (dann trägt
  `GET /history` 13+ Monate); von-bis-Picker (API kann es bereits),
  Drag-Zoom, Vergleichszeitraum. Rein Loom-seitig.
  **Frequenz:** `hoch` — jede Diagrammansicht jenseits von 30 Tagen ist
  heute leer, obwohl die Rollups 13 Monate vorhalten — eher Defekt als
  Ausbau
  **Entscheidung:** `offen`
- **SV08 — CSV-Export der Diagrammdaten** (missing, P3 S)
  *Empfehlung:* clientseitig aus den geladenen Buckets (Blob-Download);
  optional `?format=csv` am `GET /history` für API-Nutzer.
  **Frequenz:** `mittel` — gelegentlicher Export
  **Entscheidung:** `offen`
- **SV09 — Messwerterfassung kanalgenau konfigurieren** (partial, P3 M)
  Globs matchen nur Parameternamen über alle Geräte („`*POWER*`", nicht
  „nur POWER von Gerät X"); keine Nicht-Experten-UI. *Empfehlung:*
  kanal-scoped Patterns (`ADDR:4/POWER`) im Recorder-Filter + einfache
  Aufnahme-Verwaltung in der SPA. Rein Loom-seitig.
  **Frequenz:** `corner` — durch SV10 (Per-Datenpunkt-Toggle) weitgehend
  absorbiert
  **Entscheidung:** `offen`

### 4.8 Systemsteuerung

- **SY01 — Backup erstellen: Fallback für Original-CCU3** (partial, P2 S)
  Der Trigger läuft über `createBackup.sh` (nur OpenCCU/RaspberryMatic).
  *Empfehlung:* Fallback per HTTP-GET auf
  `cp_security.cgi?action=create_backup` mit Session (Muster
  `HTTPBackupRestorer` existiert). *HTTP-CGI.*
  **Frequenz:** `mittel` — selten gebraucht — dann aber im Ernstfall
  **Entscheidung:** `offen`
- **SY02 — Backup-Restore: Upload externer .sbk + Vorab-Validierung** (partial, P2 M)
  Restore geht nur aus daemon-eigenen Backups. *Empfehlung:*
  `POST /backups/upload` (multipart) + Validierungsschritt
  (Signatur-/Versions-Check der cp_security-Antwort auswerten). *HTTP-CGI.*
  **Frequenz:** `mittel` — selten gebraucht — dann aber im Ernstfall (Fremd-
  Backup einspielen)
  **Entscheidung:** `offen`
- **SY03 — System-Sicherheitsschlüssel (AES) setzen/ändern** (missing, P2 L)
  *Empfehlung:* XML-RPC `changeKey` (rfd) + `crypttool -S` per
  ReGa-`system.Exec` (Key-Index läuft nur auf der Zentrale);
  `POST /system/security-key` mit Validierung; destruktiv → Confirm
  zwingend. *XML-RPC + ReGa.*
  **Frequenz:** `corner` — einmal im Anlagenleben, destruktiv
  **Entscheidung:** `offen`
- **SY04 — Firewall-Konfiguration der CCU** (missing, P2 M)
  Modus (voll/eingeschränkt) + IP-Freigabelisten. *Empfehlung:*
  `Firewall.get/setConfiguration` per JSON-RPC + Karte mit
  Selbst-Aussperr-Schutz (eigene Daemon-IP immer freihalten). *JSON-RPC.*
  **Frequenz:** `corner` — einmal konfiguriert; die CCU-WebUI ist pro
  Zentrale verlinkt
  **Entscheidung:** `offen`
- **SY05 — Position (Koordinaten) + Zeitzone für Astro** (missing, P2 M)
  Wichtig, weil Loom Astro-Schedules bereits editiert — falsche Koordinaten
  verfälschen alle Sonnenzeiten; Voraussetzung für PR04. *Empfehlung:*
  `system.Longitude()/Latitude()` per ReGa-Skript (reines ReGa-DOM),
  Read-Back in `/system/ccu`, Karte im System-Tab. *ReGa.*
  **Frequenz:** `hoch` — einmal gesetzt, aber jede Astro-Schaltzeit der
  bereits editierbaren Wochenprofile hängt daran
  **Entscheidung:** `offen`
- **SY06 — Zentralen-Firmware-Update: Vorab-Backup, EULA/Changelog, CCU3-Pfad** (partial, P2 M)
  Check + unbeaufsichtigte Installation funktionieren nur auf
  OpenCCU/RaspberryMatic. *Empfehlung:* Vorab-Backup als Option verketten,
  Changelog-Link; Original-CCU-Pfad nur bei Bedarf, sonst by-design
  dokumentieren.
  **Frequenz:** `mittel` — wenige Male im Jahr, hängt direkt an einer
  vorhandenen Funktion
  **Entscheidung:** `offen`
- **SY07 — Recovery-Modus + „CCU-Wartung"-Karte** (missing, P2 M)
  OpenCCU-JSON-RPC `RecoveryMode.enter` (nicht auf Original-CCU3, nicht auf
  oci/lxc → Capability-Gating via `get_backend_info.fn`-Erweiterung).
  *Empfehlung:* Settings-Karte „CCU-Wartung" pro Central als gemeinsamer
  Landeplatz für Recovery/Reboot/Shutdown/SafeMode; Confirm + Hinweis auf
  die Recovery-Oberfläche auf der CCU-IP. *JSON-RPC.*
  **Frequenz:** `mittel` — Reboot ist regelmäßig, Recovery selten; die Karte
  gibt dem ausgelieferten K03 eine auffindbare Heimat
  **Entscheidung:** `offen`
- **SY08 — SSH-Zugang + root-Passwort** (missing, P3 M)
  *Empfehlung:* `CCU.getSSHState/setSSH/setSSHPassword/restartSSHDaemon`
  per JSON-RPC, Admin-Panel im System-Tab. *JSON-RPC.*
  **Frequenz:** `corner` — einmal konfiguriert; die CCU-WebUI ist pro
  Zentrale verlinkt
  **Entscheidung:** `offen`
- **SY09 — Auth für CCU-Remote-APIs erzwingen (`CCU.setAuthEnabled`)** (partial, P3 S)
  Sicherheitsrelevant (offene XML-RPC-API schwächt das Gesamtsystem).
  *Empfehlung:* Setter + Toggle + lighttpd-Neustart-Handling. *JSON-RPC.*
  **Teilstand (0.51.0+, API 3.5.0):** Die **Leseseite** ist da — der zuvor
  ungenutzte Getter ist verdrahtet, `GET /api/v1/system/ccu` meldet
  `auth_enabled`, die Fleet-Karte zeigt es als Chip. Der **Setter** fehlt
  weiterhin.
  **Frequenz:** `corner` — die Leseseite (API 3.5.0) deckt den
  Informationsbedarf, der Setter ist ein Einmal-Schalter
  **Entscheidung:** `offen`
- **SY10 — Automatische HTTPS-Umleitung der CCU** (partial, P3 S)
  *Empfehlung:* Setter + Toggle; Wechselwirkung mit der vom Daemon
  genutzten Basis-URL beachten. *JSON-RPC.*
  **Teilstand (0.51.0+, API 3.5.0):** analog SY09 — `https_redirect_enabled`
  wird gelesen und in der Fleet-Karte angezeigt; der Setter fehlt.
  **Frequenz:** `corner` — die Leseseite (API 3.5.0) deckt den
  Informationsbedarf, der Setter ist ein Einmal-Schalter
  **Entscheidung:** `offen`
- **SY11 — Session-Timeout der Loom-Sessions konfigurierbar** (partial, P3 S)
  TTL ist hart codiert (12 h). *Empfehlung:* cfg-Feld
  `north.rest.auth.session_ttl` (+ idle_ttl) inkl. i18n-Labels und
  Contract-Test. Rein Loom-seitig.
  **Frequenz:** `mittel` — einmal konfiguriert, wirkt danach auf jede
  Sitzung
  **Entscheidung:** `offen`
- **SY12 — Uhrzeit/Datum der Zentrale manuell setzen** (missing, P3 M)
  *Empfehlung:* ReGa-`system.Exec` (date/hwclock) + Interface-Clock-Sync;
  niedrig, weil NTP der Normalfall ist. *ReGa/OS.*
  **Frequenz:** `corner` — einmal; NTP ist der Normalfall
  **Entscheidung:** `offen`
- **SY13 — NTP-Server der Zentrale konfigurieren** (missing, P3 M)
  *Empfehlung:* Sync-Status lesen (chronyc via ReGa-`system.Exec`) als
  Diagnose-Anzeige; Serverliste schreiben per Exec-Skript. *ReGa/OS.*
  **Frequenz:** `corner` — einmal; NTP läuft im Normalfall von selbst
  **Entscheidung:** `offen`
- **SY14 — CCU-HTTPS-Zertifikat verwalten** (partial, P3 M)
  Loom-eigenes Zertifikat ist tauschbar; das CCU-`server.pem` nicht
  (relevant für `tls_insecure_skip_verify`). *Empfehlung:* Upload an
  `cp_network.cgi?action=cert_upload` analog HTTPBackupRestorer + lighttpd-
  Neustart. *HTTP-CGI + JSON-RPC.*
  **Frequenz:** `corner` — selten
  **Entscheidung:** `offen`
- **SY15 — Zentrale herunterfahren (Poweroff)** (missing, P3 S)
  *Empfehlung:* analog K03 im selben Endpoint-Namensraum; nachrangig.
  *ReGa.*
  **Frequenz:** `mittel` — selten für sich, fährt mit der Wartungskarte
  (SY07) mit
  **Entscheidung:** `offen`
- **SY16 — Abgesicherter Modus (SafeMode)** (missing, P3 S)
  *Empfehlung:* JSON-RPC `SafeMode.enter` neben dem CCU-Reboot; sinnvoll
  erst mit einer Addon-Sicht (E05/E06). *JSON-RPC.*
  **Frequenz:** `mittel` — selten für sich, fährt mit der Wartungskarte
  (SY07) mit
  **Entscheidung:** `offen`
- **SY17 — Funk-/LAN-Gateway-Verwaltung (BidCos)** (missing, P3 L)
  Gateway-Status/Firmware/DutyCycle + Geräte-Zuordnung
  (`setBidcosInterface`). Nur für BidCos-Bestandsanlagen mit Gateways
  relevant. *JSON-RPC + XML-RPC.*
  **Frequenz:** `corner` — nur Bestandsanlagen mit LAN-Gateways
  **Entscheidung:** `offen`
- **SY18 — Energiekosten-Satz (Preis pro kWh)** (missing, P3 S)
  *Empfehlung:* rein Loom-lokal: cfg-Feld + Kostenberechnung in
  `handlers/energy.go` + Anzeige in Energy (i18n-Pflicht beachten).
  **Frequenz:** `hoch` — die Energy-Ansicht wird regelmäßig geöffnet; es
  fehlt nur das Feld
  **Entscheidung:** `offen`
- **SY19 — Werksreset der Zentrale** (missing, P3 M)
  Nur per ReGa-`system.Exec` (Marker-Datei + Reboot) möglich; hohes
  Zerstörungspotenzial. *Empfehlung:* eher ausschließen — die
  Recovery-WebUI der CCU bleibt der sichere Weg.
  **Frequenz:** `corner` — bewusst ausschließen — Recovery-WebUI der CCU ist
  der sichere Weg
  **Entscheidung:** `offen`
- **SY20 — CCU-Netzwerkkonfiguration (IP/DNS/Hostname)** (missing, P3 L)
  Keine API; die WebUI schreibt Konfig-Dateien direkt. Fehlkonfiguration
  kappt die Verbindung Daemon→CCU. *Empfehlung:* eher ausschließen
  (Non-Goal dokumentieren, nur Anzeige in `/system/ccu` ausbauen);
  Container-Betrieb blendet die Seite ebenfalls aus.
  **Frequenz:** `corner` — bewusst ausschließen — Fehlkonfiguration kappt
  die Verbindung zum Daemon
  **Entscheidung:** `offen`

### 4.9 Diagnose & Meldungen

- **D01 — Meldungszähler in der Kopfzeile/Sidebar + Live-Reload** (partial, P2 S)
  Die `hub.*_messages`-Broadcasts existieren serverseitig, werden aber von
  keiner globalen Fläche konsumiert. *Empfehlung:* kleiner Store + Badge in
  der Sidebar mit Direktsprung; MessageList bei Broadcast nachladen.
  Rein Loom-seitig.
  **Frequenz:** `hoch` — wird bei jedem Seitenaufruf gelesen; die Broadcasts
  liegen serverseitig ungenutzt bereit
  **Entscheidung:** `offen`
- **D03 — Systemprotokoll (chronologisches Ereignisprotokoll)** (partial, P2 L)
  Der Recorder verwirft bool/enum/string; Sysvar-Änderungen werden nicht
  aufgezeichnet; keine systemweite Ereignisliste/CSV/Löschen. *Empfehlung:*
  Event-Log-Tier im History-Subsystem + `GET /api/v1/protocol` (Filter,
  CSV, Löschen) + SPA-Route. Kein CCU-Zugriff nötig.
  **Frequenz:** `mittel` — häufig nachgefragt („was war heute Nacht?"), aber
  L-Aufwand
  **Entscheidung:** `offen`
- **D04 — Loglevel der CCU-Dienste (rfd/hs485d/ReGa)** (missing, P3 M)
  *Empfehlung:* XML-RPC `logLevel` an rfd/hs485d, ReGa-Level per
  Mini-Skript; „CCU-Loglevel"-Karte neben dem LogLevelsPanel. Syslog-Ziel +
  HMIPServer-Level sind OS-gebunden → auslassen. *XML-RPC + ReGa.*
  **Frequenz:** `corner` — Debugging-Nische
  **Entscheidung:** `offen`
- **D05 — CCU-Logfile-Download (/var/log/messages, hmserver.log)** (missing, P3 L)
  Keine RPC-Schnittstelle; der WebUI-CGI streamt lokale Dateien.
  *Empfehlung:* CGI-Session-Proxy (fragil) oder künftiger CCU-Agent (A1);
  bis dahin bewusste Lücke.
  **Frequenz:** `corner` — Debugging-Nische, OS-gebunden (A1)
  **Entscheidung:** `offen`

### 4.10 Drittanbieter-API & Ökosystem

- **E01 — JSON-RPC-Kompat-Endpunkt `/api/homematic.cgi`** (missing, P2 L)
  Nur nötig, wenn Loom der einzig exponierte Endpunkt sein soll
  (Remote-Proxy, abgeschottete CCU) — solange die CCU läuft, bleibt die API
  dort erreichbar. *Empfehlung:* Scope per ADR (A2); dann eigener
  North-Adapter (`internal/north/jsonapi`): JSON-RPC-1.1-Envelope,
  Methodenregister mit Level-ACL auf Loom-Rollen, Introspektion; Stufe 1
  als Kern-Subset (`Session.*`, `Event.*` als Long-Poll-Puffer über den
  EventBus, `Interface.*`-Werte/Paramsets/Links, `Device.listAllDetail`,
  `SysVar.*`, `Program.*`, `Room.*`/`Subsection.*`, `ReGa.runScript`) auf
  bestehende Domänendienste delegiert. Wichtigste Vorarbeit:
  **ise_id-Tabelle** (ReGa-IDs beim Hub-Sync mitführen — Drittanbieter
  adressieren über ReGa-IDs).
  **Entscheidung:** `abgelehnt`
- **E02 — ReGa-Metadatenspeicher (`get/set/removeMetadata`)** (missing, P3 S)
  Von Community-Tools als K/V-Store genutzt; auch ohne E01 als kleines
  Feature über den ReGa-Runner nachrüstbar. *ReGa.*
  **Frequenz:** `corner` — Werkzeug für Drittanbieter, nicht für Bediener
  **Entscheidung:** `offen`
- **E03 — XML-API-Listen (devicelist.xml, roomlist.xml, …)** (missing, P3 M)
  Guest-Level-XML für Alt-Clients; braucht dieselbe ise_id-Tabelle wie E01.
  *Empfehlung:* nur bei konkretem Bedarf eines Ziel-Clients.
  **Entscheidung:** `abgelehnt`
- **E04 — SSDP/UPnP-Selbst-Announcement** (partial, P3 S)
  Loom announced nur mDNS (ADR 0021) — in der Windows-Netzwerkumgebung
  unsichtbar. *Empfehlung:* kleiner SSDP-Responder + Device-Description-XML
  mit presentationURL; Interface-Logik aus `ssdp/interfaces.go`
  wiederverwenden. Rein Loom-seitig.
  **Entscheidung:** `abgelehnt`
- **E05 — Addon-Deep-Links (CONFIG_URL-Kacheln)** (partial, P3 S)
  Der generische „CCU-WebUI öffnen"-Link pro Central existiert bereits
  (`Fleet.svelte`); addon-spezifische Kacheln fehlen. *Empfehlung:*
  konfigurierbare Addon-Links-Kachel (neuer Tab, Session bleibt bei der
  CCU); optional später Einbettung hinter Loom-Auth per Reverse-Proxy
  (Muster `internal/remoteproxy`).
  **Frequenz:** `mittel` — hängt davon ab, ob überhaupt Addons im Einsatz
  sind
  **Entscheidung:** `offen`
- **E06 — Addon-Vollverwaltung (Install/Update/Uninstall)** (missing, P3 XL)
  Remote nicht sauber machbar (keine RPC-API; rc.d + tar-Upload =
  OS-Zugriff). *Empfehlung:* eher ausschließen bzw. an die
  Agent-Entscheidung (A1) koppeln.
  **Frequenz:** `corner` — OS-gebunden (A1)
  **Entscheidung:** `offen`

### 4.11 Restposten aus erledigten Punkten

Kleine Reste, die beim Liefern des jeweiligen Punkts bewusst offen blieben.
Sie haben keine eigene ID und keinen Entscheidungsbedarf — sie sind hier
gesammelt, weil sie sonst mit den eingedampften Einträgen verloren gingen.

- **W02 Slice 2** — die 20-Bit-`..._VALUE`-Packung des Universallicht-
  Wochenprogramms (Hue/Sättigung | Kelvin | Effekt) decodieren/encodieren
  plus editierbares Farbwidget. Die Packung ist weder in occu noch in
  aiohomematic dokumentiert; der Encode muss gegen ein echtes HmIP-RGBW
  validiert werden (benanntes Zielgerät + Schreibfreigabe nötig).
- **W01 Layer 2** — beim HM-CC-RT-DN bleiben Metadaten-Datenpunkt
  (`week_profile`) und MQTT-Wochenprofil-Discovery still, weil die
  Normalisierung das Root-Profil ablöst (`ScheduleChannelNo=nil`). Der
  saubere Fix läuft zuerst upstream: HM-CC-RT-DN in aiohomematic auf
  `schedule_channel_no=BIDCOS_DEVICE_CHANNEL_DUMMY` registrieren, dann
  Profile regenerieren und den Modell-Snapshot neu basieren.
- **G11** — der Kommunikationstest läuft geräteweit; STICKY_UNREACH-Reset
  nach erfolgreichem Test und ein kanalgenauer Test fehlen.
- **G12** — die Kanal-Flags sind daemon-eigen; ein ReGa-Sync auf
  `oChannel.Visible` und Playwright-Baselines für die beiden Toggles stehen aus.
- **SV03** — Diagramme laden je Serie einzeln; ein gebündelter
  `GET /diagrams/{id}/data`, WS-Kommandos, ein Datenpunkt-Picker statt
  Freitextfeldern im Editor und Playwright-Baselines sind zurückgestellt.
- **SV07/V03** — godevccu kann weder `DPEnumUsagePrograms` noch
  `activateLinkParamset`; solange fehlen E2E-Abdeckung und die
  Playwright-Baseline der Link-Test-Buttons.
- **V01** — die globale Verknüpfungsübersicht ist read-only; ein
  zentralenübergreifender „Neue Verknüpfung"-Einstieg mit Quell-Kanal-Picker
  fehlt (der Weg führt weiter über das Gerätedetail).
- **V02** — Rollen-Matching deckt Kanalgruppen (HM-Tastenpaare) und die
  HmIP-RGBW-Sonderparameter noch nicht ab.
- **G03** — beim Gerätetausch migriert die CCU Links/Teams/ReGa-Referenzen
  selbst; die WebUI-seitige Energiezähler-Sysvar-Umbenennung bleibt eine
  dokumentierte Lücke (`docs/parity/by_design.md`).

---

## 5. Bereits abgedeckt (Kurzüberblick)

Funktionale Sicht zum Zeitpunkt der Erhebung; die seither gelieferten Punkte
kommen hinzu und sind einzeln in §8 aufgeführt.

- **Bedienung:** Kanalliste mit Live-Widgets (Schalter/Dimmer/Rollladen/
  Thermostat/RGBW/Schloss/Sirene), Raum-/Gewerke-Sichten, Suche/Filter,
  Duty-Cycle-/Carrier-Sense-Anzeige je Gerät.
- **Verwaltung:** Geräteliste als Verwaltungsfläche, MASTER-Parameter-Editor
  mit Validierung/Dirty-Tracking/Sessions, Anlernmodus je Interface mit
  Countdown + Posteingang, Räume/Gewerke-CRUD, Benutzer/Rollen (Admin/User/
  Gast), Profileditor mit Easymode-Vorlagen, Verknüpfung löschen/Parameter
  bearbeiten.
- **Programme/Sysvars:** Programmliste mit Ausführen/Aktiv-Toggle,
  Sysvar-Grund-CRUD + Live-Bedienung, interne ein-/ausblendbar.
- **System:** Servicemeldungen + Einzelquittierung, Alarmmeldungen,
  Firmware-Übersicht + Update-Trigger, Backup auf OpenCCU, System-Update auf
  OpenCCU, devconfig-Expertentool, Hilfe/Lizenz, de/en, HA-Integration
  (Ingress), CCU-WebUI-Deep-Link pro Central.
- **Loom-Mehrwerte ohne CCU-Pendant:** Multi-CCU/Fleet, MQTT, Matter,
  Audit-Log, OIDC/API-Tokens, Metriken, Energie-Ansicht, Diagnose-Hub mit
  Log-Tail/Captures, Alarm-Engine.

Bewusst nicht anwendbar (Auswahl): PDA-/Mobile-Sonder-UI (SPA ist
responsiv), ReGa-Benutzerkonten (Loom hat eigenes Auth-Modell),
OSRAM/Hue-Kopplungen, CloudMatic/NEO-Verweise, WLAN-/LED-/Tweaks-Seiten der
OpenCCU-Systemebene (Betrieb der Zentrale, nicht der Bridge), microSD-
Initialisierung.

---

## 6. Grundsatzentscheidungen (ADR-Kandidaten)

- **A1 — CCU-seitiger Agent ja/nein.**
  Netzwerkkonfig, Addon-Installation, LED/Tweaks, CCU-Logdateien,
  Syslog/HMIPServer-Loglevel, Werksreset sind OS-gebunden. Optionen:
  (a) dokumentiertes Non-Goal (CCU-WebUI-Deep-Link bleibt der Weg — heute
  schon vorhanden), (b) schmaler authentifizierter Agent auf der Zentrale
  (Muster: OpenCCU-Loom-Remote-Add-on / HA-Add-on-`update_script`).
  Empfehlung: (a) für 1.0, (b) als eigenes Projekt prüfen. Betrifft D05,
  E06, SY12/13/19/20.
  Die Frequenz-Einstufung stützt (a): **jeder** betroffene Punkt ist
  `corner`, und die CCU-WebUI ist pro Zentrale bereits verlinkt — ein
  Agent würde dauerhafte Pflege gegen Firmwarewechsel kosten, ohne eine
  regelmäßig genutzte Fläche zu gewinnen.
  **Entscheidung:** `offen`
- **A2 — JSON-RPC-Kompat-API.**
  Nur bei Vollersatz-Szenario mit abgeschotteter CCU nötig; Scope
  (Kern-Subset vs. Vollnachbau) und ise_id-Strategie festlegen. Betrifft
  E01–E03.
  **Entscheidung:** `offen`
- **A3 — Gruppen via HMServer-jpages-Proxy vs. nativer Nachbau.**
  Proxy ist empfohlen (Drift-Risiko der Verknüpfungs-Matrix);
  Session-Mechanik gegen `/pages/jpages` vorab verifizieren. Betrifft
  GR01–GR05.
  **Entscheidung:** `entschieden` → Proxy, siehe
  [ADR 0055](adr/0055-groups-jpages-proxy.md). Session-Frage geklärt:
  HMServer validiert die jpages-`sid` gegen ReGa (`/api/homematic.cgi`),
  also genügt Looms bestehender JSON-RPC-`Session.login`-Token; kein
  separater WebUI-Login nötig (Präzedenz: `SetDownloadFirmwareTransport`).
- **A4 — Programmeditor-Sicherheitsmodell.**
  Der ReGa-Skript-Endpunkt ist faktisch Remote-Code-Execution auf der
  Zentrale — admin-only, Audit-Log mit Klartext, Rate-Limit,
  Confirm-Dialog; als Sicherheitsrahmen einmal sauber festschreiben.
  Betrifft PR01–PR06.
  **Entscheidung:** `offen`

## 7. Umsetzungsplan (Wellen 1–6 beschlossen, 7–8 vorgeschlagen)

Welle 1 besteht aus acht unabhängigen, klein geschnittenen Paketen
(je 1–2 PRs, parallelisierbar); ab Welle 2 folgen die großen Blöcke.
Abhängigkeiten: K01 vor G10 (gleiches Paket), A3-ADR vor GR02,
GR02 vor GR03–GR05.

**Lieferstand 2026-07-31 (0.51.0) — der ursprüngliche Wellenplan (1–6) ist
abgearbeitet:** Wellen **1a–1h ✅** und **GR01 ✅** (bis 0.46.0); Wellen
**3 ✅, 5 ✅, 6 ✅** sowie die Welle-4-Kernpunkte **G11 ✅, G14 ✅** (0.47.0);
**G12 ✅** (Kanal-Sichtbarkeit/Sperre, API 2.51.0); Welle **2 ✅ komplett**
(GR02–GR05, 0.48.0, nachgeschärft bis 0.48.8).
**Offen:** die Welle-4-Restposten **G08, G16** (beide Hardware-abhängig
zurückgestellt) und — außerhalb jeder Welle — **V06** (siehe §4.5).

**Wellen 7 und 8** sind aus der Frequenz-Einstufung (§4) abgeleitet und
noch nicht beschlossen: sie bündeln die sechs `hoch`-Punkte und die
`mittel`-Punkte, die ein bereits ausgeliefertes Thema abschließen. Die
enthaltenen Punkte stehen weiterhin auf `Entscheidung: offen` — der
Vorschlag ersetzt die Einzelentscheidung nicht.

| Welle | Punkte | Inhalt |
|---|---|---|
| 1a | K01, G02, G10 | Geräte-Admin: Rename-Verdrahtung + Kanal-Rename, Lösch-Flags (RESET/FORCE) + Abhängigkeits-Check, Posteingang-Erstkonfiguration |
| 1b | K02, SV01, SV02, SV05, SV06 | Systemvariablen: LOGIC/ALARM-Widgets, CRUD-Feinheiten + LIST-Bug, Alarm-Typ anlegen, Wertelabels/Flags, Kanalzuordnung |
| 1c | K04, G07 | Meldungen: Sammelquittierung, Unterdrückung verdrahten + Verwaltungsansicht |
| 1d | K03 | CCU-Neustart (ReGa-Skript + admin-Endpunkt + Confirm) |
| 1e | PR07, PR08, PR11, PR12 | Programmliste: Regel-Zusammenfassung + letzte Ausführung, Interne-Toggle, Execute mit Bedingungsprüfung, Löschen |
| 1f | V05, V07, V08 | Verknüpfungen klein: setLinkInfo exponieren, Bewegungsmelder-Helfer, Config-Pending-Hinweis |
| 1g | V04, V09, V10, V11 | Zentralenverknüpfung: pro Kanal, PRESS_LONG-Nullung, Schutzlogik + Confirm, Hilfetext |
| 1h | G13, G15, G17, D02 | AES-Toggle, „Bestimmen"-Button, Firmware-DC-Gate + Download-Trigger, DutyCycle pro Interface |
| 2 | A3 → GR01 → GR02 → GR03, GR04, GR05 | Gruppenverwaltung — ADR zuerst (jpages-Proxy vs. Nachbau), dann Liste read-only, dann Konfigurator |
| 3 | G01, G05, G06, G04, G03 | Geräte-Workflows: Kanal-Räume/Gewerke, SGTIN-Anlernen, virtuelle Fernbedienung, restoreConfig, Gerät tauschen |
| 4 | G08, G09, G11, G12, G14, G16 | Geräte-Restarbeiten: addDevice/setTempKey, Wired-Suche, Kommunikationstest, Kanal-Sichtbarkeit/Sperre, setTeam, Spezialdialoge |
| 5 | V01, V02, V03 | Verknüpfungen: globale Übersicht, Rollen-Matching, Link-Test am Gerät |
| 6 | SV03, SV07, SV10, W01, W02 | Diagramm-Definitionen, Sysvar-Verwendungsübersicht, Protokoll-Toggle, Wochenprofil-Restlücken |
| **7** | D01, SV04, O03, SY18, O01 | **Alltagsflächen** (Vorschlag, nicht beschlossen) — alle `hoch`-Punkte mit Ausnahme von SY05 |
| **8** | SY05, SY07 (+SY15, SY16), SY01, SY02, SY06 | **Zentralen-Wartung & Korrektheit** (Vorschlag, nicht beschlossen) |

### Welle 7 — Alltagsflächen

Die höchste Nutzungsfrequenz pro Aufwand im gesamten Restbestand, und
**vollständig Loom-intern**: kein CCU-Zugriff, keine Live-Freigabe, alles
hermetisch testbar.

- **D01** (S) — Meldungs-Badge in der Sidebar. Schließt das Thema
  Meldungen ab: Quittierung einzeln und gesammelt sowie die dauerhafte
  Unterdrückung sind ausgeliefert, es fehlt nur die globale Sichtbarkeit.
- **SV04** (M) — Tier-Fallback in `QueryBuckets`. Der eigentliche Kern ist
  eine Fehlfunktion, kein Ausbau: die Rollups halten 13 Monate, die
  Abfrage liest nur die 30-Tage-Rohtabelle. Danach tragen die frisch
  ausgelieferten Diagramme (SV03) auch lange Zeiträume; von-bis-Picker,
  Zoom und Vergleichszeitraum sind die Zugabe.
- **O03** (S) — Startseite pro Benutzer über den vorhandenen
  `/me/preferences`-Store.
- **SY18** (S) — Energiekosten-Satz; ein Config-Feld schließt die
  Energy-Ansicht ab.
- **O01** (M) — Favoriten bedienbar machen (Kachel-Technik aus
  `Overview.svelte` wiederverwenden, Pin-Typen um `channel`/`program`
  erweitern). Der größte Brocken der Welle und die Voraussetzung dafür,
  dass O02/O04 überhaupt Sinn ergeben.

### Welle 8 — Zentralen-Wartung & Korrektheit

Braucht CCU-Zugriff, aber nur über ReGa- und JSON-RPC-Pfade, die es
bereits gibt.

- **SY05** (M) — Position/Koordinaten lesen und schreiben. Schließt ein
  Korrektheitsloch unter den bereits editierbaren Astro-Wochenprofilen;
  auch Voraussetzung für PR04, falls der Programmeditor je kommt.
- **SY07** (M) mit **SY15**, **SY16** — eine Karte „CCU-Wartung" pro
  Zentrale als gemeinsame Heimat für Recovery, Reboot (das ausgelieferte
  K03), Poweroff und SafeMode. Capability-Gating über
  `get_backend_info.fn`, weil Recovery auf der Original-CCU3 und in
  oci/lxc fehlt.
- **SY01**, **SY02** (S/M) — Backup-Fallback für die Original-CCU3 und
  Upload einer externen `.sbk`. Selten benutzt, aber genau dann, wenn es
  darauf ankommt.
- **SY06** (M) — Vorab-Backup und Changelog-Link beim
  Zentralen-Firmware-Update.

### Bewusst nicht eingeplant

- **V06** bleibt der einzige beschlossene Punkt ohne Wellen-Zuordnung. Die
  Frequenz-Einstufung (`mittel`, lohnt erst bei vielen gleichartigen
  Verknüpfungen) trägt keine eigene Welle. Entweder als kleinen Anhang zu
  Welle 7 mitnehmen oder die `umsetzen`-Entscheidung zurücknehmen — eine
  dritte Möglichkeit gibt es nicht.
- **G08**, **G16** bleiben zurückgestellt, bis die jeweilige Hardware
  verfügbar ist.
- Die `corner`-Punkte der Systemsteuerung (SY03, SY04, SY08, SY09/SY10-
  Setter, SY12–SY14, SY17, SY19, SY20) sind Duplikate der CCU-WebUI, die
  pro Zentrale ohnehin verlinkt ist. Sie kosten dort null Wartung und in
  Loom dauerhaft Pflege bei jedem Firmwarewechsel — das ist zugleich die
  Antwort auf **A1** (siehe §6).
- Ebenfalls offen und ohne Welle: der Programmeditor-Block (PR01–PR06,
  PR09, PR10) samt **A4**, Favoriten-Ausbau (O02, O04, O05), SV08/SV09,
  D03–D05, E02/E05/E06 und **A2**. Abgelehnt: E01, E03, E04.

Der operative Wellen-Runbook (Ausführungsschleife, CI-Fallen,
Wieder-Aufsetzen) ist mit dem abgearbeiteten Plan aufgelöst worden: die
dauerhaft nützlichen Teile — Contract-Fallen und das
Playwright-Baseline-Rezept — stehen jetzt in
[`CONTRIBUTING.md`](../CONTRIBUTING.md), der Rest in `CLAUDE.md`.

Innerhalb jeder Welle gelten die bestehenden Regeln: openapi.yaml zuerst,
Contract-Tests bei Protokollgrenzen, i18n de+en vollständig, vier
Theme-Kombinationen, Playwright-Baselines für neue Views.

---

## 8. Erledigt (Index)

Die 48 gelieferten Punkte, je eine Zeile. Der Index existiert, damit die IDs
auflösbar bleiben — sie werden in Commit-Bodies (`Ref:
docs/ccu-webui-gap-analysis.md <ID>`) und in älteren PRs zitiert. **Was ein
Punkt konkret gebracht hat, steht im `CHANGELOG.md`** unter der genannten
Version; die ausführlichen Einträge wurden am 2026-07-31 aus §4 entfernt.
Verbleibende Reste einzelner Punkte stehen in §4.11.

| ID | Punkt | geliefert |
|---|---|---|
| K01 | Gerät/Kanäle umbenennen persistent zur CCU | 0.46.0 |
| K02 | Systemvariablen Logikwert/Alarm als Schalter bedienen | 0.46.0 |
| K03 | CCU-Neustart | 0.46.0 |
| K04 | Sammelquittierung „Alle bestätigen" | 0.46.0 |
| PR07 | Programmliste: Regel-Zusammenfassung + letzte Ausführung | 0.46.0 |
| PR08 | Systeminterne Programme: Laufzeit-Toggle | 0.46.0 |
| PR11 | Programmausführung mit Bedingungsprüfung | 0.46.0 |
| PR12 | Programm löschen (nordseitig) | 0.46.0 |
| GR01 | Gruppenliste (read-only) | 0.46.0 · API 2.42.0 |
| GR02 | Gruppen-Konfigurator: anlegen, Mitglieder, speichern, löschen | 0.48.0 · API 2.53.0 |
| GR03 | Gruppe umbenennen inkl. Kanal-Namensschema | 0.48.0 |
| GR04 | „Bedienung nur über Gruppe" (`Device.setOperateGroupOnly`) | 0.48.0 |
| GR05 | Gruppenzuordnung beim Anlernen | 0.48.0 |
| G01 | Raum-/Gewerkezuordnung pro Kanal | 0.47.0 · API 2.43.0 |
| G02 | Gerät löschen mit Optionen (RESET/FORCE + Abhängigkeits-Check) | 0.46.0 |
| G03 | Gerät tauschen | 0.47.0 · API 2.43.0 |
| G04 | Gerätekonfiguration wiederherstellen (`restoreConfigToDevice`) | 0.47.0 · API 2.43.0 |
| G05 | HmIP-Anlernen ohne Internet (SGTIN + Key) | 0.47.0 · API 2.43.0 |
| G06 | Virtuelle Fernbedienung / Tastensimulation | 0.47.0 (SPA-only) |
| G07 | Servicemeldungen dauerhaft unterdrücken | 0.46.0 |
| G09 | BidCos-Wired Gerätesuche (`searchDevices`) | 0.47.0 · API 2.44.0 |
| G10 | Posteingang: Erstkonfiguration beim Accept | 0.46.0 |
| G11 | Kommunikationstest / Funktionstest (geräteweit) | 0.47.0 · API 2.44.0 |
| G12 | Kanal-Sichtbarkeit und Bediensperre | 0.47.2 · API 2.51.0 |
| G13 | Übertragungsmodus Standard vs. gesichert (AES) je Kanal | 0.46.0 |
| G14 | Team-Zuordnung (`setTeam`) | 0.47.0 · API 2.44.0 |
| G15 | „Bestimmen"-Button (`determineParameter`) | 0.46.0 |
| G17 | Geräte-Firmware: Duty-Cycle-Gate + Download-Trigger | 0.46.0 |
| V01 | Globale Verknüpfungsübersicht | 0.47.0 · API 2.45.0 |
| V02 | Rollen-Matching beim Verknüpfung-Anlegen | 0.47.0 |
| V03 | Verknüpfung am Gerät testen (`activateLinkParamset`) | 0.47.0 · API 2.49.0 |
| V04 | Zentralenverknüpfung pro Kanal | 0.46.0 |
| V05 | Verknüpfung umbenennen (`setLinkInfo`) | 0.46.0 |
| V07 | Bewegungsmelder-Helligkeits-Helfer | 0.46.0 |
| V08 | Config-Pending-Hinweis nach Link-Operationen | 0.46.0 |
| V09 | Zentralenverknüpfung deaktivieren nullt auch PRESS_LONG | 0.46.0 |
| V10 | Schutzlogik + Confirm beim Deaktivieren der Zentralenverknüpfung | 0.46.0 |
| V11 | DutyCycle-/Batterie-Hilfetext zur Zentralenverknüpfung | 0.46.0 |
| SV01 | Sysvar-CRUD-Feinheiten (inkl. LIST-Bug) | 0.46.0 |
| SV02 | Alarm-Variable (virtuelle Alarmlinie) anlegen | 0.46.0 |
| SV03 | Diagramm-Definitionen (Multi-Serien-Diagramme) | 0.47.0 · API 2.50.0 |
| SV05 | Wertelabels (ValueName0/1) + Visible-/Logging-Flags | 0.46.0 |
| SV06 | Kanalzuordnung einer Sysvar schreibbar | 0.46.0 |
| SV07 | Sysvar-Verwendungsübersicht (Programme) + Lösch-Warnung | 0.47.0 · API 2.47.0 |
| SV10 | Protokoll-Toggle am einzelnen Datenpunkt | 0.47.0 · API 2.46.0 |
| D02 | DutyCycle/Carrier-Sense pro Interface | 0.46.0 |
| W01 | HM-CC-RT-DN-Temperaturprofil (präfixloses Schema) | 0.47.0 |
| W02 | Universallicht-Wochenprogramm: Farbe/Effekt (Slice 1) | 0.47.0 · API 2.48.0 |
