# 🔄 MeshNode Indonesia - Migrasi Mosquitto ke EMQX Open Source

Folder ini berisi dokumen migrasi dan catatan history.

## Status Saat Ini

- ✅ Cutover ke EMQX sudah selesai
- ✅ Stack aktif berjalan via `docker-compose.yml` di root project
- ✅ Phase 2 relay aktif dan sudah smoke-tested
- ✅ Bridge EMQX tetap disimpan tapi disabled

## 📁 Struktur Folder `migrasi/`

```
migrasi/
├── README.md
└── MIGRATION_PLAN.md
```

## 📦 Lokasi Runtime Config Aktif

File runtime yang dipakai stack sekarang ada di folder `emqx/`:

- `emqx/acl.conf`
- `emqx/users.conf`
- `emqx/certs/`

Catatan:
- File PoC lama (contoh: `migrasi/emqx.conf`, `migrasi/docker-compose-emqx.yml`) sudah dihapus karena tidak dipakai lagi.

## Quick Check

```bash
cd /root/MeshNode

docker compose ps
docker compose config >/tmp/compose_check.out && echo "compose ok"
docker exec mqtt-relay wget -qO- http://127.0.0.1:8081/health
```

## Testing Dasar

```bash
# Test MQTT plain
mosquitto_pub -h localhost -p 1883 -u idmeshnode -P <password> -t msh/ID/test -m ok

# Test MQTT TLS
mosquitto_pub -h localhost -p 8883 --cafile ./emqx/certs/fullchain.pem \
  -u idmeshnode -P <password> -t msh/ID/test -m ok

# Start relay profile (jika belum aktif)
docker compose --profile phase2-relay up -d mqtt-relay
```

## Runtime Relay Saat Ini

- Broker lokal: `meshnode-mqtt`
- Upstream A: `mqtt.meshnode.id`
- Upstream B: `mqtt.meshtastic.org`
- Topik aktif: `msh/ID/#`
- Dedup: aktif di `mqtt-relay`
- Health endpoint: `:8081/health`
- Smoke test local publish: ✅ berhasil forward ke dua upstream

Bridge di `emqx/base.hocon` tetap ada untuk referensi, tetapi semua entry bridge dalam keadaan nonaktif.

## Ringkasan Validasi Saat Ini

- `mqtt-relay` connect ke local broker: ✅
- `mqtt-relay` connect ke upstream A: ✅
- `mqtt-relay` connect ke upstream B: ✅
- local publish diteruskan ke dua upstream: ✅
- duplicate/echo ditangani dedup cache: ✅
- health status relay: `healthy`

## Dashboard EMQX

- HTTP: `http://<host>:18083`
- HTTPS: `https://<host>:18084`

Jika perlu reset password admin dashboard:

```bash
docker exec meshnode-mqtt emqx ctl admins passwd admin '<new-password>'
```

## Referensi

- Migration detail: [`MIGRATION_PLAN.md`](./MIGRATION_PLAN.md)
