package kms_test

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/kms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoclientset "k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrlclint "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testNodeUUID    = "550e8400-e29b-41d4-a716-446655440000"
	testNodeIP      = "10.0.0.1"
	testClusterName = "myclusterfoo"
	testPlaintext   = "this is a secret LUKS key"
	legacyPrefix    = "talos-kms"
)

func newCtrlScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, clusterv1.AddToScheme(scheme))

	return scheme
}

//nolint:varnamelen // Variable name ip is appropriate for the context.
func newCtrlClientWithMachine(t *testing.T, ip string, clusterName string) *ctrlclint.Client {
	t.Helper()

	scheme := newCtrlScheme(t)

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "machine-1",
			Namespace: "default",
			Labels:    map[string]string{config.ManagedByLabel: config.ManagedByValue},
		},
		Spec: clusterv1.MachineSpec{ClusterName: clusterName},
		Status: clusterv1.MachineStatus{
			Addresses: []clusterv1.MachineAddress{
				{Type: clusterv1.MachineInternalIP, Address: ip},
			},
		},
	}

	var client ctrlclint.Client = ctrlfake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(machine).
		Build()

	return &client
}

func newEmptyCtrlClient(t *testing.T) *ctrlclint.Client {
	t.Helper()

	scheme := newCtrlScheme(t)

	var client ctrlclint.Client = ctrlfake.NewClientBuilder().WithScheme(scheme).Build()

	return &client
}

// buildLegacySecret pre-encrypts a plaintext under a fresh key + AAD nonce and
// returns a Talos-style "talos-kms-<uuid>" Secret carrying the legacy naming
// scheme. This simulates a secret created by the previous version of Kommodity.
func buildLegacySecret(t *testing.T, plaintext []byte) *corev1.Secret {
	t.Helper()

	key := make([]byte, kms.KeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)

	nonce := make([]byte, kms.AADNonceSize)
	_, err = rand.Read(nonce)
	require.NoError(t, err)

	aad := kms.BuildAAD(testNodeUUID, nonce, testNodeIP)

	ciphertext, err := kms.Encrypt(key, plaintext, aad)
	require.NoError(t, err)

	volumePrefix := "abcd1234"

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      legacyPrefix + "-" + testNodeUUID,
			Namespace: config.KommodityNamespace,
			Labels: map[string]string{
				config.ManagedByLabel: config.ManagedByValue,
				config.NodeUUIDLabel:  testNodeUUID,
				config.NodeIPLabel:    testNodeIP,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			kms.SealedFromIPKey:              []byte(testNodeIP),
			volumePrefix + kms.KeySuffix:     key,
			volumePrefix + kms.NonceSuffix:   nonce,
			volumePrefix + kms.LuksKeySuffix: ciphertext,
		},
	}
}

func newKubeClientWithSecrets(secrets ...*corev1.Secret) clientgoclientset.Interface {
	objects := make([]runtime.Object, 0, len(secrets))
	for _, s := range secrets {
		objects = append(objects, s)
	}

	return kubefake.NewSimpleClientset(objects...)
}

func TestUnsealLegacySecretFoundByLabel(t *testing.T) {
	t.Parallel()

	plaintext := []byte(testPlaintext)
	secret := buildLegacySecret(t, plaintext)
	kubeClient := newKubeClientWithSecrets(secret)

	// Recover the ciphertext to feed back into Unseal.
	var ciphertext []byte

	for k, v := range secret.Data {
		if strings.HasSuffix(k, kms.LuksKeySuffix) {
			ciphertext = v

			break
		}
	}

	require.NotEmpty(t, ciphertext)

	resp, err := kms.Unseal(context.Background(), kubeClient, testNodeUUID, testNodeIP, ciphertext)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, plaintext, resp.GetData())
}

func TestSealCreatesClusterPrefixedSecretForNewNode(t *testing.T) {
	t.Parallel()

	kubeClient := newKubeClientWithSecrets()
	ctrlClient := newCtrlClientWithMachine(t, testNodeIP, testClusterName)

	resp, err := kms.Seal(context.Background(), kubeClient, ctrlClient, testNodeUUID, testNodeIP, []byte(testPlaintext))
	require.NoError(t, err)
	require.NotNil(t, resp)

	expectedName := testClusterName + "-kms-" + testNodeUUID

	created, err := kubeClient.CoreV1().Secrets(config.KommodityNamespace).
		Get(context.Background(), expectedName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, testClusterName, created.Labels[config.ClusterNameLabel])
	assert.Equal(t, testNodeUUID, created.Labels[config.NodeUUIDLabel])
	assert.Equal(t, config.ManagedByValue, created.Labels[config.ManagedByLabel])
	assert.Equal(t, []byte(testNodeIP), created.Data[kms.SealedFromIPKey])
}

func TestSealReturnsErrClusterNotResolvedWhenNoMachineMatches(t *testing.T) {
	t.Parallel()

	kubeClient := newKubeClientWithSecrets()
	ctrlClient := newEmptyCtrlClient(t)

	_, err := kms.Seal(context.Background(), kubeClient, ctrlClient, testNodeUUID, testNodeIP, []byte(testPlaintext))
	require.ErrorIs(t, err, kms.ErrClusterNotResolved)

	// Nothing should have been written.
	list, listErr := kubeClient.CoreV1().Secrets(config.KommodityNamespace).
		List(context.Background(), metav1.ListOptions{})
	require.NoError(t, listErr)
	assert.Empty(t, list.Items)
}

func TestSealUpdatesLegacySecretInPlace(t *testing.T) {
	t.Parallel()

	plaintext := []byte(testPlaintext)
	legacy := buildLegacySecret(t, plaintext)
	originalName := legacy.Name
	kubeClient := newKubeClientWithSecrets(legacy)

	// ctrlClient should be irrelevant here because the legacy secret already
	// exists and label-lookup finds it without resolving cluster name.
	ctrlClient := newEmptyCtrlClient(t)

	_, err := kms.Seal(context.Background(), kubeClient, ctrlClient, testNodeUUID, testNodeIP, []byte("another volume"))
	require.NoError(t, err)

	// Same secret, same legacy name, but with an additional volume key set.
	updated, err := kubeClient.CoreV1().Secrets(config.KommodityNamespace).
		Get(context.Background(), originalName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, originalName, updated.Name)

	keySets := kms.ParseVolumeKeySets(updated.Data)
	assert.Len(t, keySets, 2, "expected the second Seal to add a new volume key set")

	// No new secret with the cluster prefix should have been created.
	list, listErr := kubeClient.CoreV1().Secrets(config.KommodityNamespace).
		List(context.Background(), metav1.ListOptions{})
	require.NoError(t, listErr)
	assert.Len(t, list.Items, 1)
}

func TestFindSecretByNodeUUIDReturnsAmbiguousOnMultipleMatches(t *testing.T) {
	t.Parallel()

	first := buildLegacySecret(t, []byte("a"))
	second := buildLegacySecret(t, []byte("b"))
	second.Name = testClusterName + "-kms-" + testNodeUUID

	kubeClient := newKubeClientWithSecrets(first, second)

	_, err := kms.FindSecretByNodeUUID(context.Background(), kubeClient, testNodeUUID)
	require.Error(t, err)
	assert.ErrorIs(t, err, kms.ErrAmbiguousSecret)
}

func TestUnsealReturnsNotFoundWhenNoSecretMatches(t *testing.T) {
	t.Parallel()

	kubeClient := newKubeClientWithSecrets()

	_, err := kms.Unseal(context.Background(), kubeClient, testNodeUUID, testNodeIP, []byte("ciphertext"))
	require.Error(t, err)
	assert.ErrorIs(t, err, kms.ErrSecretNotFound)
}

func TestResolveClusterNameRejectsInvalidName(t *testing.T) {
	t.Parallel()

	ctrlClient := newCtrlClientWithMachine(t, testNodeIP, "Invalid_Cluster_Name")

	_, err := kms.ResolveClusterName(context.Background(), ctrlClient, testNodeIP)
	require.Error(t, err)
	assert.ErrorIs(t, err, kms.ErrInvalidClusterName)
}
