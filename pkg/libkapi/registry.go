package libkapi

import (
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	genericregistry "k8s.io/apiserver/pkg/registry/generic"
	genericapiserver "k8s.io/apiserver/pkg/server"
	apiserverstorage "k8s.io/apiserver/pkg/server/storage"

	"k8s.io/apiserver/pkg/authorization/authorizer"
	appsrest "k8s.io/kubernetes/pkg/registry/apps/rest"
	batchrest "k8s.io/kubernetes/pkg/registry/batch/rest"
	corerest "k8s.io/kubernetes/pkg/registry/core/rest"
	networkingrest "k8s.io/kubernetes/pkg/registry/networking/rest"
	rbacrest "k8s.io/kubernetes/pkg/registry/rbac/rest"
	storagerest "k8s.io/kubernetes/pkg/registry/storage/rest"
)

// defaultEventTTL matches upstream kube-apiserver's default --event-ttl.
const defaultEventTTL = 1 * time.Hour

// restStorageProvider is the uniform shape every upstream
// k8s.io/kubernetes/pkg/registry/<group>/rest aggregator satisfies: given a
// resource-enablement source and a RESTOptionsGetter, it builds the REST
// storage for its entire API group.
type restStorageProvider interface {
	NewRESTStorage(resourceConfig apiserverstorage.APIResourceConfigSource,
		restOptionsGetter genericregistry.RESTOptionsGetter) (genericapiserver.APIGroupInfo, error)
}

// standardAPIGroup pairs an upstream restStorageProvider with the group name
// used to pick its storage codec version and, for core v1, install path.
type standardAPIGroup struct {
	displayName string
	groupName   string
	provider    restStorageProvider
	legacy      bool
	// version is filled in by resolveStandardGroupVersions.
	version schema.GroupVersion
}

// standardAPIGroups returns the standard Kubernetes API groups
// installStandardAPIGroups wires storage for: core v1, apps/v1, batch/v1,
// rbac.authorization.k8s.io/v1, networking.k8s.io, storage.k8s.io. Each
// provider comes from an upstream k8s.io/kubernetes/pkg/registry/<group>/rest
// aggregator instead of hand-written REST storage (see the PRD's "Reusing
// upstream registry storage" section). That upstream code is built against
// the k8s.io/kubernetes/pkg/api/legacyscheme global (populated in
// scheme.go), not a scheme private to this Server - see the PRD's
// "legacyscheme global singleton" risk note.
//
// authz is threaded in from the same instance setupAPIServerConfig assigns to
// genericServerConfig.Authorization.Authorizer, so the rbac.authorization.k8s.io
// group's bootstrap-roles behavior agrees with the server's actual authorizer
// instead of independently constructing its own default.
func standardAPIGroups(authz authorizer.Authorizer) []standardAPIGroup {
	return []standardAPIGroup{
		{
			displayName: "core v1", groupName: corev1.GroupName,
			provider: &corerest.GenericConfig{EventTTL: defaultEventTTL}, legacy: true,
		},
		{displayName: "apps/v1", groupName: appsv1.GroupName, provider: appsrest.StorageProvider{}},
		{displayName: "batch/v1", groupName: batchv1.GroupName, provider: batchrest.RESTStorageProvider{}},
		{
			displayName: "rbac.authorization.k8s.io", groupName: rbacv1.GroupName,
			provider: rbacrest.RESTStorageProvider{Authorizer: authz},
		},
		{displayName: "networking.k8s.io", groupName: networkingv1.GroupName, provider: networkingrest.RESTStorageProvider{}},
		{displayName: "storage.k8s.io", groupName: storagev1.GroupName, provider: storagerest.RESTStorageProvider{}},
	}
}

// resolveStandardGroupVersions fills in each group's single GA (highest-
// priority) version from scheme, so callers resolve it exactly once instead
// of every consumer re-querying the scheme per group.
//
// Only the GA version is enabled, deliberately - some of these groups (e.g.
// networking.k8s.io, storage.k8s.io) also have resources that only exist at
// a beta/alpha version (IPAddress, VolumeAttributesClass); a codec built to
// cover multiple versions of the same group runs into a genuine ambiguity in
// k8s.io/apimachinery's GroupVersioner (a beta-only kind resolves against
// whichever version is first in the list, not the version it actually
// exists at) that isn't worth working around for a first cut. Sticking to
// one version per group keeps every codec unambiguous.
func resolveStandardGroupVersions(scheme *runtime.Scheme, groups []standardAPIGroup) error {
	for groupIndex := range groups {
		versions := scheme.PrioritizedVersionsForGroup(groups[groupIndex].groupName)
		if len(versions) == 0 {
			return fmt.Errorf("%w: %q", ErrGroupVersionNotRegistered, groups[groupIndex].groupName)
		}

		groups[groupIndex].version = versions[0]
	}

	return nil
}

// groupVersions extracts each group's resolved version, in order.
func groupVersions(groups []standardAPIGroup) []schema.GroupVersion {
	versions := make([]schema.GroupVersion, len(groups))
	for i, group := range groups {
		versions[i] = group.version
	}

	return versions
}

// installStandardAPIGroups wires REST storage for every entry in groups onto
// genericServer. Each group's storage codec is pinned to its resolved GA
// version (see resolveStandardGroupVersions) - not every version the scheme
// knows about - to keep encoding unambiguous.
func installStandardAPIGroups(
	genericServer *genericapiserver.GenericAPIServer,
	groups []standardAPIGroup,
	codecs serializer.CodecFactory,
	resourceConfig apiserverstorage.APIResourceConfigSource,
	storageEndpoints []string,
) error {
	noConv := serializer.WithoutConversionCodecFactory{CodecFactory: codecs}

	for _, group := range groups {
		rog := newRESTOptionsGetter(noConv.LegacyCodec(group.version), storageEndpoints)

		info, err := group.provider.NewRESTStorage(resourceConfig, rog)
		if err != nil {
			return fmt.Errorf("failed to build %s REST storage: %w", group.displayName, err)
		}

		if group.legacy {
			err = genericServer.InstallLegacyAPIGroup("/api", &info)
		} else {
			err = genericServer.InstallAPIGroup(&info)
		}

		if err != nil {
			return fmt.Errorf("failed to install %s API group: %w", group.displayName, err)
		}
	}

	return nil
}
