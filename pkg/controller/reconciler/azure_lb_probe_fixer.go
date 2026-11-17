package reconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/zapr"
	"github.com/kommodity-io/kommodity/pkg/controller/reconciler/azurearm"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// lbProbeFixInterval is how often a reconciled AzureCluster re-verifies its
	// apiserver LB probe. It guards against a future CAPZ provider bump whose
	// LB reconcile starts enforcing its HTTPS probe again (upstream added
	// probe-merging that overwrites same-named probes), or an LB recreated by
	// a control-plane rollout.
	lbProbeFixInterval = 10 * time.Minute

	// lbProbeName is the name CAPZ gives to the apiserver LB health probe
	// (azure/services/loadbalancers, `httpsProbe`). The `LBRuleHTTPS` load
	// balancing rule references the probe by name, so the probe is converted
	// in place rather than replaced to keep the rule's reference valid.
	lbProbeName = "HTTPSProbe"

	// lbProbeAPIVersion is the api-version for direct ARM probe PUTs. It
	// matches the one CAPZ's armnetwork v4 SDK client generates its load
	// balancer calls for ("Generated from API version 2023-05-01").
	lbProbeAPIVersion = "2023-05-01"

	// lbProbePathHTTPS is the request path CAPZ configures on the HTTPS probe.
	lbProbePathHTTPS = "/readyz"

	// lbProbeProtocolTCP is the protocol value that replaces CAPZ's HTTPS
	// probe: a successful TCP connect to the apiserver port proves the control
	// plane endpoint is up without authenticating.
	lbProbeProtocolTCP = "Tcp"

	// lbProbeProtocolHTTPS is the protocol value CAPZ configures.
	lbProbeProtocolHTTPS = "Https"

	// lbProbeDefaultIntervalSeconds is the probe interval CAPZ configures;
	// used when the live probe omits the field.
	lbProbeDefaultIntervalSeconds = 15

	// lbProbeDefaultThreshold is the probe threshold CAPZ configures; used
	// when the live probe omits the field.
	lbProbeDefaultThreshold = 1

	// lbProbeFixIdentityClientSecretKey is the data key in the
	// AzureClusterIdentity's referenced Secret that holds the service
	// principal password.
	lbProbeFixIdentityClientSecretKey = "clientSecret"
)

// AzureLBProbeFixer reconciles the kube-apiserver load balancer health probe
// of Azure clusters to TCP. CAPZ hardcodes an HTTPS GET /readyz probe on the
// apiserver LB (azure/services/loadbalancers/spec.go getProbes); with
// kube-apiserver's anonymous auth disabled (the secure default), /readyz
// answers 401 to the unauthenticated probe, every control plane backend fails
// the probe, Azure drops port 6443 from rotation and control plane machines
// never get a NodeRef (NodeStartupTimeout). CAPZ exposes no field to override
// the probe, so this reconciler converts the probe to TCP: a successful TCP
// connect to the apiserver port proves the control plane endpoint is up
// without authenticating.
//
// LB probes are child objects of the LB resource; the ARM API has no probe
// sub-resource PUT (the armnetwork SDK's probes client only has Get/List),
// so the conversion is a read-modify-write of the whole LB: parse the GET
// body, flip the HTTPSProbe entry's protocol in place (LB rules reference the
// probe by name, so the rule's reference stays valid), and PUT the LB with an
// If-Match etag precondition — the same optimistic-concurrency pattern CAPZ
// itself uses for LB updates. The conversion persists across CAPZ reconciles
// on the pinned provider version (v1.21.0): its existing-LB path merges
// probes by name and never rewrites an existing probe's properties. If a
// future provider bump starts enforcing the HTTPS probe again, this
// reconciler re-applies TCP within lbProbeFixInterval.
type AzureLBProbeFixer struct {
	client.Client

	// armClient issues authenticated ARM requests. Built once at setup; the
	// per-cluster service principal is resolved per-reconcile from the
	// AzureClusterIdentity chain (same source the credential materializer
	// uses) and wrapped into a fresh ARM client each reconcile.
	armClient azurearm.GenericARMClientFactory
}

// SetupWithManager registers the reconciler with the controller manager.
func (r *AzureLBProbeFixer) SetupWithManager(
	ctx context.Context,
	mgr ctrl.Manager,
	opt controller.Options,
) error {
	logging.FromContext(ctx).Info("Setting up Azure LB probe fixer reconciler")

	builder := ctrl.NewControllerManagedBy(mgr).
		Named("kommodity-azure-lb-probe-fixer").
		For(&infrav1.AzureCluster{}).
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
		return fmt.Errorf("failed setting up Azure LB probe fixer with manager: %w", err)
	}

	return nil
}

// Reconcile converts the HTTPS /readyz probe on one AzureCluster's apiserver
// LB to TCP when needed, then requeues on the drift-guard cadence.
func (r *AzureLBProbeFixer) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logging.FromContext(ctx).With(zap.String("azureCluster", req.String()))

	azureCluster := &infrav1.AzureCluster{}

	err := r.Get(ctx, req.NamespacedName, azureCluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get AzureCluster %s: %w", req.String(), err)
	}

	if r.armClient == nil {
		// No Azure credential factory configured (e.g. Azure support disabled
		// for this kommodity instance): observe-only.
		logger.Info("No Azure credential factory configured; skipping LB probe fix")

		return ctrl.Result{}, nil
	}

	if azureCluster.GetDeletionTimestamp() != nil {
		// Cluster is being torn down; its LB is going away with it.
		logger.Info("AzureCluster is deleting; skipping LB probe fix")

		return ctrl.Result{}, nil
	}

	armClient, err := r.resolveCredentials(ctx, azureCluster)
	if err != nil {
		return r.handleCredentialError(logger, err)
	}

	fixed, retryAfter, err := r.reconcileLBProbe(ctx, armClient, azureCluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	if fixed {
		logger.Info("Converted apiserver LB probe to TCP")
	}

	if retryAfter > 0 {
		// ARM rate limited us; back off for the requested duration.
		logger.Info("ARM rate limited; requeueing", zap.Duration("retryAfter", retryAfter))

		return ctrl.Result{RequeueAfter: retryAfter}, nil
	}

	return ctrl.Result{RequeueAfter: lbProbeFixInterval}, nil
}

// handleCredentialError classifies a resolveCredentials failure: pending
// credentials requeue briefly, unresolvable inputs (e.g. a non-ServicePrincipal
// identity whose probe the operator must manage) are observe-only, anything
// else is a real error.
func (r *AzureLBProbeFixer) handleCredentialError(
	logger *zap.Logger,
	err error,
) (ctrl.Result, error) {
	if isLBProbeFixCredentialPending(err) {
		logger.Info("Azure credentials not ready; requeueing", zap.Error(err))

		return ctrl.Result{RequeueAfter: RequeueAfter}, nil
	}

	if isLBProbeFixUnresolvable(err) {
		// Observe-only like the credential materializer instead of
		// error-looping forever.
		logger.Info("LB probe fix unresolvable; leaving probe to the operator", zap.Error(err))

		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, err
}

// reconcileLBProbe fetches the apiserver LB, converts the HTTPS /readyz probe
// to TCP when it still matches CAPZ's shape, and PUTs the modified LB back
// under an etag precondition. It reports whether a change was made. retryAfter
// is positive when ARM rate limited the call and the caller should requeue for
// that duration instead of the drift-guard cadence. A missing LB (cluster
// still rolling out) is not an error: the next periodic reconcile retries.
func (r *AzureLBProbeFixer) reconcileLBProbe(
	ctx context.Context,
	armClient azurearm.GenericARMClient,
	azureCluster *infrav1.AzureCluster,
) (bool, time.Duration, error) {
	lbName := apiserverLBName(azureCluster)

	lbARMID := azurearm.LoadBalancerARMID(azureCluster.Spec.SubscriptionID, azureCluster.Spec.ResourceGroup, lbName)

	status, body, retryAfter, err := armClient.Get(ctx, lbARMID, lbProbeAPIVersion)
	if err != nil {
		return false, 0, fmt.Errorf("getting load balancer %s: %w", lbARMID, err)
	}

	if status == http.StatusTooManyRequests {
		return false, retryAfter, nil
	}

	etag, mutated, err := lbProbeGetOutcome(status, body, lbARMID)
	if err != nil || mutated == nil {
		return false, retryAfter, err
	}

	putStatus, _, putRetryAfter, err := armClient.Put(ctx, lbARMID, lbProbeAPIVersion, etag, mutated)
	if err != nil {
		return false, 0, fmt.Errorf("putting load balancer %s: %w", lbARMID, err)
	}

	if putStatus == http.StatusTooManyRequests {
		return false, putRetryAfter, nil
	}

	if putStatus == http.StatusPreconditionFailed {
		// The LB changed between our GET and PUT (e.g. CAPZ concurrently
		// appended a backend). Drop the result; the next reconcile re-reads
		// and retries on a fresh etag.
		return false, 0, nil
	}

	// Any 2xx accepts the update; Azure provisions LB changes asynchronously.
	if putStatus < 200 || putStatus >= 300 {
		return false, 0, fmt.Errorf(
			"%w: HTTP %d putting load balancer %s",
			azurearm.ErrARMTerminal, putStatus, lbARMID,
		)
	}

	return true, 0, nil
}

// lbProbeGetOutcome interprets the LB GET result. It returns the LB's etag
// plus the converted full LB body to PUT (nil body when nothing needs
// changing), translating terminal statuses into errors and treating a missing
// LB (cluster still rolling out) as a no-op.
func lbProbeGetOutcome(status int, body []byte, lbARMID string) (string, []byte, error) {
	switch {
	case status == http.StatusNotFound:
		// LB not created yet (cluster still rolling out); retry later.
		return "", nil, nil
	case status != http.StatusOK:
		return "", nil, fmt.Errorf(
			"%w: HTTP %d getting load balancer %s",
			azurearm.ErrARMTerminal, status, lbARMID,
		)
	}

	etag, mutated, err := apiserverHTTPSProbeFromLB(body)
	if err != nil {
		return "", nil, fmt.Errorf("parsing load balancer %s: %w", lbARMID, err)
	}

	// Probe absent, already TCP, or a user-modified path: nothing to do.
	return etag, mutated, nil
}

// resolveCredentials walks AzureCluster → AzureClusterIdentity → clientSecret
// Secret and returns an ARM client bound to the service principal. Returns a
// pending-credentials error when the identity chain is not complete yet.
func (r *AzureLBProbeFixer) resolveCredentials(
	ctx context.Context,
	azureCluster *infrav1.AzureCluster,
) (azurearm.GenericARMClient, error) {
	if r.armClient == nil {
		return nil, fmt.Errorf("%w: no Azure credential factory", ErrLBProbeFixUnresolvable)
	}

	if azureCluster.Spec.IdentityRef == nil {
		return nil, fmt.Errorf(
			"%w: AzureCluster %s/%s has no identityRef",
			ErrLBProbeFixUnresolvable, azureCluster.Namespace, azureCluster.Name,
		)
	}

	identity, err := r.servicePrincipalIdentity(ctx, azureCluster)
	if err != nil {
		return nil, err
	}

	clientSecret, err := r.identityClientSecret(ctx, identity)
	if err != nil {
		return nil, err
	}

	armClient, err := r.armClient(
		identity.Spec.TenantID,
		identity.Spec.ClientID,
		clientSecret,
	)
	if err != nil {
		return nil, fmt.Errorf("building ARM client: %w", err)
	}

	return armClient, nil
}

// servicePrincipalIdentity resolves and validates the AzureClusterIdentity
// referenced by the cluster: it must exist and be of type ServicePrincipal.
func (r *AzureLBProbeFixer) servicePrincipalIdentity(
	ctx context.Context,
	azureCluster *infrav1.AzureCluster,
) (*infrav1.AzureClusterIdentity, error) {
	identity := &infrav1.AzureClusterIdentity{}

	err := r.Get(ctx, identityRefKey(azureCluster), identity)
	if apierrors.IsNotFound(err) {
		return nil, fmt.Errorf(
			"%w: identity %s not found",
			ErrLBProbeFixCredentialsPending, identityRefKey(azureCluster),
		)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get AzureClusterIdentity %s: %w", identityRefKey(azureCluster), err)
	}

	if identity.Spec.Type != infrav1.ServicePrincipal {
		// Managed identities and workload identities authenticate via the node
		// or pod itself, not a client secret, so this reconciler cannot build
		// an ARM client for them. Like the credential materializer, leave the
		// probe to the operator and stay quiet rather than error-looping.
		return nil, fmt.Errorf(
			"%w: identity %s is %s, not ServicePrincipal",
			ErrLBProbeFixUnresolvable, identityRefKey(azureCluster), identity.Spec.Type,
		)
	}

	return identity, nil
}

// identityClientSecret reads the clientSecret value from the identity's
// referenced Secret.
func (r *AzureLBProbeFixer) identityClientSecret(
	ctx context.Context,
	identity *infrav1.AzureClusterIdentity,
) (string, error) {
	secretRef := identity.Spec.ClientSecret
	secret := &corev1.Secret{}

	err := r.Get(ctx, types.NamespacedName{Namespace: secretRef.Namespace, Name: secretRef.Name}, secret)
	if apierrors.IsNotFound(err) {
		return "", fmt.Errorf(
			"%w: clientSecret %s/%s not found",
			ErrLBProbeFixCredentialsPending, secretRef.Namespace, secretRef.Name,
		)
	}

	if err != nil {
		return "", fmt.Errorf(
			"failed to get identity clientSecret Secret %s/%s: %w",
			secretRef.Namespace, secretRef.Name, err,
		)
	}

	clientSecret := string(secret.Data[lbProbeFixIdentityClientSecretKey])
	if len(clientSecret) == 0 {
		return "", fmt.Errorf(
			"%w: %s in %s/%s",
			ErrValueNotFoundInSecret, lbProbeFixIdentityClientSecretKey, secretRef.Namespace, secretRef.Name,
		)
	}

	return clientSecret, nil
}

// clustersForIdentitySecret maps an identity clientSecret Secret to the
// AzureClusters using it, so a credential rotation re-runs the probe fix.
func (r *AzureLBProbeFixer) clustersForIdentitySecret(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}

	clusters := &clusterv1.ClusterList{}

	err := r.List(ctx, clusters, client.InNamespace(secret.GetNamespace()))
	if err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0, len(clusters.Items))

	for i := range clusters.Items {
		cluster := &clusters.Items[i]

		if !isAzureCluster(cluster) {
			continue
		}

		azureCluster := &infrav1.AzureCluster{}

		err := r.Get(ctx, types.NamespacedName{
			Namespace: cluster.Spec.InfrastructureRef.Namespace,
			Name:      cluster.Spec.InfrastructureRef.Name,
		}, azureCluster)
		if err != nil {
			continue
		}

		if azureCluster.Spec.IdentityRef == nil {
			continue
		}

		identity := &infrav1.AzureClusterIdentity{}

		err = r.Get(ctx, identityRefKey(azureCluster), identity)
		if err != nil || identity.Spec.ClientSecret.Name != secret.GetName() {
			continue
		}

		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(azureCluster)})
	}

	return requests
}

// apiserverLBName derives the apiserver LB name for an AzureCluster. The
// kommodity chart always sets the name (`<release>-public-lb` /
// `<release>-internal-lb`); the fallback mirrors CAPZ's defaulting for
// clusters provisioned without an explicit name.
func apiserverLBName(azureCluster *infrav1.AzureCluster) string {
	lbSpec := azureCluster.Spec.NetworkSpec.APIServerLB
	if lbSpec != nil && lbSpec.Name != "" {
		return lbSpec.Name
	}

	if lbSpec != nil && lbSpec.Type == infrav1.Internal {
		return azureCluster.GetName() + "-internal-lb"
	}

	return azureCluster.GetName() + "-public-lb"
}

// apiserverHTTPSProbeFromLB finds the apiserver probe in an ARM LB GET body
// and converts it to TCP in place, returning the LB's etag (for the If-Match
// precondition on the follow-up PUT) and the full mutated body. The body is
// decoded into a generic map so every field CAPZ set on the LB — rules,
// backends, frontend IPs, unrelated probes — survives the round-trip into the
// PUT unchanged. A nil probe with a nil error means no conversion is needed:
// the probe is absent, already TCP, or carries a user-modified request path.
func apiserverHTTPSProbeFromLB(lbBody []byte) (string, []byte, error) {
	var raw map[string]any

	err := json.Unmarshal(lbBody, &raw)
	if err != nil {
		return "", nil, fmt.Errorf("unmarshaling load balancer body: %w", err)
	}

	etag, _ := raw["etag"].(string)

	probes := rawProbes(raw)
	if probes == nil {
		return "", nil, nil
	}

	for i := range probes {
		entry, isMap := probes[i].(map[string]any)
		if !isMap || !strings.EqualFold(stringField(entry["name"]), lbProbeName) {
			continue
		}

		props, hasProps := entry["properties"].(map[string]any)
		if !hasProps {
			return "", nil, nil
		}

		isHTTPS := strings.EqualFold(stringField(props["protocol"]), lbProbeProtocolHTTPS)
		isReadyz := stringField(props["requestPath"]) == lbProbePathHTTPS

		if !isHTTPS || !isReadyz {
			// Already TCP, or a user-modified path: nothing to do.
			return "", nil, nil
		}

		props["protocol"] = lbProbeProtocolTCP
		delete(props, "requestPath")

		mutated, err := json.Marshal(raw)
		if err != nil {
			return "", nil, fmt.Errorf("re-marshaling load balancer body: %w", err)
		}

		return etag, mutated, nil
	}

	return "", nil, nil
}

// rawProbes reads the properties.probes array out of a generic decoded LB body.
func rawProbes(lb map[string]any) []any {
	props, ok := lb["properties"].(map[string]any)
	if !ok {
		return nil
	}

	probes, _ := props["probes"].([]any)

	return probes
}

// stringField reads an optional string field from a decoded JSON object.
func stringField(v any) string {
	s, _ := v.(string)

	return s
}

// isLBProbeFixCredentialPending reports whether an error means the identity
// chain is not yet complete and the reconcile should retry shortly.
func isLBProbeFixCredentialPending(err error) bool {
	return errors.Is(err, ErrLBProbeFixCredentialsPending) || errors.Is(err, ErrValueNotFoundInSecret)
}

// isLBProbeFixUnresolvable reports whether an error means the fix input is
// permanently unresolvable for this cluster (e.g. a non-ServicePrincipal
// identity) and the reconcile should give up quietly instead of error-looping.
func isLBProbeFixUnresolvable(err error) bool {
	return errors.Is(err, ErrLBProbeFixUnresolvable)
}
