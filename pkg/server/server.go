// Package server implements the Kommodity server,
// including the supported API resources.
package server

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/apis/core/install"
	"github.com/kommodity-io/kommodity/pkg/apis/core/v1alpha1"
	"github.com/kommodity-io/kommodity/pkg/genericserver"
	"github.com/kommodity-io/kommodity/pkg/kms"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
)

// ComponentName is the name of the Kommodity server component.
const ComponentName = "kommodity"

// New create a new kommodity server instance.
func New(ctx context.Context) (*genericserver.GenericServer, error) {
	srv := genericserver.New(ctx,
		genericserver.WithGRPCServerFactory(kms.NewGRPCServerFactory()),
	)

	// Defines methods for serializing and deserializing API objects.
	scheme := newScheme()

	// Provides methods for retrieving codes and serializers for specific versions and content types.
	codecs := serializer.NewCodecFactory(scheme)

	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(v1alpha1.GroupName, scheme, metav1.ParameterCodec, codecs)

	v1alpha1Storage := map[string]rest.Storage{}
	//nolint:godox // This PR is already too big, we will implement the storage in another PR.
	// TODO: Add storage for v1alpha1 resources.
	apiGroupInfo.VersionedResourcesStorageMap["v1alpha1"] = v1alpha1Storage

	if err := srv.InstallAPIGroup(&apiGroupInfo); err != nil {
		return nil, fmt.Errorf("failed to install API group: %w", err)
	}

	return srv, nil
}

// newScheme creates a new runtime scheme with the Kommodity API group installed.
func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	// Install the Kommodity API group into the scheme.
	install.Install(scheme)

	//nolint:godox // This is not something we can easily fix today.
	// TODO: Remove this once this is no longer needed.
	//       Reference: https://github.com/kubernetes/sample-apiserver/blob/master/pkg/apiserver/apiserver.go
	metav1.AddToGroupVersion(scheme, schema.GroupVersion{Version: "v1"})

	//nolint:godox // This is not something we can easily fix today.
	// TODO: Remove this once this is no longer needed.
	//       Reference: https://github.com/kubernetes/sample-apiserver/blob/master/pkg/apiserver/apiserver.go
	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.APIVersions{},
		&metav1.APIGroup{},
		&metav1.APIGroupList{},
		&metav1.APIResourceList{},
	)

	return scheme
}
