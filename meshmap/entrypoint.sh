#!/bin/sh
set -e

# Seed an empty node database so meshobserv can warm-start
if [ ! -f /data/nodes.json ]; then
  echo '{}' > /data/nodes.json
fi

# Start meshobserv in the background (MQTT listener → SQLite + in-memory NodeDB)
# meshobserv uses log.Fatal() on timeout which kills the process, so we
# wrap it in a retry loop.
(
  while true; do
    echo "[meshmap] starting meshobserv (connecting to ${MQTT_BROKER:-tcp://meshnode-mqtt:1883})..."

    # Give broker time to establish bridges on first start
    sleep 10

    /usr/bin/meshobserv \
      -m "${MQTT_BROKER:-tcp://meshnode-mqtt:1883}" \
      -u "${MQTT_USERNAME:-meshmap}" \
      -p "${MQTT_PASSWORD:-meshmap}" \
      -f /data/nodes.json || true

    echo "[meshmap] meshobserv exited ($?); retrying in 10s..."
    sleep 10
  done
) &

# Start nginx in the foreground
exec nginx -g "daemon off;"
