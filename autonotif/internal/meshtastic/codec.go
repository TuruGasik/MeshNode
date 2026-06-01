package meshtastic

import (
	"bytes"
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
)

var ErrIgnoredPacket = errors.New("ignored packet")

const (
	BroadcastNode         = 0xffffffff
	PKIChannelName        = "PKI"
	TextMessagePort       = 1
	NodeInfoPort          = 4
	RoutingAppPort        = 5
	MaxMessageBytes       = 200
	MaxPacketPayloadBytes = 237
)

type ServiceEnvelope struct {
	Packet    []byte
	ChannelID string
	GatewayID string
}

type MeshPacket struct {
	From         uint32
	To           uint32
	ChannelHash  uint32
	Decoded      []byte
	Encrypted    []byte
	ID           uint32
	HopLimit     uint32
	PublicKey    []byte
	PKIEncrypted bool
}

type DataPacket struct {
	PortNum      int32
	Payload      []byte
	WantResponse bool
}

type User struct {
	ID             string
	LongName       string
	ShortName      string
	HardwareModel  uint32
	Role           uint32
	PublicKey      []byte
	IsUnmessagable bool
}

type IncomingMessage struct {
	From        uint32
	To          uint32
	PacketID    uint32
	ChannelID   string
	Text        string
	IsDirect    bool
	IsBroadcast bool
}

func (c *Client) DecodeIncoming(payload []byte) (IncomingMessage, error) {
	envelope, err := DecodeServiceEnvelope(payload)
	if err != nil {
		return IncomingMessage{}, err
	}
	packet, err := DecodeMeshPacket(envelope.Packet)
	if err != nil {
		return IncomingMessage{}, err
	}
	if packet.From == c.cfg.FromNode {
		return IncomingMessage{}, fmt.Errorf("%w: own packet", ErrIgnoredPacket)
	}
	if len(packet.PublicKey) == pkiKeySize && !packet.PKIEncrypted {
		c.nodes.Remember(packet.From, packet.PublicKey)
	}

	decoded := packet.Decoded
	if decoded == nil {
		if packet.Encrypted == nil {
			return IncomingMessage{}, errors.New("packet does not contain decoded or encrypted payload")
		}
		decoded, err = c.decodePacketData(packet)
		if err != nil {
			return IncomingMessage{}, err
		}
	}
	data, err := DecodeData(decoded)
	if err != nil {
		return IncomingMessage{}, err
	}
	if data.PortNum == NodeInfoPort {
		user, err := DecodeUser(data.Payload)
		if err != nil {
			return IncomingMessage{}, fmt.Errorf("decode nodeinfo: %w", err)
		}
		if len(user.PublicKey) == pkiKeySize {
			c.nodes.Remember(packet.From, user.PublicKey)
		}
		return IncomingMessage{}, fmt.Errorf("%w: portnum %d", ErrIgnoredPacket, data.PortNum)
	}
	if data.PortNum != TextMessagePort {
		return IncomingMessage{}, fmt.Errorf("%w: portnum %d", ErrIgnoredPacket, data.PortNum)
	}

	return IncomingMessage{
		From:        packet.From,
		To:          packet.To,
		PacketID:    packet.ID,
		ChannelID:   envelope.ChannelID,
		Text:        string(data.Payload),
		IsDirect:    packet.To == c.cfg.FromNode,
		IsBroadcast: packet.To == BroadcastNode,
	}, nil
}

func (c *Client) decodePacketData(packet MeshPacket) ([]byte, error) {
	if packet.ChannelHash == 0 && packet.To == c.cfg.FromNode && packet.To != BroadcastNode {
		remoteKey, ok := c.nodes.Lookup(packet.From)
		if ok {
			decoded, err := pkiDecrypt(c.pkiPrivate, remoteKey, packet.ID, packet.From, packet.Encrypted)
			if err == nil {
				return decoded, nil
			}
			if len(packet.PublicKey) != pkiKeySize || bytes.Equal(packet.PublicKey, remoteKey) {
				return nil, fmt.Errorf("pki decrypt: %w", err)
			}
		}
		if len(packet.PublicKey) == pkiKeySize {
			decoded, err := pkiDecrypt(c.pkiPrivate, packet.PublicKey, packet.ID, packet.From, packet.Encrypted)
			if err != nil {
				if !ok {
					return nil, fmt.Errorf("pki decrypt with packet public key: %w", err)
				}
				return nil, fmt.Errorf("pki decrypt with cached and packet public keys failed: %w", err)
			}
			c.nodes.Remember(packet.From, packet.PublicKey)
			return decoded, nil
		}
		if !ok {
			return nil, fmt.Errorf("pki sender public key unknown: %s", FormatNodeID(packet.From))
		}
	}
	if packet.ChannelHash != 0 && packet.ChannelHash != c.channelHash {
		return nil, fmt.Errorf("channel hash mismatch: got %d want %d", packet.ChannelHash, c.channelHash)
	}
	return channelCipher(c.blockCipher, packet.ID, packet.From, packet.Encrypted), nil
}

func EncodeData(portNum int32, payload []byte, wantResponse bool) []byte {
	return EncodeDataWithRequestID(portNum, payload, wantResponse, 0)
}

// EncodeDataWithRequestID encodes a Data proto with an optional request_id
// (field 6, fixed32). Pass requestID=0 to omit the field. ACK packets set
// portNum=RoutingAppPort and requestID=<original packet id>.
func EncodeDataWithRequestID(portNum int32, payload []byte, wantResponse bool, requestID uint32) []byte {
	var out []byte
	out = protowire.AppendTag(out, 1, protowire.VarintType)
	out = protowire.AppendVarint(out, uint64(portNum))
	if len(payload) > 0 {
		out = protowire.AppendTag(out, 2, protowire.BytesType)
		out = protowire.AppendBytes(out, payload)
	}
	if wantResponse {
		out = protowire.AppendTag(out, 3, protowire.VarintType)
		out = protowire.AppendVarint(out, 1)
	}
	if requestID != 0 {
		out = protowire.AppendTag(out, 6, protowire.Fixed32Type)
		out = protowire.AppendFixed32(out, requestID)
	}
	return out
}

func DecodeData(in []byte) (DataPacket, error) {
	var data DataPacket
	for len(in) > 0 {
		num, typ, n := protowire.ConsumeTag(in)
		if n < 0 {
			return DataPacket{}, protowire.ParseError(n)
		}
		in = in[n:]
		switch num {
		case 1:
			if typ != protowire.VarintType {
				return DataPacket{}, errors.New("data.portnum has invalid wire type")
			}
			value, n := protowire.ConsumeVarint(in)
			if n < 0 {
				return DataPacket{}, protowire.ParseError(n)
			}
			data.PortNum = int32(value)
			in = in[n:]
		case 2:
			if typ != protowire.BytesType {
				return DataPacket{}, errors.New("data.payload has invalid wire type")
			}
			value, n := protowire.ConsumeBytes(in)
			if n < 0 {
				return DataPacket{}, protowire.ParseError(n)
			}
			data.Payload = append([]byte(nil), value...)
			in = in[n:]
		case 3:
			if typ != protowire.VarintType {
				return DataPacket{}, errors.New("data.want_response has invalid wire type")
			}
			value, n := protowire.ConsumeVarint(in)
			if n < 0 {
				return DataPacket{}, protowire.ParseError(n)
			}
			data.WantResponse = value != 0
			in = in[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, in)
			if n < 0 {
				return DataPacket{}, protowire.ParseError(n)
			}
			in = in[n:]
		}
	}
	return data, nil
}

func EncodeUser(nodeNum uint32, longName, shortName string, hardwareModel, role uint32, publicKey []byte) []byte {
	var out []byte
	out = appendStringField(out, 1, FormatNodeID(nodeNum))
	out = appendStringField(out, 2, longName)
	out = appendStringField(out, 3, shortName)
	out = appendVarintField(out, 5, uint64(hardwareModel))
	out = appendVarintField(out, 7, uint64(role))
	if len(publicKey) > 0 {
		out = appendBytesField(out, 8, publicKey)
	}
	out = appendVarintField(out, 9, 0)
	return out
}

func DecodeUser(in []byte) (User, error) {
	var user User
	for len(in) > 0 {
		num, typ, n := protowire.ConsumeTag(in)
		if n < 0 {
			return User{}, protowire.ParseError(n)
		}
		in = in[n:]
		switch num {
		case 1:
			value, consumed, err := consumeStringField(typ, in)
			if err != nil {
				return User{}, fmt.Errorf("user.id: %w", err)
			}
			user.ID = value
			in = in[consumed:]
		case 2:
			value, consumed, err := consumeStringField(typ, in)
			if err != nil {
				return User{}, fmt.Errorf("user.long_name: %w", err)
			}
			user.LongName = value
			in = in[consumed:]
		case 3:
			value, consumed, err := consumeStringField(typ, in)
			if err != nil {
				return User{}, fmt.Errorf("user.short_name: %w", err)
			}
			user.ShortName = value
			in = in[consumed:]
		case 5:
			if typ != protowire.VarintType {
				return User{}, errors.New("user.hw_model has invalid wire type")
			}
			value, n := protowire.ConsumeVarint(in)
			if n < 0 {
				return User{}, protowire.ParseError(n)
			}
			user.HardwareModel = uint32(value)
			in = in[n:]
		case 7:
			if typ != protowire.VarintType {
				return User{}, errors.New("user.role has invalid wire type")
			}
			value, n := protowire.ConsumeVarint(in)
			if n < 0 {
				return User{}, protowire.ParseError(n)
			}
			user.Role = uint32(value)
			in = in[n:]
		case 8:
			if typ != protowire.BytesType {
				return User{}, errors.New("user.public_key has invalid wire type")
			}
			value, n := protowire.ConsumeBytes(in)
			if n < 0 {
				return User{}, protowire.ParseError(n)
			}
			user.PublicKey = append([]byte(nil), value...)
			in = in[n:]
		case 9:
			if typ != protowire.VarintType {
				return User{}, errors.New("user.is_unmessagable has invalid wire type")
			}
			value, n := protowire.ConsumeVarint(in)
			if n < 0 {
				return User{}, protowire.ParseError(n)
			}
			user.IsUnmessagable = value != 0
			in = in[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, in)
			if n < 0 {
				return User{}, protowire.ParseError(n)
			}
			in = in[n:]
		}
	}
	return user, nil
}

func EncodeMeshPacket(from, to, packetID, channelHash, hopLimit uint32, encrypted []byte) []byte {
	return EncodeMeshPacketPacket(MeshPacket{From: from, To: to, ID: packetID, ChannelHash: channelHash, HopLimit: hopLimit, Encrypted: encrypted})
}

func EncodeMeshPacketPacket(packet MeshPacket) []byte {
	var out []byte
	out = appendFixed32Field(out, 1, packet.From)
	out = appendFixed32Field(out, 2, packet.To)
	out = appendVarintField(out, 3, uint64(packet.ChannelHash))
	out = appendBytesField(out, 5, packet.Encrypted)
	out = appendFixed32Field(out, 6, packet.ID)
	if packet.HopLimit > 0 {
		out = appendVarintField(out, 9, uint64(packet.HopLimit))
		out = appendVarintField(out, 15, uint64(packet.HopLimit))
	}
	out = appendVarintField(out, 14, 1)
	if len(packet.PublicKey) > 0 {
		out = appendBytesField(out, 16, packet.PublicKey)
	}
	if packet.PKIEncrypted {
		out = appendVarintField(out, 17, 1)
	}
	out = appendVarintField(out, 21, 1)
	return out
}

func DecodeMeshPacket(in []byte) (MeshPacket, error) {
	var packet MeshPacket
	for len(in) > 0 {
		num, typ, n := protowire.ConsumeTag(in)
		if n < 0 {
			return MeshPacket{}, protowire.ParseError(n)
		}
		in = in[n:]
		switch num {
		case 1:
			value, consumed, err := consumeFixed32Field(typ, in)
			if err != nil {
				return MeshPacket{}, fmt.Errorf("mesh.from: %w", err)
			}
			packet.From = value
			in = in[consumed:]
		case 2:
			value, consumed, err := consumeFixed32Field(typ, in)
			if err != nil {
				return MeshPacket{}, fmt.Errorf("mesh.to: %w", err)
			}
			packet.To = value
			in = in[consumed:]
		case 3:
			if typ != protowire.VarintType {
				return MeshPacket{}, errors.New("mesh.channel has invalid wire type")
			}
			value, n := protowire.ConsumeVarint(in)
			if n < 0 {
				return MeshPacket{}, protowire.ParseError(n)
			}
			packet.ChannelHash = uint32(value)
			in = in[n:]
		case 4:
			if typ != protowire.BytesType {
				return MeshPacket{}, errors.New("mesh.decoded has invalid wire type")
			}
			value, n := protowire.ConsumeBytes(in)
			if n < 0 {
				return MeshPacket{}, protowire.ParseError(n)
			}
			packet.Decoded = append([]byte(nil), value...)
			in = in[n:]
		case 5:
			if typ != protowire.BytesType {
				return MeshPacket{}, errors.New("mesh.encrypted has invalid wire type")
			}
			value, n := protowire.ConsumeBytes(in)
			if n < 0 {
				return MeshPacket{}, protowire.ParseError(n)
			}
			packet.Encrypted = append([]byte(nil), value...)
			in = in[n:]
		case 6:
			value, consumed, err := consumeFixed32Field(typ, in)
			if err != nil {
				return MeshPacket{}, fmt.Errorf("mesh.id: %w", err)
			}
			packet.ID = value
			in = in[consumed:]
		case 9:
			if typ != protowire.VarintType {
				return MeshPacket{}, errors.New("mesh.hop_limit has invalid wire type")
			}
			value, n := protowire.ConsumeVarint(in)
			if n < 0 {
				return MeshPacket{}, protowire.ParseError(n)
			}
			packet.HopLimit = uint32(value)
			in = in[n:]
		case 16:
			if typ != protowire.BytesType {
				return MeshPacket{}, errors.New("mesh.public_key has invalid wire type")
			}
			value, n := protowire.ConsumeBytes(in)
			if n < 0 {
				return MeshPacket{}, protowire.ParseError(n)
			}
			packet.PublicKey = append([]byte(nil), value...)
			in = in[n:]
		case 17:
			if typ != protowire.VarintType {
				return MeshPacket{}, errors.New("mesh.pki_encrypted has invalid wire type")
			}
			value, n := protowire.ConsumeVarint(in)
			if n < 0 {
				return MeshPacket{}, protowire.ParseError(n)
			}
			packet.PKIEncrypted = value != 0
			in = in[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, in)
			if n < 0 {
				return MeshPacket{}, protowire.ParseError(n)
			}
			in = in[n:]
		}
	}
	return packet, nil
}

func EncodeServiceEnvelope(packet []byte, channelID string, gatewayID uint32) []byte {
	var out []byte
	out = appendBytesField(out, 1, packet)
	out = appendStringField(out, 2, channelID)
	out = appendStringField(out, 3, FormatNodeID(gatewayID))
	return out
}

func DecodeServiceEnvelope(in []byte) (ServiceEnvelope, error) {
	var envelope ServiceEnvelope
	for len(in) > 0 {
		num, typ, n := protowire.ConsumeTag(in)
		if n < 0 {
			return ServiceEnvelope{}, protowire.ParseError(n)
		}
		in = in[n:]
		switch num {
		case 1:
			if typ != protowire.BytesType {
				return ServiceEnvelope{}, errors.New("envelope.packet has invalid wire type")
			}
			value, n := protowire.ConsumeBytes(in)
			if n < 0 {
				return ServiceEnvelope{}, protowire.ParseError(n)
			}
			envelope.Packet = append([]byte(nil), value...)
			in = in[n:]
		case 2:
			value, consumed, err := consumeStringField(typ, in)
			if err != nil {
				return ServiceEnvelope{}, fmt.Errorf("envelope.channel_id: %w", err)
			}
			envelope.ChannelID = value
			in = in[consumed:]
		case 3:
			value, consumed, err := consumeStringField(typ, in)
			if err != nil {
				return ServiceEnvelope{}, fmt.Errorf("envelope.gateway_id: %w", err)
			}
			envelope.GatewayID = value
			in = in[consumed:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, in)
			if n < 0 {
				return ServiceEnvelope{}, protowire.ParseError(n)
			}
			in = in[n:]
		}
	}
	if len(envelope.Packet) == 0 {
		return ServiceEnvelope{}, errors.New("envelope packet is empty")
	}
	return envelope, nil
}

func appendVarintField(out []byte, fieldNumber protowire.Number, value uint64) []byte {
	out = protowire.AppendTag(out, fieldNumber, protowire.VarintType)
	return protowire.AppendVarint(out, value)
}

func appendBytesField(out []byte, fieldNumber protowire.Number, value []byte) []byte {
	out = protowire.AppendTag(out, fieldNumber, protowire.BytesType)
	return protowire.AppendBytes(out, value)
}

func appendFixed32Field(out []byte, fieldNumber protowire.Number, value uint32) []byte {
	out = protowire.AppendTag(out, fieldNumber, protowire.Fixed32Type)
	return protowire.AppendFixed32(out, value)
}

func appendStringField(out []byte, fieldNumber protowire.Number, value string) []byte {
	return appendBytesField(out, fieldNumber, []byte(value))
}

func consumeFixed32Field(typ protowire.Type, in []byte) (uint32, int, error) {
	if typ != protowire.Fixed32Type {
		return 0, 0, errors.New("invalid wire type")
	}
	value, n := protowire.ConsumeFixed32(in)
	if n < 0 {
		return 0, 0, protowire.ParseError(n)
	}
	return value, n, nil
}

func consumeStringField(typ protowire.Type, in []byte) (string, int, error) {
	if typ != protowire.BytesType {
		return "", 0, errors.New("invalid wire type")
	}
	value, n := protowire.ConsumeBytes(in)
	if n < 0 {
		return "", 0, protowire.ParseError(n)
	}
	return string(value), n, nil
}
