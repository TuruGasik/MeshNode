package meshtastic

import (
	"context"
	"crypto/cipher"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"meshnode/autonotif/internal/util"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type Client struct {
	cfg         Config
	client      mqtt.Client
	blockCipher cipher.Block
	channelHash uint32
	pkiPrivate  []byte
	pkiPublic   []byte
	nodes       *nodeKeyStore
}

func NewClient(cfg Config) (*Client, error) {
	block, key, err := newChannelCipher(cfg.PrivateKeyB64)
	if err != nil {
		return nil, err
	}
	pkiKeys, err := LoadOrCreatePKIKey(cfg.PKIPrivateKeyB64, cfg.PKIKeyFile)
	if err != nil {
		return nil, err
	}
	return &Client{
		cfg:         cfg,
		blockCipher: block,
		channelHash: channelHash(cfg.ChannelName, key),
		pkiPrivate:  pkiKeys.Private,
		pkiPublic:   pkiKeys.Public,
		nodes:       newNodeKeyStore(cfg.NodeKeysFile),
	}, nil
}

func (c *Client) NodeNum() uint32 {
	return c.cfg.FromNode
}

func (c *Client) LongName() string {
	return c.cfg.NodeLongName
}

func (c *Client) Connect() error {
	brokerScheme := "tcp"
	if c.cfg.MQTTUseTLS {
		brokerScheme = "ssl"
	}
	broker := fmt.Sprintf("%s://%s:%d", brokerScheme, c.cfg.BrokerHost, c.cfg.BrokerPort)
	clientID := fmt.Sprintf("%s-%x", c.cfg.ClientID, util.RandomUint32())

	opts := mqtt.NewClientOptions()
	opts.AddBroker(broker)
	opts.SetClientID(clientID)
	opts.SetUsername(c.cfg.MQTTUsername)
	opts.SetPassword(c.cfg.MQTTPassword)
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectTimeout(10 * time.Second)
	if c.cfg.MQTTUseTLS {
		opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.cfg.MQTTTLSServerName})
	}

	c.client = mqtt.NewClient(opts)
	token := c.client.Connect()
	if !token.WaitTimeout(15 * time.Second) {
		return errors.New("mqtt connect timeout")
	}
	return token.Error()
}

func (c *Client) Disconnect() {
	if c.client != nil && c.client.IsConnected() {
		c.client.Disconnect(1000)
	}
}

func (c *Client) PublishTopic() string {
	return c.publishTopicForChannel(c.cfg.ChannelName)
}

func (c *Client) SubscribeTopic() string {
	return fmt.Sprintf("%s/%s/+", c.cfg.TopicRoot, c.cfg.ChannelName)
}

func (c *Client) PKISubscribeTopic() string {
	return fmt.Sprintf("%s/%s/+", c.cfg.TopicRoot, PKIChannelName)
}

func (c *Client) publishTopicForChannel(channelName string) string {
	return fmt.Sprintf("%s/%s/%s", c.cfg.TopicRoot, channelName, FormatNodeID(c.cfg.FromNode))
}

func (c *Client) PublishText(text string) error {
	return c.PublishTextTo(BroadcastNode, text)
}

func (c *Client) PublishTextTo(to uint32, text string) error {
	if c.client == nil || !c.client.IsConnected() {
		return errors.New("mqtt client is not connected")
	}
	if to != BroadcastNode {
		remoteKey, ok := c.nodes.Lookup(to)
		if ok {
			return c.publishPKIDataTo(to, TextMessagePort, []byte(text), true, remoteKey)
		}
		slog.Warn("remote public key unknown, falling back to channel encrypted direct packet", "to", FormatNodeID(to))
	}
	return c.publishDataTo(to, TextMessagePort, []byte(text), true)
}

func (c *Client) PublishNodeInfo() error {
	if c.client == nil || !c.client.IsConnected() {
		return errors.New("mqtt client is not connected")
	}
	slog.Info("publishing node info", "node", FormatNodeID(c.cfg.FromNode), "pki_public_key", base64.StdEncoding.EncodeToString(c.pkiPublic))
	return c.publishDataTo(BroadcastNode, NodeInfoPort, EncodeUser(c.cfg.FromNode, c.cfg.NodeLongName, c.cfg.NodeShortName, c.cfg.HardwareModel, c.cfg.NodeRole, c.pkiPublic), false)
}

func (c *Client) publishDataTo(to uint32, portNum int32, payload []byte, wantResponse bool) error {
	return c.publishDataToWithRequest(to, portNum, payload, wantResponse, 0)
}

func (c *Client) publishDataToWithRequest(to uint32, portNum int32, payload []byte, wantResponse bool, requestID uint32) error {
	packetID := util.RandomUint32()
	data := EncodeDataWithRequestID(portNum, payload, wantResponse, requestID)
	encrypted := channelCipher(c.blockCipher, packetID, c.cfg.FromNode, data)

	meshPacket := EncodeMeshPacket(c.cfg.FromNode, to, packetID, c.channelHash, c.cfg.HopLimit, encrypted)
	envelope := EncodeServiceEnvelope(meshPacket, c.cfg.ChannelName, c.cfg.FromNode)

	token := c.client.Publish(c.PublishTopic(), 0, false, envelope)
	if !token.WaitTimeout(10 * time.Second) {
		return errors.New("mqtt publish timeout")
	}
	return token.Error()
}

func (c *Client) publishPKIDataTo(to uint32, portNum int32, payload []byte, wantResponse bool, remotePublicKey []byte) error {
	return c.publishPKIDataToWithRequest(to, portNum, payload, wantResponse, remotePublicKey, 0)
}

func (c *Client) publishPKIDataToWithRequest(to uint32, portNum int32, payload []byte, wantResponse bool, remotePublicKey []byte, requestID uint32) error {
	packetID := util.RandomUint32()
	data := EncodeDataWithRequestID(portNum, payload, wantResponse, requestID)
	encrypted, err := pkiEncrypt(c.pkiPrivate, remotePublicKey, packetID, c.cfg.FromNode, data)
	if err != nil {
		return err
	}

	meshPacket := EncodeMeshPacketPacket(MeshPacket{
		From:         c.cfg.FromNode,
		To:           to,
		ID:           packetID,
		ChannelHash:  0,
		HopLimit:     c.cfg.HopLimit,
		Encrypted:    encrypted,
		PKIEncrypted: true,
	})
	envelope := EncodeServiceEnvelope(meshPacket, PKIChannelName, c.cfg.FromNode)

	token := c.client.Publish(c.publishTopicForChannel(PKIChannelName), 0, false, envelope)
	if !token.WaitTimeout(10 * time.Second) {
		return errors.New("mqtt publish timeout")
	}
	if err := token.Error(); err != nil {
		return err
	}
	slog.Info("pki direct packet sent", "to", FormatNodeID(to), "portnum", portNum, "packet_id", packetID)
	return nil
}

// AckDirectMessage sends a Routing.NONE ACK packet for a direct message we
// just received. requestID must be the PacketID of the message being ACKed.
// The ACK is routed via PKI when we have the sender's public key, otherwise
// it falls back to the channel-encrypted direct path — mirroring how text
// replies are routed.
func (c *Client) AckDirectMessage(to uint32, requestID uint32) error {
	if c.client == nil || !c.client.IsConnected() {
		return errors.New("mqtt client is not connected")
	}
	if requestID == 0 {
		return errors.New("ack requires non-zero request id")
	}
	// Routing proto with error_reason=NONE encodes as zero bytes (default).
	if remoteKey, ok := c.nodes.Lookup(to); ok {
		return c.publishPKIDataToWithRequest(to, RoutingAppPort, nil, false, remoteKey, requestID)
	}
	return c.publishDataToWithRequest(to, RoutingAppPort, nil, false, requestID)
}

func (c *Client) SubscribeMessages(ctx context.Context, handler func(IncomingMessage)) error {
	if c.client == nil || !c.client.IsConnected() {
		return errors.New("mqtt client is not connected")
	}
	topics := []string{c.SubscribeTopic()}
	if c.cfg.ChannelName != PKIChannelName {
		topics = append(topics, c.PKISubscribeTopic())
	}

	subscriptions := make(map[string]byte, len(topics))
	for _, topic := range topics {
		subscriptions[topic] = 0
	}

	token := c.client.SubscribeMultiple(subscriptions, func(_ mqtt.Client, msg mqtt.Message) {
		incoming, err := c.DecodeIncoming(msg.Payload())
		if err != nil {
			if errors.Is(err, ErrIgnoredPacket) {
				return
			}
			slog.Debug("incoming packet ignored", "topic", msg.Topic(), "error", err)
			return
		}
		if incoming.From == c.cfg.FromNode {
			return
		}
		handler(incoming)
	})
	if !token.WaitTimeout(10 * time.Second) {
		return errors.New("mqtt subscribe timeout")
	}
	if err := token.Error(); err != nil {
		return err
	}
	slog.Info("meshtastic responder subscribed", "topics", topics)

	go func() {
		<-ctx.Done()
		if c.client != nil && c.client.IsConnected() {
			c.client.Unsubscribe(topics...).WaitTimeout(5 * time.Second)
		}
	}()
	return nil
}

func FormatNodeID(node uint32) string {
	return util.FormatNodeID(node)
}
