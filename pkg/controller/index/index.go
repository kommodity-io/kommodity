// Package index contains index functions for indexing Kubernetes objects.
package index

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/cluster-api/controllers/clustercache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// NodeNameField is used to index Pods by their assigned node name.
	NodeNameField = "spec.nodeName"
)

// NodeNameIndex is used to index Pods by their assigned node name.
//
//nolint:gochecknoglobals // Index definitions are intended to be global.
var NodeNameIndex = clustercache.CacheOptionsIndex{
	Object:       &corev1.Pod{},
	Field:        NodeNameField,
	ExtractValue: NodeByName,
}

// NodeByName contains the logic to index Nodes by Name.
func NodeByName(o client.Object) []string {
	pod, ok := o.(*corev1.Pod)
	if !ok {
		panic(fmt.Sprintf("Expected a Node but got a %T", o))
	}

	if pod.Spec.NodeName == "" {
		return nil
	}

	return []string{pod.Spec.NodeName}
}
