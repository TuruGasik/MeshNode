package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"meshnode.id/meshmap/internal/meshtastic"
	"meshnode.id/meshmap/internal/meshtastic/generated"

	"google.golang.org/protobuf/proto"

	_ "modernc.org/sqlite"
)

var (
	nodeStoreDB *sql.DB
	nodeStoreMu sync.Mutex
)

func InitNodeStore(dbPath string) error {
	var err error
	nodeStoreDB, err = sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	if _, err = nodeStoreDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("set WAL: %w", err)
	}
	if _, err = nodeStoreDB.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return fmt.Errorf("set busy_timeout: %w", err)
	}
	_, err = nodeStoreDB.Exec(`CREATE TABLE IF NOT EXISTS nodes (
		node_num            INTEGER PRIMARY KEY,
		hex_id              TEXT    NOT NULL DEFAULT '',
		long_name           TEXT    NOT NULL DEFAULT '',
		short_name          TEXT    NOT NULL DEFAULT '',
		hw_model            TEXT    NOT NULL DEFAULT '',
		role                TEXT    NOT NULL DEFAULT '',
		fw_version          TEXT    NOT NULL DEFAULT '',
		region              TEXT    NOT NULL DEFAULT '',
		modem_preset        TEXT    NOT NULL DEFAULT '',
		has_default_ch      INTEGER NOT NULL DEFAULT 0,
		online_local_nodes  INTEGER NOT NULL DEFAULT 0,
		latitude            INTEGER NOT NULL DEFAULT 0,
		longitude           INTEGER NOT NULL DEFAULT 0,
		altitude            INTEGER NOT NULL DEFAULT 0,
		precision_bits      INTEGER NOT NULL DEFAULT 0,
		battery_level       INTEGER NOT NULL DEFAULT 0,
		voltage             REAL    NOT NULL DEFAULT 0,
		ch_util             REAL    NOT NULL DEFAULT 0,
		air_util_tx         REAL    NOT NULL DEFAULT 0,
		uptime              INTEGER NOT NULL DEFAULT 0,
		temperature         REAL    NOT NULL DEFAULT 0,
		relative_humidity   REAL    NOT NULL DEFAULT 0,
		barometric_pressure REAL    NOT NULL DEFAULT 0,
		has_position        INTEGER NOT NULL DEFAULT 0,
		first_seen          INTEGER NOT NULL DEFAULT 0,
		last_seen           INTEGER NOT NULL DEFAULT 0,
		updated_at          INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	for _, ddl := range []string{
		"CREATE INDEX IF NOT EXISTS idx_long_name ON nodes(long_name COLLATE NOCASE)",
		"CREATE INDEX IF NOT EXISTS idx_short_name ON nodes(short_name COLLATE NOCASE)",
		"CREATE INDEX IF NOT EXISTS idx_hex_id ON nodes(hex_id)",
		"CREATE INDEX IF NOT EXISTS idx_has_position ON nodes(has_position)",
		"CREATE INDEX IF NOT EXISTS idx_last_seen ON nodes(last_seen)",
	} {
		if _, err = nodeStoreDB.Exec(ddl); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	// Create position_history table for tracking node movements
	_, err = nodeStoreDB.Exec(`CREATE TABLE IF NOT EXISTS position_history (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		node_num        INTEGER NOT NULL,
		hex_id          TEXT    NOT NULL,
		latitude        INTEGER NOT NULL,
		longitude       INTEGER NOT NULL,
		altitude        INTEGER DEFAULT 0,
		precision_bits  INTEGER DEFAULT 0,
		source          TEXT    DEFAULT 'position',
		created_at      INTEGER NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create position_history table: %w", err)
	}
	_, err = nodeStoreDB.Exec(`CREATE INDEX IF NOT EXISTS idx_poshist_node ON position_history(node_num, created_at DESC)`)
	if err != nil {
		return fmt.Errorf("create poshist index: %w", err)
	}
	var count int
	nodeStoreDB.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&count)
	log.Printf("[nodestore] initialized (%d nodes in db)", count)
	return nil
}

func CloseNodeStore() {
	if nodeStoreDB != nil {
		nodeStoreDB.Close()
	}
}

// storeMessage is called for every MQTT message to persist node data to SQLite.
// It independently parses protobuf payloads (same as handleMessage) so we don't
// need fragile sed patches into each handler case.
func storeMessage(from uint32, topic string, portNum generated.PortNum, payload []byte) {
	if nodeStoreDB == nil {
		return
	}
	now := time.Now().Unix()
	hexID := fmt.Sprintf("!%x", from)

	// Always touch last_seen for any message type
	touchNode(from, hexID, now)

	switch portNum {
	case generated.PortNum_NODEINFO_APP:
		var user generated.User
		if err := proto.Unmarshal(payload, &user); err != nil {
			return
		}
		longName := user.GetLongName()
		if len(longName) == 0 {
			return
		}
		upsertUser(from, hexID, longName, user.GetShortName(), user.GetHwModel().String(), user.GetRole().String(), now)

	case generated.PortNum_POSITION_APP:
		var pos generated.Position
		if err := proto.Unmarshal(payload, &pos); err != nil {
			return
		}
		lat := pos.GetLatitudeI()
		lon := pos.GetLongitudeI()
		if lat == 0 && lon == 0 {
			return
		}
		upsertPosition(from, hexID, lat, lon, pos.GetAltitude(), pos.GetPrecisionBits(), now)
		// Store position history for tracker
		go StorePositionHistory(from, hexID, lat, lon, pos.GetAltitude(), pos.GetPrecisionBits(), "position")

	case generated.PortNum_TELEMETRY_APP:
		var tel generated.Telemetry
		if err := proto.Unmarshal(payload, &tel); err != nil {
			return
		}
		if dm := tel.GetDeviceMetrics(); dm != nil {
			upsertDeviceMetrics(from, hexID, dm.GetBatteryLevel(), dm.GetVoltage(),
				dm.GetChannelUtilization(), dm.GetAirUtilTx(), dm.GetUptimeSeconds(), now)
		} else if em := tel.GetEnvironmentMetrics(); em != nil {
			upsertEnvMetrics(from, hexID, em.GetTemperature(), em.GetRelativeHumidity(),
				em.GetBarometricPressure(), now)
		}

	case generated.PortNum_MAP_REPORT_APP:
		var mr generated.MapReport
		if err := proto.Unmarshal(payload, &mr); err != nil {
			return
		}
		longName := mr.GetLongName()
		if len(longName) == 0 {
			return
		}
		lat := mr.GetLatitudeI()
		lon := mr.GetLongitudeI()
		if lat == 0 && lon == 0 {
			return
		}
		upsertUser(from, hexID, longName, mr.GetShortName(), mr.GetHwModel().String(), mr.GetRole().String(), now)
		upsertMapReport(from, hexID, mr.GetFirmwareVersion(), mr.GetRegion().String(),
			mr.GetModemPreset().String(), mr.GetHasDefaultChannel(), mr.GetNumOnlineLocalNodes(), now)
		upsertPosition(from, hexID, lat, lon, mr.GetAltitude(), mr.GetPositionPrecision(), now)
		// Store position history for tracker
		go StorePositionHistory(from, hexID, lat, lon, mr.GetAltitude(), mr.GetPositionPrecision(), "map_report")
	}
}

func touchNode(nodeNum uint32, hexID string, now int64) {
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()
	_, _ = nodeStoreDB.Exec(`
		INSERT INTO nodes (node_num, hex_id, first_seen, last_seen, updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT(node_num) DO UPDATE SET last_seen = excluded.last_seen
	`, nodeNum, hexID, now, now, now)
}

func upsertUser(nodeNum uint32, hexID, longName, shortName, hwModel, role string, now int64) {
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()
	_, err := nodeStoreDB.Exec(`
		INSERT INTO nodes (node_num, hex_id, long_name, short_name, hw_model, role, first_seen, last_seen, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(node_num) DO UPDATE SET
			long_name  = CASE WHEN excluded.long_name  != '' THEN excluded.long_name  ELSE nodes.long_name  END,
			short_name = CASE WHEN excluded.short_name != '' THEN excluded.short_name ELSE nodes.short_name END,
			hw_model   = CASE WHEN excluded.hw_model   != '' THEN excluded.hw_model   ELSE nodes.hw_model   END,
			role       = CASE WHEN excluded.role        != '' THEN excluded.role       ELSE nodes.role       END,
			hex_id     = excluded.hex_id,
			last_seen  = excluded.last_seen,
			updated_at = excluded.updated_at
	`, nodeNum, hexID, longName, shortName, hwModel, role, now, now, now)
	if err != nil {
		log.Printf("[nodestore] user %s: %v", hexID, err)
	}
}

func upsertPosition(nodeNum uint32, hexID string, lat, lon, alt int32, precision uint32, now int64) {
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()
	hasPos := 0
	if lat != 0 || lon != 0 {
		hasPos = 1
	}
	_, err := nodeStoreDB.Exec(`
		INSERT INTO nodes (node_num, hex_id, latitude, longitude, altitude, precision_bits, has_position, first_seen, last_seen, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(node_num) DO UPDATE SET
			latitude       = excluded.latitude,
			longitude      = excluded.longitude,
			altitude       = excluded.altitude,
			precision_bits = excluded.precision_bits,
			has_position   = excluded.has_position,
			last_seen      = excluded.last_seen,
			updated_at     = excluded.updated_at
	`, nodeNum, hexID, lat, lon, alt, precision, hasPos, now, now, now)
	if err != nil {
		log.Printf("[nodestore] position %s: %v", hexID, err)
	}
}

func upsertDeviceMetrics(nodeNum uint32, hexID string, battery uint32, voltage, chUtil, airUtilTx float32, uptime uint32, now int64) {
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()
	_, err := nodeStoreDB.Exec(`
		INSERT INTO nodes (node_num, hex_id, battery_level, voltage, ch_util, air_util_tx, uptime, first_seen, last_seen, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(node_num) DO UPDATE SET
			battery_level = excluded.battery_level,
			voltage       = excluded.voltage,
			ch_util       = excluded.ch_util,
			air_util_tx   = excluded.air_util_tx,
			uptime        = excluded.uptime,
			last_seen     = excluded.last_seen,
			updated_at    = excluded.updated_at
	`, nodeNum, hexID, battery, voltage, chUtil, airUtilTx, uptime, now, now, now)
	if err != nil {
		log.Printf("[nodestore] device %s: %v", hexID, err)
	}
}

func upsertEnvMetrics(nodeNum uint32, hexID string, temperature, humidity, pressure float32, now int64) {
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()
	_, err := nodeStoreDB.Exec(`
		INSERT INTO nodes (node_num, hex_id, temperature, relative_humidity, barometric_pressure, first_seen, last_seen, updated_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(node_num) DO UPDATE SET
			temperature         = excluded.temperature,
			relative_humidity   = excluded.relative_humidity,
			barometric_pressure = excluded.barometric_pressure,
			last_seen           = excluded.last_seen,
			updated_at          = excluded.updated_at
	`, nodeNum, hexID, temperature, humidity, pressure, now, now, now)
	if err != nil {
		log.Printf("[nodestore] env %s: %v", hexID, err)
	}
}

func upsertMapReport(nodeNum uint32, hexID, fwVersion, region, modemPreset string, hasDefaultCh bool, onlineLocalNodes uint32, now int64) {
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()
	defCh := 0
	if hasDefaultCh {
		defCh = 1
	}
	_, err := nodeStoreDB.Exec(`
		INSERT INTO nodes (node_num, hex_id, fw_version, region, modem_preset, has_default_ch, online_local_nodes, first_seen, last_seen, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(node_num) DO UPDATE SET
			fw_version         = CASE WHEN excluded.fw_version   != '' THEN excluded.fw_version   ELSE nodes.fw_version   END,
			region             = CASE WHEN excluded.region       != '' THEN excluded.region       ELSE nodes.region       END,
			modem_preset       = CASE WHEN excluded.modem_preset != '' THEN excluded.modem_preset ELSE nodes.modem_preset END,
			has_default_ch     = excluded.has_default_ch,
			online_local_nodes = excluded.online_local_nodes,
			last_seen          = excluded.last_seen,
			updated_at         = excluded.updated_at
	`, nodeNum, hexID, fwVersion, region, modemPreset, defCh, onlineLocalNodes, now, now, now)
	if err != nil {
		log.Printf("[nodestore] map report %s: %v", hexID, err)
	}
}

// ── HTTP API ─────────────────────────────────────────────────

type APINode struct {
	NodeNum            uint32  `json:"nodeNum"`
	HexID              string  `json:"hexId"`
	LongName           string  `json:"longName"`
	ShortName          string  `json:"shortName"`
	HwModel            string  `json:"hwModel"`
	Role               string  `json:"role"`
	FwVersion          string  `json:"fwVersion,omitempty"`
	Region             string  `json:"region,omitempty"`
	ModemPreset        string  `json:"modemPreset,omitempty"`
	HasDefaultCh       bool    `json:"hasDefaultCh,omitempty"`
	OnlineLocalNodes   uint32  `json:"onlineLocalNodes,omitempty"`
	Latitude           float64 `json:"latitude"`
	Longitude          float64 `json:"longitude"`
	Altitude           int32   `json:"altitude,omitempty"`
	BatteryLevel       uint32  `json:"batteryLevel,omitempty"`
	Voltage            float64 `json:"voltage,omitempty"`
	ChUtil             float64 `json:"chUtil,omitempty"`
	AirUtilTx          float64 `json:"airUtilTx,omitempty"`
	Uptime             uint32  `json:"uptime,omitempty"`
	Temperature        float64 `json:"temperature,omitempty"`
	RelativeHumidity   float64 `json:"relativeHumidity,omitempty"`
	BarometricPressure float64 `json:"barometricPressure,omitempty"`
	HasPosition        bool    `json:"hasPosition"`
	FirstSeen          int64   `json:"firstSeen"`
	LastSeen           int64   `json:"lastSeen"`
}

type StatsResponse struct {
	TotalNodes   int `json:"totalNodes"`
	WithPosition int `json:"withPosition"`
	NoPosition   int `json:"noPosition"`
}

func StartAPI(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/nodes/map", handleNodeMap)
	mux.HandleFunc("/api/nodes/search", handleNodeSearch)
	mux.HandleFunc("/api/nodes/all", handleNodeAll)
	mux.HandleFunc("/api/nodes/stats", handleNodeStats)
	// Tracker API endpoints
	mux.HandleFunc("/api/tracker/nodes", handleTrackerNodes)
	mux.HandleFunc("/api/tracker/nodes/", handleTrackerNodePath)
	log.Printf("[api] starting on %s", addr)
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("[api] error: %v", err)
		}
	}()
}

// handleNodeMap serves valid nodes from in-memory NodeDB (same format as old nodes.json)
func handleNodeMap(w http.ResponseWriter, _ *http.Request) {
	NodesMutex.Lock()
	valid := Nodes.GetValid()
	NodesMutex.Unlock()
	writeJSON(w, http.StatusOK, valid)
}

func handleNodeSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query 'q' required"})
		return
	}
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()
	if nodeStoreDB == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "db not ready"})
		return
	}
	like := "%" + q + "%"
	rows, err := nodeStoreDB.Query(`
		SELECT node_num, hex_id, long_name, short_name, hw_model, role,
			fw_version, region, modem_preset, has_default_ch, online_local_nodes,
			latitude, longitude, altitude, battery_level, voltage, ch_util, air_util_tx,
			uptime, temperature, relative_humidity, barometric_pressure,
			has_position, first_seen, last_seen
		FROM nodes
		WHERE long_name LIKE ? COLLATE NOCASE
		   OR short_name LIKE ? COLLATE NOCASE
		   OR hex_id LIKE ?
		ORDER BY last_seen DESC LIMIT ?
	`, like, like, like, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	writeJSON(w, http.StatusOK, scanAPINodes(rows))
}

func handleNodeAll(w http.ResponseWriter, r *http.Request) {
	limit := 100
	offset := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v >= 0 {
		offset = v
	}
	filter := r.URL.Query().Get("filter")
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()
	if nodeStoreDB == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "db not ready"})
		return
	}
	q := `SELECT node_num, hex_id, long_name, short_name, hw_model, role,
		fw_version, region, modem_preset, has_default_ch, online_local_nodes,
		latitude, longitude, altitude, battery_level, voltage, ch_util, air_util_tx,
		uptime, temperature, relative_humidity, barometric_pressure,
		has_position, first_seen, last_seen FROM nodes`
	var args []any
	switch filter {
	case "with_position":
		q += " WHERE has_position = 1"
	case "no_position":
		q += " WHERE has_position = 0"
	}
	q += " ORDER BY last_seen DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := nodeStoreDB.Query(q, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	writeJSON(w, http.StatusOK, scanAPINodes(rows))
}

func handleNodeStats(w http.ResponseWriter, _ *http.Request) {
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()
	if nodeStoreDB == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "db not ready"})
		return
	}
	var s StatsResponse
	nodeStoreDB.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&s.TotalNodes)
	nodeStoreDB.QueryRow("SELECT COUNT(*) FROM nodes WHERE has_position=1").Scan(&s.WithPosition)
	s.NoPosition = s.TotalNodes - s.WithPosition
	writeJSON(w, http.StatusOK, s)
}

func scanAPINodes(rows *sql.Rows) []APINode {
	var out []APINode
	for rows.Next() {
		var n APINode
		var latI, lonI int32
		var hasPos, defCh int
		rows.Scan(
			&n.NodeNum, &n.HexID, &n.LongName, &n.ShortName, &n.HwModel, &n.Role,
			&n.FwVersion, &n.Region, &n.ModemPreset, &defCh, &n.OnlineLocalNodes,
			&latI, &lonI, &n.Altitude, &n.BatteryLevel, &n.Voltage, &n.ChUtil, &n.AirUtilTx,
			&n.Uptime, &n.Temperature, &n.RelativeHumidity, &n.BarometricPressure,
			&hasPos, &n.FirstSeen, &n.LastSeen,
		)
		n.Latitude = float64(latI) / 1e7
		n.Longitude = float64(lonI) / 1e7
		n.HasPosition = hasPos == 1
		n.HasDefaultCh = defCh == 1
		out = append(out, n)
	}
	if out == nil {
		out = []APINode{}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// ── Position History (Tracker) ──────────────────────────────────────────────

// PositionHistoryRecord represents a single position record
type PositionHistoryRecord struct {
	ID             int64   `json:"id"`
	NodeNum        uint32  `json:"nodeNum"`
	HexID          string  `json:"hexId"`
	LatitudeInt    int32   `json:"-"`
	LongitudeInt   int32   `json:"-"`
	LatitudeFloat  float64 `json:"latitude"`
	LongitudeFloat float64 `json:"longitude"`
	Altitude       int32   `json:"altitude,omitempty"`
	PrecisionBits  uint32  `json:"precisionBits,omitempty"`
	Source         string  `json:"source"`
	CreatedAt      int64   `json:"createdAt"`
}

// TrackerNode represents a mobile node with latest position
type TrackerNode struct {
	NodeNum        uint32                  `json:"nodeNum"`
	HexID          string                  `json:"hexId"`
	LongName       string                  `json:"longName"`
	ShortName      string                  `json:"shortName"`
	Latitude       float64                 `json:"latitude"`
	Longitude      float64                 `json:"longitude"`
	LastSeen       int64                   `json:"lastSeen"`
	PositionCount  int                     `json:"positionCount"`
	TotalDistanceM float64                 `json:"totalDistanceM,omitempty"`
	AvgSpeedKmh    float64                 `json:"avgSpeedKmh,omitempty"`
	Positions      []PositionHistoryRecord `json:"positions,omitempty"`
}

// StorePositionHistory saves a position record for a node
func StorePositionHistory(nodeNum uint32, hexID string, lat, lon, alt int32, precision uint32, source string) {
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()
	if nodeStoreDB == nil {
		return
	}

	// Skip if same position as last record (within precision threshold)
	threshold := int32(1) // minimum 1 unit (~11cm at equator)
	if precision > 0 && precision < 32 {
		threshold = int32(1 << uint(32-precision))
		if threshold < 1 {
			threshold = 1
		}
	}

	var lastLat, lastLon int32
	var lastTime int64
	now := time.Now().Unix()
	err := nodeStoreDB.QueryRow(`
		SELECT latitude, longitude, created_at FROM position_history
		WHERE node_num = ? ORDER BY created_at DESC LIMIT 1
	`, nodeNum).Scan(&lastLat, &lastLon, &lastTime)
	if err == nil {
		// Check if position changed beyond threshold
		if absInt32(lat-lastLat) <= threshold && absInt32(lon-lastLon) <= threshold {
			// Same position, skip (but update timestamp if > 1 hour old)
			if lastTime < now-3600 {
				nodeStoreDB.Exec(`UPDATE position_history SET created_at = ? WHERE id = (
					SELECT id FROM position_history WHERE node_num = ? ORDER BY created_at DESC LIMIT 1
				)`, now, nodeNum)
			}
			return
		}
	}

	_, err = nodeStoreDB.Exec(`
		INSERT INTO position_history (node_num, hex_id, latitude, longitude, altitude, precision_bits, source, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, nodeNum, hexID, lat, lon, alt, precision, source, now)
	if err != nil {
		log.Printf("[poshist] insert error: %v", err)
	}
}

func absInt32(n int32) int32 {
	if n < 0 {
		return -n
	}
	return n
}

// PrunePositionHistory removes old records
func PrunePositionHistory() {
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()
	if nodeStoreDB == nil {
		return
	}
	now := time.Now().Unix()
	cutoff := now - (7 * 24 * 60 * 60) // 7 days
	// Delete records older than 7 days
	result, err := nodeStoreDB.Exec(`DELETE FROM position_history WHERE created_at < ?`, cutoff)
	if err != nil {
		log.Printf("[poshist] prune error: %v", err)
		return
	}
	deleted, _ := result.RowsAffected()
	if deleted > 0 {
		log.Printf("[poshist] pruned %d old records", deleted)
	}
}

// CleanupDuplicatePositions removes duplicate position entries
// Keeps only the first occurrence of each unique (node_num, latitude, longitude)
func CleanupDuplicatePositions() {
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()
	if nodeStoreDB == nil {
		return
	}

	// Delete duplicate positions, keeping oldest (rn=1) for each unique (node_num, lat, lon)
	// Only affects positions appearing 2+ times
	_, err := nodeStoreDB.Exec(`
		DELETE FROM position_history
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY node_num, latitude, longitude ORDER BY id) as rn
				FROM position_history
			) WHERE rn > 1
		)
	`)
	if err != nil {
		log.Printf("[poshist] cleanup error: %v", err)
		return
	}
	log.Printf("[poshist] duplicate cleanup done")
}

// GetTrackerNodes returns nodes that have position history (mobile nodes)
func GetTrackerNodes() []TrackerNode {
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()
	if nodeStoreDB == nil {
		return nil
	}
	// First get all nodes with position history
	rows, err := nodeStoreDB.Query(`
		SELECT 
			n.node_num, n.hex_id, 
			CASE WHEN n.long_name != '' THEN n.long_name ELSE n.hex_id END as display_name,
			CASE WHEN n.short_name != '' THEN n.short_name ELSE '' END as short_name,
			n.latitude, n.longitude, n.last_seen,
			COUNT(ph.id) as pos_count
		FROM nodes n
		JOIN position_history ph ON n.node_num = ph.node_num
		WHERE n.has_position = 1
		GROUP BY n.node_num
		HAVING pos_count >= 1
		ORDER BY n.last_seen DESC
		LIMIT 100
	`)
	if err != nil {
		log.Printf("[tracker] get nodes error: %v", err)
		return nil
	}
	defer rows.Close()

	// Get position history for each node
	var nodes []TrackerNode
	for rows.Next() {
		var n TrackerNode
		var latI, lonI int32
		var displayName string
		rows.Scan(&n.NodeNum, &n.HexID, &displayName, &n.ShortName,
			&latI, &lonI, &n.LastSeen, &n.PositionCount)
		n.LongName = displayName
		n.Latitude = float64(latI) / 1e7
		n.Longitude = float64(lonI) / 1e7

		// Get recent positions for this node
		posRows, err := nodeStoreDB.Query(`
			SELECT id, node_num, hex_id, latitude, longitude, altitude, precision_bits, source, created_at
			FROM position_history
			WHERE node_num = ?
			ORDER BY created_at DESC
			LIMIT 50
		`, n.NodeNum)
		if err == nil {
			for posRows.Next() {
				var p PositionHistoryRecord
				posRows.Scan(&p.ID, &p.NodeNum, &p.HexID, &p.LatitudeInt, &p.LongitudeInt,
					&p.Altitude, &p.PrecisionBits, &p.Source, &p.CreatedAt)
				p.LatitudeFloat = float64(p.LatitudeInt) / 1e7
				p.LongitudeFloat = float64(p.LongitudeInt) / 1e7
				n.Positions = append(n.Positions, p)
			}
			posRows.Close()
		}

		nodes = append(nodes, n)
	}
	if nodes == nil {
		nodes = []TrackerNode{}
	}
	return nodes
}

// GetNodePositionHistory returns position history for a specific node
func GetNodePositionHistory(nodeNum uint32, limit int) []PositionHistoryRecord {
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()
	if nodeStoreDB == nil {
		return nil
	}
	if limit <= 0 {
		limit = 500
	}
	rows, err := nodeStoreDB.Query(`
		SELECT id, node_num, hex_id, latitude, longitude, altitude, precision_bits, source, created_at
		FROM position_history
		WHERE node_num = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, nodeNum, limit)
	if err != nil {
		log.Printf("[tracker] get history error: %v", err)
		return nil
	}
	defer rows.Close()
	var records []PositionHistoryRecord
	for rows.Next() {
		var r PositionHistoryRecord
		var latI, lonI int32
		rows.Scan(&r.ID, &r.NodeNum, &r.HexID, &latI, &lonI, &r.Altitude,
			&r.PrecisionBits, &r.Source, &r.CreatedAt)
		r.LatitudeFloat = float64(latI) / 1e7
		r.LongitudeFloat = float64(lonI) / 1e7
		records = append(records, r)
	}
	if records == nil {
		records = []PositionHistoryRecord{}
	}
	return records
}

// ── Tracker Auth ──────────────────────────────────────────────────────────────

var trackerPassword string

func SetTrackerPassword(pwd string) {
	trackerPassword = pwd
}

func checkTrackerAuth(r *http.Request) bool {
	if trackerPassword == "" {
		return true // No password set, allow all
	}
	// Check header first (used by tracker.js), then query param
	pwd := r.Header.Get("X-Tracker-Password")
	if pwd == "" {
		pwd = r.URL.Query().Get("pwd")
	}
	return pwd == trackerPassword
}

func handleTrackerNodes(w http.ResponseWriter, r *http.Request) {
	if !checkTrackerAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	nodes := GetTrackerNodes()
	writeJSON(w, http.StatusOK, nodes)
}

func handleTrackerNodePath(w http.ResponseWriter, r *http.Request) {
	if !checkTrackerAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	nodeNumStr := strings.TrimPrefix(r.URL.Path, "/api/tracker/nodes/")
	nodeNumStr = strings.TrimSuffix(nodeNumStr, "/path")
	nodeNum, err := strconv.ParseUint(nodeNumStr, 10, 32)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid node num"})
		return
	}
	limit := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 1000 {
		limit = v
	}
	records := GetNodePositionHistory(uint32(nodeNum), limit)
	writeJSON(w, http.StatusOK, records)
}

// SyncFromSQLite loads valid nodes from SQLite into the in-memory NodeDB.
// This ensures nodes persisted in SQLite appear on the map even after restart.
func SyncFromSQLite(target *meshtastic.NodeDB, mu *sync.Mutex) {
	nodeStoreMu.Lock()
	defer nodeStoreMu.Unlock()

	if nodeStoreDB == nil {
		return
	}

	rows, err := nodeStoreDB.Query(`
		SELECT node_num, hex_id, long_name, short_name, hw_model, role,
			latitude, longitude, altitude, precision_bits,
			fw_version, region, modem_preset, has_default_ch, online_local_nodes,
			battery_level, voltage, ch_util, air_util_tx, uptime,
			temperature, relative_humidity, barometric_pressure,
			first_seen, last_seen
		FROM nodes
		WHERE has_position = 1 AND long_name != ''
		ORDER BY last_seen DESC
		LIMIT 2000
	`)
	if err != nil {
		log.Printf("[sync] query error: %v", err)
		return
	}
	defer rows.Close()

	syncTime := time.Now().Unix()
	count := 0

	for rows.Next() {
		var nodeNum uint32
		var hexID, longName, shortName, hwModel, role string
		var latI, lonI, altitude int32
		var precision uint32
		var fwVersion, region, modemPreset string
		var defCh, onlineLocalNodes int
		var batteryLevel uint32
		var voltage, chUtil, airUtilTx float32
		var uptime uint32
		var temperature, humidity, pressure float32
		var firstSeen, lastSeen int64

		rows.Scan(
			&nodeNum, &hexID, &longName, &shortName, &hwModel, &role,
			&latI, &lonI, &altitude, &precision,
			&fwVersion, &region, &modemPreset, &defCh, &onlineLocalNodes,
			&batteryLevel, &voltage, &chUtil, &airUtilTx, &uptime,
			&temperature, &humidity, &pressure,
			&firstSeen, &lastSeen,
		)

		mu.Lock()
		node := (*target)[nodeNum]
		if node == nil {
			node = meshtastic.NewNode("synced")
			(*target)[nodeNum] = node
		}
		mu.Unlock()

		// Update user info
		node.LongName = longName
		node.ShortName = shortName
		node.HwModel = hwModel
		node.Role = role

		// Update position
		node.Latitude = latI
		node.Longitude = lonI
		node.Altitude = altitude
		node.Precision = precision

		// Update map report fields
		node.FwVersion = fwVersion
		node.Region = region
		node.ModemPreset = modemPreset
		node.HasDefaultCh = defCh == 1
		node.OnlineLocalNodes = uint32(onlineLocalNodes)

		// Update device metrics
		node.BatteryLevel = batteryLevel
		node.Voltage = voltage
		node.ChUtil = chUtil
		node.AirUtilTx = airUtilTx
		node.Uptime = uptime

		// Update environment metrics
		node.Temperature = temperature
		node.RelativeHumidity = humidity
		node.BarometricPressure = pressure

		// Ensure SeenBy has at least one entry (for IsValid check)
		if len(node.SeenBy) == 0 {
			node.SeenBy = map[string]int64{"synced": syncTime}
		}

		count++
	}

	log.Printf("[sync] synced %d nodes from SQLite", count)
}
