# OpenCCU-Loom — Logo-Assets

Finales Markenset. Alle Dateien bestehen ausschließlich aus geometrischen
Grundformen (Hexagone, Linien, Pfade, Rechtecke) und enthalten kein
Material Dritter — sie verletzen keine Urheberrechte oder eingetragenen
Marken (insbesondere nicht eQ-3 / HomeMatic, Matter, MQTT, Go-Gopher).

## Markenpalette

| Rolle      | Hex       | Verwendung                                                          |
| ---------- | --------- | ------------------------------------------------------------------- |
| Primär     | `#1E40AF` | Korpus, Daemon-Symbol, Wortteil „go"                                |
| Akzent     | `#14B8A6` | Bridges/Verbindungen, Tür, Wortteil „homematic", interaktive Highlights |
| Hintergrund hell | `#FFFFFF` | Negativ-Elemente innerhalb gefüllter Hexagone                   |

Bewusst gewählt: kräftiges Indigoblau (Stabilität, Daemon) plus Teal
(Verbindung/Bridge). Keine Anlehnung an eQ-3-Orange und kein Go-Cyan —
damit klar abgegrenzt von beiden Referenzwelten.

## Inventar

| Datei                          | Zweck                                                |
| ------------------------------ | ---------------------------------------------------- |
| `mark-hexhome.svg`             | Bildmarke, Farb-Variante (helle Hintergründe)        |
| `mark-hexhome-mono.svg`        | Bildmarke, einfarbig via `currentColor` (Druck, Dark/Light-neutral) |
| `mark-hexhome-inverse.svg`     | Bildmarke für dunkle Hintergründe (weiße Outlines, Teal-Tür) |
| `wordmark.svg`                 | Volle Wortmarke, Farb-Variante                       |
| `wordmark-mono.svg`            | Wortmarke, einfarbig via `currentColor`              |
| `wordmark-inverse.svg`         | Wortmarke für dunkle Hintergründe                    |
| `favicon.svg`                  | Vereinfachte Bildmarke (kein Tür-Detail, kräftigere Linien) für 16/32 px |

## Bildmarke: Hexhome

Gefülltes Hexagon mit weißer Haus-Silhouette und Teal-Tür. Konzeptionell:
HomeMatic / Heimautomatisierung in einem architekturellen Rahmen
(Hexagon = Ports-and-Adapters-Architektur + Waben-Assoziation).

Die `mono`-Variante nutzt `currentColor`, sodass CSS- oder
`color`-Attribut die Farbe bestimmt — ideal für eingebettetes Inline-SVG
in der SPA, das Tailwind-`text-*`-Klassen folgt.

## Wortmarke

Kompaktes Hexhome-Icon links, zweifarbige Wortmarke `go` (Primär) +
`homematic` (Akzent). Schrift: **Inter Bold (OFL)**, beim
Generierungsschritt direkt als Vektor-`<path>` eingebettet — die
gerenderte Wortmarke ist also vom installierten System-Font
unabhängig (PDF-Export, GitHub-README, Druck).

Regenerieren bei Änderung an Wortlaut / Größe / Positionierung:

```sh
# einmalig: opentype.js + @fontsource/inter in der SPA installieren
cd assets/ui && npm install --save-dev opentype.js @fontsource/inter && cd -

# Pfad-Regeneration:
make assets-wordmark-paths     # schreibt wordmark{,-mono,-inverse}.svg neu
make assets-sync               # spiegelt anschließend in beide Senken
```

Das Skript liegt unter `script/wordmark_to_paths.mjs` und lädt
`Inter-Bold` aus `assets/ui/node_modules/@fontsource/inter/files/`.

## Einsatz

| Kontext                            | Datei                              |
| ---------------------------------- | ---------------------------------- |
| Favicon, PWA-Icon, Browser-Tab     | `favicon.svg`                      |
| App-Icon (Android, iOS, Desktop)   | `mark-hexhome.svg`                 |
| SPA-Topbar, Login-Header           | `wordmark.svg` (hell) / `wordmark-inverse.svg` (dunkel) |
| README-Header, Dokumenten-Titel    | `wordmark.svg`                     |
| HTMX-Bootstrap-Seiten (`/about`)   | `wordmark.svg`                     |
| Print / SW-Druck / E-Mail-Footer   | `wordmark-mono.svg`                |
| Avatar (GitHub, OG-Image)          | `mark-hexhome.svg`                 |

### SPA-Einbindung

Inline-SVG via Vite-Raw-Import — so greifen Tailwind-Farbklassen auf
`currentColor`:

```svelte
<script lang="ts">
  import LogoMark from '$lib/assets/logo/mark-hexhome-mono.svg?raw';
</script>

<span class="text-brand-primary dark:text-white inline-block w-8 h-8">
  {@html LogoMark}
</span>
```

### Favicon-Einbindung

In `assets/ui/index.html` (oder dem entsprechenden Bootstrap-Template):

```html
<link rel="icon" type="image/svg+xml" href="/static/favicon.svg" />
<link rel="apple-touch-icon" href="/static/icon-192.png" />
```

`favicon.svg` ist die Quelle; daraus werden 16/32/48/192/512 px PNGs
abgeleitet (siehe „PNG-Export" unten).

## Distribution & Sync

`assets/logo/` ist **Single Source of Truth**. Aus diesem Verzeichnis
werden zwei Senken gespeist:

| Senke                             | Verwendung                                                    |
| --------------------------------- | ------------------------------------------------------------- |
| `assets/ui/public/`               | Vite kopiert nach `internal/north/ui/spa_dist/` (`/app/` im Browser) |
| `internal/north/ui/assets/logo/`  | HTMX-Bootstrap via `//go:embed`, ausgeliefert unter `/ui/assets/logo/` |

Reproduzierbarer Sync per Makefile (benötigt `librsvg` für PNG-Export):

```sh
brew install librsvg   # einmalig
make assets-sync       # regeneriert PNGs + spiegelt alles in beide Senken
```

Was `make assets-sync` macht:

1. `assets-logo-png` regeneriert alle PNGs unter `assets/logo/png/`:
   - `favicon-{16,32,48,64,192,512}.png` (aus `favicon.svg`)
   - `apple-touch-icon-180.png` (aus `favicon.svg`)
   - `mark-hexhome-{256,512}.png` (aus `mark-hexhome.svg`)
   - `wordmark-{400,1200}.png` (aus `wordmark.svg`)
2. Spiegelt die benötigten Dateien (SVGs + PNGs + Manifest) in beide
   Senken. Senken sind versionskontrolliert — der Sync ist also nur bei
   Änderungen am Quellverzeichnis nötig.

## Web-Manifest

`manifest.webmanifest` definiert den PWA-Hülle (Name, Theme-Color,
Icons). Wird in der SPA über `<link rel="manifest" href="/app/manifest.webmanifest">`
referenziert.

## Lizenz

Die Logo-Dateien stehen — wie der gesamte OpenCCU-Loom-Quellcode —
unter MIT.
