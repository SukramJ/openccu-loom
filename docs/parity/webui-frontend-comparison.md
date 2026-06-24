# Frontend-Vergleich: OpenCCU-Loom SPA vs. homematicip-local-frontend Config-Panel

**Referenz (10/10):** Legacy CCU-WebUI (`occu/WebUI/www`), **ohne** die
ausgeschlossenen Bereiche *Programme (Bearbeiten)* und *Skripte*.
**Bewertung 1–10**, 10 = funktionale Gleichwertigkeit zur WebUI.
Quelle: Code-Analyse (nicht Dokumentation), Stand 2026-06-24.

- **SPA** = `openccu-loom/assets/ui/src` (Svelte 5, ~40 k LOC, 2948 i18n-Keys, de+en)
- **PANEL** = `homematicip-local-frontend/packages` (Lit, HA-Config-Panel, de+en)

> PANEL ist bauartbedingt ein **HA-Config-Panel**: alles, was Home Assistant
> selbst erledigt (User, Netzwerk, Sicherheit, Sysvars, Räume, Live-Steuerung),
> fehlt dort absichtlich. Die SPA ist eine **Standalone-Daemon-UI** und zielt
> direkt auf den WebUI-Ersatz.

---

## Gesamttabelle

| # | Funktionsbereich | SPA | PANEL | Wichtigstes Defizit ggü. WebUI |
|---|---|:---:|:---:|---|
| **A1** | Geräteliste (Suche/Filter/Gruppierung/Bulk) | **9** | 5 | SPA übertrifft WebUI (Bulk, Multi-CCU, persist. Filter). PANEL: keine Raum-/CCU-Filter, keine Bulk-Aktionen. |
| **A2** | Gerätedetail (Kanäle, Rename, Delete, Räume) | **9** | 7 | SPA ergänzt History+Audit. PANEL: kein Rename/Delete (HA-delegiert). |
| **A3** | Live-Steuerung (14 Widgets / 84 Familien) | **8** | 1 | SPA breit; einige Familien nur Fallback-Tile. PANEL: **keine Steuerung**, nur Monitoring. |
| **A4** | Anlernen / Posteingang | **8** | 6 | SPA: kein interface-selektiver Install-Mode, keine Seriennr.-Eingabe. PANEL: kein Auto-Poll. |
| **B1** | MASTER-Paramset-Editor | **8** | 7 | SPA stark (Live-Validierung, Undo, dry-run-Presets). Beiden fehlen **benannte User-Easymodes**; SPA fehlt "Reset to Defaults". |
| **B2** | Direkte Verknüpfungen | **7** | 6 | **SPA rendert nur Receiver-Seite** des Link-Paramsets — Sender-LINK fehlt (PANEL hat beides). |
| **B3** | Heiz-/Zeitprofile, Wochenprogramme | **8** | 8 | Gleichwertig; PANEL bessere Timeline-Visualisierung + Drag-Resize, SPA mehr Copy/Fill-Funktionen. |
| **C1** | Systemvariable | **8** | 1 | SPA: Voll-CRUD, alle 5 Typen. Fehlt: Kanalbindung, isLogged-Toggle, Wert-Trend. PANEL: fehlt komplett. |
| **C2** | **Räume & Gewerke** | **1** | 1 | **Beide: keine Verwaltung/Zuordnung.** Vollständige Lücke. |
| **C3** | **Favoriten / Start-Dashboard** | **1** | 1 | **Beide: kein Favoriten-/Dashboard-Konzept.** Vollständige Lücke. |
| **C4** | Verlauf / Audit / Systemprotokoll | **8** | 4 | SPA: Audit-Diff, Live-Log-Stream, Incidents. Fehlt: CSV-Export, Datums-/Zeitfilter, Pagination. |
| **C5** | Programme (nur Liste/Status) | **9** | 1 | SPA erfüllt Ziel (Liste + Enable/Execute). PANEL: fehlt. |
| **D1** | Benutzerverwaltung + API-Tokens | **8** | 1 | SPA vollständig. PANEL: HA-delegiert. |
| **D2** | Netzwerk *(Standalone: N/A)* | — | — | Konzeptionell außerhalb des Daemon-Scope (kein CCU-OS). |
| **D3** | Sicherheit (OIDC, CCU-TLS) | **6** | 1 | SPA: kein Zertifikats-Upload, kein Self-Service-Passwort, kein HTTPS-Setup des Daemon-Endpunkts. |
| **D4** | Firmware-/System-Update | **8** | 7 | SPA: Geräte-OTA + System-Update. Fehlt: manueller Firmware-Datei-Upload (Offline). |
| **D5** | Backup & Restore | **7** | 4 | SPA: Create/Download/Restore. Fehlt: Upload externer Backups, Zeitplan, Restore-Fortschritt. |
| **D6** | Wartung/Zeit/Neustart *(teils N/A)* | **4** | 1 | SPA: nur Restart + Cache-Clear. Fehlt: Log-Level-UI, Shutdown. Zeit/Display/Werksreset = N/A. |
| **D7** | App-Einstellungen / **Multi-CCU** | **9** | 3 | SPA: Multi-CCU first-class (Alleinstellung). PANEL: single-entry. |

---

## Aggregat (Kern-Scope, ohne N/A-Bereiche D2 / Zeit-Display-Werksreset)

| | SPA | PANEL |
|---|:---:|:---:|
| Ungewichteter Mittelwert über 18 anwendbare Bereiche | **≈ 7,0** | **≈ 3,6** |
| Bereiche auf WebUI-Niveau (≥8) | 11 / 18 | 1 / 18 |
| Bereiche mit Totalausfall (=1) | 2 (Räume, Favoriten) | 9 |

**Quantität:** Die SPA deckt nahezu den gesamten WebUI-Funktionsumfang ab
(18/20 Bereiche real implementiert), das PANEL deckt nur den Geräte-/Kanal-/
Profil-/Verknüpfungs-Kern ab und überlässt den Rest Home Assistant.

**Qualität:** Wo die SPA implementiert, erreicht oder übertrifft sie meist
WebUI-Niveau (Bulk-Aktionen, Live-Validierung, Undo, Multi-CCU, Audit-Diff).
Das PANEL ist im verbleibenden Kern qualitativ sehr stark (Link-Sender+Receiver,
Schedule-Timeline, Touch-UX), aber als Gesamt-WebUI-Ersatz unvollständig.

---

## Priorisierte echte Defizite der SPA (Ziel: WebUI-Ersatz)

1. **Räume & Gewerke (C2 = 1)** — keine Verwaltung/Kanalzuordnung. Eine der
   zwei Kern-Organisationsachsen der CCU fehlt vollständig.
2. **Favoriten / Start-Dashboard (C3 = 1)** — kein Schnellzugriffs-/Landing-Konzept.
3. **Link-Sender-Paramset (B2)** — `LinkConfigPanel.svelte:55-61` zeigt nur die
   Receiver-Seite; die Sender-LINK-Parametrierung fehlt (PANEL kann beides).
4. **Wartung & Sicherheit (D6=4 / D3=6)** — kein Log-Level-UI, kein
   Zertifikats-Upload, kein Self-Service-Passwortwechsel.
5. **Verlauf-Export (C4)** — kein CSV-Export, keine Datums-/Zeitfilter,
   500-Zeilen-Cap ohne Pagination gegenüber WebUI-Systemprotokoll.
6. **Anlernen (A4)** — kein interface-selektiver Install-Mode, keine manuelle
   Seriennummer-Eingabe.
7. **Benannte User-Easymodes (B1)** — beide Frontends bieten nur Read-only-Presets,
   kein "Profil speichern als…"-Flow wie `ic_neweasymode.cgi`.

## SPA-Stärken über die WebUI hinaus

- **Multi-CCU first-class** (D7) — eine Instanz, viele Zentralen.
- **Bulk-Operationen** auf der Geräteliste (Raum/Firmware).
- **Audit-Log mit before/after-Parameter-Diff**, Live-Log-Stream (SSE).
- **History-Charts** je Datenpunkt im Gerätedetail.
- **Matter-Bridge-Verwaltung** (Fabrics/Pairing/Exposure) — in WebUI nicht existent.
- **Paramset-Export/Import** als JSON-Snapshot.
