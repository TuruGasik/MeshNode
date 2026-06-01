package meshtastic

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestPKIEncryptDecryptRoundTrip(t *testing.T) {
	alicePrivate, err := GeneratePKIPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	bobPrivate, err := GeneratePKIPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	alicePublic, err := PublicKeyFromPrivate(alicePrivate)
	if err != nil {
		t.Fatal(err)
	}
	bobPublic, err := PublicKeyFromPrivate(bobPrivate)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := EncodeData(TextMessagePort, []byte("ping"), true)
	extraNonce := []byte{1, 2, 3, 4}
	encrypted, err := pkiEncryptWithExtraNonce(alicePrivate, bobPublic, 0x01020304, 0x11111111, plaintext, extraNonce)
	if err != nil {
		t.Fatalf("pkiEncryptWithExtraNonce() error = %v", err)
	}
	if len(encrypted) != len(plaintext)+pkiOverhead {
		t.Fatalf("encrypted length = %d, want %d", len(encrypted), len(plaintext)+pkiOverhead)
	}

	decrypted, err := pkiDecrypt(bobPrivate, alicePublic, 0x01020304, 0x11111111, encrypted)
	if err != nil {
		t.Fatalf("pkiDecrypt() error = %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted = %x, want %x", decrypted, plaintext)
	}
}

func TestPKIDecryptMeshtasticFirmwareVector(t *testing.T) {
	privateKey := mustHex(t, "a00330633e63522f8a4d81ec6d9d1e6617f6c8ffd3a4c698229537d44e522277")
	publicKey := mustHex(t, "db18fc50eea47f00251cb784819a3cf5fc361882597f589f0d7ff820e8064457")
	encrypted := mustHex(t, "40df24abfcc30a17a3d9046726099e796a1c036a792b")
	want := mustHex(t, "08011204746573744800")

	decrypted, err := pkiDecrypt(privateKey, publicKey, 0x13b2d662, 0x00000929, encrypted)
	if err != nil {
		t.Fatalf("pkiDecrypt() error = %v", err)
	}
	if !bytes.Equal(decrypted, want) {
		t.Fatalf("decrypted = %x, want %x", decrypted, want)
	}
}

func TestPKIDecryptRejectsTamperedCiphertext(t *testing.T) {
	alicePrivate, _ := GeneratePKIPrivateKey()
	bobPrivate, _ := GeneratePKIPrivateKey()
	alicePublic, _ := PublicKeyFromPrivate(alicePrivate)
	bobPublic, _ := PublicKeyFromPrivate(bobPrivate)

	encrypted, err := pkiEncryptWithExtraNonce(alicePrivate, bobPublic, 1, 2, []byte("hello"), []byte{5, 6, 7, 8})
	if err != nil {
		t.Fatal(err)
	}
	encrypted[0] ^= 0xff
	if _, err := pkiDecrypt(bobPrivate, alicePublic, 1, 2, encrypted); err == nil {
		t.Fatal("pkiDecrypt() expected authentication error")
	}
}

func TestLoadOrCreatePKIKeyPersistsKey(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "meshtastic-pki.key")
	first, err := LoadOrCreatePKIKey("", keyFile)
	if err != nil {
		t.Fatalf("LoadOrCreatePKIKey() create error = %v", err)
	}
	if len(first.Private) != pkiKeySize || len(first.Public) != pkiKeySize {
		t.Fatalf("invalid key sizes: %+v", first)
	}
	info, err := os.Stat(keyFile)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %o, want 600", info.Mode().Perm())
	}

	second, err := LoadOrCreatePKIKey("", keyFile)
	if err != nil {
		t.Fatalf("LoadOrCreatePKIKey() load error = %v", err)
	}
	if !bytes.Equal(first.Private, second.Private) || !bytes.Equal(first.Public, second.Public) {
		t.Fatal("persisted key changed after reload")
	}
}

func TestLoadOrCreatePKIKeyUsesEnvKey(t *testing.T) {
	privateKey, err := GeneratePKIPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(privateKey)
	pair, err := LoadOrCreatePKIKey(encoded, filepath.Join(t.TempDir(), "ignored.key"))
	if err != nil {
		t.Fatalf("LoadOrCreatePKIKey() env error = %v", err)
	}
	if !bytes.Equal(pair.Private, privateKey) {
		t.Fatal("private key mismatch")
	}
}

func mustHex(t *testing.T, value string) []byte {
	t.Helper()
	out, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
