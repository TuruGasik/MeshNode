# MeshNode
MeshNode Indonesia MQTT and Map

A Mosquitto MQTT broker for Indonesian Meshtastic users with bridges to the Indonesian community server and the global Meshtastic MQTT server, plus a self-hosted [MeshMap](https://github.com/brianshea2/meshmap.net) that shows nodes on an interactive map.

## Bridges

| Connection | Server | Username |
|---|---|---|
| Indonesian Community | `mqtt.s-project.web.id` | `idmeshnode` |
| Global Meshtastic | `mqtt.meshtastic.org` | `meshdev` |

All `msh/ID_923/#` topics (the default Indonesian Meshtastic channel) are bridged bidirectionally between this local broker and both remote servers.

## Health Monitoring

[Uptime Kuma](https://github.com/louislam/uptime-kuma) is included as a self-hosted health monitor.  
It runs on port **3001** and lets you track the broker and both bridge connections from a web dashboard.

### Recommended monitors

After opening the dashboard (`http://<your-host>:3001`) and creating an admin account, add the following monitors:

| Name | Type | Host / URL | Port / Topic |
|---|---|---|---|
| Local MQTT Broker | TCP Port | `meshnode-mqtt` (or `localhost`) | `1883` |
| Bridge – Indonesian Community | TCP Port | `mqtt.s-project.web.id` | `1883` |
| Bridge – Global Meshtastic | TCP Port | `mqtt.meshtastic.org` | `1883` |
| MeshMap | HTTP(s) | `http://meshmap` (or `http://localhost:8080`) | — |

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

Open `http://<your-host>:8080` to view the node map.  
The map refreshes automatically every 65 seconds (set by the upstream meshmap.net frontend) and shows all position-reporting nodes heard via MQTT.

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `MQTT_BROKER` | `tcp://meshnode-mqtt:1883` | MQTT broker URL |
| `MQTT_USERNAME` | `meshmap` | MQTT username |
| `MQTT_PASSWORD` | `meshmap` | MQTT password |

## Requirements

- [Docker](https://docs.docker.com/get-docker/)
- [Docker Compose](https://docs.docker.com/compose/install/)

## Quick Start

```bash
# Clone the repository
git clone https://github.com/TuruGasik/MeshNode.git
cd MeshNode

# Start the broker and monitoring dashboard
docker compose up -d

# Check logs
docker compose logs -f
```

- MQTT broker → port **1883**
- MQTT broker (TLS) → port **8883**
- MeshMap (node map) → port **8080** (`http://<your-host>:8080`)
- Uptime Kuma dashboard → port **3001** (`http://<your-host>:3001`)

## Configuration

The Mosquitto configuration is located at `mosquitto/config/mosquitto.conf`.  
Persistent data and logs are stored in Docker named volumes (`mosquitto-data`, `mosquitto-log`, `uptime-kuma-data`, `meshmap-data`).

### Local secrets / files to keep out of git

- `mosquitto/passwd`
- `mosquitto/certs/`
- `deploy_certs.sh` (local environment script with domain/path specific values)

### Optional helper scripts

- `scripts/monitor_mosquitto_tls.sh` → monitors Docker logs and prints TLS (`8883`) client connection events.

## Stopping

```bash
docker compose down
```
