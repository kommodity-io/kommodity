package rest

import (
	"context"
	"fmt"
	"strings"

	"github.com/kommodity-io/kommodity/pkg/config"
	"k8s.io/apimachinery/pkg/labels"
	clientgoclientset "k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrlclint "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	configMapReportPrefix = "attestation-report"
)

// FindManagedMachineByIP finds a managed-by-kommodity Machine by its IP address.
//
//nolint:varnamelen // Variable name ip is appropriate for the context.
func FindManagedMachineByIP(ctx context.Context, ctrlClient *ctrlclint.Client, ip string) (*clusterv1.Machine, error) {
	var machines clusterv1.MachineList

	err := (*ctrlClient).List(ctx, &machines, &ctrlclint.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{
			config.ManagedByLabel: "kommodity",
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list managed-by kommodity machines: %w", err)
	}

	for _, machine := range machines.Items {
		addresses := machine.Status.Addresses
		if len(addresses) == 0 {
			continue
		}

		for _, addr := range addresses {
			if addr.Address == ip {
				return &machine, nil
			}
		}
	}

	return nil, ErrNoMachineFound
}

// GetConfigMapReportName generates the ConfigMap name for storing the attestation report of a given machine.
func GetConfigMapReportName(machine *clusterv1.Machine) string {
	deploymentName := machine.Labels[config.DeploymentNameLabel]
	machineSuffix := strings.TrimPrefix(machine.Name, deploymentName+"-")

	return fmt.Sprintf("%s-%s", configMapReportPrefix, machineSuffix)
}

// GetConfigMapAPI returns the ConfigMap API interface for the Kommodity namespace.
func GetConfigMapAPI(kubeClient *clientgoclientset.Clientset) v1.ConfigMapInterface {
	return kubeClient.CoreV1().ConfigMaps(config.KommodityNamespace)
}
