# MeshNode

MeshNode Indonesia stack berbasis **EMQX + MeshMap** untuk jaringan Meshtastic.

## Status Arsitektur Saat Ini

### Phase 1 (aktif)
- ✅ EMQX broker (`meshnode-mqtt`)
- ✅ ACL file-based
- ✅ TLS MQTT (`8883`)
- ✅ EMQX Dashboard HTTP (`18083`) + HTTPS (`18084`)
- ✅ MeshMap (`meshmap`)

### Phase 2 (aktif, opsional via profile)
- ✅ `mqtt-relay` deduplication (profile `phase2-relay`)
- ✅ Upstream integration via relay multi-broker
- ✅ EMQX bridge config tetap ada tapi disabled

## Topik MQTT

- Namespace utama: `msh/ID/#`

Saat relay aktif, topik `msh/ID/#` dipakai di:
- broker lokal `meshnode-mqtt`
- upstream `mqtt.meshnode.id`
- upstream `mqtt.meshtastic.org`

## Struktur Folder Penting

- `docker-compose.yml` → stack aktif
- `emqx/acl.conf` → ACL runtime EMQX
- `emqx/users.conf` → bootstrap user EMQX (CSV)
- `emqx/certs/` → TLS cert/key (`fullchain.pem`, `privkey.pem`)
- `meshmap/` → source + Docker build MeshMap
- `mqtt-relay/` → source relay multi-broker + dedup
- `migrasi/` → dokumen migration plan/history

## Requirement

- Docker
- Docker Compose

## Quick Start

```bash
git clone https://github.com/TuruGasik/MeshNode.git
cd MeshNode

# 1) Siapkan environment
cp .env.example .env

# 2) Pastikan user bootstrap EMQX benar
# file: emqx/users.conf
# format:
# user_id,password,is_superuser
# idmeshnode,<password>,false
# meshmap,<password>,false

# 3) Jalankan stack inti
docker compose up -d

# 4) Aktifkan relay jika ingin integrasi upstream
docker compose --profile phase2-relay up -d mqtt-relay

# 5) Cek status
docker compose ps
```

## Akses Service

- MQTT: `1883`
- MQTT TLS: `8883`
- EMQX Dashboard HTTP: `http://<host>:18083`
- EMQX Dashboard HTTPS: `https://<host>:18084`
- MeshMap: `https://<domain>` (ports `80/443`)

## Catatan Dashboard EMQX

- User default dashboard: `admin`
- Password admin harus diganti setelah deploy awal.
- Reset password admin via CLI container:

```bash
docker exec meshnode-mqtt emqx ctl admins passwd admin '<new-password>'
```

## TLS Certificate Deployment

Script helper:

```bash
./deploy_certs.sh
```

Script ini akan:
- copy cert LetsEncrypt ke `emqx/certs/`
- set permission yang cocok untuk user `emqx` di container
- restart `meshnode-mqtt` dan `meshmap`

## Konfigurasi ACL

File: `emqx/acl.conf`

Contoh rule aktif saat ini:
- `idmeshnode`: full access ke `msh/ID/#`
- `meshmap`: subscribe only ke `msh/ID/#`
- `mqtt-relay`: subscribe/publish ke `msh/ID/#`
- default: deny

Setelah ubah ACL, apply dengan:

```bash
docker compose restart meshnode-mqtt
```

## Monitoring Scripts

- `scripts/monitor_mqtt.py` → monitor multi-broker
- `scripts/monitor_mqtt_relay.py` → monitor pipeline relay/dedup (Phase 2)

Jalankan dengan env dari `.env`:

```bash
source .env
python3 scripts/monitor_mqtt.py
```

## Phase 2 (Relay)

`mqtt-relay` menangani koneksi ke dua upstream dan dedup traffic inbound. Aktifkan saat siap:

```bash
docker compose --profile phase2-relay up -d mqtt-relay
```

Health check relay:

```bash
docker exec mqtt-relay wget -qO- http://127.0.0.1:8081/health
```

Flow saat relay aktif:
- local `meshnode-mqtt` → relay → 2 upstream
- upstream A/B → relay → local `meshnode-mqtt`
- duplicate inbound dibuang oleh dedup cache

### Detail Arsitektur `mqtt-relay`

Relay dibuat sebagai service Go terpisah agar kontrol flow lebih jelas dibanding mengandalkan bridge logic broker.

```mermaid
flowchart LR
	A[mqtt.meshnode.id<br/>Upstream A] --> R[mqtt-relay<br/>dedup + routing]
	B[mqtt.meshtastic.org<br/>Upstream B] --> R
	R --> L[EMQX local broker<br/>meshnode-mqtt]
	L --> R
	R --> A
	R --> B
	L --> M[MeshMap / local clients]

	D[(Dedup cache<br/>SHA256(topic + payload)<br/>TTL 600s)] --- R
	H[/Health endpoint<br/>:8081/health/] --- R
```

Komponen utamanya:
- **1 koneksi broker lokal**: `meshnode-mqtt`
- **2 koneksi upstream**:
	- `mqtt.meshnode.id`
	- `mqtt.meshtastic.org`
- **1 dedup cache in-memory** dengan TTL
- **1 health endpoint** di `:8081/health`

Semua koneksi subscribe ke root topic yang sama:
- `msh/ID/#`

Jadi relay bekerja di atas **namespace kanonik yang sama** untuk local dan semua upstream, bukan dengan namespace bridge buatan.

### Cara Kerja Dedup

Untuk setiap pesan, relay membuat fingerprint:

- `SHA256(topic + payload)`

Hash ini disimpan di cache beserta metadata:
- waktu pertama terlihat
- sumber pesan (`local`, `upstream_a`, `upstream_b`)

Aturan sederhananya:
- jika pesan **baru dari local** → teruskan ke kedua upstream
- jika pesan **baru dari upstream** → teruskan ke local broker
- jika hash **sudah pernah ada dalam window TTL** → anggap duplicate/echo dan drop

TTL default saat ini:
- `DEDUP_TTL=600` detik

Cleanup cache berjalan periodik dengan:
- `CLEANUP_INTERVAL=60` detik

### Kenapa Model Ini Dipakai

Keuntungan pendekatan ini:
- **anti-loop lebih mudah dipahami**
- **duplicate dari 2 upstream bisa dibuang**
- **EMQX tetap sederhana** sebagai broker lokal
- **debugging lebih gampang** lewat stats dan health endpoint
- **routing bisa diubah di code**, tidak perlu memaksa rule/bridge config broker

### Statistik Runtime Relay

Relay expose statistik internal yang berguna untuk observability:

- `received` → total pesan yang diterima relay
- `from_local` → pesan yang datang dari broker lokal
- `from_up_a` → pesan dari upstream A
- `from_up_b` → pesan dari upstream B
- `relayed_in` → pesan upstream yang diteruskan ke local
- `relayed_out` → pesan local yang diteruskan ke upstream
- `dropped` → duplicate/echo yang dibuang
- `cache_size` → jumlah hash aktif di dedup cache

Interpretasi cepat:
- `relayed_in` naik → upstream ke local berjalan
- `relayed_out` naik → local ke upstream berjalan
- `dropped` naik → dedup aktif bekerja

### Health Check dan Monitoring

Cek health JSON:

```bash
docker exec mqtt-relay wget -qO- http://127.0.0.1:8081/health
```

Lihat log relay:

```bash
docker compose logs --tail=120 mqtt-relay
```

Monitor pipeline relay secara live:

```bash
source .env
python3 scripts/monitor_mqtt_relay.py
```

### Status Validasi Saat Ini

Yang sudah tervalidasi:
- relay connect ke broker lokal
- relay connect ke 2 upstream
- publish lokal berhasil diteruskan ke 2 upstream
- stats `relayed_in`, `relayed_out`, dan `dropped` bergerak konsisten
- health endpoint status `healthy`

Artinya runtime saat ini sudah memenuhi model:

- **upstream → local**
- **local → upstream**
- **duplicate inbound dibuang oleh dedup cache**

## Stop Stack

```bash
docker compose down
```

## Konfigurasi Meshtastic Client

| Parameter | Nilai |
|---|---|
| Address | `mqtt://<host>:1883` |
| Username | `idmeshnode` |
| Password | sesuai `emqx/users.conf` / DB EMQX |
| Root topic | `msh/ID` |
