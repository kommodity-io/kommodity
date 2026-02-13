package kms_test

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/kms"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()

	key := make([]byte, kms.KeySize)

	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	plaintext := []byte("this is a secret LUKS key")
	aad := []byte("some-uuid-value" + string(make([]byte, kms.AADNonceSize)) + "10.0.0.1")

	ciphertext, err := kms.Encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	decrypted, err := kms.Decrypt(key, ciphertext, aad)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatalf("decrypted data does not match plaintext: got %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptWithWrongAADFails(t *testing.T) {
	t.Parallel()

	key := make([]byte, kms.KeySize)

	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	plaintext := []byte("this is a secret LUKS key")
	aad := []byte("correct-aad-data")

	ciphertext, err := kms.Encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	wrongAAD := []byte("wrong-aad-data")

	_, err = kms.Decrypt(key, ciphertext, wrongAAD)
	if err == nil {
		t.Fatal("decrypt should have failed with wrong AAD")
	}
}

func TestDecryptWithNilAADFailsWhenEncryptedWithAAD(t *testing.T) {
	t.Parallel()

	key := make([]byte, kms.KeySize)

	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	plaintext := []byte("this is a secret LUKS key")
	aad := []byte("some-aad")

	ciphertext, err := kms.Encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	_, err = kms.Decrypt(key, ciphertext, nil)
	if err == nil {
		t.Fatal("decrypt should have failed with nil AAD when encrypted with AAD")
	}
}

func TestEncryptDecryptWithNilAAD(t *testing.T) {
	t.Parallel()

	key := make([]byte, kms.KeySize)

	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	plaintext := []byte("this is a secret LUKS key")

	ciphertext, err := kms.Encrypt(key, plaintext, nil)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	decrypted, err := kms.Decrypt(key, ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatalf("decrypted data does not match plaintext: got %q, want %q", decrypted, plaintext)
	}
}

func TestBuildAADDeterminism(t *testing.T) {
	t.Parallel()

	nodeUUID := "550e8400-e29b-41d4-a716-446655440000"

	nonce := make([]byte, kms.AADNonceSize)
	for i := range nonce {
		nonce[i] = byte(i)
	}

	peerIP := "10.0.0.1"

	aad1 := kms.BuildAAD(nodeUUID, nonce, peerIP)
	aad2 := kms.BuildAAD(nodeUUID, nonce, peerIP)

	if !bytes.Equal(aad1, aad2) {
		t.Fatal("buildAAD should be deterministic for the same inputs")
	}
}

func TestBuildAADUniqueness(t *testing.T) {
	t.Parallel()

	nodeUUID := "550e8400-e29b-41d4-a716-446655440000"
	peerIP := "10.0.0.1"

	nonce1 := make([]byte, kms.AADNonceSize)
	nonce2 := make([]byte, kms.AADNonceSize)
	nonce2[0] = 1

	aad1 := kms.BuildAAD(nodeUUID, nonce1, peerIP)
	aad2 := kms.BuildAAD(nodeUUID, nonce2, peerIP)

	if bytes.Equal(aad1, aad2) {
		t.Fatal("buildAAD should produce different output for different nonces")
	}

	aad3 := kms.BuildAAD(nodeUUID, nonce1, "10.0.0.2")

	if bytes.Equal(aad1, aad3) {
		t.Fatal("buildAAD should produce different output for different IPs")
	}

	aad4 := kms.BuildAAD("660e8400-e29b-41d4-a716-446655440000", nonce1, peerIP)

	if bytes.Equal(aad1, aad4) {
		t.Fatal("buildAAD should produce different output for different UUIDs")
	}
}

func TestDecryptCiphertextTooShort(t *testing.T) {
	t.Parallel()

	key := make([]byte, kms.KeySize)

	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	_, err = kms.Decrypt(key, []byte("short"), nil)
	if err == nil {
		t.Fatal("decrypt should fail for short ciphertext")
	}
}

func TestParseVolumeKeySets(t *testing.T) {
	t.Parallel()

	secretData := map[string][]byte{
		kms.SealedFromIPKey:                     []byte("10.0.0.1"),
		"a1b2c3d4" + kms.KeySuffix:              make([]byte, kms.KeySize),
		"a1b2c3d4" + kms.NonceSuffix:            make([]byte, kms.AADNonceSize),
		"a1b2c3d4" + kms.LuksKeySuffix:           []byte("encrypted-data-1"),
		"e5f6a7b8" + kms.KeySuffix:              make([]byte, kms.KeySize),
		"e5f6a7b8" + kms.NonceSuffix:            make([]byte, kms.AADNonceSize),
		"e5f6a7b8" + kms.LuksKeySuffix:           []byte("encrypted-data-2"),
	}

	sets := kms.ParseVolumeKeySets(secretData)
	if len(sets) != 2 {
		t.Fatalf("expected 2 volume key sets, got %d", len(sets))
	}
}

func TestParseVolumeKeySetsSkipsIncomplete(t *testing.T) {
	t.Parallel()

	secretData := map[string][]byte{
		kms.SealedFromIPKey:                   []byte("10.0.0.1"),
		"complete" + kms.KeySuffix:            make([]byte, kms.KeySize),
		"complete" + kms.NonceSuffix:          make([]byte, kms.AADNonceSize),
		"complete" + kms.LuksKeySuffix:         []byte("encrypted-data"),
		// incomplete: missing nonce and luksKey
		"incomplete" + kms.KeySuffix: make([]byte, kms.KeySize),
	}

	sets := kms.ParseVolumeKeySets(secretData)
	if len(sets) != 1 {
		t.Fatalf("expected 1 volume key set, got %d", len(sets))
	}
}
