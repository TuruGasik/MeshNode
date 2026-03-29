# MeshNode
MeshNode Indonesia MQTT and Map

A Mosquitto MQTT broker for Indonesian Meshtastic users with bridges to the Indonesian community server and the global Meshtastic MQTT server.

## Bridges

| Connection | Server | Username |
|---|---|---|
| Indonesian Community | `mqtt.s-project.web.id` | `idmeshnode` |
| Global Meshtastic | `mqtt.meshtastic.org` | `meshdev` |

All `msh/ID_923/#` topics (the default Indonesian Meshtastic channel) are bridged bidirectionally between this local broker and both remote servers.

## Requirements

- [Docker](https://docs.docker.com/get-docker/)
- [Docker Compose](https://docs.docker.com/compose/install/)

## Quick Start

```bash
# Clone the repository
git clone https://github.com/TuruGasik/MeshNode.git
cd MeshNode

# Start the broker
docker compose up -d

# Check logs
docker compose logs -f
```

The broker listens on port **1883** and allows anonymous connections from local clients.

## Configuration

The Mosquitto configuration is located at `mosquitto/config/mosquitto.conf`.  
Persistent data and logs are stored in Docker named volumes (`mosquitto-data`, `mosquitto-log`).

## Stopping

```bash
docker compose down
```
