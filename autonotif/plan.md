# Refactor Plan — autonotif

> **Status: ✅ Selesai (arsip).** Semua fase sudah closed. File ini disimpan sebagai catatan history refactor; bukan plan aktif. Untuk dokumentasi service yang aktual, lihat `README.md`.

Tujuan: rapikan struktur modular tanpa mengubah behavior yang sudah jalan. Setiap fase harus build hijau + test hijau sebelum lanjut fase berikutnya.

---

## Fase 0 — Baseline

- [x] `go vet ./...` + `go build ./...` + `go test ./...` di kondisi awal, catat hasilnya.
- [x] Pastikan tidak ada uncommitted changes selain plan ini.

---

## Fase 1 — Cheap wins (low risk, mechanical moves)

### 1.1 Pindah state file BMKG keluar dari `notify`
- [x] `internal/notify/state.go` → `internal/bmkg/state.go` (ganti package ke `bmkg`).
- [x] Update import di `internal/bmkg/bmkg.go`: `notify.LoadState` → `LoadState`, `notify.SaveState` → `SaveState`.
- [x] Hapus file lama; biarkan `internal/notify/notify.go` (interface `TextPublisher` masih dipakai).

### 1.2 Hapus `LoadBotRuntimeConfig` side-effect
- [x] Hapus auto-`SaveBotRuntimeConfig` di dalam `LoadBotRuntimeConfig` ketika file ada/valid.
- [x] Tetap auto-create file kalau belum ada (itu wajar untuk first run).
- [x] Tambah test `config_test.go`: load file existing tidak menulis ulang.

### 1.3 Konsistenkan default path
- [x] `DefaultStateFile` baru di `config`: `/data/autonotif-bmkg-state.json`.
- [x] Pindah default ke konstanta di `config` (sekarang hardcoded `".autonotif-state.json"` di `Load()`).
- [x] `DefaultHantavirusDB` juga diangkat ke konstanta.

### 1.4 Ekstrak helper logger + node info publisher
- [x] Pindah `setupLogger` dari `main.go` ke `internal/util/log.go`.
- [x] Pindah `startNodeInfoPublisher` ke `internal/meshtastic/publisher.go`.

**Verify:** `go build ./... && go test ./...`

---

## Fase 2 — Pisah `runHantavirusOnce` dari `main.go`

### 2.1 Buat `internal/hantavirus/service.go`
- [x] Pindah `runHantavirusOnce` jadi `func RunOnce(ctx context.Context, cfg config.HantavirusConfig) error`.
- [x] Pindah helper `countRaw` ke package yang sama (unexported).
- [x] `main.go` cukup: `if cfg.Hantavirus.Once { return hantavirus.RunOnce(ctx, cfg.Hantavirus) }`.

### 2.2 Rename `MigrateRaw` + `Migrate`
- [x] Gabung jadi satu `func Migrate(ctx, db) error` yang panggil keduanya internal.
- [x] Update call site di `db.Open`.
- [x] `MigrateRaw` tetap exported (digunakan langsung oleh test internal).

---

## Fase 3 — Decouple `bot` dari `bmkg`

### 3.1 Definisikan command registry di `bot`
- [x] `bot.Command` + `bot.CommandHandler` + `Responder.Register(cmd Command)`.

### 3.2 Pindah handler `gempa` keluar dari `responder.go`
- [x] Di `main.go`, register `gempa` lewat closure yang inject `bmkg.NewFetcher`.
- [x] Hapus import `bmkg` dari `internal/bot/responder.go`.
- [x] Built-in commands (`ping`, `status`, `help`, `config`) tetap di `responder.go`.

### 3.3 Update test
- [x] Test existing tetap hijau.
- [x] Tambah `TestResponderRegisterCustomCommand` dan `TestResponderAdminOnlyCustomCommand`.

---

## Fase 4 — Split package `meshtastic` (revised)

Awalnya direncanakan split ke subpackage. Setelah dikaji, lebih idiomatic Go untuk pisah ke file terpisah dalam satu package, karena `Client` shared state dengan codec/crypto/nodekeys.

### 4.1 Ekstrak crypto helpers
- [x] `crypto.go` baru: `channelCipher`, `newChannelCipher`, `decodeMeshtasticKey`, `channelHash`, `xorHash`.
- [x] `client.go` panggil helper baru, hilang duplikasi `encryptData`/`decryptData`.

### 4.2 Ekstrak node key cache
- [x] `nodekeys.go` baru: `nodeKeyStore` (mu + map + file persistence) sebagai komponen mandiri.
- [x] `Client` punya `*nodeKeyStore` field, panggil `Remember`/`Lookup` instead of method receivers di `Client`.
- [x] `client.go` jauh lebih tipis (~200 line dari sebelumnya ~400).

---

## Fase 5 — Split `config` per modul

### 5.1 Tiap modul define config-nya sendiri
- [x] `internal/bmkg/config.go` → `bmkg.Config` + `bmkg.LoadConfig()`.
- [x] `internal/meshtastic/config.go` → `meshtastic.Config` + `meshtastic.LoadConfig()`.
- [x] `internal/bot/config.go` → `bot.Config` + `bot.RuntimeConfig` + `bot.LoadConfig()` + `LoadRuntimeConfig`/`SaveRuntimeConfig`.
- [x] `internal/hantavirus/config.go` → `hantavirus.Config` + `hantavirus.LoadConfig()`.

### 5.2 `internal/config/config.go` jadi assembler tipis
- [x] Hanya berisi top-level `Config` struct + `Load()` yang merangkai per-module configs.
- [x] Helper `getEnv*` + `loadDotEnv` pindah ke `internal/util/env.go` sebagai exported helpers.
- [x] Test config lama dihapus (test sekarang ada di tiap modul lewat existing `config_test.go` di bot, dll).

---

## Fase 6 — Reorg `hantavirus` jadi multi-source layout (SKIPPED)

Setelah dikaji, reorg ke subpackage akan menambah friction:
- `Case` di-share oleh mapper, store, dan service → split paksa banyak cross-package import.
- 9 file flat saat ini sudah ter-organisir per concern (model/store/source/service).
- Worth dilakukan kalau ada source ke-3 dalam waktu dekat. Belum ada, jadi skip.

---

## Fase 7 — Final pass

- [x] `go vet ./...` final, no warnings.
- [x] `gofmt -w` applied ke `main.go` dan `internal/meshtastic/client.go`.
- [x] All tests passing.
- [x] Smoke test runtime: `DRY_RUN=true MESSAGE="smoke-test refactor"` jalan normal.

---

## Yang TIDAK akan dikerjakan (out of scope)

- Ganti `paho.mqtt.golang` ke library lain.
- Tambah feature baru (misal source notifikasi baru).
- Ganti SQLite ke DB lain.
- Refactor protobuf encoding logic.
- Ubah default channel name / private key / topic root.

---

## Urutan eksekusi

Fase **1 → 2 → 3 → 4 → 5** wajib berurutan karena tiap fase pakai hasil fase sebelumnya. Fase **6** independen, boleh terakhir atau di-skip. Fase **7** selalu paling akhir.

Setiap commit harus build hijau. Kalau di tengah fase ada test yang fail karena perubahan struktural (bukan logic), update test-nya di commit yang sama.
