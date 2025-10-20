// Package kms implements the Talos Linux KMS service, which provides
// a networked key management system for full disk encryption.
//
// This package includes a mock implementation of the gRPC server for
// the SideroLabs KMS API.
package kms

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/siderolabs/kms-client/api/kms"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoclientset "k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

const (
	//nolint:gosec // this is just a prefix for KMS secrets
	secretPrefix       = "talos-kms-"
	keySize            = 32 // 256 bits
	kommodityNamespace = "kommodity-system"
)

// ServiceServer is a struct that implements the ServiceServer interface.
type ServiceServer struct {
	kms.UnimplementedKMSServiceServer

	config *config.KommodityConfig
}

// Seal is a method that encrypts data using the KMS service.
// DISCLAIMER: This is a mock implementation that appends a "sealed:" prefix to the input data.
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

	kubeClient, err := clientgoclientset.NewForConfig(s.config.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	encryptionKey, err := getEncryptionKey(ctx, kubeClient, nodeUUID)
	if apierrors.IsNotFound(err) {
		encryptionKey, err = createEncryptionKey(ctx, kubeClient, nodeUUID)
		if err != nil {
			return nil, fmt.Errorf("failed to create encryption key: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("failed to get encryption key: %w", err)
	}

	encryptedData, err := encrypt(encryptionKey, data)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt data: %w", err)
	}

	return &kms.Response{Data: encryptedData}, nil
}

// Unseal is a method that decrypts data using the KMS service.
// DISCLAIMER: This is a mock implementation that removes the "sealed:" prefix from the input data.
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

	kubeClient, err := clientgoclientset.NewForConfig(s.config.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	encryptionKey, err := getEncryptionKey(ctx, kubeClient, nodeUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to get encryption key: %w", err)
	}

	decryptedData, err := decrypt(encryptionKey, data)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data: %w", err)
	}

	return &kms.Response{Data: decryptedData}, nil
}

// NewGRPCServerFactory returns an initializer function that initializes the mock KMS service.
func NewGRPCServerFactory(cfg *config.KommodityConfig) combinedserver.GRPCServerFactory {
	return func(srv *grpc.Server) error {
		// Create a new KMS service server and register it with the gRPC server.
		kms.RegisterKMSServiceServer(srv, &ServiceServer{config: cfg})

		return nil
	}
}

func getEncryptionKey(ctx context.Context, kubeClient *clientgoclientset.Clientset, nodeUUID string) ([]byte, error) {
	secretName := getSecretName(nodeUUID)

	secret, err := getSecretsAPI(kubeClient).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %s: %w", secretName, err)
	}

	return secret.Data["encryptionKey"], nil
}

func createEncryptionKey(ctx context.Context, kubeClient *clientgoclientset.Clientset,
	nodeUUID string) ([]byte, error) {
	secretName := getSecretName(nodeUUID)

	key := make([]byte, keySize)

	_, err := rand.Read(key)
	if err != nil {
		return nil, fmt.Errorf("failed to generate encryption key: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: kommodityNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "kommodity",
				"talos.dev/node-uuid":          nodeUUID,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"encryptionKey": key,
		},
	}

	_, err = getSecretsAPI(kubeClient).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create secret %s: %w", secretName, err)
	}

	return key, nil
}

func getSecretsAPI(kubeClient *clientgoclientset.Clientset) v1.SecretInterface {
	return (*kubeClient).CoreV1().Secrets(kommodityNamespace)
}

func getSecretName(nodeUUID string) string {
	return secretPrefix + nodeUUID
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
