package libkapi

import (
	"fmt"
	"sync"

	apiextensionsinternal "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/apis/audit"
	apiregistration "k8s.io/kube-aggregator/pkg/apis/apiregistration"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	// legacyscheme.Scheme is a package-level singleton that the blank imports
	// below populate with the standard Kubernetes API groups as a side effect;
	// registry.go wires REST storage for these groups using upstream aggregator
	// functions built against this exact global scheme, so newScheme extends it
	// in place rather than maintaining a separate copy. See the PRD's
	// "legacyscheme global singleton" risk note.
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	// Registers apps/v1 types (Deployment, StatefulSet, ...) into legacyscheme.Scheme.
	_ "k8s.io/kubernetes/pkg/apis/apps/install"
	// Registers batch/v1 types (Job, CronJob) into legacyscheme.Scheme.
	_ "k8s.io/kubernetes/pkg/apis/batch/install"
	// Registers core/v1 types (Namespace, Secret, ConfigMap, ...) into legacyscheme.Scheme.
	_ "k8s.io/kubernetes/pkg/apis/core/install"
	// Registers networking.k8s.io types (NetworkPolicy, Ingress, ...) into legacyscheme.Scheme.
	_ "k8s.io/kubernetes/pkg/apis/networking/install"
	// Registers rbac.authorization.k8s.io/v1 types (Role, RoleBinding, ...) into legacyscheme.Scheme.
	_ "k8s.io/kubernetes/pkg/apis/rbac/install"
	// Registers storage.k8s.io types (StorageClass, VolumeAttachment, ...) into legacyscheme.Scheme.
	_ "k8s.io/kubernetes/pkg/apis/storage/install"
)

// schemeMu serializes mutation of the legacyscheme.Scheme singleton.
// runtime.Scheme's AddKnownTypeWithName isn't safe for concurrent callers
// (it writes directly into the scheme's internal maps), and
// legacyscheme.Scheme itself is shared across every newScheme call (see the
// import comment below), so the lock must be package-level too. server.go's
// newMu already serializes newScheme's only production caller (New), but
// spike_test.go also calls newScheme directly, bypassing that lock - keeping
// this one here makes newScheme safe on its own regardless of caller.
//nolint:gochecknoglobals // guards legacyscheme.Scheme, itself a package-level singleton.
var schemeMu sync.Mutex

// newScheme returns the shared runtime.Scheme and CodecFactory used across the
// entire delegation chain: the standard-API delegate, the CRD server, and the
// aggregator.
//
// It extends legacyscheme.Scheme (already populated with core/apps/batch/rbac/
// networking/storage by the blank imports above) with the apiextensions and
// apiregistration types the CRD and aggregation layers need, plus any extra
// types the caller supplied via Config.Scheme.
func newScheme(extra *runtime.Scheme) (*runtime.Scheme, serializer.CodecFactory, error) {
	schemeMu.Lock()
	defer schemeMu.Unlock()

	scheme := legacyscheme.Scheme

	addFuncs := []struct {
		name string
		fn   func(*runtime.Scheme) error
	}{
		{"apiextensionsinternal.AddToScheme", apiextensionsinternal.AddToScheme},
		{"apiextensionsv1.AddToScheme", apiextensionsv1.AddToScheme},
		{"apiregistration.AddToScheme", apiregistration.AddToScheme},
		{"apiregistrationv1.AddToScheme", apiregistrationv1.AddToScheme},
		{"metav1.AddMetaToScheme", metav1.AddMetaToScheme},
		{"audit.AddToScheme", audit.AddToScheme},
	}

	for _, add := range addFuncs {
		err := add.fn(scheme)
		if err != nil {
			return nil, serializer.CodecFactory{}, fmt.Errorf("failed to add %s to scheme: %w", add.name, err)
		}
	}

	err := addExtraTypes(scheme, extra)
	if err != nil {
		return nil, serializer.CodecFactory{}, err
	}

	codecs := serializer.NewCodecFactory(scheme)

	return scheme, codecs, nil
}

// addExtraTypes registers every known type of extra onto scheme.
func addExtraTypes(scheme *runtime.Scheme, extra *runtime.Scheme) error {
	if extra == nil {
		return nil
	}

	for gvk := range extra.AllKnownTypes() {
		obj, err := extra.New(gvk)
		if err != nil {
			return fmt.Errorf("failed to instantiate caller-supplied type %s: %w", gvk, err)
		}

		scheme.AddKnownTypeWithName(gvk, obj)
	}

	return nil
}
