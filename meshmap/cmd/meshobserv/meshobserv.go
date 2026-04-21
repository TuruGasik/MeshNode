package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"meshnode.id/meshmap/internal/meshtastic"
	"meshnode.id/meshmap/internal/meshtastic/generated"

	"google.golang.org/protobuf/proto"
)

const (
	NodeExpiration     = 3600000000 // ~100 years
	NeighborExpiration = 7200       // 2 hr
	MetricsExpiration  = 7200       // 2 hr
	PruneWriteInterval = time.Minute
	SyncInterval       = 30 * time.Second
	RateLimitCount     = 1500
	RateLimitDuration  = 5 * time.Minute
)

var (
	Nodes      meshtastic.NodeDB
	NodesMutex sync.Mutex
	Receiving  atomic.Bool
)

func handleMessage(from uint32, topic string, portNum generated.PortNum, payload []byte) {
	Receiving.Store(true)
	go storeMessage(from, topic, portNum, payload)
	switch portNum {
	case generated.PortNum_TEXT_MESSAGE_APP:
		log.Printf("[msg] (%v) <%v> %s", topic, from, payload)
	case generated.PortNum_POSITION_APP:
		var position generated.Position
		if err := proto.Unmarshal(payload, &position); err != nil {
			log.Printf("[warn] could not parse Position payload from %v on %v: %v", from, topic, err)
			return
		}
		latitude := position.GetLatitudeI()
		longitude := position.GetLongitudeI()
		altitude := position.GetAltitude()
		precision := position.GetPrecisionBits()
		if latitude == 0 && longitude == 0 {
			return
		}
		// Store position history for tracker
		go StorePositionHistory(from, fmt.Sprintf("!%x", from), latitude, longitude, altitude, precision, "position")
		NodesMutex.Lock()
		if Nodes[from] == nil {
			Nodes[from] = meshtastic.NewNode(topic)
		}
		Nodes[from].UpdatePosition(latitude, longitude, altitude, precision)
		Nodes[from].UpdateSeenBy(topic)
		NodesMutex.Unlock()
	case generated.PortNum_NODEINFO_APP:
		var user generated.User
		if err := proto.Unmarshal(payload, &user); err != nil {
			log.Printf("[warn] could not parse User payload from %v on %v: %v", from, topic, err)
			return
		}
		longName := user.GetLongName()
		shortName := user.GetShortName()
		hwModel := user.GetHwModel().String()
		role := user.GetRole().String()
		if len(longName) == 0 {
			return
		}
		NodesMutex.Lock()
		if Nodes[from] == nil {
			Nodes[from] = meshtastic.NewNode(topic)
		}
		Nodes[from].UpdateUser(longName, shortName, hwModel, role)
		NodesMutex.Unlock()
	case generated.PortNum_TELEMETRY_APP:
		var telemetry generated.Telemetry
		if err := proto.Unmarshal(payload, &telemetry); err != nil {
			log.Printf("[warn] could not parse Telemetry payload from %v on %v: %v", from, topic, err)
			return
		}
		if deviceMetrics := telemetry.GetDeviceMetrics(); deviceMetrics != nil {
			batteryLevel := deviceMetrics.GetBatteryLevel()
			voltage := deviceMetrics.GetVoltage()
			chUtil := deviceMetrics.GetChannelUtilization()
			airUtilTx := deviceMetrics.GetAirUtilTx()
			uptime := deviceMetrics.GetUptimeSeconds()
			NodesMutex.Lock()
			if Nodes[from] == nil {
				Nodes[from] = meshtastic.NewNode(topic)
			}
			Nodes[from].UpdateDeviceMetrics(batteryLevel, voltage, chUtil, airUtilTx, uptime)
			NodesMutex.Unlock()
		} else if envMetrics := telemetry.GetEnvironmentMetrics(); envMetrics != nil {
			temperature := envMetrics.GetTemperature()
			relativeHumidity := envMetrics.GetRelativeHumidity()
			barometricPressure := envMetrics.GetBarometricPressure()
			lux := envMetrics.GetLux()
			windDirection := envMetrics.GetWindDirection()
			windSpeed := envMetrics.GetWindSpeed()
			windGust := envMetrics.GetWindGust()
			radiation := envMetrics.GetRadiation()
			rainfall1 := envMetrics.GetRainfall_1H()
			rainfall24 := envMetrics.GetRainfall_24H()
			NodesMutex.Lock()
			if Nodes[from] == nil {
				Nodes[from] = meshtastic.NewNode(topic)
			}
			Nodes[from].UpdateEnvironmentMetrics(
				temperature,
				relativeHumidity,
				barometricPressure,
				lux,
				windDirection,
				windSpeed,
				windGust,
				radiation,
				rainfall1,
				rainfall24,
			)
			NodesMutex.Unlock()
		}
	case generated.PortNum_NEIGHBORINFO_APP:
		var neighborInfo generated.NeighborInfo
		if err := proto.Unmarshal(payload, &neighborInfo); err != nil {
			log.Printf("[warn] could not parse NeighborInfo payload from %v on %v: %v", from, topic, err)
			return
		}
		nodeNum := neighborInfo.GetNodeId()
		neighbors := neighborInfo.GetNeighbors()
		if nodeNum != from {
			return
		}
		if len(neighbors) == 0 {
			return
		}
		NodesMutex.Lock()
		if Nodes[from] == nil {
			Nodes[from] = meshtastic.NewNode(topic)
		}
		for _, neighbor := range neighbors {
			neighborNum := neighbor.GetNodeId()
			if neighborNum == 0 {
				continue
			}
			Nodes[from].UpdateNeighborInfo(neighborNum, neighbor.GetSnr())
		}
		NodesMutex.Unlock()
	case generated.PortNum_MAP_REPORT_APP:
		log.Printf("[mapreport] received from %v on %v", from, topic)
		var mapReport generated.MapReport
		if err := proto.Unmarshal(payload, &mapReport); err != nil {
			log.Printf("[warn] could not parse MapReport payload from %v on %v: %v", from, topic, err)
			return
		}
		log.Printf("[mapreport] parsed: longName=%q, lat=%v, lon=%v", mapReport.GetLongName(), mapReport.GetLatitudeI(), mapReport.GetLongitudeI())
		longName := mapReport.GetLongName()
		shortName := mapReport.GetShortName()
		hwModel := mapReport.GetHwModel().String()
		role := mapReport.GetRole().String()
		fwVersion := mapReport.GetFirmwareVersion()
		region := mapReport.GetRegion().String()
		modemPreset := mapReport.GetModemPreset().String()
		hasDefaultCh := mapReport.GetHasDefaultChannel()
		onlineLocalNodes := mapReport.GetNumOnlineLocalNodes()
		latitude := mapReport.GetLatitudeI()
		longitude := mapReport.GetLongitudeI()
		altitude := mapReport.GetAltitude()
		precision := mapReport.GetPositionPrecision()
		if len(longName) == 0 {
			return
		}
		if latitude == 0 && longitude == 0 {
			return
		}
		// Store position history for tracker
		go StorePositionHistory(from, longName, latitude, longitude, altitude, precision, "mapreport")
		NodesMutex.Lock()
		if Nodes[from] == nil {
			Nodes[from] = meshtastic.NewNode(topic)
		}
		Nodes[from].UpdateUser(longName, shortName, hwModel, role)
		Nodes[from].UpdateMapReport(fwVersion, region, modemPreset, hasDefaultCh, onlineLocalNodes)
		Nodes[from].UpdatePosition(latitude, longitude, altitude, precision)
		Nodes[from].UpdateSeenBy(topic)
		NodesMutex.Unlock()
	}
}

func main() {
	var dbPath, blockedPath, broker, username, password, trackerPassword string
	flag.StringVar(&dbPath, "f", "", "node database `file`")
	flag.StringVar(&blockedPath, "b", "", "node blocklist `file`")
	flag.StringVar(&broker, "m", "tcp://mqtt.meshtastic.org:1883", "MQTT broker `URL`")
	flag.StringVar(&username, "u", "meshdev", "MQTT broker `username`")
	flag.StringVar(&password, "p", "large4cats", "MQTT broker `password`")
	flag.StringVar(&trackerPassword, "t", "", "tracker page `password`")
	flag.Parse()

	// Set tracker password from env/flag
	if envPwd := os.Getenv("TRACKER_PASSWORD"); envPwd != "" {
		SetTrackerPassword(envPwd)
	} else if trackerPassword != "" {
		SetTrackerPassword(trackerPassword)
	}
	// load or make NodeDB
	if len(dbPath) > 0 {
		err := Nodes.LoadFile(dbPath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Fatalf("[fatal] load nodes: %v", err)
		}
		log.Printf("[info] loaded %v nodes from disk", len(Nodes))
	}
	// init SQLite node store
	if err := InitNodeStore("/data/nodes.db"); err != nil {
		log.Fatalf("[fatal] init nodestore: %v", err)
	}
	defer CloseNodeStore()
	StartAPI(":8080")

	if Nodes == nil {
		Nodes = make(meshtastic.NodeDB)
	}

	// initial sync from SQLite to in-memory
	SyncFromSQLite(&Nodes, &NodesMutex)
	// load node blocklist
	blocked := make(map[uint32]struct{})
	if len(blockedPath) > 0 {
		f, err := os.Open(blockedPath)
		if err != nil {
			log.Fatalf("[fatal] open blocklist: %v", err)
		}
		s := bufio.NewScanner(f)
		for s.Scan() {
			n, err := strconv.ParseUint(s.Text(), 10, 32)
			if err == nil {
				blocked[uint32(n)] = struct{}{}
				log.Printf("[info] node %v blocked", n)
			}
		}
		f.Close()
		err = s.Err()
		if err != nil {
			log.Fatalf("[fatal] read blocklist: %v", err)
		}
	}
	// maintain per-node message counters for rate limiting
	var counters sync.Map // as map[uint32]*uint32
	go func() {
		for {
			time.Sleep(RateLimitDuration)
			counters.Clear()
		}
	}()
	// connect to MQTT
	client := &meshtastic.MQTTClient{
		Broker:   broker,
		Username: username,
		Password: password,
		Topics: []string{
			"msh/ID/2/map/",
			"msh/ID/2/e/+/+",
		},
		TopicRegex: regexp.MustCompile(`^msh(?:/[^/]+)+/2/(?:e/[^/]+/![0-9a-f#]+|map/)$`),
		Accept: func(from uint32) bool {
			if _, found := blocked[from]; found {
				return false
			}
			v, _ := counters.LoadOrStore(from, new(uint32))
			count := atomic.AddUint32(v.(*uint32), 1)
			if count >= RateLimitCount {
				if count%100 == 0 {
					log.Printf("[info] node %v rate limited (%v messages)", from, count)
				}
				return false
			}
			return true
		},
		BlockCipher:    meshtastic.NewBlockCipher(meshtastic.DefaultKey),
		MessageHandler: handleMessage,
	}
	err := client.Connect()
	if err != nil {
		log.Fatalf("[fatal] connect: %v", err)
	}
	// start NodeDB prune and write loop
	go func() {
		for {
			time.Sleep(PruneWriteInterval)
			NodesMutex.Lock()
			Nodes.Prune(NodeExpiration, NeighborExpiration, MetricsExpiration, NodeExpiration)
			if len(dbPath) > 0 {
				valid := Nodes.GetValid()
				err := valid.WriteFile(dbPath)
				if err != nil {
					log.Fatalf("[fatal] write nodes: %v", err)
				}
				log.Printf("[info] wrote %v nodes to disk", len(valid))
			}
			NodesMutex.Unlock()
			if !Receiving.CompareAndSwap(true, false) {
				log.Fatal("[fatal] no messages received")
			}
		}
	}()

	// periodic sync from SQLite to in-memory NodeDB
	go func() {
		for {
			time.Sleep(SyncInterval)
			SyncFromSQLite(&Nodes, &NodesMutex)
		}
	}()

	// periodic prune of position history (every 6 hours)
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		CleanupDuplicatePositions() // clean duplicates at startup
		PrunePositionHistory()      // then prune old records
		for {
			<-ticker.C
			CleanupDuplicatePositions()
			PrunePositionHistory()
		}
	}()

	// wait until exit
	terminate := make(chan os.Signal, 1)
	signal.Notify(terminate, syscall.SIGINT, syscall.SIGTERM)
	<-terminate
	log.Print("[info] exiting")
	client.Disconnect()
}
