# Support

## Looking for help with OpenCCU-Loom?

- **Documentation** first: <https://sukramj.github.io/openccu-loom/> —
  getting started, configuration, the admin guides and the integration
  pages (MQTT, REST/WebSocket, Matter, Home Assistant).
- **Bugs and feature requests** go to this repository's
  [issue tracker](https://github.com/SukramJ/openccu-loom/issues). Please
  name the version you run (the About page of the web UI shows it), how
  the daemon is deployed (binary, Docker, Home Assistant add-on) and which
  system it talks to (CCU model and firmware, or openccu-lite), and attach
  the relevant part of the log. The issue templates ask for exactly this.
- **Security problems** are reported privately: see
  [SECURITY.md](SECURITY.md).

## What belongs elsewhere

OpenCCU-Loom is not the Home Assistant integration: problems with
`homematicip_local` or aiohomematic belong on
[their tracker](https://github.com/SukramJ/homematicip_local/issues).
Problems of an openccu-lite system itself (the firmware, its web UI)
belong on [openccu-lite's tracker](https://github.com/hobbyquaker/openccu-lite/issues);
problems of a CCU or its firmware belong to
[OpenCCU](https://github.com/OpenCCU/OpenCCU) or eQ-3. How OpenCCU-Loom
*talks to* those systems is in scope here.
