#!/command/with-contenv bashio
# shellcheck shell=bash
# Entrypoint for the OpenCCU-Loom Remote ingress proxy inside the HA add-on.
# The proxy reads /data/options.json itself (instances, log_level); only the
# listen port is fixed here — it must equal ingress_port in config.yaml.

bashio::log.info "Starting OpenCCU-Loom Remote (ingress proxy)..."

exec /usr/bin/openccu-loom-remote --options /data/options.json --listen :8234
