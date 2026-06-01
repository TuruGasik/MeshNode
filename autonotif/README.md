# AutoNotif BMKG → Meshtastic MQTT

Service Go untuk mengambil data gempa BMKG terbaru lalu mengirim notifikasi ke Meshtastic MQTT sebagai paket protobuf terenkripsi.

## Fitur

- Fetch data BMKG dari endpoint `autogempa.json`, atau INATEWS2 dari `live30event.xml`.
- Interval default `2s` atau sekitar 30 request/menit.
- Publish ke MQTT Meshtastic binary protobuf.
- Enkripsi AES-CTR sesuai Meshtastic channel PSK.
- Deduplikasi event dengan file state lokal.
- Mode test lokal: `DRY_RUN`, `ONCE`, `SEND_ON_START`, dan `MESSAGE`.

## Format Pesan

Contoh pesan yang dikirim:

```text
Info Gempa - 29 Apr 2026, 17:09:04 WIB

M4.3 Ked 6 km
Pusat gempa berada di darat 10 km Barat Laut Luwu Timur
Dirasakan: III Malili, III Sorowako

Src: BMKG
```

## Konfigurasi Default

| Env | Default | Keterangan |
| --- | --- | --- |
| `BMKG_SOURCE` | `bmkg` | Sumber data gempa: `bmkg` atau `inatews2` |
| `BMKG_URL` | `https://data.bmkg.go.id/DataMKG/TEWS/autogempa.json` | Endpoint BMKG |
| `BMKG_INATEWS2_URL` | `https://bmkg-content-inatews.storage.googleapis.com/live30event.xml` | Endpoint INATEWS2 XML |
| `POLL_INTERVAL` | `2s` | Interval polling |
| `ONCE` | `false` | Fetch dan publish sekali lalu exit |
| `DRY_RUN` | `false` | Cetak pesan tanpa publish MQTT |
| `SEND_ON_START` | `false` | Kirim event terbaru saat service start |
| `MESSAGE` | kosong | Kirim custom message sekali lalu exit; tidak fetch BMKG dan tidak pakai state |
| `STATE_FILE` | `/data/autonotif-bmkg-state.json` atau `/data/autonotif-bmkg-inatews2-state.json` | File dedupe event terakhir; default mengikuti `BMKG_SOURCE` |
| `MQTT_HOST` | `meshnode-mqtt` | Broker MQTT |
| `MQTT_PORT` | `1883` | Port MQTT; set `8883` untuk MQTTS/TLS |
| `AUTONOTIF_MQTT_USER` | dari `../.env` | Username MQTT khusus AutoNotif |
| `AUTONOTIF_MQTT_PASS` | dari `../.env` | Password MQTT khusus AutoNotif |
| `MQTT_USERNAME` | kosong | Fallback username MQTT jika `AUTONOTIF_MQTT_USER` tidak ada |
| `MQTT_PASSWORD` | kosong | Fallback password MQTT jika `AUTONOTIF_MQTT_PASS` tidak ada |
| `MQTT_TLS` | otomatis `true` jika port `8883` | Pakai TLS |
| `MESHTASTIC_TOPIC_ROOT` | `msh/ID/2/e` | Root topic Meshtastic protobuf |
| `MESHTASTIC_CHANNEL` | `GempaBumi` | Nama channel Meshtastic |
| `MESHTASTIC_PRIVATE_KEY` | `GA==` | PSK channel GempaBumi; mendukung marker simple Meshtastic seperti `AQ==`/`GA==` atau AES key base64 |
| `MESHTASTIC_FROM_NODE` | `0x77727342` | Virtual node ID, tampil sebagai `!77727342` |
| `MESHTASTIC_ROLE` | `0` | Role virtual node; default `0` agar lebih cocok untuk DM/chat, bukan sensor |
| `MESHTASTIC_HOP_LIMIT` | `3` | Hop limit packet |
| `MESHTASTIC_NODE_KEYS_FILE` | `/data/meshtastic-nodekeys.json` | Cache node session keys (PKI) yang di-discover dari traffic |
| `MIN_MAGNITUDE` | `0` | Filter magnitude minimal; gempa di bawah ini tidak dikirim |
| `MQTT_CLIENT_ID` | `MeshNode-WRS` | Prefix client ID MQTT |
| `BOT_ENABLE_RESPONDER` | `true` | Aktifkan responder DM bot |
| `BOT_REPLY_TO_DM` | `true` | Balas DM dari user non-admin/public; jika `false`, user non-admin diabaikan |
| `BOT_ADMIN_NODES` | `!af1e4204` | Daftar node admin dipisah koma; `!af1e4204` adalah admin `Y0TR` |
| `BOT_ADMIN_BYPASS` | `true` | Admin tetap dibalas walau `BOT_REPLY_TO_DM=false` |
| `BOT_CONFIG_FILE` | `/data/autonotif-bot-config.json` | Persisted runtime config bot (admin list, dll) |

## Responder Bot

Channel `GempaBumi` dipakai sebagai outbound notification channel saja. Bot tidak membalas command broadcast/public di channel, termasuk `ping` atau `help`.

Interaksi dua arah dilakukan lewat DM ke virtual node AutoNotif. Command DM yang tersedia:

- `help` / `menu` — daftar command
- `ping` — balas `pong`
- `status` — status virtual node
- `gempa` — ambil data BMKG terbaru lalu balas via DM

Admin default:

```text
Node ID   : !af1e4204
Shortname : Y0TR
```

Contoh matikan response DM untuk user umum, tapi admin tetap bisa akses:

```dotenv
BOT_REPLY_TO_DM=false
BOT_ADMIN_BYPASS=true
BOT_ADMIN_NODES=!af1e4204
```

Saat dijalankan dari folder `autonotif`, service otomatis membaca `.env` dari:

1. `autonotif/.env`
2. `../.env`

Jadi credential AutoNotif cukup disimpan di `/root/MeshNode/.env`:

```dotenv
AUTONOTIF_MQTT_USER=...
AUTONOTIF_MQTT_PASS=...
```

## Cara Test Lokal

Masuk folder service:

```bash
cd /root/MeshNode/autonotif
```

Dry run tanpa publish MQTT:

```bash
DRY_RUN=true ONCE=true go run .
```

Dry run pakai INATEWS2 XML:

```bash
BMKG_SOURCE=inatews2 DRY_RUN=true ONCE=true go run .
```

Publish sekali ke MQTT:

```bash
rm -f .autonotif-state.json
ONCE=true go run .
```

Kirim custom test message tanpa fetch BMKG:

```bash
MESSAGE='Test GempaBumi dari AutoNotif' go run .
```

Dry run custom message:

```bash
DRY_RUN=true MESSAGE='Test GempaBumi dari AutoNotif' go run .
```

Jalankan loop dan kirim event terbaru saat start:

```bash
rm -f .autonotif-state.json
SEND_ON_START=true go run .
```

## Deduplikasi

Service menyimpan ID gempa terakhir yang sukses dikirim di file state. Default source `bmkg` memakai:

```text
/data/autonotif-bmkg-state.json
```

Source `inatews2` memakai default terpisah supaya tidak bentrok saat berganti source:

```text
/data/autonotif-bmkg-inatews2-state.json
```

Jika file ini ada dan event BMKG masih sama, service tidak akan mengirim ulang. Untuk test paksa kirim ulang, hapus file state:

```bash
rm -f .autonotif-state.json
```

## Topic MQTT

Default publish topic:

```text
msh/ID/2/e/GempaBumi/!77727342
```

Format ini mengikuti topic Meshtastic app-origin:

```text
msh/<region>/2/e/<channel>/!<nodeid>
```

## Catatan Penting untuk App Meshtastic

Broker menerima payload bukan berarti pesan langsung muncul di app. Device/app penerima harus subscribe channel yang sama.

Cek subscriber aktif di EMQX:

```bash
docker exec meshnode-mqtt emqx ctl subscriptions list | grep 'msh/ID'
```

Jika Android MQTT proxy hanya subscribe:

```text
msh/ID/2/e/LongFast/+
msh/ID/2/e/PKI/+
```

maka pesan ke `GempaBumi` tidak akan masuk app. Solusinya:

1. Tambahkan channel `GempaBumi` di Meshtastic dengan PSK yang sama.
2. Pastikan MQTT uplink/downlink aktif untuk channel tersebut.
3. Atau sementara kirim ke channel yang sudah disubscribe, misalnya `LongFast`:

```bash
MESHTASTIC_CHANNEL=LongFast ONCE=true go run .
```

## Docker Compose

Service `autonotif` sudah dipasang di root `docker-compose.yml`. Override yang aktif di compose:

- `AUTONOTIF_MQTT_USER` / `AUTONOTIF_MQTT_PASS` (dari `.env`)
- `MQTT_HOST=meshnode-mqtt`, `MQTT_PORT=1883`, `MQTT_TLS=false`
- `BMKG_SOURCE=inatews2`
- `MIN_MAGNITUDE=4.0`
- `STATE_FILE=/data/autonotif-bmkg-inatews2-state.json`
- `MESHTASTIC_NODE_KEYS_FILE=/data/meshtastic-nodekeys.json`
- `BOT_CONFIG_FILE=/data/autonotif-bot-config.json`
- `LOG_LEVEL=info`

Konfigurasi lain memakai default dari binary. Volume `./autonotif/data` di-mount ke `/data` untuk persist state, node keys cache, bot config, dan PKI key.

Jalankan bersama stack utama:

```bash
cd /root/MeshNode
docker compose up -d --build autonotif
```

Cek health dan log:

```bash
docker compose ps autonotif
docker compose logs -f autonotif
```

## Build Docker Manual

```bash
docker build -t autonotif-bmkg .
```
