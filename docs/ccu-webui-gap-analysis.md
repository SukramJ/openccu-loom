# CCU-WebUI-Ersatz: Gap-Analyse und offene Punkte

Stand: 2026-07-21 · Basis: OpenCCU-Loom 0.45.0 (`main`), occu (CCU3-WebUI,
`../occu/WebUI/www/`), OpenCCU (`../OpenCCU/buildroot-external/`).
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
2. **Gruppenverwaltung** (HmIP-/BidCos-Heizungsgruppen) — komplette
   CRUD-Orchestrierung fehlt; läuft CCU-seitig über die HMServer-Endpunkte
   `/pages/jpages/group/*`, die Loom heute gar nicht anspricht.
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

Legende: Status `partial`/`missing` · Priorität P1 (Kernfunktion für
WebUI-Ersatz) / P2 (wichtig) / P3 (nice-to-have) · Aufwand S/M/L/XL.
Empfohlene CCU-Schnittstelle jeweils am Ende der Empfehlung. Abgedeckte
Funktionen je Bereich sind in §5 zusammengefasst.

### 4.1 Quick Wins (alle P1, Aufwand S)

- **K01 — Gerät/Kanäle umbenennen persistent zur CCU** (partial, P1 S)
  Rename setzt heute nur `dev.Name` in-memory
  (`internal/central/adapter/device_admin.go`); die JSON-RPC-Rename-Kette
  existiert ungenutzt (`RenameDevice`/`RenameChannel` in
  `internal/client/backends/ccu_extended.go`, `central.SetRenameDeviceFn`
  nirgends verdrahtet). *Empfehlung:* verdrahten, Kanal-Rename als
  `PATCH /devices/{addr}/channels/{no}` exponieren, „alle Kanäle
  mitbenennen"-Option (OpenCCU 0099) anbieten. *JSON-RPC, vorhanden.*
  **Entscheidung:** `umsetzen`
- **K02 — Systemvariablen Logikwert/Alarm als Schalter bedienen** (partial, P1 S)
  SPA prüft auf `BOOL`, der Daemon liefert die Wire-Typen `LOGIC`/`ALARM`
  durch → der häufigste Sysvar-Typ ist nur per Freitext „true/false"
  bedienbar (`SysvarList.svelte`, `Favorites.svelte`). *Empfehlung:*
  Widget-Zweige um LOGIC/ALARM erweitern; reiner SPA-Fix.
  **Entscheidung:** `umsetzen`
- **K03 — CCU-Neustart** (missing, P1 S)
  Eine der häufigsten Wartungsaktionen, nicht auslösbar. *Empfehlung:*
  ReGa-Skript (`system.Save(); system.Exec("/sbin/reboot")`) als
  `POST /system/ccu/{central}/reboot` (admin, Confirm); die
  Readiness-Maschine (`ccu_readiness.go`) trägt den Reconnect bereits.
  *HM-Script/ReGa.*
  **Entscheidung:** `umsetzen`
- **K04 — Sammelquittierung „Alle bestätigen"** (missing, P1 S)
  Nach Stromausfall stehen dutzende Sticky-Unreach-Meldungen an; heute nur
  Einzel-Ack. Gilt für Service- UND Alarmmeldungen. *Empfehlung:* Button pro
  Tab in `MessageList.svelte` + optional Bulk-Endpoint (ein ReGa-Skript
  quittiert alle in einem Roundtrip). *ReGa-Runner, vorhanden.*
  **Entscheidung:** `umsetzen`

### 4.2 Programme, Zentralenverknüpfungen & HM-Script

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
- **PR07 — Programmliste: Regel-Zusammenfassung + letzte Ausführung** (partial, P2 M)
  Die Bedingungs-/Aktivitäts-Spalten der WebUI fehlen (Regelbaum wird nie
  gelesen); `last_executed` liegt im DTO, wird aber nicht angezeigt.
  *Empfehlung:* `last_executed`-Spalte sofort (reine SPA-Arbeit);
  Regel-Zusammenfassung per `oProgram.Rule()`-Traversierung als
  summary-Feld. *ReGa (Zusammenfassung), SPA (Spalte).*
  **Entscheidung:** `umsetzen`
- **PR08 — Systeminterne Programme: Laufzeit-Toggle** (partial, P3 S)
  Filter existiert nur als Experten-Config-Flag (`include_internal_programs`).
  *Empfehlung:* Query-Parameter + Toggle in `ProgramList.svelte` (analog
  OpenCCU 0134/0152). Rein Loom-seitig.
  **Entscheidung:** `umsetzen`
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
- **PR11 — Programmausführung mit Bedingungsprüfung** (missing, P3 S)
  OpenCCU-Erweiterung (Patch 0122): nur ausführen, wenn die Wenn-Bedingung
  aktuell wahr ist. *Empfehlung:* optionaler Parameter `check_conditions`
  an `POST /programs/{id}/execute`, als kuratiertes `.fn`-Skript. *ReGa.*
  **Entscheidung:** `umsetzen`
- **PR12 — Programm löschen (nordseitig)** (missing, P2 S)
  Kein `DELETE /programs/{id}` (unabhängig vom Editor nützlich; auch
  Voraussetzung für `Program.deleteProgramByName` in E01). *Empfehlung:*
  ReGa-Programmlöschung als kleines `.fn`-Skript + REST/WS. *ReGa.*
  **Entscheidung:** `umsetzen`

### 4.3 Gruppenverwaltung (HmIP-/BidCos-Heizungsgruppen)

- **GR01 — Gruppenliste** (missing, P1 M)
  Keinerlei Gruppen-Sicht; virtuelle Gruppengeräte erscheinen nur als
  gewöhnliche Geräte ohne Mitglieder-Kontext. *Empfehlung:* Read-only-
  Einstieg: JSON-RPC `CCU.getHeatingGroupList` (liest `groups.gson`),
  `GET /api/v1/groups` mit Join gegen das Loom-Gerätemodell, SPA-Liste mit
  Links auf die vorhandenen Geräteseiten (Bedienen/Parametrieren der
  virtuellen Geräte funktioniert heute schon). *JSON-RPC.*
  **Entscheidung:** `umsetzen` — **umgesetzt** (API 2.42.0): `GET
  /api/v1/groups` (+ `?central=`) und WS `groups.list`, typisierter
  `groups.gson`-Parser, SPA-Ansicht „Heizungsgruppen".
- **GR02 — Gruppen-Konfigurator: anlegen, Mitglieder, speichern, löschen** (missing, P1 XL)
  Die Orchestrierung (virtuelles Gerät am VirtualDevices-Interface,
  Direktverknüpfungs-Matrix je Gruppentyp, `groups.gson`-Pflege,
  CONFIG_PENDING-Nachlauf) läuft CCU-seitig über die HMServer-Endpunkte
  `/pages/jpages/group/{create,save,delete,suitableGroupMembers}`.
  *Empfehlung:* diese Endpunkte als Southbound-Backend ansprechen (Proxy mit
  CCU-Session) statt die Matrix nativ nachzubauen — Nachbau wäre
  drift-gefährdet. Vorab Session-Frage klären (Hauptaufwandstreiber);
  asynchron mit Progress-Broadcast; Fallback nativer Nachbau nur per ADR.
  Vgl. Grundsatzentscheidung A3.
  **Entscheidung:** `umsetzen`
- **GR03 — Gruppe umbenennen inkl. Kanal-Namensschema** (partial, P2 S)
  Loom kann nur das virtuelle Gerät generisch umbenennen; die CCU zieht
  `groups.gson`, Gerät und alle Kanäle (`Gruppenname:Kanalnummer`) nach.
  *Empfehlung:* im Konfigurator über den group/save-Pfad der CCU, danach
  Geräte-Reload. Setzt GR02 voraus.
  **Entscheidung:** `umsetzen`
- **GR04 — „Bedienung nur über Gruppe" (`Device.setOperateGroupOnly`)** (missing, P3 S)
  *Empfehlung:* kleiner JSON-RPC-Durchstich, Schalter pro Mitgliedsgerät in
  der Gruppen-Detailansicht. Setzt GR02 voraus. *JSON-RPC.*
  **Entscheidung:** `umsetzen`
- **GR05 — Gruppenzuordnung beim Anlernen** (missing, P3 M)
  „Zur Gruppe hinzufügen" direkt aus dem Posteingang (OpenCCU 0187 patcht
  diesen Ablauf). *Empfehlung:* im Accept-Flow für gruppenfähige
  Gerätetypen optional eine Zielgruppe anbieten. Setzt GR02 voraus.
  **Entscheidung:** `umsetzen`

### 4.4 Geräteverwaltung & Bedienung

- **G01 — Raum-/Gewerkezuordnung pro Kanal** (partial, P2 M)
  CCU ordnet je Kanal zu, Loom nur je Gerät; `set_device_rooms.fn`
  akzeptiert laut Kopf bereits Kanaladressen, nur Adapterpfad +
  `PATCH /devices/{addr}/channels/{no}` fehlen. Zusatzbefund: openapi.yaml
  dokumentiert bei `patchDevice` nur `name` — Spec-Drift, `rooms`/
  `functions` nachziehen. *ReGa, vorhanden.*
  **Entscheidung:** `umsetzen` — **umgesetzt** (API 2.43.0): `PATCH
  /devices/{addr}/channels/{no}` akzeptiert `rooms`/`functions`, WS
  `device.set_channel_rooms` / `device.set_channel_functions`,
  `ChannelSummary.rooms`, rename-sichere ReGa-Skript-Auflösung,
  SPA-Kanal-Editoren, Audit `device_assignment`. (Der bei `patchDevice`
  gemeldete Drift war bereits geschlossen.)
- **G02 — Gerät löschen mit Optionen (RESET/FORCE + Abhängigkeits-Check)** (partial, P2 S)
  `deleteDevice` wird fest mit `flags=0` gerufen; „ab Werk zurücksetzen"
  (1) und „erzwingen" (2) plus Vorab-Warnung über abhängige Links/Programme
  fehlen. *Empfehlung:* Flags durchreichen, Link-/Programm-Aggregat im
  SPA-Confirm anzeigen. *XML-RPC.*
  **Entscheidung:** `umsetzen`
- **G03 — Gerät tauschen** (missing, P2 L)
  Geführter Austausch (Links + Programm-/Sysvar-Referenzen migrieren,
  Altgerät abmelden). Der CCU-seitige `replaceDevice`-Callback ist bereits
  implementiert (`callback_handlers.go:572`) — es fehlt die northbound
  Auslöse-Fläche + LINK-/ReGa-Migration als asynchroner Workflow.
  *XML-RPC + ReGa kombiniert.*
  **Entscheidung:** `umsetzen` — **umgesetzt** (API 2.43.0): `GET
  /devices/{addr}/replace-candidates` + `POST /devices/{addr}/replace`
  (WS `device.replace_candidates` / `device.replace`), Southbound
  `listReplaceableDevices`/`replaceDevice` (nur BidCos-RF/-Wired),
  Eager-Modell-Swap + relaxierter Cross-Type-Guard, Inbox-Replace-Dialog,
  Audit `device_replace`. Die CCU migriert Links/Teams/ReGa-Referenzen
  selbst; die WebUI-seitige Energiezähler-Sysvar-Umbenennung ist eine
  dokumentierte Restlücke (siehe `docs/parity/by_design.md`).
- **G04 — Gerätekonfiguration wiederherstellen (`restoreConfigToDevice`)** (missing, P2 M)
  Nach Geräte-Werksreset die zentralseitig gespeicherte Konfiguration
  (MASTER aller Kanäle + Link-Peerings) komplett neu übertragen
  (OpenCCU 0151). rfd + HMIPServer unterstützen es, hs485d/CUxD nicht →
  Capability-Gating. *Empfehlung:* Backend-Op + `POST
  /devices/{addr}/config/restore` + Button im Gerätedetail (CONFIG_PENDING-
  Badge als Fortschritt existiert schon). *XML-RPC.*
  **Entscheidung:** `umsetzen` — **umgesetzt** (API 2.43.0): `POST
  /devices/{addr}/config/restore` (WS `device.restore_config`, admin,
  Audit), Southbound `restoreConfigToDevice` mit Per-Interface-Gate
  (HmIP-RF/BidCos-RF), `DeviceSummary.config_restore_supported` +
  Gerätedetail-Button.
- **G05 — HmIP-Anlernen ohne Internet (SGTIN + Key)** (missing, P2 M)
  `setInstallModeHMIP` wird fest mit `installMode:"ALL"` gesendet;
  LOCAL-Modus mit SGTIN+Key (inkl. Base32→Base16) fehlt. *Empfehlung:*
  Request erweitern + SPA-Formular im Inbox-Bereich. *JSON-RPC.*
  **Entscheidung:** `umsetzen` — **umgesetzt** (API 2.43.0):
  `POST /install-mode/interfaces` + `install_mode.enable` um `sgtin`/`key`
  erweitert, serverseitige Normalisierung in `pkg/hmproto` (SGTIN +
  Base32→Base16-Key), kein Broadcast-Fallback, Audit
  `install_mode`/`install_mode_local`, SPA-Offline-Anlernformular.
- **G06 — Virtuelle Fernbedienung / Tastensimulation** (partial, P2 S)
  Modell kennt `IsVirtualRemote` bereits; die SPA rendert BUTTON-Kanäle
  read-only. *Empfehlung:* Tastenraster mit Kurz/Lang-Buttons
  (PRESS_SHORT/LONG via vorhandenem setValue) im Gerätedetail.
  **Entscheidung:** `umsetzen` — **umgesetzt** (reine SPA, kein
  API-Bump): `VirtualRemoteKeyGrid` im Gerätedetail + interaktives
  `ButtonEvent`-Widget (Gate `operations.write && usage==data_point`),
  Einzel-Bool-`setValue` mit `device.trigger`-Flash-Feedback.
- **G07 — Servicemeldungen dauerhaft unterdrücken** (partial, P2 M)
  Die komplette Client-Schicht (`SuppressServiceMessage`,
  `GetSuppressedServiceMessages`, Koordinator-Naht) existiert
  **unverdrahtet**; der heutige „disable"-Endpoint quittiert nur.
  *Empfehlung:* verdrahten, Liste unterdrückter Meldungen + Aufheben,
  SPA-Aktion „Dauerhaft ausblenden". *JSON-RPC, vorhanden.*
  **Entscheidung:** `umsetzen`
- **G08 — Anlernen per Seriennummer (BidCos `addDevice` + `setTempKey`)** (partial, P3 M)
  Gezieltes Anlernfenster existiert; es fehlen der aktive
  addDevice-Fetch-Pfad und der Geräte-AES-Key-Dialog bei Key-Mismatch.
  *XML-RPC.*
  **Entscheidung:** `umsetzen` — **zurückgestellt** (2026-07-23): Recon
  vollständig (BidCos-RF-only: `addDevice`/`setTempKey`/
  `getKeyMismatchDevice`). Blocker: der Key-Mismatch-Fault-Code ist nur
  gegen echte BidCos-RF-Hardware sicher zu mappen, godevccu simuliert
  kein Anlernen. Reaktivieren, sobald ein BidCos-RF-Gerät + Live-Freigabe
  vorliegt.
- **G09 — BidCos-Wired Gerätesuche (`searchDevices`)** (missing, P3 S)
  Bus-Scan für Wired-Geräte in den Posteingang. *Empfehlung:*
  `Operations.SearchDevices` + `POST /install-mode/search`. *XML-RPC an
  hs485d.*
  **Entscheidung:** `umsetzen` — **umgesetzt** (API 2.44.0): `POST
  /install-mode/search` + WS `install_mode.search`, `SearchDevices` nur
  BidCos-Wired, Inbox-Refresh + SPA-Button.
- **G10 — Posteingang: Erstkonfiguration beim Accept** (partial, P3 S)
  Name/Raum/Gewerk direkt beim Übernehmen vergeben (heute erst danach im
  Gerätedetail). *Empfehlung:* Accept-Body um `{name, rooms, functions}`
  erweitern, vorhandene Setter nach dem ReadyConfig-Flip aufrufen. Setzt
  K01 voraus.
  **Entscheidung:** `umsetzen`
- **G11 — Kommunikationstest / Funktionstest je Kanal** (missing, P3 M)
  Aktiver Test mit OK-Quittung wie im CCU-Posteingang. *Empfehlung:*
  `POST /devices/{addr}/test` (ping bzw. getParamset-Roundtrip, danach
  STICKY_UNREACH zurücksetzen), Ergebnis-Badge. *XML-RPC.*
  **Entscheidung:** `umsetzen` — **umgesetzt** (API 2.44.0, geräteweit):
  `POST /devices/{addr}/test` + WS `device.test` über die CCU-ReGa
  `DevStartComTest` (Start+Poll), Radio-Interfaces + SPA-Badge.
  STICKY_UNREACH-Reset und channel-level bleiben Follow-ups.
- **G12 — Kanal-Sichtbarkeit und Bediensperre** (missing, P3 M)
  Kanäle aus Bedienlisten ausblenden / Bedienung sperren (Gast-Ansichten).
  *Empfehlung:* daemon-eigene Kanal-Flags im SQLite-Store + Filterung in
  Listen/MQTT/Matter; optional ReGa-Sync (`oChannel.Visible`).
  **Entscheidung:** `umsetzen`
- **G13 — Übertragungsmodus Standard vs. gesichert (AES) je Kanal** (partial, P3 S)
  Kein Umschalt-Dialog; AES_ACTIVE ist per Default durch Sichtbarkeitsfilter
  verdeckt. *Empfehlung:* AES-Toggle als eigene Zeile im Kanal-Panel,
  Schreiben via vorhandenem putParamset; ReGa-Modus-Sync per Mini-Skript.
  **Entscheidung:** `umsetzen`
- **G14 — Team-Zuordnung (`setTeam`, z. B. Rauchmelder)** (missing, P3 M)
  TEAM wird nur gelesen. *Empfehlung:* `Operations.SetTeam` +
  `PUT /devices/{addr}/channels/{no}/team` + Team-Picker. *XML-RPC.*
  **Entscheidung:** `umsetzen` — **umgesetzt** (API 2.44.0): `setTeam` +
  `listTeams` (BidCos-RF/HmIP-RF), `GET .../team-candidates` +
  `PUT .../team` (WS `device.team_candidates`/`device.set_team`),
  `team_supported` + SPA-Team-Picker.
- **G15 — „Bestimmen"-Button (`determineParameter`)** (partial, P3 S)
  WS-Kommando `paramset.determine` existiert, hat aber keinen Aufrufer in
  der SPA. *Empfehlung:* determine-fähige Parameter im ui-schema markieren,
  Button in `ParameterField.svelte`.
  **Entscheidung:** `umsetzen`
- **G16 — Gerätespezifische Spezialdialoge** (partial, P3 L)
  E-Paper-/Statusdisplay-Zeileneditor, Jalousie-Hex-Hilfe,
  HmW-IO-Typ-Umschaltung, gemeinsames Editieren von Kanalgruppen (häufigste
  Fälle wie Klima-Party/RGBW/Display-Text sind abgedeckt). *Empfehlung:*
  priorisiert E-Paper-Editor als CDP-Erweiterung; Kanalgruppen im ui-schema
  als Gruppe mit Write-Fan-out.
  **Entscheidung:** `umsetzen` — **zurückgestellt** (2026-07-23): Der
  E-Paper-Kern (HM-Dis-EP-WM55) hängt am SUBMIT-Hexstring-Byte-Layout,
  das weder in occu noch aiohomematic steht und nur gegen ein echtes
  HM-Dis-EP-WM55 verifizierbar ist (Gerät nicht verfügbar). Die HmIP-
  Display-Familie (HmIP-WRCD/SDV) ist bereits über die `textdisplay`-CDP
  abgedeckt. Reaktivieren nur mit realer HM-Dis-EP-WM55-Validierung.
- **G17 — Geräte-Firmware: Duty-Cycle-Gate + Download-Trigger nordseitig** (partial, P3 S)
  Übersicht, Update-Trigger und Verteilphasen-Anzeige sind vorhanden; das
  Duty-Cycle-Gate vor dem Update fehlt, und der fertig verdrahtete
  `DownloadFirmware`-Backend-Pfad hat keinen REST/WS/SPA-Auslöser.
  *Empfehlung:* Gate im Handler (Warn-Confirm), Download-Endpoint ergänzen.
  **Entscheidung:** `umsetzen`

### 4.5 Direkte Verknüpfungen & Zentralenverknüpfungen

- **V01 — Globale Verknüpfungsübersicht** ✅ erledigt (0.47.0, API 2.45.0)
  `GET /api/v1/links` (+ read-only WS `links.list_all`) aggregiert alle
  Direktverknüpfungen über sämtliche Zentralen in einer flachen Liste;
  jede Verknüpfung trägt jetzt `central_name` + `interface_id`. Kein
  Wire-Change: `getLinks` mit leerer Adresse liefert pro (Zentrale,
  Interface) den interfaceweiten Link-Bestand in einem Call
  (`LinksDomain.ListAllLinks`, symmetrische Anreicherung, Dedup pro
  Zentrale, Best-Effort-Skip offline/CUxD). Optionaler `?central=`-Scope
  (404 bei unbekannter Zentrale). Neue SPA-Ansicht `#/links` mit Suche +
  Zentral-Filter, jede Zeile verlinkt zum Gerätedetail zum Bearbeiten
  (Anlegen/Ändern läuft weiter über den bestehenden Geräte-Tab).
  Getestet: Domain- + Handler- + WS-Unit-Tests, LinkList-vitest,
  Playwright light+dark-Baselines.
  **Live-CCU-validiert (2026-07-23, 172.18.4.39):** `getLinks("", 0)` liefert
  auf HmIP-RF 8 Links mit voller Metadaten (SENDER/RECEIVER/NAME/FLAGS),
  BidCos-RF leer ohne Fault — genau der interfaceweite Bestand, auf dem
  `ListAllLinks` aufsetzt.
  **Follow-up (optional):** ein zentralenübergreifender „Neue
  Verknüpfung"-Einstieg mit Quell-Kanal-Picker direkt aus der Übersicht
  (heute führt der Weg über das Gerätedetail).
- **V02 — Rollen-Matching beim Verknüpfung-Anlegen** ✅ erledigt (0.47.0)
  `channelMatchesRole` ignorierte den role-Parameter und rief pro Kandidat
  ein `getLinkPeers` — jeder link-fähige Kanal wurde für beide Rollen
  angeboten. Jetzt echte Token-Intersektion der rohen CCU
  `LINK_SOURCE_ROLES` / `LINK_TARGET_ROLES` (wie `check_role_match` in
  occu `devconfig.cgi`): `sender`-Quelle ∩ Kandidat-`LinkTargetRoles`,
  `receiver`-Quelle ∩ Kandidat-`LinkSourceRoles`. Die Rollen werden beim
  Ingest auf das Kanalmodell gestempelt (`Channel.SetLinkRoles`), also
  kein CCU-Roundtrip mehr (entfernt einen `getLinkPeers`-Call pro
  Kandidat). Leere Quell-Rollen: vorhandener Kanal ohne Richtungsrolle →
  ausgeschlossen (CCU-strikt); nur der WS-Geräte-Probe (Quelle abwesend)
  fällt auf die Präsenzprüfung zurück. Keine Schema-/API-Änderung.
  Getestet: `intersects`, Richtungs-Divergenz, Ausschluss, Präsenz-Regel,
  Model-Accessor + Pipeline-Population.
  **Live-CCU-validiert (2026-07-23, 172.18.4.39):** reales Token-Vokabular
  bestätigt — Sender `JEQ0702833:1` (HM-Sen-MDIR-O) `LINK_SOURCE_ROLES =
  "KEYMATIC SWITCH WINMATIC"` ∩ Empfänger `KEQ0843929:1` (HM-LC-Sw4-DR)
  `LINK_TARGET_ROLES = "SWITCH WCS_TIPTRONIC_SENSOR WEATHER_CS"` = {SWITCH};
  MAINTENANCE-Kanäle mit leeren Rollen werden korrekt ausgeschlossen.
  **Follow-up (optional):** Kanalgruppen (HM-Tastenpaare) +
  HmIP-RGBW-Sonderparameter — siehe W02.
- **V03 — Verknüpfung am Gerät testen (`activateLinkParamset`)** ✅ erledigt
  (0.47.0, API 2.49.0)
  Neue Operations-Methode `ActivateLinkParamset` (XML-RPC
  `activateLinkParamset(receiver, sender, longPress)`, CCU-only; CUxD/Homegear
  → `ErrUnsupported`/501). `LinksDomain.ActivateLink` löst über den
  **RECEIVER** auf (LINK-Paramset liegt am Empfänger), auditiert
  `link_activate`. REST `POST /devices/{addr}/links/test` (op-gated, 202) +
  operator-WS `links.activate_paramset`. SPA: „Test (kurz/langer
  Tastendruck)"-Buttons im LINK-Profileditor mit **Bestätigungsdialog**
  (löst den Aktor physisch aus). `links.test_profile` bleibt read-only
  (unverändert). Getestet: Backend-Dispatch (beide Bool-Werte) +
  ErrNotWired + CUxD/Homegear-Unsupported, Adapter (Receiver-Auflösung +
  Audit), Handler (202/400/501/502/503), WS-Dispatch + Write-Klassifizierung.
  **Live-CCU-validiert (2026-07-23, CCU 172.18.4.39, HM-LC-Sw4-DR
  `KEQ0843929:1`, mit Freigabe):** temporäre Verknüpfung angelegt →
  `activateLinkParamset(receiver, sender, longPress)` kurz+lang **fehlerfrei**,
  Schalter-STATE physisch False→True → Cleanup (removeLink + STATE=false, CCU
  im Ausgangszustand). Der Wire-Call ist damit gegen echte Hardware bestätigt
  (godevccu kennt `activateLinkParamset` nicht → E2E-Skip bleibt). Optionaler
  godevccu-No-op-Handler + SPA-Button-vitest/Playwright-Baseline zurückgestellt.
- **V04 — Zentralenverknüpfung pro Kanal** (partial, P2 M)
  Loom schaltet nur geräteweit (alle Press-Kanäle). *Empfehlung:*
  optionalen `channel`-Parameter an den bestehenden Endpunkten + Pro-Kanal-
  Schalter in der Kanalansicht (CCU-Verhalten: Patch 0171).
  **Entscheidung:** `umsetzen`
- **V05 — Verknüpfung umbenennen (`setLinkInfo`)** (partial, P3 S)
  Wire-Methode existiert komplett (`InterfaceClient.SetLinkInfo`), ist aber
  nirgends exponiert. *Empfehlung:* `PATCH /devices/{addr}/links` +
  Umbenennen-Dialog. *XML-RPC, vorhanden.*
  **Entscheidung:** `umsetzen`
- **V06 — Eigene Profilvorlagen (Benutzer-Easymodes)** (missing, P3 M)
  Bewährte LINK-Parametrierung als wiederverwendbare Vorlage. *Empfehlung:*
  SQLite-Store + REST-CRUD `/link-profiles` + „Als Vorlage speichern" im
  ProfileSelector. Rein Loom-seitig.
  **Entscheidung:** `umsetzen`
- **V07 — Bewegungsmelder-Helligkeits-Helfer** (missing, P3 S)
  Ist-Helligkeit des Senders per Klick als Schaltschwelle ins
  LINK-Bedingungsfeld übernehmen (`ic_md.cgi`). *Empfehlung:* UI-Helfer auf
  vorhandenen Endpunkten (BRIGHTNESS lesen → COND-Feld setzen).
  **Entscheidung:** `umsetzen`
- **V08 — Config-Pending-Hinweis nach Link-Operationen** (partial, P3 S)
  Bei schlafenden Batteriegeräten fehlt der Hinweis „wird erst beim
  nächsten Aufwachen übertragen". *Empfehlung:* RX_MODE des Zielgeräts
  prüfen (im Modell vorhanden), Info-Toast/Banner.
  **Entscheidung:** `umsetzen`
- **V09 — Zentralenverknüpfung deaktivieren nullt auch PRESS_LONG** (partial, P3 S)
  Loom nullt nur PRESS_SHORT (aiohomematic-Verhalten); die CCU setzt beide
  Zähler, sonst bleibt die interne Verknüpfung im Gerät aktiv.
  *Empfehlung:* zweiten `reportValueUsage(PRESS_LONG, 0)`-Aufruf ergänzen;
  Abweichung in `docs/parity/by_design.md` dokumentieren.
  **Entscheidung:** `umsetzen`
- **V10 — Schutzlogik + Confirm beim Deaktivieren der Zentralenverknüpfung** (partial, P3 M)
  Keine Prüfung auf CCU-Programm-Nutzung, kein Bestätigungsdialog (das
  Panel nutzt zudem einen Inline-Banner statt confirmStore/toastStore —
  Abweichung vom SPA-Betriebskonzept). *Empfehlung:* Confirm + Warnhinweis
  minimal; Ausbaustufe: Programm-Referenzprüfung per ReGa.
  **Entscheidung:** `umsetzen`
- **V11 — DutyCycle-/Batterie-Hilfetext zur Zentralenverknüpfung** (partial, P3 S)
  Der operativ wichtigste Erklärtext fehlt (ohne Verknüpfung keine
  Tasterevents; Aktivieren erhöht DutyCycle/Batterieverbrauch).
  *Empfehlung:* Hilfe-Hinweis im CentralLinksPanel, de+en.
  **Entscheidung:** `umsetzen`

### 4.6 Organisation: Favoriten, Benutzer

- **O01 — Favoritenseiten bedienbar machen** (partial, P2 M)
  Geräte-Pins sind reine Sprungkarten; Kanäle und Programme sind nicht
  pinbar. *Empfehlung:* Kachel-Technik aus `Overview.svelte` (CDP-/
  Auto-Tiles) wiederverwenden, Pin-Typen um `channel`/`program` erweitern.
  Rein Loom-seitig.
  **Entscheidung:** `offen`
- **O02 — Favoriten-Editor (Seiten, Reihenfolge, Layout)** (missing, P2 L)
  Mehrere benannte Seiten, Drag-and-Drop-Reihenfolge, Trennzeilen,
  Spaltenzahl. *Empfehlung:* serverseitiges Seitenmodell (SQLite +
  REST-CRUD `/favorite-pages`); CCU-Layout-Artefakte (Ausrichtung,
  Namensposition) entfallen. Rein Loom-seitig.
  **Entscheidung:** `offen`
- **O03 — Startseite pro Benutzer** (missing, P2 S)
  Konfigurierbare Einstiegsroute nach Login. *Empfehlung:* `start_route`
  über den vorhandenen `/me/preferences`-Store; `App.svelte` wertet sie
  beim Initial-Load aus; Auswahlfeld in Settings.
  **Entscheidung:** `offen`
- **O04 — Favoriten-Zuordnung: global / an Benutzer / als Startseite** (missing, P3 M)
  Geteilte Seiten, Admin-Zuweisung, Startseiten-Flag. *Empfehlung:*
  owner/visibility-Feld auf dem Seitenmodell (setzt O02 voraus).
  **Entscheidung:** `offen`
- **O05 — Auto-Login (Wandtablet/Kiosk)** (missing, P3 M)
  *Empfehlung:* Opt-in-Config mit Subject + Pflicht-CIDR-Beschränkung als
  zusätzliche Auth-Quelle in `internal/auth/chain.go` (Muster:
  Ingress-Pfad); Sicherheitsabwägung als ADR.
  **Entscheidung:** `offen`

### 4.7 Systemvariablen & Diagramme

- **SV01 — Sysvar-CRUD-Feinheiten** (partial, P2 S)
  Umbenennen (`oSv.Name()` + `name` im PATCH), Beschreibung beim Anlegen,
  Typwechsel in place; dazu ein realer Bug: der SPA-Edit-Dialog patcht
  Wertelisten realer CCU-Variablen nie, weil er auf `ENUM` prüft, der
  Daemon aber den Wire-Typ `LIST` liefert (auch e2e-Fixtures korrigieren).
  *ReGa + SPA.*
  **Entscheidung:** `umsetzen`
- **SV02 — Alarm-Variable (virtuelle Alarmlinie) anlegen** (missing, P2 S)
  Bestehende Alarm-Variablen werden korrekt gespiegelt/quittiert; nur das
  Anlegen fehlt. *Empfehlung:* ALARM-Zweig in `create_system_variable.fn`
  (`ivtBinary`/`istAlarm`), Enum in Request/openapi.yaml, SPA-Option.
  *ReGa.*
  **Entscheidung:** `umsetzen`
- **SV03 — Diagramm-Definitionen (benannte Multi-Serien-Diagramme)** ✅ erledigt
  (0.47.0, API 2.50.0)
  Loom-natives Feature: Tabelle `diagram_configs` in der Haupt-DB (Migration
  029, Owner+Visibility private/shared, config_json mit Serien-Liste,
  Validierung: nicht-leere `central` je Serie, ≤8 Serien, ≤64 KB) +
  `DiagramConfigStore` (List own+shared / Get / Create / Update / Delete,
  Owner-or-Admin). REST-CRUD `/api/v1/diagrams` (Reads jede Rolle,
  subject-scoped; Writes op-gated; Audit `diagram_config`) via cmd-Adapter
  (Store-Sentinels → Handler-Sentinels). SPA: neue `MultiSeriesChart`
  (N Avg-Linien, kategorische Palette, Legende, Per-Serie-Fallback,
  Bereichs-Toolbar) + `Diagrams.svelte` (Liste + Editor mit Serien-Builder)
  + `#/diagrams`-Route + Nav + i18n en+de.
  **Gating (auf Wunsch):** die gesamte Diagramm-Fläche (Nav + Seite) ist an
  die neue `history.v1`-Info-Capability gebunden und nur sichtbar, wenn die
  Verlaufsaufzeichnung aktiv ist.
  Getestet: Store-CRUD/Ownership/Validierung/Multi-CCU, Handler
  (200/201/204/400/401/403/404/503 + Audit), Contract-Walk-Fake,
  Diagrams-Gating-vitest.
  **Follow-up (zurückgestellt):** gebündelter `GET /diagrams/{id}/data`,
  WS-Commands, Editor-Datenpunkt-Picker statt Freitextfelder, Playwright-Baselines.
- **SV04 — Diagramm-Anzeige: Langzeit, freier Zeitraum, Zoom, Vergleich** (partial, P2 M)
  `QueryBuckets` liest nur die Raw-Tabelle (Retention 30 d), obwohl
  Hourly-/Daily-Rollups existieren → Tier-Fallback einbauen (dann trägt
  `GET /history` 13+ Monate); von-bis-Picker (API kann es bereits),
  Drag-Zoom, Vergleichszeitraum. Rein Loom-seitig.
  **Entscheidung:** `offen`
- **SV05 — Wertelabels (ValueName0/1) + Visible-/Logging-Flags** (missing, P3 M)
  Logik-/Alarm-Variablen zeigen nacktes true/false statt z. B.
  „anwesend/abwesend". *Empfehlung:* Felder in Fetch/DTO/PATCH durchreichen
  (SysVar.getAll liefert sie bereits), Labels am Switch rendern. *JSON-RPC +
  ReGa.*
  **Entscheidung:** `umsetzen`
- **SV06 — Kanalzuordnung einer Sysvar schreibbar** (partial, P3 S)
  Lesen/Spiegeln vorhanden; Setzen/Ändern/Entfernen fehlt (`chn_id` ist
  hartkodiert -1). *Empfehlung:* Parameter durchreichen + Kanal-Picker im
  Edit-Dialog. *JSON-RPC + ReGa.*
  **Entscheidung:** `umsetzen`
- **SV07 — Sysvar-Verwendungsübersicht (Programme) + Lösch-Warnung** ✅ erledigt
  (0.47.0, API 2.47.0)
  `GET /api/v1/sysvars/{name}/usage` (+ read-only WS `sysvars.usage`) über
  das neue ReGa-Skript `usage_by_sysvar.fn`, das die CCU-native
  `DPEnumUsagePrograms()` der Variablen nutzt (wie das CCU-WebUI) — erfasst
  ALLE Referenzen (Bedingungen + Aktivitäten), nicht nur die Root-Regel.
  Anreicherung aus der Hub-Programmregistry (lokalisierter Name, Unique-ID,
  `is_internal`, beobachteter Active-Status), ReGa-Name als Fallback. Neues
  optionales `SysvarUsageReader`-Interface am Hub (via SetMutator
  type-assert, ohne SysvarMutator zu brechen). SPA: Best-Effort-Warnung im
  Lösch-Confirm (blockiert das Löschen nie). Getestet: Runner, Skript-Pin
  (34→35) + Placeholder, Hub-Modell (Reader/no-reader/SetMutator-Wiring),
  Handler-Anreicherung (200/404/503), WS-Dispatch, SysvarList-vitest.
  godevccu kann `DPEnumUsagePrograms` nicht bedienen (Pattern-Engine) →
  E2E-Skip; godevccu-Handler als späterer Folge-Fix.
  **Live-CCU-validiert (2026-07-23, 172.18.4.39):** Empty-Pfad + Nicht-Leer-Pfad
  gegen echte Hardware bestätigt. Nach Anlegen eines Programms `AAAb`, das die
  Sysvar `bbb1_2` referenziert, liefert das unveränderte `usage_by_sysvar.fn`
  exakt die vom Handler erwartete Form:
  `[{"id":"6924","name":"AAAb","active":true}]` — `DPEnumUsagePrograms()`
  findet die Referenz, ID/`UriEncode`-Name/`active`-Boolean stimmen, JSON-Framing
  korrekt. Der Inter-Element-Trenner (`WriteLine(',')`) feuert erst ab dem
  2. Programm auf derselben Sysvar; mit nur einer Referenz nicht ausgelöst,
  aber der `foreach`-Rumpf (der einzige CCU-seitige Unbekannte) ist damit
  verifiziert.
- **SV08 — CSV-Export der Diagrammdaten** (missing, P3 S)
  *Empfehlung:* clientseitig aus den geladenen Buckets (Blob-Download);
  optional `?format=csv` am `GET /history` für API-Nutzer.
  **Entscheidung:** `offen`
- **SV09 — Messwerterfassung kanalgenau konfigurieren** (partial, P3 M)
  Globs matchen nur Parameternamen über alle Geräte („`*POWER*`", nicht
  „nur POWER von Gerät X"); keine Nicht-Experten-UI. *Empfehlung:*
  kanal-scoped Patterns (`ADDR:4/POWER`) im Recorder-Filter + einfache
  Aufnahme-Verwaltung in der SPA. Rein Loom-seitig.
  **Entscheidung:** `offen`
- **SV10 — Protokoll-Toggle am einzelnen Datenpunkt** ✅ erledigt
  (0.47.0, API 2.46.0)
  „Aufzeichnen"-Switch im History-Tab des Gerätedetails, der die
  Glob-Richtlinie (`Include`/`Exclude`) je Datenpunkt-Instanz überstimmt;
  „auf Standard zurücksetzen" löscht die Übersteuerung. Persistenz: sparse
  Tabelle `measurement_recording_overrides` in der History-DB (Migration
  004), In-Memory-Overlay (`history.RecordingOverrides`) am Recorder-Hot-Path
  (kein Platten-Read je Event). REST: `GET`/`PUT /api/v1/history/recording`
  (REST-only wie History/Energy — kein WS). Numeric- + Live-Provenance-Guards
  bleiben wirksam (Force-On kann keinen Nicht-Numerik-/Nicht-Live-Wert
  aufzeichnen). Purge bei Geräte-/Zentral-Entfernung mit den Messwerten.
  Getestet: Store-CRUD/Purge, Overlay + Recorder-Precedence, Handler,
  RecordToggle-vitest. Gated hinter dem Opt-in-History-Feature (E2E-Skip).

### 4.8 Systemsteuerung

- **SY01 — Backup erstellen: Fallback für Original-CCU3** (partial, P2 S)
  Der Trigger läuft über `createBackup.sh` (nur OpenCCU/RaspberryMatic).
  *Empfehlung:* Fallback per HTTP-GET auf
  `cp_security.cgi?action=create_backup` mit Session (Muster
  `HTTPBackupRestorer` existiert). *HTTP-CGI.*
  **Entscheidung:** `offen`
- **SY02 — Backup-Restore: Upload externer .sbk + Vorab-Validierung** (partial, P2 M)
  Restore geht nur aus daemon-eigenen Backups. *Empfehlung:*
  `POST /backups/upload` (multipart) + Validierungsschritt
  (Signatur-/Versions-Check der cp_security-Antwort auswerten). *HTTP-CGI.*
  **Entscheidung:** `offen`
- **SY03 — System-Sicherheitsschlüssel (AES) setzen/ändern** (missing, P2 L)
  *Empfehlung:* XML-RPC `changeKey` (rfd) + `crypttool -S` per
  ReGa-`system.Exec` (Key-Index läuft nur auf der Zentrale);
  `POST /system/security-key` mit Validierung; destruktiv → Confirm
  zwingend. *XML-RPC + ReGa.*
  **Entscheidung:** `offen`
- **SY04 — Firewall-Konfiguration der CCU** (missing, P2 M)
  Modus (voll/eingeschränkt) + IP-Freigabelisten. *Empfehlung:*
  `Firewall.get/setConfiguration` per JSON-RPC + Karte mit
  Selbst-Aussperr-Schutz (eigene Daemon-IP immer freihalten). *JSON-RPC.*
  **Entscheidung:** `offen`
- **SY05 — Position (Koordinaten) + Zeitzone für Astro** (missing, P2 M)
  Wichtig, weil Loom Astro-Schedules bereits editiert — falsche Koordinaten
  verfälschen alle Sonnenzeiten; Voraussetzung für PR04. *Empfehlung:*
  `system.Longitude()/Latitude()` per ReGa-Skript (reines ReGa-DOM),
  Read-Back in `/system/ccu`, Karte im System-Tab. *ReGa.*
  **Entscheidung:** `offen`
- **SY06 — Zentralen-Firmware-Update: Vorab-Backup, EULA/Changelog, CCU3-Pfad** (partial, P2 M)
  Check + unbeaufsichtigte Installation funktionieren nur auf
  OpenCCU/RaspberryMatic. *Empfehlung:* Vorab-Backup als Option verketten,
  Changelog-Link; Original-CCU-Pfad nur bei Bedarf, sonst by-design
  dokumentieren.
  **Entscheidung:** `offen`
- **SY07 — Recovery-Modus + „CCU-Wartung"-Karte** (missing, P2 M)
  OpenCCU-JSON-RPC `RecoveryMode.enter` (nicht auf Original-CCU3, nicht auf
  oci/lxc → Capability-Gating via `get_backend_info.fn`-Erweiterung).
  *Empfehlung:* Settings-Karte „CCU-Wartung" pro Central als gemeinsamer
  Landeplatz für Recovery/Reboot/Shutdown/SafeMode; Confirm + Hinweis auf
  die Recovery-Oberfläche auf der CCU-IP. *JSON-RPC.*
  **Entscheidung:** `offen`
- **SY08 — SSH-Zugang + root-Passwort** (missing, P3 M)
  *Empfehlung:* `CCU.getSSHState/setSSH/setSSHPassword/restartSSHDaemon`
  per JSON-RPC, Admin-Panel im System-Tab. *JSON-RPC.*
  **Entscheidung:** `offen`
- **SY09 — Auth für CCU-Remote-APIs erzwingen (`CCU.setAuthEnabled`)** (missing, P3 S)
  Sicherheitsrelevant (offene XML-RPC-API schwächt das Gesamtsystem); der
  Getter existiert ungenutzt im Transport. *Empfehlung:* Setter + Toggle +
  lighttpd-Neustart-Handling. *JSON-RPC.*
  **Entscheidung:** `offen`
- **SY10 — Automatische HTTPS-Umleitung der CCU** (missing, P3 S)
  Getter existiert ungenutzt. *Empfehlung:* Setter + Toggle; Wechselwirkung
  mit der vom Daemon genutzten Basis-URL beachten. *JSON-RPC.*
  **Entscheidung:** `offen`
- **SY11 — Session-Timeout der Loom-Sessions konfigurierbar** (partial, P3 S)
  TTL ist hart codiert (12 h). *Empfehlung:* cfg-Feld
  `north.rest.auth.session_ttl` (+ idle_ttl) inkl. i18n-Labels und
  Contract-Test. Rein Loom-seitig.
  **Entscheidung:** `offen`
- **SY12 — Uhrzeit/Datum der Zentrale manuell setzen** (missing, P3 M)
  *Empfehlung:* ReGa-`system.Exec` (date/hwclock) + Interface-Clock-Sync;
  niedrig, weil NTP der Normalfall ist. *ReGa/OS.*
  **Entscheidung:** `offen`
- **SY13 — NTP-Server der Zentrale konfigurieren** (missing, P3 M)
  *Empfehlung:* Sync-Status lesen (chronyc via ReGa-`system.Exec`) als
  Diagnose-Anzeige; Serverliste schreiben per Exec-Skript. *ReGa/OS.*
  **Entscheidung:** `offen`
- **SY14 — CCU-HTTPS-Zertifikat verwalten** (partial, P3 M)
  Loom-eigenes Zertifikat ist tauschbar; das CCU-`server.pem` nicht
  (relevant für `tls_insecure_skip_verify`). *Empfehlung:* Upload an
  `cp_network.cgi?action=cert_upload` analog HTTPBackupRestorer + lighttpd-
  Neustart. *HTTP-CGI + JSON-RPC.*
  **Entscheidung:** `offen`
- **SY15 — Zentrale herunterfahren (Poweroff)** (missing, P3 S)
  *Empfehlung:* analog K03 im selben Endpoint-Namensraum; nachrangig.
  *ReGa.*
  **Entscheidung:** `offen`
- **SY16 — Abgesicherter Modus (SafeMode)** (missing, P3 S)
  *Empfehlung:* JSON-RPC `SafeMode.enter` neben dem CCU-Reboot; sinnvoll
  erst mit einer Addon-Sicht (E05/E06). *JSON-RPC.*
  **Entscheidung:** `offen`
- **SY17 — Funk-/LAN-Gateway-Verwaltung (BidCos)** (missing, P3 L)
  Gateway-Status/Firmware/DutyCycle + Geräte-Zuordnung
  (`setBidcosInterface`). Nur für BidCos-Bestandsanlagen mit Gateways
  relevant. *JSON-RPC + XML-RPC.*
  **Entscheidung:** `offen`
- **SY18 — Energiekosten-Satz (Preis pro kWh)** (missing, P3 S)
  *Empfehlung:* rein Loom-lokal: cfg-Feld + Kostenberechnung in
  `handlers/energy.go` + Anzeige in Energy (i18n-Pflicht beachten).
  **Entscheidung:** `offen`
- **SY19 — Werksreset der Zentrale** (missing, P3 M)
  Nur per ReGa-`system.Exec` (Marker-Datei + Reboot) möglich; hohes
  Zerstörungspotenzial. *Empfehlung:* eher ausschließen — die
  Recovery-WebUI der CCU bleibt der sichere Weg.
  **Entscheidung:** `offen`
- **SY20 — CCU-Netzwerkkonfiguration (IP/DNS/Hostname)** (missing, P3 L)
  Keine API; die WebUI schreibt Konfig-Dateien direkt. Fehlkonfiguration
  kappt die Verbindung Daemon→CCU. *Empfehlung:* eher ausschließen
  (Non-Goal dokumentieren, nur Anzeige in `/system/ccu` ausbauen);
  Container-Betrieb blendet die Seite ebenfalls aus.
  **Entscheidung:** `offen`

### 4.9 Diagnose & Meldungen

- **D01 — Meldungszähler in der Kopfzeile/Sidebar + Live-Reload** (partial, P2 S)
  Die `hub.*_messages`-Broadcasts existieren serverseitig, werden aber von
  keiner globalen Fläche konsumiert. *Empfehlung:* kleiner Store + Badge in
  der Sidebar mit Direktsprung; MessageList bei Broadcast nachladen.
  Rein Loom-seitig.
  **Entscheidung:** `offen`
- **D02 — DutyCycle/Carrier-Sense pro Interface (inkl. LAN-Gateways)** (partial, P2 M)
  Heute nur gerätebasiert (HAP/DRAP/Funkmodul-Gerät); reine BidCos-Anlagen
  haben gar keine Anzeige. *Empfehlung:* `Interface.listBidcosInterfaces`
  periodisch pollen, als Felder an `GET /interfaces` + Spalte in
  Diagnostics mit Warnschwellen; als Gate für G17 nutzen. *JSON-RPC.*
  **Entscheidung:** `umsetzen`
- **D03 — Systemprotokoll (chronologisches Ereignisprotokoll)** (partial, P2 L)
  Der Recorder verwirft bool/enum/string; Sysvar-Änderungen werden nicht
  aufgezeichnet; keine systemweite Ereignisliste/CSV/Löschen. *Empfehlung:*
  Event-Log-Tier im History-Subsystem + `GET /api/v1/protocol` (Filter,
  CSV, Löschen) + SPA-Route. Kein CCU-Zugriff nötig.
  **Entscheidung:** `offen`
- **D04 — Loglevel der CCU-Dienste (rfd/hs485d/ReGa)** (missing, P3 M)
  *Empfehlung:* XML-RPC `logLevel` an rfd/hs485d, ReGa-Level per
  Mini-Skript; „CCU-Loglevel"-Karte neben dem LogLevelsPanel. Syslog-Ziel +
  HMIPServer-Level sind OS-gebunden → auslassen. *XML-RPC + ReGa.*
  **Entscheidung:** `offen`
- **D05 — CCU-Logfile-Download (/var/log/messages, hmserver.log)** (missing, P3 L)
  Keine RPC-Schnittstelle; der WebUI-CGI streamt lokale Dateien.
  *Empfehlung:* CGI-Session-Proxy (fragil) oder künftiger CCU-Agent (A1);
  bis dahin bewusste Lücke.
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
  **Entscheidung:** `offen`
- **E06 — Addon-Vollverwaltung (Install/Update/Uninstall)** (missing, P3 XL)
  Remote nicht sauber machbar (keine RPC-API; rc.d + tar-Upload =
  OS-Zugriff). *Empfehlung:* eher ausschließen bzw. an die
  Agent-Entscheidung (A1) koppeln.
  **Entscheidung:** `offen`

### 4.11 Wochenprofile (Restlücken)

Die Wochenprofil-/Heizprofil-Editoren (HmIP P1–P6, BidCos HM-TC-IT,
Schalt-Wochenprogramme inkl. Astro/Kopierfunktionen, OpenCCU-0193-Felder)
sind abgedeckt. Offen:

- **W01 — HM-CC-RT-DN-Temperaturprofil (präfixloses Schema)** ✅ erledigt
  (0.47.0, Layer 1)
  HM-CC-RT-DN / HM-CC-RT-DN-BoM tragen ihr einziges Wochenprofil als
  präfixlose `ENDTIME_/TEMPERATURE_`-Keys direkt im geräteweiten
  MASTER-Paramset (kein `P[1-6]_`-Präfix, kein dedizierter Schedule-Kanal).
  Der Resolver (`FindScheduleChannel` Path 3 → `device.ChannelNumberDevice`),
  der Parser (`slotPattern` mit optionalem Präfix → P1) und der Writer
  (`serializeClimateScheduleBare` + `climateScheduleIsBare`-Erkennung aus
  der MASTER-Beschreibung) behandeln das Bare-Schema jetzt bidirektional;
  ein Präfix-Write würde auf der CCU still no-op'en. Keine
  API-Kontraktänderung (Read `404`→`200`). Getestet: Unit-Tests der Helfer
  + End-to-End-Round-Trip gegen godevccu
  (`tests/integration/schedule_bare_e2e_test.go`).
  **Layer 2 (Follow-up, nicht in 0.47.0):** Der Metadaten-DP
  (`week_profile`) + MQTT-Wochenprofil-Discovery bleiben still, weil die
  Normalisierung das Root-Profil ablöst (RT-DN hat `ScheduleChannelNo=nil`).
  Der architektonisch saubere Fix ist zuerst upstream: HM-CC-RT-DN in
  aiohomematic auf `schedule_channel_no=BIDCOS_DEVICE_CHANNEL_DUMMY`
  registrieren, dann Profile regenerieren + Modell-Snapshot neu basieren.
- **W02 — Universallicht-Wochenprogramm: Farbe/Effekt je Schaltpunkt** ✅ Slice 1
  erledigt (0.47.0, API 2.48.0)
  `WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE/VALUE` (HmIP-RGBW/DALI/LSC)
  + `WP_OUTPUT_BEHAVIOUR` (HmIP-BSL) werden jetzt als **opake Ints** durch
  DTO- und Model-Pfad getragen (`SimpleScheduleEntry.color_type/color_value/
  output_behaviour`, `ScheduleField`-Enum + Filter erweitert). Emit nur bei
  Vorhandensein (nil ≠ 0), an den aktuellen Slot des Eintrags geklebt →
  deterministischer Erhalt über Reorder/Insert/Delete (vorher nicht-
  deterministisch verwaist/vererbt). `ColorCapable`-Flag am Schedule; SPA
  zeigt eine **read-only Farb-Kategorie-Badge** je Schaltpunkt (Slice-1-sicher,
  kein Write). Getestet: Round-Trip/Reorder/0-Erhalt (DTO + Model), Filter,
  Editor-vitest.
  **Slice 2 (zurückgestellt, braucht Live-RGBW-Gerät + Freigabe):** die
  20-Bit-`..._VALUE`-Packung (Hue/Sättigung|Kelvin|Effekt) decodieren/encodieren
  + editierbares Farbwidget. Die Packung ist in occu/aiohomematic nicht
  dokumentiert → Encode muss gegen ein echtes HmIP-RGBW (172.18.4.29,
  benanntes Zielgerät + Schreibfreigabe) validiert werden.

---

## 5. Bereits abgedeckt (Kurzüberblick)

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

## 7. Umsetzungsplan (nur beschlossene Punkte)

Welle 1 besteht aus acht unabhängigen, klein geschnittenen Paketen
(je 1–2 PRs, parallelisierbar); ab Welle 2 folgen die großen Blöcke.
Abhängigkeiten: K01 vor G10 (gleiches Paket), A3-ADR vor GR02,
GR02 vor GR03–GR05.

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

Zurückgestellt (Status `offen`) bleiben insbesondere der
Programmeditor-Block (PR01–PR06, PR09, PR10), Favoriten/Benutzer (O01–O05),
Diagramm-Anzeige/Export (SV04, SV08, SV09), die gesamte Systemsteuerung
(SY01–SY20), Diagnose-Ausbau (D01, D03–D05) und das Addon-Umfeld
(E02, E05, E06) samt der ADR-Fragen A1/A2/A4. Abgelehnt: E01, E03, E04.

Innerhalb jeder Welle gelten die bestehenden Regeln: openapi.yaml zuerst,
Contract-Tests bei Protokollgrenzen, i18n de+en vollständig, vier
Theme-Kombinationen, Playwright-Baselines für neue Views.
