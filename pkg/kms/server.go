// Package kms implements the Talos Linux KMS service, which provides
// a networked key management system for full disk encryption.
//
// This package includes a mock implementation of the gRPC server for
// the SideroLabs KMS API.
package kms

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strings"

	"github.com/google/uuid"
	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/siderolabs/kms-client/api/kms"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoclientset "k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

const (
	//nolint:gosec // this is just a prefix for KMS secrets
	secretPrefix     = "talos-kms"
	keySize          = 32 // 256 bits
	aadNonceSize     = 32 // 256-bit random nonce for AAD
	volumePrefixSize = 8  // 8 random bytes → 16 hex chars for volume group prefix

	sealedFromIPKey = "sealedFromIP"
	keySuffix       = ".key"
	nonceSuffix     = ".nonce"
	luksKeySuffix   = ".luksKey"
)

// volumeKeySet holds the key material for a single volume group.
type volumeKeySet struct {
	prefix        string
	encryptionKey []byte
	aadNonce      []byte
	luksKey       []byte
}

// ServiceServer is a struct that implements the ServiceServer interface.
type ServiceServer struct {
	kms.UnimplementedKMSServiceServer

	config *config.KommodityConfig
}

// Seal is a method that encrypts data using the KMS service.
func (s *ServiceServer) Seal(ctx context.Context, req *kms.Request) (*kms.Response, error) {
	nodeUUID, err := validateNodeUUID(req)
	if err != nil {
		return nil, fmt.Errorf("failed to validate node UUID: %w", err)
	}

	data := req.GetData()
	if len(data) == 0 {
		//nolint:wrapcheck // we want a gRPC status error here
		return nil, status.Error(codes.InvalidArgument, "data is required")
	}

	peerIP, err := extractPeerIP(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to extract peer IP: %w", err)
	}

	kubeClient, err := clientgoclientset.NewForConfig(s.config.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	secretName := fmt.Sprintf("%s-%s", secretPrefix, nodeUUID)

	secret, err := getSecretsAPI(kubeClient).Get(ctx, secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		encryptedData, createErr := createNodeSecret(ctx, kubeClient, nodeUUID, peerIP, data)
		if createErr != nil {
			return nil, fmt.Errorf("failed to create node secret: %w", createErr)
		}

		return &kms.Response{Data: encryptedData}, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to get secret %s: %w", secretName, err)
	}

	encryptedData, err := addVolumeToSecret(ctx, kubeClient, secret, nodeUUID, peerIP, data)
	if err != nil {
		return nil, fmt.Errorf("failed to add volume to secret: %w", err)
	}

	return &kms.Response{Data: encryptedData}, nil
}

// Unseal is a method that decrypts data using the KMS service.
func (s *ServiceServer) Unseal(ctx context.Context, req *kms.Request) (*kms.Response, error) {
	nodeUUID, err := validateNodeUUID(req)
	if err != nil {
		return nil, fmt.Errorf("failed to validate node UUID: %w", err)
	}

	data := req.GetData()
	if len(data) == 0 {
		//nolint:wrapcheck // we want a gRPC status error here
		return nil, status.Error(codes.InvalidArgument, "data is required")
	}

	peerIP, err := extractPeerIP(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to extract peer IP: %w", err)
	}

	kubeClient, err := clientgoclientset.NewForConfig(s.config.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	secretName := fmt.Sprintf("%s-%s", secretPrefix, nodeUUID)

	secret, err := getSecretsAPI(kubeClient).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %s: %w", secretName, err)
	}

	storedIP := string(secret.Data[sealedFromIPKey])
	if storedIP != peerIP {
		return nil, status.Errorf(codes.PermissionDenied,
			"%v: sealed from %q, unseal requested from %q", ErrIPMismatch, storedIP, peerIP)
	}

	keySets := parseVolumeKeySets(secret.Data)
	if len(keySets) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoVolumeKeySets, secretName)
	}

	for _, ks := range keySets {
		aad := buildAAD(nodeUUID, ks.aadNonce, peerIP)

		decryptedData, decErr := decrypt(ks.encryptionKey, data, aad)
		if decErr == nil {
			return &kms.Response{Data: decryptedData}, nil
		}
	}

	return nil, fmt.Errorf("%w: secret %s", ErrNoMatchingSecret, secretName)
}

// NewGRPCServerFactory returns an initializer function that initializes the mock KMS service.
func NewGRPCServerFactory(cfg *config.KommodityConfig) combinedserver.GRPCServerFactory {
	return func(srv *grpc.Server) error {
		// Create a new KMS service server and register it with the gRPC server.
		kms.RegisterKMSServiceServer(srv, &ServiceServer{config: cfg})

		return nil
	}
}

func createNodeSecret(ctx context.Context,
	kubeClient *clientgoclientset.Clientset,
	nodeUUID, peerIP string,
	plaintext []byte,
) ([]byte, error) {
	prefix, err := generateVolumePrefix()
	if err != nil {
		return nil, fmt.Errorf("failed to generate volume prefix: %w", err)
	}

	key := make([]byte, keySize)

	_, err = rand.Read(key)
	if err != nil {
		return nil, fmt.Errorf("failed to generate encryption key: %w", err)
	}

	aadNonce := make([]byte, aadNonceSize)

	_, err = rand.Read(aadNonce)
	if err != nil {
		return nil, fmt.Errorf("failed to generate AAD nonce: %w", err)
	}

	aad := buildAAD(nodeUUID, aadNonce, peerIP)

	encryptedData, err := encrypt(key, plaintext, aad)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt data: %w", err)
	}

	secretName := fmt.Sprintf("%s-%s", secretPrefix, nodeUUID)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: config.KommodityNamespace,
			Labels:    config.GetKommodityLabels(nodeUUID, peerIP),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			sealedFromIPKey:        []byte(peerIP),
			prefix + keySuffix:     key,
			prefix + nonceSuffix:   aadNonce,
			prefix + luksKeySuffix: encryptedData,
		},
	}

	_, err = getSecretsAPI(kubeClient).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create secret %s: %w", secretName, err)
	}

	return encryptedData, nil
}

func addVolumeToSecret(ctx context.Context,
	kubeClient *clientgoclientset.Clientset,
	secret *corev1.Secret,
	nodeUUID, peerIP string,
	plaintext []byte,
) ([]byte, error) {
	storedIP := string(secret.Data[sealedFromIPKey])
	if storedIP != peerIP {
		return nil, status.Errorf(codes.PermissionDenied,
			"%v: sealed from %q, seal requested from %q", ErrIPMismatch, storedIP, peerIP)
	}

	prefix, err := generateVolumePrefix()
	if err != nil {
		return nil, fmt.Errorf("failed to generate volume prefix: %w", err)
	}

	key := make([]byte, keySize)

	_, err = rand.Read(key)
	if err != nil {
		return nil, fmt.Errorf("failed to generate encryption key: %w", err)
	}

	aadNonce := make([]byte, aadNonceSize)

	_, err = rand.Read(aadNonce)
	if err != nil {
		return nil, fmt.Errorf("failed to generate AAD nonce: %w", err)
	}

	aad := buildAAD(nodeUUID, aadNonce, peerIP)

	encryptedData, err := encrypt(key, plaintext, aad)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt data: %w", err)
	}

	secret.Data[prefix+keySuffix] = key
	secret.Data[prefix+nonceSuffix] = aadNonce
	secret.Data[prefix+luksKeySuffix] = encryptedData

	_, err = getSecretsAPI(kubeClient).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to update secret %s: %w", secret.Name, err)
	}

	return encryptedData, nil
}

func buildAAD(nodeUUID string, aadNonce []byte, peerIP string) []byte {
	aad := make([]byte, 0, len(nodeUUID)+len(aadNonce)+len(peerIP))
	aad = append(aad, []byte(nodeUUID)...)
	aad = append(aad, aadNonce...)
	aad = append(aad, []byte(peerIP)...)

	return aad
}

func extractPeerIP(ctx context.Context) (string, error) {
	client, ok := peer.FromContext(ctx)
	if !ok {
		return "", ErrEmptyClientContext
	}

	host, _, err := net.SplitHostPort(client.Addr.String())
	if err != nil {
		return "", fmt.Errorf("failed to extract client IP: %w", err)
	}

	return host, nil
}

func generateVolumePrefix() (string, error) {
	randBytes := make([]byte, volumePrefixSize)

	_, err := rand.Read(randBytes)
	if err != nil {
		return "", fmt.Errorf("failed to generate volume prefix: %w", err)
	}

	return hex.EncodeToString(randBytes), nil
}

func parseVolumeKeySets(secretData map[string][]byte) []volumeKeySet {
	sets := make([]volumeKeySet, 0, len(secretData))
	seen := make(map[string]bool)

	for k := range secretData {
		if !strings.HasSuffix(k, keySuffix) {
			continue
		}

		prefix := strings.TrimSuffix(k, keySuffix)
		if prefix == "" || seen[prefix] {
			continue
		}

		seen[prefix] = true

		encKey := secretData[prefix+keySuffix]
		nonce := secretData[prefix+nonceSuffix]
		luksKey := secretData[prefix+luksKeySuffix]

		if len(encKey) != keySize || len(nonce) != aadNonceSize || len(luksKey) == 0 {
			continue
		}

		sets = append(sets, volumeKeySet{
			prefix:        prefix,
			encryptionKey: encKey,
			aadNonce:      nonce,
			luksKey:       luksKey,
		})
	}

	return sets
}

func getSecretsAPI(kubeClient *clientgoclientset.Clientset) v1.SecretInterface {
	return kubeClient.CoreV1().Secrets(config.KommodityNamespace)
}

func validateNodeUUID(req *kms.Request) (string, error) {
	nodeUUID := strings.ToLower(req.GetNodeUuid())
	if nodeUUID == "" {
		//nolint:wrapcheck // we want a gRPC status error here
		return "", status.Error(codes.InvalidArgument, "node_uuid is required")
	}

	_, err := uuid.Parse(nodeUUID)
	if err != nil {
		//nolint:wrapcheck // we want a gRPC status error here
		return "", status.Error(codes.InvalidArgument, "bad node_uuid")
	}

	return nodeUUID, nil
}
