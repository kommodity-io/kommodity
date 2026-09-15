package azurearm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// finalizerName guards Azure resources owned by the embedded reconciler.
	// It is intentionally distinct from ASO's own finalizer so the two never
	// interfere during migration.
	finalizerName = "kommodity.io/azurearm-finalizer"

	// armSpecHashAnnotation stores a SHA-256 truncated fingerprint of the
	// last-applied ARM spec body. When the fingerprint changes the reconciler
	// re-PUTs the resource so spec drift is caught without a full diff.
	armSpecHashAnnotation = "kommodity.io/arm-spec-hash"

	// deletionStartedAnnotation records (RFC3339) when the reconciler first
	// observed an outstanding Azure deletion for a resource. It backs the
	// deletion grace period so teardown is never wedged indefinitely.
	deletionStartedAnnotation = "kommodity.io/azurearm-delete-started-at"

	// reconcilingRequeueInterval is how often we poll an in-flight ARM operation.
	reconcilingRequeueInterval = 15 * time.Second
	// driftRequeueInterval is the cadence for re-checking a converged resource.
	driftRequeueInterval = 10 * time.Minute

	provisioningStateSucceeded = "Succeeded"
	provisioningStateFailed    = "Failed"
	provisioningStateCanceled  = "Canceled"
)

// Reconciler is a generic controller that materializes a single ASO custom
// resource kind into Azure via ARM. One instance is registered per managed GVK.
type Reconciler struct {
	client.Client

	controllerName string
	newObj         func() genruntime.ARMMetaObject
	armIDFor       armIDFunc
	creds          *credentialProvider

	// deletionGracePeriod bounds how long we wait for Azure to remove a managed
	// resource before releasing the finalizer anyway. Non-positive disables it.
	deletionGracePeriod time.Duration
}

// SetupWithManager registers this reconciler for its kind with the manager.
func (r *Reconciler) SetupWithManager(
	ctx context.Context,
	mgr ctrl.Manager,
	opt controller.Options,
) error {
	logging.FromContext(ctx).Info("Setting up embedded Azure ARM controller",
		zap.String("controller", r.controllerName))

	err := ctrl.NewControllerManagedBy(mgr).
		Named(r.controllerName).
		For(r.newObj()).
		WithOptions(opt).
		Complete(r)
	if err != nil {
		return fmt.Errorf("setting up %s controller: %w", r.controllerName, err)
	}

	return nil
}

// Reconcile drives a single ASO resource towards its desired Azure state.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logging.FromContext(ctx).With(
		zap.String("controller", r.controllerName),
		zap.String("resource", req.String()))

	obj := r.newObj()

	err := r.Get(ctx, req.NamespacedName, obj)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("getting resource: %w", err)
	}

	policy := reconcilePolicyFor(obj)

	if !obj.GetDeletionTimestamp().IsZero() {
		result, deleteErr := r.reconcileDelete(ctx, obj, policy)
		if apierrors.IsConflict(deleteErr) {
			logger.Info("Conflict on delete; requeueing")

			return ctrl.Result{Requeue: true}, nil
		}

		return result, deleteErr
	}

	requeue, finalizerErr := r.ensureFinalizers(ctx, obj, policy, logger)
	if finalizerErr != nil {
		return ctrl.Result{}, finalizerErr
	}

	if requeue {
		return ctrl.Result{Requeue: true}, nil
	}

	if !policy.AllowsModify() {
		logger.Info("Reconcile policy does not allow modification; reflecting Azure state only",
			zap.String("policy", string(policy)))

		return r.reconcileReadOnly(ctx, obj)
	}

	result, normalErr := r.reconcileNormal(ctx, obj)
	if normalErr != nil && apierrors.IsConflict(normalErr) {
		logger.Info("Conflict during reconcile; requeueing")

		return ctrl.Result{Requeue: true}, nil
	}

	return result, normalErr
}

// ensureFinalizers reconciles the resource's finalizers to match its reconcile
// policy. Our finalizer is only carried on resources we actually delete in Azure
// (policy "manage"); for "skip"/"detach-on-delete" resources it is pure liability
// — we never issue an ARM DELETE for them, so a lingering finalizer can only wedge
// CR/namespace teardown — and is therefore removed. Any stale ASO finalizer left
// from a previous sidecar-era deployment is stripped regardless. Returns (true,
// nil) when a mutation was made and the caller should requeue, (false, nil) when no
// change was needed, or (false, err) when the update failed.
func (r *Reconciler) ensureFinalizers(
	ctx context.Context,
	obj genruntime.ARMMetaObject,
	policy annotations.ReconcilePolicyValue,
	logger *zap.Logger,
) (bool, error) {
	changed := false

	if policy.AllowsDelete() {
		if controllerutil.AddFinalizer(obj, finalizerName) {
			changed = true
		}
	} else if controllerutil.RemoveFinalizer(obj, finalizerName) {
		logger.Info("Removing azurearm finalizer from non-managed resource",
			zap.String("policy", string(policy)))

		changed = true
	}

	// Clusters previously managed by the ASO Docker sidecar carry this finalizer;
	// strip it so teardown is not blocked by a reconciler that no longer runs.
	if controllerutil.RemoveFinalizer(obj, genruntime.ReconcilerFinalizer) {
		logger.Info("Removing stale ASO finalizer from resource")

		changed = true
	}

	if !changed {
		return false, nil
	}

	err := r.Update(ctx, obj)
	if err != nil {
		if apierrors.IsConflict(err) {
			return true, nil
		}

		return false, fmt.Errorf("updating finalizers: %w", err)
	}

	return true, nil
}

func (r *Reconciler) reconcileNormal(
	ctx context.Context,
	obj genruntime.ARMMetaObject,
) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)

	creds, err := r.creds.resolve(ctx, obj)
	if err != nil {
		if isTransientCredentialError(err) {
			logger.Info("Azure credentials not ready; requeueing", zap.Error(err))

			return r.requeueReconciling(ctx, obj)
		}

		return ctrl.Result{}, fmt.Errorf("resolving credentials: %w", err)
	}

	armID, err := r.armIDFor(ctx, r.Client, obj, creds.subscriptionID)
	if err != nil {
		if errors.Is(err, ErrARMIDUnresolvable) {
			logger.Info("ARM ID not yet resolvable; requeueing", zap.Error(err))

			return r.requeueReconciling(ctx, obj)
		}

		return ctrl.Result{}, fmt.Errorf("resolving ARM ID: %w", err)
	}

	apiVersion := obj.GetAPIVersion()

	getResp, err := creds.armClient.get(ctx, armID, apiVersion)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("getting resource %s: %w", armID, err)
	}

	if getResp.statusCode == http.StatusTooManyRequests {
		logger.Info("ARM rate limited on GET; requeueing",
			zap.Duration("retryAfter", getResp.retryAfter))

		return ctrl.Result{RequeueAfter: getResp.retryAfter}, nil
	}

	if getResp.statusCode == http.StatusNotFound {
		return r.putResource(ctx, obj, creds, armID, apiVersion)
	}

	return r.reconcileExisting(ctx, obj, creds, armID, apiVersion, getResp.body)
}

// reconcileReadOnly implements the skip-policy path: resolve credentials, GET the
// resource from ARM, and populate the status/resource-id annotation. Does not PUT
// or DELETE the Azure resource. This is required for resources CAPZ marks with
// reconcile-policy: skip (e.g. all ResourceGroup CRs) so that the ARM resource ID
// annotation is set and dependent resources can resolve their owner ARM IDs.
func (r *Reconciler) reconcileReadOnly(ctx context.Context, obj genruntime.ARMMetaObject) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)

	creds, err := r.creds.resolve(ctx, obj)
	if err != nil {
		if isTransientCredentialError(err) {
			logger.Info("Azure credentials not ready; requeueing", zap.Error(err))

			return r.requeueReconciling(ctx, obj)
		}

		return ctrl.Result{}, fmt.Errorf("resolving credentials: %w", err)
	}

	armID, err := r.armIDFor(ctx, r.Client, obj, creds.subscriptionID)
	if err != nil {
		if errors.Is(err, ErrARMIDUnresolvable) {
			logger.Info("ARM ID not yet resolvable; requeueing", zap.Error(err))

			return r.requeueReconciling(ctx, obj)
		}

		return ctrl.Result{}, fmt.Errorf("resolving ARM ID: %w", err)
	}

	getResp, err := creds.armClient.get(ctx, armID, obj.GetAPIVersion())
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("getting resource %s: %w", armID, err)
	}

	if getResp.statusCode == http.StatusTooManyRequests {
		logger.Info("ARM rate limited on GET; requeueing",
			zap.Duration("retryAfter", getResp.retryAfter))

		return ctrl.Result{RequeueAfter: getResp.retryAfter}, nil
	}

	if getResp.statusCode == http.StatusNotFound {
		setNotFound(obj)

		err = r.Status().Update(ctx, obj)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("updating not-found status: %w", err)
		}

		return ctrl.Result{RequeueAfter: driftRequeueInterval}, nil
	}

	return r.reconcileSucceeded(ctx, obj, armID, getResp.body)
}

// reconcileExisting evaluates an existing Azure resource's provisioning state and
// drives it to Ready, re-applies on terminal failure or spec drift, or requeues
// while in flight.
func (r *Reconciler) reconcileExisting(
	ctx context.Context,
	obj genruntime.ARMMetaObject,
	creds *azureCredentials,
	armID string,
	apiVersion string,
	body []byte,
) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)
	state := parseProvisioningState(body)

	switch {
	case strings.EqualFold(state, provisioningStateSucceeded):
		if r.specDrifted(obj) {
			logger.Info("Spec drift detected; re-applying")

			return r.putResource(ctx, obj, creds, armID, apiVersion)
		}

		return r.reconcileSucceeded(ctx, obj, armID, body)

	case strings.EqualFold(state, provisioningStateFailed),
		strings.EqualFold(state, provisioningStateCanceled):
		logger.Info("Azure resource in terminal-failure state; re-applying",
			zap.String("provisioningState", state))

		return r.putResource(ctx, obj, creds, armID, apiVersion)

	default:
		logger.Info("Azure resource still reconciling", zap.String("provisioningState", state))

		return r.requeueReconciling(ctx, obj)
	}
}

// specDrifted reports whether the desired spec has changed since the last
// successful PUT by comparing the current spec hash with the stored annotation.
// Returns false if the hash cannot be computed (safe default: no spurious re-PUT).
func (r *Reconciler) specDrifted(obj genruntime.ARMMetaObject) bool {
	currentHash, err := computeSpecHash(obj)
	if err != nil {
		return false
	}

	storedHash := obj.GetAnnotations()[armSpecHashAnnotation]

	return storedHash != "" && storedHash != currentHash
}

// isTransientCredentialError reports whether a credential resolution error should
// be treated as transient (requeue) rather than terminal.
func isTransientCredentialError(err error) bool {
	return errors.Is(err, ErrCredentialSecretNotFound) || errors.Is(err, ErrCredentialSecretIncomplete)
}

func (r *Reconciler) putResource(
	ctx context.Context,
	obj genruntime.ARMMetaObject,
	creds *azureCredentials,
	armID string,
	apiVersion string,
) (ctrl.Result, error) {
	armBody, err := r.buildARMBody(obj)
	if err != nil {
		return ctrl.Result{}, err
	}

	putResp, err := creds.armClient.put(ctx, armID, apiVersion, armBody)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("putting resource %s: %w", armID, err)
	}

	if putResp.statusCode == http.StatusTooManyRequests {
		logging.FromContext(ctx).Info("ARM rate limited on PUT; requeueing",
			zap.Duration("retryAfter", putResp.retryAfter))

		return ctrl.Result{RequeueAfter: putResp.retryAfter}, nil
	}

	if isTerminalARMError(putResp.statusCode) {
		return r.handleTerminalPUTError(ctx, obj, putResp.statusCode)
	}

	r.storeSpecHash(ctx, obj)

	return r.requeueReconciling(ctx, obj)
}

// buildARMBody constructs the ARM request body for the given object.
func (r *Reconciler) buildARMBody(obj genruntime.ARMMetaObject) (any, error) {
	details, err := buildResolvedDetails(obj)
	if err != nil {
		return nil, fmt.Errorf("building resolved details: %w", err)
	}

	spec := obj.GetSpec()

	converter, ok := spec.(genruntime.ToARMConverter)
	if !ok {
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedResourceType, spec)
	}

	armBody, err := converter.ConvertToARM(details)
	if err != nil {
		return nil, fmt.Errorf("converting spec to ARM: %w", err)
	}

	return armBody, nil
}

// isTerminalARMError reports whether an HTTP status code from a PUT represents a
// terminal error that will not resolve by retrying (client-error range, excluding
// 404 and 409 which are handled separately).
func isTerminalARMError(statusCode int) bool {
	return statusCode >= http.StatusBadRequest &&
		statusCode < http.StatusInternalServerError &&
		statusCode != http.StatusNotFound &&
		statusCode != http.StatusConflict &&
		statusCode != http.StatusTooManyRequests
}

// handleTerminalPUTError marks the resource as permanently failed and returns a
// terminal error so the controller does not spin on an unfixable spec.
func (r *Reconciler) handleTerminalPUTError(
	ctx context.Context,
	obj genruntime.ARMMetaObject,
	statusCode int,
) (ctrl.Result, error) {
	msg := fmt.Sprintf("ARM PUT failed with HTTP %d (terminal); fix the resource spec", statusCode)
	logging.FromContext(ctx).Warn(msg, zap.Int("statusCode", statusCode))
	setFailed(obj, msg)

	err := r.Status().Update(ctx, obj)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status after terminal ARM error: %w", err)
	}

	return ctrl.Result{}, fmt.Errorf("%w: HTTP %d from ARM PUT", ErrARMTerminal, statusCode)
}

// storeSpecHash computes the current spec hash and persists it as an annotation.
// Errors are logged but do not block the reconcile loop.
func (r *Reconciler) storeSpecHash(ctx context.Context, obj genruntime.ARMMetaObject) {
	specHash, err := computeSpecHash(obj)
	if err != nil {
		return
	}

	genruntime.AddAnnotation(obj, armSpecHashAnnotation, specHash)

	updateErr := r.Update(ctx, obj)
	if updateErr != nil && !apierrors.IsConflict(updateErr) {
		logging.FromContext(ctx).Warn("Failed to persist spec hash annotation", zap.Error(updateErr))
	}
}

func (r *Reconciler) reconcileSucceeded(
	ctx context.Context,
	obj genruntime.ARMMetaObject,
	armID string,
	body []byte,
) (ctrl.Result, error) {
	// Persist the ARM ID annotation and refresh the spec hash (a metadata update).
	// r.Update refreshes obj from the server; do this before the status update so
	// the in-memory status is not overwritten.
	genruntime.SetResourceID(obj, armID)

	specHash, hashErr := computeSpecHash(obj)
	if hashErr == nil {
		genruntime.AddAnnotation(obj, armSpecHashAnnotation, specHash)
	}

	err := r.Update(ctx, obj)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("setting resource-id annotation: %w", err)
	}

	err = r.populateStatus(obj, body)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("populating status: %w", err)
	}

	setSucceeded(obj)

	err = r.Status().Update(ctx, obj)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return ctrl.Result{RequeueAfter: driftRequeueInterval}, nil
}

// requeueReconciling sets the Ready=False/Reconciling condition and requeues.
func (r *Reconciler) requeueReconciling(
	ctx context.Context,
	obj genruntime.ARMMetaObject,
) (ctrl.Result, error) {
	setReconciling(obj)

	err := r.Status().Update(ctx, obj)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return ctrl.Result{RequeueAfter: reconcilingRequeueInterval}, nil
}

func (r *Reconciler) populateStatus(obj genruntime.ARMMetaObject, body []byte) error {
	armStatus, err := genruntime.NewEmptyARMStatus(obj, r.Scheme())
	if err != nil {
		return fmt.Errorf("constructing empty ARM status: %w", err)
	}

	if len(body) > 0 {
		err = json.Unmarshal(body, armStatus)
		if err != nil {
			return fmt.Errorf("unmarshaling ARM status: %w", err)
		}
	}

	status, err := genruntime.NewEmptyVersionedStatus(obj, r.Scheme())
	if err != nil {
		return fmt.Errorf("constructing empty versioned status: %w", err)
	}

	fromARM, ok := status.(genruntime.FromARMConverter)
	if !ok {
		return fmt.Errorf("%w: status %T does not implement FromARMConverter", ErrUnsupportedResourceType, status)
	}

	err = fromARM.PopulateFromARM(ownerReferenceFor(obj), valueOf(armStatus))
	if err != nil {
		return fmt.Errorf("populating status from ARM: %w", err)
	}

	err = obj.SetStatus(status)
	if err != nil {
		return fmt.Errorf("setting status: %w", err)
	}

	return nil
}

func (r *Reconciler) reconcileDelete(
	ctx context.Context,
	obj genruntime.ARMMetaObject,
	policy annotations.ReconcilePolicyValue,
) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(obj, finalizerName) {
		return ctrl.Result{}, nil
	}

	if !policy.AllowsDelete() {
		logger.Info("Reconcile policy does not allow deletion in Azure; removing finalizer",
			zap.String("policy", string(policy)))

		return r.removeFinalizer(ctx, obj)
	}

	creds, err := r.creds.resolve(ctx, obj)
	if err != nil {
		// Best-effort: do not wedge namespace teardown on missing credentials.
		logger.Warn("Azure credentials unavailable during delete; removing finalizer (Azure resource may be orphaned)",
			zap.Error(err))

		return r.removeFinalizer(ctx, obj)
	}

	armID, err := r.armIDFor(ctx, r.Client, obj, creds.subscriptionID)
	if err != nil {
		// The owner is already gone (or otherwise unresolvable), so the resource is
		// unreachable for a targeted delete. Release the finalizer rather than wedge
		// teardown — a parent delete cascades to children in Azure anyway.
		logger.Warn("Cannot resolve ARM ID during delete; removing finalizer", zap.Error(err))

		return r.removeFinalizer(ctx, obj)
	}

	return r.reconcileDeleteARM(ctx, obj, creds, armID)
}

// reconcileDeleteARM drives the actual ARM deletion of a managed resource. It GETs
// first so the authoritative "gone" signal is a 404 on read (an async DELETE only
// returns 202/200), which also clears the finalizer when the resource was already
// removed as a side effect of deleting a parent (e.g. a subnet gone with its VNet).
func (r *Reconciler) reconcileDeleteARM(
	ctx context.Context,
	obj genruntime.ARMMetaObject,
	creds *azureCredentials,
	armID string,
) (ctrl.Result, error) {
	logger := logging.FromContext(ctx)
	apiVersion := obj.GetAPIVersion()

	getResp, err := creds.armClient.get(ctx, armID, apiVersion)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("getting resource %s during delete: %w", armID, err)
	}

	if getResp.statusCode == http.StatusTooManyRequests {
		return ctrl.Result{RequeueAfter: getResp.retryAfter}, nil
	}

	if getResp.statusCode == http.StatusNotFound {
		logger.Info("Azure resource already gone; removing finalizer")

		return r.removeFinalizer(ctx, obj)
	}

	// The resource still exists. Bound how long we wait so teardown is never wedged
	// indefinitely on a resource Azure refuses to delete.
	expired, graceErr := r.deletionGraceExpired(ctx, obj)
	if graceErr != nil {
		return ctrl.Result{}, graceErr
	}

	if expired {
		logger.Warn("Azure resource still present after deletion grace period; "+
			"removing finalizer (resource may be orphaned)",
			zap.String("armID", armID),
			zap.Duration("gracePeriod", r.deletionGracePeriod))

		return r.removeFinalizer(ctx, obj)
	}

	delResp, err := creds.armClient.delete(ctx, armID, apiVersion)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("deleting resource %s: %w", armID, err)
	}

	if delResp.statusCode == http.StatusTooManyRequests {
		logger.Info("ARM rate limited on DELETE; requeueing",
			zap.Duration("retryAfter", delResp.retryAfter))

		return ctrl.Result{RequeueAfter: delResp.retryAfter}, nil
	}

	// Any in-flight/accepted status (202/200) — and a 409 where a dependent still
	// references the resource (e.g. a NAT gateway attached to a not-yet-deleted
	// subnet) — is transient: a sibling reconcile clears the dependency and the
	// next GET confirms removal. We never treat DELETE's status as terminal.
	logger.Info("Azure resource deletion issued; awaiting removal",
		zap.Int("statusCode", delResp.statusCode))

	setDeleting(obj)

	err = r.Status().Update(ctx, obj)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return ctrl.Result{RequeueAfter: reconcilingRequeueInterval}, nil
}

// deletionGraceExpired reports whether the resource has been pending Azure deletion
// for longer than the configured grace period. The first time it is observed
// pending, it stamps a timestamp annotation; thereafter it compares against it. A
// non-positive grace period disables the safety net.
func (r *Reconciler) deletionGraceExpired(ctx context.Context, obj genruntime.ARMMetaObject) (bool, error) {
	if r.deletionGracePeriod <= 0 {
		return false, nil
	}

	started := obj.GetAnnotations()[deletionStartedAnnotation]
	if started == "" {
		genruntime.AddAnnotation(obj, deletionStartedAnnotation, time.Now().UTC().Format(time.RFC3339))

		err := r.Update(ctx, obj)
		if err != nil {
			return false, fmt.Errorf("stamping deletion-started annotation: %w", err)
		}

		return false, nil
	}

	startedAt, err := time.Parse(time.RFC3339, started)
	if err != nil {
		// Unparseable timestamp: treat as not expired so we keep trying to delete.
		//nolint:nilerr // a malformed annotation is non-fatal; do not abort the delete.
		return false, nil
	}

	return time.Since(startedAt) > r.deletionGracePeriod, nil
}

func (r *Reconciler) removeFinalizer(
	ctx context.Context,
	obj genruntime.ARMMetaObject,
) (ctrl.Result, error) {
	if controllerutil.RemoveFinalizer(obj, finalizerName) {
		err := r.Update(ctx, obj)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

// reconcilePolicyFor returns the resource's reconcile policy, defaulting to
// "manage" when the annotation is absent (matching ASO's default behaviour).
func reconcilePolicyFor(obj genruntime.ARMMetaObject) annotations.ReconcilePolicyValue {
	value := annotations.ReconcilePolicyValue(obj.GetAnnotations()[annotations.ReconcilePolicy])
	if value == "" {
		return annotations.ReconcilePolicyManage
	}

	return value
}

func ownerReferenceFor(obj genruntime.ARMMetaObject) genruntime.ArbitraryOwnerReference {
	owner := obj.Owner()
	if owner == nil {
		return genruntime.ArbitraryOwnerReference{}
	}

	return genruntime.ArbitraryOwnerReference{
		Name:  owner.Name,
		Group: owner.Group,
		Kind:  owner.Kind,
	}
}

// parseProvisioningState extracts properties.provisioningState from an ARM
// response body. All resource kinds the reconciler owns surface it there.
func parseProvisioningState(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	var parsed struct {
		Properties struct {
			ProvisioningState string `json:"provisioningState"`
		} `json:"properties"`
	}

	err := json.Unmarshal(body, &parsed)
	if err != nil {
		return ""
	}

	return parsed.Properties.ProvisioningState
}

// computeSpecHash computes a truncated SHA-256 fingerprint of the current desired
// ARM spec body. It is used to detect whether a spec change has been pushed to
// Kubernetes but not yet propagated to Azure.
func computeSpecHash(obj genruntime.ARMMetaObject) (string, error) {
	details, err := buildResolvedDetails(obj)
	if err != nil {
		return "", fmt.Errorf("building resolved details for hash: %w", err)
	}

	spec := obj.GetSpec()

	converter, ok := spec.(genruntime.ToARMConverter)
	if !ok {
		return "", fmt.Errorf("%w: %T has no ConvertToARM", ErrUnsupportedResourceType, spec)
	}

	armBody, err := converter.ConvertToARM(details)
	if err != nil {
		return "", fmt.Errorf("converting spec for hash: %w", err)
	}

	bodyBytes, err := json.Marshal(armBody)
	if err != nil {
		return "", fmt.Errorf("marshaling spec for hash: %w", err)
	}

	sum := sha256.Sum256(bodyBytes)

	// First 8 bytes (16 hex chars) give a 64-bit fingerprint — sufficient for
	// drift detection while keeping the annotation compact.
	return hex.EncodeToString(sum[:8]), nil
}

// valueOf dereferences a pointer value so PopulateFromARM (which expects a value,
// not a pointer) can consume it.
func valueOf(value any) any {
	reflected := reflect.ValueOf(value)
	if reflected.Kind() == reflect.Pointer {
		return reflected.Elem().Interface()
	}

	return value
}
