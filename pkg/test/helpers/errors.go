package helpers

import "errors"

var (
	errKindClusterCreation = errors.New("failed to create kind cluster")
	errKubeVirtInstall     = errors.New("failed to install KubeVirt")
	errCDIInstall          = errors.New("failed to install CDI")
	errKubeVirtNotReady    = errors.New("KubeVirt not ready within timeout")
	errCDINotReady         = errors.New("CDI not ready within timeout")
	errManifestFetch       = errors.New("unexpected HTTP status fetching manifest")
)
