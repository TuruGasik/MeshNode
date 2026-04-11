#!/usr/bin/env bash
# ============================================================
# MeshNode MQTT Broker Monitor
# One-shot snapshot or real-time watch of Mosquitto $SYS analytics
#
# Usage:
#   ./mqtt_monitor.sh                       # one-shot, lengkap (+ klien info)
#   ./mqtt_monitor.sh -w                    # watch mode, update tiap 10d
#   ./mqtt_monitor.sh -w 5                  # watch mode, update tiap 5d
#   ./mqtt_monitor.sh -u meshmap -p PASS    # explicit creds
#   ./mqtt_monitor.sh -l /path/to/log.log   # save to file (one-shot only)
# ============================================================

HOST="${MQTT_BROKER_HOST:-localhost}"
PORT="${MQTT_BROKER_PORT:-1883}"
USER="${MQTT_MONITOR_USER:-idmeshnode}"
PASS="${MQTT_MONITOR_PASS:-node4all}"
LOG_FILE="${MQTT_LOG_FILE:-}"
WATCH_MODE=0
WATCH_INTERVAL=10
DOCKER_CONTAINER="meshnode-mqtt"

# --- parse args ---
while getopts ":u:p:h:l:w:" opt; do
  case $opt in
    u) USER="$OPTARG" ;;
    p) PASS="$OPTARG" ;;
    h) HOST="$OPTARG" ;;
    l) LOG_FILE="$OPTARG" ;;
    w) WATCH_MODE=1
       [[ -n "$OPTARG" && "$OPTARG" =~ ^[0-9]+$ ]] && WATCH_INTERVAL="$OPTARG" ;;
    *) ;;
  esac
done

# --- format helpers ---
format_bytes() {
  local b="${1:-0}"
  [[ "$b" =~ ^[0-9]+$ ]] || { echo "0 B"; return; }
  if (( b >= 1073741824 )); then
    echo "$(echo "scale=2; $b/1073741824" | bc 2>/dev/null || echo "$b") GB"
  elif (( b >= 1048576 )); then
    echo "$(echo "scale=2; $b/1048576" | bc 2>/dev/null || echo "$b") MB"
  elif (( b >= 1024 )); then
    echo "$(echo "scale=2; $b/1024" | bc 2>/dev/null || echo "$b") KB"
  else
    echo "${b} B"
  fi
}

fmt_seconds() {
  local s="${1:-0}"
  [[ "$s" =~ ^[0-9]+ ]] || { echo "$s"; return; }
  local d=$((s / 86400))
  local h=$(( (s % 86400) / 3600 ))
  local m=$(( (s % 3600) / 60 ))
  echo "${d}d ${h}h ${m}m"
}

# --- extract value from raw data ---
val() {
  echo "$raw" | grep "^$1 " | head -1 | sed "s|^$1 ||"
}

# --- fetch $SYS stats (fast, ~5 detik) ---
fetch_stats() {
  raw=$(mosquitto_sub -h "$HOST" -p "$PORT" -u "$USER" -P "$PASS" \
    -v \
    -t '$SYS/broker/clients/+' \
    -t '$SYS/broker/connections/+' \
    -t '$SYS/broker/messages/+' \
    -t '$SYS/broker/bytes/+' \
    -t '$SYS/broker/uptime' \
    -C 20 -W 5 2>/dev/null) || true

  [[ -z "$raw" ]] && return 1

  clients_active=$(val '$SYS/broker/clients/active')
  clients_connected=$(val '$SYS/broker/clients/connected')
  clients_disconnected=$(val '$SYS/broker/clients/disconnected')
  clients_max=$(val '$SYS/broker/clients/maximum')
  clients_expired=$(val '$SYS/broker/clients/expired')
  uptime_val=$(val '$SYS/broker/uptime')
  messages_received=$(val '$SYS/broker/messages/received')
  messages_sent=$(val '$SYS/broker/messages/sent')
  messages_stored=$(val '$SYS/broker/messages/stored')
  messages_dropped=$(val '$SYS/broker/messages/dropped')
  bytes_received=$(val '$SYS/broker/bytes/received')
  bytes_sent=$(val '$SYS/broker/bytes/sent')
  return 0
}

# --- check bridge status from recent docker logs ---
check_bridges() {
  local pings
  pings=$(docker logs --since 2m "$DOCKER_CONTAINER" 2>&1 \
    | grep "Received PINGRESP from" \
    | sed 's/.*from //' \
    | grep "bridge-" \
    | sort -u) || true
  if [[ -n "$pings" ]]; then
    local count
    count=$(echo "$pings" | wc -l)
    echo "$count active"
  else
    echo "0"
  fi
}

# --- render $SYS stats ---
render_stats() {
  local uptime_fmt=""
  [[ -n "${uptime_val:-}" ]] && uptime_fmt=$(fmt_seconds "${uptime_val%% *}")

  local bridge_status="${bridge_active:-0}"
  if [[ "$bridge_status" == "0" && "$WATCH_MODE" != "1" ]]; then
    bridge_status=$(check_bridges)
  fi

  # Message rate in watch mode
  local msg_recv_rate=""
  local msg_sent_rate=""
  if [[ "$WATCH_MODE" = "1" && -n "${prev_msgs_recv:-}" && "${messages_received:-}" =~ ^[0-9]+$ ]]; then
    local diff_r=$(( messages_received - prev_msgs_recv ))
    local diff_s=$(( messages_sent - prev_msgs_sent ))
    msg_recv_rate=" (+${diff_r}/${WATCH_INTERVAL}s)"
    msg_sent_rate=" (+${diff_s}/${WATCH_INTERVAL}s)"
  fi
  prev_msgs_recv="${messages_received:-0}"
  prev_msgs_sent="${messages_sent:-0}"

  local mode_label
  if [[ "$WATCH_MODE" = "1" ]]; then
    mode_label="LIVE (${WATCH_INTERVAL}s)"
  else
    mode_label="SNAPSHOT"
  fi

  cat <<EOF
═══════════════════════════════════════════
  MeshNode MQTT Broker — ${mode_label}
  $(date '+%Y-%m-%d %H:%M:%S')
═══════════════════════════════════════════

  Connections
  ─────────────────────────────────
  Active clients:     ${clients_active:--}
  Total connected:    ${clients_connected:--}
  Total disconnected: ${clients_disconnected:--}
  Peak concurrent:    ${clients_max:--}
  Expired:            ${clients_expired:--}
  Bridge connections: ${bridge_status}

  Messages
  ─────────────────────────────────
  Received:   ${messages_received:--}${msg_recv_rate}
  Sent:       ${messages_sent:--}${msg_sent_rate}
  Stored:     ${messages_stored:--}
  Dropped:    ${messages_dropped:-0}

  Traffic
  ─────────────────────────────────
  Bytes in:    $(format_bytes "$bytes_received")
  Bytes out:   $(format_bytes "$bytes_sent")
  Net delta:   $(format_bytes "$(( ${bytes_sent:-0} - ${bytes_received:-0} ))")

  Uptime: ${uptime_fmt:--}
EOF
}

# ============================================================
# MODE: Watch (real-time loop, stats only — no docker logs)
# ============================================================
if [[ "$WATCH_MODE" = "1" ]]; then
  trap 'echo -e "\n[Watch stopped]"; exit 0' INT TERM

  prev_msgs_recv=""
  prev_msgs_sent=""

  echo -e "  [\033[1;32m●\033[0m] Watching broker at $HOST:$PORT — Ctrl+C to stop"
  echo ""

  while true; do
    if fetch_stats; then
      render_stats
    else
      echo "[ERROR] Could not connect to broker at $HOST:$PORT"
    fi
    sleep "$WATCH_INTERVAL"
  done
  exit 0
fi

# ============================================================
# MODE: One-shot (complete stats + docker logs client analysis)
# ============================================================
raw=""
if ! fetch_stats; then
  echo "ERROR: Could not connect to broker at $HOST:$PORT" >&2
  exit 1
fi

bridge_active=$(val '$SYS/broker/connections/active')
render_stats

# --- parse Meshtastic clients from Docker logs (last 1 hour) ---
raw_clients=$(docker logs --since 1h "$DOCKER_CONTAINER" 2>&1 \
  | grep "New client connected" \
  | grep -v "auto-" \
  | sort -t' ' -k5 -u \
  | sort -k1n \
  | tail -100) || true

declare -A seen_clients
client_list=""
unique_nodes=0
unique_infra=0

while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  client_id=$(echo "$line" | sed 's/.*as //' | awk '{print $1}')
  ip=$(echo "$line" | grep -oP 'from \K[^ ]+')
  [[ -z "$client_id" || "${seen_clients[$client_id]:-}" == "1" ]] && continue
  seen_clients[$client_id]=1

  ts=$(echo "$line" | awk '{print $1}')
  last_time=""
  if [[ "$ts" =~ ^[0-9]+$ ]]; then
    last_time=$(date -d @"$ts" '+%H:%M:%S' 2>/dev/null || echo "$ts")
  fi

  if [[ "$client_id" == Meshtastic* ]]; then
    unique_nodes=$((unique_nodes + 1))
  else
    unique_infra=$((unique_infra + 1))
  fi
  client_list+="  • ${client_id} (${ip}) — last:${last_time}\n"
done <<< "$raw_clients"

{
  echo ""
  echo "  Clients (non-probe, last 1h)"
  echo "  ─────────────────────────────────"
  echo "  Meshtastic nodes: ${unique_nodes}"
  echo "  Infra services:   ${unique_infra}"
  if [[ -n "$client_list" ]]; then
    echo ""
    echo -e "$client_list" | sed '/^$/d'
  fi
  echo "═══════════════════════════════════════════"
}

# --- optional log ---
if [[ -n "$LOG_FILE" ]]; then
  render_stats >> "$LOG_FILE"
  echo "[INFO] Appended to $LOG_FILE"
fi
