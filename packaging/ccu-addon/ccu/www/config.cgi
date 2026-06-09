#!/usr/bin/env tclsh
# SPDX-License-Identifier: MIT
# Add-on "Settings" entry on the CCU "Additional Software" page. Renders a
# small branded landing card (OpenCCU-Loom logo + a button into the Config
# UI), mirroring how ccu-jack presents its own logo on its settings page.
# The daemon is configured entirely through the Config UI (the SPA on the
# REST listener, port 8080); first run lands on the login / setup wizard.
#
# Implementation note: tclsh `puts {...}` treats `{` / `}` literally, so the
# markup uses inline `style="..."` attributes only — no `<style>{ }` block —
# to keep the brace quoting balanced.
puts -nonewline "Content-Type: text/html; charset=utf-8\r\n\r\n"
puts {<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>OpenCCU-Loom</title>
</head>
<body style="margin:0;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;background:#f1f5f9;color:#0f172a;">
  <div style="max-width:420px;margin:14vh auto 0;padding:32px 28px;background:#ffffff;border:1px solid #e2e8f0;border-radius:16px;box-shadow:0 1px 3px rgba(15,23,42,.08);text-align:center;">
    <svg viewBox="0 0 200 200" width="96" height="96" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="OpenCCU-Loom" style="display:block;margin:0 auto 16px;">
      <title>OpenCCU-Loom</title>
      <g stroke="#475569" stroke-width="14" stroke-linecap="round" fill="none">
        <line x1="22" y1="56" x2="178" y2="56"/>
        <line x1="22" y1="84" x2="178" y2="84"/>
        <line x1="22" y1="112" x2="178" y2="112"/>
        <line x1="22" y1="140" x2="178" y2="140"/>
      </g>
      <rect x="85" y="40" width="30" height="116" rx="8" fill="#0F766E"/>
      <g stroke="#FFFFFF" stroke-width="2" stroke-linecap="round" opacity="0.4">
        <line x1="92" y1="60" x2="108" y2="60"/>
        <line x1="92" y1="100" x2="108" y2="100"/>
        <line x1="92" y1="136" x2="108" y2="136"/>
      </g>
    </svg>
    <h1 style="margin:0 0 6px;font-size:20px;font-weight:650;">OpenCCU-Loom</h1>
    <p style="margin:0 0 22px;font-size:13px;line-height:1.5;color:#64748b;">
      Bridges this CCU to MQTT, a REST + WebSocket API, a Config UI and a Matter bridge.
    </p>
    <a id="cfg" href="#" style="display:inline-block;padding:11px 22px;background:#0F766E;color:#ffffff;font-size:14px;font-weight:600;text-decoration:none;border-radius:10px;">
      Open Config UI
    </a>
    <p style="margin:18px 0 0;font-size:11px;color:#94a3b8;">
      The Config UI is served on port 8080.
    </p>
  </div>
  <script>document.getElementById('cfg').href='http://'+window.location.hostname+':8080/app/'</script>
</body>
</html>
}
