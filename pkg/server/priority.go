package server

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	v1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

const (
	groupAuthentication        = "authentication.k8s.io"
	groupCertificates          = "certificates.k8s.io"
	groupAdmissionRegistration = "admissionregistration.k8s.io"
	groupCoordination          = "coordination.k8s.io"
	groupFlowControl           = "flowcontrol.apiserver.k8s.io"
	versionV1beta1             = "v1beta1"
	versionV1alpha1            = "v1alpha1"
)

type apiServicePriority struct {
	// Group indicates the order of the group relative to other groups.
	Group int32
	// Version indicates the relative order of the Version inside of its group.
	Version int32
}

//nolint:mnd // Copied from k8s.io/kube-aggregator/pkg/apiserver/priority.go
func defaultGenericAPIServicePriorities() map[schema.GroupVersion]apiServicePriority {
	return map[schema.GroupVersion]apiServicePriority{
		{Group: "", Version: "v1"}: {Group: 18000, Version: 1},
		// to my knowledge, nothing below here collides
		{Group: "events.k8s.io", Version: "v1"}:                        {Group: 17750, Version: 15},
		{Group: "events.k8s.io", Version: versionV1beta1}:              {Group: 17750, Version: 5},
		{Group: groupAuthentication, Version: "v1"}:                    {Group: 17700, Version: 15},
		{Group: groupAuthentication, Version: versionV1beta1}:          {Group: 17700, Version: 9},
		{Group: groupAuthentication, Version: versionV1alpha1}:         {Group: 17700, Version: 1},
		{Group: "authorization.k8s.io", Version: "v1"}:                 {Group: 17600, Version: 15},
		{Group: groupCertificates, Version: "v1"}:                      {Group: 17300, Version: 15},
		{Group: groupCertificates, Version: versionV1beta1}:            {Group: 17300, Version: 9},
		{Group: groupCertificates, Version: versionV1alpha1}:           {Group: 17300, Version: 1},
		{Group: "rbac.authorization.k8s.io", Version: "v1"}:            {Group: 17000, Version: 15},
		{Group: "apiextensions.k8s.io", Version: "v1"}:                 {Group: 16700, Version: 15},
		{Group: groupAdmissionRegistration, Version: "v1"}:             {Group: 16700, Version: 15},
		{Group: groupAdmissionRegistration, Version: versionV1beta1}:   {Group: 16700, Version: 12},
		{Group: groupAdmissionRegistration, Version: versionV1alpha1}:  {Group: 16700, Version: 9},
		{Group: groupCoordination, Version: "v1"}:                      {Group: 16500, Version: 15},
		{Group: groupCoordination, Version: versionV1beta1}:            {Group: 16500, Version: 13},
		{Group: groupCoordination, Version: "v1alpha2"}:                {Group: 16500, Version: 12},
		{Group: "discovery.k8s.io", Version: "v1"}:                     {Group: 16200, Version: 15},
		{Group: "discovery.k8s.io", Version: versionV1beta1}:           {Group: 16200, Version: 12},
		{Group: groupFlowControl, Version: "v1"}:                       {Group: 16100, Version: 21},
		{Group: groupFlowControl, Version: "v1beta3"}:                  {Group: 16100, Version: 18},
		{Group: groupFlowControl, Version: "v1beta2"}:                  {Group: 16100, Version: 15},
		{Group: groupFlowControl, Version: versionV1beta1}:             {Group: 16100, Version: 12},
		{Group: groupFlowControl, Version: versionV1alpha1}:            {Group: 16100, Version: 9},
		{Group: "internal.apiserver.k8s.io", Version: versionV1alpha1}: {Group: 16000, Version: 9},
		{Group: "resource.k8s.io", Version: "v1alpha3"}:                {Group: 15900, Version: 9},
		{Group: "storagemigration.k8s.io", Version: "v1alpha1"}:        {Group: 15800, Version: 9},
		{Group: "storage.k8s.io", Version: "v1"}:                       {Group: 15700, Version: 9},
		// Append a new group to the end of the list if unsure.
		// You can use min(existing group)-100 as the initial value for a group.
		// Version can be set to 9 (to have space around) for a new group.
	}
}

func makeAPIService(groupVersion schema.GroupVersion,
	priorities map[schema.GroupVersion]apiServicePriority) *v1.APIService {
	apiServicePriority, ok := priorities[groupVersion]
	if !ok {
		// if we aren't found, then we shouldn't register ourselves because it could result in a CRD group version
		// being permanently stuck in the APIServices list.
		return nil
	}

	return &v1.APIService{
		Name: groupVersion.Version + "." + groupVersion.Group,
		Spec: v1.APIServiceSpec{
			Group:                groupVersion.Group,
			Version:              groupVersion.Version,
			GroupPriorityMinimum: apiServicePriority.Group,
			VersionPriority:      apiServicePriority.Version,
		},
	}
}
