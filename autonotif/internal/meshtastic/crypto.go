package meshtastic

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
)

var defaultMeshtasticKey = []byte{0xd4, 0xf1, 0xbb, 0x3a, 0x20, 0x29, 0x07, 0x59, 0xf0, 0xbc, 0xff, 0xab, 0xcf, 0x4e, 0x69, 0x01}

// channelCipher applies AES-CTR with the Meshtastic-style nonce derived from
// packet ID and source node. Encryption and decryption use the same operation.
func channelCipher(block cipher.Block, packetID, from uint32, in []byte) []byte {
	out := make([]byte, len(in))
	nonce := make([]byte, 16)
	binary.LittleEndian.PutUint32(nonce[0:], packetID)
	binary.LittleEndian.PutUint32(nonce[8:], from)
	cipher.NewCTR(block, nonce).XORKeyStream(out, in)
	return out
}

// newChannelCipher returns a block cipher initialised with the channel PSK.
func newChannelCipher(privateKeyB64 string) (cipher.Block, []byte, error) {
	key, err := decodeMeshtasticKey(privateKeyB64)
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	return block, key, nil
}

func decodeMeshtasticKey(b64 string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}
	switch len(key) {
	case 1:
		if key[0] == 0x00 {
			return nil, errors.New("unencrypted Meshtastic PSK marker is not supported")
		}
		expanded := append([]byte(nil), defaultMeshtasticKey...)
		expanded[len(expanded)-1] += key[0] - 1
		return expanded, nil
	case 16, 24, 32:
		return key, nil
	default:
		return nil, fmt.Errorf("invalid key length %d bytes; want simple PSK marker or AES-128/192/256 key", len(key))
	}
}

func channelHash(channelName string, key []byte) uint32 {
	return uint32(xorHash([]byte(channelName)) ^ xorHash(key))
}

func xorHash(data []byte) byte {
	var result byte
	for _, b := range data {
		result ^= b
	}
	return result
}
