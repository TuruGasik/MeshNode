#!/bin/sh
set -e

# Seed an empty node database so nginx can serve /nodes.json immediately
if [ ! -f /data/nodes.json ]; then
  echo '{}' > /data/nodes.json
fi

# Start meshobserv in the background (MQTT listener → writes /data/nodes.json)
/usr/bin/meshobserv \
  -m "${MQTT_BROKER:-tcp://meshnode-mqtt:1883}" \
  -u "${MQTT_USERNAME:-meshmap}" \
  -p "${MQTT_PASSWORD:-meshmap}" \
  -f /data/nodes.json &

# Start nginx in the foreground
exec nginx -g "daemon off;"
