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
//
//	s.AddConversionFunc((*corev1.Secret)(nil), (*core.Secret)(nil), …)
//
// and they are wired into our scheme by coreapiv1.RegisterConversions in
// pkg/server/utils.go. scheme.Convert(v1Secret, coreSecret, nil) dispatches to
// Convert_v1_Secret_To_core_Secret (which merges StringData into Data) plus
// any other hooks registered for this type pair.
//
// As a hard security invariant, this function ALWAYS merges StringData into
// Data and clears StringData before returning — even if the scheme-based
// conversion is unavailable or fails — so plaintext StringData can never reach
// storage or be returned to clients. The scheme error is still surfaced for
// visibility; the caller may log it without risking a leak.
//
// To add a new normalization, register an additional conversion on the scheme.
func applySecretConversions(scheme *runtime.Scheme, secret *corev1.Secret) error {
	internal := &core.Secret{}

	convertErr := scheme.Convert(secret, internal, nil)
	if convertErr == nil {
		secret.Data = internal.Data
	}

	// Defensive merge: mirrors the upstream rule that StringData overwrites
	// Data per key. Runs unconditionally so we never persist plaintext
	// StringData even if scheme.Convert returned an error above.
	if len(secret.StringData) > 0 {
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}

		for key, value := range secret.StringData {
			secret.Data[key] = []byte(value)
		}
	}

	secret.StringData = nil

	if convertErr != nil {
		return fmt.Errorf("failed to convert v1 Secret via scheme: %w", convertErr)
	}

	return nil
}
