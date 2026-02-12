package kms

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	plaintext := []byte("this is a secret LUKS key")
	aad := []byte("some-uuid-value" + string(make([]byte, aadNonceSize)) + "10.0.0.1")

	ciphertext, err := encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	decrypted, err := decrypt(key, ciphertext, aad)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatalf("decrypted data does not match plaintext: got %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptWithWrongAADFails(t *testing.T) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	plaintext := []byte("this is a secret LUKS key")
	aad := []byte("correct-aad-data")

	ciphertext, err := encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	wrongAAD := []byte("wrong-aad-data")

	_, err = decrypt(key, ciphertext, wrongAAD)
	if err == nil {
		t.Fatal("decrypt should have failed with wrong AAD")
	}
}

func TestDecryptWithNilAADFailsWhenEncryptedWithAAD(t *testing.T) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	plaintext := []byte("this is a secret LUKS key")
	aad := []byte("some-aad")

	ciphertext, err := encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	_, err = decrypt(key, ciphertext, nil)
	if err == nil {
		t.Fatal("decrypt should have failed with nil AAD when encrypted with AAD")
	}
}

func TestEncryptDecryptWithNilAAD(t *testing.T) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	plaintext := []byte("this is a secret LUKS key")

	ciphertext, err := encrypt(key, plaintext, nil)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	decrypted, err := decrypt(key, ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatalf("decrypted data does not match plaintext: got %q, want %q", decrypted, plaintext)
	}
}

func TestBuildAADDeterminism(t *testing.T) {
	nodeUUID := "550e8400-e29b-41d4-a716-446655440000"
	nonce := make([]byte, aadNonceSize)
	for i := range nonce {
		nonce[i] = byte(i)
	}

	peerIP := "10.0.0.1"

	aad1 := buildAAD(nodeUUID, nonce, peerIP)
	aad2 := buildAAD(nodeUUID, nonce, peerIP)

	if !bytes.Equal(aad1, aad2) {
		t.Fatal("buildAAD should be deterministic for the same inputs")
	}
}

func TestBuildAADUniqueness(t *testing.T) {
	nodeUUID := "550e8400-e29b-41d4-a716-446655440000"
	peerIP := "10.0.0.1"

	nonce1 := make([]byte, aadNonceSize)
	nonce2 := make([]byte, aadNonceSize)
	nonce2[0] = 1

	aad1 := buildAAD(nodeUUID, nonce1, peerIP)
	aad2 := buildAAD(nodeUUID, nonce2, peerIP)

	if bytes.Equal(aad1, aad2) {
		t.Fatal("buildAAD should produce different output for different nonces")
	}

	aad3 := buildAAD(nodeUUID, nonce1, "10.0.0.2")

	if bytes.Equal(aad1, aad3) {
		t.Fatal("buildAAD should produce different output for different IPs")
	}

	aad4 := buildAAD("660e8400-e29b-41d4-a716-446655440000", nonce1, peerIP)

	if bytes.Equal(aad1, aad4) {
		t.Fatal("buildAAD should produce different output for different UUIDs")
	}
}

func TestDecryptCiphertextTooShort(t *testing.T) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	_, err := decrypt(key, []byte("short"), nil)
	if err == nil {
		t.Fatal("decrypt should fail for short ciphertext")
	}
}

func TestParseVolumeKeySets(t *testing.T) {
	secretData := map[string][]byte{
		sealedFromIPKey:          []byte("10.0.0.1"),
		"a1b2c3d4" + keySuffix:   make([]byte, keySize),
		"a1b2c3d4" + nonceSuffix: make([]byte, aadNonceSize),
		"a1b2c3d4" + luksKeySuffix: []byte("encrypted-data-1"),
		"e5f6a7b8" + keySuffix:   make([]byte, keySize),
		"e5f6a7b8" + nonceSuffix: make([]byte, aadNonceSize),
		"e5f6a7b8" + luksKeySuffix: []byte("encrypted-data-2"),
	}

	sets := parseVolumeKeySets(secretData)
	if len(sets) != 2 {
		t.Fatalf("expected 2 volume key sets, got %d", len(sets))
	}

	prefixes := map[string]bool{}
	for _, s := range sets {
		prefixes[s.prefix] = true
	}

	if !prefixes["a1b2c3d4"] || !prefixes["e5f6a7b8"] {
		t.Fatalf("unexpected prefixes: %v", prefixes)
	}
}

func TestParseVolumeKeySetsSkipsIncomplete(t *testing.T) {
	secretData := map[string][]byte{
		sealedFromIPKey:          []byte("10.0.0.1"),
		"complete" + keySuffix:   make([]byte, keySize),
		"complete" + nonceSuffix: make([]byte, aadNonceSize),
		"complete" + luksKeySuffix: []byte("encrypted-data"),
		// incomplete: missing nonce and luksKey
		"incomplete" + keySuffix: make([]byte, keySize),
	}

	sets := parseVolumeKeySets(secretData)
	if len(sets) != 1 {
		t.Fatalf("expected 1 volume key set, got %d", len(sets))
	}

	if sets[0].prefix != "complete" {
		t.Fatalf("expected prefix 'complete', got %q", sets[0].prefix)
	}
}
