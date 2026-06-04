package secrets

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/apis/core"
)

// applySecretConversions normalizes a v1 Secret using the conversion hooks
// mounted on the scheme. Upstream Kubernetes registers these in
// pkg/apis/core/v1/zz_generated.conversion.go via
func applySecretConversions(scheme *runtime.Scheme, secret *corev1.Secret) error {
	internal := &core.Secret{}

	err := scheme.Convert(secret, internal, nil)
	if err != nil {
		return fmt.Errorf("failed to convert v1 Secret via scheme: %w", err)
	}

	secret.Data = internal.Data
	secret.StringData = nil

	return nil
}
