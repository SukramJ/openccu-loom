#!/command/with-contenv bashio
# shellcheck shell=bash
# Entrypoint for the openccu-loom daemon inside the HA add-on.
# with-contenv injects the supervisor environment so bashio::config works.

log_level="$(bashio::config 'log_level')"
if bashio::var.has_value "${log_level}"; then
    export OPENCCU_LOOM_LOG_LEVEL="${log_level}"
fi

bashio::log.info "Starting OpenCCU-Loom (log_level=${OPENCCU_LOOM_LOG_LEVEL:-info})..."

exec /usr/bin/openccu-loom run
