package auth

import (
	"context"
	"crypto/rsa"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	bearertoken "k8s.io/apiserver/pkg/authentication/request/bearertoken"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/kubernetes/pkg/serviceaccount"
)

// defaultSAIssuer matches serviceaccount.LegacyIssuer.
const defaultSAIssuer = "kubernetes/serviceaccount"

const (
	// DefaultSigningKeyNamespace is the default namespace where the
	// service account signing key Secret is stored.
	DefaultSigningKeyNamespace = "kube-system"

	// DefaultSigningKeySecretName is the default name of the Secret that
	// stores the service account signing key.
	//
	//nolint:gosec // G101 — this is a Secret name, not a credential.
	DefaultSigningKeySecretName = "libkapi-service-account-signing-key"
)

// ServiceAccountTokenGetter provides access to ServiceAccount, Pod, Secret,
// and Node objects for token validation. It mirrors
// k8s.io/kubernetes/pkg/serviceaccount.ServiceAccountTokenGetter — existing
// implementations satisfy this interface structurally, so callers don't need
// to import the upstream package to implement it.
type ServiceAccountTokenGetter interface {
	GetServiceAccount(namespace, name string) (*corev1.ServiceAccount, error)
	GetPod(namespace, name string) (*corev1.Pod, error)
	GetSecret(namespace, name string) (*corev1.Secret, error)
	GetNode(name string) (*corev1.Node, error)
}

// SecretsGetter provides access to Secret objects. It mirrors
// k8s.io/client-go/kubernetes/typed/core/v1.SecretsGetter.
type SecretsGetter interface {
	Secrets(namespace string) corev1client.SecretInterface
}

// KeyPersistenceConfig configures signing-key persistence to a Kubernetes
// Secret so the key survives restarts. The caller is responsible for
// loading the key on restart (e.g. by reading the Secret before calling
// libkapi.New and passing it as ServiceAccountConfig.SigningKey).
type KeyPersistenceConfig struct {
	// Namespace is where the signing key Secret is stored.
	// Default: "kube-system". Created if it doesn't exist.
	Namespace string

	// SecretName is the name of the signing key Secret.
	// Default: "libkapi-service-account-signing-key".
	SecretName string

	// TokenSecretsNamespace is where SA token secrets are listed for
	// rotation when the signing key changes. Default: "kube-system".
	TokenSecretsNamespace string

	// OnTokenRotated is called for each SA token secret that was rotated
	// (deleted and recreated) during key rotation. The caller can use this
	// for side effects like updating autoscaler ConfigMaps.
	// If nil, no callback is made. Errors are logged but don't stop rotation.
	OnTokenRotated func(ctx context.Context, rotatedSecret *corev1.Secret) error
}

// ServiceAccountConfig configures ServiceAccount token authentication and
// management.
type ServiceAccountConfig struct {
	// Issuer for SA tokens. Default: "kubernetes/serviceaccount".
	Issuer string

	// SigningKey verifies token signatures. If nil, a 4096-bit RSA key
	// is generated in-memory at server build time.
	SigningKey *rsa.PrivateKey

	// KeyPersistence, if non-nil, persists the signing key to a Secret
	// so it survives restarts. If nil, the key is ephemeral.
	KeyPersistence *KeyPersistenceConfig

	// RootCA is included in token secrets as ca.crt. If nil, token
	// secrets are created without ca.crt.
	RootCA []byte

	// TokenGetter validates SA existence. If nil, libkapi builds one from
	// the server's loopback client (full validation, matching pkg/server).
	TokenGetter ServiceAccountTokenGetter

	// SecretsGetter validates Secret existence. If nil, libkapi builds one
	// from the loopback client.
	SecretsGetter SecretsGetter
}

// WithServiceAccount adds a ServiceAccount token authenticator to the chain.
// The authenticator is built later by BuildSAAuthenticator, which needs the
// server's LoopbackClientConfig for SA/Secret lookup.
func WithServiceAccount(cfg ServiceAccountConfig) Option {
	return func(_ context.Context, o *config) error {
		o.saConfig = &cfg

		return nil
	}
}

// BuildSAAuthenticator builds the ServiceAccount token authenticator using
// the signing key and the server's loopback config. This is called by
// buildServer after genericServerConfig is available, because the SA/Secret
// lookup (when not provided by the caller) needs a kube client.
//
// Mirrors pkg/server/auth.go's setupServiceAccountAuth (lines 238-276).
func BuildSAAuthenticator(
	saCfg *ServiceAccountConfig,
	signingKey *rsa.PrivateKey,
	loopbackConfig *restclient.Config,
) (authenticator.Request, error) {
	keysGetter, err := serviceaccount.StaticPublicKeysGetter([]any{&signingKey.PublicKey})
	if err != nil {
		return nil, fmt.Errorf("failed to create static public keys getter: %w", err)
	}

	saGetter, secretsGet, err := ResolveSALookup(saCfg, loopbackConfig)
	if err != nil {
		return nil, err
	}

	validator, err := serviceaccount.NewLegacyValidator(true, saGetter, secretsGet)
	if err != nil {
		return nil, fmt.Errorf("failed to create legacy validator: %w", err)
	}

	issuer := saCfg.Issuer
	if issuer == "" {
		issuer = defaultSAIssuer
	}

	saAuth := serviceaccount.JWTTokenAuthenticator(
		[]string{issuer},
		keysGetter,
		nil, // no audience validation for legacy tokens
		validator,
	)

	return bearertoken.New(saAuth), nil
}

// ResolveSALookup returns the SA token getter and secrets getter, either
// from the caller-provided implementations or built from the loopback client.
func ResolveSALookup(
	saCfg *ServiceAccountConfig,
	loopbackConfig *restclient.Config,
) (serviceaccount.ServiceAccountTokenGetter, corev1client.SecretsGetter, error) {
	if saCfg.TokenGetter != nil && saCfg.SecretsGetter != nil {
		return saCfg.TokenGetter, saCfg.SecretsGetter, nil
	}

	client, err := kubernetes.NewForConfig(loopbackConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create kubernetes client for SA lookup: %w", err)
	}

	saGetter := saCfg.TokenGetter
	if saGetter == nil {
		saGetter = &LoopbackSAGetter{Client: client}
	}

	secretsGet := saCfg.SecretsGetter
	if secretsGet == nil {
		secretsGet = &LoopbackSecretsGetter{Client: client}
	}

	return saGetter, secretsGet, nil
}

// LoopbackSAGetter implements ServiceAccountTokenGetter using a loopback
// kube client, matching pkg/server/auth.go's serviceAccountTokenGetter.
type LoopbackSAGetter struct {
	Client kubernetes.Interface
}

// GetServiceAccount fetches a ServiceAccount by namespace and name.
func (g *LoopbackSAGetter) GetServiceAccount(namespace, name string) (*corev1.ServiceAccount, error) {
	serviceAccount, err := g.Client.CoreV1().ServiceAccounts(namespace).Get(
		context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get service account %s/%s: %w", namespace, name, err)
	}

	return serviceAccount, nil
}

// GetPod is not supported in libkapi — pods are not stored.
func (g *LoopbackSAGetter) GetPod(_, _ string) (*corev1.Pod, error) {
	return nil, errNotSupportedInLibkapi("pods")
}

// GetSecret fetches a Secret by namespace and name.
func (g *LoopbackSAGetter) GetSecret(namespace, name string) (*corev1.Secret, error) {
	secret, err := g.Client.CoreV1().Secrets(namespace).Get(
		context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, name, err)
	}

	return secret, nil
}

// GetNode is not supported in libkapi — nodes are not stored.
func (g *LoopbackSAGetter) GetNode(_ string) (*corev1.Node, error) {
	return nil, errNotSupportedInLibkapi("nodes")
}

// LoopbackSecretsGetter implements SecretsGetter using a loopback kube client.
type LoopbackSecretsGetter struct {
	Client kubernetes.Interface
}

// Secrets returns the SecretInterface for the given namespace.
func (g *LoopbackSecretsGetter) Secrets(namespace string) corev1client.SecretInterface {
	return g.Client.CoreV1().Secrets(namespace)
}

// ResolveSigningKey returns the signing key from cfg, or generates a new
// 4096-bit RSA key if cfg.SigningKey is nil.
func ResolveSigningKey(cfg *ServiceAccountConfig) (*rsa.PrivateKey, error) {
	if cfg.SigningKey != nil {
		return cfg.SigningKey, nil
	}

	return GenerateRSAPrivateKey()
}

// ResolveSigningKeyNamespace returns the namespace for the signing key
// Secret: kp.Namespace if set, else systemNamespace if set, else
// DefaultSigningKeyNamespace as a last-resort fallback for direct callers
// that pass "" for systemNamespace. libkapi.New's own call chain always
// passes cfg.resolvedSystemNamespace(), which is never empty.
func ResolveSigningKeyNamespace(kp *KeyPersistenceConfig, systemNamespace string) string {
	if kp.Namespace != "" {
		return kp.Namespace
	}

	if systemNamespace != "" {
		return systemNamespace
	}

	return DefaultSigningKeyNamespace
}

// ResolveSigningKeySecretName returns the secret name for the signing key
// Secret, defaulting to DefaultSigningKeySecretName if not set.
func ResolveSigningKeySecretName(kp *KeyPersistenceConfig) string {
	if kp.SecretName != "" {
		return kp.SecretName
	}

	return DefaultSigningKeySecretName
}

// errNotSupportedInLibkapi is returned for resources that libkapi doesn't store.
func errNotSupportedInLibkapi(resource string) error {
	return fmt.Errorf("%w: %s", ErrResourceNotSupported, resource)
}
