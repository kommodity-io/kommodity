package reconciler

import (
	"context"
	"fmt"
	"time"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// RequeueAfter is the duration to wait before requeuing a request.
	RequeueAfter = 10 * time.Second
)

// DownstreamClientConfig holds the configuration needed to create a Kubernetes client for downstream clusters.
type DownstreamClientConfig struct {
	client.Client

	ClusterName string
}

// FetchKubeConfigFromSecret retrieves the kubeconfig from a Kubernetes Secret.
func (c *DownstreamClientConfig) FetchKubeConfigFromSecret(ctx context.Context) ([]byte, error) {
	kubeConfigSecret := &corev1.Secret{}

	kubeConfigSecretName := c.ClusterName + "-kubeconfig"

	err := c.Get(ctx, client.ObjectKey{
		Name:      kubeConfigSecretName,
		Namespace: "default",
	}, kubeConfigSecret)
	if err != nil {
		logging.FromContext(ctx).Error("Failed to get kubeconfig secret",
			zap.String("secretName", kubeConfigSecretName),
			zap.Error(err))

		return nil, fmt.Errorf("failed to get kubeconfig secret %s: %w", kubeConfigSecretName, err)
	}

	kubeConfigBytes, ok := kubeConfigSecret.Data["value"]
	if !ok {
		logging.FromContext(ctx).Error("Kubeconfig value not found in secret",
			zap.String("secretName", kubeConfigSecretName))

		return nil, fmt.Errorf("kubeconfig %w: %s", ErrValueNotFoundInSecret, kubeConfigSecretName)
	}

	return kubeConfigBytes, nil
}

// FetchDownstreamKubernetesClient retrieves the kubeconfig from a
// Kubernetes Secret and creates a client for the downstream cluster.
func (c *DownstreamClientConfig) FetchDownstreamKubernetesClient(ctx context.Context) (client.Client, error) {
	kubeConfigBytes, err := c.FetchKubeConfigFromSecret(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch kubeconfig from secret: %w", err)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigBytes)
	if err != nil {
		logging.FromContext(ctx).Error("Failed to create REST config from kubeconfig", zap.Error(err))

		return nil, fmt.Errorf("failed to create REST config from kubeconfig: %w", err)
	}

	downstreamClient, err := client.New(restConfig, client.Options{})
	if err != nil {
		logging.FromContext(ctx).Error("Failed to create downstream Kubernetes client", zap.Error(err))

		return nil, fmt.Errorf("failed to create downstream Kubernetes client: %w", err)
	}

	return downstreamClient, nil
}

// CheckClusterReady verifies the downstream cluster is reachable by attempting
// to get the kube-system namespace. Returns ErrClusterNotReady if the cluster
// is not yet accessible.
func CheckClusterReady(ctx context.Context, kubeClient client.Client) error {
	ns := &corev1.Namespace{}

	err := kubeClient.Get(ctx, client.ObjectKey{Name: "kube-system"}, ns)
	if err != nil {
		logging.FromContext(ctx).Debug("Downstream cluster not ready",
			zap.Error(err))

		return fmt.Errorf("%w: %w", ErrClusterNotReady, err)
	}

	return nil
}

// ApplySecretToClient renders a secret template with the provided
// secret data and applies it to the given Kubernetes client.
func ApplySecretToClient(ctx context.Context, kubeClient client.Client, secret *corev1.Secret) error {
	newSecret := &corev1.Secret{}
	newSecret.Type = secret.Type
	newSecret.Name = secret.Name
	newSecret.Namespace = secret.Namespace

	if secret.Labels != nil {
		newSecret.Labels = secret.Labels
	}

	if secret.Data != nil {
		newSecret.Data = secret.Data
	}

	if secret.StringData != nil {
		newSecret.StringData = secret.StringData
	}

	err := kubeClient.Create(ctx, newSecret)
	if apierrors.IsAlreadyExists(err) {
		err := kubeClient.Update(ctx, newSecret)
		if err != nil {
			return fmt.Errorf("failed to update provider secret %s: %w", newSecret.Name, err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to create provider secret %s: %w", newSecret.Name, err)
	}

	return nil
}
