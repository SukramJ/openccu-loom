# Security policy

This repository is **OpenCCU-Loom**, a standalone daemon that bridges
Homematic CCUs and openccu-lite systems to MQTT, REST/WebSocket, a web
Config UI and a Matter bridge. Security problems in OpenCCU-Loom — in the
daemon, its web UI, the `hmcli` CLI, the Home Assistant add-ons or the
release artifacts — are reported here.

## Supported versions

Security fixes go into the newest release and the `main` branch. The
project publishes a fast-moving 0.x series; there is no maintained older
line. The About page of the web UI shows the version you run.

## Reporting a vulnerability

Please report it privately, through GitHub's
**[Report a vulnerability](https://github.com/SukramJ/openccu-loom/security/advisories/new)**
form (the repository's *Security* tab), not as a public issue. Say which
release you run (the About page shows it), how the daemon is deployed
(binary, Docker, Home Assistant add-on), what an attacker needs (network
access, a user account, an API token, a position on the LAN) and how to
reproduce it. You get an answer as soon as possible; a fix is released and
the advisory published once it is out.

How the daemon defends the surfaces it exposes — the threat model, the
authentication options, secret handling and the limits a reverse proxy has
to cover — is in [docs/SECURITY.md](docs/SECURITY.md).

## What belongs elsewhere

- A problem in the CCU firmware or in eQ-3's software (`rfd`, `hs485d`,
  `multimacd`, `hmipserver`, ReGaHSS, the WebUI) belongs to
  [OpenCCU](https://github.com/OpenCCU/OpenCCU/security) or eQ-3, not here.
- A problem in an **openccu-lite** system or its system service occulited
  belongs to
  [openccu-lite](https://github.com/hobbyquaker/openccu-lite/security).
- A problem in the Home Assistant integration built on aiohomematic
  belongs to
  [homematicip_local](https://github.com/SukramJ/homematicip_local).

A problem in how OpenCCU-Loom *talks to* any of these systems is in scope
here.
