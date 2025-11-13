package rest

import (
	"fmt"
	"strings"

	"github.com/kommodity-io/kommodity/pkg/config"
	clientgoclientset "k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	configMapReportPrefix = "attestation-report"
)

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

// GetSecretAPI returns the Secret API interface for the Kommodity namespace.
func GetSecretAPI(kubeClient *clientgoclientset.Clientset) v1.SecretInterface {
	return kubeClient.CoreV1().Secrets(config.KommodityNamespace)
}
