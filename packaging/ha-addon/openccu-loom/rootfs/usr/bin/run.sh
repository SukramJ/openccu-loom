#!/command/with-contenv bashio
# shellcheck shell=bash
# Entrypoint for the openccu-loom daemon inside the HA add-on.
# with-contenv injects the supervisor environment so bashio::config works.

log_level="$(bashio::config 'log_level')"
if bashio::var.has_value "${log_level}"; then
    export OPENCCU_LOOM_LOG_LEVEL="${log_level}"
fi

# Ports are operator-configurable add-on options (host_network: true, so the
# daemon binds them directly on the host). Pass each through to the daemon's
# env override. The REST port must equal ingress_port (8119, static) for the
# Ingress panel to work — change rest_port only for direct-access setups.
rest_port="$(bashio::config 'rest_port')"
if bashio::var.has_value "${rest_port}"; then
    export OPENCCU_LOOM_REST_LISTEN=":${rest_port}"
fi
xmlrpc_callback_port="$(bashio::config 'xmlrpc_callback_port')"
if bashio::var.has_value "${xmlrpc_callback_port}"; then
    export OPENCCU_LOOM_CALLBACK_PORT="${xmlrpc_callback_port}"
fi
binrpc_callback_port="$(bashio::config 'binrpc_callback_port')"
if bashio::var.has_value "${binrpc_callback_port}"; then
    export OPENCCU_LOOM_CALLBACK_BIN_PORT="${binrpc_callback_port}"
fi

# Embedded mode: Home Assistant owns this daemon's config surface. It is an
# explicit opt-in rather than something derived from running behind Ingress —
# this add-on is also used without the Homematic(IP) Local integration, and
# there the reduced surface would remove views HA cannot replace.
ui_embedded="$(bashio::config 'ui_embedded')"
if bashio::var.has_value "${ui_embedded}"; then
    export OPENCCU_LOOM_UI_EMBEDDED="${ui_embedded}"
fi

bashio::log.info "Starting OpenCCU-Loom (log_level=${OPENCCU_LOOM_LOG_LEVEL:-info}, rest=${rest_port:-8119}, xmlrpc_cb=${xmlrpc_callback_port:-8120}, binrpc_cb=${binrpc_callback_port:-8129}, ui_embedded=${ui_embedded:-false})..."

exec /usr/bin/openccu-loom run
