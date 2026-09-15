//nolint:testpackage // white-box tests exercise unexported reconciler internals
package azurearm

import (
	"errors"
	"testing"

	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	testSecretName = "my-secret"
	testNamespace  = "team-a"
)

func TestSecretRefForObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		annotation  string
		defaultName string
		objNS       string
		want        types.NamespacedName
	}{
		{
			name:       "annotation name resolves in object namespace",
			annotation: testSecretName,
			objNS:      testNamespace,
			want:       types.NamespacedName{Namespace: testNamespace, Name: testSecretName},
		},
		{
			name:       "annotation namespace/name is honoured",
			annotation: "creds-ns/" + testSecretName,
			objNS:      testNamespace,
			want:       types.NamespacedName{Namespace: "creds-ns", Name: testSecretName},
		},
		{
			name:        "falls back to default secret in object namespace",
			defaultName: "default-secret",
			objNS:       testNamespace,
			want:        types.NamespacedName{Namespace: testNamespace, Name: "default-secret"},
		},
		{
			name:  "no annotation and no default yields empty ref",
			objNS: testNamespace,
			want:  types.NamespacedName{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			provider := newCredentialProvider(nil, test.defaultName)
			resourceGroup := newResourceGroup("rg")
			resourceGroup.SetNamespace(test.objNS)

			if test.annotation != "" {
				resourceGroup.SetAnnotations(map[string]string{annotations.PerResourceSecret: test.annotation})
			}

			got := provider.secretRefForObject(resourceGroup)
			if got != test.want {
				t.Fatalf("secretRefForObject = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBuildCredentials(t *testing.T) {
	t.Parallel()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "creds"},
		Data: map[string][]byte{
			keyAzureSubscriptionID: []byte("sub-123"),
			keyAzureTenantID:       []byte("tenant-123"),
			keyAzureClientID:       []byte("client-123"),
			keyAzureClientSecret:   []byte("super-secret"),
		},
	}

	creds, err := buildCredentials(secret)
	if err != nil {
		t.Fatalf("buildCredentials returned error: %v", err)
	}

	if creds.subscriptionID != "sub-123" {
		t.Fatalf("subscriptionID = %q, want sub-123", creds.subscriptionID)
	}

	if creds.armClient == nil {
		t.Fatal("expected a non-nil ARM client")
	}
}

func TestBuildCredentialsIncomplete(t *testing.T) {
	t.Parallel()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "creds"},
		Data: map[string][]byte{
			keyAzureSubscriptionID: []byte("sub-123"),
			// missing tenant, client, secret
		},
	}

	_, err := buildCredentials(secret)
	if !errors.Is(err, ErrCredentialSecretIncomplete) {
		t.Fatalf("expected ErrCredentialSecretIncomplete, got %v", err)
	}
}
