package helpers

import "errors"

var (
	errKindClusterCreation     = errors.New("failed to create kind cluster")
	errKubeVirtInstall         = errors.New("failed to install KubeVirt")
	errCDIInstall              = errors.New("failed to install CDI")
	errKubeVirtNotReady        = errors.New("KubeVirt not ready within timeout")
	errCDINotReady             = errors.New("CDI not ready within timeout")
	errManifestFetch           = errors.New("unexpected HTTP status fetching manifest")
	errMoreServersThanExpected = errors.New("found more servers than expected in Scaleway")
	errUnexpectedState         = errors.New("unexpected state")
	errInvalidRegion           = errors.New("invalid region provided")
	errInvalidZone             = errors.New("invalid zone provided")
	errRepoRootNotFound        = errors.New("repo root not found")
	errContainerNil            = errors.New("container is nil")
	errControlPlaneSvcNotFound = errors.New("control plane Service not found in infra cluster")
	errControlPlanePodNotReady = errors.New("no ready pod backing control plane Service")
	errKubeconfigInvalid       = errors.New("kubeconfig secret content is invalid")
	errPortForwardNotReady     = errors.New("timed out waiting for port-forward to pod")
	errPortForwardNoPorts      = errors.New("port-forward returned no ports")
)
