//nolint:testpackage // White-box test exercises the unexported strategy directly.
package secrets

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	testSecretName = "s"
	testNamespace  = "ns"
	keyFoo         = "foo"
	keyKeep        = "keep"
)

func TestPrepareForCreate_MergesStringDataIntoData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *corev1.Secret
		wantData map[string][]byte
	}{
		{
			name: "stringData populates empty data",
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: testSecretName, Namespace: testNamespace},
				StringData: map[string]string{keyFoo: "bar"},
			},
			wantData: map[string][]byte{keyFoo: []byte("bar")},
		},
		{
			name: "stringData overrides data on key collision",
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: testSecretName, Namespace: testNamespace},
				Data:       map[string][]byte{keyFoo: []byte("old"), keyKeep: []byte("v")},
				StringData: map[string]string{keyFoo: "new"},
			},
			wantData: map[string][]byte{keyFoo: []byte("new"), keyKeep: []byte("v")},
		},
		{
			name: "nil stringData leaves data untouched",
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: testSecretName, Namespace: testNamespace},
				Data:       map[string][]byte{keyFoo: []byte("bar")},
			},
			wantData: map[string][]byte{keyFoo: []byte("bar")},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			secretStrategy{}.PrepareForCreate(context.Background(), testCase.input)

			assert.Equal(t, testCase.wantData, testCase.input.Data)
			assert.Empty(t, testCase.input.StringData, "StringData should be cleared after merge")
		})
	}
}

func TestPrepareForUpdate_MergesStringDataIntoData(t *testing.T) {
	t.Parallel()

	oldSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testSecretName, Namespace: testNamespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{keyFoo: []byte("old")},
	}

	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testSecretName, Namespace: testNamespace},
		Data:       map[string][]byte{keyFoo: []byte("old"), keyKeep: []byte("v")},
		StringData: map[string]string{keyFoo: "new"},
	}

	secretStrategy{}.PrepareForUpdate(context.Background(), newSecret, oldSecret)

	require.Equal(t, corev1.SecretTypeOpaque, newSecret.Type, "empty type should inherit from old")
	assert.Equal(t, map[string][]byte{keyFoo: []byte("new"), keyKeep: []byte("v")}, newSecret.Data)
	assert.Empty(t, newSecret.StringData)
}
