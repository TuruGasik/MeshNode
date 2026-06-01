# MeshMap local development

Frontend MeshMap bisa dijalankan lokal dengan Vite watcher. Perubahan di `website/**/*.html`, `website/css/**/*.css`, dan `website/js/**/*.js` akan auto reload.

## 1. Install dependency dev

```bash
cd meshmap
npm install
```

## 2. Jalankan frontend watcher saja

Pakai mode ini kalau backend/API `meshobserv` sudah jalan di Docker atau host lain.

```bash
cd meshmap
npm run dev
```

Buka:

- `http://localhost:5178/`
- `http://localhost:5178/tracker.html`

Default proxy API menuju `http://127.0.0.1:8080`. Override jika perlu:

```bash
VITE_API_TARGET=http://localhost:8080 npm run dev
```

Kalau memakai backend dari container `meshmap`, pastikan compose sudah direstart setelah port `8080` diekspos:

```bash
docker compose up -d --build meshmap
```

## 3. Jalankan API + frontend lokal

Butuh generated Meshtastic protobuf Go files di `internal/meshtastic/generated`.

```bash
cd meshmap
mkdir -p data
npm run dev:full
```

Script ini menjalankan:

- `go run ./cmd/meshobserv` di port `8080`
- Vite dev server di port `5178`
- SQLite lokal di `./data/nodes.db`

Environment yang bisa diset:

```bash
MQTT_BROKER=tcp://localhost:1883 \
MQTT_USERNAME=meshmap \
MQTT_PASSWORD=meshmap \
TRACKER_PASSWORD=devpass \
npm run dev:full
```

## 4. HTTPS lokal opsional

Kalau perlu test secure context:

```bash
VITE_DEV_HTTPS=true npm run dev
```

Buka `https://localhost:5178/` dan accept self-signed certificate.
