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
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
	knet "github.com/kommodity-io/kommodity/pkg/net"
	"github.com/siderolabs/kms-client/api/kms"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	clientgoclientset "k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	ctrlclint "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	keySize          = 32 // 256 bits
	aadNonceSize     = 32 // 256-bit random nonce for AAD
	volumePrefixSize = 8  // 8 random bytes → 16 hex chars for volume group prefix

	sealedFromIPKey = "sealedFromIP"
	keySuffix       = ".key"
	nonceSuffix     = ".nonce"
	luksKeySuffix   = ".luksKey"

	// secretNameFormat builds the cluster-scoped secret name "<cluster>-kms-<nodeUUID>".
	// Legacy secrets named "talos-kms-<nodeUUID>" are still found via the
	// node-uuid label, so this name only applies to newly created secrets.
	//nolint:gosec // this is just a name template for KMS secrets
	secretNameFormat = "%s-kms-%s"
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
	nodeUUID, clientIP, data, err := parseRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	kubeClient, err := clientgoclientset.NewForConfig(s.config.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	ctrlClient, err := ctrlclint.New(s.config.ClientConfig.LoopbackClientConfig, ctrlclint.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller client: %w", err)
	}

	return seal(ctx, kubeClient, &ctrlClient, nodeUUID, clientIP, data)
}

// Unseal is a method that decrypts data using the KMS service.
func (s *ServiceServer) Unseal(ctx context.Context, req *kms.Request) (*kms.Response, error) {
	nodeUUID, clientIP, data, err := parseRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	kubeClient, err := clientgoclientset.NewForConfig(s.config.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	return unseal(ctx, kubeClient, nodeUUID, clientIP, data)
}

// NewGRPCServerFactory returns an initializer function that initializes the mock KMS service.
func NewGRPCServerFactory(cfg *config.KommodityConfig) combinedserver.GRPCServerFactory {
	return func(srv *grpc.Server) error {
		// Create a new KMS service server and register it with the gRPC server.
		kms.RegisterKMSServiceServer(srv, &ServiceServer{config: cfg})

		return nil
	}
}

func parseRequest(ctx context.Context, req *kms.Request) (string, string, []byte, error) {
	nodeUUID, err := validateNodeUUID(req)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to validate node UUID: %w", err)
	}

	data := req.GetData()
	if len(data) == 0 {
		//nolint:wrapcheck // we want a gRPC status error here
		return "", "", nil, status.Error(codes.InvalidArgument, "data is required")
	}

	clientIP, err := extractClientIP(ctx)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to extract client IP: %w", err)
	}

	return nodeUUID, clientIP, data, nil
}

func seal(ctx context.Context,
	kubeClient clientgoclientset.Interface,
	ctrlClient *ctrlclint.Client,
	nodeUUID string,
	clientIP string,
	data []byte,
) (*kms.Response, error) {
	secret, err := findSecretByNodeUUID(ctx, kubeClient, nodeUUID)
	if errors.Is(err, ErrSecretNotFound) {
		clusterName, resolveErr := resolveClusterName(ctx, ctrlClient, clientIP)
		if resolveErr != nil {
			return nil, resolveErr
		}

		encryptedData, createErr := createNodeSecret(ctx, kubeClient, clusterName, nodeUUID, clientIP, data)
		if createErr != nil {
			return nil, fmt.Errorf("failed to create node secret: %w", createErr)
		}

		return &kms.Response{Data: encryptedData}, nil
	}

	if err != nil {
		return nil, err
	}

	encryptedData, err := addVolumeToSecret(ctx, kubeClient, secret, nodeUUID, clientIP, data)
	if err != nil {
		return nil, fmt.Errorf("failed to add volume to secret: %w", err)
	}

	return &kms.Response{Data: encryptedData}, nil
}

func unseal(ctx context.Context,
	kubeClient clientgoclientset.Interface,
	nodeUUID string,
	clientIP string,
	data []byte,
) (*kms.Response, error) {
	secret, err := findSecretByNodeUUID(ctx, kubeClient, nodeUUID)
	if errors.Is(err, ErrSecretNotFound) {
		return nil, fmt.Errorf("%w: node %s", ErrSecretNotFound, nodeUUID)
	}

	if err != nil {
		return nil, err
	}

	storedIP := string(secret.Data[sealedFromIPKey])
	if storedIP != clientIP {
		return nil, status.Errorf(codes.PermissionDenied,
			"%v: sealed from %q, unseal requested from %q", ErrIPMismatch, storedIP, clientIP)
	}

	keySets := parseVolumeKeySets(secret.Data)
	if len(keySets) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoVolumeKeySets, secret.Name)
	}

	for _, ks := range keySets {
		aad := buildAAD(nodeUUID, ks.aadNonce, clientIP)

		decryptedData, decErr := decrypt(ks.encryptionKey, data, aad)
		if decErr == nil {
			return &kms.Response{Data: decryptedData}, nil
		}
	}

	return nil, fmt.Errorf("%w: secret %s", ErrNoMatchingSecret, secret.Name)
}

// findSecretByNodeUUID returns the unique KMS secret for a node, identified by
// the talos.dev/node-uuid label. Lookup is label-based rather than name-based
// so that legacy "talos-kms-<uuid>" secrets and new "<cluster>-kms-<uuid>"
// secrets are both discoverable.
func findSecretByNodeUUID(ctx context.Context,
	kubeClient clientgoclientset.Interface,
	nodeUUID string,
) (*corev1.Secret, error) {
	selector := fmt.Sprintf("%s=%s,%s=%s",
		config.ManagedByLabel, config.ManagedByValue,
		config.NodeUUIDLabel, nodeUUID)

	secrets, err := getSecretsAPI(kubeClient).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("failed to list secrets for node %s: %w", nodeUUID, err)
	}

	switch len(secrets.Items) {
	case 0:
		return nil, ErrSecretNotFound
	case 1:
		return &secrets.Items[0], nil
	default:
		return nil, fmt.Errorf("%w: node %s matched %d secrets", ErrAmbiguousSecret, nodeUUID, len(secrets.Items))
	}
}

// resolveClusterName derives the owning cluster name from the requesting node's
// IP using the same path the metadata and attestation services rely on.
func resolveClusterName(ctx context.Context,
	ctrlClient *ctrlclint.Client,
	clientIP string,
) (string, error) {
	machine, err := knet.FindManagedMachineByIP(ctx, ctrlClient, clientIP)
	if err != nil {
		return "", fmt.Errorf("%w: ip %s: %w", ErrClusterNotResolved, clientIP, err)
	}

	clusterName := machine.Spec.ClusterName
	if errs := validation.IsDNS1123Label(clusterName); len(errs) > 0 {
		return "", fmt.Errorf("%w: %q: %s", ErrInvalidClusterName, clusterName, strings.Join(errs, "; "))
	}

	return clusterName, nil
}

func createNodeSecret(ctx context.Context,
	kubeClient clientgoclientset.Interface,
	clusterName string,
	nodeUUID string,
	peerIP string,
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

	secretName := fmt.Sprintf(secretNameFormat, clusterName, nodeUUID)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: config.KommodityNamespace,
			Labels:    config.GetKommodityClusterLabels(nodeUUID, peerIP, clusterName),
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
	kubeClient clientgoclientset.Interface,
	secret *corev1.Secret,
	nodeUUID string,
	peerIP string,
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

func getSecretsAPI(kubeClient clientgoclientset.Interface) v1.SecretInterface {
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
