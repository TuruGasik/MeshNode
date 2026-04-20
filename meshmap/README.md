# MeshNode Indonesia — Peta Node Meshtastic

Peta realtime node [Meshtastic](https://meshtastic.org/) di jaringan **ID** Indonesia.  
Berdasarkan [meshmap.net](https://github.com/brianshea2/meshmap.net) oleh Brian Shea (AGPL-3.0), di-custom untuk komunitas [MeshNode Indonesia](https://github.com/TuruGasik/MeshNode).

**Live:** [map.dari.asia](https://map.dari.asia)

## Fitur
- Menampilkan semua node yang mengirim posisi valid via MQTT di kanal ID
- Data node diperbarui setiap ~1 menit
- Node yang tidak aktif > 1 hari otomatis disembunyikan dari peta (tetap tersimpan di database)
- Duplikasi node (nama sama, device di-reflash) otomatis di-hide jika ada yang online
- Pencarian node berdasarkan nama, ID, atau hex
- Node Database modal — lihat semua node dengan filter: Semua, Online, Offline, Dead, Punya Posisi, Tanpa Posisi
- Marker bulat berwarna: 🔵 Online (< 12 jam) | 🔴 Offline (12–24 jam)
- Dark mode
- SQLite backend — semua node yang pernah terdeteksi disimpan permanen
- API endpoints: `/api/nodes/map`, `/api/nodes/all`, `/api/nodes/search`, `/api/nodes/stats`

## Arsitektur
```
docker-compose.yml
├── meshnode-mqtt     — EMQX MQTT broker lokal
├── meshmap           — nginx + meshobserv (Go binary)
│   ├── meshobserv    — MQTT listener, NodeDB in-memory, SQLite store, HTTP API
│   ├── nginx         — reverse proxy (/api → :8080), TLS, static files
│   └── website/      — frontend (Leaflet.js, css/style.css, js/app.js)
└── mqtt-relay        — relay/dedup local ↔ upstream brokers
```

Catatan runtime saat ini:
- `meshobserv` subscribe ke broker lokal `meshnode-mqtt`
- upstream traffic masuk melalui `mqtt-relay`
- dedup dilakukan sebelum pesan upstream dipublish ke broker lokal

## Build & Deploy
```bash
# Build
docker compose build meshmap

# Deploy
docker compose up -d meshmap
```

## Go Module
Module: `meshnode.id/meshmap`  
Build: Go 1.25, CGO_ENABLED=0, pure Go SQLite ([modernc.org/sqlite](https://modernc.org/sqlite))

## Tech Stack
- **Backend:** Go 1.25, protobuf, MQTT (paho), SQLite (WAL mode)
- **Frontend:** Leaflet.js 1.9.4, MarkerCluster, Leaflet Search, EasyButton, Font Awesome 4.7, Inter font
- **Infra:** Docker, nginx, Let's Encrypt TLS

## Lisensi
AGPL-3.0 — Lihat [LICENSE](LICENSE) untuk detail.  
Berdasarkan karya [Brian Shea](https://github.com/brianshea2/meshmap.net).  
Repo: [github.com/TuruGasik/MeshNode](https://github.com/TuruGasik/MeshNode)
