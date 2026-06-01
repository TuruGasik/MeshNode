package meshtastic

import "testing"

func TestEncodeDecodeData(t *testing.T) {
	encoded := EncodeData(TextMessagePort, []byte("ping"), true)
	decoded, err := DecodeData(encoded)
	if err != nil {
		t.Fatalf("DecodeData() error = %v", err)
	}
	if decoded.PortNum != TextMessagePort {
		t.Fatalf("PortNum = %d", decoded.PortNum)
	}
	if string(decoded.Payload) != "ping" {
		t.Fatalf("Payload = %q", decoded.Payload)
	}
	if !decoded.WantResponse {
		t.Fatal("WantResponse = false")
	}
}

func TestEncodeDecodeServiceEnvelopeAndMeshPacket(t *testing.T) {
	publicKey := bytesOf(32, 0x7a)
	mesh := EncodeMeshPacketPacket(MeshPacket{
		From:         0x11111111,
		To:           0x22222222,
		ID:           0x33333333,
		ChannelHash:  42,
		HopLimit:     3,
		Encrypted:    []byte("ciphertext"),
		PublicKey:    publicKey,
		PKIEncrypted: true,
	})
	envelope := EncodeServiceEnvelope(mesh, "GempaBumi", 0x11111111)

	decodedEnvelope, err := DecodeServiceEnvelope(envelope)
	if err != nil {
		t.Fatalf("DecodeServiceEnvelope() error = %v", err)
	}
	if decodedEnvelope.ChannelID != "GempaBumi" {
		t.Fatalf("ChannelID = %q", decodedEnvelope.ChannelID)
	}
	if decodedEnvelope.GatewayID != "!11111111" {
		t.Fatalf("GatewayID = %q", decodedEnvelope.GatewayID)
	}

	decodedPacket, err := DecodeMeshPacket(decodedEnvelope.Packet)
	if err != nil {
		t.Fatalf("DecodeMeshPacket() error = %v", err)
	}
	if decodedPacket.From != 0x11111111 || decodedPacket.To != 0x22222222 || decodedPacket.ID != 0x33333333 {
		t.Fatalf("decoded packet mismatch: %+v", decodedPacket)
	}
	if decodedPacket.ChannelHash != 42 {
		t.Fatalf("ChannelHash = %d", decodedPacket.ChannelHash)
	}
	if decodedPacket.HopLimit != 3 {
		t.Fatalf("HopLimit = %d", decodedPacket.HopLimit)
	}
	if string(decodedPacket.Encrypted) != "ciphertext" {
		t.Fatalf("Encrypted = %q", decodedPacket.Encrypted)
	}
	if !decodedPacket.PKIEncrypted {
		t.Fatal("PKIEncrypted = false")
	}
	if string(decodedPacket.PublicKey) != string(publicKey) {
		t.Fatalf("PublicKey = %x", decodedPacket.PublicKey)
	}
}

func TestEncodeDecodeUserPublicKey(t *testing.T) {
	publicKey := bytesOf(32, 0x42)
	encoded := EncodeUser(0x77727342, "MeshNode WRS", "-GB-", 255, 0, publicKey)
	decoded, err := DecodeUser(encoded)
	if err != nil {
		t.Fatalf("DecodeUser() error = %v", err)
	}
	if decoded.ID != "!77727342" || decoded.LongName != "MeshNode WRS" || decoded.ShortName != "-GB-" {
		t.Fatalf("decoded user mismatch: %+v", decoded)
	}
	if decoded.HardwareModel != 255 || decoded.Role != 0 {
		t.Fatalf("decoded user model/role mismatch: %+v", decoded)
	}
	if decoded.IsUnmessagable {
		t.Fatal("IsUnmessagable = true")
	}
	if string(decoded.PublicKey) != string(publicKey) {
		t.Fatalf("PublicKey = %x", decoded.PublicKey)
	}
}

func TestDecodeServiceEnvelopeRejectsEmptyPacket(t *testing.T) {
	_, err := DecodeServiceEnvelope(EncodeServiceEnvelope(nil, "GempaBumi", 0x11111111))
	if err == nil {
		t.Fatal("DecodeServiceEnvelope() expected error for empty packet")
	}
}

func bytesOf(n int, value byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = value
	}
	return out
}
