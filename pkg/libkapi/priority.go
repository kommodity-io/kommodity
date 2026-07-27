package libkapi

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	v1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

type apiServicePriority struct {
	// Group indicates the order of the group relative to other groups.
	Group int32
	// Version indicates the relative order of the Version inside of its group.
	Version int32
}

// defaultGenericAPIServicePriorities carries priorities only for the group-
// versions libkapi actually installs (see standardAPIGroups and
// setupAPIExtensionConfig) - unlike upstream k8s.io/kube-aggregator's own
// table, which also covers built-in groups this library doesn't wire
// (events.k8s.io, flowcontrol.apiserver.k8s.io, ...). makeAPIService
// silently skips registering any group-version missing here, so add an
// entry whenever installStandardAPIGroups grows a new group.
//
//nolint:mnd // Priority values copied from k8s.io/kube-aggregator/pkg/apiserver/priority.go
func defaultGenericAPIServicePriorities() map[schema.GroupVersion]apiServicePriority {
	return map[schema.GroupVersion]apiServicePriority{
		{Group: "", Version: "v1"}:                          {Group: 18000, Version: 1},
		{Group: "apps", Version: "v1"}:                      {Group: 17500, Version: 15},
		{Group: "batch", Version: "v1"}:                     {Group: 17400, Version: 15},
		{Group: "rbac.authorization.k8s.io", Version: "v1"}: {Group: 17000, Version: 15},
		{Group: "networking.k8s.io", Version: "v1"}:         {Group: 17200, Version: 15},
		{Group: "apiextensions.k8s.io", Version: "v1"}:      {Group: 16700, Version: 15},
		{Group: "coordination.k8s.io", Version: "v1"}:       {Group: 16500, Version: 15},
		{Group: "storage.k8s.io", Version: "v1"}:            {Group: 15700, Version: 9},
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
		ObjectMeta: metav1.ObjectMeta{Name: groupVersion.Version + "." + groupVersion.Group},
		Spec: v1.APIServiceSpec{
			Group:                groupVersion.Group,
			Version:              groupVersion.Version,
			GroupPriorityMinimum: apiServicePriority.Group,
			VersionPriority:      apiServicePriority.Version,
		},
	}
}
