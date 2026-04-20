# 🔄 Migration Plan: Mosquitto → EMQX Open Source
## MeshNode Indonesia - Detailed Migration Strategy

**Document Version**: 1.3  
**Date**: April 20, 2026  
**Status**: Implemented (Phase 1 completed, Phase 2 relay active)  
**Estimated Effort**: 2-3 days  

---

## 📋 Table of Contents

1. [Executive Summary](#executive-summary)
2. [Current Runtime Architecture](#current-runtime-architecture)
3. [Target Architecture](#target-architecture)
4. [Migration Phases](#migration-phases)
5. [Detailed Steps](#detailed-steps)
6. [Risk Assessment](#risk-assessment)
7. [Rollback Plan](#rollback-plan)
8. [Testing Strategy](#testing-strategy)
9. [Post-Migration Checklist](#post-migration-checklist)
10. [Appendix](#appendix)

---

## 📊 Executive Summary

> **Runtime Revision (April 20, 2026):**
> Bridge EMQX tetap disimpan di config tetapi dalam keadaan **disabled**.
> Integrasi upstream aktif memakai **Go `mqtt-relay` multi-broker**.
> EMQX sekarang berperan sebagai **broker lokal murni**.

### Objective
Migrate from Mosquitto MQTT Broker to EMQX Open Source while maintaining:
- ✅ Core MQTT broker functionality
- ✅ ACL-based access control
- ✅ TLS/SSL encryption
- ✅ Zero downtime for MeshMap web UI

Phase 2 (active now):
- ✅ Go `mqtt-relay` connected to local EMQX + 2 upstream brokers
- ✅ Deduplication active for upstream-to-local flow
- ✅ Local publish forwarded to both upstream brokers

### Current Implementation Snapshot (Apr 20, 2026)
- Active compose: `docker-compose.yml` (root)
- Active config path: `emqx/`
  - `emqx/acl.conf`
  - `emqx/users.conf`
  - `emqx/certs/`
- EMQX image: `emqx/emqx:5.10.3`
- Dashboard:
  - HTTP `18083`
  - HTTPS `18084` (enabled)
- Relay: `mqtt-relay` via profile `phase2-relay` (active and tested)
- Bridge config file: `emqx/base.hocon` kept for reference, all bridge entries disabled
- Runtime relay config loaded from `.env`
- Relay health endpoint: `http://mqtt-relay:8081/health`
- Smoke test status: local publish to `msh/ID/#` successfully forwarded to both upstream brokers

---

## 🏗️ Current Runtime Architecture

```
┌─────────────────────────────────────────────────────────┐
│                      EMQX Broker                         │
│  ┌──────────────┐                                       │
│  │  ACL (file)  │                                       │
│  │  - idmeshnode│                                       │
│  │  - meshmap   │                                       │
│  └──────────────┘                                       │
│  ┌──────────────────────────────────────────────────┐   │
│  │  MQTT 1883 + MQTTS 8883                          │   │
│  └──────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────┐   │
│  │  Dashboard HTTP 18083 / HTTPS 18084             │   │
│  └──────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
         │                                       │
         ▼                                       ▼
┌──────────────┐                      ┌──────────────────┐
│  Meshtastic  │                      │  MeshMap Web UI  │
│  Nodes       │                      │  (subscriber)    │
│  (idmeshnode)│                      │                  │
└──────────────┘                      └──────────────────┘

Phase 2 (current runtime):
- Go relay subscribes to local `msh/ID/#`
- Go relay subscribes to upstream A `mqtt.meshnode.id`
- Go relay subscribes to upstream B `mqtt.meshtastic.org`
- Upstream messages are deduplicated before publish to local EMQX
- Local messages are forwarded to both upstream brokers
- Runtime counters expose `received`, `from_local`, `from_up_a`, `from_up_b`, `relayed_in`, `relayed_out`, `dropped`, `cache_size`
```

---

## 🎯 Target Architecture

### Phase 1 (implemented)
- EMQX core broker + ACL + TLS
- MeshMap stable on top of `msh/ID/#`

### Phase 2 (next)
- Keep EMQX bridges disabled
- Run relay as the only upstream integration layer
- Continue soak and duplicate-behavior validation

---

## 🚀 Migration Phases

### Phase 1: Preparation
**Status**: ✅ Completed

Key outputs:
- Runtime moved to EMQX stack
- ACL + users bootstrap validated
- TLS listener and dashboard enabled
- Legacy Mosquitto runtime removed

### Phase 2: Staging Validation
**Status**: ✅ Completed (practical validation on host)

Key outputs:
- Service startup/health checks passed
- ACL behavior verified
- MeshMap API and MQTT integration verified

### Phase 3: Load / Stability Checks
**Status**: ✅ Partially completed (smoke + relay functional checks)

Remaining optional:
- Sustained throughput tests
- Soak testing window with monitoring
- Controlled duplicate injection from both upstreams

### Phase 4: Production Cutover
**Status**: ✅ Completed

### Phase 5: Post-Migration Validation
**Status**: ✅ Completed for Phase 1 baseline

---

## 🛠️ Detailed Steps (Updated to Current Paths)

### 1) Backup
```bash
cp -r /root/MeshNode /root/backupmeshnode
```

### 2) Validate active config files
```bash
cat emqx/acl.conf
cat emqx/users.conf
```

### 3) Validate compose
```bash
docker compose config >/tmp/compose_check.out && echo "compose ok"
```

### 4) Start / restart services
```bash
docker compose up -d meshnode-mqtt meshmap
docker compose --profile phase2-relay up -d mqtt-relay
docker compose ps
```

### 5) Connectivity checks
```bash
# MQTT test
mosquitto_pub -h localhost -p 1883 -u idmeshnode -P '<password>' -t 'msh/ID/test' -m ok

# TLS test
mosquitto_pub -h localhost -p 8883 --cafile ./emqx/certs/fullchain.pem \
  -u idmeshnode -P '<password>' -t 'msh/ID/test' -m ok

# MeshMap API
curl -k -H 'Host: <your-domain>' https://127.0.0.1/api/nodes/stats

# Relay health
docker exec mqtt-relay wget -qO- http://127.0.0.1:8081/health

# Relay logs
docker compose logs --tail=120 mqtt-relay
```

### 5b) Relay smoke test (validated)
```bash
echo 'BEFORE:'
docker exec mqtt-relay wget -qO- http://127.0.0.1:8081/health

docker run --rm --network meshnode_default eclipse-mosquitto:2 \
  mosquitto_pub -h meshnode-mqtt -p 1883 \
  -u idmeshnode -P '<password>' \
  -t msh/ID/copilot-smoke -m 'smoke-test'

echo 'AFTER:'
docker exec mqtt-relay wget -qO- http://127.0.0.1:8081/health
```

Expected result:
- `from_local` bertambah `+1`
- `relayed_out` bertambah `+2`
- `cache_size` bertambah `+1`
- `dropped` dapat ikut naik karena echo/duplicate dari upstream

### 6) Dashboard checks
```bash
# HTTP login endpoint
curl -sS -H 'Content-Type: application/json' \
  -X POST http://127.0.0.1:18083/api/v5/login \
  -d '{"username":"admin","password":"<admin-pass>"}'

# HTTPS login endpoint
curl -k -sS -H 'Content-Type: application/json' \
  -X POST https://127.0.0.1:18084/api/v5/login \
  -d '{"username":"admin","password":"<admin-pass>"}'
```

---

## ⚠️ Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Relay upstream connection failures | 🟡 Medium | 🔴 High | Auto reconnect + health endpoint + logs |
| ACL misconfiguration | 🟢 Low | 🟡 Medium | File ACL tested + broker logs checked |
| TLS key permission issues | 🟡 Medium | 🔴 High | Ensure key readable by container user |
| Data loss during migration | 🟢 Low | 🔴 High | Full backup before cutover |

---

## 🔄 Rollback Plan (Current)

```bash
cd /root/MeshNode

# stop current stack
docker compose down

# restore from backup if needed
# (example)
# rsync -a --delete /root/backupmeshnode/ /root/MeshNode/

# bring up restored stack
cd /root/MeshNode
docker compose up -d
```

---

## 🧪 Testing Strategy

### Phase 1 baseline (done)
- [x] ACL rules validated
- [x] MQTT/TLS connectivity validated
- [x] MeshMap API healthy
- [x] Dashboard HTTP/HTTPS reachable

### Phase 2 tests (pending)
- [x] Enable relay profile
- [x] Local → upstream smoke test
- [x] Relay health/status validation
- [x] Runtime stats validation (`relayed_in`, `relayed_out`, `dropped`)
- [ ] Repeated inbound dedup test with identical payload from both upstreams
- [ ] Longer soak monitoring window

---

## ✅ Post-Migration Checklist

### Immediate
- [x] All services running
- [x] MQTT connectivity verified
- [x] TLS connectivity verified
- [x] MeshMap UI/API showing data

### Short-term
- [x] No critical startup errors
- [x] Healthchecks passing
- [ ] Optional soak monitoring 24h+

### Phase 2 (Deferred)
- [x] Keep bridge configuration disabled
- [x] Enable relay service (`phase2-relay`)
- [x] Validate relay stats and local-to-upstream forwarding
- [ ] Validate strict inbound dedup with controlled duplicate payloads

### Observed Smoke-Test Result (Apr 20, 2026)

Observed counters during local publish tests:

- `from_local` naik konsisten
- `relayed_out` naik `+2` per publish lokal
- `dropped` ikut naik karena echo / duplicate handling
- relay status `healthy`

Interpretasi:
- flow **local → upstream** tervalidasi
- flow **upstream → local** aktif dari traffic real upstream
- dedup aktif dan membuang pesan balik yang tidak perlu

---

## 📎 Appendix

### A. Configuration Mapping (Updated)

| Legacy (Mosquitto era) | Current EMQX runtime | Notes |
|------------------------|----------------------|-------|
| `mosquitto/acl` | `emqx/acl.conf` | Active ACL file |
| `migrasi/users.conf` | `emqx/users.conf` | CSV bootstrap file |
| `mosquitto/certs/` | `emqx/certs/` | Active cert path (folder renamed) |
| `migrasi/*.conf` | (docs only) | Runtime files moved |
| Bridge integration | `mqtt-relay/` | Active upstream integration path |

### B. Port Mapping

| Service | Port | Notes |
|---------|------|-------|
| MQTT | 1883 | Plain |
| MQTT over TLS | 8883 | TLS |
| Dashboard HTTP | 18083 | Active |
| Dashboard HTTPS | 18084 | Active |

### C. Useful EMQX Commands

```bash
# status
docker exec meshnode-mqtt emqx ctl status

# list clients
docker exec meshnode-mqtt emqx ctl clients list

# reset dashboard admin password
docker exec meshnode-mqtt emqx ctl admins passwd admin '<new-password>'
```

### D. Useful Relay Commands

```bash
# relay logs
docker compose logs --tail=120 mqtt-relay

# relay health
docker exec mqtt-relay wget -qO- http://127.0.0.1:8081/health

# start relay
docker compose --profile phase2-relay up -d mqtt-relay

# stop relay
docker compose stop mqtt-relay
```

---

## 📞 Support

- EMQX docs: https://www.emqx.io/docs/
- EMQX GitHub: https://github.com/emqx/emqx

---

**End of Migration Plan**
