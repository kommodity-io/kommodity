// Package kms implements the Talos Linux KMS service, which provides
// a networked key management system for full disk encryption.
//
// The service is exposed once on the gRPC server (one registration per
// gRPC contract), but dispatches each request to a per-cluster handler
// chosen by the :authority pseudo-header. Each per-cluster ServiceServer
// reads and writes its KMS secrets inside the namespace named after the
// cluster, so cross-cluster bleed is structurally impossible.
package kms

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/siderolabs/kms-client/api/kms"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
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

	authorityKey = ":authority"
)

// volumeKeySet holds the key material for a single volume group.
type volumeKeySet struct {
	prefix        string
	encryptionKey []byte
	aadNonce      []byte
	luksKey       []byte
}

// ServiceServer is a per-cluster KMS handler. ClusterName is baked in at
// construction; the handler reads and writes secrets in the namespace of
// the same name.
type ServiceServer struct {
	kms.UnimplementedKMSServiceServer

	config      *config.KommodityConfig
	clusterName string
}

// NewServiceServer creates a KMS handler bound to a single cluster. All
// secrets it touches live in the namespace named after that cluster.
func NewServiceServer(cfg *config.KommodityConfig, clusterName string) *ServiceServer {
	return &ServiceServer{config: cfg, clusterName: clusterName}
}

// Router is the single gRPC handler registered against the combined server.
// It maintains one ServiceServer per known cluster and dispatches requests
// by resolving the cluster name from the request's :authority pseudo-header.
type Router struct {
	kms.UnimplementedKMSServiceServer

	config   *config.KommodityConfig
	domain   string
	handlers sync.Map // map[string]*ServiceServer
}

// NewRouter returns a Router configured against the KMS domain from cfg.
// The Router refuses to route until per-cluster handlers are registered.
func NewRouter(cfg *config.KommodityConfig) *Router {
	domain := ""
	if cfg.KMSConfig != nil {
		domain = strings.TrimSpace(cfg.KMSConfig.Domain)
	}

	return &Router{config: cfg, domain: domain}
}

// Domain returns the configured KMS domain suffix.
func (r *Router) Domain() string {
	return r.domain
}

// Register installs a per-cluster handler. Safe to call repeatedly; existing
// handlers for the same cluster are preserved.
func (r *Router) Register(clusterName string) {
	r.handlers.LoadOrStore(clusterName, NewServiceServer(r.config, clusterName))
}

// Deregister removes the per-cluster handler. Safe to call for unknown clusters.
func (r *Router) Deregister(clusterName string) {
	r.handlers.Delete(clusterName)
}

// Seal dispatches the request to the per-cluster handler resolved from :authority.
func (r *Router) Seal(ctx context.Context, req *kms.Request) (*kms.Response, error) {
	handler, err := r.dispatch(ctx)
	if err != nil {
		return nil, err
	}

	return handler.Seal(ctx, req)
}

// Unseal dispatches the request to the per-cluster handler resolved from :authority.
func (r *Router) Unseal(ctx context.Context, req *kms.Request) (*kms.Response, error) {
	handler, err := r.dispatch(ctx)
	if err != nil {
		return nil, err
	}

	return handler.Unseal(ctx, req)
}

func (r *Router) dispatch(ctx context.Context) (*ServiceServer, error) {
	if r.domain == "" {
		//nolint:wrapcheck // we want a gRPC status error here
		return nil, status.Error(codes.FailedPrecondition, ErrKMSDomainNotConfigured.Error())
	}

	clusterName, err := r.clusterFromContext(ctx)
	if err != nil {
		//nolint:wrapcheck // we want a gRPC status error here
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	value, ok := r.handlers.Load(clusterName)
	if !ok {
		//nolint:wrapcheck // we want a gRPC status error here
		return nil, status.Errorf(codes.Unavailable,
			"%v: %s", ErrClusterNotRegistered, clusterName)
	}

	handler, ok := value.(*ServiceServer)
	if !ok {
		//nolint:wrapcheck // unreachable: only ServiceServer is stored in the map
		return nil, status.Errorf(codes.Internal,
			"unexpected handler type for cluster %s", clusterName)
	}

	return handler, nil
}

// clusterFromContext extracts the cluster name from the gRPC :authority
// pseudo-header by stripping the configured domain suffix.
func (r *Router) clusterFromContext(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", ErrMissingAuthority
	}

	authority := firstNonEmpty(md.Get(authorityKey))
	if authority == "" {
		return "", ErrMissingAuthority
	}

	host := authority
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	suffix := "." + r.domain

	name, ok := strings.CutSuffix(host, suffix)
	if !ok {
		return "", fmt.Errorf("%w: host %q not under domain %q", ErrInvalidAuthority, host, r.domain)
	}

	errs := validation.IsDNS1123Label(name)
	if len(errs) > 0 {
		return "", fmt.Errorf("%w: %q: %s", ErrInvalidAuthority, name, strings.Join(errs, "; "))
	}

	return name, nil
}

func firstNonEmpty(values []string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}

// Seal encrypts data using the KMS service for the cluster this handler serves.
func (s *ServiceServer) Seal(ctx context.Context, req *kms.Request) (*kms.Response, error) {
	nodeUUID, clientIP, data, err := parseRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	kubeClient, err := clientgoclientset.NewForConfig(s.config.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	return seal(ctx, kubeClient, s.clusterName, nodeUUID, clientIP, data)
}

// Unseal decrypts data using the KMS service for the cluster this handler serves.
func (s *ServiceServer) Unseal(ctx context.Context, req *kms.Request) (*kms.Response, error) {
	nodeUUID, clientIP, data, err := parseRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	kubeClient, err := clientgoclientset.NewForConfig(s.config.ClientConfig.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	return unseal(ctx, kubeClient, s.clusterName, nodeUUID, clientIP, data)
}

// NewGRPCServerFactory returns a GRPCServerFactory that registers a single
// Router on the gRPC server, plus the Router itself so the caller can wire
// up the per-cluster reconciler.
func NewGRPCServerFactory(cfg *config.KommodityConfig) (combinedserver.GRPCServerFactory, *Router) {
	router := NewRouter(cfg)

	factory := combinedserver.GRPCServerFactory(func(srv *grpc.Server) error {
		kms.RegisterKMSServiceServer(srv, router)

		return nil
	})

	return factory, router
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
	namespace string,
	nodeUUID string,
	clientIP string,
	data []byte,
) (*kms.Response, error) {
	secret, err := findSecretByNodeUUID(ctx, kubeClient, namespace, nodeUUID)
	if errors.Is(err, ErrSecretNotFound) {
		encryptedData, createErr := createNodeSecret(ctx, kubeClient, namespace, nodeUUID, clientIP, data)
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
	namespace string,
	nodeUUID string,
	clientIP string,
	data []byte,
) (*kms.Response, error) {
	secret, err := findSecretByNodeUUID(ctx, kubeClient, namespace, nodeUUID)
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

// findSecretByNodeUUID returns the unique KMS secret for a node within the
// cluster namespace, identified by the talos.dev/node-uuid label. Lookup is
// label-based so legacy "talos-kms-<uuid>" secrets and any future renames
// stay discoverable.
func findSecretByNodeUUID(ctx context.Context,
	kubeClient clientgoclientset.Interface,
	namespace string,
	nodeUUID string,
) (*corev1.Secret, error) {
	selector := fmt.Sprintf("%s=%s,%s=%s",
		config.ManagedByLabel, config.ManagedByValue,
		config.NodeUUIDLabel, nodeUUID)

	secrets, err := getSecretsAPI(kubeClient, namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
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

func createNodeSecret(ctx context.Context,
	kubeClient clientgoclientset.Interface,
	namespace string,
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

	secretName := fmt.Sprintf("%s-%s", secretPrefix, nodeUUID)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels:    config.GetKommodityClusterLabels(nodeUUID, peerIP, namespace),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			sealedFromIPKey:        []byte(peerIP),
			prefix + keySuffix:     key,
			prefix + nonceSuffix:   aadNonce,
			prefix + luksKeySuffix: encryptedData,
		},
	}

	_, err = getSecretsAPI(kubeClient, namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create secret %s/%s: %w", namespace, secretName, err)
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

	_, err = getSecretsAPI(kubeClient, secret.Namespace).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to update secret %s/%s: %w", secret.Namespace, secret.Name, err)
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

func getSecretsAPI(kubeClient clientgoclientset.Interface, namespace string) v1.SecretInterface {
	return kubeClient.CoreV1().Secrets(namespace)
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
