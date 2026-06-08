package azurearm

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	keyAzureSubscriptionID = "AZURE_SUBSCRIPTION_ID"
	keyAzureTenantID       = "AZURE_TENANT_ID"
	keyAzureClientID       = "AZURE_CLIENT_ID"
	//nolint:gosec // G101: Secret data key name, not a credential
	keyAzureClientSecret = "AZURE_CLIENT_SECRET"
)

// azureCredentials bundles a per-resource resolved Azure credential together with
// its target subscription and a ready-to-use ARM client.
type azureCredentials struct {
	subscriptionID string
	armClient      *armClient
}

// credentialProvider resolves and caches Azure credentials from the Kubernetes
// Secret referenced by a resource's "serviceoperator.azure.com/credential-from"
// annotation (falling back to a configured default Secret name).
type credentialProvider struct {
	client            client.Client
	defaultSecretName string

	mu    sync.Mutex
	cache map[types.NamespacedName]cachedCredential
}

type cachedCredential struct {
	resourceVersion string
	creds           *azureCredentials
}

func newCredentialProvider(c client.Client, defaultSecretName string) *credentialProvider {
	return &credentialProvider{
		client:            c,
		defaultSecretName: defaultSecretName,
		cache:             make(map[types.NamespacedName]cachedCredential),
	}
}

// resolve returns the Azure credentials for the given resource, building (and
// caching) a fresh credential when the backing Secret is new or rotated.
func (p *credentialProvider) resolve(
	ctx context.Context,
	obj genruntime.ARMMetaObject,
) (*azureCredentials, error) {
	secretRef := p.secretRefForObject(obj)
	if secretRef.Name == "" {
		return nil, fmt.Errorf("%w: no credential-from annotation or default secret", ErrCredentialSecretNotFound)
	}

	var secret corev1.Secret

	err := p.client.Get(ctx, secretRef, &secret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %s", ErrCredentialSecretNotFound, secretRef)
		}

		return nil, fmt.Errorf("getting credential secret %s: %w", secretRef, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	cached, ok := p.cache[secretRef]
	if ok && cached.resourceVersion == secret.ResourceVersion {
		return cached.creds, nil
	}

	creds, err := buildCredentials(&secret)
	if err != nil {
		return nil, err
	}

	p.cache[secretRef] = cachedCredential{resourceVersion: secret.ResourceVersion, creds: creds}

	return creds, nil
}

// secretRefForObject resolves the Secret reference for a resource. The annotation
// value may be "name" (resolved in the resource's namespace) or "namespace/name".
func (p *credentialProvider) secretRefForObject(obj genruntime.ARMMetaObject) types.NamespacedName {
	value := obj.GetAnnotations()[annotations.PerResourceSecret]
	if value == "" {
		value = p.defaultSecretName
	}

	if value == "" {
		return types.NamespacedName{}
	}

	namespace, name, found := strings.Cut(value, "/")
	if !found {
		return types.NamespacedName{Namespace: obj.GetNamespace(), Name: value}
	}

	return types.NamespacedName{Namespace: namespace, Name: name}
}

func buildCredentials(secret *corev1.Secret) (*azureCredentials, error) {
	subscriptionID := string(secret.Data[keyAzureSubscriptionID])
	tenantID := string(secret.Data[keyAzureTenantID])
	clientID := string(secret.Data[keyAzureClientID])
	clientSecret := string(secret.Data[keyAzureClientSecret])

	if subscriptionID == "" || tenantID == "" || clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("%w: %s/%s", ErrCredentialSecretIncomplete, secret.Namespace, secret.Name)
	}

	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("building client secret credential: %w", err)
	}

	armCli, err := newARMClient(cred)
	if err != nil {
		return nil, err
	}

	return &azureCredentials{subscriptionID: subscriptionID, armClient: armCli}, nil
}
