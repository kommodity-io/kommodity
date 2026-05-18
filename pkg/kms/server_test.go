package kms_test

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/kms"
	kmsapi "github.com/siderolabs/kms-client/api/kms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoclientset "k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

const (
	testNodeUUID  = "550e8400-e29b-41d4-a716-446655440000"
	testNodeIP    = "10.0.0.1"
	testCluster   = "prd-par01"
	testPlaintext = "this is a secret LUKS key"
	legacyPrefix  = "talos-kms"
)

func buildExistingSecret(t *testing.T, namespace string, plaintext []byte) *corev1.Secret {
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
			Namespace: namespace,
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

func newKubeClient(secrets ...*corev1.Secret) clientgoclientset.Interface {
	objects := make([]runtime.Object, 0, len(secrets))
	for _, s := range secrets {
		objects = append(objects, s)
	}

	return kubefake.NewSimpleClientset(objects...)
}

func ctxWithAuthority(authority string) context.Context {
	md := metadata.New(map[string]string{kms.AuthorityKey: authority})

	return metadata.NewIncomingContext(context.Background(), md)
}

// ----- Seal: new node booting after this patch was deployed -----

func TestSeal_NewNodeCreatesSecretInClusterNamespace(t *testing.T) {
	t.Parallel()

	kubeClient := newKubeClient()

	_, err := kms.Seal(context.Background(), kubeClient, testCluster, testNodeUUID, testNodeIP, []byte(testPlaintext))
	require.NoError(t, err)

	// Secret is created in the cluster's namespace, using the legacy
	// "talos-kms-<uuid>" name (namespace isolates clusters now).
	expectedName := legacyPrefix + "-" + testNodeUUID
	created, getErr := kubeClient.CoreV1().Secrets(testCluster).
		Get(context.Background(), expectedName, metav1.GetOptions{})
	require.NoError(t, getErr)

	assert.Equal(t, testCluster, created.Namespace)
	assert.Equal(t, testCluster, created.Labels[config.ClusterNameLabel])
	assert.Equal(t, testNodeUUID, created.Labels[config.NodeUUIDLabel])
	assert.Equal(t, config.ManagedByValue, created.Labels[config.ManagedByLabel])
	assert.Equal(t, []byte(testNodeIP), created.Data[kms.SealedFromIPKey])

	// It is NOT created in the default namespace.
	list, listErr := kubeClient.CoreV1().Secrets("default").
		List(context.Background(), metav1.ListOptions{})
	require.NoError(t, listErr)
	assert.Empty(t, list.Items)
}

// ----- Seal: node that booted before this patch and already has a secret -----

func TestSeal_PrePatchNodeWithExistingSecretAddsVolumeInPlace(t *testing.T) {
	t.Parallel()

	plaintext := []byte(testPlaintext)
	existing := buildExistingSecret(t, testCluster, plaintext)
	originalName := existing.Name
	kubeClient := newKubeClient(existing)

	_, err := kms.Seal(context.Background(), kubeClient, testCluster, testNodeUUID, testNodeIP, []byte("another volume"))
	require.NoError(t, err)

	updated, getErr := kubeClient.CoreV1().Secrets(testCluster).
		Get(context.Background(), originalName, metav1.GetOptions{})
	require.NoError(t, getErr)
	assert.Equal(t, originalName, updated.Name)

	keySets := kms.ParseVolumeKeySets(updated.Data)
	assert.Len(t, keySets, 2, "expected the second Seal to add a new volume key set in place")

	list, listErr := kubeClient.CoreV1().Secrets(testCluster).
		List(context.Background(), metav1.ListOptions{})
	require.NoError(t, listErr)
	assert.Len(t, list.Items, 1, "no second secret should have been created")
}

// ----- Unseal -----

func TestUnseal_FindsSecretByLabelRegardlessOfName(t *testing.T) {
	t.Parallel()

	plaintext := []byte(testPlaintext)
	secret := buildExistingSecret(t, testCluster, plaintext)

	// Rename the secret to simulate a "future" naming scheme; lookup is
	// label-based so this must still work.
	secret.Name = "weird-future-name-" + testNodeUUID

	kubeClient := newKubeClient(secret)

	var ciphertext []byte

	for k, v := range secret.Data {
		if strings.HasSuffix(k, kms.LuksKeySuffix) {
			ciphertext = v

			break
		}
	}

	require.NotEmpty(t, ciphertext)

	resp, err := kms.Unseal(context.Background(), kubeClient, testCluster, testNodeUUID, testNodeIP, ciphertext)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, plaintext, resp.GetData())
}

func TestUnseal_ReturnsNotFoundWhenNoSecretMatches(t *testing.T) {
	t.Parallel()

	kubeClient := newKubeClient()

	_, err := kms.Unseal(context.Background(), kubeClient, testCluster, testNodeUUID, testNodeIP, []byte("ciphertext"))
	require.Error(t, err)
	assert.ErrorIs(t, err, kms.ErrSecretNotFound)
}

// ----- Cluster namespace isolation -----

func TestSeal_DoesNotSeeOtherClusterSecrets(t *testing.T) {
	t.Parallel()

	// A secret with the same node UUID exists in another cluster's namespace.
	otherCluster := "stg-par01"
	other := buildExistingSecret(t, otherCluster, []byte("other-cluster-plaintext"))
	kubeClient := newKubeClient(other)

	// Sealing for testCluster must NOT find that secret; it should create a
	// fresh one in testCluster's namespace.
	_, err := kms.Seal(context.Background(), kubeClient, testCluster, testNodeUUID, testNodeIP, []byte(testPlaintext))
	require.NoError(t, err)

	myList, listErr := kubeClient.CoreV1().Secrets(testCluster).
		List(context.Background(), metav1.ListOptions{})
	require.NoError(t, listErr)
	assert.Len(t, myList.Items, 1)

	otherList, listErr := kubeClient.CoreV1().Secrets(otherCluster).
		List(context.Background(), metav1.ListOptions{})
	require.NoError(t, listErr)
	assert.Len(t, otherList.Items, 1, "the other cluster's secret must be untouched")
}

// ----- clusterFromContext: parsing the :authority pseudo-header -----

func TestClusterFromContext_NoAuthorityReturnsMissingAuthority(t *testing.T) {
	t.Parallel()

	_, err := kms.ClusterFromContext(context.Background())
	require.ErrorIs(t, err, kms.ErrMissingAuthority)
}

func TestClusterFromContext_ValidAuthorityReturnsFirstLabel(t *testing.T) {
	t.Parallel()

	ctx := ctxWithAuthority(testCluster + ".kms.kommodity:443")

	name, err := kms.ClusterFromContext(ctx)
	require.NoError(t, err)
	assert.Equal(t, testCluster, name)
}

func TestClusterFromContext_BareHostnameReturnsItself(t *testing.T) {
	t.Parallel()

	// No domain suffix at all: the whole hostname is taken as the cluster.
	ctx := ctxWithAuthority(testCluster + ":443")

	name, err := kms.ClusterFromContext(ctx)
	require.NoError(t, err)
	assert.Equal(t, testCluster, name)
}

func TestClusterFromContext_InvalidLabelReturnsInvalidAuthority(t *testing.T) {
	t.Parallel()

	// Capital letters and underscores are not valid DNS-1123 labels.
	ctx := ctxWithAuthority("INVALID_LABEL.kms.kommodity:443")

	_, err := kms.ClusterFromContext(ctx)
	require.ErrorIs(t, err, kms.ErrInvalidAuthority)
}

// ----- Router: onboarded vs not onboarded -----

// TestRouter_UnregisteredClusterReturns404 verifies that when no cluster has
// been onboarded (handler map is empty), the Router rejects requests with
// codes.NotFound rather than reaching any per-cluster handler.
func TestRouter_UnregisteredClusterReturns404(t *testing.T) {
	t.Parallel()

	router := kms.NewRouter(&config.KommodityConfig{})

	ctx := ctxWithAuthority(testCluster + ".kms.kommodity:443")

	_, err := router.Seal(ctx, &kmsapi.Request{NodeUuid: testNodeUUID, Data: []byte("x")})
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok, "expected a gRPC status error")
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), kms.ErrClusterNotRegistered.Error())
}

// TestRouter_OnboardedClusterReachesHandler verifies the production routing
// path: Register the cluster the way the Cluster reconciler does, then send a
// request with :authority pointing at that cluster. The request must reach
// the per-cluster handler — verified by the error NOT being the Router's
// not-registered 404.
func TestRouter_OnboardedClusterReachesHandler(t *testing.T) {
	t.Parallel()

	router := kms.NewRouter(&config.KommodityConfig{})
	router.Register(testCluster)

	ctx := ctxWithAuthority(testCluster + ".kms.kommodity:443")

	_, err := router.Seal(ctx, &kmsapi.Request{NodeUuid: testNodeUUID, Data: []byte("x")})
	require.Error(t, err)

	// The routing succeeded — the request reached the per-cluster handler.
	// We do NOT see the not-registered 404 that an empty router would return.
	assert.NotContains(t, err.Error(), kms.ErrClusterNotRegistered.Error())
}

// TestRouter_DeregisterRemovesHandler verifies the cleanup half of the
// reconciler's contract.
func TestRouter_DeregisterRemovesHandler(t *testing.T) {
	t.Parallel()

	router := kms.NewRouter(&config.KommodityConfig{})
	router.Register(testCluster)
	router.Deregister(testCluster)

	ctx := ctxWithAuthority(testCluster + ".kms.kommodity:443")

	_, err := router.Seal(ctx, &kmsapi.Request{NodeUuid: testNodeUUID, Data: []byte("x")})
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}
