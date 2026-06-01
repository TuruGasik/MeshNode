package meshtastic

import (
	"time"

	"meshnode/autonotif/internal/util"
)

const (
	DefaultBrokerHost    = "meshnode-mqtt"
	DefaultBrokerPort    = 1883
	DefaultPrivateKey    = "GA=="
	DefaultChannelName   = "GempaBumi"
	DefaultTopicRoot     = "msh/ID/2/e"
	DefaultPKIKeyFile    = "/data/meshtastic-pki.key"
	DefaultNodeKeysFile  = "/data/meshtastic-nodekeys.json"
	DefaultFromNode      = 0x77727342
	DefaultNodeLongName  = "MeshNode WRS"
	DefaultNodeShortName = "-GB-"
	DefaultHardwareModel = 255
	DefaultNodeRole      = 0
	DefaultClientID      = "MeshNode-WRS"
	DefaultNodeInfoEvery = 3 * time.Hour
	DefaultHopLimit      = 3
)

// Config holds Meshtastic / MQTT transport configuration.
type Config struct {
	BrokerHost        string
	BrokerPort        int
	MQTTUsername      string
	MQTTPassword      string
	MQTTUseTLS        bool
	MQTTTLSServerName string
	TopicRoot         string
	ChannelName       string
	PrivateKeyB64     string
	PKIPrivateKeyB64  string
	PKIKeyFile        string
	NodeKeysFile      string
	FromNode          uint32
	HopLimit          uint32
	ClientID          string
	NodeLongName      string
	NodeShortName     string
	HardwareModel     uint32
	NodeRole          uint32
	NodeInfoInterval  time.Duration
}

// LoadConfig reads Meshtastic settings from environment variables, applying
// defaults for any missing values.
func LoadConfig() Config {
	port := util.GetEnvInt("MQTT_PORT", DefaultBrokerPort)
	return Config{
		BrokerHost:        util.GetEnv("MQTT_HOST", DefaultBrokerHost),
		BrokerPort:        port,
		MQTTUsername:      util.GetEnvAny([]string{"AUTONOTIF_MQTT_USER", "MQTT_USERNAME"}, ""),
		MQTTPassword:      util.GetEnvAny([]string{"AUTONOTIF_MQTT_PASS", "MQTT_PASSWORD"}, ""),
		MQTTUseTLS:        util.GetEnvBool("MQTT_TLS", port == 8883),
		MQTTTLSServerName: util.GetEnv("MQTT_TLS_SERVER_NAME", ""),
		TopicRoot:         trimRightSlash(util.GetEnv("MESHTASTIC_TOPIC_ROOT", DefaultTopicRoot)),
		ChannelName:       util.GetEnv("MESHTASTIC_CHANNEL", DefaultChannelName),
		PrivateKeyB64:     util.GetEnv("MESHTASTIC_PRIVATE_KEY", DefaultPrivateKey),
		PKIPrivateKeyB64:  util.GetEnv("MESHTASTIC_PKI_PRIVATE_KEY", ""),
		PKIKeyFile:        util.GetEnv("MESHTASTIC_PKI_KEY_FILE", DefaultPKIKeyFile),
		NodeKeysFile:      util.GetEnv("MESHTASTIC_NODE_KEYS_FILE", DefaultNodeKeysFile),
		FromNode:          uint32(util.GetEnvInt("MESHTASTIC_FROM_NODE", DefaultFromNode)),
		HopLimit:          uint32(util.GetEnvInt("MESHTASTIC_HOP_LIMIT", DefaultHopLimit)),
		ClientID:          util.GetEnv("MQTT_CLIENT_ID", DefaultClientID),
		NodeLongName:      util.GetEnv("MESHTASTIC_LONG_NAME", DefaultNodeLongName),
		NodeShortName:     util.GetEnv("MESHTASTIC_SHORT_NAME", DefaultNodeShortName),
		HardwareModel:     uint32(util.GetEnvInt("MESHTASTIC_HW_MODEL", DefaultHardwareModel)),
		NodeRole:          uint32(util.GetEnvInt("MESHTASTIC_ROLE", DefaultNodeRole)),
		NodeInfoInterval:  util.GetEnvDuration("NODE_INFO_INTERVAL", DefaultNodeInfoEvery),
	}
}

func trimRightSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
