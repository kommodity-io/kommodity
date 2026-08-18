package helpers

import "errors"

var (
	errKindClusterCreation     = errors.New("failed to create kind cluster")
	errKubeVirtInstall         = errors.New("failed to install KubeVirt")
	errCDIInstall              = errors.New("failed to install CDI")
	errKubeVirtNotReady        = errors.New("KubeVirt not ready within timeout")
	errCDINotReady             = errors.New("CDI not ready within timeout")
	errManifestFetch           = errors.New("unexpected HTTP status fetching manifest")
	errMoreServersThanExpected = errors.New("found more servers than expected")
	errHcloudTokenNotSet       = errors.New("HCLOUD_TOKEN environment variable is not set")
	errUnexpectedState         = errors.New("unexpected state")
	errInvalidRegion           = errors.New("invalid region provided")
	errInvalidZone             = errors.New("invalid zone provided")
	errRepoRootNotFound        = errors.New("repo root not found")
	errContainerNil            = errors.New("container is nil")
)
