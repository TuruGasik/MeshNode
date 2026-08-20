# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

MeshNode is a self-hosted Meshtastic backend stack for Indonesia, orchestrated entirely through `docker-compose.yml`. Four services share one canonical MQTT namespace (`msh/ID/#`):

- **`meshnode-mqtt`** — EMQX 6.2.0 broker (stock image + mounted config). Central hub; all other services connect to it.
- **`meshmap`** — Go backend `meshobserv` (MQTT → SQLite + in-memory NodeDB, JSON API on `:8080`) fronted by nginx serving a Leaflet map. nginx here is also a multi-tenant reverse proxy for several unrelated `*.dari.asia` / `*.meshnode.id` domains.
- **`autonotif`** — Go service that polls BMKG/INATEWS2 earthquake feeds and publishes alerts to a Meshtastic channel, plus a DM command responder. Handles Meshtastic protobuf encoding + channel/PKI crypto itself.
- **`mqtt-relay`** — Go dedup relay bridging the local broker to two upstreams (`mqtt.meshnode.id` with `mqtt1..mqtt4.meshnode.id` fallbacks, and `mqtt.meshtastic.org`). **Behind the `phase2-relay` compose profile — dormant unless explicitly started.**

## Common commands

```bash
# Build all buildable services (autonotif, meshmap, mqtt-relay)
docker compose build

# Run the core stack (EMQX + meshmap + autonotif; relay stays off)
docker compose up -d

# Start the relay (Phase 2) — requires the profile flag every time
docker compose --profile phase2-relay up -d mqtt-relay

# Validate compose + env interpolation without running anything
docker compose config
docker compose --profile phase2-relay config

# Logs / status / health
docker compose ps
docker compose logs --tail=120 <service>
curl -s http://127.0.0.1:8080/api/nodes/stats          # meshmap API
curl -s http://127.0.0.1:8081/health                   # relay (phase2 only)
```

Go tests live in `autonotif/` and `mqtt-relay/` (there are none for meshmap):

```bash
cd mqtt-relay && go test ./...           # dedup, topic parsing (incl. a race test)
cd autonotif && go test ./...            # db, meshtastic codec/pki, hantavirus, util
go test -run TestName ./internal/...     # single test
```

meshmap frontend/backend local dev (needs Go + Node):

```bash
cd meshmap
npm install
npm run dev:full     # concurrently runs meshobserv (dev:api) + Vite (dev, :5178)
```

Note: `meshobserv` depends on Meshtastic protobuf Go code that the Docker build generates from `github.com/meshtastic/protobufs` (see `meshmap/Dockerfile`); a clean local `go run` needs those generated files present. The Docker build is the canonical build path.

## Configuration model — read this before touching config

**All configuration is centralized in a single gitignored `.env`** (`.env.example` is the committed template). It is organized into numbered, per-service sections with **per-service prefixes**: `MESHMAP_*`, `AUTONOTIF_*`, `EMQX_*`, `RELAY_*`, plus a monitoring section.

Key mechanics:

- **The Go code does NOT read the prefixed names.** `docker-compose.yml` maps each prefixed `.env` var to the internal env name the service actually reads (e.g. `LOG_LEVEL=${AUTONOTIF_LOG_LEVEL}`, `MQTT_HOST=${AUTONOTIF_MQTT_HOST}`). When adding config, add the value to `.env`/`.env.example` under the right section **and** wire the mapping in the compose `environment:` block. Do not rename env vars inside Go source.
- **Credentials have a single source of truth in the EMQX section.** MQTT passwords are defined once as `EMQX_USER_*_PASS` and referenced from multiple services in compose (e.g. meshmap's `MQTT_PASSWORD=${EMQX_USER_MESHMAP_PASS}`, relay's `LOCAL_MQTT_PASSWORD=${EMQX_USER_RELAY_PASS}`). `.env` values cannot reference each other — cross-references only work inside `docker-compose.yml`.
- **EMQX users are generated at container start, not stored in a file.** `emqx/gen-bootstrap-users.sh` (run via a compose `command:` override; the stock EMQX entrypoint still runs and `exec`s it) writes the bootstrap CSV to `/tmp/emqx-bootstrap-users.csv` from `BOOT_USER_*`/`BOOT_PASS_*` env. The old `emqx/users.conf` is gone. Generator inputs are named `BOOT_*` on purpose — anything named `EMQX_*` gets mapped onto EMQX HOCON config keys.
- **The EMQX bootstrap file only ADDS users absent from the built-in DB (mnesia in the `emqx-data` volume). It never updates existing passwords.** To rotate a live password: edit `.env`, restart the dependent service, and update the broker user via the dashboard (`https://127.0.0.1:18084`) or REST API — or wipe the `emqx-data` volume to force a full re-bootstrap (loses retained messages, sessions, and dashboard users).
- **`emqx/acl.conf` references usernames literally.** If you rename an MQTT username in `.env`, update the matching ACL rule too.

## Architecture details that span files

- **EMQX config comes from three places at once:** compose `environment:` (listeners, auth/authz backend, dashboard, node cookie/license/log level), mounted files (`emqx/acl.conf`, `emqx/base.hocon`), and dashboard-persisted state in the `emqx-data` volume. `emqx/base.hocon` also holds **disabled** (`enable = false`) native MQTT bridges kept only as a migration reference — live upstream bridging is done by `mqtt-relay`, not by EMQX bridges. Credentials in `base.hocon` are redacted (`REDACTED`); the real ones live in `.env`.
- **meshobserv is supervised by a retry loop.** It calls `log.Fatal()` on MQTT connect timeout, so `meshmap/entrypoint.sh` wraps it in a `while true` loop and runs nginx in the foreground. Don't assume a single long-lived process.
- **Relay dedup model:** each message is fingerprinted `SHA256(topic + payload)` and cached with TTL (`RELAY_DEDUP_TTL`, default 600s). New-from-local → forward to both upstreams; new-from-upstream → forward to local; already-seen within TTL → dropped as echo. This is why all connections subscribe to the same `msh/ID/#` root rather than using bridge-specific namespaces. Runtime stats (`received`, `from_local`, `from_up_a/b`, `relayed_in/out`, `dropped`, `cache_size`) are exposed at `:8081/health` and `/metrics`.
- **Monitoring scripts** (`scripts/monitor_mqtt.py`, `scripts/monitor_mqtt_relay.py`) run on the host and parse `.env` directly. They prefer the new prefixed names (`RELAY_UPSTREAM_A_HOST`, `EMQX_USER_RELAY_PASS`, …) and fall back to legacy unprefixed names. `scripts/monitor_gempabumi.py` is untracked/gitignored.

## Gitignore notes

`.env`, `certs/`, `*.pem`/`*.key`, `emqx/*` (except `acl.conf`, `base.hocon`, `gen-bootstrap-users.sh`), `scripts/*` (except the two tracked monitors), build artifacts, and `autonotif/data/` (runtime PKI keys, node keys, state, db) are all ignored. TLS certs are deployed via `./deploy_certs.sh` (copies Let's Encrypt certs to `certs/letsencrypt/`, chowns to UID 1000, restarts the broker).
