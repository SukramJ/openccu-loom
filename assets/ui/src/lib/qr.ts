// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

/**
 * QR-code SVG generator for Matter pairing codes.
 *
 * Uses the `qrcode` package (MIT) to compute the QR matrix via
 * `QRCode.create()`, then renders it as an inline SVG using a
 * compact path command — identical to the approach in the qrcode
 * library's own svg-tag renderer, but driven synchronously so the
 * result fits into a Svelte `$derived` rune without async plumbing.
 *
 * The Matter QR payload starts with "MT:" and is alphanumeric;
 * the library selects alphanumeric mode automatically.
 */

import { create as qrCreate } from "qrcode";

const MARGIN = 4; // quiet-zone modules (spec minimum: 4)

/**
 * Generate a compact SVG path string from the QR bit matrix.
 * Each row of set modules is emitted as a single horizontal line
 * segment, matching the output of the qrcode svg-tag renderer.
 */
function modulesToPath(data: Uint8Array, size: number, margin: number): string {
  let path = "";
  let moveBy = 0;
  let newRow = false;
  let lineLength = 0;

  for (let i = 0; i < data.length; i++) {
    const col = i % size;
    const row = Math.floor(i / size);

    if (col === 0 && !newRow) newRow = true;

    if (data[i]) {
      lineLength++;

      if (!(i > 0 && col > 0 && data[i - 1])) {
        if (newRow) {
          path += `M${col + margin} ${0.5 + row + margin}`;
          newRow = false;
        } else {
          path += `m${moveBy} 0`;
        }
        moveBy = 0;
      }

      if (!(col + 1 < size && data[i + 1])) {
        path += `h${lineLength}`;
        lineLength = 0;
      }
    } else {
      moveBy++;
    }
  }

  return path;
}

/**
 * Returns a self-contained SVG string encoding `payload` as a QR code.
 *
 * The modules are painted black on a white plate regardless of theme:
 * scanners need a light quiet zone and dark modules, so inverting this
 * for dark mode would produce a code that reads as decoration and
 * scans on nothing.
 *
 * The function signature is intentionally synchronous so callers can
 * use it inside Svelte `$derived` runes without async plumbing.
 *
 * @param payload  Arbitrary string; Matter payloads start with "MT:".
 * @param opts.size  Width/height in pixels of the rendered SVG (default 200).
 * @returns  An SVG element string suitable for `{@html ...}` injection.
 */
export function renderQrSvg(payload: string, opts?: { size?: number }): string {
  const px = opts?.size ?? 200;

  const qr = qrCreate(payload, { errorCorrectionLevel: "M" });
  const { size, data } = qr.modules;
  const totalModules = size + MARGIN * 2;
  const path = modulesToPath(data, size, MARGIN);

  return (
    `<svg xmlns="http://www.w3.org/2000/svg"` +
    ` width="${px}" height="${px}"` +
    ` viewBox="0 0 ${totalModules} ${totalModules}"` +
    ` shape-rendering="crispEdges">` +
    `<path fill="white" d="M0 0h${totalModules}v${totalModules}H0z"/>` +
    `<path stroke="black" d="${path}"/>` +
    `</svg>`
  );
}
