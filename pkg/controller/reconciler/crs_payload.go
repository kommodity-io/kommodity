package reconciler

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	addonsv1 "sigs.k8s.io/cluster-api/api/addons/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// clustersByAnnotationValue returns reconcile requests for every Cluster in the
// given Secret's namespace whose annotation `annotationKey` equals the Secret's
// name. Used by CRS-payload reconcilers to wire credential-rotation watches.
func clustersByAnnotationValue(
	ctx context.Context,
	cli client.Client,
	secret *corev1.Secret,
	annotationKey string,
) []reconcile.Request {
	clusters := &clusterv1.ClusterList{}

	err := cli.List(ctx, clusters, client.InNamespace(secret.Namespace))
	if err != nil {
		logging.FromContext(ctx).Error("Failed to list Clusters for Secret watch",
			zap.String("secret", secret.Namespace+"/"+secret.Name),
			zap.String("annotationKey", annotationKey),
			zap.Error(err))

		return nil
	}

	requests := make([]reconcile.Request, 0, len(clusters.Items))

	for i := range clusters.Items {
		cluster := &clusters.Items[i]
		if cluster.Annotations[annotationKey] != secret.Name {
			continue
		}

		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(cluster),
		})
	}

	return requests
}

// upsertCRSPayloadSecret upserts a ClusterResourceSet-typed Secret that wraps a
// single downstream manifest. OwnerRef → Cluster so payload GC's on delete.
func upsertCRSPayloadSecret(
	ctx context.Context,
	cli client.Client,
	cluster *clusterv1.Cluster,
	payloadName string,
	dataKey string,
	manifest []byte,
) error {
	payload := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      payloadName,
			Namespace: cluster.Namespace,
		},
	}

	operation, err := ctrl.CreateOrUpdate(ctx, cli, payload, func() error {
		payload.Type = addonsv1.ClusterResourceSetSecretType
		payload.Data = map[string][]byte{
			dataKey: manifest,
		}

		if payload.Labels == nil {
			payload.Labels = map[string]string{}
		}

		payload.Labels["cluster.x-k8s.io/cluster-name"] = cluster.Name
		payload.Labels["app.kubernetes.io/managed-by"] = "kommodity"

		payload.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: clusterv1.GroupVersion.String(),
			Kind:       "Cluster",
			Name:       cluster.Name,
			UID:        cluster.UID,
		}}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to upsert CRS payload Secret %s/%s: %w",
			cluster.Namespace, payloadName, err)
	}

	logging.FromContext(ctx).Info("CRS payload Secret operation",
		zap.String("operation", string(operation)),
		zap.String("secret", cluster.Namespace+"/"+payloadName))

	return nil
}
