# MeshNode MQTT Relay — Deduplication Service

**Bahasa:** Go 1.24 · **Broker Library:** Eclipse Paho MQTT · **Protobuf Parsing:** `google.golang.org/protobuf/encoding/protowire`

MQTT Relay adalah service yang menjembatani broker MQTT lokal (EMQX) dengan dua broker upstream, sekaligus melakukan **deduplikasi pesan** agar paket Meshtastic yang sama tidak berputar-putar (loop) di antara broker-broker tersebut.

---

## Daftar Isi

- [Arsitektur](#arsitektur)
- [Topologi Jaringan](#topologi-jaringan)
- [Alur Pesan (Message Flow)](#alur-pesan-message-flow)
  - [Outbound: Local → Upstream](#1-outbound-local--upstream)
  - [Inbound: Upstream → Local](#2-inbound-upstream--local)
  - [Echo Suppression](#3-echo-suppression)
- [Logika Deduplikasi](#logika-deduplikasi)
  - [Canonical Hashing](#canonical-hashing)
  - [DedupStore](#dedupstore)
  - [Routing Decision (targetsFor)](#routing-decision-targetsfor)
- [Skenario & Edge Cases](#skenario--edge-cases)
- [Struktur File](#struktur-file)
- [Konfigurasi (Environment Variables)](#konfigurasi-environment-variables)
- [Health Check API](#health-check-api)
- [Statistik & Monitoring](#statistik--monitoring)
- [Build & Deployment](#build--deployment)
- [Testing](#testing)

---

## Arsitektur

```
┌──────────────────────────────────────────────────────────────────────┐
│                         MQTT Relay (Go)                             │
│                                                                      │
│  ┌────────────┐    ┌─────────────┐    ┌─────────────────────────┐   │
│  │   Relay     │◄──►│ DedupStore  │    │  HealthState + Stats    │   │
│  │ (relay.go)  │    │ (dedup.go)  │    │  (main.go)              │   │
│  └──┬───┬───┬──┘    └─────────────┘    └────────────┬────────────┘   │
│     │   │   │                                       │                │
│     │   │   │                              HTTP :8081 /health        │
└─────┼───┼───┼───────────────────────────────────────┼────────────────┘
      │   │   │                                       │
      ▼   ▼   ▼                                       ▼
   Local  UpA  UpB                              Docker healthcheck
```

**Komponen utama:**

| Komponen | File | Fungsi |
|---|---|---|
| **Config** | `main.go` | Load env vars, setup logging, bootstrap koneksi MQTT |
| **Relay** | `relay.go` | Routing pesan antar broker + statistik |
| **DedupStore** | `dedup.go` | Hash-based dedup dengan TTL + cleanup goroutine |
| **HealthState** | `main.go` | Health tracking + HTTP endpoint `/health` |

---

## Topologi Jaringan

```
                    ┌─────────────────────┐
                    │   Upstream A         │
                    │ mqtt.meshnode.id     │
                    │ (Regional Broker)    │
                    └─────────┬───────────┘
                              │
                              │ subscribe & publish
                              │ topic: msh/ID/#
                              ▼
┌──────────────┐      ┌───────────────┐      ┌─────────────────────┐
│  Meshtastic  │◄────►│  Local EMQX   │◄────►│    MQTT Relay (Go)  │
│   Devices    │      │ meshnode-mqtt  │      │   (this service)    │
└──────────────┘      └───────────────┘      └───────────┬─────────┘
                                                         │
                              │ subscribe & publish      │
                              │ topic: msh/ID/#          │
                              ▼                          │
                    ┌─────────────────────┐              │
                    │   Upstream B         │◄─────────────┘
                    │ mqtt.meshtastic.org  │
                    │ (Global Broker)      │
                    └─────────────────────┘
```

Relay terhubung ke **3 broker sekaligus**:
1. **Local** (`meshnode-mqtt`) — EMQX instance lokal, terhubung ke perangkat Meshtastic
2. **Upstream A** (`mqtt.meshnode.id`) — Broker regional Indonesia
3. **Upstream B** (`mqtt.meshtastic.org`) — Broker global Meshtastic

---

## Alur Pesan (Message Flow)

### 1. Outbound: Local → Upstream

Perangkat Meshtastic mengirim paket ke broker lokal. Relay menangkap pesan ini lalu meneruskan ke kedua upstream.

```
Meshtastic Device
      │
      ▼ publish
┌─────────────┐
│ Local EMQX  │
└──────┬──────┘
       │ subscribe msh/ID/#
       ▼
┌──────────────────────────────────────────┐
│ Relay.HandleLocalMessage()               │
│                                          │
│ 1. stats.Received++, stats.FromLocal++   │
│ 2. TopicMatcher → apakah cocok?          │
│ 3. CanonicalHash(topic, payload)         │
│ 4. dedup.CheckAndStore(hash, "local")    │
│ 5. targetsFor("local", seen)             │
│    ├─ seen.IsNew=true  → relay ke UpA+B │
│    └─ seen.IsNew=false → DROP (echo)     │
│ 6. Publish ke target, stats.RelayedOut++ │
└──────────────┬───────────┬───────────────┘
               │           │
               ▼           ▼
         ┌──────────┐ ┌──────────┐
         │Upstream A│ │Upstream B│
         └──────────┘ └──────────┘
```

### 2. Inbound: Upstream → Local

Node lain di jaringan mengirim pesan melalui upstream. Relay menangkap dan meneruskan ke broker lokal.

```
        Other Meshtastic Node (remote)
                   │
                   ▼ publish
         ┌──────────────────┐
         │ Upstream A or B  │
         └────────┬─────────┘
                  │ subscribe msh/ID/#
                  ▼
┌──────────────────────────────────────────┐
│ Relay.HandleUpstreamA/BMessage()         │
│                                          │
│ 1. stats.Received++, stats.FromUpA/B++   │
│ 2. TopicMatcher → apakah cocok?          │
│ 3. CanonicalHash(topic, payload)         │
│ 4. dedup.CheckAndStore(hash, "up_a/b")   │
│ 5. targetsFor("upstream_a/b", seen)      │
│    ├─ seen.IsNew=true  → relay ke Local  │
│    └─ seen.IsNew=false → DROP (duplikat) │
│ 6. Publish ke Local, stats.RelayedIn++   │
└──────────────────┬───────────────────────┘
                   │
                   ▼
            ┌─────────────┐
            │ Local EMQX  │
            └──────┬──────┘
                   │
                   ▼
          Meshtastic Device (lokal)
```

### 3. Echo Suppression

Ini adalah skenario kritis yang dicegah oleh dedup:

```
                  TANPA DEDUP (loop!)
                  ═══════════════════
Upstream A ──publish──► Local EMQX
                          │
              Relay subscribe menangkap
              pesan ini dari local
                          │
                          ▼
              Relay mengirim kembali
              ke Upstream A & B  ← LOOP! ♻️
                          │
                          ▼
              Upstream A menerima lagi
              → masuk lagi ke local → ∞

                  DENGAN DEDUP ✅
                  ════════════════
Upstream A ──publish──► Relay (hash=abc, source=up_a)
                          │
                    dedup.Store("abc", "up_a")
                          │
                    Relay publish ke Local EMQX
                          │
                    Local EMQX echo kembali ke Relay
                          │
              Relay.HandleLocalMessage()
              hash="abc" → CheckAndStore → seen.IsNew=false
              targetsFor("local", seen{IsNew:false})
                          │
                    return nil → DROP ✅
                    Loop dicegah!
```

---

## Logika Deduplikasi

### Canonical Hashing

Meshtastic menggunakan protobuf `ServiceEnvelope` yang membungkus `MeshPacket`. Masalahnya, broker/bridge terkadang memodifikasi field-field mutable seperti `hop_limit`, `via_mqtt`, atau `transport_mechanism`. Jika kita hash raw bytes, pesan yang **secara logika sama** akan menghasilkan hash berbeda.

**Solusi: Canonical Hash** — hanya hash field yang immutable:

```
ServiceEnvelope (protobuf)
└─ field 1: MeshPacket (bytes)
     ├─ field 1: from      (fixed32) ✅ di-hash
     ├─ field 2: to        (fixed32) ✅ di-hash
     ├─ field 3: channel   (varint)  ✅ di-hash
     ├─ field 4: decoded   (bytes)   ✅ di-hash
     ├─ field 5: encrypted (bytes)   ✅ di-hash
     ├─ field 6: id        (fixed32) ✅ di-hash
     ├─ field 9: hop_limit (varint)  ❌ SKIP (mutable)
     ├─ field 14: via_mqtt (varint)  ❌ SKIP (mutable)
     └─ field 15: ...      (varint)  ❌ SKIP (mutable)
```

**Alur hashing:**

```go
CanonicalHash(topic, payload)
  │
  ├─ serviceEnvelopePacket(payload) → extract MeshPacket bytes
  │     └─ parse protobuf field 1 dari ServiceEnvelope
  │
  ├─ canonicalMeshtasticPacket(packet)
  │     ├─ parse field: from, to, channel, encrypted/decoded, id
  │     ├─ skip semua field lain (hop_limit, via_mqtt, dll)
  │     └─ build canonical bytes: [from|to|id|channel|encrypted|decoded]
  │
  └─ SHA-256(topic + canonical_bytes) → hex string
```

**Fallback:** Jika payload bukan protobuf valid atau field wajib tidak lengkap, fallback ke `Hash(topic, raw_payload)`.

### DedupStore

```go
type DedupStore struct {
    store sync.Map    // key: hash (string), value: DedupEntry
    ttl   time.Duration
}

type DedupEntry struct {
    Timestamp int64   // unix timestamp saat pertama dilihat
    Source    string   // "local", "upstream_a", atau "upstream_b"
}
```

**Operasi `CheckAndStore(hash, source)`:**

```
CheckAndStore("abc123", "local")
      │
      ├─ store.LoadOrStore("abc123", entry)
      │
      ├─ NOT loaded (hash baru)
      │     └─ return SeenResult{IsNew: true}
      │
      ├─ LOADED (hash sudah ada)
      │     ├─ Cek TTL: now - prev.Timestamp >= ttl?
      │     │     ├─ YES (expired) → refresh, return {IsNew: true}
      │     │     └─ NO (masih valid) → return {IsNew: false, PreviousSrc: prev.Source}
```

**Cleanup Loop** berjalan sebagai goroutine, periodik membersihkan entry yang expired:

```
CleanupLoop(interval=60s)
  │
  every 60s:
    store.Range(func(key, value))
      ├─ entry expired? → store.Delete(key), evicted++
      └─ entry valid?   → remaining++
    log "evicted=N, remaining=M"
```

### Routing Decision (targetsFor)

```go
func targetsFor(source, seen) → (targets[], direction)
```

| Source | seen.IsNew | Hasil | Arah |
|---|---|---|---|
| `local` | `true` | → Upstream A + Upstream B | `OUT` |
| `local` | `false` | → **DROP** (echo dari relay IN sebelumnya) | `OUT` |
| `upstream_a` | `true` | → Local | `IN` |
| `upstream_a` | `false` | → **DROP** (duplikat, sudah diterima dari sumber lain) | `IN` |
| `upstream_b` | `true` | → Local | `IN` |
| `upstream_b` | `false` | → **DROP** (duplikat) | `IN` |

**Catatan penting:** Pesan dari upstream **TIDAK pernah** diteruskan ke upstream lainnya. Hanya diteruskan ke local. Ini mencegah relay menjadi bridge antar-upstream.

---

## Skenario & Edge Cases

### Skenario 1: Node Lokal Mengirim Pesan Baru

```
1. Device publish ke Local EMQX → topic "msh/ID/2/e/LongFast/!aabbccdd"
2. Relay.HandleLocalMessage() dipanggil
3. CanonicalHash → "hash_X" (baru, belum pernah dilihat)
4. CheckAndStore("hash_X", "local") → IsNew=true
5. targetsFor("local", {IsNew:true}) → [UpA, UpB]
6. Publish ke Upstream A ✅
7. Publish ke Upstream B ✅
8. stats.RelayedOut += 2
```

### Skenario 2: Pesan dari Upstream A (Node Remote)

```
1. Node remote publish via Upstream A → "msh/ID/2/e/LongFast/!11223344"
2. Relay.HandleUpstreamAMessage() dipanggil
3. CanonicalHash → "hash_Y" (baru)
4. CheckAndStore("hash_Y", "upstream_a") → IsNew=true
5. targetsFor("upstream_a", {IsNew:true}) → [Local]
6. Publish ke Local EMQX ✅
7. stats.RelayedIn += 1
```

### Skenario 3: Echo Suppression (Anti-Loop)

```
Lanjutan dari Skenario 2...
8. Local EMQX menerima pesan → karena Relay subscribe local,
   Relay.HandleLocalMessage() dipanggil lagi
9. CanonicalHash → "hash_Y" (sama!)
10. CheckAndStore("hash_Y", "local") → IsNew=false, PreviousSrc="upstream_a"
11. targetsFor("local", {IsNew:false}) → nil (DROP)
12. stats.Dropped += 1
13. Loop dicegah! ✅
```

### Skenario 4: Pesan Sama dari Upstream A dan B

```
1. Upstream A mengirim pesan hash_Z → Relay menangkap
2. CheckAndStore("hash_Z", "upstream_a") → IsNew=true → relay ke Local
3. Beberapa detik kemudian, Upstream B mengirim pesan yang sama
4. CheckAndStore("hash_Z", "upstream_b") → IsNew=false (sudah ada dari up_a)
5. targetsFor("upstream_b", {IsNew:false}) → nil (DROP)
6. Hanya satu salinan sampai ke local ✅
```

### Skenario 5: TTL Expired — Pesan Lama Muncul Kembali

```
1. hash_Z terakhir dilihat 600+ detik lalu (TTL default)
2. Pesan dengan hash_Z muncul lagi dari Upstream A
3. CheckAndStore → entry ada tapi expired
4. Refresh entry, return IsNew=true
5. Relay ke Local ✅ (dianggap pesan baru karena TTL sudah lewat)
```

### Skenario 6: Hop Metadata Berubah

```
1. Upstream A kirim MeshPacket dengan hop_limit=4, via_mqtt=false
2. CanonicalHash → "hash_W" (hanya hash from/to/id/channel/encrypted)
3. relay ke Local ✅
4. Upstream B kirim paket YANG SAMA tapi hop_limit=3, via_mqtt=true
5. CanonicalHash → "hash_W" (sama! karena canonical skip hop fields)
6. CheckAndStore → IsNew=false → DROP ✅
7. Tanpa canonical hash, ini akan jadi duplikat di local
```

### Skenario 7: Upstream Down — Auto-Degradation

Relay otomatis menangani upstream yang down tanpa intervensi manual:

```
1. Upstream A disconnect / unreachable
2. SetConnectionLostHandler dipanggil → log WARNING (sekali)
3. SetAutoReconnect=true → Paho library otomatis reconnect
4. Reconnect attempts di-log sebagai DEBUG (tidak spam di INFO)
5. Selama disconnect:
   - Pesan dari local tetap dikirim ke Upstream B saja
   - targetsFor() hanya memasukkan broker yang connected()
   - /health endpoint menunjukkan upstream_a.connected=false
6. Saat Upstream A kembali hidup:
   - OnConnectHandler dipanggil otomatis
   - Re-subscribe ke TOPIC_ROOT
   - Pesan kembali di-relay ke kedua upstream
   - Tidak perlu restart relay
```

### Skenario 8: Non-Protobuf Payload

```
1. Pesan datang dengan payload yang bukan protobuf valid
2. canonicalMeshtasticPacket() return (nil, false)
3. Fallback ke Hash(topic, raw_payload) — hash raw bytes
4. Dedup tetap bekerja, tapi tidak bisa mendeteksi
   duplikat yang hanya berbeda di hop metadata
```

### Skenario 9: Upstream Tidak Dikonfigurasi

```
1. UPSTREAM_A_HOST dikosongkan di .env
2. Relay start → skip pembuatan client Upstream A
3. Log: "Upstream A not configured, skipping"
4. Relay hanya bekerja dengan Local ↔ Upstream B
5. /health menunjukkan upstream_a.configured=false
6. Jika ingin re-enable: isi UPSTREAM_A_HOST, restart relay
```

---

## Struktur File

```
mqtt-relay/
├── main.go          # Entry point, config, MQTT client setup, health server
├── relay.go         # Core relay logic, message routing, stats
├── dedup.go         # Deduplication store, canonical hashing, protobuf parsing
├── dedup_test.go    # Unit tests untuk canonical hash
├── topic_test.go    # Unit tests untuk topic matcher (wildcard `+`/`#`)
├── Dockerfile       # Multi-stage build (golang:1.24-alpine → alpine:3.20)
├── go.mod           # Go module definition
├── go.sum           # Dependency checksums
└── mqtt-relay       # Compiled binary (jika sudah di-build)
```

---

## Konfigurasi (Environment Variables)

| Variable | Default | Deskripsi |
|---|---|---|
| `LOCAL_MQTT_HOST` | `meshnode-mqtt` | Hostname broker MQTT lokal |
| `LOCAL_MQTT_PORT` | `1883` | Port broker lokal |
| `LOCAL_MQTT_TLS` | `false` | Aktifkan TLS untuk koneksi lokal |
| `LOCAL_MQTT_TLS_SERVER_NAME` | _(kosong)_ | SNI server name untuk TLS |
| `LOCAL_MQTT_USERNAME` | _(kosong)_ | Username autentikasi broker lokal |
| `LOCAL_MQTT_PASSWORD` | _(kosong)_ | Password autentikasi broker lokal |
| `UPSTREAM_A_HOST` | _(kosong)_ | Hostname broker Upstream A. **Kosongkan untuk disable.** |
| `UPSTREAM_A_PORT` | `1883` | Port broker Upstream A (gunakan `8883` untuk TLS) |
| `UPSTREAM_A_USERNAME` | _(kosong)_ | Username Upstream A |
| `UPSTREAM_A_PASSWORD` | _(kosong)_ | Password Upstream A |
| `UPSTREAM_A_TLS` | `false` | Aktifkan TLS untuk Upstream A |
| `UPSTREAM_A_TLS_SERVER_NAME` | _(kosong)_ | SNI server name Upstream A |
| `UPSTREAM_B_HOST` | _(kosong)_ | Hostname broker Upstream B. **Kosongkan untuk disable.** |
| `UPSTREAM_B_PORT` | `1883` | Port broker Upstream B (gunakan `8883` untuk TLS) |
| `UPSTREAM_B_USERNAME` | _(kosong)_ | Username Upstream B |
| `UPSTREAM_B_PASSWORD` | _(kosong)_ | Password Upstream B |
| `UPSTREAM_B_TLS` | `false` | Aktifkan TLS untuk Upstream B |
| `UPSTREAM_B_TLS_SERVER_NAME` | _(kosong)_ | SNI server name Upstream B |
| `TOPIC_ROOT` | `msh/ID/#` | Topic filter MQTT (mendukung wildcard `+` single-level dan `#` multi-level) |
| `DEDUP_TTL` | `600` | Waktu hidup entry dedup dalam detik (10 menit) |
| `CLEANUP_INTERVAL` | `60` | Interval pembersihan entry expired dalam detik |
| `STATS_INTERVAL` | `60` | Interval log statistik dalam detik |
| `PUBLISH_QOS` | `0` | QoS untuk publish ke broker target (0/1/2) |
| `SUBSCRIBE_QOS` | `0` | QoS untuk subscribe ke setiap broker (0/1/2) |
| `PUBLISH_TIMEOUT_MS` | `5000` | Timeout per-publish dalam milidetik |
| `HEALTH_PORT` | `8081` | Port HTTP server `/health` dan `/metrics` |
| `SHUTDOWN_TIMEOUT_MS` | `5000` | Timeout drain in-flight publish saat shutdown (SIGTERM/SIGINT) |
| `LOG_LEVEL` | `INFO` | Level log: `DEBUG`, `INFO`, `WARN`, `ERROR` |

> **Catatan:**
> - Jika `UPSTREAM_A_HOST` atau `UPSTREAM_B_HOST` dikosongkan, relay skip pembuatan client untuk upstream tersebut.
> - Jika upstream dikonfigurasi tapi initial connect gagal, relay tetap start dan reconnect di background (tidak crash). Hanya kegagalan koneksi ke broker **lokal** yang fatal.
> - Untuk broker public Meshtastic seperti `mqtt.meshtastic.org` yang butuh TLS, set `UPSTREAM_X_TLS=true` dan `UPSTREAM_X_PORT=8883`.

---

## Health Check API

HTTP server berjalan di port **8081** (configurable via `HEALTH_PORT`) dengan dua endpoint:
- `/health` — JSON status untuk liveness/readiness probe
- `/metrics` — Prometheus text exposition format untuk scraping

**Request:**
```bash
curl http://localhost:8081/health
curl http://localhost:8081/metrics
```

**Response (healthy):** HTTP 200
```json
{
  "status": "healthy",
  "reason": "",
  "mqtt_connected": true,
  "uptime_seconds": 3600,
  "last_message_at": "2026-05-13T17:00:00Z",
  "upstreams": {
    "upstream_a": {
      "configured": true,
      "connected": true,
      "host": "mqtt.meshnode.id:1883"
    },
    "upstream_b": {
      "configured": true,
      "connected": true,
      "host": "mqtt.meshtastic.org:1883"
    }
  },
  "stats": {
    "received": 12450,
    "from_local": 5230,
    "from_up_a": 4100,
    "from_up_b": 3120,
    "relayed_in": 4800,
    "relayed_out": 8200,
    "dropped": 4450,
    "cache_size": 342
  }
}
```

**Response (upstream A down — struktur response sama, hanya nilai `connected` yang berubah):**
```json
{
  "status": "healthy",
  "mqtt_connected": true,
  "upstreams": {
    "upstream_a": {
      "configured": true,
      "connected": false,
      "host": "mqtt.meshnode.id:1883"
    },
    "upstream_b": {
      "configured": true,
      "connected": true,
      "host": "mqtt.meshtastic.org:8883"
    }
  }
}
```

> Status `healthy`/`degraded`/`unhealthy` ditentukan **hanya** oleh koneksi broker **lokal** dan apakah ada pesan masuk dalam 10 menit terakhir. Upstream yang down tidak menurunkan status — relay tetap berfungsi dengan upstream yang tersisa.

**Status levels:**

| Status | Kondisi | HTTP Code |
|---|---|---|
| `healthy` | MQTT local connected, pesan aktif | 200 |
| `degraded` | MQTT local connected, tapi tidak ada pesan >10 menit | 503 |
| `unhealthy` | MQTT local disconnected | 503 |

---

## Statistik & Monitoring

Relay mencatat statistik secara periodik (sesuai `STATS_INTERVAL`):

| Metrik | Deskripsi |
|---|---|
| `received` | Total pesan diterima dari semua sumber |
| `from_local` | Pesan yang berasal dari broker lokal |
| `from_up_a` | Pesan yang berasal dari Upstream A |
| `from_up_b` | Pesan yang berasal dari Upstream B |
| `relayed_in` | Pesan yang berhasil di-relay dari upstream ke local |
| `relayed_out` | Pesan yang berhasil di-relay dari local ke upstream |
| `dropped` | Pesan yang di-drop karena duplikat |
| `cache_size` | Jumlah entry aktif di dedup store |

Semua counter menggunakan `atomic.Int64` sehingga **zero lock overhead** dan thread-safe.

### Prometheus Metrics

Endpoint `/metrics` mengekspos counter dalam format Prometheus text exposition (tanpa dependency tambahan, pakai stdlib):

| Metric | Type | Labels | Deskripsi |
|---|---|---|---|
| `mqtt_relay_up` | gauge | — | 1 = local broker connected, 0 = disconnected |
| `mqtt_relay_uptime_seconds` | counter | — | Uptime proses dalam detik |
| `mqtt_relay_last_message_timestamp_seconds` | gauge | — | Unix timestamp pesan terakhir diterima |
| `mqtt_relay_messages_received_total` | counter | `source` | Total pesan per sumber (`local`, `upstream_a`, `upstream_b`) |
| `mqtt_relay_messages_relayed_total` | counter | `direction` | Total pesan yang di-relay (`in`, `out`) |
| `mqtt_relay_messages_dropped_total` | counter | — | Total pesan di-drop (duplikat / loop suppression) |
| `mqtt_relay_dedup_cache_size` | gauge | — | Jumlah entry aktif di dedup store |
| `mqtt_relay_upstream_connected` | gauge | `label`, `host` | Status koneksi per upstream (1/0) |

**Contoh scrape config Prometheus:**
```yaml
scrape_configs:
  - job_name: mqtt-relay
    scrape_interval: 30s
    static_configs:
      - targets: ['mqtt-relay:8081']
```

**Contoh alert rule:**
```yaml
- alert: MqttRelayDown
  expr: mqtt_relay_up == 0
  for: 1m
  annotations:
    summary: "MQTT Relay local broker disconnected"

- alert: MqttRelayNoTraffic
  expr: rate(mqtt_relay_messages_received_total[5m]) == 0
  for: 10m
  annotations:
    summary: "MQTT Relay tidak menerima pesan selama 10 menit"
```

---

## Build & Deployment

### Build Lokal

```bash
cd mqtt-relay
go build -ldflags="-s -w" -o mqtt-relay .
```

### Docker Build

```bash
docker build -t mqtt-relay ./mqtt-relay
```

### Docker Compose

```bash
# Jalankan bersama stack MeshNode
docker compose --profile phase2-relay up -d mqtt-relay
```

> **Catatan:** Service `mqtt-relay` menggunakan profile `phase2-relay`, jadi harus diaktifkan secara eksplisit.

### Dockerfile (Multi-stage)

```dockerfile
# Stage 1: Build binary Go dengan alpine
FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY go.mod ./
COPY *.go ./
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o mqtt-relay .

# Stage 2: Runtime minimal (~8MB)
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /build/mqtt-relay /usr/local/bin/mqtt-relay
CMD ["mqtt-relay"]
```

---

## Testing

### Unit Test

```bash
cd mqtt-relay
go test -v ./...
```

**Test case yang tersedia:**

- `TestCanonicalHashIgnoresMutableHopMetadata` — Memastikan dua MeshPacket yang hanya berbeda di field mutable (`hop_limit`, `via_mqtt`) menghasilkan canonical hash yang **sama**, sementara raw hash-nya **berbeda**.
- `TestMatchMQTTTopic` — Memvalidasi topic matcher menangani wildcard `+` (single-level) dan `#` (multi-level) sesuai spesifikasi MQTT.
- `TestDedupStoreSize` — Memastikan counter `Size()` akurat dan O(1) (atomic), bukan iterasi penuh.

### Manual Test

```bash
# Cek health endpoint
curl -s http://localhost:8081/health | jq .

# Monitor log secara real-time
docker logs -f mqtt-relay

# Dengan debug logging
LOG_LEVEL=DEBUG ./mqtt-relay
```

---

## Ringkasan Alur Lengkap

```
┌──────────────────────────────────────────────────────────────────┐
│                      handleMessage(source, msg)                  │
│                                                                  │
│  1. stats.Received++ & per-source counter++                      │
│  2. health.Touch() → update last_message_at                      │
│  3. TopicMatcher(topic) → reject jika tidak cocok                │
│  4. hash = CanonicalHash(topic, payload)                         │
│     └─ protobuf? → hash immutable fields only                   │
│     └─ non-protobuf? → hash raw bytes                           │
│  5. seen = dedup.CheckAndStore(hash, source)                     │
│     └─ hash baru / expired → seen.IsNew = true                  │
│     └─ hash masih valid → seen.IsNew = false                    │
│  6. targets = targetsFor(source, seen)                           │
│     ├─ local + new → [UpA, UpB]     (outbound relay)            │
│     ├─ local + !new → []            (echo, DROP)                │
│     ├─ upstream + new → [Local]     (inbound relay)             │
│     └─ upstream + !new → []         (duplicate, DROP)           │
│  7. len(targets)==0 → stats.Dropped++, return                    │
│  8. for target in targets: client.Publish(topic, 0, false, msg)  │
│  9. Update stats: RelayedOut (local src) atau RelayedIn (up src) │
└──────────────────────────────────────────────────────────────────┘
```
