#!/usr/bin/env bash
set -euo pipefail

SERVICE="${1:-meshnode-mqtt}"
TAIL_LINES="${TAIL_LINES:-0}"
COMPOSE_CMD="${COMPOSE_CMD:-docker compose}"

if ! command -v docker >/dev/null 2>&1; then
  echo "[ERROR] docker command not found." >&2
  exit 1
fi

echo "[INFO] Monitoring TLS client connections for service: ${SERVICE}"
echo "[INFO] Source: ${COMPOSE_CMD} logs -f --tail ${TAIL_LINES} ${SERVICE}"
echo "[INFO] Press Ctrl+C to stop."

eval "${COMPOSE_CMD} logs -f --tail ${TAIL_LINES} ${SERVICE}" 2>&1 | awk '
function now() {
  return strftime("%Y-%m-%d %H:%M:%S")
}
function cleanup_stale(now_ts, e) {
  for (e in pending_port) {
    if ((now_ts - pending_ts[e]) > 120) {
      delete pending_port[e]
      delete pending_ts[e]
    }
  }
}
{
  msg = $0
  sub(/^.*\|[[:space:]]*/, "", msg)

  now_ts = systime()
  cleanup_stale(now_ts)

  if (match(msg, /New connection from ([^ ]+) on port ([0-9]+)\./, m)) {
    endpoint = m[1]
    port = m[2]
    pending_port[endpoint] = port
    pending_ts[endpoint] = now_ts

    if (port == "8883") {
      printf("[%s] TLS handshake started from %s\n", now(), endpoint)
      fflush()
    }
    next
  }

  if (match(msg, /New client connected from ([^ ]+) as ([^ ]+) \(([^)]*)\)\./, m)) {
    endpoint = m[1]
    client_id = m[2]
    session_meta = m[3]

    username = "-"
    if (match(session_meta, /u\x27[^\x27]*\x27/, u)) {
      username = u[0]
      sub(/^u\x27/, "", username)
      sub(/\x27$/, "", username)
    }

    if (pending_port[endpoint] == "8883") {
      printf("[%s] TLS CONNECTED  endpoint=%s  client_id=%s  username=%s\n", now(), endpoint, client_id, username)
      fflush()
    }

    delete pending_port[endpoint]
    delete pending_ts[endpoint]
    next
  }

  if (match(msg, /Client ([^ ]+) \[([^]]+)\] disconnected\./, d)) {
    endpoint = d[2]
    if (pending_port[endpoint] == "8883") {
      printf("[%s] TLS disconnected before full session  endpoint=%s\n", now(), endpoint)
      fflush()
    }
    delete pending_port[endpoint]
    delete pending_ts[endpoint]
    next
  }
}
'