package reconciler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// azureInfraKind is the AzureCluster infrastructure kind on a CAPI Cluster's
	// infrastructureRef. Only clusters referencing it are materialized.
	azureInfraKind = "AzureCluster"

	// asoSecretSuffix is appended to the cluster name to derive the embedded ARM
	// reconciler's credential Secret — the name CAPZ stamps as
	// `serviceoperator.azure.com/credential-from` on every ASO CR.
	asoSecretSuffix = "-aso-secret"

	// identityClientSecretKey is the data key in the AzureClusterIdentity's
	// referenced Secret that holds the service principal password.
	identityClientSecretKey = "clientSecret"

	// ccmCloudConfigKey is the data key the Azure cloud-controller-manager reads
	// its configuration from.
	ccmCloudConfigKey = "cloud-config"

	// materializerManagedByLabel marks Secrets this reconciler owns. A Secret
	// without it that already carries data is treated as operator-supplied and
	// left untouched (the manual escape hatch).
	materializerManagedByLabel = "kommodity.io/azure-credential-materializer"
	// managedByLabelValue is the value set on materializerManagedByLabel.
	managedByLabelValue = "true"

	// Azure cloud-provider rate-limit/backoff defaults baked into the generated
	// cloud-config (mirroring the proven working configuration).
	ccmBackoffRetries  = 6
	ccmRateLimitQPS    = 3.0
	ccmRateLimitBucket = 10

	// ccmValueStandard is the cloud-config value for loadBalancerSku and vmType.
	ccmValueStandard = "standard"
)

// AzureCredentialMaterializer derives the per-cluster Azure credential Secrets
// (the embedded ARM reconciler's `<cluster>-aso-secret` and the CCM cloud-config
// Secret) from the single operator-supplied AzureClusterIdentity, so an Azure
// cluster only needs the identity's clientSecret Secret to be created by hand.
type AzureCredentialMaterializer struct {
	client.Client
}

// SetupWithManager registers the reconciler with the controller manager.
func (r *AzureCredentialMaterializer) SetupWithManager(
	ctx context.Context,
	mgr ctrl.Manager,
	opt controller.Options,
) error {
	logging.FromContext(ctx).Info("Setting up Azure credential materializer reconciler")

	builder := ctrl.NewControllerManagedBy(mgr).
		Named("kommodity-azure-credential-materializer").
		For(&clusterv1.Cluster{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.clustersForIdentitySecret),
		).
		WithOptions(opt).
		WithEventFilter(predicates.ResourceNotPaused(
			mgr.GetScheme(),
			zapr.NewLogger(logging.FromContext(ctx)),
		))

	err := builder.Complete(r)
	if err != nil {
		return fmt.Errorf("failed setting up Azure credential materializer with manager: %w", err)
	}

	return nil
}

// Reconcile derives and upserts the Azure credential Secrets for one Cluster.
func (r *AzureCredentialMaterializer) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)

	cluster := &clusterv1.Cluster{}

	err := r.Get(ctx, req.NamespacedName, cluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get Cluster %s: %w", req.String(), err)
	}

	if !isAzureCluster(cluster) {
		return ctrl.Result{}, nil
	}

	creds, requeue, err := r.resolveIdentityCredentials(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	if requeue {
		return ctrl.Result{RequeueAfter: RequeueAfter}, nil
	}

	if creds == nil {
		// Non-ServicePrincipal identity (or no identity): nothing to materialize.
		return ctrl.Result{}, nil
	}

	err = r.materializeASOSecret(ctx, cluster, creds)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.materializeCCMSecret(ctx, cluster, creds)
	if err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Reconciled Azure credential Secrets", zap.String("cluster", req.String()))

	return ctrl.Result{}, nil
}

// azureCredentials bundles the service principal fields resolved from the
// AzureClusterIdentity and its owning AzureCluster.
type azureCredentials struct {
	subscriptionID string
	tenantID       string
	clientID       string
	clientSecret   string
	resourceGroup  string
	location       string
	vnetName       string
	vnetRG         string
}

// resolveIdentityCredentials walks Cluster → AzureCluster → AzureClusterIdentity →
// clientSecret Secret. It returns (nil, false, nil) when the identity is not a
// ServicePrincipal (nothing to do), and (_, true, nil) when a dependency is not
// yet present and the caller should requeue.
func (r *AzureCredentialMaterializer) resolveIdentityCredentials(
	ctx context.Context,
	cluster *clusterv1.Cluster,
) (*azureCredentials, bool, error) {
	logger := logging.FromContext(ctx)

	azureCluster := &infrav1.AzureCluster{}
	key := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Spec.InfrastructureRef.Name}

	err := r.Get(ctx, key, azureCluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, true, nil
		}

		return nil, false, fmt.Errorf("failed to get AzureCluster %s: %w", key, err)
	}

	if azureCluster.Spec.IdentityRef == nil {
		logger.Info("AzureCluster has no identityRef; skipping credential materialization",
			zap.String("azureCluster", key.String()))

		return nil, false, nil
	}

	identity := &infrav1.AzureClusterIdentity{}
	identityKey := identityRefKey(azureCluster)

	err = r.Get(ctx, identityKey, identity)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, true, nil
		}

		return nil, false, fmt.Errorf("failed to get AzureClusterIdentity %s: %w", identityKey, err)
	}

	if identity.Spec.Type != infrav1.ServicePrincipal {
		logger.Info("AzureClusterIdentity is not a ServicePrincipal; leaving credentials to the operator",
			zap.String("identity", identityKey.String()),
			zap.String("type", string(identity.Spec.Type)))

		return nil, false, nil
	}

	clientSecret, requeue, err := r.readClientSecret(ctx, identity)
	if err != nil || requeue {
		return nil, requeue, err
	}

	return &azureCredentials{
		subscriptionID: azureCluster.Spec.SubscriptionID,
		tenantID:       identity.Spec.TenantID,
		clientID:       identity.Spec.ClientID,
		clientSecret:   clientSecret,
		resourceGroup:  azureCluster.Spec.ResourceGroup,
		location:       azureCluster.Spec.Location,
		vnetName:       azureCluster.Spec.NetworkSpec.Vnet.Name,
		vnetRG:         azureCluster.Spec.NetworkSpec.Vnet.ResourceGroup,
	}, false, nil
}

// readClientSecret fetches the service principal password from the identity's
// referenced Secret. Returns requeue=true if the Secret is not present yet.
func (r *AzureCredentialMaterializer) readClientSecret(
	ctx context.Context,
	identity *infrav1.AzureClusterIdentity,
) (string, bool, error) {
	secretRef := identity.Spec.ClientSecret
	secret := &corev1.Secret{}

	err := r.Get(ctx, types.NamespacedName{Namespace: secretRef.Namespace, Name: secretRef.Name}, secret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logging.FromContext(ctx).Info("AzureClusterIdentity clientSecret Secret not found yet, requeuing",
				zap.String("secret", secretRef.Namespace+"/"+secretRef.Name))

			return "", true, nil
		}

		return "", false, fmt.Errorf("failed to get identity clientSecret Secret %s/%s: %w",
			secretRef.Namespace, secretRef.Name, err)
	}

	value := secret.Data[identityClientSecretKey]
	if len(value) == 0 {
		return "", false, fmt.Errorf("%w: %s in %s/%s",
			ErrValueNotFoundInSecret, identityClientSecretKey, secretRef.Namespace, secretRef.Name)
	}

	return string(value), false, nil
}

// materializeASOSecret upserts `<cluster>-aso-secret` with the discrete AZURE_*
// keys the embedded ARM reconciler reads (and CAPZ's credential-from points at).
func (r *AzureCredentialMaterializer) materializeASOSecret(
	ctx context.Context,
	cluster *clusterv1.Cluster,
	creds *azureCredentials,
) error {
	data := map[string][]byte{
		"AZURE_SUBSCRIPTION_ID": []byte(creds.subscriptionID),
		"AZURE_TENANT_ID":       []byte(creds.tenantID),
		"AZURE_CLIENT_ID":       []byte(creds.clientID),
		"AZURE_CLIENT_SECRET":   []byte(creds.clientSecret),
	}

	return r.upsertManagedSecret(ctx, cluster, cluster.Name+asoSecretSuffix, data)
}

// materializeCCMSecret upserts the cloud-config Secret named by the cluster's
// CCM annotation, which CCMCRSReconciler then delivers to the workload cluster.
// It is a no-op when CCM is not enabled (no annotation).
func (r *AzureCredentialMaterializer) materializeCCMSecret(
	ctx context.Context,
	cluster *clusterv1.Cluster,
	creds *azureCredentials,
) error {
	secretName := cluster.Annotations[AnnotationCCMSecretName]
	if secretName == "" {
		return nil
	}

	cloudConfig, err := buildAzureCloudConfig(cluster.Name, creds)
	if err != nil {
		return err
	}

	return r.upsertManagedSecret(ctx, cluster, secretName, map[string][]byte{ccmCloudConfigKey: cloudConfig})
}

// upsertManagedSecret creates or updates a Secret owned by the Cluster, refusing
// to overwrite a pre-existing Secret that is not managed by this reconciler (the
// operator-supplied escape hatch).
func (r *AzureCredentialMaterializer) upsertManagedSecret(
	ctx context.Context,
	cluster *clusterv1.Cluster,
	name string,
	data map[string][]byte,
) error {
	existing := &corev1.Secret{}

	err := r.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: name}, existing)
	if err == nil && len(existing.Data) > 0 && existing.Labels[materializerManagedByLabel] != managedByLabelValue {
		logging.FromContext(ctx).Info("Secret exists with data and is not materializer-managed; leaving it untouched",
			zap.String("secret", cluster.Namespace+"/"+name))

		return nil
	}

	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get Secret %s/%s: %w", cluster.Namespace, name, err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cluster.Namespace},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		secret.Type = corev1.SecretTypeOpaque
		secret.Data = data

		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}

		secret.Labels[materializerManagedByLabel] = managedByLabelValue
		secret.Labels["cluster.x-k8s.io/cluster-name"] = cluster.Name
		secret.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: clusterv1.GroupVersion.String(),
			Kind:       "Cluster",
			Name:       cluster.Name,
			UID:        cluster.UID,
		}}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to upsert materialized Secret %s/%s: %w", cluster.Namespace, name, err)
	}

	return nil
}

// clustersForIdentitySecret enqueues Azure Clusters whose AzureClusterIdentity
// references the changed Secret, so a clientSecret rotation re-materializes the
// derived Secrets. Secrets this reconciler manages are ignored to avoid a loop.
func (r *AzureCredentialMaterializer) clustersForIdentitySecret(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok || secret.Labels[materializerManagedByLabel] == managedByLabelValue {
		return nil
	}

	clusters := &clusterv1.ClusterList{}

	err := r.List(ctx, clusters, client.InNamespace(secret.Namespace))
	if err != nil {
		logging.FromContext(ctx).Error("Failed to list Clusters for identity Secret watch",
			zap.String("secret", secret.Namespace+"/"+secret.Name), zap.Error(err))

		return nil
	}

	requests := make([]reconcile.Request, 0)

	for i := range clusters.Items {
		cluster := &clusters.Items[i]
		if r.identitySecretMatches(ctx, cluster, secret.Name) {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
		}
	}

	return requests
}

// identitySecretMatches reports whether the given Cluster's AzureClusterIdentity
// references a clientSecret Secret with the given name.
func (r *AzureCredentialMaterializer) identitySecretMatches(
	ctx context.Context,
	cluster *clusterv1.Cluster,
	secretName string,
) bool {
	if !isAzureCluster(cluster) {
		return false
	}

	azureCluster := &infrav1.AzureCluster{}
	key := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Spec.InfrastructureRef.Name}

	err := r.Get(ctx, key, azureCluster)
	if err != nil || azureCluster.Spec.IdentityRef == nil {
		return false
	}

	identity := &infrav1.AzureClusterIdentity{}

	err = r.Get(ctx, identityRefKey(azureCluster), identity)
	if err != nil {
		return false
	}

	return identity.Spec.ClientSecret.Name == secretName
}

// isAzureCluster reports whether a CAPI Cluster's infrastructureRef points at an
// AzureCluster.
func isAzureCluster(cluster *clusterv1.Cluster) bool {
	return cluster.Spec.InfrastructureRef != nil && cluster.Spec.InfrastructureRef.Kind == azureInfraKind
}

// identityRefKey resolves the namespaced name of an AzureCluster's identity,
// defaulting the namespace to the AzureCluster's when the ref omits it.
func identityRefKey(azureCluster *infrav1.AzureCluster) types.NamespacedName {
	ref := azureCluster.Spec.IdentityRef
	namespace := ref.Namespace

	if namespace == "" {
		namespace = azureCluster.Namespace
	}

	return types.NamespacedName{Namespace: namespace, Name: ref.Name}
}

// buildAzureCloudConfig renders the Azure cloud-provider cloud-config JSON from
// the resolved credentials and the chart's deterministic resource naming
// (`<cluster>-node-{nsg,subnet,routetable}`). The field set mirrors the proven
// working configuration.
func buildAzureCloudConfig(clusterName string, creds *azureCredentials) ([]byte, error) {
	config := map[string]any{
		"tenantId":                     creds.tenantID,
		"subscriptionId":               creds.subscriptionID,
		"resourceGroup":                creds.resourceGroup,
		"location":                     creds.location,
		"useManagedIdentityExtension":  false,
		"aadClientId":                  creds.clientID,
		"aadClientSecret":              creds.clientSecret,
		"loadBalancerSku":              ccmValueStandard,
		"vmType":                       ccmValueStandard,
		"useInstanceMetadata":          true,
		"securityGroupName":            clusterName + "-node-nsg",
		"securityGroupResourceGroup":   creds.resourceGroup,
		"vnetName":                     creds.vnetName,
		"vnetResourceGroup":            creds.vnetRG,
		"subnetName":                   clusterName + "-node-subnet",
		"routeTableName":               clusterName + "-node-routetable",
		"cloudProviderBackoff":         true,
		"cloudProviderBackoffRetries":  ccmBackoffRetries,
		"cloudProviderRateLimit":       true,
		"cloudProviderRateLimitQPS":    ccmRateLimitQPS,
		"cloudProviderRateLimitBucket": ccmRateLimitBucket,
	}

	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Azure cloud-config: %w", err)
	}

	return data, nil
}
