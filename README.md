# MeshNode
MeshNode Indonesia MQTT and Map

A Mosquitto MQTT broker for Indonesian Meshtastic users with bridges to the Indonesian community server and the global Meshtastic MQTT server.

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

> **Tip – MQTT monitor type:** Uptime Kuma also has a native **MQTT** monitor.  
> Use it to verify that messages are actually flowing through a bridge:  
> set the broker URL to `mqtt://localhost:1883`, subscribe to `msh/ID_923/#`,  
> and configure the expected keyword/value your nodes publish.

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
- Uptime Kuma dashboard → port **3001** (`http://<your-host>:3001`)

## Configuration

The Mosquitto configuration is located at `mosquitto/config/mosquitto.conf`.  
Persistent data and logs are stored in Docker named volumes (`mosquitto-data`, `mosquitto-log`, `uptime-kuma-data`).

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
