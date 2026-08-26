#!/usr/bin/env node
// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.
//
// Converts the live `<text>` in assets/logo/wordmark*.svg to vector
// <path> elements derived from Inter Bold (OFL via @fontsource/inter).
// That way the wordmark renders identically regardless of installed
// system fonts (PDF export, GitHub README, third-party docs).
//
// Run:  node script/wordmark_to_paths.mjs
// Or:   make assets-wordmark-paths

import { readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
// Resolve from the SPA's node_modules to avoid a duplicate npm root.
import * as opentype from "../assets/ui/node_modules/opentype.js/dist/opentype.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");
const fontPath = resolve(
  root,
  "assets/ui/node_modules/@fontsource/inter/files/inter-latin-700-normal.woff",
);
const outDir = resolve(root, "assets/logo");

const fontBuf = readFileSync(fontPath);
const arrBuf = fontBuf.buffer.slice(
  fontBuf.byteOffset,
  fontBuf.byteOffset + fontBuf.byteLength,
);
const font = opentype.parse(arrBuf, { lowMemory: false });

// Layout matches the live-text wordmark.svg before this script ran:
//   font-size: 68, baseline-y: 95, start-x: 135, letter-spacing: -1.5
// We approximate letter-spacing by shrinking each glyph's advance by
// 1.5 px — opentype.js exposes per-glyph paths so we can place each
// glyph individually.
const FONT_SIZE = 68;
const BASELINE_Y = 95;
const START_X = 135;
const LETTER_SPACING = -1.5;

function buildSegment(text, x0) {
  // Returns { d, advance } where `d` is the combined SVG path data
  // for the whole substring and `advance` is the total horizontal
  // advance (used to position the next segment).
  let x = x0;
  let d = "";
  for (const ch of text) {
    const glyph = font.charToGlyph(ch);
    const path = glyph.getPath(x, BASELINE_Y, FONT_SIZE);
    d += (d ? " " : "") + path.toPathData(2);
    x += (glyph.advanceWidth / font.unitsPerEm) * FONT_SIZE + LETTER_SPACING;
  }
  return { d, advance: x - x0 };
}

const go = buildSegment("go", START_X);
const homematic = buildSegment("homematic", START_X + go.advance);

const ICON_COLOR = `<g transform="translate(10,10) scale(0.6)">
    <polygon points="15,100 57.5,26.4 142.5,26.4 185,100 142.5,173.6 57.5,173.6" fill="#1E40AF"/>
    <path d="M 60 105 L 100 70 L 140 105 L 140 150 L 60 150 Z" fill="none" stroke="#FFFFFF" stroke-width="9" stroke-linejoin="round" stroke-linecap="round"/>
    <rect x="92" y="118" width="16" height="32" rx="2" fill="#14B8A6"/>
  </g>`;

const ICON_MONO = `<g transform="translate(10,10) scale(0.6)">
    <polygon points="15,100 57.5,26.4 142.5,26.4 185,100 142.5,173.6 57.5,173.6" fill="none" stroke="currentColor" stroke-width="8" stroke-linejoin="round"/>
    <path d="M 60 105 L 100 70 L 140 105 L 140 150 L 60 150 Z" fill="none" stroke="currentColor" stroke-width="8" stroke-linejoin="round" stroke-linecap="round"/>
    <rect x="92" y="118" width="16" height="32" rx="2" fill="currentColor"/>
  </g>`;

const ICON_INVERSE = `<g transform="translate(10,10) scale(0.6)">
    <polygon points="15,100 57.5,26.4 142.5,26.4 185,100 142.5,173.6 57.5,173.6" fill="none" stroke="#FFFFFF" stroke-width="8" stroke-linejoin="round"/>
    <path d="M 60 105 L 100 70 L 140 105 L 140 150 L 60 150 Z" fill="none" stroke="#FFFFFF" stroke-width="8" stroke-linejoin="round" stroke-linecap="round"/>
    <rect x="92" y="118" width="16" height="32" rx="2" fill="#14B8A6"/>
  </g>`;

function buildSvg({ icon, goFill, homematicFill, title }) {
  return `<svg viewBox="0 0 640 140" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="openccu-loom">
  <title>${title}</title>
  ${icon}
  <path d="${go.d}" fill="${goFill}"/>
  <path d="${homematic.d}" fill="${homematicFill}"/>
</svg>
`;
}

writeFileSync(
  resolve(outDir, "wordmark.svg"),
  buildSvg({
    icon: ICON_COLOR,
    goFill: "#1E40AF",
    homematicFill: "#14B8A6",
    title: "openccu-loom — Wordmark",
  }),
);

writeFileSync(
  resolve(outDir, "wordmark-mono.svg"),
  buildSvg({
    icon: ICON_MONO,
    goFill: "currentColor",
    homematicFill: "currentColor",
    title: "openccu-loom — Wordmark (mono)",
  }),
);

writeFileSync(
  resolve(outDir, "wordmark-inverse.svg"),
  buildSvg({
    icon: ICON_INVERSE,
    goFill: "#FFFFFF",
    homematicFill: "#14B8A6",
    title: "openccu-loom — Wordmark (inverse, für dunkle Hintergründe)",
  }),
);

console.log("regenerated wordmark{,-mono,-inverse}.svg with embedded Inter Bold paths");
console.log(`  text span: ${START_X} → ${(START_X + go.advance + homematic.advance).toFixed(1)} (font-size ${FONT_SIZE}, letter-spacing ${LETTER_SPACING})`);
