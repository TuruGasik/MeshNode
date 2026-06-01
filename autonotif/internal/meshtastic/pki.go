package meshtastic

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/curve25519"
)

const (
	pkiKeySize       = 32
	pkiTagSize       = 8
	pkiExtraNonceLen = 4
	pkiNonceSize     = 13
	pkiOverhead      = pkiTagSize + pkiExtraNonceLen
)

type PKIKeyPair struct {
	Private []byte
	Public  []byte
}

func LoadOrCreatePKIKey(privateKeyB64, keyFile string) (PKIKeyPair, error) {
	if strings.TrimSpace(privateKeyB64) != "" {
		privateKey, err := decodePKIPrivateKey(privateKeyB64)
		if err != nil {
			return PKIKeyPair{}, err
		}
		return newPKIKeyPair(privateKey)
	}

	if strings.TrimSpace(keyFile) != "" {
		if data, err := os.ReadFile(keyFile); err == nil {
			privateKey, err := decodePKIPrivateKey(string(data))
			if err != nil {
				return PKIKeyPair{}, fmt.Errorf("decode pki key file %s: %w", keyFile, err)
			}
			return newPKIKeyPair(privateKey)
		} else if !errors.Is(err, os.ErrNotExist) {
			return PKIKeyPair{}, fmt.Errorf("read pki key file %s: %w", keyFile, err)
		}
	}

	privateKey, err := GeneratePKIPrivateKey()
	if err != nil {
		return PKIKeyPair{}, err
	}
	if strings.TrimSpace(keyFile) != "" {
		if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
			return PKIKeyPair{}, fmt.Errorf("create pki key directory: %w", err)
		}
		encoded := base64.StdEncoding.EncodeToString(privateKey) + "\n"
		if err := os.WriteFile(keyFile, []byte(encoded), 0o600); err != nil {
			return PKIKeyPair{}, fmt.Errorf("write pki key file %s: %w", keyFile, err)
		}
	}
	return newPKIKeyPair(privateKey)
}

func GeneratePKIPrivateKey() ([]byte, error) {
	privateKey := make([]byte, pkiKeySize)
	if _, err := rand.Read(privateKey); err != nil {
		return nil, fmt.Errorf("generate pki private key: %w", err)
	}
	clampCurve25519PrivateKey(privateKey)
	return privateKey, nil
}

func PublicKeyFromPrivate(privateKey []byte) ([]byte, error) {
	if len(privateKey) != pkiKeySize {
		return nil, fmt.Errorf("invalid pki private key length %d", len(privateKey))
	}
	publicKey, err := curve25519.X25519(privateKey, curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("derive pki public key: %w", err)
	}
	return publicKey, nil
}

func newPKIKeyPair(privateKey []byte) (PKIKeyPair, error) {
	privateKey = append([]byte(nil), privateKey...)
	clampCurve25519PrivateKey(privateKey)
	publicKey, err := PublicKeyFromPrivate(privateKey)
	if err != nil {
		return PKIKeyPair{}, err
	}
	return PKIKeyPair{Private: privateKey, Public: publicKey}, nil
}

func decodePKIPrivateKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	privateKey, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode pki private key: %w", err)
	}
	if len(privateKey) != pkiKeySize {
		return nil, fmt.Errorf("invalid pki private key length %d bytes; want 32", len(privateKey))
	}
	return privateKey, nil
}

func clampCurve25519PrivateKey(privateKey []byte) {
	privateKey[0] &= 248
	privateKey[31] &= 127
	privateKey[31] |= 64
}

func pkiEncrypt(localPrivate, remotePublic []byte, packetID, from uint32, plaintext []byte) ([]byte, error) {
	if len(plaintext)+pkiOverhead > MaxPacketPayloadBytes {
		return nil, fmt.Errorf("pki payload too large: %d bytes", len(plaintext)+pkiOverhead)
	}
	extraNonce := make([]byte, pkiExtraNonceLen)
	if _, err := rand.Read(extraNonce); err != nil {
		return nil, fmt.Errorf("generate pki nonce: %w", err)
	}
	return pkiEncryptWithExtraNonce(localPrivate, remotePublic, packetID, from, plaintext, extraNonce)
}

func pkiEncryptWithExtraNonce(localPrivate, remotePublic []byte, packetID, from uint32, plaintext, extraNonce []byte) ([]byte, error) {
	if len(extraNonce) != pkiExtraNonceLen {
		return nil, fmt.Errorf("invalid pki extra nonce length %d", len(extraNonce))
	}
	key, err := sharedAESKey(localPrivate, remotePublic)
	if err != nil {
		return nil, err
	}
	nonce := pkiNonce(packetID, extraNonce, from)
	ciphertext, tag, err := aesCCMEncrypt(key, nonce, plaintext)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(ciphertext)+pkiOverhead)
	out = append(out, ciphertext...)
	out = append(out, tag...)
	out = append(out, extraNonce...)
	return out, nil
}

func pkiDecrypt(localPrivate, remotePublic []byte, packetID, from uint32, encrypted []byte) ([]byte, error) {
	if len(encrypted) <= pkiOverhead {
		return nil, fmt.Errorf("pki payload too small: %d bytes", len(encrypted))
	}
	extraNonce := encrypted[len(encrypted)-pkiExtraNonceLen:]
	tagStart := len(encrypted) - pkiOverhead
	ciphertext := encrypted[:tagStart]
	tag := encrypted[tagStart : tagStart+pkiTagSize]
	key, err := sharedAESKey(localPrivate, remotePublic)
	if err != nil {
		return nil, err
	}
	nonce := pkiNonce(packetID, extraNonce, from)
	return aesCCMDecrypt(key, nonce, ciphertext, tag)
}

func sharedAESKey(localPrivate, remotePublic []byte) ([]byte, error) {
	if len(localPrivate) != pkiKeySize {
		return nil, fmt.Errorf("invalid local pki private key length %d", len(localPrivate))
	}
	if len(remotePublic) != pkiKeySize {
		return nil, fmt.Errorf("invalid remote pki public key length %d", len(remotePublic))
	}
	shared, err := curve25519.X25519(localPrivate, remotePublic)
	if err != nil {
		return nil, fmt.Errorf("pki x25519: %w", err)
	}
	digest := sha256.Sum256(shared)
	return digest[:], nil
}

func pkiNonce(packetID uint32, extraNonce []byte, from uint32) []byte {
	nonce := make([]byte, pkiNonceSize)
	binary.LittleEndian.PutUint64(nonce[0:8], uint64(packetID))
	copy(nonce[4:8], extraNonce)
	binary.LittleEndian.PutUint32(nonce[8:12], from)
	return nonce
}

func aesCCMEncrypt(key, nonce, plaintext []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	mac := cbcMAC(block, nonce, plaintext)
	s0 := ctrBlock(nonce, 0)
	block.Encrypt(s0, s0)
	tag := make([]byte, pkiTagSize)
	for i := range tag {
		tag[i] = mac[i] ^ s0[i]
	}
	ciphertext := append([]byte(nil), plaintext...)
	ccmCTR(block, nonce, ciphertext)
	return ciphertext, tag, nil
}

func aesCCMDecrypt(key, nonce, ciphertext, tag []byte) ([]byte, error) {
	if len(tag) != pkiTagSize {
		return nil, fmt.Errorf("invalid pki tag length %d", len(tag))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plaintext := append([]byte(nil), ciphertext...)
	ccmCTR(block, nonce, plaintext)
	mac := cbcMAC(block, nonce, plaintext)
	s0 := ctrBlock(nonce, 0)
	block.Encrypt(s0, s0)
	expectedTag := make([]byte, pkiTagSize)
	for i := range expectedTag {
		expectedTag[i] = mac[i] ^ s0[i]
	}
	if !bytes.Equal(expectedTag, tag) {
		return nil, errors.New("pki authentication failed")
	}
	return plaintext, nil
}

func cbcMAC(block cipher.Block, nonce, plaintext []byte) []byte {
	flags := byte(((pkiTagSize - 2) / 2) << 3)
	flags |= byte(2 - 1)

	x := make([]byte, aes.BlockSize)
	b0 := make([]byte, aes.BlockSize)
	b0[0] = flags
	copy(b0[1:14], nonce)
	binary.BigEndian.PutUint16(b0[14:16], uint16(len(plaintext)))
	xorBlock(x, b0)
	block.Encrypt(x, x)

	for offset := 0; offset < len(plaintext); offset += aes.BlockSize {
		var chunk [aes.BlockSize]byte
		copy(chunk[:], plaintext[offset:])
		xorBlock(x, chunk[:])
		block.Encrypt(x, x)
	}
	return x
}

func ccmCTR(block cipher.Block, nonce []byte, data []byte) {
	stream := make([]byte, aes.BlockSize)
	for offset, counter := 0, uint16(1); offset < len(data); offset, counter = offset+aes.BlockSize, counter+1 {
		block.Encrypt(stream, ctrBlock(nonce, counter))
		for i := 0; i < aes.BlockSize && offset+i < len(data); i++ {
			data[offset+i] ^= stream[i]
		}
	}
}

func ctrBlock(nonce []byte, counter uint16) []byte {
	out := make([]byte, aes.BlockSize)
	out[0] = 1
	copy(out[1:14], nonce)
	binary.BigEndian.PutUint16(out[14:16], counter)
	return out
}

func xorBlock(dst, src []byte) {
	for i := 0; i < aes.BlockSize; i++ {
		dst[i] ^= src[i]
	}
}
