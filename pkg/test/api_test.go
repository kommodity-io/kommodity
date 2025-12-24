package test_test

import (
	"context"
	"os"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/test/helpers"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//nolint:gochecknoglobals // Test environment needs to be reused by all tests.
var env helpers.TestEnvironment

func TestMain(m *testing.M) {
	// --- Setup ---
	env = helpers.SetupContainers()

	// Run tests
	code := m.Run()

	// --- Teardown ---
	env.Teardown()

	os.Exit(code)
}

// func TestAPIIntegration(t *testing.T) {
// 	t.Parallel()

// 	client := env.KommodityK8s
// 	groups, err := client.Discovery().ServerGroups()
// 	require.NoError(t, err)

// 	var coreGroupVersions []string
// 	for _, group := range groups.Groups {
// 		if group.Name == "" {
// 			for _, version := range group.Versions {
// 				coreGroupVersions = append(coreGroupVersions, version.Version)
// 			}
// 			break
// 		}
// 	}
// 	require.Contains(t, coreGroupVersions, "v1")
// }

func TestCreateSecret(t *testing.T) {
	t.Parallel()

	client := env.KommodityK8s
	ctx := context.Background()

	// Ensure default namespace exists

	_, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
		},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		require.NoError(t, err)
	}

	// Create secret that holds K3s kubeconfig

	secret, err := client.CoreV1().Secrets("default").Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kubevirt-credentials",
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"kubeconfig": env.K3sKubeconfig,
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	require.Equal(t, corev1.SecretTypeOpaque, secret.Type)

	created, err := client.CoreV1().Secrets("default").Get(ctx, "kubevirt-credentials", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, corev1.SecretTypeOpaque, created.Type)
	require.Equal(t, env.K3sKubeconfig, created.Data["kubeconfig"])
}
