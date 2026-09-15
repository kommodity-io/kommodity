package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

const (
	// RSAKeySize is the size of the RSA key to generate.
	RSAKeySize = 4096
	// SigningKeyDataKey is the key in the Secret data that stores the private key PEM.
	SigningKeyDataKey = "key"
	// SigningKeyLabel is the label on the signing key Secret.
	SigningKeyLabel = "kommodity.io/managed-by"
	// SigningKeyValue is the value for the signing key label.
	SigningKeyValue = "libkapi"
)

// GenerateRSAPrivateKey generates a new RSA private key.
func GenerateRSAPrivateKey() (*rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, RSAKeySize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key: %w", err)
	}

	return key, nil
}

// ConvertRSAKeyToPEM converts an RSA private key to PEM format.
func ConvertRSAKeyToPEM(key *rsa.PrivateKey) []byte {
	keyBytes := x509.MarshalPKCS1PrivateKey(key)

	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keyBytes,
	})
}

// ConvertPEMToRSAKey converts PEM-encoded bytes to an RSA private key.
func ConvertPEMToRSAKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, ErrPEMDecodeFailed
	}

	rsaKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		parsedKey, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("%w: %w, %w", ErrKeyParseFailed, err, err2)
		}

		var success bool

		rsaKey, success = parsedKey.(*rsa.PrivateKey)
		if !success {
			return nil, ErrKeyNotRSA
		}
	}

	return rsaKey, nil
}

// CreateSigningKeySecret creates a new Secret with the signing key.
// Returns an IsAlreadyExists error if the Secret already exists.
// The namespace is created if it doesn't exist.
func CreateSigningKeySecret(
	ctx context.Context,
	client corev1client.CoreV1Interface,
	key *rsa.PrivateKey,
	namespace string,
	secretName string,
) error {
	err := EnsureNamespace(ctx, client, namespace)
	if err != nil {
		return err
	}

	keyPEM := ConvertRSAKeyToPEM(key)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				SigningKeyLabel: SigningKeyValue,
			},
		},
		Data: map[string][]byte{
			SigningKeyDataKey: keyPEM,
		},
		Type: corev1.SecretTypeOpaque,
	}

	_, err = client.Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create signing key secret: %w", err)
	}

	return nil
}

// LoadSigningKeyFromSecret loads the signing key from an existing Secret.
func LoadSigningKeyFromSecret(
	ctx context.Context,
	client corev1client.CoreV1Interface,
	namespace string,
	secretName string,
) (*rsa.PrivateKey, error) {
	secret, err := client.Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get signing key secret: %w", err)
	}

	keyPEM, ok := secret.Data[SigningKeyDataKey]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrSigningKeyDataMissing, SigningKeyDataKey)
	}

	return ConvertPEMToRSAKey(keyPEM)
}

// LoadOrCreateSigningKey loads the signing key from a Kubernetes Secret if
// it exists, or generates a new key and creates the Secret if it doesn't.
// This ensures the key persists across restarts without being rotated.
//
// Must be called after the server is listening (it uses the loopback client).
func LoadOrCreateSigningKey(
	ctx context.Context,
	client corev1client.CoreV1Interface,
	keyPersistence *KeyPersistenceConfig,
	systemNamespace string,
) (*rsa.PrivateKey, error) {
	namespace := ResolveSigningKeyNamespace(keyPersistence, systemNamespace)
	secretName := ResolveSigningKeySecretName(keyPersistence)

	// Try to load from existing Secret.
	key, err := LoadSigningKeyFromSecret(ctx, client, namespace, secretName)
	if err == nil {
		return key, nil
	}

	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to load signing key from secret: %w", err)
	}

	// Secret doesn't exist — generate a new key.
	newKey, err := GenerateRSAPrivateKey()
	if err != nil {
		return nil, err
	}

	// Create the Secret. If a race caused it to be created between our
	// load and create, fall back to loading the existing key.
	err = CreateSigningKeySecret(ctx, client, newKey, namespace, secretName)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return LoadSigningKeyFromSecret(ctx, client, namespace, secretName)
		}

		return nil, err
	}

	return newKey, nil
}

// PersistSigningKey persists the signing key to a Kubernetes Secret,
// creating or updating it. Used by the signing key rotation hook when
// the Secret is deleted and needs to be recreated.
func PersistSigningKey(
	ctx context.Context,
	client corev1client.CoreV1Interface,
	key *rsa.PrivateKey,
	namespace string,
	secretName string,
) error {
	err := EnsureNamespace(ctx, client, namespace)
	if err != nil {
		return err
	}

	keyPEM := ConvertRSAKeyToPEM(key)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				SigningKeyLabel: SigningKeyValue,
			},
		},
		Data: map[string][]byte{
			SigningKeyDataKey: keyPEM,
		},
		Type: corev1.SecretTypeOpaque,
	}

	_, err = client.Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create signing key secret: %w", err)
		}

		// Secret already exists (race) — update it with our key.
		existingSecret, err := client.Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get existing signing key secret: %w", err)
		}

		existingSecret.Data[SigningKeyDataKey] = keyPEM

		_, err = client.Secrets(namespace).Update(ctx, existingSecret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update signing key secret: %w", err)
		}
	}

	return nil
}

// EnsureNamespace creates the namespace if it doesn't already exist.
func EnsureNamespace(ctx context.Context, client corev1client.CoreV1Interface, namespace string) error {
	_, err := client.Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create namespace %q: %w", namespace, err)
	}

	return nil
}
