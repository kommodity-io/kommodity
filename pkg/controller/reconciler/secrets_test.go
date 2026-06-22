package reconciler_test

import (
	"testing"

	"github.com/kommodity-io/kommodity/pkg/controller/reconciler"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestMergeExtraSecretEntriesEmpty(t *testing.T) {
	t.Parallel()

	got := reconciler.MergeExtraSecretEntries(&corev1.Secret{})
	require.Equal(t, map[string][]byte{}, got)
}

func TestMergeExtraSecretEntriesDataOnly(t *testing.T) {
	t.Parallel()

	secret := &corev1.Secret{
		Data: map[string][]byte{"argocd-cortiacr": []byte("payload-a")},
	}

	want := map[string][]byte{"argocd-cortiacr": []byte("payload-a")}
	require.Equal(t, want, reconciler.MergeExtraSecretEntries(secret))
}

func TestMergeExtraSecretEntriesStringDataOnly(t *testing.T) {
	t.Parallel()

	secret := &corev1.Secret{
		StringData: map[string]string{"argocd-cortiacr": "payload-a"},
	}

	want := map[string][]byte{"argocd-cortiacr": []byte("payload-a")}
	require.Equal(t, want, reconciler.MergeExtraSecretEntries(secret))
}

func TestMergeExtraSecretEntriesMerges(t *testing.T) {
	t.Parallel()

	secret := &corev1.Secret{
		Data:       map[string][]byte{"argocd-cortiacr": []byte("payload-a")},
		StringData: map[string]string{"datadog-keys": "payload-b"},
	}

	want := map[string][]byte{
		"argocd-cortiacr": []byte("payload-a"),
		"datadog-keys":    []byte("payload-b"),
	}
	require.Equal(t, want, reconciler.MergeExtraSecretEntries(secret))
}

func TestMergeExtraSecretEntriesStringDataOverridesData(t *testing.T) {
	t.Parallel()

	secret := &corev1.Secret{
		Data:       map[string][]byte{"argocd-cortiacr": []byte("from-data")},
		StringData: map[string]string{"argocd-cortiacr": "from-stringdata"},
	}

	want := map[string][]byte{"argocd-cortiacr": []byte("from-stringdata")}
	require.Equal(t, want, reconciler.MergeExtraSecretEntries(secret))
}
