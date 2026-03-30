#!/usr/bin/env sh
set -eu

TEMPLATE_PATH="/mosquitto/config/mosquitto.conf.template"
RENDERED_PATH="/tmp/mosquitto.conf"

required_vars="BRIDGE_COMMUNITY_USER BRIDGE_COMMUNITY_PASS BRIDGE_GLOBAL_USER BRIDGE_GLOBAL_PASS BRIDGE_MESHNODEID_USER BRIDGE_MESHNODEID_PASS"
for var_name in $required_vars; do
  eval "var_value=\${$var_name:-}"
  if [ -z "$var_value" ]; then
    echo "[ERROR] Required environment variable is missing: $var_name" >&2
    exit 1
  fi
done

envsubst < "$TEMPLATE_PATH" > "$RENDERED_PATH"
exec /usr/sbin/mosquitto -c "$RENDERED_PATH"
