#!/command/with-contenv bashio
# shellcheck shell=bash
# Entrypoint for the OpenCCU-Loom Remote ingress proxy inside the HA add-on.
# The proxy reads /data/options.json itself (instances, log_level); only the
# listen port is fixed here — it must equal ingress_port in config.yaml.
# Tokens from a pairing with an openccu-lite box are kept in /data/paired.json
# (mode 0600), next to the options and inside the add-on's backup.

bashio::log.info "Starting OpenCCU-Loom Remote (ingress proxy)..."

exec /usr/bin/openccu-loom-remote --options /data/options.json --state /data/paired.json --listen :8234
