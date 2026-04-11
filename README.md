# MeshNode
MeshNode Indonesia MQTT and Map

A Mosquitto MQTT broker for Indonesian Meshtastic users with bridges to the Indonesian community server and the global Meshtastic MQTT server, plus a self-hosted [MeshMap](https://github.com/brianshea2/meshmap.net) that shows nodes on an interactive map.

## Architecture Overview

Local Meshtastic clients use the **standard topic `msh/ID_923/#`** — same as everyone else. Anti-echo and deduplication are handled transparently by the relay service using SHA-256 hashing.

The system uses **three internal namespaces** to prevent echo loops:

1. **`msh/ID_923/#`** — Standard message namespace
   - Local clients publish AND subscribe here
   - Relay publishes deduplicated inbound messages here
   - Relay also subscribes here (self-echo handled by dedup)

2. **`msh/bridge_in/ID_923/#`** — Bridge inbound namespace
   - External bridges publish raw inbound messages here
   - Only the relay service subscribes to this namespace

3. **`msh/relay/ID_923/#`** — Bridge outbound namespace
   - Relay publishes outbound messages here (from local clients)
   - Bridges pick up messages from here to send externally

## Bridges

| Connection | Server | Username |
|---|---|---|
| Indonesian Community | `mqtt.meshnode.id` | `idmeshnode` |
| Global Meshtastic | `mqtt.meshtastic.org` | `meshdev` |
| Padang | `103.141.75.100` | `meshnodeid` |

All bridges are configured to:
- **Inbound**: Remote `msh/ID_923/#` → Local `msh/bridge_in/ID_923/#`
- **Outbound**: Local `msh/relay/ID_923/#` → Remote `msh/ID_923/#`

The relay service processes messages from both `msh/ID_923/#` (local clients) and `msh/bridge_in/ID_923/#` (bridge inbound), performs SHA-256 deduplication, and routes them accordingly.

### 🛡️ MQTT Relay & Deduplication (Go Subsystem)

Since multiple bridges send the same Meshtastic network traffic from remote servers, a specialized **MQTT Relay service (written in Go)** operates as a sidecar to the local broker. 
- **Anti-Echo Loop**: Prevents identical messages from bouncing between local nodes, bridges, and external servers through a combination of namespace separation (bridges use `bridge_in/` and `relay/`) and SHA-256 dedup (self-echo and cross-direction echo are automatically dropped).
- **De-duplication**: Filters out duplicate messages received from the 3 independent bridges down to a single canonical copy. Also prevents local client messages from echoing back via external servers.
- **High Performance**: Designed entirely in Golang utilizing a lock-free `sync.Map` hash cache and goroutines to process up to 50,000 req/s out of the box with <15MB RAM usage.

## Health Monitoring

[Uptime Kuma](https://github.com/louislam/uptime-kuma) is included as a self-hosted health monitor.  
It runs on port **3001** and lets you track the broker and both bridge connections from a web dashboard.

### Recommended monitors

After opening the dashboard (`http://<your-host>:3001`) and creating an admin account, add the following monitors:

| Name | Type | Host / URL | Port / Topic |
|---|---|---|---|
| Local MQTT Broker | TCP Port | `meshnode-mqtt` (or `localhost`) | `1883` |
| Bridge – Indonesian Community | TCP Port | `mqtt.meshnode.id` | `1883` |
| Bridge – Global Meshtastic | TCP Port | `mqtt.meshtastic.org` | `1883` |
| Bridge – Padang | TCP Port | `103.141.75.100` | `1883` |
| MeshMap | HTTP(s) | `https://<your-domain>` (or `http://localhost`) | — |

> **Tip – MQTT monitor type:** Uptime Kuma also has a native **MQTT** monitor.  
> Use it to verify that messages are actually flowing through a bridge:  
> set the broker URL to `mqtt://localhost:1883`, subscribe to `msh/ID_923/#`,  
> and configure the expected keyword/value your nodes publish.

## MeshMap (Node Map)

A self-hosted deployment of [meshmap.net](https://github.com/brianshea2/meshmap.net) that connects to the local MQTT broker and displays Meshtastic nodes on an interactive web map.

### Setup

The MeshMap service needs an MQTT user to subscribe to messages on the local broker.  
Create the user before starting the stack for the first time:

```bash
# Run from the MeshNode directory – creates (or updates) a "meshmap" user
docker run --rm -v "$(pwd)/mosquitto/passwd:/mosquitto/passwd" \
  eclipse-mosquitto:2 mosquitto_passwd -b /mosquitto/passwd meshmap meshmap
```

> **Note:** If you choose a different username or password, update the `MQTT_USERNAME` / `MQTT_PASSWORD`
> environment variables in `docker-compose.yml` to match.

### Access

Open `https://<your-domain>` (port **80** / **443**) to view the node map.  
Multiple domains can be served from the same instance — e.g. `map.dari.asia` and `map.meshnode.id` both point to the same map and API.

### Environment variables (MeshMap)

| Variable | Default | Description |
|---|---|---|
| `MQTT_BROKER` | `tcp://meshnode-mqtt:1883` | MQTT broker URL |
| `MQTT_USERNAME` | `meshmap` | MQTT username |
| `MQTT_PASSWORD` | `meshmap` | MQTT password |

### Environment variables (MQTT Relay)

| Variable | Default | Description |
|---|---|---|
| `MQTT_HOST` | `meshnode-mqtt` | MQTT broker hostname |
| `MQTT_PORT` | `1883` | MQTT broker port |
| `MQTT_USERNAME` | `mqtt-relay` | MQTT username for relay |
| `MQTT_PASSWORD` | (from `.env`) | MQTT password for relay |
| `SUBSCRIBE_TOPIC` | `msh/ID_923/#` | Standard message topic (local clients + relay self-echo) |
| `SUBSCRIBE_BRIDGE_IN` | `msh/bridge_in/ID_923/#` | Bridge inbound topic |
| `BRIDGE_IN_PREFIX` | `msh/bridge_in/` | Prefix for bridge inbound topics |
| `RELAY_PREFIX` | `msh/relay/` | Prefix for outbound relay topics |
| `SOURCE_PREFIX` | `msh/` | Canonical topic prefix |
| `DEDUP_TTL` | `600` | Deduplication TTL in seconds |
| `LOG_LEVEL` | `INFO` | Log level (DEBUG/INFO/WARN/ERROR) |

## Requirements

- [Docker](https://docs.docker.com/get-docker/)
- [Docker Compose](https://docs.docker.com/compose/install/)

## Quick Start

```bash
# Clone the repository
git clone https://github.com/TuruGasik/MeshNode.git
cd MeshNode

# Copy and edit the environment file
cp .env.example .env
# Edit .env with your credentials (bridge usernames/passwords, TLS domain, etc.)

# Create MQTT users for meshmap and mqtt-relay
docker run --rm -v "$(pwd)/mosquitto/passwd:/mosquitto/passwd" \
  eclipse-mosquitto:2 mosquitto_passwd -b /mosquitto/passwd meshmap <your-meshmap-password>
docker run --rm -v "$(pwd)/mosquitto/passwd:/mosquitto/passwd" \
  eclipse-mosquitto:2 mosquitto_passwd -b /mosquitto/passwd mqtt-relay <your-relay-password>

# Start all services
docker compose up -d

# Check logs
docker compose logs -f
```

- MQTT broker → port **1883**
- MQTT broker (TLS) → port **8883**
- MeshMap (node map) → port **80** / **443** (`https://<your-domain>`)
- Uptime Kuma dashboard → port **3001** (`http://<your-host>:3001`)

## Configuration

The Mosquitto configuration is located at `mosquitto/config/mosquitto.conf`.  
Persistent data and logs are stored in Docker named volumes (`mosquitto-data`, `mosquitto-log`, `uptime-kuma-data`, `meshmap-data`).

### Local secrets / files to keep out of git

- `mosquitto/passwd`
- `mosquitto/certs/`
- `deploy_certs.sh` (local environment script with domain/path specific values)

### Optional helper scripts

- `scripts/monitor_mosquitto_tls.sh` — monitors Docker logs and prints TLS (`8883`) client connection events.
- `scripts/monitor_mqtt.py` — monitors multiple MQTT servers simultaneously (LOCAL, COMMUNITY, PADANG).
- `scripts/monitor_mqtt_relay.py` — monitors the relay dedup pipeline (bridge_in vs clean vs relayed) with stats.

> **Note:** The monitoring scripts read credentials from environment variables (`MONITOR_LOCAL_USER`, etc.).
> See `.env.example` for the full list. Run with: `source .env && python3 scripts/monitor_mqtt.py`

## Stopping

```bash
docker compose down
```

### Konfigurasi Meshtastic Client

| Parameter | Nilai |
|---|---|
| **Address** | `mqtt://kemplu.dari.asia:1883` |
| **Username** | `idmeshnode` |
| **Password** | `node4all` |
| **Root topic** | `msh/ID_923` |

> **Note:** Root topic menggunakan `msh/ID_923` — standard topic yang sama dengan semua user Meshtastic lainnya.
> Anti-echo loop ditangani secara otomatis oleh relay dedup service.