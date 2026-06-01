package meshtastic

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"

	"meshnode.id/meshmap/internal/meshtastic/generated"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"google.golang.org/protobuf/proto"
)

var DefaultKey = []byte{
	0xd4, 0xf1, 0xbb, 0x3a,
	0x20, 0x29, 0x07, 0x59,
	0xf0, 0xbc, 0xff, 0xab,
	0xcf, 0x4e, 0x69, 0x01,
}

func NewBlockCipher(key []byte) cipher.Block {
	c, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	return c
}

type MQTTClient struct {
	Broker         string
	Username       string
	Password       string
	Topics         []string
	TopicRegex     *regexp.Regexp
	Accept         func(from uint32) bool
	BlockCipher    cipher.Block
	MessageHandler func(from uint32, topic string, portNum generated.PortNum, payload []byte)
	mqtt.Client
}

func (c *MQTTClient) Connect() error {
	randomId := make([]byte, 4)
	rand.Read(randomId)
	opts := mqtt.NewClientOptions()
	opts.AddBroker(c.Broker)
	opts.SetClientID(fmt.Sprintf("meshobserv-%x", randomId))
	opts.SetUsername(c.Username)
	opts.SetPassword(c.Password)
	opts.SetOrderMatters(false)
	opts.SetDefaultPublishHandler(c.handleMessage)
	if brokerUsesTLS(c.Broker) {
		opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: mqttTLSServerName(c.Broker)})
	}
	c.Client = mqtt.NewClient(opts)
	token := c.Client.Connect()
	<-token.Done()
	if err := token.Error(); err != nil {
		return err
	}
	log.Print("[mqtt] connected")
	topics := make(map[string]byte)
	for _, topic := range c.Topics {
		topics[topic] = 0
	}
	token = c.SubscribeMultiple(topics, nil)
	<-token.Done()
	if err := token.Error(); err != nil {
		return err
	}
	log.Print("[mqtt] subscribed")
	return nil
}

func brokerUsesTLS(broker string) bool {
	u, err := url.Parse(broker)
	if err != nil {
		return false
	}
	return u.Scheme == "ssl" || u.Scheme == "tls" || u.Scheme == "mqtts"
}

func mqttTLSServerName(broker string) string {
	if serverName := os.Getenv("MQTT_TLS_SERVER_NAME"); serverName != "" {
		return serverName
	}
	if serverName := os.Getenv("TLS_DOMAIN"); serverName != "" {
		return serverName
	}
	u, err := url.Parse(broker)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func (c *MQTTClient) Disconnect() {
	if c.IsConnected() {
		c.Client.Disconnect(1000)
	}
}

func (c *MQTTClient) handleMessage(_ mqtt.Client, msg mqtt.Message) {
	// filter topic
	topic := msg.Topic()
	if !c.TopicRegex.MatchString(topic) {
		log.Printf("[mqtt] topic %q did not match filter", topic)
		return
	}
	log.Printf("[mqtt] topic %q matched filter", topic)
	// parse ServiceEnvelope
	var envelope generated.ServiceEnvelope
	if err := proto.Unmarshal(msg.Payload(), &envelope); err != nil {
		// ignore non-Meshtastic payloads
		return
	}
	// get MeshPacket
	packet := envelope.GetPacket()
	if packet == nil {
		return
	}
	// no anonymous packets
	from := packet.GetFrom()
	if from == 0 {
		return
	}
	// ignore PKI direct messages
	if packet.GetPkiEncrypted() {
		return
	}
	// check sender
	if c.Accept != nil && !c.Accept(from) {
		return
	}
	// get Data, try decoded first
	data := packet.GetDecoded()
	if data == nil {
		// data must be (probably) encrypted
		encrypted := packet.GetEncrypted()
		if encrypted == nil {
			return
		}
		// decrypt
		nonce := make([]byte, 16)
		binary.LittleEndian.PutUint32(nonce[0:], packet.GetId())
		binary.LittleEndian.PutUint32(nonce[8:], from)
		decrypted := make([]byte, len(encrypted))
		cipher.NewCTR(c.BlockCipher, nonce).XORKeyStream(decrypted, encrypted)
		// parse Data
		data = new(generated.Data)
		if err := proto.Unmarshal(decrypted, data); err != nil {
			// ignore, probably encrypted with other psk
			return
		}
	}
	c.MessageHandler(from, topic, data.GetPortnum(), data.GetPayload())
}

func init() {
	mqtt.ERROR = log.New(os.Stderr, "[mqtt] error: ", log.Flags()|log.Lmsgprefix)
	mqtt.CRITICAL = log.New(os.Stderr, "[mqtt] crit: ", log.Flags()|log.Lmsgprefix)
}
